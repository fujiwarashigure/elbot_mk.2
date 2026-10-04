package app

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"elbot/internal/agent"
	"elbot/internal/config"
	"elbot/internal/health"
	"elbot/internal/request"
	"elbot/internal/sysinfo"
	"elbot/internal/vision"
)

type lazyJSONProvider struct {
	mu sync.RWMutex
	fn func() any
}

func (p *lazyJSONProvider) Set(fn func() any) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.fn = fn
	p.mu.Unlock()
}

func (p *lazyJSONProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if p == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": "provider is nil"})
		return
	}
	p.mu.RLock()
	fn := p.fn
	p.mu.RUnlock()
	if fn == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": "provider is not initialized"})
		return
	}
	writeJSONResponse(w, http.StatusOK, fn())
}

func writeJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// lazyHTTPHandler is an extra health handler whose implementation is wired
// after the runtime stage has finished.
type lazyHTTPHandler struct {
	mu sync.RWMutex
	fn func(http.ResponseWriter, *http.Request)
}

func (h *lazyHTTPHandler) Set(fn func(http.ResponseWriter, *http.Request)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.fn = fn
	h.mu.Unlock()
}

func (h *lazyHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": "handler is nil"})
		return
	}
	h.mu.RLock()
	fn := h.fn
	h.mu.RUnlock()
	if fn == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": "handler is not initialized"})
		return
	}
	fn(w, r)
}

// rateLimitMetrics 是 /metrics 里的限速统计。
type rateLimitMetrics struct {
	Allowed             int64      `json:"allowed"`
	Rejected            int64      `json:"rejected"`
	UserRejected        int64      `json:"user_rejected"`
	GroupRejected       int64      `json:"group_rejected"`
	Keys                int        `json:"keys"`
	UserMessagesPerMin  int        `json:"user_messages_per_minute"`
	UserBurst           int        `json:"user_burst"`
	GroupMessagesPerMin int        `json:"group_messages_per_minute"`
	GroupBurst          int        `json:"group_burst"`
	LastReason          string     `json:"last_reason,omitempty"`
	LastRejectedAt      *time.Time `json:"last_rejected_at,omitempty"`
}

// imageLimitMetrics 是 /metrics 里的生图并发统计。
type imageLimitMetrics struct {
	Active  int `json:"active"`
	Waiting int `json:"waiting"`
}

// visionRunnerStats 是 /metrics 里单个视觉角色的运行器占用。
type visionRunnerStats struct {
	ActiveJobs        int `json:"active_jobs"`
	QueuedJobs        int `json:"queued_jobs"`
	MaxConcurrentJobs int `json:"max_concurrent_jobs"`
	MaxWaiters        int `json:"max_waiters"`
}

// visionMetrics 是 /metrics 里的图片描述引擎统计。image_to_prompt 工具与聊天
// 视觉兜底共用同一份计数器，因此计数只上报一次；两者的限流占用分别上报。
// 只包含计数、上限与占用，不含图片内容、媒体 ID、缓存键或凭据。
type visionMetrics struct {
	Cache         map[string]int64   `json:"cache,omitempty"`
	Coalesced     int64              `json:"coalesced"`
	Jobs          int64              `json:"jobs_started"`
	Errors        map[string]int64   `json:"errors,omitempty"`
	DurationsMS   map[string]int64   `json:"durations_ms,omitempty"`
	ImageToPrompt *visionRunnerStats `json:"image_to_prompt,omitempty"`
	Fallback      *visionRunnerStats `json:"fallback,omitempty"`
}

// visionMetricsState collects the shared counters plus the per-role runner
// occupancy. It is written once during startup and read by the metrics handler.
type visionMetricsState struct {
	counters *vision.Counters
	roles    map[string]*vision.Service
}

func newVisionMetricsState() *visionMetricsState {
	return &visionMetricsState{counters: vision.NewCounters(), roles: map[string]*vision.Service{}}
}

// countersValue returns the shared counters, creating them on demand so the
// callers never pass a nil Metrics.
func (s *visionMetricsState) countersValue() *vision.Counters {
	if s == nil {
		return nil
	}
	if s.counters == nil {
		s.counters = vision.NewCounters()
	}
	return s.counters
}

func (s *visionMetricsState) set(role string, service *vision.Service) {
	if s == nil || service == nil {
		return
	}
	if s.roles == nil {
		s.roles = map[string]*vision.Service{}
	}
	s.roles[role] = service
}

func (s *visionMetricsState) runnerStats(role string) *visionRunnerStats {
	service := s.roles[role]
	if service == nil {
		return nil
	}
	stats := service.Stats()
	return &visionRunnerStats{
		ActiveJobs:        stats.ActiveJobs,
		QueuedJobs:        stats.QueuedJobs,
		MaxConcurrentJobs: stats.MaxConcurrentJobs,
		MaxWaiters:        stats.MaxWaiters,
	}
}

// snapshot renders the /metrics payload. It returns nil when no vision role is
// configured, so an unused feature does not add noise to the endpoint.
func (s *visionMetricsState) snapshot() *visionMetrics {
	if s == nil {
		return nil
	}
	imageToPrompt := s.runnerStats("image_to_prompt")
	fallback := s.runnerStats("fallback")
	if imageToPrompt == nil && fallback == nil {
		return nil
	}
	metrics := &visionMetrics{
		ImageToPrompt: imageToPrompt,
		Fallback:      fallback,
	}
	if s.counters != nil {
		counters := s.counters.Snapshot()
		metrics.Cache = counters.Cache
		metrics.Coalesced = counters.Coalesced
		metrics.Jobs = counters.Jobs
		metrics.Errors = counters.Errors
		if len(counters.Durations) > 0 {
			metrics.DurationsMS = make(map[string]int64, len(counters.Durations))
			for stage, elapsed := range counters.Durations {
				metrics.DurationsMS[stage] = elapsed.Milliseconds()
			}
		}
	}
	return metrics
}

type opsDiagnostics struct {
	CollectedAt       time.Time               `json:"collected_at"`
	Health            health.Snapshot         `json:"health"`
	Tasks             request.Snapshot        `json:"tasks"`
	RateLimit         rateLimitMetrics        `json:"rate_limit"`
	Models            []health.ModelStatus    `json:"models,omitempty"`
	Platforms         []health.PlatformStatus `json:"platforms,omitempty"`
	LastRestartReason string                  `json:"last_restart_reason,omitempty"`
}

type opsMetrics struct {
	Version       string                  `json:"version,omitempty"`
	CollectedAt   time.Time               `json:"collected_at"`
	UptimeSeconds int64                   `json:"uptime_seconds"`
	Health        health.Snapshot         `json:"health"`
	Tasks         request.Snapshot        `json:"tasks"`
	Resources     sysinfo.Snapshot        `json:"resources"`
	Platforms     []health.PlatformStatus `json:"platforms,omitempty"`
	Models        []health.ModelStatus    `json:"models,omitempty"`
	RateLimit     rateLimitMetrics        `json:"rate_limit"`
	ImageLimit    imageLimitMetrics       `json:"image_limit"`
	Vision        *visionMetrics          `json:"vision,omitempty"`
}

func collectOpsDiagnostics(state *health.State, agt *agent.Agent) any {
	now := time.Now()
	healthSnapshot := health.Snapshot{}
	if state != nil {
		healthSnapshot = state.Snapshot()
	}
	tasks := request.Snapshot{}
	rateLimit := rateLimitMetrics{}
	if agt != nil {
		tasks = agt.ActiveRequests()
		status := agt.RateLimitStatus()
		rateLimit = rateLimitMetrics{
			Allowed:             status.Allowed,
			Rejected:            status.Rejected,
			UserRejected:        status.UserRejected,
			GroupRejected:       status.GroupRejected,
			Keys:                status.Keys,
			UserMessagesPerMin:  status.UserMessagesPerMin,
			UserBurst:           status.UserBurst,
			GroupMessagesPerMin: status.GroupMessagesPerMin,
			GroupBurst:          status.GroupBurst,
			LastReason:          status.LastReason,
			LastRejectedAt:      status.LastRejectedAt,
		}
	}
	return opsDiagnostics{
		CollectedAt:       now,
		Health:            healthSnapshot,
		Tasks:             tasks,
		RateLimit:         rateLimit,
		Models:            healthSnapshot.Models,
		Platforms:         healthSnapshot.Platforms,
		LastRestartReason: healthSnapshot.LastRestartReason,
	}
}

func collectOpsMetrics(cfg *config.Config, state *health.State, agt *agent.Agent, imageLimiter interface{ Stats() (int, int) }, visionStats func() *visionMetrics) any {
	now := time.Now()
	root := ""
	if cfg != nil {
		root = cfg.Sandbox.Root
		if root == "" {
			root = filepath.Dir(cfg.Storage.SessionsSQLitePath)
		}
	}
	snapshot := sysinfo.Collect(root)
	tasks := request.Snapshot{}
	if agt != nil {
		tasks = agt.ActiveRequests()
	}
	healthSnapshot := health.Snapshot{}
	if state != nil {
		healthSnapshot = state.Snapshot()
	}
	metrics := opsMetrics{
		CollectedAt: now,
		Health:      healthSnapshot,
		Tasks:       tasks,
		Resources:   snapshot,
	}
	if agt != nil {
		status := agt.RateLimitStatus()
		metrics.RateLimit = rateLimitMetrics{
			Allowed:             status.Allowed,
			Rejected:            status.Rejected,
			UserRejected:        status.UserRejected,
			GroupRejected:       status.GroupRejected,
			Keys:                status.Keys,
			UserMessagesPerMin:  status.UserMessagesPerMin,
			UserBurst:           status.UserBurst,
			GroupMessagesPerMin: status.GroupMessagesPerMin,
			GroupBurst:          status.GroupBurst,
			LastReason:          status.LastReason,
			LastRejectedAt:      status.LastRejectedAt,
		}
	}
	if imageLimiter != nil {
		active, waiting := imageLimiter.Stats()
		metrics.ImageLimit = imageLimitMetrics{Active: active, Waiting: waiting}
	}
	if visionStats != nil {
		metrics.Vision = visionStats()
	}
	if !healthSnapshot.StartedAt.IsZero() {
		metrics.UptimeSeconds = int64(now.Sub(healthSnapshot.StartedAt).Seconds())
	}
	metrics.Version = healthSnapshot.Version
	metrics.Platforms = healthSnapshot.Platforms
	metrics.Models = healthSnapshot.Models
	return metrics
}
