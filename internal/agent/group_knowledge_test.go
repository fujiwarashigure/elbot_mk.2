package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/platform"
	"elbot/internal/security"
)

func testBoolPtr(value bool) *bool { return &value }

func knowledgeTestContext() context.Context {
	return platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "cli",
		PlatformUserID:   "admin",
		Nickname:         "管理员",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
		GroupRole:        security.GroupRoleAdmin,
	})
}

func enableKnowledgeTestPolicy(t *testing.T, a *Agent, ctx context.Context) {
	t.Helper()
	a.setGroupPolicyForScope(a.baseScope(ctx), config.GroupPolicyConfig{ResponseMode: "all"})
}

func TestGroupKnowledgeAnswersLocallyAndPersists(t *testing.T) {
	adapter := &fakePlatform{}
	fake := &fakeLLM{}
	a := New(adapter, fake, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := knowledgeTestContext()
	enableKnowledgeTestPolicy(t, a, ctx)

	if err := a.HandleMessage(ctx, "/faq add 怎么绑定 => 在设置里绑定"); err != nil {
		t.Fatalf("add knowledge: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "k1") {
		t.Fatalf("add output = %q, want k1", adapter.out.String())
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if entries := state.GroupKnowledge["cli:group:9"]; len(entries) != 1 || entries[0].Question != "怎么绑定" {
		t.Fatalf("stored entries = %#v", entries)
	}

	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "怎么绑定？"); err != nil {
		t.Fatalf("answer question: %v", err)
	}
	if got := len(fake.chatRequests()); got != 0 {
		t.Fatalf("chat requests after FAQ hit = %d, want 0", got)
	}
	if !strings.Contains(adapter.out.String(), "在设置里绑定") {
		t.Fatalf("FAQ output = %q, want answer", adapter.out.String())
	}

	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "/faq list"); err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "怎么绑定") {
		t.Fatalf("list output = %q, want question", adapter.out.String())
	}

	if err := a.HandleMessage(ctx, "/faq remove k1"); err != nil {
		t.Fatalf("remove knowledge: %v", err)
	}
	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "怎么绑定？"); err != nil {
		t.Fatalf("fallback question: %v", err)
	}
	if got := len(fake.chatRequests()); got != 1 {
		t.Fatalf("chat requests after removal = %d, want 1 LLM fallback", got)
	}
}

func TestGroupKnowledgeOffFallsThroughToChat(t *testing.T) {
	adapter := &fakePlatform{}
	fake := &fakeLLM{}
	a := New(adapter, fake, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := knowledgeTestContext()
	enableKnowledgeTestPolicy(t, a, ctx)

	if err := a.HandleMessage(ctx, "/faq add 怎么绑定 => 在设置里绑定"); err != nil {
		t.Fatalf("add knowledge: %v", err)
	}
	if _, err := a.SetGroupPolicy(ctx, "knowledge", "off"); err != nil {
		t.Fatalf("SetGroupPolicy off: %v", err)
	}
	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "怎么绑定？"); err != nil {
		t.Fatalf("off question: %v", err)
	}
	if got := len(fake.chatRequests()); got != 1 {
		t.Fatalf("chat requests with knowledge off = %d, want 1", got)
	}
	if strings.Contains(adapter.out.String(), "在设置里绑定") {
		t.Fatalf("knowledge off still answered: %q", adapter.out.String())
	}
}

func TestGroupKnowledgeGlobalDisableRejectsManagement(t *testing.T) {
	adapter := &fakePlatform{}
	a := New(adapter, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	a.groupKnowledgeCfg.Enabled = testBoolPtr(false)
	ctx := knowledgeTestContext()
	enableKnowledgeTestPolicy(t, a, ctx)

	// The command layer propagates service errors; the deferred message
	// handler still sends the user-facing notice, so assert on output instead.
	_ = a.HandleMessage(ctx, "/faq add 怎么绑定 => 在设置里绑定")
	if !strings.Contains(adapter.out.String(), "全局关闭") {
		t.Fatalf("output = %q, want global disabled notice", adapter.out.String())
	}
	if _, err := a.GroupKnowledgeAdd(ctx, "x", "y", "exact", nil, nil); err == nil {
		t.Fatal("GroupKnowledgeAdd must fail when globally disabled")
	}
}

func TestGroupKnowledgeLoadsAtStartup(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.toml")
	ctx := knowledgeTestContext()

	first := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	first.statePath = statePath
	enableKnowledgeTestPolicy(t, first, ctx)
	if _, err := first.GroupKnowledgeAdd(ctx, "怎么绑定", "在设置里绑定", "exact", nil, nil); err != nil {
		t.Fatalf("add: %v", err)
	}

	second := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	second.statePath = statePath
	second.loadRuntimeStateAtStartup()
	entries, err := second.GroupKnowledgeList(ctx)
	if err != nil {
		t.Fatalf("list after reload: %v", err)
	}
	if len(entries) != 1 || entries[0].Question != "怎么绑定" {
		t.Fatalf("reloaded entries = %#v", entries)
	}
}

func TestGroupKnowledgeCommandModes(t *testing.T) {
	adapter := &fakePlatform{}
	fake := &fakeLLM{}
	a := New(adapter, fake, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := knowledgeTestContext()
	enableKnowledgeTestPolicy(t, a, ctx)

	if err := a.HandleMessage(ctx, "/faq add-contains 绑定 => contains answer"); err != nil {
		t.Fatalf("add-contains: %v", err)
	}
	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "请问这个绑定怎么操作？"); err != nil {
		t.Fatalf("contains question: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "contains answer") {
		t.Fatalf("contains output = %q", adapter.out.String())
	}
	if len(fake.chatRequests()) != 0 {
		t.Fatalf("contains hit must not call LLM")
	}

	if err := a.HandleMessage(ctx, "/faq add-keyword 退款,流程 => keyword answer"); err != nil {
		t.Fatalf("add-keyword: %v", err)
	}
	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "退款流程是什么？"); err != nil {
		t.Fatalf("keyword question: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "keyword answer") {
		t.Fatalf("keyword output = %q", adapter.out.String())
	}
	if len(fake.chatRequests()) != 0 {
		t.Fatalf("keyword hit must not call LLM")
	}
}
