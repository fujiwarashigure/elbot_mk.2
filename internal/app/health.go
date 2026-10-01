package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/health"
)

const (
	defaultHealthLiveStaleSeconds = 90
	healthAddrEnv                 = "ELBOT_HEALTH_ADDR"
	healthLiveStaleEnv            = "ELBOT_HEALTH_LIVE_STALE_SECONDS"
	healthOpsTokenEnv             = "ELBOT_OPS_TOKEN"
	healthRestartReasonEnv        = "ELBOT_RESTART_REASON_FILE"
	healthRestartReasonValueEnv   = "ELBOT_LAST_RESTART_REASON"
)

func startHealthServer(cfg *config.Config, logger *slog.Logger, version string, extraHandlers map[string]http.Handler) (*health.State, *health.Server, error) {
	if cfg == nil {
		return nil, nil, nil
	}
	configDir := filepath.Dir(cfg.ConfigPath)
	addr, ok, err := config.ConfigEnv(healthAddrEnv, configDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", healthAddrEnv, err)
	}
	addr = strings.TrimSpace(addr)
	if !ok || addr == "" {
		return nil, nil, nil
	}

	liveStale := defaultHealthLiveStaleSeconds * time.Second
	if raw, staleOK, staleErr := config.ConfigEnv(healthLiveStaleEnv, configDir); staleErr != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", healthLiveStaleEnv, staleErr)
	} else if staleOK && strings.TrimSpace(raw) != "" {
		seconds, parseErr := strconv.Atoi(strings.TrimSpace(raw))
		if parseErr != nil || seconds <= 0 {
			return nil, nil, fmt.Errorf("%s must be a positive integer, got %q", healthLiveStaleEnv, raw)
		}
		liveStale = time.Duration(seconds) * time.Second
	}

	opsToken, _, tokenErr := config.ConfigEnv(healthOpsTokenEnv, configDir)
	if tokenErr != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", healthOpsTokenEnv, tokenErr)
	}
	if strings.TrimSpace(opsToken) == "" && !isLoopbackHealthAddr(addr) && logger != nil {
		logger.Warn("health server is reachable on a non-loopback address without ELBOT_OPS_TOKEN; protect /tasks and /metrics before exposing them through a reverse proxy", "addr", addr)
	}

	state := health.NewState(health.Options{Version: version, LiveStale: liveStale})
	server, err := health.NewServer(health.ServerOptions{
		Addr:              addr,
		State:             state,
		ExtraHandlerToken: strings.TrimSpace(opsToken),
		Logger:            logger,
		ExtraHandlers:     extraHandlers,
		Checkers: []health.Checker{
			health.DirWritable{CheckName: "sessions_sqlite_dir", Dir: filepath.Dir(cfg.Storage.SessionsSQLitePath)},
			health.DirWritable{CheckName: "chat_history_sqlite_dir", Dir: filepath.Dir(cfg.Storage.ChatHistorySQLitePath)},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create health server: %w", err)
	}
	if err := server.Start(); err != nil {
		return nil, nil, err
	}
	if logger != nil {
		logger.Info("health endpoints started", "addr", addr, "live_stale", liveStale.String())
	}
	return state, server, nil
}

func applyRestartReason(state *health.State) {
	if state == nil {
		return
	}
	if value := strings.TrimSpace(os.Getenv(healthRestartReasonValueEnv)); value != "" {
		state.SetLastRestartReason(value)
		return
	}
	path := strings.TrimSpace(os.Getenv(healthRestartReasonEnv))
	if path == "" {
		if runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
			path = filepath.Join(runtimeDir, "elbot", "last_restart_reason")
		}
	}
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if value := strings.TrimSpace(string(data)); value != "" {
		state.SetLastRestartReason(value)
	}
}

func isLoopbackHealthAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func closeHealthServer(ctx context.Context, server *health.Server) error {
	if server == nil {
		return nil
	}
	return server.Close(ctx)
}
