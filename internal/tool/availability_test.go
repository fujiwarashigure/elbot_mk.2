package tool

import (
	"context"
	"testing"

	"elbot/internal/llm"
	"elbot/internal/security"
)

// availabilityTool 复用 registry_test.go 的 fakeTool，只覆盖 Info 中 fakeTool 无法表达
// 的字段（ForegroundOnly / VisionRequired）。
type availabilityTool struct {
	fakeTool
	info Info
}

func (t availabilityTool) Info() Info { return t.info }

func (t availabilityTool) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.ToolFunctionSchema{Name: t.info.Name, Parameters: map[string]any{"type": "object"}}}
}

func TestToolAvailabilityReasonDistinguishesRejectionFromMissing(t *testing.T) {
	ctx := context.Background()
	actor := security.Actor{ID: "u1", Platform: "cli", Role: security.RoleUser}
	policy := security.NewPolicy("low", "critical", map[string][]string{"cli": {"local"}})
	backgroundCtx := WithSandboxContext(ctx, SandboxContext{Root: "s", Dir: "s", Background: true})

	cases := []struct {
		name      string
		ctx       context.Context
		candidate Tool
		want      string
	}{
		{
			name:      "missing tool",
			ctx:       ctx,
			candidate: nil,
			want:      ToolReasonNotFound,
		},
		{
			name:      "foreground only tool in background",
			ctx:       backgroundCtx,
			candidate: availabilityTool{info: Info{Name: "workspace", Risk: RiskLow, ForegroundOnly: true}},
			want:      ToolReasonContextUnavailable,
		},
		{
			// 能力未声明时 VisionAvailable 保持历史行为（true），因此必须显式声明
			// vision=false 才构成"当前上下文不可用"。
			name:      "vision required without capability",
			ctx:       WithCapabilities(ctx, Capabilities{Vision: false}),
			candidate: availabilityTool{info: Info{Name: "view_image", Risk: RiskLow, VisionRequired: true}},
			want:      ToolReasonContextUnavailable,
		},
		{
			name:      "superadmin only",
			ctx:       ctx,
			candidate: availabilityTool{info: Info{Name: "admin", Risk: RiskLow, SuperadminOnly: true}},
			want:      ToolReasonSuperadminOnly,
		},
		{
			name:      "risk above policy",
			ctx:       ctx,
			candidate: fakeTool{name: "shell", risk: RiskHigh},
			want:      ToolReasonRiskDenied,
		},
		{
			name:      "allowed",
			ctx:       ctx,
			candidate: fakeTool{name: "web", risk: RiskLow},
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToolAvailabilityReason(tc.ctx, actor, policy, tc.candidate); got != tc.want {
				t.Fatalf("ToolAvailabilityReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestToolAvailabilityReasonMatchesExistingGates 固定新原因入口与既有判定入口
// （InfoAvailableInContext + CanAccessTool）的等价性：迁移调用点时不允许顺带改变准入。
func TestToolAvailabilityReasonMatchesExistingGates(t *testing.T) {
	ctx := context.Background()
	policy := security.NewPolicy("low", "critical", map[string][]string{"cli": {"local"}})
	candidates := []Tool{
		fakeTool{name: "web", risk: RiskLow},
		fakeTool{name: "shell", risk: RiskHigh},
		fakeTool{name: "admin", risk: RiskLow, superadminOnly: true},
		fakeTool{name: "hidden", risk: RiskLow, hidden: true},
		availabilityTool{info: Info{Name: "workspace", Risk: RiskLow, ForegroundOnly: true}},
		availabilityTool{info: Info{Name: "view_image", Risk: RiskLow, VisionRequired: true}},
	}
	actors := []security.Actor{
		{ID: "u1", Platform: "cli", Role: security.RoleUser},
		{ID: "cli:local", Platform: "cli", Role: security.RoleSuperadmin},
	}
	contexts := []context.Context{
		ctx,
		WithSandboxContext(ctx, SandboxContext{Root: "s", Dir: "s", Background: true}),
		WithCapabilities(ctx, Capabilities{Vision: false}),
	}
	for _, actor := range actors {
		for _, candidate := range candidates {
			info := candidate.Info()
			for _, testCtx := range contexts {
				available := InfoAvailableInContext(testCtx, info) && CanAccessTool(actor, policy, info)
				reason := ToolAvailabilityReason(testCtx, actor, policy, candidate)
				if available != (reason == "") {
					t.Fatalf("actor=%s tool=%s background=%v: available=%v reason=%q",
						actor.Role, info.Name, BackgroundContext(testCtx), available, reason)
				}
			}
		}
	}
}

func TestRegistryToolAvailabilityReasonCoversHiddenAndMissing(t *testing.T) {
	ctx := context.Background()
	actor := security.Actor{ID: "cli:local", Platform: "cli", Role: security.RoleSuperadmin}
	policy := security.NewPolicy("low", "critical", map[string][]string{"cli": {"local"}})
	registry := NewRegistry()
	_ = registry.Register(fakeTool{name: "visible", risk: RiskLow})
	_ = registry.Register(fakeTool{name: "hidden", risk: RiskLow, hidden: true})

	cases := []struct {
		name        string
		toolName    string
		allowHidden bool
		want        string
	}{
		{name: "missing", toolName: "nope", want: ToolReasonNotFound},
		{name: "empty name", toolName: "  ", want: ToolReasonNotFound},
		{name: "hidden not allowed", toolName: "hidden", allowHidden: false, want: ToolReasonHidden},
		{name: "hidden allowed", toolName: "hidden", allowHidden: true, want: ""},
		{name: "visible", toolName: "visible", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RegistryToolAvailabilityReason(ctx, registry, actor, policy, tc.toolName, tc.allowHidden); got != tc.want {
				t.Fatalf("RegistryToolAvailabilityReason = %q, want %q", got, tc.want)
			}
		})
	}
	if got := RegistryToolAvailabilityReason(ctx, nil, actor, policy, "visible", false); got != ToolReasonNotFound {
		t.Fatalf("nil registry reason = %q", got)
	}
}
