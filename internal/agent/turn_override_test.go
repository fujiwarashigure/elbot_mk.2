package agent

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/security"
	"elbot/internal/tool"
	"elbot/internal/tool/builtin"
)

func TestTurnModelOverrideAppliesForOneTurn(t *testing.T) {
	ctx := context.Background()
	p := &fakePlatform{}
	store := newTestStore(t)
	f := &fakeLLM{replies: []string{"one", "two"}}
	a := New(p, f, "default-model", config.ProviderConfig{}, store)
	a.SetSecurityPolicy(security.NewPolicy("low", "high", map[string][]string{"cli": {"local"}}))
	a.modelProfiles = map[string]config.ModelSelection{
		"cheap": {Provider: "default", Model: "cheap-model"},
	}

	if err := a.HandleMessage(ctx, "@model:cheap 你好"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	requests := f.chatRequests()
	if len(requests) == 0 || requests[0].Model != "cheap-model" {
		t.Fatalf("first turn model = %#v", requests)
	}
	if notice := p.out.String(); !strings.Contains(notice, "本轮已启用：model:cheap") {
		t.Fatalf("notice = %q", notice)
	}

	if err := a.HandleMessage(ctx, "再来一条"); err != nil {
		t.Fatalf("HandleMessage second: %v", err)
	}
	requests = f.chatRequests()
	last := requests[len(requests)-1]
	if last.Model != "default-model" {
		t.Fatalf("second turn should fall back to the default model, got %q", last.Model)
	}
}

func TestTurnOverrideDeniedForRegularUser(t *testing.T) {
	ctx := context.Background()
	p := &fakePlatform{}
	store := newTestStore(t)
	f := &fakeLLM{replies: []string{"done"}}
	a := New(p, f, "default-model", config.ProviderConfig{}, store)
	a.SetSecurityPolicy(security.NewPolicy("low", "high", nil))
	a.modelProfiles = map[string]config.ModelSelection{
		"cheap": {Provider: "default", Model: "cheap-model"},
	}

	if err := a.HandleMessage(ctx, "@model:cheap 你好"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if notice := p.out.String(); !strings.Contains(notice, "仅超级管理员可以声明") {
		t.Fatalf("notice = %q", notice)
	}
	requests := f.chatRequests()
	if len(requests) == 0 || requests[0].Model != "default-model" {
		t.Fatalf("regular user must not switch models: %#v", requests)
	}
}

func TestTurnToolProfileInjectsSchemasForOneTurn(t *testing.T) {
	ctx := context.Background()
	p := &fakePlatform{}
	store := newTestStore(t)
	f := &fakeLLM{replies: []string{"one", "two"}}
	a := New(p, f, "m", config.ProviderConfig{}, store)
	a.SetSecurityPolicy(security.NewPolicy("low", "high", map[string][]string{"cli": {"local"}}))
	registry := tool.NewRegistry()
	_ = registry.Register(tool.NewDiscoverTool(registry))
	_ = registry.Register(builtin.NewWebSearchTool())
	a.SetToolRuntime(registry, nil)
	a.toolProfiles = map[string][]string{"web": {"web_search"}}

	if err := a.HandleMessage(ctx, "@use:web 查一下"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	requests := f.chatRequests()
	if len(requests) == 0 || !strings.Contains(toolNames(requests[0].Tools), "web_search") {
		t.Fatalf("first turn tools = %s", toolNames(requests[0].Tools))
	}

	if err := a.HandleMessage(ctx, "再来一条"); err != nil {
		t.Fatalf("HandleMessage second: %v", err)
	}
	requests = f.chatRequests()
	last := requests[len(requests)-1]
	if strings.Contains(toolNames(last.Tools), "web_search") {
		t.Fatalf("tool profile leaked into the next turn: %s", toolNames(last.Tools))
	}
}

func TestTurnOverrideInvalidProfile(t *testing.T) {
	ctx := context.Background()
	p := &fakePlatform{}
	store := newTestStore(t)
	f := &fakeLLM{replies: []string{"done"}}
	a := New(p, f, "m", config.ProviderConfig{}, store)
	a.SetSecurityPolicy(security.NewPolicy("low", "high", map[string][]string{"cli": {"local"}}))

	if err := a.HandleMessage(ctx, "@image:nope 画一张"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if notice := p.out.String(); !strings.Contains(notice, "未配置的 profile：image:nope") {
		t.Fatalf("notice = %q", notice)
	}
}

func TestTurnOverrideChineseAliasAndHashPrefix(t *testing.T) {
	ctx := context.Background()
	p := &fakePlatform{}
	store := newTestStore(t)
	f := &fakeLLM{replies: []string{"one", "two"}}
	a := New(p, f, "default-model", config.ProviderConfig{}, store)
	a.SetSecurityPolicy(security.NewPolicy("low", "high", map[string][]string{"cli": {"local"}}))
	a.modelProfiles = map[string]config.ModelSelection{"pro": {Provider: "default", Model: "pro-model"}}
	a.modelAliases = map[string]string{"pro": "pro", "强": "pro", "强模型": "pro"}
	a.imageProfiles = map[string]bool{"hq": true}
	a.imageAliases = map[string]string{"hq": "hq", "高清": "hq"}
	a.toolProfiles = map[string][]string{"admin": {"web_search"}}
	a.toolAliases = map[string]string{"admin": "admin", "管理": "admin"}

	if err := a.HandleMessage(ctx, "#模型:强 #生图:高清 #工具:管理 帮我看看"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	requests := f.chatRequests()
	if len(requests) == 0 || requests[0].Model != "pro-model" {
		t.Fatalf("model = %#v", requests)
	}
	notice := p.out.String()
	for _, want := range []string{"model:pro", "image:hq", "use:admin"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice %q missing %q", notice, want)
		}
	}
}
