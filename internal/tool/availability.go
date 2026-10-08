package tool

import (
	"context"
	"strings"

	"elbot/internal/security"
)

// 本文件定义"工具为什么没被采用"的机器可读原因，以及产出它的唯一判定入口。
//
// 此前预载与发现路径把四类完全不同的判定压成一个自由字符串
// reason="not_found_or_not_allowed"：注册表里没有、当前上下文不可用（后台专用/需要视觉
// 能力）、角色或策略拒绝、被隐藏。事后从审计日志无法区分"策略拒绝"与"工具不存在"，
// 也无法据此统计拒绝率。判定顺序与 CanAccessTool / InfoAvailableInContext 保持一致，
// 保证同一个工具在任何调用点得到同一个原因。
//
// 原因字符串只表达判定事实，不表达日志等级；等级与操作结果由来源（agent）决定。
const (
	// ToolReasonNotFound 表示注册表里没有这个名字。
	ToolReasonNotFound = "tool_not_found"
	// ToolReasonContextUnavailable 表示工具在当前上下文不可用（ForegroundOnly 工具在
	// 后台上下文中，或 VisionRequired 工具在没有视觉能力的模型上）。
	ToolReasonContextUnavailable = "tool_context_unavailable"
	// ToolReasonHidden 表示工具是隐藏工具，不在当前路径的暴露范围内。
	ToolReasonHidden = "tool_hidden"
	// ToolReasonSuperadminOnly 表示工具仅超级管理员可用，而当前 actor 不是超管。
	ToolReasonSuperadminOnly = "tool_requires_superadmin"
	// ToolReasonRiskDenied 表示工具风险等级高于策略允许的上限。
	ToolReasonRiskDenied = "tool_risk_above_allowed_level"
	// ToolReasonNoSchema 表示工具解析完成但没有可用 schema，无法注入模型。
	ToolReasonNoSchema = "tool_no_schema"
)

// ToolAvailabilityReason 报告工具在当前上下文与策略下是否可用；可用时返回空字符串。
// 它把 CanAccessTool 与 InfoAvailableInContext 的判定拆成互斥的机器可读原因，供
// 审计日志和预载路径使用。
func ToolAvailabilityReason(ctx context.Context, actor security.Actor, policy *security.Policy, candidate Tool) string {
	if candidate == nil {
		return ToolReasonNotFound
	}
	info := candidate.Info()
	if !InfoAvailableInContext(ctx, info) {
		return ToolReasonContextUnavailable
	}
	return ToolAccessReason(actor, policy, info)
}

// ToolAccessReason 只报告角色与策略层面的判定原因，不检查上下文可用性。
func ToolAccessReason(actor security.Actor, policy *security.Policy, info Info) string {
	if info.SuperadminOnly && actor.Role != security.RoleSuperadmin {
		return ToolReasonSuperadminOnly
	}
	if policy == nil {
		policy = security.DefaultPolicy()
	}
	if !policy.CanUseTool(actor, normalizeRisk(info.Risk, RiskHigh), info.OwnerScoped) {
		return ToolReasonRiskDenied
	}
	return ""
}

// RegistryToolAvailabilityReason 按名字从注册表取工具并报告不可用原因。名字不存在时
// 返回 ToolReasonNotFound；存在但隐藏时返回 ToolReasonHidden（隐藏工具不应出现在
// 允许暴露隐藏工具的路径之外的审计里）。
func RegistryToolAvailabilityReason(ctx context.Context, registry *Registry, actor security.Actor, policy *security.Policy, name string, allowHidden bool) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ToolReasonNotFound
	}
	if registry == nil {
		return ToolReasonNotFound
	}
	candidate, ok := registry.Get(name)
	if !ok {
		return ToolReasonNotFound
	}
	if reason := ToolAvailabilityReason(ctx, actor, policy, candidate); reason != "" {
		return reason
	}
	if candidate.Info().Hidden && !allowHidden {
		return ToolReasonHidden
	}
	return ""
}
