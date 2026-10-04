package vision

import (
	"sync"
	"time"
)

// Counters is a small thread-safe Metrics implementation for tests and the
// in-process operations snapshot. It deliberately keeps only bounded, low
// cardinality keys and never stores labels such as media IDs.
type Counters struct {
	mu        sync.Mutex
	cache     map[string]int64
	coalesced int64
	jobs      int64
	durations map[string]time.Duration
	errors    map[string]int64
}

// NewCounters returns an empty Counters.
func NewCounters() *Counters {
	return &Counters{
		cache:     map[string]int64{},
		durations: map[string]time.Duration{},
		errors:    map[string]int64{},
	}
}

func (c *Counters) CacheLookup(result string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.cache[result]++
	c.mu.Unlock()
}

func (c *Counters) Coalesced() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.coalesced++
	c.mu.Unlock()
}

func (c *Counters) JobStarted() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.jobs++
	c.mu.Unlock()
}

func (c *Counters) Duration(stage string, seconds float64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.durations[stage] += time.Duration(seconds * float64(time.Second))
	c.mu.Unlock()
}

func (c *Counters) Error(class string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.errors[class]++
	c.mu.Unlock()
}

// Snapshot is a copy of the current counters.
type Snapshot struct {
	Cache     map[string]int64
	Coalesced int64
	Jobs      int64
	Durations map[string]time.Duration
	Errors    map[string]int64
}

// MetricsSnapshot implements Snapshotter so a Service can expose its
// instrumentation to /metrics.
func (c *Counters) MetricsSnapshot() Snapshot {
	return c.Snapshot()
}

// Snapshot returns a deep copy of the counters.
func (c *Counters) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clone := func(source map[string]int64) map[string]int64 {
		out := make(map[string]int64, len(source))
		for key, value := range source {
			out[key] = value
		}
		return out
	}
	durations := make(map[string]time.Duration, len(c.durations))
	for key, value := range c.durations {
		durations[key] = value
	}
	return Snapshot{
		Cache:     clone(c.cache),
		Coalesced: c.coalesced,
		Jobs:      c.jobs,
		Durations: durations,
		Errors:    clone(c.errors),
	}
}
