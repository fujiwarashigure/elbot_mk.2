package vision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"elbot/internal/llm"
)

// Defaults for the process-local caches. They are conservative so a small
// container stays bounded even if the operator tunes nothing.
const (
	DefaultCacheTTL          = 30 * time.Minute
	DefaultCacheMaxEntries   = 128
	DefaultMaxCacheValueSize = 16 * 1024
	DefaultNegativeCacheTTL  = 30 * time.Second
	DefaultNegativeMax       = 128
	DefaultSharedTimeout     = 3 * time.Minute
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
	if now == nil {
		now = time.Now
	}
	if max <= 0 {
		max = DefaultCacheMaxEntries
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	if maxValueSize <= 0 {
		maxValueSize = DefaultMaxCacheValueSize
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
	value.Text = strings.TrimSpace(value.Text)
	// Never cache an empty or oversized value, so a misbehaving upstream cannot
	// grow the cache beyond the per-entry budget. Together with the entry cap
	// this also bounds the total bytes held by the cache.
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
	code       string
	typeName   string
	param      string
	message    string
	// category is the adapter-controlled failure class; it is replayed so a
	// cached rejection keeps classifying the same way as the original error.
	category string
	expiry   time.Time
}

// negativeCache remembers only deterministic, safe failures so a known-bad
// configuration does not hit the upstream once per image. It stores structured
// fields, never a raw response body.
type negativeCache struct {
	mu      sync.Mutex
	entries map[string]negativeEntry
	max     int
	ttl     time.Duration
	now     func() time.Time
}

func newNegativeCache(max int, ttl time.Duration, now func() time.Time) *negativeCache {
	if now == nil {
		now = time.Now
	}
	if max <= 0 {
		max = DefaultNegativeMax
	}
	if ttl <= 0 {
		ttl = DefaultNegativeCacheTTL
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
	return &llm.APIError{
		StatusCode: entry.statusCode,
		Code:       entry.code,
		Type:       entry.typeName,
		Param:      entry.param,
		Message:    entry.message,
		Category:   entry.category,
	}
}

// put stores err only when it is an explicit, deterministic API failure.
// Transient failures (429, 5xx, network) and any cancellation/timeout are never
// cached, regardless of what status code a gateway attached to them.
func (c *negativeCache) put(key string, err error) {
	if c == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	apiErr, ok := llm.AsAPIError(err)
	if !ok || !apiErr.Deterministic() {
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
	c.entries[key] = negativeEntry{
		statusCode: apiErr.StatusCode,
		code:       apiErr.Code,
		typeName:   apiErr.Type,
		param:      apiErr.Param,
		message:    apiErr.Message,
		category:   apiErr.Category,
		expiry:     now.Add(c.ttl),
	}
}

type inflightCall struct {
	done   chan struct{}
	result Result
	err    error
	// waiters counts the callers currently waiting on done. It is guarded by
	// inflight.mu and bounds the queue behind one upstream job.
	waiters int
}

// ErrTooManyWaiters is returned when one upstream job already has its maximum
// number of coalesced callers: new callers fail fast instead of piling up in an
// unbounded queue behind a slow provider.
var ErrTooManyWaiters = errors.New("vision: too many callers waiting for the same job")

// inflight collapses concurrent identical jobs into one upstream call.
//
// Lifecycle: the shared work runs on a context detached from the first caller
// so one caller aborting does not fail the others. It still inherits the
// caller's deadline (a per-request timeout must bound the work) and is
// additionally canceled when the service context is canceled, so shutting the
// service down stops in-flight upstream work instead of letting it run to the
// shared timeout. Each waiter can abandon its own wait independently; if every
// waiter leaves, the work is still bounded by sharedTimeout.
type inflight struct {
	mu    sync.Mutex
	calls map[string]*inflightCall
	base  context.Context
}

func newInflight(base context.Context) *inflight {
	if base == nil {
		base = context.Background()
	}
	return &inflight{calls: map[string]*inflightCall{}, base: base}
}

func (g *inflight) do(ctx context.Context, key string, sharedTimeout time.Duration, maxWaiters int, fn func(context.Context) (Result, error)) (Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, false, err
	}
	g.mu.Lock()
	if call, ok := g.calls[key]; ok {
		if maxWaiters > 0 && call.waiters >= maxWaiters {
			g.mu.Unlock()
			return Result{}, false, ErrTooManyWaiters
		}
		call.waiters++
		g.mu.Unlock()
		result, err := waitInflight(ctx, call)
		g.mu.Lock()
		call.waiters--
		g.mu.Unlock()
		return result, true, err
	}
	call := &inflightCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	if sharedTimeout <= 0 {
		sharedTimeout = DefaultSharedTimeout
	}
	// WithoutCancel drops the caller's cancellation (one caller giving up must
	// not kill the job for everyone else) and, with it, the caller's deadline,
	// so the deadline is re-applied explicitly: the per-request timeout must
	// still bound the work even after every caller has walked away. The service
	// context is bridged in so shutdown can cancel the detached job too.
	sharedCtx := context.WithoutCancel(ctx)
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		sharedCtx, cancel = context.WithDeadline(sharedCtx, deadline)
	} else {
		sharedCtx, cancel = context.WithCancel(sharedCtx)
	}
	sharedCtx, timeoutCancel := context.WithTimeout(sharedCtx, sharedTimeout)
	go func() {
		defer cancel()
		defer timeoutCancel()
		stop := context.AfterFunc(g.base, cancel)
		defer stop()
		result, err := runShared(sharedCtx, fn)
		g.mu.Lock()
		call.result, call.err = result, err
		// Close before deleting: a caller arriving in this window still finds
		// the finished call and reuses it instead of starting a second one.
		close(call.done)
		delete(g.calls, key)
		g.mu.Unlock()
	}()
	result, err := waitInflight(ctx, call)
	return result, false, err
}

// runShared keeps a panic inside the detached goroutine from taking down the
// process; it is reported to every waiter as an error.
func runShared(ctx context.Context, fn func(context.Context) (Result, error)) (result Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = Result{}, fmt.Errorf("vision: job panicked: %v", recovered)
		}
	}()
	return fn(ctx)
}

func waitInflight(ctx context.Context, call *inflightCall) (Result, error) {
	select {
	case <-call.done:
		return call.result, call.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}
