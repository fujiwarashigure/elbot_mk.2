package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestLimiterBurstAndRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(Limit{RatePerSecond: 1, Burst: 2, IdleTTL: time.Minute})
	l.now = func() time.Time { return now }

	if got := l.Allow("u1"); !got.Allowed || got.Remaining != 1 {
		t.Fatalf("first Allow = %#v", got)
	}
	if got := l.Allow("u1"); !got.Allowed || got.Remaining != 0 {
		t.Fatalf("second Allow = %#v", got)
	}
	if got := l.Allow("u1"); got.Allowed || got.RetryAfter != time.Second {
		t.Fatalf("third Allow = %#v", got)
	}
	now = now.Add(2 * time.Second)
	if got := l.Allow("u1"); !got.Allowed {
		t.Fatalf("Allow after refill = %#v", got)
	}
}

func TestLimiterIndependentKeys(t *testing.T) {
	l := New(Limit{RatePerSecond: 1, Burst: 1})
	if !l.Allow("a").Allowed || !l.Allow("b").Allowed {
		t.Fatal("independent keys should not share buckets")
	}
	if l.Allow("a").Allowed {
		t.Fatal("key a should be exhausted")
	}
}

func TestLimiterDisabled(t *testing.T) {
	l := New(Limit{RatePerSecond: 0, Burst: 0})
	for i := 0; i < 100; i++ {
		if !l.Allow("u").Allowed {
			t.Fatal("disabled limiter should always allow")
		}
	}
	if l.Keys() != 0 {
		t.Fatalf("disabled limiter tracked %d keys", l.Keys())
	}
}

func TestLimiterCleanup(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(Limit{RatePerSecond: 1, Burst: 1, IdleTTL: time.Second})
	l.now = func() time.Time { return now }
	if !l.Allow("old").Allowed {
		t.Fatal("allow old failed")
	}
	now = now.Add(2 * time.Second)
	if !l.Allow("new").Allowed {
		t.Fatal("allow new failed")
	}
	// A cleanup happens at request 1024 or when bucket count >= 4096; request it explicitly.
	l.mu.Lock()
	l.cleanupLocked(now)
	l.mu.Unlock()
	if l.Keys() != 1 {
		t.Fatalf("keys = %d, want 1", l.Keys())
	}
}

func TestLimiterConcurrent(t *testing.T) {
	l := New(Limit{RatePerSecond: 1, Burst: 1})
	var wg sync.WaitGroup
	allowed := 0
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("same").Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 1 {
		t.Fatalf("allowed = %d, want 1", allowed)
	}
}
