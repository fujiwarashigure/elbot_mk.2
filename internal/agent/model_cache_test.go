package agent

import (
	"errors"
	"testing"
	"time"
)

func TestModelListCacheTTL(t *testing.T) {
	if got := modelListCacheTTL(nil); got != modelListSuccessTTL {
		t.Fatalf("success TTL = %s, want %s", got, modelListSuccessTTL)
	}
	if got := modelListCacheTTL(errors.New("boom")); got != modelListErrorTTL {
		t.Fatalf("error TTL = %s, want %s", got, modelListErrorTTL)
	}
	if modelListErrorTTL > modelListSuccessTTL {
		t.Fatalf("error TTL (%s) must not outlive success TTL (%s)", modelListErrorTTL, modelListSuccessTTL)
	}
}

func TestModelListCacheEntryFresh(t *testing.T) {
	now := time.Now()
	entry := modelListCacheEntry{expiresAt: now.Add(time.Second)}
	if !entry.fresh(now) {
		t.Fatal("entry should be fresh before its expiry")
	}
	if entry.fresh(now.Add(2 * time.Second)) {
		t.Fatal("entry must expire once now passes expiresAt")
	}
	// A zero entry (never populated) must be treated as expired.
	var zero modelListCacheEntry
	if zero.fresh(now) {
		t.Fatal("zero-value entry must not be considered fresh")
	}
}
