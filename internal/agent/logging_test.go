package agent

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"elbot/internal/logging"
)

type fakeLogManager struct {
	runtime *slog.Logger
	audit   *slog.Logger
	dir     string
}

func (m fakeLogManager) Runtime() *slog.Logger { return m.runtime }
func (m fakeLogManager) Audit() *slog.Logger   { return m.audit }
func (m fakeLogManager) LogDir() string        { return m.dir }

func TestAuditRecordsCarryModuleSource(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	a := &Agent{}
	a.SetLogManager(fakeLogManager{runtime: logger, audit: logger})

	a.audit("permission_denied", "actor_id", "u1", "reason", "slash_command_requires_superadmin", "result", logging.ResultRejected)

	text := output.String()
	if !strings.Contains(text, "module="+logging.ModuleAgent) {
		t.Fatalf("audit record has no module source: %s", text)
	}
	if !strings.Contains(text, "event=permission_denied") || !strings.Contains(text, "result="+logging.ResultRejected) {
		t.Fatalf("audit record lost event or result: %s", text)
	}
}

func TestAuditLogWithoutLoggerIsNoop(t *testing.T) {
	a := &Agent{}
	a.audit("permission_denied", "reason", "no logger")
	a.auditError("message_error", "error", "boom")
}

func TestNormalizeAuditAttrsConvertsErrorsToStrings(t *testing.T) {
	err := errors.New("upstream connection reset")
	attrs := normalizeAuditAttrs([]any{"event", "llm_error", "error", err, "count", 3})
	if _, isError := attrs[3].(error); isError {
		t.Fatalf("error attribute was not converted: %#v", attrs[3])
	}
	text, ok := attrs[3].(string)
	if !ok || !strings.Contains(text, "upstream connection reset") {
		t.Fatalf("converted attribute = %#v", attrs[3])
	}
	if attrs[5] != 3 {
		t.Fatalf("non-error attribute changed: %#v", attrs[5])
	}
}
