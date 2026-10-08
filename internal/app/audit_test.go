package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"elbot/internal/logging"
)

// fakeLogManager 让 auditFunc 的输出可断言，避免测试依赖真实日志文件。
type fakeLogManager struct {
	runtime *slog.Logger
	audit   *slog.Logger
	elnis   *slog.Logger
	dir     string
}

func (m fakeLogManager) Runtime() *slog.Logger { return m.runtime }
func (m fakeLogManager) Audit() *slog.Logger   { return m.audit }
func (m fakeLogManager) Elnis() *slog.Logger   { return m.elnis }
func (m fakeLogManager) LogDir() string        { return m.dir }
func (m fakeLogManager) Close() error          { return nil }

// TestAuditFuncCarriesAppModule 固定 app 层审计来源契约：所有 app 审计记录（命名失败、
// cron、Elwisp、hook、平台连接）都经 auditFunc，必须带 module=app，否则按来源筛选时会
// 整类记录查不到。
func TestAuditFuncCarriesAppModule(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	audit := auditFunc(fakeLogManager{runtime: logger, audit: logger})

	audit("session_naming_failed", "session_id", "s1", "result", logging.ResultFailed)

	text := output.String()
	if !strings.Contains(text, "module="+logging.ModuleApp) {
		t.Fatalf("app 审计记录缺少 module 来源: %s", text)
	}
	if !strings.Contains(text, "event=session_naming_failed") || !strings.Contains(text, "result="+logging.ResultFailed) {
		t.Fatalf("app 审计记录丢了 event 或 result: %s", text)
	}
}

// TestAuditFuncKeepsCallerAttributes 确认调用方给的属性不会被来源标识挤掉，且顺序稳定。
func TestAuditFuncKeepsCallerAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	audit := auditFunc(fakeLogManager{runtime: logger, audit: logger})

	audit("hook_tool_call", "hook", "demo", "tool", "shell")

	text := output.String()
	for _, want := range []string{"event=hook_tool_call", "module=" + logging.ModuleApp, "hook=demo", "tool=shell"} {
		if !strings.Contains(text, want) {
			t.Fatalf("app 审计记录缺少 %q: %s", want, text)
		}
	}
}
