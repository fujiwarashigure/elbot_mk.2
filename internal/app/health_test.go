package app

import (
	"context"
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
