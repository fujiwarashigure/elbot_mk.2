package commands

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/command"
)

func TestPreviewContentTruncatesLongMessages(t *testing.T) {
	long := strings.Repeat("中😀", 101)
	got := previewContent(long)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("long preview must be marked as truncated: %q", got)
	}
	body := strings.TrimSuffix(got, "...")
	if runes := len([]rune(body)); runes != 200 {
		t.Fatalf("preview kept %d runes, want 200", runes)
	}
	if short := previewContent("短消息\n换行"); short != "短消息 换行" {
		t.Fatalf("short preview = %q", short)
	}
}

type doctorStub struct {
	text string
	err  error
}

func (d doctorStub) Doctor(context.Context) (string, error) { return d.text, d.err }

func TestDoctorCommandIsReadOnlyAndSuperadminOnly(t *testing.T) {
	cmd := NewDoctor(Deps{Doctor: doctorStub{text: "Everything is OK"}})
	if cmd.Info().MinRole != "" {
		t.Fatalf("doctor must stay superadmin-only, MinRole = %q", cmd.Info().MinRole)
	}
	result, err := cmd.Handle(context.Background(), command.Request{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Content != "Everything is OK" {
		t.Fatalf("result = %q", result.Content)
	}

	disabled, err := NewDoctor(Deps{}).Handle(context.Background(), command.Request{})
	if err != nil || !strings.Contains(disabled.Content, "未启用") {
		t.Fatalf("disabled result = %#v err=%v", disabled, err)
	}
}
