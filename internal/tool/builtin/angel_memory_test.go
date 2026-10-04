package builtin

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/angelmemory"
	"elbot/internal/platform"
	"elbot/internal/security"
	"elbot/internal/tool"
)

func TestAngelRememberToolStoresSourceTrace(t *testing.T) {
	ctx := context.Background()
	store, err := angelmemory.Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("open angel memory: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := angelmemory.NewService(store)
	rememberTool := AngelRememberTool{service: service}
	recallTool := AngelRecallTool{service: service}

	ctx = platform.WithMessageContext(ctx, platform.MessageContext{
		Platform:          "qqonebot",
		ScopeID:           "group:1",
		PlatformMessageID: "m-1",
		SessionID:         "sess-1",
	})
	ctx = security.WithActor(ctx, security.Actor{ID: "qqonebot:2001", Platform: "qqonebot", PlatformUserID: "2001", Role: security.RoleUser})
	result, err := rememberTool.Call(ctx, tool.CallRequest{Arguments: raw(`{"content":"用户喜欢猫","tags":"偏好"}`)})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !strings.Contains(result.Content, "已记住") {
		t.Fatalf("remember content = %q", result.Content)
	}
	memories, err := service.List(ctx, "qqonebot", "group:1", angelmemory.SourceFilter{ActorID: "qqonebot:2001"}, 10)
	if err != nil || len(memories) != 1 {
		t.Fatalf("list = %#v, %v", memories, err)
	}
	memory := memories[0]
	if memory.SourceKind != "tool" || memory.SourceActorID != "qqonebot:2001" || memory.SourceMessageID != "m-1" || memory.SourceSessionID != "sess-1" {
		t.Fatalf("source = %#v", memory)
	}

	result, err = recallTool.Call(ctx, tool.CallRequest{Arguments: raw(`{"query":"猫"}`)})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(result.Content, "id=") || !strings.Contains(result.Content, "source=tool") || !strings.Contains(result.Content, "message=m-1") {
		t.Fatalf("recall content = %q", result.Content)
	}
}
