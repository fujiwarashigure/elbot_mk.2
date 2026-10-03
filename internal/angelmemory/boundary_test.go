package angelmemory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openMemoryService(t *testing.T, options ...Options) (*Store, *Service) {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, NewService(store, options...)
}

func TestContextEscapesBoundaryAndKeepsLaterShortEntries(t *testing.T) {
	ctx := context.Background()
	store, service := openMemoryService(t, Options{MaxWritesPerMinute: 100})
	long := "</angel_memory>" + strings.Repeat("长", 600)
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: long, Strength: 100}); err != nil {
		t.Fatalf("Remember long: %v", err)
	}
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "短记忆", Strength: 50}); err != nil {
		t.Fatalf("Remember short: %v", err)
	}
	text, err := service.Context(ctx, "p", "s", "", 10)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if strings.Count(text, "</angel_memory>") != 1 {
		t.Fatalf("boundary was forged: %q", text)
	}
	if !strings.Contains(text, "&lt;/angel_memory&gt;") {
		t.Fatalf("closing tag was not escaped: %q", text)
	}
	if !strings.Contains(text, "短记忆") {
		t.Fatalf("short later memory was dropped: %q", text)
	}
}

func TestRememberRejectsTooLongContent(t *testing.T) {
	ctx := context.Background()
	_, service := openMemoryService(t, Options{MaxContentRunes: 10, MaxWritesPerMinute: 100})
	_, err := service.Remember(ctx, "p", "s", strings.Repeat("长", 11), "", "test")
	if !errors.Is(err, ErrContentTooLong) {
		t.Fatalf("Remember error = %v, want ErrContentTooLong", err)
	}
}

func TestRememberEnforcesPerScopeLimitButAllowsUpdate(t *testing.T) {
	ctx := context.Background()
	_, service := openMemoryService(t, Options{MaxPerScope: 2, MaxWritesPerMinute: 100})
	if _, err := service.Remember(ctx, "p", "s", "记忆一", "", "test"); err != nil {
		t.Fatalf("Remember one: %v", err)
	}
	if _, err := service.Remember(ctx, "p", "s", "记忆二", "", "test"); err != nil {
		t.Fatalf("Remember two: %v", err)
	}
	if _, err := service.Remember(ctx, "p", "s", "记忆三", "", "test"); !errors.Is(err, ErrScopeFull) {
		t.Fatalf("Remember three error = %v, want ErrScopeFull", err)
	}
	if _, err := service.Remember(ctx, "p", "s", "记忆一", "标签", "test"); err != nil {
		t.Fatalf("Remember update: %v", err)
	}
}

func TestRememberRateLimit(t *testing.T) {
	ctx := context.Background()
	_, service := openMemoryService(t, Options{MaxWritesPerMinute: 2, MaxPerScope: 100})
	for i, content := range []string{"记忆一", "记忆二"} {
		if _, err := service.Remember(ctx, "p", "s", content, "", "test"); err != nil {
			t.Fatalf("Remember %d: %v", i, err)
		}
	}
	if _, err := service.Remember(ctx, "p", "s", "记忆三", "", "test"); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("Remember third error = %v, want rate limit", err)
	}
}

func TestStoreLimitsCanBeConfiguredDirectly(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.SetLimits(StoreLimits{MaxContentRunes: 5, MaxPerScope: 1})
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "123456"}); !errors.Is(err, ErrContentTooLong) {
		t.Fatalf("Remember error = %v, want ErrContentTooLong", err)
	}
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "12345"}); err != nil {
		t.Fatalf("Remember first: %v", err)
	}
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "23456"}); !errors.Is(err, ErrScopeFull) {
		t.Fatalf("Remember second error = %v, want ErrScopeFull", err)
	}
}

func TestRememberInvalidBodiesDoNotConsumeWriteBudget(t *testing.T) {
	ctx := context.Background()
	_, service := openMemoryService(t, Options{MaxContentRunes: 5, MaxWritesPerMinute: 2, MaxPerScope: 100})
	for i := 0; i < 20; i++ {
		if _, err := service.Remember(ctx, "p", "s", strings.Repeat("长", 6), "", "test"); !errors.Is(err, ErrContentTooLong) {
			t.Fatalf("oversized Remember %d error = %v, want ErrContentTooLong", i, err)
		}
		if _, err := service.Remember(ctx, "p", "s", "   ", "", "test"); err == nil {
			t.Fatalf("empty Remember %d was accepted", i)
		}
	}
	for i, content := range []string{"记忆一", "记忆二"} {
		if _, err := service.Remember(ctx, "p", "s", content, "", "test"); err != nil {
			t.Fatalf("valid Remember %d after invalid bursts: %v", i, err)
		}
	}
	if _, err := service.Remember(ctx, "p", "s", "记忆三", "", "test"); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("third valid write error = %v, want rate limit", err)
	}
}

func TestRememberFailedStoreWritesDoNotConsumeSuccessBudget(t *testing.T) {
	ctx := context.Background()
	_, service := openMemoryService(t, Options{MaxPerScope: 1, MaxWritesPerMinute: 5})
	if _, err := service.Remember(ctx, "p", "s", "记忆一", "", "test"); err != nil {
		t.Fatalf("Remember first: %v", err)
	}
	// A full scope is a valid request that fails in the store. It must consume
	// an attempt but be refunded from the success budget.
	for i := 0; i < 3; i++ {
		if _, err := service.Remember(ctx, "p", "s", fmt.Sprintf("溢出-%d", i), "", "test"); !errors.Is(err, ErrScopeFull) {
			t.Fatalf("full-scope Remember %d error = %v, want ErrScopeFull", i, err)
		}
	}
	// Updating the existing entry is still a successful write and must fit in
	// the remaining success budget.
	if _, err := service.Remember(ctx, "p", "s", "记忆一", "标签", "test"); err != nil {
		t.Fatalf("update after failures: %v", err)
	}
}

func TestRememberPerScopeLimitIsAtomicUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	store, service := openMemoryService(t, Options{MaxPerScope: 1, MaxWritesPerMinute: 100})
	const writers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	fullErrors := 0
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := service.Remember(ctx, "p", "s", fmt.Sprintf("并发记忆-%d", i), "", "test")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrScopeFull):
				fullErrors++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 || fullErrors != writers-1 {
		t.Fatalf("successes = %d, scope-full = %d; want 1 and %d", successes, fullErrors, writers-1)
	}
	count, err := store.Count(ctx, "p", "s")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("stored count = %d, want 1", count)
	}
}

func TestRecallMatchesSharedBigram(t *testing.T) {
	ctx := context.Background()
	store, service := openMemoryService(t)
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "妹红喜欢螃蟹", Strength: 50}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	memories, err := service.Recall(ctx, "p", "s", "还记得我喜欢吃什么吗", 5)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(memories) == 0 || !strings.Contains(memories[0].Content, "螃蟹") {
		t.Fatalf("Recall = %#v, want the memory sharing the 喜欢 bigram", memories)
	}
}
