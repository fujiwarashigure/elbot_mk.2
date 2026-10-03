// Package ratelimit implements a small process-local, per-key sliding-window
// limiter with stale-key sweeping. It is dependency-free so independent
// in-memory services can share the same battle-tested behaviour.
package ratelimit

import (
	"sync"
	"time"
)

// Window is a per-key sliding-window counter. A nil pointer or a zero Window
// allows every request, which lets callers treat limiting as optional.
type Window struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	now       func() time.Time
	hits      map[string][]time.Time
	lastSweep time.Time
}

// New returns a limiter that allows max hits per key in each window.
func New(max int, window time.Duration) *Window {
	return NewWithClock(max, window, time.Now)
}

// NewWithClock is New with an injectable clock, mainly for tests.
func NewWithClock(max int, window time.Duration, now func() time.Time) *Window {
	if max <= 0 || window <= 0 || now == nil {
		return &Window{}
	}
	return &Window{max: max, window: window, now: now, hits: map[string][]time.Time{}}
}

// Allow records one hit and reports whether the key stayed within budget.
func (w *Window) Allow(key string) bool {
	if w == nil || w.max <= 0 || w.window <= 0 {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	w.sweepLocked(now)
	cutoff := now.Add(-w.window)
	kept := w.hits[key][:0]
	for _, at := range w.hits[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= w.max {
		w.hits[key] = kept
		return false
	}
	w.hits[key] = append(kept, now)
	return true
}

// Refund releases one previously recorded hit. It is used when a reserved
// write did not actually persist, so failed writes do not consume the budget.
func (w *Window) Refund(key string) {
	if w == nil || w.max <= 0 || w.window <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	entries := w.hits[key]
	if len(entries) == 0 {
		return
	}
	entries = entries[:len(entries)-1]
	if len(entries) == 0 {
		delete(w.hits, key)
		return
	}
	w.hits[key] = entries
}

// Len reports how many keys currently hold a hit. It is intended for tests and
// lightweight diagnostics.
func (w *Window) Len() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.hits)
}

// sweepLocked drops scopes that have no hit inside the current window. It runs
// at most once per window so the amortised cost stays constant.
func (w *Window) sweepLocked(now time.Time) {
	if !w.lastSweep.IsZero() && now.Sub(w.lastSweep) < w.window {
		return
	}
	w.lastSweep = now
	cutoff := now.Add(-w.window)
	for key, entries := range w.hits {
		kept := entries[:0]
		for _, at := range entries {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		if len(kept) == 0 {
			delete(w.hits, key)
			continue
		}
		w.hits[key] = kept
	}
}
