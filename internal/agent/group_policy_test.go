package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/ops/concurrency"
	"elbot/internal/platform"
	"elbot/internal/security"
	"elbot/internal/toolrun"
)

func TestGroupPolicySetAndWakeup(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qqonebot",
		PlatformUserID:   "admin",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
		GroupRole:        security.GroupRoleAdmin,
	})
	if _, err := a.SetGroupPolicy(ctx, "response", "all"); err != nil {
		t.Fatalf("SetGroupPolicy response: %v", err)
	}
	if _, err := a.SetGroupPolicy(ctx, "wake", "小助手,hey bot"); err != nil {
		t.Fatalf("SetGroupPolicy wake: %v", err)
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	policy := state.GroupPolicy["qqonebot:group:9"]
	if policy.ResponseMode != "all" || len(policy.WakeKeywords) != 2 {
		t.Fatalf("stored policy = %#v", policy)
	}
	wakeCtx := platform.WithMessageContext(ctx, platform.MessageContext{
		Platform:         "qqonebot",
		PlatformUserID:   "member",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
		GroupRole:        security.GroupRoleMember,
	})
	if !a.messageWakeup(wakeCtx, "普通群消息") {
		t.Fatal("response mode all should wake ordinary group message")
	}
	if _, err := a.SetGroupPolicy(wakeCtx, "response", "off"); err == nil {
		t.Fatal("member should not manage policy")
	}
}

func TestGroupPolicyLearningModerationRequiresSuperadmin(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qqonebot",
		PlatformUserID:   "admin",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
		GroupRole:        security.GroupRoleAdmin,
	})
	if _, err := a.SetGroupPolicy(ctx, "learning-moderation", "on"); err == nil {
		t.Fatal("group admin must not grant learning moderation")
	}
	superCtx := security.WithActor(ctx, security.Actor{ID: "qqonebot:root", Platform: "qqonebot", PlatformUserID: "root", Role: security.RoleSuperadmin, GroupRole: security.GroupRoleAdmin})
	if _, err := a.SetGroupPolicy(superCtx, "learning-moderation", "on"); err != nil {
		t.Fatalf("superadmin grant: %v", err)
	}
	if !a.groupAdminCommandGrant(ctx, "learning") {
		t.Fatal("granted group admin should moderate learning")
	}
	if a.groupAdminCommandGrant(ctx, "shell") {
		t.Fatal("learning grant must not authorize other commands")
	}
}

func TestLearningCommandRequiresGroupGrant(t *testing.T) {
	p := &fakePlatform{}
	a := New(p, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	actor := security.Actor{ID: "cli:admin", Platform: "cli", PlatformUserID: "admin", Role: security.RoleUser, GroupRole: security.GroupRoleAdmin}
	ctx := security.WithActor(platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "cli",
		PlatformUserID:   "admin",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
		GroupRole:        security.GroupRoleAdmin,
	}), actor)
	handled, err := a.commandExecutor.Handle(ctx, "/learning status")
	if err != nil || !handled {
		t.Fatalf("learning denied call = handled %v err %v", handled, err)
	}
	if !strings.Contains(p.out.String(), "权限") {
		t.Fatalf("learning was not denied: %q", p.out.String())
	}
	a.setGroupPolicyForScope(a.scope(ctx), config.GroupPolicyConfig{LearningModeration: true})
	p.out.Reset()
	handled, err = a.commandExecutor.Handle(ctx, "/learning status")
	if err != nil || !handled {
		t.Fatalf("learning granted call = handled %v err %v", handled, err)
	}
	if strings.Contains(p.out.String(), "需要") && strings.Contains(p.out.String(), "权限") {
		t.Fatalf("learning remained denied: %q", p.out.String())
	}
}

func TestGroupPolicyDoesNotCrossGroups(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	base := context.Background()
	group9 := platform.WithMessageContext(base, platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "admin", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleAdmin,
	})
	if _, err := a.SetGroupPolicy(group9, "response", "off"); err != nil {
		t.Fatalf("SetGroupPolicy group9: %v", err)
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if _, ok := state.GroupPolicy["qqonebot:group:10"]; ok {
		t.Fatal("group 9 policy must not write group 10")
	}
	if status := a.GroupPolicyStatus(group9); !strings.Contains(status, "off") {
		t.Fatalf("group 9 status = %q", status)
	}
}

func TestGroupToolAllowlistExpandsProfilesAndDenyAll(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "admin", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleAdmin,
	})
	a.toolProfiles = map[string][]string{"web": {"web_search"}}
	a.toolAliases = map[string]string{"search": "web"}

	if _, err := a.SetGroupPolicy(ctx, "tool-allow", "search"); err != nil {
		t.Fatalf("SetGroupPolicy tool-allow profile: %v", err)
	}
	if ok, reason := a.authorizeToolCall(ctx, llm.ToolCallRequest{Name: "web_search"}, toolrun.ResolvedTool{Name: "web_search"}); !ok {
		t.Fatalf("profile tool denied: %s", reason)
	}

	if _, err := a.SetGroupPolicy(ctx, "tool-allow", "none"); err != nil {
		t.Fatalf("SetGroupPolicy tool-allow none: %v", err)
	}
	if ok, _ := a.authorizeToolCall(ctx, llm.ToolCallRequest{Name: "web_search"}, toolrun.ResolvedTool{Name: "web_search"}); ok {
		t.Fatal("empty group allowlist must deny normal tools")
	}
	if ok, _ := a.authorizeToolCall(ctx, llm.ToolCallRequest{Name: "discover_tool"}, toolrun.ResolvedTool{Name: "discover_tool"}); ok {
		t.Fatal("empty group allowlist must deny discover_tool too")
	}
	if _, err := a.SetGroupPolicy(ctx, "tool-allow", "discover_tool,web"); err != nil {
		t.Fatalf("SetGroupPolicy discover_tool: %v", err)
	}
	if ok, _ := a.authorizeToolCall(ctx, llm.ToolCallRequest{Name: "discover_tool"}, toolrun.ResolvedTool{Name: "discover_tool"}); !ok {
		t.Fatal("explicit discover_tool entry should be honored")
	}
}

func TestGroupModelCatalogRejectsArbitraryProviderModel(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	a.modelProfiles = map[string]config.ModelSelection{"cheap": {Provider: "prov", Model: "cheap-model"}}
	a.modelAliases = map[string]string{"cheap": "cheap"}
	a.modelRuntime.clients = map[string]llm.LLM{"prov": &fakeLLM{}}
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "admin", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleAdmin,
	})
	if _, err := a.SetGroupPolicy(ctx, "default-model", "prov/expensive"); err == nil {
		t.Fatal("group admin must not select an uncatalogued provider/model")
	}
	superCtx := security.WithActor(ctx, security.Actor{ID: "qqonebot:root", Platform: "qqonebot", PlatformUserID: "root", Role: security.RoleSuperadmin, GroupRole: security.GroupRoleAdmin})
	if _, err := a.SetGroupPolicy(superCtx, "allowed-models", "prov/expensive"); err != nil {
		t.Fatalf("superadmin allowed-models: %v", err)
	}
	if _, err := a.SetGroupPolicy(ctx, "default-model", "prov/expensive"); err != nil {
		t.Fatalf("group admin select catalogued model: %v", err)
	}
	selection, ok := a.groupDefaultModelSelection(ctx)
	if !ok || selection.Provider != "prov" || selection.Model != "expensive" {
		t.Fatalf("default model = %#v ok=%v", selection, ok)
	}
}

func TestLearningModerationActionsAreGranularAndRevocable(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "admin", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleAdmin,
	})
	superCtx := security.WithActor(ctx, security.Actor{ID: "qqonebot:root", Platform: "qqonebot", PlatformUserID: "root", Role: security.RoleSuperadmin, GroupRole: security.GroupRoleAdmin})
	if _, err := a.SetGroupPolicy(superCtx, "learning-moderation", "on"); err != nil {
		t.Fatalf("grant moderation: %v", err)
	}
	if _, err := a.SetGroupPolicy(superCtx, "learning-moderation-actions", "view"); err != nil {
		t.Fatalf("set moderation actions: %v", err)
	}
	if !a.AuthorizeLearningAction(ctx, config.LearningModerationView) {
		t.Fatal("view action should be granted")
	}
	if a.AuthorizeLearningAction(ctx, config.LearningModerationDecide) {
		t.Fatal("decide action was not granted")
	}
	if a.AuthorizeLearningAction(ctx, config.LearningModerationMine) {
		t.Fatal("mine action was not granted")
	}
	if _, err := a.SetGroupPolicy(superCtx, "learning-moderation", "off"); err != nil {
		t.Fatalf("revoke moderation: %v", err)
	}
	if a.AuthorizeLearningAction(ctx, config.LearningModerationView) {
		t.Fatal("view action must be revoked immediately")
	}
}

func TestBudgetLedgerPersistsAndIsIdempotent(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "member", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleMember,
	})
	a.setGroupPolicyForScope(a.scope(ctx), config.GroupPolicyConfig{ImageQuota: 2})
	if ok, reason := a.reserveBudget(ctx, "image", "call-1"); !ok {
		t.Fatalf("reserve call-1: %s", reason)
	}
	if ok, reason := a.reserveBudget(ctx, "image", "call-1"); !ok {
		t.Fatalf("idempotent replay rejected: %s", reason)
	}
	if used, _ := a.budgetUsageForScope(ctx); used != 1 {
		t.Fatalf("used after replay = %d, want 1", used)
	}
	if ok, reason := a.reserveBudget(ctx, "image", "call-2"); !ok {
		t.Fatalf("reserve call-2: %s", reason)
	}
	if ok, _ := a.reserveBudget(ctx, "image", "call-3"); ok {
		t.Fatal("third reservation exceeded quota")
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Budget.Reservations) != 2 {
		t.Fatalf("persisted reservations = %d, want 2", len(state.Budget.Reservations))
	}
	b := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	b.statePath = a.statePath
	b.setBudgetSnapshot(state.Budget.Reservations)
	if used, _ := b.budgetUsageForScope(ctx); used != 2 {
		t.Fatalf("reloaded used = %d, want 2", used)
	}
}

func TestBudgetEnforcesGroupUserAndGlobalLimits(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{UserImageDaily: 1, GlobalImageDaily: 2}
	policy := config.GroupPolicyConfig{ImageQuota: 5, UserImageQuota: 1}
	ctx1 := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "u1", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleMember,
	})
	ctx2 := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "u2", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleMember,
	})
	a.setGroupPolicyForScope(a.scope(ctx1), policy)
	if ok, reason := a.reserveBudget(ctx1, "image", "u1-call-1"); !ok {
		t.Fatalf("user1 first reserve: %s", reason)
	}
	if ok, _ := a.reserveBudget(ctx1, "image", "u1-call-2"); ok {
		t.Fatal("per-group per-user image limit should reject the second user1 call")
	}
	if ok, reason := a.reserveBudget(ctx2, "image", "u2-call-1"); !ok {
		t.Fatalf("user2 first reserve: %s", reason)
	}
	if ok, _ := a.reserveBudget(ctx2, "image", "u2-call-2"); ok {
		t.Fatal("global image limit should reject a third call across users")
	}
}

func TestChatTokenAndCostBudget(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	a.budgetLimits = config.BudgetLimitsConfig{GlobalChatTokensDaily: 100, UserChatTokensDaily: 50, GlobalChatCostDaily: 0.5}
	a.pricing = config.DailyReportConfig{Prices: map[string]config.ModelPriceConfig{
		"test-model": {InputPerMillion: 1, OutputPerMillion: 1},
	}}
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "member", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleMember,
	})
	if err := a.checkChatBudget(ctx, config.ModelSelection{Provider: "p", Model: "test-model"}); err != nil {
		t.Fatalf("initial token budget: %v", err)
	}
	a.recordChatUsage(ctx, "test-model", &llm.Usage{PromptTokens: 60, TotalTokens: 60}, "usage-1")
	if err := a.checkChatBudget(ctx, config.ModelSelection{Provider: "p", Model: "test-model"}); err == nil {
		t.Fatal("user token budget should reject usage over 50 tokens")
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Budget.Tokens) != 1 || len(state.Budget.Costs) != 1 {
		t.Fatalf("token/cost ledger not persisted: tokens=%d costs=%d", len(state.Budget.Tokens), len(state.Budget.Costs))
	}
}

func TestToolExecutionIdempotencyReservation(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "member", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleMember,
	})
	if ok, reason := a.reserveToolExecution(ctx, "call-1", "digest-a"); !ok {
		t.Fatalf("first execution reservation: %s", reason)
	}
	if ok, reason := a.reserveToolExecution(ctx, "call-1", "digest-a"); ok || !strings.Contains(reason, "重复执行") {
		t.Fatalf("same execution replay = ok=%v reason=%q, want duplicate rejection", ok, reason)
	}
	if ok, reason := a.reserveToolExecution(ctx, "call-1", "digest-b"); ok || !strings.Contains(reason, "不同参数") {
		t.Fatalf("different digest reuse = ok=%v reason=%q, want parameter error", ok, reason)
	}
}

func TestBudgetCallIDDigestRejectsParameterReuse(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "member", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleMember,
	})
	a.setGroupPolicyForScope(a.scope(ctx), config.GroupPolicyConfig{ImageQuota: 5})
	if ok, reason := a.reserveBudgetRequest(ctx, "image", budgetReservationRequest{CallID: "call-1", Digest: "digest-a"}); !ok {
		t.Fatalf("first reserve: %s", reason)
	}
	if ok, reason := a.reserveBudgetRequest(ctx, "image", budgetReservationRequest{CallID: "call-1", Digest: "digest-a"}); !ok {
		t.Fatalf("same request replay: %s", reason)
	}
	if ok, _ := a.reserveBudgetRequest(ctx, "image", budgetReservationRequest{CallID: "call-1", Digest: "digest-b"}); ok {
		t.Fatal("same call ID with different digest should be rejected")
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Budget.Digests) != 1 {
		t.Fatalf("persisted digest count = %d, want 1", len(state.Budget.Digests))
	}
}

func TestBudgetWriteFailureRollsBackAndDeniesRestrictedCall(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(blocker, "state.toml")
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "member", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleMember,
	})
	a.setGroupPolicyForScope(a.scope(ctx), config.GroupPolicyConfig{ImageQuota: 2})
	ok, reason := a.reserveBudget(ctx, "image", "call-1")
	if ok {
		t.Fatal("reserve should fail when the ledger cannot be persisted")
	}
	if !strings.Contains(reason, "写入失败") {
		t.Fatalf("denial reason = %q, want ledger write failure", reason)
	}
	if used, _ := a.budgetUsageForScope(ctx); used != 0 {
		t.Fatalf("failed reservation was not rolled back: used=%d", used)
	}
}

func TestResetUnknownGroupPolicyFieldDoesNotWipePolicy(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "admin", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleAdmin,
	})
	if _, err := a.SetGroupPolicy(ctx, "response", "off"); err != nil {
		t.Fatalf("set response: %v", err)
	}
	if _, err := a.ResetGroupPolicy(ctx, "does-not-exist"); err == nil {
		t.Fatal("unknown reset field should error")
	}
	if got := a.groupPolicyForScope(a.scope(ctx)).ResponseModeValue(); got != "off" {
		t.Fatalf("unknown reset wiped policy: response=%q", got)
	}
}

func TestExecutionModelAuthorizationUsesGroupCatalog(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "member", ScopeID: "group:9", ConversationKind: platform.ConversationGroup, GroupRole: security.GroupRoleMember,
	})
	a.setGroupPolicyForScope(a.scope(ctx), config.GroupPolicyConfig{AllowedModels: []string{"provider/allowed"}})
	if err := a.authorizeExecutionModelSelection(ctx, config.ModelSelection{Provider: "provider", Model: "allowed"}); err != nil {
		t.Fatalf("catalogued model denied: %v", err)
	}
	if err := a.authorizeExecutionModelSelection(ctx, config.ModelSelection{Provider: "provider", Model: "hidden"}); err == nil {
		t.Fatal("model outside group catalog should be denied")
	}
}

func TestProviderConcurrencyLimit(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.providerLimitCfg = concurrency.Config{Max: 1}
	release, err := a.acquireProviderSlot(context.Background(), "provider-a")
	if err != nil {
		t.Fatalf("first provider slot: %v", err)
	}
	if _, err := a.acquireProviderSlot(context.Background(), "provider-a"); err == nil {
		t.Fatal("second provider slot should be rejected while the first is held")
	}
	release()
	if _, err := a.acquireProviderSlot(context.Background(), "provider-a"); err != nil {
		t.Fatalf("provider slot after release: %v", err)
	}
}
