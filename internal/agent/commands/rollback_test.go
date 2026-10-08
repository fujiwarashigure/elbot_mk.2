package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/command"
	"elbot/internal/security"
	"elbot/internal/session"
	"elbot/internal/utils/fileops"
)

func TestRollbackCommandListsAndRestoresOneFile(t *testing.T) {
	ctx := context.Background()
	store := newCommandTestStore(t)
	svc := session.NewService(store)
	scope := session.Scope{ActorID: "cli:local", Platform: "cli", PlatformScopeID: "local", IsCLI: true}
	current, err := svc.Create(ctx, scope, session.CreateRequest{Title: "current"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	path := filepath.Join(t.TempDir(), "a.txt")
	original := []byte("one\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	backups := fileops.NewRollbackStore()
	after := []byte("two\n")
	backups.Record(current.ID, path, original, 0o644, true, fileops.ContentRevision(original), fileops.ContentRevision(after))
	if err := os.WriteFile(path, after, 0o644); err != nil {
		t.Fatal(err)
	}

	deps := Deps{FileBackups: backups, Sessions: svc, Scope: func(context.Context) session.Scope { return scope }}
	cmd := NewRollback(deps)
	if cmd.Info().MinRole != "" {
		t.Fatalf("rollback must stay superadmin-only, MinRole = %q", cmd.Info().MinRole)
	}

	page, err := cmd.Handle(ctx, command.Request{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(page.Content, "1. "+path) || !strings.Contains(page.Content, "切换 Session") {
		t.Fatalf("list = %q", page.Content)
	}

	restored, err := cmd.Handle(ctx, command.Request{Args: "1"})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.Contains(restored.Content, "已回滚") {
		t.Fatalf("restore = %q", restored.Content)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(original) {
		t.Fatalf("content = %q, want %q", string(content), string(original))
	}

	// The record is consumed and unknown numbers are reported.
	if _, err := cmd.Handle(ctx, command.Request{Args: "1"}); err != nil {
		t.Fatalf("second restore: %v", err)
	}
	missing, err := cmd.Handle(ctx, command.Request{Args: "9"})
	if err != nil || !strings.Contains(missing.Content, "没有编号 9") {
		t.Fatalf("missing = %#v err=%v", missing, err)
	}
	if _, err := cmd.Handle(ctx, command.Request{Args: "abc"}); err != nil {
		t.Fatalf("invalid argument: %v", err)
	}
}

func TestRollbackCommandWithoutBackupsOrSessions(t *testing.T) {
	ctx := security.WithActor(context.Background(), security.Actor{ID: "cli:local", Platform: "cli", Role: security.RoleSuperadmin})
	empty := NewRollback(Deps{})
	result, err := empty.Handle(ctx, command.Request{})
	if err != nil || !strings.Contains(result.Content, "未启用") {
		t.Fatalf("result = %#v err=%v", result, err)
	}
	noSessions := NewRollback(Deps{FileBackups: fileops.NewRollbackStore()})
	result, err = noSessions.Handle(ctx, command.Request{})
	if err != nil || !strings.Contains(result.Content, "没有可用会话") {
		t.Fatalf("result = %#v err=%v", result, err)
	}
}
