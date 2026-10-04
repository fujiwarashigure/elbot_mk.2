package qqonebot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewFromPlatformConfigReadsAccessTokenFromDotEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("QQONEBOT_TOKEN=from-dotenv\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("QQONEBOT_TOKEN", "")

	adapter, err := NewFromPlatformConfig(map[string]any{
		"enabled":          true,
		"access_token_env": "QQONEBOT_TOKEN",
	}, nil, nil, nil, nil, nil, dir, "", 0, 0)
	if err != nil {
		t.Fatalf("NewFromPlatformConfig: %v", err)
	}
	if got, want := adapter.cfg.AccessToken, "from-dotenv"; got != want {
		t.Fatalf("access token = %q, want %q", got, want)
	}
}

func TestNewFromPlatformConfigPrefersSystemEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("QQONEBOT_TOKEN=from-dotenv\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("QQONEBOT_TOKEN", "from-environment")

	adapter, err := NewFromPlatformConfig(map[string]any{
		"enabled":          true,
		"access_token_env": "QQONEBOT_TOKEN",
	}, nil, nil, nil, nil, nil, dir, "", 0, 0)
	if err != nil {
		t.Fatalf("NewFromPlatformConfig: %v", err)
	}
	if got, want := adapter.cfg.AccessToken, "from-environment"; got != want {
		t.Fatalf("access token = %q, want %q", got, want)
	}
}

func TestNewFromPlatformConfigPrefersLegacyAccessToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("QQONEBOT_TOKEN=from-dotenv\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("QQONEBOT_TOKEN", "from-environment")

	adapter, err := NewFromPlatformConfig(map[string]any{
		"enabled":          true,
		"access_token":     "legacy-token",
		"access_token_env": "QQONEBOT_TOKEN",
	}, nil, nil, nil, nil, nil, dir, "", 0, 0)
	if err != nil {
		t.Fatalf("NewFromPlatformConfig: %v", err)
	}
	if got, want := adapter.cfg.AccessToken, "legacy-token"; got != want {
		t.Fatalf("access token = %q, want %q", got, want)
	}
}

func TestNewFromPlatformConfigAllowsMissingAccessToken(t *testing.T) {
	adapter, err := NewFromPlatformConfig(map[string]any{"enabled": true}, nil, nil, nil, nil, nil, t.TempDir(), "", 0, 0)
	if err != nil {
		t.Fatalf("NewFromPlatformConfig: %v", err)
	}
	if adapter.cfg.AccessToken != "" {
		t.Fatalf("access token = %q, want empty", adapter.cfg.AccessToken)
	}
}
func TestNewFromPlatformConfigAppliesInboundDefaults(t *testing.T) {
	adapter, err := NewFromPlatformConfig(map[string]any{
		"enabled":                   true,
		"inbound_dedup_ttl_seconds": 120,
	}, nil, nil, nil, nil, nil, t.TempDir(), "", 0, 0)
	if err != nil {
		t.Fatalf("NewFromPlatformConfig: %v", err)
	}
	if adapter.cfg.InboundDedupEnabled == nil || !*adapter.cfg.InboundDedupEnabled {
		t.Fatal("inbound dedup should default to enabled")
	}
	if adapter.cfg.PreprocessWorkers <= 0 || adapter.cfg.PreprocessQueueSize <= 0 {
		t.Fatalf("preprocess defaults = %d/%d", adapter.cfg.PreprocessWorkers, adapter.cfg.PreprocessQueueSize)
	}
	if adapter.inbound == nil || adapter.inbound.dedup == nil {
		t.Fatal("inbound runtime should be initialized")
	}
	if got := adapter.inbound.dedup.ttl; got != 2*time.Minute {
		t.Fatalf("dedup ttl = %s", got)
	}
}

func TestNewFromPlatformConfigCanDisableInboundDedup(t *testing.T) {
	adapter, err := NewFromPlatformConfig(map[string]any{
		"enabled":               true,
		"inbound_dedup_enabled": false,
	}, nil, nil, nil, nil, nil, t.TempDir(), "", 0, 0)
	if err != nil {
		t.Fatalf("NewFromPlatformConfig: %v", err)
	}
	if adapter.inbound == nil {
		t.Fatal("inbound runtime should exist")
	}
	if adapter.inbound.dedup != nil {
		t.Fatal("dedup should be disabled")
	}
	duplicate, _ := adapter.beginInboundJob("k")
	if duplicate {
		t.Fatal("disabled dedup must not suppress")
	}
}
