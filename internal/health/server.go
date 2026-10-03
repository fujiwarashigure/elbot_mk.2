package health

import (
	"context"
	"crypto/subtle"
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
	Addr          string
	State         *State
	Checkers      []Checker
	ExtraHandlers map[string]http.Handler
	// ExtraHandlerToken protects /tasks, /metrics, /diagnostics, any other
	// extra handler, and /healthz. Empty means those endpoints are disabled
	// unless AllowUnauthenticatedExtraHandlers is explicitly true.
	ExtraHandlerToken string
	// AllowUnauthenticatedExtraHandlers is an explicit opt-in for exposing
	// sensitive ops handlers without a token. It should only be used for
	// trusted loopback-only diagnostics.
	AllowUnauthenticatedExtraHandlers bool
	Logger                            *slog.Logger
	ReadHeaderTimeout                 time.Duration
}

// Server exposes /live and /ready for internal or loopback access. /healthz is
// only registered when an ops token protects it or the caller explicitly opts
// into unauthenticated extra handlers.
type Server struct {
	addr              string
	state             *State
	checkers          []Checker
	extraHandlerToken string
	logger            *slog.Logger
	server            *http.Server
	listener          net.Listener
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
		addr:              addr,
		state:             opts.State,
		checkers:          opts.Checkers,
		extraHandlerToken: strings.TrimSpace(opts.ExtraHandlerToken),
		logger:            opts.Logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/live", s.handleLive)
	mux.HandleFunc("/ready", s.handleReady)
	healthzHandler := http.Handler(http.HandlerFunc(s.handleHealthz))
	switch {
	case s.extraHandlerToken != "":
		mux.Handle("/healthz", s.requireExtraHandlerToken(healthzHandler))
	case opts.AllowUnauthenticatedExtraHandlers:
		mux.Handle("/healthz", healthzHandler)
	default:
		// Secure default: without a token the detailed snapshot stays
		// unregistered, so only /live and /ready are exposed. This matches the
		// documented behaviour instead of silently leaking checker errors.
	}
	for path, handler := range opts.ExtraHandlers {
		if handler == nil {
			continue
		}
		if !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("health extra handler path must start with /: %q", path)
		}
		if s.extraHandlerToken != "" {
			handler = s.requireExtraHandlerToken(handler)
		} else if !opts.AllowUnauthenticatedExtraHandlers {
			// Secure default: a missing token disables the sensitive ops
			// handlers instead of exposing them silently.
			continue
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
	if s.state.SchedulerKnown() {
		if s.state.SchedulerLive() {
			checks["scheduler"] = "ok"
		} else {
			ready = false
			checks["scheduler"] = "heartbeat stale"
		}
	} else {
		checks["scheduler"] = "not started"
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
	return ready, checks
}

func (s *Server) requireExtraHandlerToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			token = strings.TrimSpace(r.Header.Get("X-Elbot-Ops-Token"))
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.extraHandlerToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
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
