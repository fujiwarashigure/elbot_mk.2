package agent

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	agentcommands "elbot/internal/agent/commands"
	"elbot/internal/delivery"
	"elbot/internal/logging"
)

// runtimeStateWatchEvery bounds how long an external state.toml edit can stay
// unnoticed without the operator running /state reload. It only costs one stat
// per tick when nothing changed.
const runtimeStateWatchEvery = 15 * time.Second

// runtimeStateReload is the result of one state.toml refresh.
type runtimeStateReload struct {
	Path string
	// Applied is true when the file was read and merged into memory.
	Applied bool
	// Changed lists the sections whose effective value differs from before.
	Changed []string
	// ModTime is the file mtime that was applied.
	ModTime time.Time
}

// StartRuntimeStateWatch polls state.toml so an edit made outside the process
// takes effect without a restart. It is safe to call more than once and exits
// when ctx is done.
func (a *Agent) StartRuntimeStateWatch(ctx context.Context) {
	if a == nil || a.statePath == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.stateWatchMu.Lock()
	if a.stateWatchStarted {
		a.stateWatchMu.Unlock()
		return
	}
	a.stateWatchStarted = true
	a.stateWatchMu.Unlock()

	go func() {
		ticker := time.NewTicker(runtimeStateWatchEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.pollRuntimeState(ctx)
			}
		}
	}()
}

func (a *Agent) pollRuntimeState(ctx context.Context) {
	reload, err := a.refreshRuntimeState(false)
	if err != nil {
		if a.logger != nil {
			a.logger.Warn("reload externally edited state config", "path", a.statePath, "error", err.Error())
		}
		return
	}
	if !reload.Applied || len(reload.Changed) == 0 {
		return
	}
	a.audit("runtime_state_reloaded", "path", reload.Path, "sections", strings.Join(reload.Changed, ","), "trigger", "watch", "result", logging.ResultSucceeded)
	_, _ = a.SendNotice(ctx, delivery.Notice{Outputs: []delivery.Output{delivery.Text(runtimeStateReloadText(reload))}, Level: slog.LevelInfo})
}

// ReloadRuntimeState applies state.toml immediately, ignoring the mtime gate, so
// an operator can force an external edit to take effect without waiting for the
// watcher or restarting the process.
func (a *Agent) ReloadRuntimeState(ctx context.Context) (agentcommands.RuntimeStateReloadReport, error) {
	_ = ctx
	reload, err := a.refreshRuntimeState(true)
	report := agentcommands.RuntimeStateReloadReport{Path: reload.Path, Applied: reload.Applied, Changed: reload.Changed, ModTime: reload.ModTime}
	if err != nil {
		return report, err
	}
	if reload.Applied && len(reload.Changed) > 0 {
		a.audit("runtime_state_reloaded", "path", reload.Path, "sections", strings.Join(reload.Changed, ","), "trigger", "command", "result", logging.ResultSucceeded)
	}
	return report, nil
}

// RuntimeStateStatus reports the loaded state file and whether the on-disk file
// is newer than what this process applied.
func (a *Agent) RuntimeStateStatus() agentcommands.RuntimeStateStatus {
	status := agentcommands.RuntimeStateStatus{}
	if a == nil || a.statePath == "" {
		return status
	}
	status.Path = a.statePath
	a.stateMu.Lock()
	status.LoadedModTime = a.stateModTime
	a.stateMu.Unlock()
	info, err := os.Stat(a.statePath)
	if err != nil {
		return status
	}
	status.FileModTime = info.ModTime()
	status.Pending = status.LoadedModTime.IsZero() || info.ModTime().After(status.LoadedModTime)
	return status
}

func runtimeStateReloadText(reload runtimeStateReload) string {
	sections := strings.Join(reload.Changed, "、")
	if sections == "" {
		sections = "（无变化）"
	}
	text := "state.toml 已被外部修改并生效：" + sections
	if !reload.ModTime.IsZero() {
		text += "（" + reload.ModTime.Format("2006-01-02 15:04:05") + "）"
	}
	return text
}
