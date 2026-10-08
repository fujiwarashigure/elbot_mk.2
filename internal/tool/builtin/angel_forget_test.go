package builtin

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elbot/internal/angelmemory"
	"elbot/internal/platform"
	"elbot/internal/ratelimit"
	"elbot/internal/security"
	"elbot/internal/tool"
)

func newAngelForgetTestService(t *testing.T) *angelmemory.Service {
	t.Helper()
	store, err := angelmemory.Open(context.Background(), filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("open angel memory: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return angelmemory.NewService(store)
}

func angelForgetTestContext(actorID string) context.Context {
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:          "qqonebot",
		ScopeID:           "group:1",
		PlatformMessageID: "m-1",
		SessionID:         "sess-1",
	})
	return security.WithActor(ctx, security.Actor{ID: actorID, Platform: "qqonebot", PlatformUserID: strings.TrimPrefix(actorID, "qqonebot:"), Role: security.RoleUser})
}

func angelToolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		names = append(names, candidate.Name())
	}
	return names
}

func TestAngelForgetToolIsOptInAndReachableThroughRecall(t *testing.T) {
	service := newAngelForgetTestService(t)
	disabled := angelToolNames(NewAngelMemoryTools(service, AngelMemoryToolOptions{}))
	if strings.Join(disabled, ",") != angelRememberToolName+","+angelRecallToolName {
		t.Fatalf("tools without opt-in = %#v", disabled)
	}
	enabled := NewAngelMemoryTools(service, AngelMemoryToolOptions{AllowForget: true})
	if strings.Join(angelToolNames(enabled), ",") != angelRememberToolName+","+angelRecallToolName+","+angelForgetToolName {
		t.Fatalf("tools with opt-in = %#v", angelToolNames(enabled))
	}
	var recall tool.Tool
	for _, candidate := range enabled {
		if candidate.Name() == angelRecallToolName {
			recall = candidate
		}
	}
	if deps := strings.Join(recall.Info().DependsOn, ","); deps != angelForgetToolName {
		t.Fatalf("recall dependencies = %q, want %q", deps, angelForgetToolName)
	}
	if info := (AngelForgetTool{service: service}).Info(); info.Risk != tool.RiskHigh || !info.OwnerScoped || !info.Hidden {
		t.Fatalf("angel_forget info = %#v", info)
	}

	// Discovered through angel_recall, the hidden forget tool still reaches the
	// model with a schema; on its own it must stay undiscoverable.
	registry := tool.NewRegistry()
	for _, candidate := range enabled {
		if err := registry.Register(candidate); err != nil {
			t.Fatalf("register %s: %v", candidate.Name(), err)
		}
	}
	details, errs := registry.DiscoverDetails(context.Background(), []string{angelRecallToolName}, nil)
	if len(errs) != 0 {
		t.Fatalf("discover angel_recall errors = %#v", errs)
	}
	names := map[string]bool{}
	for _, detail := range details {
		if detail.Schema != nil {
			names[detail.Info.Name] = true
		}
	}
	if !names[angelForgetToolName] {
		t.Fatalf("angel_forget was not injected with angel_recall: %#v", details)
	}
	if root, err := registry.Discover(angelForgetToolName); err == nil && len(root.Tools) > 0 {
		t.Fatalf("hidden angel_forget must not be discoverable on its own: %#v", root.Tools)
	}
}

func TestAngelForgetRequiresOwnMemoryAndConfirmation(t *testing.T) {
	service := newAngelForgetTestService(t)
	ctx := angelForgetTestContext("qqonebot:2001")
	own, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "我自己的记忆", "", angelmemory.Source{Kind: "tool", ActorID: "qqonebot:2001"})
	if err != nil {
		t.Fatalf("remember own: %v", err)
	}
	other, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "别人的记忆", "", angelmemory.Source{Kind: "tool", ActorID: "qqonebot:2002"})
	if err != nil {
		t.Fatalf("remember other: %v", err)
	}
	legacy, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "旧版记忆", "", angelmemory.Source{Label: "tool"})
	if err != nil {
		t.Fatalf("remember legacy: %v", err)
	}
	forget := AngelForgetTool{service: service, deletions: ratelimit.New(angelForgetMaxPerMinute, time.Minute)}

	// Another member's memory is not even resolvable for this actor.
	result, err := forget.Call(ctx, tool.CallRequest{Arguments: raw(`{"id":"` + other.ID + `","confirm":true}`)})
	if err != nil {
		t.Fatalf("forget other: %v", err)
	}
	if !strings.Contains(result.Content, "没有找到") {
		t.Fatalf("other memory result = %q", result.Content)
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", other.ID); err != nil {
		t.Fatalf("another member's memory was deleted: %v", err)
	}

	// A legacy entry has no source actor, so the tool must refuse it.
	result, err = forget.Call(ctx, tool.CallRequest{Arguments: raw(`{"id":"` + legacy.ID + `","confirm":true}`)})
	if err != nil {
		t.Fatalf("forget legacy: %v", err)
	}
	if !strings.Contains(result.Content, "没有找到") {
		t.Fatalf("legacy memory result = %q", result.Content)
	}

	// First call without confirm only previews the target.
	result, err = forget.Call(ctx, tool.CallRequest{Arguments: raw(`{"id":"` + own.ID[:8] + `"}`)})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !strings.Contains(result.Content, "尚未删除") || !strings.Contains(result.Content, own.ID) {
		t.Fatalf("preview result = %q", result.Content)
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", own.ID); err != nil {
		t.Fatalf("memory deleted before confirmation: %v", err)
	}

	// Confirmed call deletes exactly that entry.
	result, err = forget.Call(ctx, tool.CallRequest{Arguments: raw(`{"id":"` + own.ID[:8] + `","confirm":true}`)})
	if err != nil {
		t.Fatalf("confirmed delete: %v", err)
	}
	if !strings.Contains(result.Content, "已删除") {
		t.Fatalf("delete result = %q", result.Content)
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", own.ID); !errors.Is(err, angelmemory.ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestAngelForgetPreflightRejectsForeignMemory(t *testing.T) {
	service := newAngelForgetTestService(t)
	ctx := angelForgetTestContext("qqonebot:2001")
	other, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "别人的记忆", "", angelmemory.Source{Kind: "tool", ActorID: "qqonebot:2002"})
	if err != nil {
		t.Fatalf("remember other: %v", err)
	}
	forget := AngelForgetTool{service: service}
	if err := forget.PreflightConfirmation(ctx, tool.CallRequest{Arguments: raw(`{"id":"` + other.ID + `","confirm":true}`)}); err == nil {
		t.Fatal("preflight accepted another member's memory")
	}
	if _, err := forget.RiskDetail(ctx, tool.CallRequest{Arguments: raw(`{"id":"` + other.ID + `"}`)}); err == nil {
		t.Fatal("risk detail rendered another member's memory")
	}
}

func TestAngelForgetRateLimitBoundsModelDeletions(t *testing.T) {
	service := newAngelForgetTestService(t)
	ctx := angelForgetTestContext("qqonebot:2001")
	forget := AngelForgetTool{service: service, deletions: ratelimit.New(angelForgetMaxPerMinute, time.Minute)}
	for i := 0; i < angelForgetMaxPerMinute; i++ {
		memory, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "记忆"+string(rune('A'+i)), "", angelmemory.Source{Kind: "tool", ActorID: "qqonebot:2001"})
		if err != nil {
			t.Fatalf("remember %d: %v", i, err)
		}
		result, err := forget.Call(ctx, tool.CallRequest{Arguments: raw(`{"id":"` + memory.ID + `","confirm":true}`)})
		if err != nil || !strings.Contains(result.Content, "已删除") {
			t.Fatalf("delete %d = %q, %v", i, result.Content, err)
		}
	}
	extra, err := service.RememberWithSource(ctx, "qqonebot", "group:1", "超限记忆", "", angelmemory.Source{Kind: "tool", ActorID: "qqonebot:2001"})
	if err != nil {
		t.Fatalf("remember extra: %v", err)
	}
	result, err := forget.Call(ctx, tool.CallRequest{Arguments: raw(`{"id":"` + extra.ID + `","confirm":true}`)})
	if err != nil {
		t.Fatalf("over-limit delete: %v", err)
	}
	if !strings.Contains(result.Content, "最多通过工具删除") {
		t.Fatalf("over-limit result = %q", result.Content)
	}
	if _, err := service.Get(ctx, "qqonebot", "group:1", extra.ID); err != nil {
		t.Fatalf("over-limit memory was deleted: %v", err)
	}
}
