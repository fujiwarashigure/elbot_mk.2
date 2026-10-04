package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/command"
	"elbot/internal/security"
)

// StateModule exposes state.toml hot reload to operators.
type StateModule struct{}

func (StateModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return registrar.Register(stateCommand{deps: deps})
}

type stateCommand struct{ deps Deps }

func (c stateCommand) Info() command.Info {
	return command.Info{
		Name:        "state",
		Usage:       "/state | /state reload",
		Description: "查看运行时状态文件 state.toml 的加载状态，或立即重新加载外部修改。",
		MinRole:     security.RoleSuperadmin,
		Help: strings.TrimSpace(`Usage:
  /state          # 查看 state.toml 路径、已加载时间和是否有未生效的外部修改
  /state reload   # 立即重新读取 state.toml 并应用

说明:
  进程每 15 秒自动检测一次 state.toml 的外部修改并生效，无需重启；
  热加载覆盖 mode_models / compact_model / naming_model / context_overflow /
  group_policy / group_knowledge / group_services / group_runtime。
  budget 额度账本由运行中的进程独占，只在进程启动时从文件恢复。`),
	}
}

func (c stateCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	if c.deps.RuntimeState == nil {
		return &command.Result{Content: "运行时状态热加载未配置。"}, nil
	}
	fields := strings.Fields(req.Args)
	action := "status"
	if len(fields) > 0 {
		action = strings.ToLower(strings.TrimSpace(fields[0]))
	}
	switch action {
	case "", "status", "show":
		return &command.Result{Content: formatRuntimeStateStatus(c.deps.RuntimeState.RuntimeStateStatus())}, nil
	case "reload":
		report, err := c.deps.RuntimeState.ReloadRuntimeState(ctx)
		if err != nil {
			return &command.Result{Content: fmt.Sprintf("重新加载 state.toml 失败：%v", err)}, nil
		}
		commandAudit(c.deps, "runtime_state_reload_command", "path", report.Path, "sections", strings.Join(report.Changed, ","), "applied", report.Applied)
		return &command.Result{Content: formatRuntimeStateReload(report)}, nil
	default:
		return &command.Result{Content: "用法：/state | /state reload"}, nil
	}
}

func (c stateCommand) Complete(ctx context.Context, req command.CompletionRequest) []command.Completion {
	_ = ctx
	token := currentCompletionToken(req)
	fields := strings.Fields(req.Args)
	if len(fields) == 0 || isFirstArg(req, token) {
		return completeStaticOptions([]completionOption{
			{Text: "status", Description: "查看 state.toml 加载状态"},
			{Text: "reload", Description: "立即重新加载 state.toml"},
		}, token.Text, token.Start, token.End, "state_action")
	}
	return nil
}

func formatRuntimeStateStatus(status RuntimeStateStatus) string {
	if strings.TrimSpace(status.Path) == "" {
		return "未配置 state.toml（运行时状态不会持久化）。"
	}
	lines := []string{"运行时状态文件：" + status.Path}
	if status.LoadedModTime.IsZero() {
		lines = append(lines, "已加载：尚未加载")
	} else {
		lines = append(lines, "已加载："+status.LoadedModTime.Format("2006-01-02 15:04:05"))
	}
	if !status.FileModTime.IsZero() {
		lines = append(lines, "文件修改时间："+status.FileModTime.Format("2006-01-02 15:04:05"))
	}
	if status.Pending {
		lines = append(lines, "存在未生效的外部修改：可用 /state reload 立即生效（进程也会在 15 秒内自动生效）")
	} else {
		lines = append(lines, "当前内存状态与文件一致。")
	}
	return strings.Join(lines, "\n")
}

func formatRuntimeStateReload(report RuntimeStateReloadReport) string {
	if !report.Applied {
		return "state.toml 未加载（未配置或文件不存在）。"
	}
	when := ""
	if !report.ModTime.IsZero() {
		when = "（" + report.ModTime.Format("2006-01-02 15:04:05") + "）"
	}
	if len(report.Changed) == 0 {
		return "state.toml 已重新加载" + when + "，运行状态无变化。"
	}
	return fmt.Sprintf("state.toml 已重新加载%s，生效内容：%s。", when, strings.Join(report.Changed, "、"))
}
