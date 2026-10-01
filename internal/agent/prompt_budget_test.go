package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/contextmgr"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/security"
)

func newOverflowAgent(t *testing.T, replies []string, mode string) (*Agent, *fakePlatform, *fakeLLM) {
	t.Helper()
	p := &fakePlatform{}
	f := &fakeLLM{replies: replies}
	a := New(p, f, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.SetContextOptions(config.ContextConfig{
		MaxPromptRatio:        0.8,
		SingleMessageMaxRatio: 0.5,
		ReserveOutputTokens:   0,
		OverflowMode:          mode,
	}, config.ModelMetadataConfig{DefaultContextWindow: 100}, nil, config.ModelSelection{})
	return a, p, f
}

func TestPromptOverflowRejectsOversizedMessage(t *testing.T) {
	ctx := context.Background()
	a, p, f := newOverflowAgent(t, []string{"unused"}, "reject")
	if err := a.HandleMessage(ctx, strings.Repeat("警告", 200)); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if got := len(f.chatRequests()); got != 0 {
		t.Fatalf("model requests = %d, want 0", got)
	}
	if !strings.Contains(p.out.String(), "长消息保护已触发") || !strings.Contains(p.out.String(), "未发送给模型") {
		t.Fatalf("alert = %q", p.out.String())
	}
	session, err := a.sessions.Current(ctx, a.scope(ctx))
	if err != nil {
		t.Fatalf("current session: %v", err)
	}
	messages, err := a.store.Messages().ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("oversized message was persisted: %#v", messages)
	}
}

func TestPromptOverflowTruncatePersistsFittingMessage(t *testing.T) {
	ctx := context.Background()
	a, p, f := newOverflowAgent(t, []string{"ok"}, "truncate")
	if err := a.HandleMessage(ctx, strings.Repeat("很长的消息", 100)); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	requests := f.chatRequests()
	if len(requests) != 1 {
		t.Fatalf("model requests = %d, want 1; alerts = %q", len(requests), p.out.String())
	}
	latest := llm.SegmentsContentText(requests[0].Messages[len(requests[0].Messages)-1].Segments)
	if !strings.Contains(latest, "已自动截断") {
		t.Fatalf("latest user text = %q", latest)
	}
	if got := contextmgr.EstimateMessagesTokens(requests[0].Messages); got > 80 {
		t.Fatalf("estimated prompt = %d, want <= 80", got)
	}
	if !strings.Contains(p.out.String(), "已自动截断后继续处理") {
		t.Fatalf("alert = %q", p.out.String())
	}
	session, err := a.sessions.Current(ctx, a.scope(ctx))
	if err != nil {
		t.Fatalf("current session: %v", err)
	}
	messages, err := a.store.Messages().ListBySession(ctx, session.ID)
	if err != nil || len(messages) < 1 {
		t.Fatalf("messages = %#v, err = %v", messages, err)
	}
	if messages[0].Role != "user" || !strings.Contains(messages[0].Content, "已自动截断") {
		t.Fatalf("persisted content = %q", messages[0].Content)
	}
}

func TestPromptOverflowSummarizeUsesCompactModel(t *testing.T) {
	ctx := context.Background()
	a, p, f := newOverflowAgent(t, []string{"简要摘要", "最终回答"}, "summarize")
	if err := a.HandleMessage(ctx, strings.Repeat("很长消息", 15)); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	requests := f.chatRequests()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	firstSystem := llm.SegmentsContentText(requests[0].Messages[0].Segments)
	if !strings.Contains(firstSystem, "消息压缩器") {
		t.Fatalf("first request is not a summary request: %q", firstSystem)
	}
	latest := llm.SegmentsContentText(requests[1].Messages[len(requests[1].Messages)-1].Segments)
	if !strings.Contains(latest, "简要摘要") {
		t.Fatalf("main request latest user = %q", latest)
	}
	if !strings.Contains(p.out.String(), "已自动摘要后继续处理") {
		t.Fatalf("alert = %q", p.out.String())
	}
}

func TestContextPolicySetResetAndPersist(t *testing.T) {
	ctx := context.Background()
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	msgCtx := platform.WithMessageContext(ctx, platform.MessageContext{
		Platform:       "qqonebot",
		PlatformUserID: "admin",
		ScopeID:        "group:9",
		GroupRole:      security.GroupRoleAdmin,
	})
	if _, err := a.SetContextPolicy(msgCtx, "work", "summarize"); err != nil {
		t.Fatalf("SetContextPolicy: %v", err)
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	override := state.ContextOverflow["qqonebot:group:9"]
	if override.Work != "summarize" {
		t.Fatalf("state override = %#v", override)
	}
	status := a.ContextPolicyStatus(msgCtx)
	if !strings.Contains(status, "summarize") || !strings.Contains(status, "当前群覆盖") {
		t.Fatalf("status = %q", status)
	}
	if _, err := a.ResetContextPolicy(msgCtx, "work"); err != nil {
		t.Fatalf("ResetContextPolicy: %v", err)
	}
	state, err = config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState after reset: %v", err)
	}
	if len(state.ContextOverflow) != 0 {
		t.Fatalf("context overflow after reset = %#v", state.ContextOverflow)
	}
}
