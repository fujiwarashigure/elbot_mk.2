package asr

import (
	"errors"
	"sync"
	"time"
)

type successEntry struct {
	value  Result
	expiry time.Time
}

// successCache is a small TTL map with earliest-expiry eviction.
type successCache struct {
	mu           sync.Mutex
	entries      map[string]successEntry
	max          int
	ttl          time.Duration
	maxValueSize int
	now          func() time.Time
}

func newSuccessCache(max int, ttl time.Duration, maxValueSize int, now func() time.Time) *successCache {
	if max <= 0 {
		max = DefaultCacheMaxEntries
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	if maxValueSize <= 0 {
		maxValueSize = DefaultMaxCacheValueSize
	}
	if now == nil {
		now = time.Now
	}
	return &successCache{entries: map[string]successEntry{}, max: max, ttl: ttl, maxValueSize: maxValueSize, now: now}
}

func (c *successCache) get(key string) (Result, bool) {
	if c == nil {
		return Result{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return Result{}, false
	}
	if !c.now().Before(entry.expiry) {
		delete(c.entries, key)
		return Result{}, false
	}
	value := entry.value
	value.Cached = true
	return value, true
}

func (c *successCache) put(key string, value Result) {
	if c == nil {
		return
	}
	value.Text = CleanText(value.Text)
	if value.Text == "" || len(value.Text) > c.maxValueSize {
		return
	}
	value.Cached = false
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for candidate, entry := range c.entries {
		if !now.Before(entry.expiry) {
			delete(c.entries, candidate)
		}
	}
	for len(c.entries) >= c.max {
		oldestKey := ""
		var oldest time.Time
		for candidate, entry := range c.entries {
			if oldestKey == "" || entry.expiry.Before(oldest) {
				oldestKey, oldest = candidate, entry.expiry
			}
		}
		if oldestKey == "" {
			break
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = successEntry{value: value, expiry: now.Add(c.ttl)}
}

type negativeEntry struct {
	statusCode int
	message    string
	expiry     time.Time
}

// negativeCache remembers only deterministic, non-retryable failures so a bad
// configuration does not hit the provider once per recording.
type negativeCache struct {
	mu      sync.Mutex
	entries map[string]negativeEntry
	max     int
	ttl     time.Duration
	now     func() time.Time
}

func newNegativeCache(max int, ttl time.Duration, now func() time.Time) *negativeCache {
	if max <= 0 {
		max = DefaultNegativeCacheMaxEntries
	}
	if ttl <= 0 {
		ttl = DefaultNegativeCacheTTL
	}
	if now == nil {
		now = time.Now
	}
	return &negativeCache{entries: map[string]negativeEntry{}, max: max, ttl: ttl, now: now}
}

func (c *negativeCache) get(key string) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil
	}
	if !c.now().Before(entry.expiry) {
		delete(c.entries, key)
		return nil
	}
	return &HTTPError{StatusCode: entry.statusCode, Message: entry.message}
}

func (c *negativeCache) put(key string, err error) {
	if c == nil || !deterministicError(err) {
		return
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for candidate, entry := range c.entries {
		if !now.Before(entry.expiry) {
			delete(c.entries, candidate)
		}
	}
	for len(c.entries) >= c.max {
		oldestKey := ""
		var oldest time.Time
		for candidate, entry := range c.entries {
			if oldestKey == "" || entry.expiry.Before(oldest) {
				oldestKey, oldest = candidate, entry.expiry
			}
		}
		if oldestKey == "" {
			break
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = negativeEntry{statusCode: httpErr.StatusCode, message: httpErr.Message, expiry: now.Add(c.ttl)}
}
