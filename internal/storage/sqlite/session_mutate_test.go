package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"elbot/internal/storage"
)

func newMutateTestSession(t *testing.T) (*Store, *storage.Session) {
	t.Helper()
	store := newTestStore(t)
	session := &storage.Session{
		OwnerID:         "u1",
		Platform:        "cli",
		PlatformScopeID: "local",
		Title:           "mutate",
		Metadata:        `{"workspace_dir":"D:\\work"}`,
	}
	if err := store.Sessions().Create(context.Background(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store, session
}

func decodeMutateToolNames(metadata string) ([]string, error) {
	if metadata == "" {
		return nil, nil
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadata), &decoded); err != nil {
		return nil, err
	}
	raw, ok := decoded["tool_cache"]
	if !ok {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, err
	}
	return names, nil
}

func TestSessionMutateReadsLatestRowInsideTransaction(t *testing.T) {
	store, session := newMutateTestSession(t)
	ctx := context.Background()

	// A snapshot taken before the concurrent write below, exactly like the
	// session object tool discovery keeps around between reads.
	stale, err := store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}

	if _, err := store.Sessions().Mutate(ctx, session.ID, func(current *storage.Session) error {
		current.Title = "renamed by concurrent writer"
		current.UpdatedAt = storage.Now()
		return nil
	}); err != nil {
		t.Fatalf("concurrent mutate: %v", err)
	}

	var seen *storage.Session
	updated, err := store.Sessions().Mutate(ctx, session.ID, func(current *storage.Session) error {
		seen = current
		current.Metadata = `{"workspace_dir":"D:\\work","tool_cache":["web_search"]}`
		current.UpdatedAt = storage.Now()
		return nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if seen == nil || seen.Title != "renamed by concurrent writer" {
		t.Fatalf("callback saw stale snapshot: %#v", seen)
	}
	if updated.Title != "renamed by concurrent writer" {
		t.Fatalf("mutate returned stale row: %#v", updated)
	}
	if updated.Metadata != `{"workspace_dir":"D:\\work","tool_cache":["web_search"]}` {
		t.Fatalf("mutate returned metadata %q", updated.Metadata)
	}

	persisted, err := store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get persisted: %v", err)
	}
	if persisted.Title != "renamed by concurrent writer" {
		t.Fatalf("persisted title = %q", persisted.Title)
	}
	names, err := decodeMutateToolNames(persisted.Metadata)
	if err != nil {
		t.Fatalf("decode persisted metadata %q: %v", persisted.Metadata, err)
	}
	if len(names) != 1 || names[0] != "web_search" {
		t.Fatalf("persisted metadata = %q", persisted.Metadata)
	}

	// The stale snapshot is a different object and must stay untouched.
	if stale.Title == "renamed by concurrent writer" {
		t.Fatal("Mutate must not rewrite the caller's snapshot")
	}
}

func TestSessionMutatePropagatesCallbackErrorWithoutWriting(t *testing.T) {
	store, session := newMutateTestSession(t)
	ctx := context.Background()
	sentinel := errors.New("reject mutation")

	_, err := store.Sessions().Mutate(ctx, session.ID, func(current *storage.Session) error {
		current.Title = "must not be written"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Mutate error = %v", err)
	}

	persisted, err := store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get persisted: %v", err)
	}
	if persisted.Title != "mutate" {
		t.Fatalf("failed mutation was persisted: %q", persisted.Title)
	}
}

func TestSessionMutateMissingSessionReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.Sessions().Mutate(ctx, "missing", func(*storage.Session) error { return nil })
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Mutate missing error = %v", err)
	}

	if _, err := store.Sessions().Mutate(ctx, "missing", nil); err == nil {
		t.Fatal("Mutate with nil callback should fail")
	}
}

// TestSessionMutateSerializesConcurrentMetadataWriters is the regression test for
// the discovery/preload lost-update bug: read-modify-write through Get + Update
// from stale snapshots loses concurrent writers, while Mutate applies every one
// of them. Concurrent writers are the common case, since one turn records the
// tool cache, the workspace and the usage of the same session row.
func TestSessionMutateSerializesConcurrentMetadataWriters(t *testing.T) {
	store, session := newMutateTestSession(t)
	const writers = 8

	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("tool_%02d", i)
			_, err := store.Sessions().Mutate(context.Background(), session.ID, func(current *storage.Session) error {
				names, decodeErr := decodeMutateToolNames(current.Metadata)
				if decodeErr != nil {
					return decodeErr
				}
				names = append(names, name)
				sort.Strings(names)
				encoded, marshalErr := json.Marshal(map[string]any{"workspace_dir": `D:\work`, "tool_cache": names})
				if marshalErr != nil {
					return marshalErr
				}
				current.Metadata = string(encoded)
				current.UpdatedAt = storage.Now()
				return nil
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	persisted, err := store.Sessions().Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get persisted: %v", err)
	}
	names, err := decodeMutateToolNames(persisted.Metadata)
	if err != nil {
		t.Fatalf("decode persisted metadata %q: %v", persisted.Metadata, err)
	}
	if len(names) != writers {
		t.Fatalf("lost updates: got %d of %d names: %v", len(names), writers, names)
	}
	for i := 0; i < writers; i++ {
		want := fmt.Sprintf("tool_%02d", i)
		if names[i] != want {
			t.Fatalf("names[%d] = %q, want %q (%v)", i, names[i], want, names)
		}
	}
}
