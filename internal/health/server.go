package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

const defaultReadHeaderTimeout = 5 * time.Second

// ServerOptions configures the independent health HTTP server.
type ServerOptions struct {
	Addr              string
	State             *State
	Checkers          []Checker
	ExtraHandlers     map[string]http.Handler
	Logger            *slog.Logger
	ReadHeaderTimeout time.Duration
}

// Server exposes /live, /ready and /healthz for internal or loopback access.
type Server struct {
	addr     string
	state    *State
	checkers []Checker
	logger   *slog.Logger
	server   *http.Server
	listener net.Listener
}

// NewServer creates a health server without binding a port.
func NewServer(opts ServerOptions) (*Server, error) {
	addr := strings.TrimSpace(opts.Addr)
	if addr == "" {
		return nil, fmt.Errorf("health server address is empty")
	}
	if opts.State == nil {
		return nil, fmt.Errorf("health server state is nil")
	}
	readHeaderTimeout := opts.ReadHeaderTimeout
	if readHeaderTimeout <= 0 {
		readHeaderTimeout = defaultReadHeaderTimeout
	}
	s := &Server{
		addr:     addr,
		state:    opts.State,
		checkers: opts.Checkers,
		logger:   opts.Logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/live", s.handleLive)
	mux.HandleFunc("/ready", s.handleReady)
	mux.HandleFunc("/healthz", s.handleHealthz)
	for path, handler := range opts.ExtraHandlers {
		if handler == nil {
			continue
		}
		if !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("health extra handler path must start with /: %q", path)
		}
		mux.Handle(path, handler)
	}
	s.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	return s, nil
}

// Start binds the configured address and starts serving in a goroutine.
func (s *Server) Start() error {
	if s == nil || s.server == nil {
		return fmt.Errorf("health server is nil")
	}
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen health endpoint %s: %w", s.addr, err)
	}
	s.listener = listener
	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if s.logger != nil {
				s.logger.Error("health server stopped with error", "error", err.Error())
			}
		}
	}()
	return nil
}

// Close gracefully shuts down the health server.
func (s *Server) Close(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

// Handler returns the HTTP handler for tests.
func (s *Server) Handler() http.Handler {
	if s == nil || s.server == nil {
		return http.NotFoundHandler()
	}
	return s.server.Handler
}

type liveResponse struct {
	Status        string    `json:"status"`
	Version       string    `json:"version,omitempty"`
	UptimeSeconds int64     `json:"uptime_seconds"`
	LastHeartbeat time.Time `json:"last_heartbeat,omitempty"`
}

type readyResponse struct {
	Status string            `json:"status"`
	Ready  bool              `json:"ready"`
	Checks map[string]string `json:"checks,omitempty"`
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	snapshot := s.state.Snapshot()
	response := liveResponse{
		Status:        "live",
		Version:       snapshot.Version,
		UptimeSeconds: snapshot.UptimeSeconds,
		LastHeartbeat: snapshot.LastHeartbeat,
	}
	status := http.StatusOK
	if !snapshot.Live {
		response.Status = "not_live"
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, response)
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	ready, checks := s.evaluateReady(r.Context())
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	response := readyResponse{
		Status: "ready",
		Ready:  ready,
		Checks: checks,
	}
	if !ready {
		response.Status = "not_ready"
	}
	writeJSON(w, status, response)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	snapshot := s.snapshot(r.Context())
	status := http.StatusOK
	if snapshot.Status == "not_live" || snapshot.Status == "not_ready" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, snapshot)
}

func (s *Server) snapshot(ctx context.Context) Snapshot {
	snapshot := s.state.Snapshot()
	ready, checks := s.evaluateReady(ctx)
	snapshot.Ready = ready
	if snapshot.Checks == nil {
		snapshot.Checks = map[string]string{}
	}
	for name, value := range checks {
		snapshot.Checks[name] = value
	}
	switch {
	case !snapshot.Live:
		snapshot.Status = "not_live"
	case !ready:
		snapshot.Status = "not_ready"
	case snapshot.Degraded:
		snapshot.Status = "degraded"
	default:
		snapshot.Status = "ok"
	}
	return snapshot
}

func (s *Server) evaluateReady(ctx context.Context) (bool, map[string]string) {
	checks := map[string]string{}
	ready := true
	if s.state.ShuttingDown() {
		ready = false
		checks["process"] = "shutting down"
	} else if !s.state.IsReady() {
		ready = false
		checks["process"] = "not ready"
	} else {
		checks["process"] = "ready"
	}
	for _, checker := range s.checkers {
		if checker == nil {
			continue
		}
		name := checker.Name()
		if err := checker.Check(ctx); err != nil {
			ready = false
			checks[name] = err.Error()
			continue
		}
		checks[name] = "ok"
	}
	platformsReady, platformChecks := s.state.PlatformReadiness()
	if !platformsReady {
		ready = false
	}
	for name, value := range platformChecks {
		checks[name] = value
	}
	return ready, checks
}

func allowGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
