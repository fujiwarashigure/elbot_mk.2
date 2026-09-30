// Package diskguard provides tiered disk usage checks for non-essential media writes.
package diskguard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"elbot/internal/sysinfo"
)

// Level is a coarse disk health level.
type Level string

const (
	LevelOK       Level = "ok"
	LevelWarn     Level = "warn"
	LevelCritical Level = "critical"
)

// Config controls thresholds. Ratios are used-space ratios (0.85 = 85% used).
type Config struct {
	WarnRatio     float64
	CriticalRatio float64
	MinFreeBytes  uint64
	CacheTTL      time.Duration
}

// Guard caches disk checks for a short period to avoid stat storms.
type Guard struct {
	mu      sync.Mutex
	cfg     Config
	root    string
	level   Level
	checked time.Time
	err     error
}

// New creates a disk guard.
func New(root string, cfg Config) *Guard {
	if cfg.WarnRatio <= 0 || cfg.WarnRatio >= 1 {
		cfg.WarnRatio = 0.85
	}
	if cfg.CriticalRatio <= 0 || cfg.CriticalRatio >= 1 {
		cfg.CriticalRatio = 0.95
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 5 * time.Second
	}
	return &Guard{root: root, cfg: cfg}
}

// Check returns the current disk level. When critical is true, callers should
// reject non-essential media writes while allowing SQLite/config writes.
func (g *Guard) Check(ctx context.Context) (Level, bool, error) {
	if g == nil {
		return LevelOK, false, nil
	}
	if err := ctx.Err(); err != nil {
		return LevelOK, false, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.checked) < g.cfg.CacheTTL {
		return g.level, g.level == LevelCritical, g.err
	}
	total, free, err := sysinfo.DiskUsage(g.root)
	if err != nil {
		g.level = LevelOK
		g.checked = time.Now()
		g.err = err
		return g.level, false, err
	}
	level := LevelOK
	if total > 0 {
		used := 1 - float64(free)/float64(total)
		if g.cfg.MinFreeBytes > 0 && free < g.cfg.MinFreeBytes {
			level = LevelCritical
		} else if used >= g.cfg.CriticalRatio {
			level = LevelCritical
		} else if used >= g.cfg.WarnRatio {
			level = LevelWarn
		}
	}
	g.level = level
	g.checked = time.Now()
	g.err = nil
	return level, level == LevelCritical, nil
}

// Error formats a critical disk error for callers.
func (g *Guard) Error(level Level) error {
	if level != LevelCritical {
		return nil
	}
	return fmt.Errorf("disk space is critically low; refusing non-essential media writes")
}
