// Package ratelimit provides a small keyed token-bucket limiter.
package ratelimit

import (
	"strings"
	"sync"
	"time"
)

const (
	defaultIdleTTL         = 10 * time.Minute
	cleanupEveryNAllows    = 1024
	cleanupWhenBucketCount = 4096
)

// Limit configures one token bucket class.
type Limit struct {
	// RatePerSecond is the refill rate. Values <= 0 disable the limiter.
	RatePerSecond float64
	// Burst is the bucket capacity. Values <= 0 default to 1.
	Burst int
	// IdleTTL is how long an idle key is kept before cleanup.
	IdleTTL time.Duration
}

// Decision is the result of one Allow call.
type Decision struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

type bucket struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

// Limiter limits independent keys with a shared bucket configuration.
type Limiter struct {
	mu       sync.Mutex
	limit    Limit
	buckets  map[string]*bucket
	now      func() time.Time
	requests int
}

// New creates a keyed limiter. A non-positive rate disables all checks.
func New(limit Limit) *Limiter {
	if limit.Burst <= 0 {
		limit.Burst = 1
	}
	if limit.IdleTTL <= 0 {
		limit.IdleTTL = defaultIdleTTL
	}
	return &Limiter{
		limit:   limit,
		buckets: map[string]*bucket{},
		now:     time.Now,
	}
}

// Allow checks whether key may consume one token.
func (l *Limiter) Allow(key string) Decision {
	if l == nil || l.limit.RatePerSecond <= 0 {
		return Decision{Allowed: true}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return Decision{Allowed: true}
	}

	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: float64(l.limit.Burst), last: now, lastSeen: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.limit.RatePerSecond
		if b.tokens > float64(l.limit.Burst) {
			b.tokens = float64(l.limit.Burst)
		}
		b.last = now
	}
	b.lastSeen = now
	l.requests++
	l.cleanupLocked(now)

	if b.tokens >= 1 {
		b.tokens--
		return Decision{Allowed: true, Remaining: int(b.tokens)}
	}
	retryAfter := time.Duration(0)
	if l.limit.RatePerSecond > 0 {
		retryAfter = time.Duration((1 - b.tokens) / l.limit.RatePerSecond * float64(time.Second))
	}
	return Decision{Allowed: false, Remaining: 0, RetryAfter: retryAfter}
}

// Keys returns the number of tracked keys. Useful for metrics/tests.
func (l *Limiter) Keys() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (l *Limiter) cleanupLocked(now time.Time) {
	if l.requests%cleanupEveryNAllows != 0 && len(l.buckets) < cleanupWhenBucketCount {
		return
	}
	for key, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.limit.IdleTTL {
			delete(l.buckets, key)
		}
	}
}
