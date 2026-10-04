package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/request"
	"elbot/internal/security"
)

func memberPanelTestContext() context.Context {
	return platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "cli",
		PlatformUserID:   "member",
		Nickname:         "普通成员",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
		GroupRole:        security.GroupRoleMember,
	})
}

func TestMemberPanelShowsOwnTasksAndQuota(t *testing.T) {
	adapter := &fakePlatform{}
	a := New(adapter, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := memberPanelTestContext()
	scope := a.baseScope(ctx)
	a.setGroupPolicyForScope(scope, config.GroupPolicyConfig{
		ImageQuota:      10,
		UserImageQuota:  3,
		VisionQuota:     5,
		UserVisionQuota: 2,
		ChatTokensQuota: 1000,
		ChatCostQuota:   0.5,
	})
	a.budgetLimits = config.BudgetLimitsConfig{
		GlobalImageDaily:      20,
		UserImageDaily:        4,
		GlobalVisionDaily:     10,
		UserVisionDaily:       3,
		GlobalChatTokensDaily: 5000,
		UserChatTokensDaily:   2000,
		GlobalChatCostDaily:   1.0,
		UserChatCostDaily:     0.2,
	}.Normalized()

	if ok, reason := a.reserveBudget(ctx, "image", "img-1"); !ok {
		t.Fatalf("reserve image: %s", reason)
	}
	if ok, reason := a.reserveBudget(ctx, "vision", "vision-1"); !ok {
		t.Fatalf("reserve vision: %s", reason)
	}
	a.recordChatUsage(ctx, "test-model", &llm.Usage{PromptTokens: 60, TotalTokens: 60}, "usage-1")

	session, err := a.sessions.GetOrCreateCurrentWithMode(ctx, a.scope(ctx), "hi", a.sessions.DefaultMode())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	reqInfo, _, done, err := a.requests.Start(ctx, request.StartRequest{
		SessionID: session.ID,
		Kind:      request.KindTurn,
		Label:     "chat",
		FairKey:   a.requestFairKey(ctx),
		ScopeKey:  a.requestScopeKey(ctx),
	})
	if err != nil {
		t.Fatalf("start request: %v", err)
	}
	defer done()

	panel, err := a.MemberPanel(ctx, "all")
	if err != nil {
		t.Fatalf("MemberPanel: %v", err)
	}
	for _, want := range []string{
		"我的面板",
		"当前会话",
		"我的任务",
		reqInfo.ID,
		"本群生图日",
		"已用 1 / 10",
		"本群单用户生图日",
		"已用 1 / 3",
		"全局单用户生图日",
		"已用 1 / 4",
		"本群视觉日",
		"已用 1 / 5",
		"本群单用户视觉日",
		"已用 1 / 2",
		"本群日聊天 token",
		"已用 60 / 1000",
		"全局单用户日聊天 token",
		"已用 60 / 2000",
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("panel missing %q:\n%s", want, panel)
		}
	}

	quotaOnly, err := a.MemberPanel(ctx, "quota")
	if err != nil {
		t.Fatalf("MemberPanel quota: %v", err)
	}
	if strings.Contains(quotaOnly, "我的任务") {
		t.Fatalf("quota view unexpectedly contains tasks:\n%s", quotaOnly)
	}
	tasksOnly, err := a.MemberPanel(ctx, "tasks")
	if err != nil {
		t.Fatalf("MemberPanel tasks: %v", err)
	}
	if strings.Contains(tasksOnly, "我的额度") {
		t.Fatalf("tasks view unexpectedly contains quota:\n%s", tasksOnly)
	}
}

func TestMemberPanelCommand(t *testing.T) {
	adapter := &fakePlatform{}
	a := New(adapter, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := memberPanelTestContext()

	if err := a.HandleMessage(ctx, "/me quota"); err != nil {
		t.Fatalf("HandleMessage /me quota: %v", err)
	}
	out := adapter.out.String()
	if !strings.Contains(out, "我的额度") {
		t.Fatalf("output = %q, want quota panel", out)
	}
	if strings.Contains(out, "我的任务") {
		t.Fatalf("output = %q, quota view must not contain tasks", out)
	}
}
