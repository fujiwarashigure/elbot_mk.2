package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"elbot/internal/config"
)

func TestStartHealthServerUsesEnvironment(t *testing.T) {
	t.Setenv(healthAddrEnv, "127.0.0.1:0")
	t.Setenv(healthLiveStaleEnv, "30")

	dataDir := t.TempDir()
	configDir := t.TempDir()
	cfg := &config.Config{
		ConfigPath: filepath.Join(configDir, "app.toml"),
		Storage: config.StorageConfig{
			SessionsSQLitePath:    filepath.Join(dataDir, "sessions.db"),
			ChatHistorySQLitePath: filepath.Join(dataDir, "history.db"),
		},
	}
	state, server, err := startHealthServer(cfg, nil, "test-version", nil)
	if err != nil {
		t.Fatalf("startHealthServer: %v", err)
	}
	if state == nil || server == nil {
		t.Fatalf("state/server = %#v/%#v", state, server)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	state.Beat()
	if !state.IsLive() {
		t.Fatal("state should be live after Beat")
	}
}

func TestStartHealthServerRejectsInvalidStaleValue(t *testing.T) {
	t.Setenv(healthAddrEnv, "127.0.0.1:0")
	t.Setenv(healthLiveStaleEnv, "not-a-number")
	_, _, err := startHealthServer(&config.Config{ConfigPath: filepath.Join(t.TempDir(), "app.toml")}, nil, "test", nil)
	if err == nil {
		t.Fatal("startHealthServer() error = nil")
	}
}
func TestStartHealthServerProtectsExtraHandlersWithOpsToken(t *testing.T) {
	t.Setenv(healthAddrEnv, "127.0.0.1:0")
	t.Setenv(healthOpsTokenEnv, "ops-secret")

	dataDir := t.TempDir()
	cfg := &config.Config{
		ConfigPath: filepath.Join(t.TempDir(), "app.toml"),
		Storage: config.StorageConfig{
			SessionsSQLitePath:    filepath.Join(dataDir, "sessions.db"),
			ChatHistorySQLitePath: filepath.Join(dataDir, "history.db"),
		},
	}
	_, server, err := startHealthServer(cfg, nil, "test-version", map[string]http.Handler{
		"/tasks": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	})
	if err != nil {
		t.Fatalf("startHealthServer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /tasks status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Authorization", "Bearer ops-secret")
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated /tasks status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestStartHealthServerDisablesExtraHandlersWithoutToken(t *testing.T) {
	t.Setenv(healthAddrEnv, "127.0.0.1:0")
	t.Setenv(healthOpsTokenEnv, "")
	t.Setenv(healthAllowUnauthenticatedEnv, "")

	dataDir := t.TempDir()
	cfg := &config.Config{
		ConfigPath: filepath.Join(t.TempDir(), "app.toml"),
		Storage: config.StorageConfig{
			SessionsSQLitePath:    filepath.Join(dataDir, "sessions.db"),
			ChatHistorySQLitePath: filepath.Join(dataDir, "history.db"),
		},
	}
	_, server, err := startHealthServer(cfg, nil, "test-version", map[string]http.Handler{
		"/tasks": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	})
	if err != nil {
		t.Fatalf("startHealthServer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})

	for _, path := range []string{"/tasks", "/healthz"} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		server.Handler().ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("unauthenticated %s status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}

func TestStartHealthServerAllowsHealthzWhenExplicitlyUnauthenticated(t *testing.T) {
	t.Setenv(healthAddrEnv, "127.0.0.1:0")
	t.Setenv(healthOpsTokenEnv, "")
	t.Setenv(healthAllowUnauthenticatedEnv, "1")

	dataDir := t.TempDir()
	cfg := &config.Config{
		ConfigPath: filepath.Join(t.TempDir(), "app.toml"),
		Storage: config.StorageConfig{
			SessionsSQLitePath:    filepath.Join(dataDir, "sessions.db"),
			ChatHistorySQLitePath: filepath.Join(dataDir, "history.db"),
		},
	}
	state, server, err := startHealthServer(cfg, nil, "test-version", nil)
	if err != nil {
		t.Fatalf("startHealthServer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	state.Beat()
	state.SetReady(true)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("explicit unauthenticated /healthz status = %d, want %d, body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestStartHealthServerProtectsHealthzWithOpsToken(t *testing.T) {
	t.Setenv(healthAddrEnv, "127.0.0.1:0")
	t.Setenv(healthOpsTokenEnv, "ops-secret")

	dataDir := t.TempDir()
	cfg := &config.Config{
		ConfigPath: filepath.Join(t.TempDir(), "app.toml"),
		Storage: config.StorageConfig{
			SessionsSQLitePath:    filepath.Join(dataDir, "sessions.db"),
			ChatHistorySQLitePath: filepath.Join(dataDir, "history.db"),
		},
	}
	state, server, err := startHealthServer(cfg, nil, "test-version", nil)
	if err != nil {
		t.Fatalf("startHealthServer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	state.Beat()
	state.SetReady(true)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /healthz status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Authorization", "Bearer ops-secret")
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated /healthz status = %d, want %d, body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}
