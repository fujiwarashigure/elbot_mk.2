package vision

import (
	"context"
	"strings"
	"testing"
	"time"

	"elbot/internal/llm"
)

func TestSuccessCachePutGetAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	cache := newSuccessCache(8, time.Minute, DefaultMaxCacheValueSize, func() time.Time { return now })

	if _, ok := cache.get("missing"); ok {
		t.Fatal("missing key returned a hit")
	}
	cache.put("k", Result{Text: "value"})
	got, ok := cache.get("k")
	if !ok || got.Text != "value" || !got.Cached {
		t.Fatalf("get = (%+v, %v)", got, ok)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := cache.get("k"); ok {
		t.Fatal("expired entry was still returned")
	}
}

func TestSuccessCacheRejectsEmptyAndOversizedValues(t *testing.T) {
	cache := newSuccessCache(8, time.Minute, DefaultMaxCacheValueSize, time.Now)
	cache.put("empty", Result{Text: "   "})
	if _, ok := cache.get("empty"); ok {
		t.Fatal("empty value was cached")
	}
	cache.put("big", Result{Text: strings.Repeat("x", DefaultMaxCacheValueSize+1)})
	if _, ok := cache.get("big"); ok {
		t.Fatal("oversized value was cached")
	}
}

func TestSuccessCacheHonorsConfiguredValueLimit(t *testing.T) {
	cache := newSuccessCache(8, time.Minute, 4, time.Now)
	cache.put("too-big", Result{Text: "12345"})
	if _, ok := cache.get("too-big"); ok {
		t.Fatal("value over the configured per-entry limit was cached")
	}
	cache.put("fits", Result{Text: "1234"})
	if _, ok := cache.get("fits"); !ok {
		t.Fatal("value at the configured per-entry limit should be cached")
	}
}

func TestSuccessCacheEvictsAtCapacity(t *testing.T) {
	now := time.Unix(1000, 0)
	cache := newSuccessCache(2, time.Minute, DefaultMaxCacheValueSize, func() time.Time { return now })
	cache.put("a", Result{Text: "a"})
	now = now.Add(time.Second)
	cache.put("b", Result{Text: "b"})
	now = now.Add(time.Second)
	cache.put("c", Result{Text: "c"})

	if _, ok := cache.get("a"); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, ok := cache.get("c"); !ok {
		t.Fatal("newest entry should be present")
	}
}

func TestNegativeCacheStoresOnlyDeterministicFailures(t *testing.T) {
	now := time.Unix(1000, 0)
	cache := newNegativeCache(8, time.Minute, func() time.Time { return now })

	cache.put("transient", &llm.APIError{StatusCode: 429, Code: "model_not_found", Message: "rate limited"})
	if err := cache.get("transient"); err != nil {
		t.Fatalf("transient failure must not be cached: %v", err)
	}
	cache.put("plain", errPlain{})
	if err := cache.get("plain"); err != nil {
		t.Fatalf("non-API error must not be cached: %v", err)
	}
	cache.put("bare400", &llm.APIError{StatusCode: 400, Message: "bad request"})
	if err := cache.get("bare400"); err != nil {
		t.Fatalf("bare 400 must not be cached: %v", err)
	}

	cache.put("deterministic", &llm.APIError{
		StatusCode: 404,
		Code:       "model_not_found",
		Message:    "missing",
		Category:   llm.ErrorCategoryModelNotFound,
	})
	err := cache.get("deterministic")
	if err == nil {
		t.Fatal("deterministic failure should be cached")
	}
	apiErr, ok := llm.AsAPIError(err)
	if !ok || apiErr.Code != "model_not_found" || apiErr.StatusCode != 404 {
		t.Fatalf("cached error = %#v", err)
	}
	// The adapter-controlled category must survive the cache round trip: a
	// replayed rejection has to classify exactly like the original error.
	if apiErr.Category != llm.ErrorCategoryModelNotFound {
		t.Fatalf("cached category = %q", apiErr.Category)
	}

	now = now.Add(2 * time.Minute)
	if err := cache.get("deterministic"); err != nil {
		t.Fatalf("negative entry outlived its TTL: %v", err)
	}
}

func TestNegativeCacheIgnoresCancellationAndTimeout(t *testing.T) {
	now := time.Unix(1000, 0)
	cache := newNegativeCache(8, time.Minute, func() time.Time { return now })

	// A gateway may attach a deterministic-looking 4xx status before the body
	// read is canceled. The cancellation must win so one caller giving up can
	// never poison the cache for later requests.
	canceled := &llm.APIError{
		StatusCode: 404,
		Code:       "model_not_found",
		Message:    "missing",
		Cause:      context.Canceled,
	}
	cache.put("canceled", canceled)
	if err := cache.get("canceled"); err != nil {
		t.Fatalf("canceled failure was negative-cached: %v", err)
	}

	timedOut := &llm.APIError{
		StatusCode: 400,
		Code:       "invalid_value",
		Message:    "bad value",
		Cause:      context.DeadlineExceeded,
	}
	cache.put("timeout", timedOut)
	if err := cache.get("timeout"); err != nil {
		t.Fatalf("timed-out failure was negative-cached: %v", err)
	}

	// The same structured failure without a cancellation cause is still cached.
	realFailure := &llm.APIError{StatusCode: 404, Code: "model_not_found", Message: "missing"}
	cache.put("real", realFailure)
	if err := cache.get("real"); err == nil {
		t.Fatal("a deterministic failure should still be cached")
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "plain error" }
