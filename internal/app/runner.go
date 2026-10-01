package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const defaultShutdownTimeout = 30 * time.Second

type cleanupStep struct {
	name  string
	close func(context.Context) error
}

func (r *Runner) Run(ctx context.Context, opts Options) (runErr error) {
	var cleanups []cleanupStep
	defer func() {
		shutdownTimeout := r.shutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = defaultShutdownTimeout
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		for i := len(cleanups) - 1; i >= 0; i-- {
			if err := cleanups[i].close(shutdownCtx); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("close %s: %w", cleanups[i].name, err))
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}

	mode, err := r.deps.Environment.ResolveMode(opts.Mode)
	if err != nil {
		return err
	}
	marker, err := r.deps.Environment.ClaimServiceMarker(mode)
	if err != nil {
		return err
	}
	if marker != nil {
		cleanups = append(cleanups, cleanupStep{name: "service marker", close: func(context.Context) error { return marker.Close() }})
	}

	profiler := r.deps.Environment.NewStartupProfiler(opts.StartedAt)
	if profiler == nil {
		return fmt.Errorf("app: environment returned nil startup profiler")
	}
	foundation, err := r.deps.Foundation.Build(ctx, FoundationRequest{Options: opts, Mode: mode, Profiler: profiler})
	if err != nil {
		return err
	}
	if foundation == nil {
		return fmt.Errorf("app: foundation factory returned incomplete components")
	}
	if foundation.Lifecycle == nil {
		return fmt.Errorf("app: foundation factory returned incomplete components")
	}
	cleanups = append(cleanups, cleanupStep{name: "foundation", close: foundation.Lifecycle.Close})
	if foundation.Logger == nil {
		return fmt.Errorf("app: foundation factory returned incomplete components")
	}

	tasksProvider := &lazyJSONProvider{}
	metricsProvider := &lazyJSONProvider{}
	diagnosticsProvider := &lazyJSONProvider{}
	memoryAdminProvider := &lazyHTTPHandler{}
	learningAdminProvider := &lazyHTTPHandler{}
	healthState, healthServer, err := startHealthServer(foundation.Config, foundation.Logger, opts.Version, map[string]http.Handler{
		"/tasks":            tasksProvider,
		"/metrics":          metricsProvider,
		"/diagnostics":      diagnosticsProvider,
		"/plugins/memory":   memoryAdminProvider,
		"/plugins/learning": learningAdminProvider,
	})
	if err != nil {
		return err
	}
	if healthServer != nil {
		cleanups = append(cleanups, cleanupStep{name: "health server", close: func(ctx context.Context) error {
			return closeHealthServer(ctx, healthServer)
		}})
	}
	applyRestartReason(healthState)
	if healthState != nil {
		cleanups = append(cleanups, cleanupStep{name: "health state", close: func(context.Context) error {
			healthState.SetShuttingDown(true)
			healthState.SetReady(false)
			return nil
		}})
	}

	models, err := r.deps.Models.Build(ModelRequest{Foundation: foundation, Profiler: profiler, Health: healthState})
	if err != nil {
		return err
	}
	platforms, err := r.deps.Platforms.Build(PlatformRequest{Foundation: foundation, Mode: mode, Profiler: profiler})
	if err != nil {
		return err
	}
	runtime, err := r.deps.Runtime.Build(ctx, RuntimeRequest{Foundation: foundation, Models: models, Platforms: platforms, Profiler: profiler})
	if err != nil {
		return err
	}
	if runtime == nil {
		return fmt.Errorf("app: runtime factory returned incomplete components")
	}
	if runtime.Lifecycle == nil {
		return fmt.Errorf("app: runtime factory returned incomplete components")
	}
	cleanups = append(cleanups, cleanupStep{name: "runtime", close: runtime.Lifecycle.Close})
	if runtime.Handler == nil {
		return fmt.Errorf("app: runtime factory returned incomplete components")
	}

	platforms, err = r.deps.Integrations.Attach(ctx, IntegrationRequest{
		Foundation: foundation,
		Runtime:    runtime,
		Platforms:  platforms,
		Mode:       mode,
		Profiler:   profiler,
		Health:     healthState,
	})
	if err != nil {
		return err
	}

	tasksProvider.Set(func() any { return runtime.Agent.ActiveRequests() })
	metricsProvider.Set(func() any {
		return collectOpsMetrics(foundation.Config, healthState, runtime.Agent, runtime.ImageLimiter)
	})
	diagnosticsProvider.Set(func() any { return collectOpsDiagnostics(healthState, runtime.Agent) })
	memoryAdminProvider.Set(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		platform := r.URL.Query().Get("platform")
		scopeID := r.URL.Query().Get("scope_id")
		count, err := runtime.Agent.AngelMemoryCount(r.Context(), platform, scopeID)
		if err != nil {
			writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"platform": platform, "scope_id": scopeID, "count": count})
	})
	learningAdminProvider.Set(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		platform := r.URL.Query().Get("platform")
		scopeID := r.URL.Query().Get("scope_id")
		pending, approved, err := runtime.Agent.SelfLearningStats(r.Context(), platform, scopeID)
		if err != nil {
			writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"platform": platform, "scope_id": scopeID, "pending": pending, "approved": approved})
	})
	if healthState != nil {
		runtime.Handler = healthHandler{inner: runtime.Handler, state: healthState}
		healthState.SetReady(true)
	}
	startupDuration := profiler.Flush()
	foundation.Logger.Info("elbot startup completed", "startup_duration", startupDuration.String())
	var afterStart func(context.Context)
	if shouldStartCron(mode) && foundation.StartCron != nil {
		afterStart = func(ctx context.Context) {
			foundation.StartCron(ctx, runtime.CronService)
		}
	}
	var heartbeat func()
	if healthState != nil {
		heartbeat = healthState.Beat
	}
	return r.deps.Executor.Run(ctx, PlatformRunRequest{
		Handler:    runtime.Handler,
		Logger:     foundation.Logger,
		Runtimes:   platforms.Runtimes,
		AfterStart: afterStart,
		Heartbeat:  heartbeat,
	})
}
