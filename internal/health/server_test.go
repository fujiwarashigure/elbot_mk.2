package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerLiveReadyAndDegraded(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state := NewState(Options{LiveStale: 10 * time.Second, Now: func() time.Time { return now }})
	server, err := NewServer(ServerOptions{Addr: "127.0.0.1:0", State: state})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// /live only means the process is running; readiness and scheduler health
	// are separate concerns.
	assertStatus(t, handler, "/live", http.StatusOK, "live")
	assertStatus(t, handler, "/ready", http.StatusServiceUnavailable, "not_ready")

	state.Beat()
	state.SetReady(true)
	state.ExpectPlatform("qqonebot")
	state.MarkPlatformConnected("qqonebot")
	assertStatus(t, handler, "/live", http.StatusOK, "live")
	assertStatus(t, handler, "/ready", http.StatusOK, "ready")
	assertStatus(t, handler, "/healthz", http.StatusOK, "ok")

	state.RecordModelError("deepseek", fmt.Errorf("upstream timeout"))
	assertStatus(t, handler, "/ready", http.StatusOK, "ready")
	assertStatus(t, handler, "/healthz", http.StatusOK, "degraded")

	state.MarkPlatformDisconnected("qqonebot", fmt.Errorf("closed"))
	assertStatus(t, handler, "/ready", http.StatusOK, "ready")
	assertStatus(t, handler, "/healthz", http.StatusOK, "degraded")
}

func TestServerReadyCheckerFailure(t *testing.T) {
	state := NewState(Options{})
	state.Beat()
	state.SetReady(true)
	server, err := NewServer(ServerOptions{
		Addr:  "127.0.0.1:0",
		State: state,
		Checkers: []Checker{
			CheckerFunc{CheckName: "disk", Fn: func(context.Context) error { return fmt.Errorf("disk full") }},
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body readyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Ready || body.Checks["disk"] != "disk full" {
		t.Fatalf("ready response = %#v", body)
	}
}

func TestServerExtraHandlers(t *testing.T) {
	state := NewState(Options{})
	server, err := NewServer(ServerOptions{
		Addr:  "127.0.0.1:0",
		State: state,
		ExtraHandlers: map[string]http.Handler{
			"/tasks": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}),
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestServerExtraHandlersRequireToken(t *testing.T) {
	state := NewState(Options{})
	server, err := NewServer(ServerOptions{
		Addr:  "127.0.0.1:0",
		State: state,
		ExtraHandlers: map[string]http.Handler{
			"/metrics": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}),
		},
		ExtraHandlerToken: "ops-secret",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer ops-secret")
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want %d, body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestServerRejectsNonGet(t *testing.T) {
	state := NewState(Options{})
	server, err := NewServer(ServerOptions{Addr: "127.0.0.1:0", State: state})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/live", nil)
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func assertStatus(t *testing.T, handler http.Handler, path string, want int, wantStatus string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	handler.ServeHTTP(recorder, req)
	if recorder.Code != want {
		t.Fatalf("%s status = %d, want %d, body = %s", path, recorder.Code, want, recorder.Body.String())
	}
	if wantStatus == "" {
		return
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s decode body: %v", path, err)
	}
	if body["status"] != wantStatus {
		t.Fatalf("%s status field = %#v, want %q", path, body["status"], wantStatus)
	}
}
