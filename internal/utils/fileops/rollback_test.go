package fileops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRollbackStoreRestoresLastEdit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	before := []byte("alpha\n")
	after := []byte("beta\n")
	if err := os.WriteFile(path, after, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewRollbackStore()
	info, available := store.Record("s1", path, before, 0o600, true, ContentRevision(before), ContentRevision(after))
	if !available || info.ID != 1 || info.Created || info.Path != path || info.Bytes != int64(len(before)) {
		t.Fatalf("record = %#v available=%v", info, available)
	}
	if listed := store.List("s1"); len(listed) != 1 || listed[0].ID != 1 {
		t.Fatalf("list = %#v", listed)
	}
	if got, ok := store.Get("s1", 1); !ok || got.Path != path {
		t.Fatalf("get = %#v ok=%v", got, ok)
	}

	// An external writer wins: rollback must not discard its change.
	if err := os.WriteFile(path, []byte("external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore(ctx, "s1", 1); !errors.Is(err, ErrRollbackConflict) {
		t.Fatalf("restore error = %v, want ErrRollbackConflict", err)
	}
	if content, _ := os.ReadFile(path); string(content) != "external\n" {
		t.Fatalf("conflicting file was overwritten: %q", string(content))
	}

	// Once the file matches the recorded edit again the rollback succeeds and
	// consumes the record.
	if err := os.WriteFile(path, after, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore(ctx, "s1", 1); err != nil {
		t.Fatalf("restore: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(before) {
		t.Fatalf("restored content = %q, want %q", string(content), string(before))
	}
	if _, err := store.Restore(ctx, "s1", 1); !errors.Is(err, ErrRollbackNotFound) {
		t.Fatalf("second restore = %v, want ErrRollbackNotFound", err)
	}
}

func TestRollbackStoreRemovesFileCreatedByTheEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(path, []byte("created\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewRollbackStore()
	info, _ := store.Record("s1", path, nil, 0, false, "", ContentRevision([]byte("created\n")))
	if !info.Created {
		t.Fatalf("record = %#v, want Created", info)
	}
	if _, err := store.Restore(context.Background(), "s1", info.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created file must be removed, stat err = %v", err)
	}
}

func TestRollbackStoreScopesSessionsAndReplacesOlderRecords(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.txt")
	second := filepath.Join(dir, "b.txt")

	store := NewRollbackStore()
	store.Record("s1", first, []byte("v1\n"), 0o644, true, "r1", "r2")
	store.Record("s1", second, []byte("v2\n"), 0o644, true, "r3", "r4")
	store.Record("s2", first, []byte("v9\n"), 0o644, true, "r5", "r6")
	// A newer edit of the same file replaces the older record.
	store.Record("s1", first, []byte("v3\n"), 0o644, true, "r7", "r8")

	s1 := store.List("s1")
	if len(s1) != 2 || s1[0].Path != second || s1[1].Path != first {
		t.Fatalf("s1 list = %#v", s1)
	}
	if _, ok := store.Get("s1", 1); ok {
		t.Fatal("a replaced record is still reachable")
	}
	if s2 := store.List("s2"); len(s2) != 1 {
		t.Fatalf("s2 list = %#v", s2)
	}

	store.Forget("s1")
	if len(store.List("s1")) != 0 || len(store.List("s2")) != 1 {
		t.Fatalf("forget leaked: s1=%#v s2=%#v", store.List("s1"), store.List("s2"))
	}
	if _, err := store.Restore(context.Background(), "s1", 3); !errors.Is(err, ErrRollbackNotFound) {
		t.Fatalf("restore after forget = %v, want ErrRollbackNotFound", err)
	}
}

func TestRollbackStoreEvictsOldestRecords(t *testing.T) {
	store := NewRollbackStore()
	store.maxRecords = 2
	store.maxBytes = 1024
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		path := filepath.Join(dir, string(rune('a'+i))+".txt")
		store.Record("s1", path, []byte("content\n"), 0o644, true, "a", "b")
	}
	if listed := store.List("s1"); len(listed) != 2 {
		t.Fatalf("list = %#v, want the two newest records", listed)
	}
	if _, ok := store.Get("s1", 1); ok {
		t.Fatal("the oldest record must be evicted")
	}
}
