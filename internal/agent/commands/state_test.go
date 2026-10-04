package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"elbot/internal/angelmemory"
	"elbot/internal/command"
	"elbot/internal/security"
	"elbot/internal/session"
)

type fakeRuntimeStateService struct {
	report RuntimeStateReloadReport
	err    error
	status RuntimeStateStatus
	calls  int
}

func (s *fakeRuntimeStateService) ReloadRuntimeState(context.Context) (RuntimeStateReloadReport, error) {
	s.calls++
	return s.report, s.err
}

func (s *fakeRuntimeStateService) RuntimeStateStatus() RuntimeStateStatus { return s.status }

func TestStateCommandStatusAndReload(t *testing.T) {
	service := &fakeRuntimeStateService{
		status: RuntimeStateStatus{Path: "/data/config/elbot/state.toml", LoadedModTime: time.Unix(1, 0), FileModTime: time.Unix(2, 0), Pending: true},
		report: RuntimeStateReloadReport{Path: "/data/config/elbot/state.toml", Applied: true, Changed: []string{"group_policy", "group_services"}},
	}
	deps := Deps{RuntimeState: service, Audit: func(string, ...any) {}}
	handler := stateCommand{deps: deps}

	status, err := handler.Handle(context.Background(), command.Request{Name: "state"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(status.Content, "state.toml") || !strings.Contains(status.Content, "未生效的外部修改") {
		t.Fatalf("status content = %q", status.Content)
	}

	reload, err := handler.Handle(context.Background(), command.Request{Name: "state", Args: "reload"})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if service.calls != 1 {
		t.Fatalf("reload calls = %d, want 1", service.calls)
	}
	if !strings.Contains(reload.Content, "group_policy") || !strings.Contains(reload.Content, "group_services") {
		t.Fatalf("reload content = %q", reload.Content)
	}

	failing := stateCommand{deps: Deps{RuntimeState: &fakeRuntimeStateService{err: errors.New("boom")}}}
	result, err := failing.Handle(context.Background(), command.Request{Name: "state", Args: "reload"})
	if err != nil {
		t.Fatalf("failing reload returned error: %v", err)
	}
	if !strings.Contains(result.Content, "失败") {
		t.Fatalf("failing reload content = %q", result.Content)
	}
}

func TestMemoryBackfillCommandPreviewAndApply(t *testing.T) {
	admin := security.Actor{ID: "qqonebot:root", Platform: "qqonebot", PlatformUserID: "root", Role: security.RoleSuperadmin}
	deps, service, _, _ := forgetTestDeps(t, admin)
	ctx := forgetTestContext(deps, admin)
	if _, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "旧记忆", "tool", angelmemory.Source{Label: "tool"}); err != nil {
		t.Fatalf("remember legacy: %v", err)
	}
	if _, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "新记忆", "", angelmemory.Source{Kind: "tool", ActorID: admin.ID, MessageID: "m-new"}); err != nil {
		t.Fatalf("remember linked: %v", err)
	}
	handler := memoryCommand{deps: deps}

	preview, err := handler.Handle(ctx, command.Request{Name: "memory", Args: "backfill"})
	if err != nil {
		t.Fatalf("backfill preview: %v", err)
	}
	if !strings.Contains(preview.Content, "尚未修改任何数据") || !strings.Contains(preview.Content, `source="tool"`) {
		t.Fatalf("preview content = %q", preview.Content)
	}
	if !strings.Contains(preview.Content, "--confirm") {
		t.Fatalf("preview should ask for confirmation: %q", preview.Content)
	}

	applied, err := handler.Handle(ctx, command.Request{Name: "memory", Args: "backfill --confirm"})
	if err != nil {
		t.Fatalf("backfill apply: %v", err)
	}
	if !strings.Contains(applied.Content, "已为 1 条旧记忆回填") {
		t.Fatalf("apply content = %q", applied.Content)
	}

	memories, err := service.List(ctx, "qqonebot", "group:1", angelmemory.SourceFilter{}, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, memory := range memories {
		if memory.Content == "旧记忆" && memory.SourceKind != "tool" {
			t.Fatalf("legacy memory kind = %q, want tool", memory.SourceKind)
		}
		if memory.Content == "旧记忆" && (memory.SourceActorID != "" || memory.SourceMessageID != "" || memory.SourceSessionID != "") {
			t.Fatalf("legacy provenance was guessed: %#v", memory)
		}
	}
}

func TestStateCommandUsesScopeIndependentDeps(t *testing.T) {
	deps := Deps{Scope: func(context.Context) session.Scope { return session.Scope{} }}
	handler := stateCommand{deps: deps}
	result, err := handler.Handle(context.Background(), command.Request{Name: "state"})
	if err != nil {
		t.Fatalf("status without service: %v", err)
	}
	if !strings.Contains(result.Content, "未配置") {
		t.Fatalf("content = %q", result.Content)
	}
}
