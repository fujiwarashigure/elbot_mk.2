package vision

// Metrics receives lightweight observations from the service. Implementations
// must be safe for concurrent use and must never log image bytes, base64, API
// keys, media IDs or cache keys.
type Metrics interface {
	// CacheLookup records "hit", "negative_hit" or "miss".
	CacheLookup(result string)
	// Coalesced records a request served by an in-flight identical job.
	Coalesced()
	// JobStarted records one unique upstream job.
	JobStarted()
	// Duration records a stage duration in seconds: "preprocess", "upstream",
	// "total".
	Duration(stage string, seconds float64)
	// Error records a safe error class such as "timeout", "canceled",
	// "http_429" or "truncated".
	Error(class string)
}

// NoopMetrics drops every observation.
type NoopMetrics struct{}

func (NoopMetrics) CacheLookup(string)       {}
func (NoopMetrics) Coalesced()               {}
func (NoopMetrics) JobStarted()              {}
func (NoopMetrics) Duration(string, float64) {}
func (NoopMetrics) Error(string)             {}

// Snapshotter is implemented by Metrics values that can report their counters.
// The in-process Counters implements it; Services report it through Stats().
type Snapshotter interface {
	MetricsSnapshot() Snapshot
}
