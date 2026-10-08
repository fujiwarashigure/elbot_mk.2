package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/logging"
	"elbot/internal/security"
	"elbot/internal/session"
)

func cloneGroupPolicy(input map[string]config.GroupPolicyConfig) map[string]config.GroupPolicyConfig {
	if len(input) == 0 {
		return map[string]config.GroupPolicyConfig{}
	}
	out := make(map[string]config.GroupPolicyConfig, len(input))
	for key, value := range input {
		out[key] = value.Normalize()
	}
	return out
}

func (a *Agent) setGroupPolicySnapshot(snapshot map[string]config.GroupPolicyConfig) {
	a.groupPolicyMu.Lock()
	a.groupPolicy = cloneGroupPolicy(snapshot)
	a.groupPolicyMu.Unlock()
}

func (a *Agent) groupPolicySnapshot() map[string]config.GroupPolicyConfig {
	a.groupPolicyMu.RLock()
	defer a.groupPolicyMu.RUnlock()
	return cloneGroupPolicy(a.groupPolicy)
}

func (a *Agent) groupPolicyForScope(scope session.Scope) config.GroupPolicyConfig {
	key := contextOverflowKey(scope)
	a.groupPolicyMu.RLock()
	defer a.groupPolicyMu.RUnlock()
	return a.groupPolicy[key].Normalize()
}

func (a *Agent) setGroupPolicyForScope(scope session.Scope, value config.GroupPolicyConfig) {
	key := contextOverflowKey(scope)
	value = value.Normalize()
	a.groupPolicyMu.Lock()
	defer a.groupPolicyMu.Unlock()
	if a.groupPolicy == nil {
		a.groupPolicy = map[string]config.GroupPolicyConfig{}
	}
	if value.IsZero() {
		delete(a.groupPolicy, key)
		return
	}
	a.groupPolicy[key] = value
}

func (a *Agent) isGroupScope(ctx context.Context) bool {
	if a == nil || a.platform == nil {
		return false
	}
	scope := a.scope(ctx)
	scopeID := strings.TrimSpace(scope.PlatformScopeID)
	return strings.HasPrefix(scopeID, "group:") || strings.HasPrefix(scopeID, "supergroup:")
}

func (a *Agent) canManageCurrentGroupPolicy(ctx context.Context) bool {
	if a == nil || !a.isGroupScope(ctx) {
		return false
	}
	actor := a.actor(ctx)
	if actor.Role == security.RoleSuperadmin {
		return true
	}
	return actor.GroupRole == security.GroupRoleOwner || actor.GroupRole == security.GroupRoleAdmin
}

func (a *Agent) GroupPolicyStatus(ctx context.Context) string {
	scope := a.scope(ctx)
	key := contextOverflowKey(scope)
	if !a.isGroupScope(ctx) {
		return "群级策略只适用于群聊；当前不是群聊作用域。"
	}
	policy := a.groupPolicyForScope(scope)
	quiet := policy.QuietHours
	if quiet == "" {
		quiet = "未设置"
	}
	var sb strings.Builder
	sb.WriteString("群级策略\n")
	sb.WriteString(fmt.Sprintf("作用域：%s\n", key))
	sb.WriteString(fmt.Sprintf("唤醒词：%s\n", displayStringList(policy.WakeKeywords)))
	sb.WriteString(fmt.Sprintf("响应模式：%s（mention=默认，all=全部，keyword=仅唤醒词，reply=仅回复，off=关闭）\n", policy.ResponseModeValue()))
	sb.WriteString(fmt.Sprintf("会话线程：%s（per_user=每人独立，group=全群共享并串行）\n", policy.ThreadModeValue()))
	sb.WriteString(fmt.Sprintf("连续消息合并窗口：%s\n", mergeWindowText(policy.MergeWindowMS)))
	defaultMode := policy.DefaultMode
	if defaultMode == "" {
		defaultMode = "继承全局"
	}
	sb.WriteString(fmt.Sprintf("默认会话模式：%s\n", defaultMode))
	defaultModel := policy.DefaultModel
	if defaultModel == "" {
		defaultModel = "继承全局"
	}
	sb.WriteString(fmt.Sprintf("默认模型别名：%s\n", defaultModel))
	allowedModels := displayStringList(policy.AllowedModels)
	if len(policy.AllowedModels) == 0 {
		allowedModels = "默认（仅已配置模型别名/profile）"
	}
	sb.WriteString(fmt.Sprintf("群可选模型目录：%s\n", allowedModels))
	toolText := "继承全局"
	switch {
	case policy.ToolAllowlistRestricted() && len(policy.ToolAllowlist) == 0:
		toolText = "空（当前群禁止全部工具）"
	case policy.ToolAllowlistRestricted():
		toolText = displayStringList(policy.ToolAllowlist)
	}
	sb.WriteString(fmt.Sprintf("工具白名单：%s\n", toolText))
	imageUsed, visionUsed := a.budgetUsageForScope(ctx)
	asrUsed := a.asrBudgetUsageForScope(ctx)
	sb.WriteString(fmt.Sprintf("生图日额度：%s\n", budgetText(policy.ImageQuota, imageUsed)))
	sb.WriteString(fmt.Sprintf("视觉日额度：%s\n", budgetText(policy.VisionQuota, visionUsed)))
	sb.WriteString(fmt.Sprintf("语音转写日额度：%s\n", budgetText(policy.ASRQuota, asrUsed)))
	sb.WriteString(fmt.Sprintf("单用户生图日额度：%s\n", budgetText(policy.UserImageQuota, 0)))
	sb.WriteString(fmt.Sprintf("单用户视觉日额度：%s\n", budgetText(policy.UserVisionQuota, 0)))
	sb.WriteString(fmt.Sprintf("单用户语音转写日额度：%s\n", budgetText(policy.UserASRQuota, 0)))
	chatTokens, chatCosts := a.chatBudgetUsageForScope(ctx)
	sb.WriteString(fmt.Sprintf("群日聊天 token 额度：%s\n", int64BudgetText(policy.ChatTokensQuota, chatTokens)))
	sb.WriteString(fmt.Sprintf("群日聊天费用额度：%s\n", costBudgetText(policy.ChatCostQuota, chatCosts)))
	sb.WriteString(fmt.Sprintf("静默时段：%s\n", quiet))
	sb.WriteString(fmt.Sprintf("群分析：%s；学习：%s；历史记录：%s；知识库：%s；语音转写：%s；提醒/投票/报名：%s\n", enabledText(policy.IsGroupAnalysisEnabled()), enabledText(policy.IsLearningEnabled()), enabledText(policy.IsHistoryEnabled()), enabledText(policy.IsKnowledgeEnabled()), enabledText(policy.IsASREnabled()), enabledText(policy.IsServicesEnabled())))
	actions := policy.LearningModerationActionsValue()
	if len(actions) == 0 {
		actions = []string{"无"}
	}
	sb.WriteString(fmt.Sprintf("群管理员学习审核授权：%s（权限：%s）\n", enabledText(policy.LearningModeration), strings.Join(actions, "、")))
	sb.WriteString("修改：/grouppolicy <field> <value>；重置：/grouppolicy reset [field]")
	return sb.String()
}

func budgetText(limit int, used int64) string {
	if limit <= 0 {
		return fmt.Sprintf("已用 %d，未设置上限", used)
	}
	remaining := int64(limit) - used
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("已用 %d / %d（剩余 %d）", used, limit, remaining)
}

func int64BudgetText(limit, used int64) string {
	if limit <= 0 {
		return fmt.Sprintf("已用 %d，未设置上限", used)
	}
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("已用 %d / %d（剩余 %d）", used, limit, remaining)
}

func costBudgetText(limit float64, usedMicros int64) string {
	used := float64(usedMicros) / 1_000_000
	if limit <= 0 {
		return fmt.Sprintf("已用 %.6f，未设置上限", used)
	}
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("已用 %.6f / %.6f（剩余 %.6f）", used, limit, remaining)
}

func (a *Agent) rawGroupPolicy(scope session.Scope) config.GroupPolicyConfig {
	if a == nil {
		return config.GroupPolicyConfig{}
	}
	key := contextOverflowKey(scope)
	a.groupPolicyMu.RLock()
	defer a.groupPolicyMu.RUnlock()
	return a.groupPolicy[key]
}

func (a *Agent) groupMergeWindow(ctx context.Context) time.Duration {
	if a == nil {
		return 0
	}
	scope := a.baseScope(ctx)
	policy := a.rawGroupPolicy(scope)
	ms := policy.MergeWindowMS
	if ms <= 0 {
		return 0
	}
	if ms > 10000 {
		ms = 10000
	}
	return time.Duration(ms) * time.Millisecond
}

func mergeWindowText(ms int) string {
	if ms <= 0 {
		return "关闭"
	}
	return fmt.Sprintf("%d ms", ms)
}

func displayStringList(values []string) string {
	if len(values) == 0 {
		return "无"
	}
	return strings.Join(values, "、")
}

func enabledText(value bool) string {
	if value {
		return "开"
	}
	return "关"
}

func groupPolicyFieldAuditValue(policy config.GroupPolicyConfig, field string) string {
	policy = policy.Normalize()
	switch field {
	case "wake":
		return displayStringList(policy.WakeKeywords)
	case "response":
		return policy.ResponseModeValue()
	case "thread-mode":
		return policy.ThreadModeValue()
	case "merge-window":
		return strconv.Itoa(policy.MergeWindowMS)
	case "default-mode":
		if policy.DefaultMode == "" {
			return "inherit"
		}
		return policy.DefaultMode
	case "default-model":
		return policy.DefaultModel
	case "allowed-models":
		return displayStringList(policy.AllowedModels)
	case "tool-allow":
		if !policy.ToolAllowlistRestricted() {
			return "inherit"
		}
		if len(policy.ToolAllowlist) == 0 {
			return "none"
		}
		return displayStringList(policy.ToolAllowlist)
	case "image-quota":
		return strconv.Itoa(policy.ImageQuota)
	case "vision-quota":
		return strconv.Itoa(policy.VisionQuota)
	case "asr-quota":
		return strconv.Itoa(policy.ASRQuota)
	case "user-image-quota":
		return strconv.Itoa(policy.UserImageQuota)
	case "user-vision-quota":
		return strconv.Itoa(policy.UserVisionQuota)
	case "user-asr-quota":
		return strconv.Itoa(policy.UserASRQuota)
	case "chat-tokens-quota":
		return strconv.FormatInt(policy.ChatTokensQuota, 10)
	case "chat-cost-quota":
		return strconv.FormatFloat(policy.ChatCostQuota, 'f', -1, 64)
	case "quiet":
		return policy.QuietHours
	case "analysis":
		return enabledText(policy.IsGroupAnalysisEnabled())
	case "learning":
		return enabledText(policy.IsLearningEnabled())
	case "history":
		return enabledText(policy.IsHistoryEnabled())
	case "knowledge":
		return enabledText(policy.IsKnowledgeEnabled())
	case "asr":
		return enabledText(policy.IsASREnabled())
	case "services":
		return enabledText(policy.IsServicesEnabled())
	case "learning-moderation":
		return enabledText(policy.LearningModeration)
	case "learning-moderation-actions":
		return displayStringList(policy.LearningModerationActionsValue())
	default:
		return ""
	}
}

func (a *Agent) SetGroupPolicy(ctx context.Context, field, value string) (string, error) {
	if !a.canManageCurrentGroupPolicy(ctx) {
		if !a.isGroupScope(ctx) {
			return "", fmt.Errorf("群级策略只能由当前群的群主/管理员修改")
		}
		return "", fmt.Errorf("需要当前群群主/管理员或机器人超级管理员权限")
	}
	field = canonicalGroupPolicyField(field)
	if field == "" {
		return "", fmt.Errorf("未知的群策略字段")
	}
	scope := a.scope(ctx)
	policyKey := contextOverflowKey(scope)
	previous := a.groupPolicySnapshot()
	previousPolicy := previous[policyKey]
	policy := a.groupPolicyForScope(scope)
	policyValue := value
	actor := a.actor(ctx)
	switch field {
	case "allowed-models", "learning-moderation", "learning-moderation-actions":
		if actor.Role != security.RoleSuperadmin {
			return "", fmt.Errorf("%s 只能由机器人超级管理员配置；请让超级管理员在本群执行该命令", field)
		}
	}
	switch field {
	case "wake":
		policy.WakeKeywords = splitGroupList(value)
	case "response":
		mode := strings.ToLower(strings.TrimSpace(value))
		switch mode {
		case "mention", "all", "keyword", "keywords", "reply", "replies", "off", "none", "disabled":
			policy.ResponseMode = mode
		default:
			return "", fmt.Errorf("无效的响应模式 %q，可选：mention、all、keyword、reply、off", value)
		}
	case "thread-mode":
		mode := strings.ToLower(strings.TrimSpace(value))
		switch mode {
		case "group", "shared", "multi", "multi_user", "multi-user":
			policy.ThreadMode = config.ThreadModeGroup
		case "per_user", "per-user", "single", "isolated", "inherit", "clear", "reset", "default":
			policy.ThreadMode = ""
		default:
			return "", fmt.Errorf("无效的会话线程模式 %q，可选：per-user、group", value)
		}
	case "merge-window":
		ms, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || ms < 0 || ms > 10000 {
			return "", fmt.Errorf("连续消息合并窗口必须是 0-10000 之间的整数毫秒")
		}
		policy.MergeWindowMS = ms
	case "default-mode":
		mode := strings.ToLower(strings.TrimSpace(value))
		switch mode {
		case "inherit", "clear", "reset", "default":
			policy.DefaultMode = ""
		case "work", "chat":
			policy.DefaultMode = mode
		default:
			return "", fmt.Errorf("无效的默认会话模式 %q，可选：work、chat、inherit", value)
		}
	case "default-model":
		if strings.EqualFold(strings.TrimSpace(value), "clear") || strings.EqualFold(strings.TrimSpace(value), "inherit") {
			policy.DefaultModel = ""
			break
		}
		selection, ok := a.resolveGroupModelSelection(value)
		if !ok {
			return "", fmt.Errorf("未知或不可用的模型 %q，请使用已配置的模型别名或 provider/model", strings.TrimSpace(value))
		}
		if !a.groupModelSelectionAllowed(policy, selection, value) {
			return "", fmt.Errorf("模型 %q 不在本群可选模型目录内；请让超级管理员先配置 /grouppolicy allowed-models", strings.TrimSpace(value))
		}
		policy.DefaultModel = strings.TrimSpace(value)
	case "tool-allow":
		switch {
		case strings.EqualFold(strings.TrimSpace(value), "none") || strings.EqualFold(strings.TrimSpace(value), "deny") || strings.EqualFold(strings.TrimSpace(value), "denyall"):
			policy.ToolAllowlist = nil
			policy.ToolAllowlistSet = true
		case isClearValue(value):
			policy.ToolAllowlist = nil
			policy.ToolAllowlistSet = false
		default:
			policy.ToolAllowlist = splitGroupList(value)
			policy.ToolAllowlistSet = true
		}
	case "allowed-models":
		if isClearValue(value) {
			policy.AllowedModels = nil
			break
		}
		values := splitGroupList(value)
		if len(values) == 0 {
			return "", fmt.Errorf("allowed-models 不能为空；可发送 clear 恢复默认目录")
		}
		for _, item := range values {
			if item == "*" {
				continue
			}
			if _, ok := a.resolveGroupModelSelection(item); !ok {
				return "", fmt.Errorf("allowed-models 中的 %q 不是已配置的模型别名或 provider/model", item)
			}
		}
		policy.AllowedModels = values
	case "image-quota":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return "", fmt.Errorf("生图日额度必须是非负整数")
		}
		policy.ImageQuota = n
	case "vision-quota":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return "", fmt.Errorf("视觉日额度必须是非负整数")
		}
		policy.VisionQuota = n
	case "asr-quota":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return "", fmt.Errorf("语音转写日额度必须是非负整数")
		}
		policy.ASRQuota = n
	case "user-image-quota":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return "", fmt.Errorf("单用户生图日额度必须是非负整数")
		}
		policy.UserImageQuota = n
	case "user-vision-quota":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return "", fmt.Errorf("单用户视觉日额度必须是非负整数")
		}
		policy.UserVisionQuota = n
	case "user-asr-quota":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return "", fmt.Errorf("单用户语音转写日额度必须是非负整数")
		}
		policy.UserASRQuota = n
	case "chat-tokens-quota":
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || n < 0 {
			return "", fmt.Errorf("群日聊天 token 额度必须是非负整数")
		}
		policy.ChatTokensQuota = n
	case "chat-cost-quota":
		n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || n < 0 {
			return "", fmt.Errorf("群日聊天费用额度必须是非负数")
		}
		policy.ChatCostQuota = n
	case "quiet":
		if isClearValue(value) {
			policy.QuietHours = ""
		} else if !validQuietHours(value) {
			return "", fmt.Errorf("静默时段格式应为 HH:MM-HH:MM，例如 23:00-07:30")
		} else {
			policy.QuietHours = strings.TrimSpace(value)
		}
	case "analysis":
		enabled, err := parseEnabled(value)
		if err != nil {
			return "", err
		}
		policy.GroupAnalysis = &enabled
	case "learning":
		enabled, err := parseEnabled(value)
		if err != nil {
			return "", err
		}
		policy.Learning = &enabled
	case "history":
		enabled, err := parseEnabled(value)
		if err != nil {
			return "", err
		}
		policy.History = &enabled
	case "knowledge":
		enabled, err := parseEnabled(value)
		if err != nil {
			return "", err
		}
		policy.Knowledge = &enabled
	case "services":
		enabled, err := parseEnabled(value)
		if err != nil {
			return "", err
		}
		policy.Services = &enabled
	case "asr":
		enabled, err := parseEnabled(value)
		if err != nil {
			return "", err
		}
		policy.ASR = &enabled
	case "learning-moderation":
		enabled, err := parseEnabled(value)
		if err != nil {
			return "", err
		}
		policy.LearningModeration = enabled
		if !enabled {
			policy.LearningModerationActions = nil
		}
	case "learning-moderation-actions":
		if isClearValue(value) {
			policy.LearningModerationActions = nil
			break
		}
		values := splitGroupList(value)
		if len(values) == 0 {
			return "", fmt.Errorf("learning-moderation-actions 不能为空")
		}
		for _, item := range values {
			item = strings.ToLower(strings.TrimSpace(item))
			switch item {
			case config.LearningModerationView, config.LearningModerationDecide, config.LearningModerationMine, config.LearningModerationDelete, config.LearningModerationExport, config.LearningModerationPolicy:
			default:
				return "", fmt.Errorf("无效的学习审核权限 %q，可选：view、decide、mine、delete、export、policy", item)
			}
		}
		policy.LearningModerationActions = values
	default:
		return "", fmt.Errorf("未知的群策略字段")
	}
	policy = policy.Normalize()
	a.setGroupPolicyForScope(scope, policy)
	if err := a.saveRuntimeState(); err != nil {
		a.setGroupPolicySnapshot(previous)
		return "", err
	}
	a.audit("group_policy_update",
		"actor_id", actor.ID,
		"scope", policyKey,
		"field", field,
		"old", groupPolicyFieldAuditValue(previousPolicy, field),
		"new", groupPolicyFieldAuditValue(policy, field),
		"result", logging.ResultSucceeded,
	)
	return fmt.Sprintf("群策略已更新：%s = %s", field, policyValue), nil
}

func (a *Agent) ResetGroupPolicy(ctx context.Context, field string) (string, error) {
	if !a.canManageCurrentGroupPolicy(ctx) {
		return "", fmt.Errorf("需要当前群群主/管理员或机器人超级管理员权限")
	}
	scope := a.scope(ctx)
	previous := a.groupPolicySnapshot()
	rawField := strings.ToLower(strings.TrimSpace(field))
	if rawField == "" || rawField == "all" {
		a.setGroupPolicyForScope(scope, config.GroupPolicyConfig{})
		if err := a.saveRuntimeState(); err != nil {
			a.setGroupPolicySnapshot(previous)
			return "", err
		}
		a.audit("group_policy_reset", "actor_id", a.actor(ctx).ID, "scope", contextOverflowKey(scope), "field", "all", "result", logging.ResultSucceeded)
		return "当前群策略已重置为全局默认。", nil
	}
	field = canonicalGroupPolicyField(rawField)
	if field == "" {
		return "", fmt.Errorf("未知的群策略字段")
	}
	actor := a.actor(ctx)
	if actor.Role != security.RoleSuperadmin {
		switch field {
		case "allowed-models", "learning-moderation", "learning-moderation-actions":
			return "", fmt.Errorf("%s 只能由机器人超级管理员重置", field)
		}
	}
	policy := a.groupPolicyForScope(scope)
	switch field {
	case "wake":
		policy.WakeKeywords = nil
	case "response":
		policy.ResponseMode = ""
	case "thread-mode":
		policy.ThreadMode = ""
	case "merge-window":
		policy.MergeWindowMS = 0
	case "default-mode":
		policy.DefaultMode = ""
	case "default-model":
		policy.DefaultModel = ""
	case "allowed-models":
		policy.AllowedModels = nil
	case "tool-allow":
		policy.ToolAllowlist = nil
		policy.ToolAllowlistSet = false
	case "image-quota":
		policy.ImageQuota = 0
	case "vision-quota":
		policy.VisionQuota = 0
	case "asr-quota":
		policy.ASRQuota = 0
	case "user-image-quota":
		policy.UserImageQuota = 0
	case "user-vision-quota":
		policy.UserVisionQuota = 0
	case "user-asr-quota":
		policy.UserASRQuota = 0
	case "chat-tokens-quota":
		policy.ChatTokensQuota = 0
	case "chat-cost-quota":
		policy.ChatCostQuota = 0
	case "quiet":
		policy.QuietHours = ""
	case "analysis":
		policy.GroupAnalysis = nil
	case "learning":
		policy.Learning = nil
	case "history":
		policy.History = nil
	case "knowledge":
		policy.Knowledge = nil
	case "services":
		policy.Services = nil
	case "asr":
		policy.ASR = nil
	case "learning-moderation":
		policy.LearningModeration = false
		policy.LearningModerationActions = nil
	case "learning-moderation-actions":
		policy.LearningModerationActions = nil
	default:
		return "", fmt.Errorf("未知的群策略字段")
	}
	a.setGroupPolicyForScope(scope, policy.Normalize())
	if err := a.saveRuntimeState(); err != nil {
		a.setGroupPolicySnapshot(previous)
		return "", err
	}
	a.audit("group_policy_reset", "actor_id", a.actor(ctx).ID, "scope", contextOverflowKey(scope), "field", field, "result", logging.ResultSucceeded)
	return fmt.Sprintf("群策略字段已重置：%s", field), nil
}

func canonicalGroupPolicyField(field string) string {
	field = strings.ToLower(strings.TrimSpace(field))
	field = strings.ReplaceAll(field, "_", "-")
	switch field {
	case "wake", "wakeup", "keywords", "wake-keywords":
		return "wake"
	case "response", "response-mode", "mode":
		return "response"
	case "thread-mode", "thread", "threads", "shared-session", "session-thread":
		return "thread-mode"
	case "merge-window", "merge", "merge-ms", "merge-window-ms":
		return "merge-window"
	case "default-mode", "session-mode":
		return "default-mode"
	case "default-model", "model":
		return "default-model"
	case "allowed-models", "model-catalog", "models":
		return "allowed-models"
	case "tool-allow", "tools", "tool-allowlist":
		return "tool-allow"
	case "image-quota", "image":
		return "image-quota"
	case "vision-quota", "vision":
		return "vision-quota"
	case "asr-quota", "voice-quota", "speech-quota":
		return "asr-quota"
	case "user-image-quota", "user-image":
		return "user-image-quota"
	case "user-vision-quota", "user-vision":
		return "user-vision-quota"
	case "user-asr-quota", "user-asr", "user-voice-quota":
		return "user-asr-quota"
	case "chat-tokens-quota", "chat-tokens", "tokens-quota":
		return "chat-tokens-quota"
	case "chat-cost-quota", "chat-cost", "cost-quota":
		return "chat-cost-quota"
	case "quiet", "quiet-hours":
		return "quiet"
	case "analysis", "group-analysis":
		return "analysis"
	case "learning":
		return "learning"
	case "history":
		return "history"
	case "knowledge", "kb", "faq":
		return "knowledge"
	case "services", "service", "group-services", "reminders", "polls", "signups":
		return "services"
	case "asr", "voice", "voice-transcription":
		return "asr"
	case "learning-moderation", "moderation":
		return "learning-moderation"
	case "learning-moderation-actions", "moderation-actions", "learning-actions":
		return "learning-moderation-actions"
	case "", "all":
		return ""
	default:
		return ""
	}
}

func splitGroupList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || isClearValue(value) {
		return nil
	}
	for _, separator := range []string{"，", ",", ";", "；", "、"} {
		value = strings.ReplaceAll(value, separator, "\n")
	}
	return normalizeStringList(strings.Split(value, "\n"))
}

func isClearValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "clear", "none", "reset", "off", "disable", "disabled":
		return true
	default:
		return false
	}
}

func parseEnabled(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes", "1", "enable", "enabled":
		return true, nil
	case "off", "false", "no", "0", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("布尔值应为 on/off")
	}
}

func validQuietHours(value string) bool {
	start, end, ok := parseQuietHours(value)
	return ok && start >= 0 && end >= 0
}

func parseQuietHours(value string) (int, int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, false
	}
	left, right, ok := strings.Cut(value, "-")
	if !ok {
		return 0, 0, false
	}
	start, okStart := parseClockMinutes(left)
	end, okEnd := parseClockMinutes(right)
	if !okStart || !okEnd {
		return 0, 0, false
	}
	return start, end, true
}

func parseClockMinutes(value string) (int, bool) {
	hourText, minuteText, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return 0, false
	}
	hour, errHour := strconv.Atoi(strings.TrimSpace(hourText))
	minute, errMinute := strconv.Atoi(strings.TrimSpace(minuteText))
	if errHour != nil || errMinute != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func quietHoursActive(quiet string, now time.Time) bool {
	start, end, ok := parseQuietHours(quiet)
	if !ok {
		return false
	}
	current := now.Hour()*60 + now.Minute()
	if start == end {
		return true
	}
	if start < end {
		return current >= start && current < end
	}
	return current >= start || current < end
}

func (a *Agent) groupQuietHoursActive(ctx context.Context) bool {
	if !a.isGroupScope(ctx) {
		return false
	}
	policy := a.groupPolicyForScope(a.scope(ctx))
	if strings.TrimSpace(policy.QuietHours) == "" {
		return false
	}
	return quietHoursActive(policy.QuietHours, time.Now())
}

func (a *Agent) requestFairKey(ctx context.Context) string {
	scope := a.scope(ctx)
	platform := strings.TrimSpace(scope.Platform)
	if platform == "" {
		platform = "unknown"
	}
	scopeID := strings.TrimSpace(scope.PlatformScopeID)
	actorID := strings.TrimSpace(scope.ActorID)
	if scopeID == "" {
		scopeID = "unknown"
	}
	if actorID == "" {
		actorID = "unknown"
	}
	return platform + ":" + scopeID + ":" + actorID
}

// resolveGroupModelSelection resolves a group model policy value to a concrete
// configured selection. Raw provider/model references are accepted only when
// the provider client exists; group policy still has to admit them.
func (a *Agent) resolveGroupModelSelection(raw string) (config.ModelSelection, bool) {
	if a == nil {
		return config.ModelSelection{}, false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return config.ModelSelection{}, false
	}
	if name, ok := a.modelAliases[strings.ToLower(raw)]; ok {
		if selection, ok := a.modelProfiles[name]; ok && a.clientForProvider(selection.Provider) != nil {
			return selection, true
		}
	}
	if selection, ok := a.modelProfiles[raw]; ok && a.clientForProvider(selection.Provider) != nil {
		return selection, true
	}
	provider, model, ok := strings.Cut(raw, "/")
	if !ok {
		return config.ModelSelection{}, false
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" || a.clientForProvider(provider) == nil {
		return config.ModelSelection{}, false
	}
	return config.ModelSelection{Provider: provider, Model: model}, true
}

// groupModelSelectionAllowed applies the superadmin-managed per-group model
// catalog. With an empty catalog only configured aliases/profiles are allowed,
// so a group admin cannot select an arbitrary expensive provider/model.
func (a *Agent) groupModelSelectionAllowed(policy config.GroupPolicyConfig, selection config.ModelSelection, raw string) bool {
	catalog := policy.Normalize().AllowedModels
	if len(catalog) == 0 {
		raw = strings.TrimSpace(raw)
		if _, ok := a.modelAliases[strings.ToLower(raw)]; ok {
			return true
		}
		if _, ok := a.modelProfiles[raw]; ok {
			return true
		}
		return false
	}
	for _, item := range catalog {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "*" {
			return true
		}
		if strings.EqualFold(item, raw) {
			return true
		}
		if candidate, ok := a.resolveGroupModelSelection(item); ok && candidate == selection {
			return true
		}
		if strings.EqualFold(item, selection.Provider+"/"+selection.Model) {
			return true
		}
	}
	return false
}

func (a *Agent) groupDefaultModelSelection(ctx context.Context) (config.ModelSelection, bool) {
	if a == nil || !a.isGroupScope(ctx) {
		return config.ModelSelection{}, false
	}
	policy := a.groupPolicyForScope(a.scope(ctx))
	value := strings.TrimSpace(policy.DefaultModel)
	if value == "" {
		return config.ModelSelection{}, false
	}
	selection, ok := a.resolveGroupModelSelection(value)
	if !ok {
		return config.ModelSelection{}, false
	}
	if !a.groupModelSelectionAllowed(policy, selection, value) {
		return config.ModelSelection{}, false
	}
	return selection, true
}

// GroupLearningEnabled reports whether the current group allows the learning
// layer. Non-group scopes keep the global behavior.
func (a *Agent) GroupLearningEnabled(ctx context.Context) bool {
	if a == nil || !a.isGroupScope(ctx) {
		return true
	}
	return a.groupPolicyForScope(a.scope(ctx)).IsLearningEnabled()
}

// AuthorizeLearningAction is the server-side action gate for group admins who
// received a learning-moderation grant. Superadmins bypass the group grant.
func (a *Agent) AuthorizeLearningAction(ctx context.Context, action string) bool {
	if a == nil || !a.isGroupScope(ctx) {
		return false
	}
	actor := a.actor(ctx)
	if actor.Role == security.RoleSuperadmin {
		return true
	}
	if actor.GroupRole != security.GroupRoleOwner && actor.GroupRole != security.GroupRoleAdmin {
		return false
	}
	policy := a.groupPolicyForScope(a.scope(ctx))
	if !policy.LearningModeration {
		return false
	}
	action = strings.ToLower(strings.TrimSpace(action))
	for _, allowed := range policy.LearningModerationActionsValue() {
		if allowed == action {
			return true
		}
	}
	return false
}

func (a *Agent) groupDefaultSessionMode(ctx context.Context) string {
	if a == nil || !a.isGroupScope(ctx) {
		return ""
	}
	return a.groupPolicyForScope(a.scope(ctx)).DefaultMode
}

func (a *Agent) groupAdminCommandGrant(ctx context.Context, commandName string) bool {
	if strings.TrimSpace(commandName) != "learning" {
		return false
	}
	if a == nil || !a.isGroupScope(ctx) {
		return false
	}
	actor := a.actor(ctx)
	if actor.GroupRole != security.GroupRoleOwner && actor.GroupRole != security.GroupRoleAdmin {
		return false
	}
	return a.groupPolicyForScope(a.scope(ctx)).LearningModeration
}

// GroupHistoryEnabledForScope is the repository-facing history policy. It is
// called with trusted platform/scope identifiers from storage rows, not from
// prompt or tool arguments.
func (a *Agent) GroupHistoryEnabledForScope(platform, scopeID string) bool {
	if a == nil {
		return true
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	if platform == "" || scopeID == "" {
		return false
	}
	if !strings.HasPrefix(scopeID, "group:") && !strings.HasPrefix(scopeID, "supergroup:") {
		return true
	}
	key := contextOverflowKey(session.Scope{Platform: platform, PlatformScopeID: scopeID})
	a.groupPolicyMu.RLock()
	policy := a.groupPolicy[key]
	a.groupPolicyMu.RUnlock()
	return policy.IsHistoryEnabled()
}

// historyEnabled reports whether new persistent conversation/history rows may
// be written for the current scope. The current turn is still processed in
// memory; turning history off only stops new writes and does not delete rows
// that already exist.
func (a *Agent) historyEnabled(ctx context.Context) bool {
	if a == nil || !a.isGroupScope(ctx) {
		return true
	}
	return a.groupPolicyForScope(a.scope(ctx)).IsHistoryEnabled()
}

// learningEnabledForScope is the service-level learning lifecycle gate. It is
// used by observation hooks, candidate mining and context injection so a
// group policy change takes effect even when a hook or background task was
// already queued.
func (a *Agent) learningEnabledForScope(platform, scopeID string) bool {
	if a == nil {
		return true
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	if platform == "" || scopeID == "" {
		return false
	}
	if !strings.HasPrefix(scopeID, "group:") && !strings.HasPrefix(scopeID, "supergroup:") {
		return true
	}
	key := contextOverflowKey(session.Scope{Platform: platform, PlatformScopeID: scopeID})
	a.groupPolicyMu.RLock()
	policy := a.groupPolicy[key]
	a.groupPolicyMu.RUnlock()
	return policy.IsLearningEnabled()
}

// requestScopeKey returns the stable group/private scope bucket used by the
// request queue's per-scope queue cap. It deliberately excludes the actor.
func (a *Agent) requestScopeKey(ctx context.Context) string {
	return requestScopeKeyFromScope(a.scope(ctx))
}

func requestScopeKeyFromScope(scope session.Scope) string {
	platform := strings.TrimSpace(scope.Platform)
	if platform == "" {
		platform = "unknown"
	}
	scopeID := strings.TrimSpace(scope.PlatformScopeID)
	if scopeID == "" {
		scopeID = "unknown"
	}
	return platform + ":" + scopeID
}

func normalizeStringList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
