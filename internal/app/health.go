package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
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

	state := health.NewState(health.Options{Version: version, LiveStale: liveStale})
	server, err := health.NewServer(health.ServerOptions{
		Addr:          addr,
		State:         state,
		Logger:        logger,
		ExtraHandlers: extraHandlers,
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

func closeHealthServer(ctx context.Context, server *health.Server) error {
	if server == nil {
		return nil
	}
	return server.Close(ctx)
}
