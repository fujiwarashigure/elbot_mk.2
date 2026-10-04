package commands

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/angelmemory"
	"elbot/internal/command"
	"elbot/internal/memory/resident"
	"elbot/internal/security"
	"elbot/internal/session"
)

func forgetTestDeps(t *testing.T, actor security.Actor) (Deps, *angelmemory.Service, *resident.Store, session.Scope) {
	t.Helper()
	ctx := context.Background()
	store, err := angelmemory.Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("open angel memory: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := angelmemory.NewService(store, angelmemory.Options{MaxWritesPerMinute: 100})
	residentStore := resident.NewStore(filepath.Join(t.TempDir(), "memories.toml"))
	scope := session.Scope{Platform: "qqonebot", PlatformScopeID: "group:1", ActorID: actor.ID}
	deps := Deps{
		AngelMemory:    service,
		ResidentMemory: residentStore,
		Scope:          func(context.Context) session.Scope { return scope },
		Audit:          func(string, ...any) {},
	}
	return deps, service, residentStore, scope
}

func forgetTestContext(deps Deps, actor security.Actor) context.Context {
	return security.WithActor(context.Background(), actor)
}

func TestForgetCommandOnlyDeletesOwnGroupMemoriesForMembers(t *testing.T) {
	member := security.Actor{ID: "qqonebot:2001", Platform: "qqonebot", PlatformUserID: "2001", Role: security.RoleUser, GroupRole: security.GroupRoleMember}
	deps, service, _, _ := forgetTestDeps(t, member)
	ctx := forgetTestContext(deps, member)
	own, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "我自己的记忆", "", angelmemory.Source{ActorID: member.ID, MessageID: "m-own"})
	if err != nil {
		t.Fatalf("remember own: %v", err)
	}
	other, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "别人的记忆", "", angelmemory.Source{ActorID: "qqonebot:2002", MessageID: "m-other"})
	if err != nil {
		t.Fatalf("remember other: %v", err)
	}

	handler := forgetCommand{deps: deps}
	result, err := handler.Handle(ctx, command.Request{Args: "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(result.Content, "我自己的记忆") || strings.Contains(result.Content, "别人的记忆") {
		t.Fatalf("list content = %q", result.Content)
	}
	if _, err := handler.Handle(ctx, command.Request{Args: other.ID}); err == nil {
		t.Fatal("member deleted another actor's memory")
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", other.ID); err != nil {
		t.Fatalf("other actor memory was removed: %v", err)
	}

	result, err = handler.Handle(ctx, command.Request{Args: own.ID[:8]})
	if err != nil {
		t.Fatalf("delete own: %v", err)
	}
	if !strings.Contains(result.Content, "已删除") {
		t.Fatalf("delete own content = %q", result.Content)
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", own.ID); err == nil {
		t.Fatal("own memory still exists after delete")
	}
}

func TestForgetCommandGroupAdminCanDeleteOthersAndBySource(t *testing.T) {
	admin := security.Actor{ID: "qqonebot:1000", Platform: "qqonebot", PlatformUserID: "1000", Role: security.RoleUser, GroupRole: security.GroupRoleOwner}
	deps, service, _, _ := forgetTestDeps(t, admin)
	ctx := forgetTestContext(deps, admin)
	first, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "同消息的记忆一", "", angelmemory.Source{ActorID: "qqonebot:2002", MessageID: "m-source"})
	if err != nil {
		t.Fatalf("remember first: %v", err)
	}
	second, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "同消息的记忆二", "", angelmemory.Source{ActorID: "qqonebot:2003", MessageID: "m-source"})
	if err != nil {
		t.Fatalf("remember second: %v", err)
	}

	handler := forgetCommand{deps: deps}
	if _, err := handler.Handle(ctx, command.Request{Args: first.ID[:8]}); err != nil {
		t.Fatalf("delete first by id: %v", err)
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", first.ID); err == nil {
		t.Fatal("first memory still exists after admin delete")
	}
	result, err := handler.Handle(ctx, command.Request{Args: "source m-source"})
	if err != nil {
		t.Fatalf("delete by source: %v", err)
	}
	if !strings.Contains(result.Content, "已删除 1 条") {
		t.Fatalf("delete by source content = %q", result.Content)
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", second.ID); err == nil {
		t.Fatal("second memory still exists after admin source delete")
	}
}

func TestForgetCommandResidentClear(t *testing.T) {
	member := security.Actor{ID: "qqonebot:2001", Platform: "qqonebot", PlatformUserID: "2001", Role: security.RoleUser, GroupRole: security.GroupRoleMember}
	deps, _, residentStore, _ := forgetTestDeps(t, member)
	ctx := forgetTestContext(deps, member)
	actorScope := resident.ActorScope(member)
	if err := residentStore.AppendNormal(ctx, actorScope, "用户喜欢短回复。"); err != nil {
		t.Fatalf("append normal: %v", err)
	}
	if err := residentStore.WriteCore(ctx, actorScope, "核心记忆"); err != nil {
		t.Fatalf("write core: %v", err)
	}

	handler := forgetCommand{deps: deps}
	if _, err := handler.Handle(ctx, command.Request{Args: "resident normal"}); err != nil {
		t.Fatalf("clear normal: %v", err)
	}
	memory, err := residentStore.Read(ctx, actorScope)
	if err != nil {
		t.Fatalf("read after normal clear: %v", err)
	}
	if strings.TrimSpace(memory.Normal) != "" || strings.TrimSpace(memory.Core) == "" {
		t.Fatalf("memory after normal clear = %#v", memory)
	}
	if _, err := handler.Handle(ctx, command.Request{Args: "resident core"}); err != nil {
		t.Fatalf("clear core without confirm: %v", err)
	}
	memory, err = residentStore.Read(ctx, actorScope)
	if err != nil {
		t.Fatalf("read after core no-confirm: %v", err)
	}
	if strings.TrimSpace(memory.Core) == "" {
		t.Fatal("core was cleared without confirmation")
	}
	if _, err := handler.Handle(ctx, command.Request{Args: "resident all --confirm"}); err != nil {
		t.Fatalf("clear all: %v", err)
	}
	if _, err := residentStore.Read(ctx, actorScope); err != resident.ErrNotFound {
		t.Fatalf("resident memory after all clear error = %v, want ErrNotFound", err)
	}
}

func TestMemoryCommandListsShowsAndDeletesWithSource(t *testing.T) {
	admin := security.Actor{ID: "qqonebot:root", Platform: "qqonebot", PlatformUserID: "root", Role: security.RoleSuperadmin}
	deps, service, _, _ := forgetTestDeps(t, admin)
	ctx := forgetTestContext(deps, admin)
	memory, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "长期记忆内容", "标签", angelmemory.Source{
		Kind:      "tool",
		ActorID:   "qqonebot:2001",
		MessageID: "m-1",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	handler := memoryCommand{deps: deps}
	result, err := handler.Handle(ctx, command.Request{Args: "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(result.Content, "长期记忆内容") || !strings.Contains(result.Content, "source") && !strings.Contains(result.Content, "message=m-1") {
		t.Fatalf("list content = %q", result.Content)
	}
	result, err = handler.Handle(ctx, command.Request{Args: "show " + memory.ID[:8]})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(result.Content, "来源：") || !strings.Contains(result.Content, "message=m-1") {
		t.Fatalf("show content = %q", result.Content)
	}
	result, err = handler.Handle(ctx, command.Request{Args: "delete " + memory.ID[:8]})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(result.Content, "已删除") {
		t.Fatalf("delete content = %q", result.Content)
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", memory.ID); err == nil {
		t.Fatal("memory still exists after delete")
	}
}
