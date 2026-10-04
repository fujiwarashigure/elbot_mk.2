package vision

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"elbot/internal/llm"
	"elbot/internal/ops/concurrency"
)

// concurrencyProbeLLM records the highest number of simultaneous ChatStream
// calls and blocks until released, so the runner cap can be observed.
type concurrencyProbeLLM struct {
	mu         sync.Mutex
	current    int
	max        int
	saturateAt int
	release    chan struct{}
	ready      chan struct{}
	readyOne   sync.Once
}

func newConcurrencyProbeLLM(saturateAt int) *concurrencyProbeLLM {
	return &concurrencyProbeLLM{release: make(chan struct{}), ready: make(chan struct{}), saturateAt: saturateAt}
}

func (p *concurrencyProbeLLM) ChatStream(ctx context.Context, _ llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	p.mu.Lock()
	p.current++
	if p.current > p.max {
		p.max = p.current
	}
	if p.current >= p.saturateAt {
		p.readyOne.Do(func() { close(p.ready) })
	}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.current--
		p.mu.Unlock()
	}()

	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{DeltaContent: "described"}
	close(ch)
	return ch, nil
}

func (p *concurrencyProbeLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func (p *concurrencyProbeLLM) maxConcurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.max
}

// distinctRequest returns a request with its own cache key, i.e. its own job.
func distinctRequest(t *testing.T, index int) Request {
	t.Helper()
	req := testRequest(t)
	req.MediaID = fmt.Sprintf("media:%064x", index)
	return req
}

func TestServiceBoundsConcurrentJobs(t *testing.T) {
	client := newConcurrencyProbeLLM(2)
	service := newTestService(t, client, NewCounters(), func(opts *Options) {
		opts.MaxConcurrentJobs = 2
		opts.MaxJobQueue = 8
	})

	const jobs = 6
	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		req := distinctRequest(t, i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.Describe(context.Background(), req); err != nil {
				t.Errorf("Describe: %v", err)
			}
		}()
	}
	select {
	case <-client.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("the first two jobs never started")
	}
	// Give the remaining four jobs a chance to exceed the cap; they are queued
	// locally and must not reach the provider.
	time.Sleep(50 * time.Millisecond)
	if got := client.maxConcurrent(); got != 2 {
		t.Fatalf("concurrent upstream jobs = %d, want the configured 2", got)
	}
	close(client.release)
	wg.Wait()
	if got := client.maxConcurrent(); got != 2 {
		t.Fatalf("peak concurrent upstream jobs = %d, want 2", got)
	}
}

func TestServiceRejectsExcessWaitersForOneJob(t *testing.T) {
	client := &concurrencyProbeLLM{release: make(chan struct{}), ready: make(chan struct{}), saturateAt: 1}
	service := newTestService(t, client, NewCounters(), func(opts *Options) {
		opts.MaxConcurrentJobs = 1
		opts.MaxJobWaiters = 2
	})
	req := testRequest(t)

	leaderErr := make(chan error, 1)
	go func() {
		_, err := service.Describe(context.Background(), req)
		leaderErr <- err
	}()
	select {
	case <-client.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("leader never reached the provider")
	}

	const extra = 6
	results := make(chan error, extra)
	for i := 0; i < extra; i++ {
		go func() {
			_, err := service.Describe(context.Background(), req)
			results <- err
		}()
	}
	// Only MaxJobWaiters callers may coalesce; the rest fail fast, which means
	// they are the first results to arrive (the admitted ones are blocked).
	for rejected := 0; rejected < extra-2; rejected++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrTooManyWaiters) {
				t.Fatalf("waiter error = %v, want ErrTooManyWaiters", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for waiter rejections")
		}
	}
	close(client.release)
	if err := <-leaderErr; err != nil {
		t.Fatalf("leader error = %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("admitted waiter error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("admitted waiters never finished")
		}
	}
}

// TestServiceRejectsJobWhenQueueIsFull proves a saturated vision backend fails
// fast instead of letting an unbounded queue of jobs pile up: with one runner
// and one queue slot, exactly one of three simultaneous distinct jobs is
// rejected with the limiter's full-queue error.
func TestServiceRejectsJobWhenQueueIsFull(t *testing.T) {
	client := &concurrencyProbeLLM{release: make(chan struct{}), ready: make(chan struct{}), saturateAt: 1}
	service := newTestService(t, client, NewCounters(), func(opts *Options) {
		opts.MaxConcurrentJobs = 1
		opts.MaxJobQueue = 1
		opts.MaxJobWaiters = -1
	})

	const jobs = 3
	results := make(chan error, jobs)
	for i := 0; i < jobs; i++ {
		req := distinctRequest(t, i)
		go func() {
			_, err := service.Describe(context.Background(), req)
			results <- err
		}()
	}

	rejected := 0
	for i := 0; i < jobs-2; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, concurrency.ErrFull) {
				t.Fatalf("queued job error = %v, want ErrFull", err)
			}
			rejected++
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for the full-queue rejection")
		}
	}
	if rejected != 1 {
		t.Fatalf("rejected jobs = %d, want 1", rejected)
	}
	close(client.release)
	for i := 0; i < jobs-1; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("admitted job error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("admitted jobs never finished")
		}
	}
}

func TestServiceStatsReportsCountersAndLimits(t *testing.T) {
	counters := NewCounters()
	client := &fakeLLM{stream: []llm.StreamChunk{{DeltaContent: "described"}}}
	service := newTestService(t, client, counters, func(opts *Options) {
		opts.MaxConcurrentJobs = 3
		opts.MaxJobWaiters = 7
	})
	if _, err := service.Describe(context.Background(), testRequest(t)); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	stats := service.Stats()
	if stats.Jobs != 1 || stats.Cache["miss"] != 1 || stats.Coalesced != 0 {
		t.Fatalf("stats = %#v", stats)
	}
	if stats.MaxConcurrentJobs != 3 || stats.MaxWaiters != 7 {
		t.Fatalf("limits = %d/%d, want 3/7", stats.MaxConcurrentJobs, stats.MaxWaiters)
	}
	if stats.ActiveJobs != 0 || stats.QueuedJobs != 0 {
		t.Fatalf("limiter occupancy after the call = %d/%d", stats.ActiveJobs, stats.QueuedJobs)
	}
	if len(stats.Errors) != 0 {
		t.Fatalf("unexpected error counters: %#v", stats.Errors)
	}

	// A cache hit must show up in the counters the ops endpoint serves.
	if _, err := service.Describe(context.Background(), testRequest(t)); err != nil {
		t.Fatalf("cached Describe: %v", err)
	}
	stats = service.Stats()
	if stats.Cache["hit"] != 1 || stats.Jobs != 1 {
		t.Fatalf("cached stats = %#v", stats)
	}
}
