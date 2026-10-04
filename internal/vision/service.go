// Package vision turns images into text with a configured vision-capable LLM.
//
// It is the single description engine shared by the built-in image_to_prompt
// tool and the chat vision fallback. Cache keys are versioned and include a
// full configuration fingerprint, so changing the model, prompt or preprocessing
// always misses while a log-level change does not.
package vision

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"elbot/internal/llm"
	"elbot/internal/media"
	"elbot/internal/ops/concurrency"
)

const (
	// DefaultTimeout bounds preprocessing + request + stream for one job.
	DefaultTimeout = 90 * time.Second
	// DefaultMaxOutputBytes caps the accumulated streamed answer.
	DefaultMaxOutputBytes = 64 * 1024
	// DefaultMaxConcurrentJobs bounds how many distinct upstream vision jobs run
	// at the same time. A burst of images (or the parallel multi-image fallback)
	// must not open an unbounded number of provider connections.
	DefaultMaxConcurrentJobs = 4
	// DefaultMaxJobQueue bounds how many jobs may wait for a runner slot before
	// new callers are rejected with a full-queue error.
	DefaultMaxJobQueue = 16
	// DefaultMaxJobWaiters bounds how many callers may coalesce onto one
	// in-flight job; further callers fail fast instead of waiting forever in an
	// unbounded queue behind a slow provider.
	DefaultMaxJobWaiters = 16
)

// Options configures a Service. The zero value is not usable; New applies the
// documented defaults for unset fields.
type Options struct {
	Client        llm.LLM
	Provider      string
	Endpoint      string
	Model         string
	MaxTokens     int
	Temperature   float64
	MaxEdge       int
	MaxImageBytes int64
	Timeout       time.Duration
	// MaxOutputBytes caps the accumulated streamed answer (default 64 KiB).
	MaxOutputBytes int

	CacheTTL          time.Duration
	CacheMaxEntries   int
	MaxCacheValueSize int
	NegativeTTL       time.Duration
	NegativeMax       int
	SharedTimeout     time.Duration

	// MaxConcurrentJobs bounds the upstream jobs running at the same time
	// (default DefaultMaxConcurrentJobs). A negative value disables the cap.
	MaxConcurrentJobs int
	// MaxJobQueue bounds the jobs waiting for a runner slot (default
	// DefaultMaxJobQueue). A negative value disables the queue cap, in which
	// case a job only waits for its own context.
	MaxJobQueue int
	// MaxJobWaiters bounds the callers coalesced onto one in-flight job
	// (default DefaultMaxJobWaiters). A negative value disables the cap.
	MaxJobWaiters int

	// BaseContext, when set, cancels all in-flight shared jobs when it is done.
	// Embedders pass their process/shutdown context so a slow upstream call is
	// stopped on shutdown instead of running until SharedTimeout.
	BaseContext context.Context

	// CredentialEpoch lets a credential/account change invalidate cached
	// results without putting the credential itself in the key.
	CredentialEpoch uint64

	Metrics Metrics
	Now     func() time.Time
}

// Prompt is one description template. Version must change whenever System or
// User changes in a way that alters the output shape; it is part of the cache
// fingerprint together with a hash of the actual text.
type Prompt struct {
	Version string
	System  string
	User    string
}

// Request is one image to describe.
type Request struct {
	MediaID  string
	Data     []byte
	MIMEType string
	Prompt   Prompt
}

// Result is one successful description. Provider/Model identify the model that
// actually produced it, so callers never present a fallback result as the main
// model's output.
type Result struct {
	Text     string
	Provider string
	Model    string
	Cached   bool
}

// Service describes images with one shared cache and coalescing layer.
type Service struct {
	opts     Options
	success  *successCache
	negative *negativeCache
	inflight *inflight
	// runner bounds concurrent upstream jobs. Acquire is a no-op when the cap is
	// disabled, so the hot path stays allocation-free in that case.
	runner            *concurrency.Limiter
	maxConcurrentJobs int
	maxWaiters        int
}

// New builds a Service, applying defaults for unset cache/timeout fields.
func New(opts Options) *Service {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if opts.Metrics == nil {
		opts.Metrics = NoopMetrics{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	// 0 means "use the default", negative means "unbounded": Default* values are
	// deliberately small enough to protect a provider from a burst of images.
	maxConcurrent := DefaultMaxConcurrentJobs
	if opts.MaxConcurrentJobs < 0 {
		maxConcurrent = 0
	} else if opts.MaxConcurrentJobs > 0 {
		maxConcurrent = opts.MaxConcurrentJobs
	}
	maxQueue := DefaultMaxJobQueue
	if opts.MaxJobQueue < 0 {
		maxQueue = 0
	} else if opts.MaxJobQueue > 0 {
		maxQueue = opts.MaxJobQueue
	}
	maxWaiters := DefaultMaxJobWaiters
	if opts.MaxJobWaiters < 0 {
		maxWaiters = 0
	} else if opts.MaxJobWaiters > 0 {
		maxWaiters = opts.MaxJobWaiters
	}
	return &Service{
		opts:              opts,
		success:           newSuccessCache(opts.CacheMaxEntries, opts.CacheTTL, opts.MaxCacheValueSize, opts.Now),
		negative:          newNegativeCache(opts.NegativeMax, opts.NegativeTTL, opts.Now),
		inflight:          newInflight(opts.BaseContext),
		runner:            concurrency.New(concurrency.Config{Max: maxConcurrent, QueueSize: maxQueue}),
		maxConcurrentJobs: maxConcurrent,
		maxWaiters:        maxWaiters,
	}
}

func (s *Service) metrics() Metrics {
	if s == nil || s.opts.Metrics == nil {
		return NoopMetrics{}
	}
	return s.opts.Metrics
}

// Describe returns a description for one image, using the success cache, the
// negative cache and in-flight coalescing before calling the upstream model.
func (s *Service) Describe(ctx context.Context, req Request) (Result, error) {
	if s == nil || s.opts.Client == nil || strings.TrimSpace(s.opts.Model) == "" {
		return Result{}, fmt.Errorf("vision: model is not configured")
	}
	if strings.TrimSpace(req.MediaID) == "" {
		return Result{}, fmt.Errorf("vision: media id is required")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if s.opts.Timeout > 0 {
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, s.opts.Timeout)
		defer timeoutCancel()
		ctx = timeoutCtx
	}

	key, err := s.cacheKey(req)
	if err != nil {
		return Result{}, err
	}

	if cached, ok := s.success.get(key); ok {
		s.metrics().CacheLookup("hit")
		return cached, nil
	}
	if negative := s.negative.get(key); negative != nil {
		s.metrics().CacheLookup("negative_hit")
		return Result{}, negative
	}
	s.metrics().CacheLookup("miss")

	started := time.Now()
	result, coalesced, err := s.inflight.do(ctx, key, s.opts.SharedTimeout, s.maxWaiters, func(sharedCtx context.Context) (Result, error) {
		// Re-check after becoming the leader: a caller that missed the cache and
		// queued behind the previous wave must not start a second upstream call
		// once that wave has just finished.
		if cached, ok := s.success.get(key); ok {
			return cached, nil
		}
		// Bound the number of upstream jobs. Waiting here is bounded by the
		// shared job context (caller deadline / shared timeout), so a saturated
		// provider can never make a caller wait forever.
		release, err := s.runner.Acquire(sharedCtx)
		if err != nil {
			return Result{}, fmt.Errorf("vision: job queue: %w", err)
		}
		defer release()
		s.metrics().JobStarted()
		value, runErr := s.run(sharedCtx, req)
		if runErr != nil {
			// Only deterministic (explicit model/parameter) failures are
			// remembered; transient failures never are.
			s.negative.put(key, runErr)
			s.metrics().Error(classifyError(runErr))
			return Result{}, runErr
		}
		s.success.put(key, value)
		return value, nil
	})
	if coalesced {
		s.metrics().Coalesced()
	}
	if err != nil {
		return Result{}, err
	}
	s.metrics().Duration("total", time.Since(started).Seconds())
	return result, nil
}

func (s *Service) cacheKey(req Request) (string, error) {
	requestHash, err := requestFingerprint{
		MaxTokens:      s.opts.MaxTokens,
		Temperature:    s.opts.Temperature,
		MaxEdge:        s.opts.MaxEdge,
		MaxImageBytes:  s.opts.MaxImageBytes,
		OutputMIMEType: "image/jpeg",
	}.hash()
	if err != nil {
		return "", err
	}
	identity := CacheIdentity{
		SchemaVersion:     CacheSchemaVersion,
		MediaID:           strings.TrimSpace(req.MediaID),
		Provider:          strings.TrimSpace(s.opts.Provider),
		Endpoint:          strings.TrimSpace(s.opts.Endpoint),
		Model:             strings.TrimSpace(s.opts.Model),
		PromptVersion:     strings.TrimSpace(req.Prompt.Version),
		PromptHash:        hashText(req.Prompt.System + "\x00" + req.Prompt.User),
		PreprocessVersion: PreprocessVersion,
		RequestHash:       requestHash,
		CredentialEpoch:   s.opts.CredentialEpoch,
	}
	return identity.Key()
}

// run performs the preprocess + upstream call for a cache miss.
func (s *Service) run(ctx context.Context, req Request) (Result, error) {
	preprocessStart := time.Now()
	data, mimeType, err := media.PrepareVisionImageContext(ctx, req.Data, req.MIMEType, s.opts.MaxEdge, s.opts.MaxImageBytes)
	if err != nil {
		return Result{}, err
	}
	s.metrics().Duration("preprocess", time.Since(preprocessStart).Seconds())
	if strings.TrimSpace(mimeType) == "" {
		mimeType = http.DetectContentType(data)
	}
	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)

	upstreamStart := time.Now()
	// A dedicated cancellable context lets us stop reading and close the
	// transport as soon as we give up (output cap, error chunk, or a caller
	// that abandons the shared job) instead of leaving the producer blocked on
	// an unread channel until the outer deadline fires.
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	stream, err := s.opts.Client.ChatStream(streamCtx, llm.ChatRequest{
		Model: s.opts.Model,
		Messages: []llm.LLMMessage{
			{Role: llm.RoleSystem, Segments: llm.TextSegments(req.Prompt.System)},
			{Role: llm.RoleUser, Segments: []llm.MessageSegment{
				{Type: llm.SegmentText, Text: req.Prompt.User},
				{Type: llm.SegmentImage, URL: dataURL, MIMEType: mimeType},
			}},
		},
		Temperature: s.opts.Temperature,
		MaxTokens:   s.opts.MaxTokens,
	})
	if err != nil {
		return Result{}, err
	}

	var out strings.Builder
	finishReason := ""
	for chunk := range stream {
		if chunk.Error != nil {
			return Result{}, chunk.Error
		}
		if chunk.FinishReason != "" {
			// The reason can arrive on an otherwise empty terminal event.
			finishReason = chunk.FinishReason
		}
		out.WriteString(chunk.DeltaContent)
		if s.opts.MaxOutputBytes > 0 && out.Len() > s.opts.MaxOutputBytes {
			return Result{}, fmt.Errorf("vision: model output exceeded %d KiB before finishing", s.opts.MaxOutputBytes>>10)
		}
	}
	s.metrics().Duration("upstream", time.Since(upstreamStart).Seconds())

	// A channel that closed without a terminator is surfaced by the adapter as
	// an Error chunk, so success is never inferred from "the channel closed".
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("vision: request stopped before finishing: %w", err)
	}
	if truncated(finishReason) {
		return Result{}, fmt.Errorf("vision: model output was truncated (finish_reason=%s)", finishReason)
	}
	value := CleanText(out.String())
	if value == "" {
		return Result{}, fmt.Errorf("vision: model returned an empty result")
	}
	return Result{Text: value, Provider: s.opts.Provider, Model: s.opts.Model}, nil
}

func truncated(finishReason string) bool {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length", "max_tokens", "max_output_tokens":
		return true
	default:
		return false
	}
}

func classifyError(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if apiErr, ok := llm.AsAPIError(err); ok && apiErr.StatusCode > 0 {
		return fmt.Sprintf("http_%d", apiErr.StatusCode)
	}
	return "other"
}

// Stats is the in-process operations view of one Service. It carries counters,
// caps and limiter occupancy only: no image bytes, media IDs, cache keys or
// credentials, so it is safe to serve from the ops /metrics endpoint.
type Stats struct {
	Cache             map[string]int64 `json:"cache,omitempty"`
	Coalesced         int64            `json:"coalesced"`
	Jobs              int64            `json:"jobs_started"`
	Errors            map[string]int64 `json:"errors,omitempty"`
	DurationsMS       map[string]int64 `json:"durations_ms,omitempty"`
	ActiveJobs        int              `json:"active_jobs"`
	QueuedJobs        int              `json:"queued_jobs"`
	MaxConcurrentJobs int              `json:"max_concurrent_jobs"`
	MaxWaiters        int              `json:"max_waiters"`
}

// Stats returns the current instrumentation. It is safe to call concurrently
// and on a nil Service.
func (s *Service) Stats() Stats {
	stats := Stats{}
	if s == nil {
		return stats
	}
	if snapshotter, ok := s.opts.Metrics.(Snapshotter); ok {
		snapshot := snapshotter.MetricsSnapshot()
		stats.Cache = snapshot.Cache
		stats.Coalesced = snapshot.Coalesced
		stats.Jobs = snapshot.Jobs
		stats.Errors = snapshot.Errors
		if len(snapshot.Durations) > 0 {
			stats.DurationsMS = make(map[string]int64, len(snapshot.Durations))
			for stage, elapsed := range snapshot.Durations {
				stats.DurationsMS[stage] = elapsed.Milliseconds()
			}
		}
	}
	if s.runner != nil {
		stats.ActiveJobs, stats.QueuedJobs = s.runner.Stats()
	}
	stats.MaxConcurrentJobs = s.maxConcurrentJobs
	stats.MaxWaiters = s.maxWaiters
	return stats
}
