package agent

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	"elbot/internal/llm"
	"elbot/internal/toolrun"
)

// authorizeToolCall is the server-side execution gate for group tool policy.
// It is intentionally independent of prompt text and tool arguments: the
// allowlist is loaded from state for the current group scope.
func (a *Agent) authorizeToolCall(ctx context.Context, call llm.ToolCallRequest, resolved toolrun.ResolvedTool) (bool, string) {
	allowlist := a.groupToolAllowlist(ctx)
	if allowlist == nil {
		return true, ""
	}
	for _, name := range toolAuthorizationNames(call, resolved) {
		if name == "" {
			continue
		}
		if allowlist[strings.ToLower(strings.TrimSpace(name))] {
			return true, ""
		}
	}
	return false, "tool is not in this group's allowlist"
}

func toolAuthorizationNames(call llm.ToolCallRequest, resolved toolrun.ResolvedTool) []string {
	names := make([]string, 0, 4)
	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	appendName(call.Name)
	appendName(resolved.Name)
	if resolved.Cached != nil {
		appendName(resolved.Cached.Name)
		appendName(resolved.Cached.CanonicalName)
	}
	return names
}

// groupToolAllowlist returns the expanded concrete tool names allowed in the
// current group. nil means "inherit the global tool policy" (unrestricted);
// a non-nil empty map means "deny all group tools".
func (a *Agent) groupToolAllowlist(ctx context.Context) map[string]bool {
	if a == nil || !a.isGroupScope(ctx) {
		return nil
	}
	policy := a.groupPolicyForScope(a.scope(ctx))
	if !policy.ToolAllowlistRestricted() {
		return nil
	}
	allowed := map[string]bool{}
	for _, value := range policy.ToolAllowlist {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "", "none", "deny", "denyall":
			continue
		case "*", "all":
			return nil
		}
		profileName := value
		if resolved, ok := a.toolAliases[value]; ok {
			profileName = strings.ToLower(strings.TrimSpace(resolved))
		}
		if tools := a.toolProfiles[profileName]; len(tools) > 0 {
			for _, toolName := range tools {
				toolName = strings.ToLower(strings.TrimSpace(toolName))
				if toolName != "" {
					allowed[toolName] = true
				}
			}
			continue
		}
		allowed[value] = true
	}
	return allowed
}

func (a *Agent) beforeExecuteToolCall(ctx context.Context, call llm.ToolCallRequest, resolved toolrun.ResolvedTool) error {
	if a == nil || a.platform == nil {
		return nil
	}
	// Final authorization check immediately before quota reservation and
	// execution. The runner already checked after queueing, but a policy
	// update can land between that check and this callback.
	if allowed, reason := a.authorizeToolCall(ctx, call, resolved); !allowed {
		return fmt.Errorf("工具授权已变更：%s", reason)
	}
	policy := a.groupPolicyForScope(a.scope(ctx))
	switch {
	case !policy.IsGroupAnalysisEnabled() && toolNamesInclude(call, resolved, "group_analysis"):
		return fmt.Errorf("本群已关闭群分析")
	case !policy.IsHistoryEnabled() && toolNamesInclude(call, resolved, "search_chat_history", "get_chat_history_around", "reply_to_chat_history_message"):
		return fmt.Errorf("本群已关闭历史记录")
	case !policy.IsLearningEnabled() && toolNamesInclude(call, resolved, "self_learning_review"):
		return fmt.Errorf("本群已关闭学习观察")
	}
	if toolNamesInclude(call, resolved, "group_analysis") && strings.TrimSpace(a.groupAnalysisModel.Provider) != "" && strings.TrimSpace(a.groupAnalysisModel.Model) != "" {
		if err := a.authorizeExecutionModelSelection(ctx, a.groupAnalysisModel); err != nil {
			return fmt.Errorf("本群模型目录不允许群分析摘要模型：%w", err)
		}
	}
	kind := toolExecutionQuotaKind(call, resolved)
	if kind == "" {
		return nil
	}
	if kind == "vision" && strings.TrimSpace(a.visionSelection.Provider) != "" && strings.TrimSpace(a.visionSelection.Model) != "" {
		if err := a.authorizeExecutionModelSelection(ctx, a.visionSelection); err != nil {
			return fmt.Errorf("本群模型目录不允许视觉模型：%w", err)
		}
	}
	callID := strings.TrimSpace(call.ID)
	executionID := callID
	if callID == "" {
		callID = shortHash(kind + "\x00" + call.Name + "\x00" + call.Arguments)
	}
	digest := shortHash(kind + "\x00" + strings.ToLower(strings.TrimSpace(call.Name)) + "\x00" + compactArguments(call.Arguments))
	if ok, reason := a.reserveToolExecution(ctx, executionID, digest); !ok {
		return fmt.Errorf("工具执行幂等校验失败：%s", reason)
	}
	ok, reason := a.reserveBudgetRequest(ctx, kind, budgetReservationRequest{CallID: callID, Digest: digest})
	if !ok {
		a.releaseToolExecution(ctx, executionID)
		return fmt.Errorf("受限调用被额度账本拒绝：%s", reason)
	}
	return nil
}

func (a *Agent) filterGroupFeatureSchemas(ctx context.Context, schemas []llm.ToolSchema) []llm.ToolSchema {
	if a == nil || !a.isGroupScope(ctx) || len(schemas) == 0 {
		return schemas
	}
	policy := a.groupPolicyForScope(a.scope(ctx))
	out := make([]llm.ToolSchema, 0, len(schemas))
	for _, schema := range schemas {
		name := strings.ToLower(strings.TrimSpace(schema.Function.Name))
		switch {
		case !policy.IsGroupAnalysisEnabled() && name == "group_analysis":
			continue
		case !policy.IsHistoryEnabled() && isHistoryToolName(name):
			continue
		case !policy.IsLearningEnabled() && name == "self_learning_review":
			continue
		}
		out = append(out, schema)
	}
	return out
}

func isHistoryToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "search_chat_history", "get_chat_history_around", "reply_to_chat_history_message":
		return true
	default:
		return false
	}
}

func toolNamesInclude(call llm.ToolCallRequest, resolved toolrun.ResolvedTool, want ...string) bool {
	for _, name := range toolAuthorizationNames(call, resolved) {
		for _, candidate := range want {
			if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(candidate)) {
				return true
			}
		}
	}
	return false
}

func toolExecutionQuotaKind(call llm.ToolCallRequest, resolved toolrun.ResolvedTool) string {
	for _, name := range toolAuthorizationNames(call, resolved) {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "image_generate":
			return "image"
		case "image_to_prompt":
			return "vision"
		}
	}
	return ""
}

func shortHash(value string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return fmt.Sprintf("%x", h.Sum32())
}
