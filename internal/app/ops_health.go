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

func collectOpsMetrics(cfg *config.Config, state *health.State, agt *agent.Agent, imageLimiter interface{ Stats() (int, int) }) any {
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
	if !healthSnapshot.StartedAt.IsZero() {
		metrics.UptimeSeconds = int64(now.Sub(healthSnapshot.StartedAt).Seconds())
	}
	metrics.Version = healthSnapshot.Version
	metrics.Platforms = healthSnapshot.Platforms
	metrics.Models = healthSnapshot.Models
	return metrics
}
