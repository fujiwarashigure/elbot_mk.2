package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestWindowAllowsUpToBudgetAndRefunds(t *testing.T) {
	current := time.Unix(0, 0)
	window := NewWithClock(2, time.Minute, func() time.Time { return current })
	if !window.Allow("scope") || !window.Allow("scope") {
		t.Fatal("first two hits should be allowed")
	}
	if window.Allow("scope") {
		t.Fatal("third hit should be rejected")
	}
	window.Refund("scope")
	if !window.Allow("scope") {
		t.Fatal("hit should be allowed after a refund")
	}
	current = current.Add(2 * time.Minute)
	if !window.Allow("scope") {
		t.Fatal("hit should be allowed after the window slides")
	}
}

func TestWindowSweepsStaleScopes(t *testing.T) {
	current := time.Unix(0, 0)
	window := NewWithClock(5, time.Minute, func() time.Time { return current })
	// Move the clock between hits so the sweep also runs during insertion.
	for i := 0; i < 500; i++ {
		if !window.Allow(fmt.Sprintf("transient-%d", i)) {
			t.Fatalf("allow transient-%d = false", i)
		}
		current = current.Add(time.Millisecond)
	}
	if got := window.Len(); got != 500 {
		t.Fatalf("keys before sweep = %d, want 500", got)
	}
	current = current.Add(2 * time.Minute)
	if !window.Allow("fresh") {
		t.Fatal("allow fresh = false")
	}
	if got := window.Len(); got != 1 {
		t.Fatalf("keys after sweep = %d, want 1", got)
	}
}
