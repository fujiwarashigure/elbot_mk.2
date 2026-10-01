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

// rateLimitMetrics 是 /metrics 里的限速统计（agent.RateLimitStats）。
type rateLimitMetrics struct {
	Allowed  int64 `json:"allowed"`
	Rejected int64 `json:"rejected"`
	Keys     int   `json:"keys"`
}

// imageLimitMetrics 是 /metrics 里的生图并发统计。
type imageLimitMetrics struct {
	Active  int `json:"active"`
	Waiting int `json:"waiting"`
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
		allowed, rejected, keys := agt.RateLimitStats()
		metrics.RateLimit = rateLimitMetrics{Allowed: allowed, Rejected: rejected, Keys: keys}
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
