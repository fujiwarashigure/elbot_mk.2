package agent

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/directive"
	"elbot/internal/logging"
	"elbot/internal/security"
	"elbot/internal/storage"
	"elbot/internal/tool"
	"elbot/internal/toolrun"
)

type toolDirectiveResult struct {
	Text        string
	Injected    []string
	Existing    []string
	Invalid     []string
	ModeBlocked bool
	Err         error
}

// toolReasonSkillHasNoSchema 表示该名字是 Skill 本体（详情提供者），不是可注入的
// top-level 工具 schema。它与 internal/tool 的原因常量同属一套取值，因此只在 agent
// 内部用于包装工具路径。
const toolReasonSkillHasNoSchema = "skill_has_no_schema"

// toolReasonNotSkillLike 表示 @skill: 指向的工具不是详情提供者（是普通工具），不能按
// Skill 注入。
const toolReasonNotSkillLike = "not_a_skill"

type skillDirectiveResult struct {
	Text             string
	Skills           []string
	InjectedWrappers []string
	ExistingWrappers []string
	Invalid          []string
	ModeBlocked      bool
}

func (a *Agent) applyToolDirectives(ctx context.Context, session *storage.Session, text string) toolDirectiveResult {
	result := toolDirectiveResult{Text: text}
	if session == nil || a.toolRuntime.registry == nil || !containsAny(text, directive.ToolPrefix, directive.ToolFullPrefix, directive.ToolShortPrefix, directive.ToolShortFull) {
		return result
	}
	matches := directive.ToolMatches(text)
	if len(matches) == 0 {
		return result
	}
	if session.Mode != storage.SessionModeWork {
		result.ModeBlocked = true
		remove := make([]bool, len(matches))
		for i, match := range matches {
			result.Invalid = append(result.Invalid, match.Name)
			remove[i] = true
		}
		result.Text = directive.StripToolMatches(text, matches, remove)
		return result
	}
	if !isBackgroundSession(session) {
		ctx = tool.WithWorkspaceStore(ctx, sessionWorkspaceStore{agent: a, session: session})
	}

	remove := make([]bool, len(matches))
	seenInjected := map[string]bool{}
	seenDiscoveryContent := map[string]bool{}
	discoveryContent := []string{}
	for i, match := range matches {
		name := match.Name
		discovery, tagName, ok := a.discoveryForToolDirective(ctx, name)
		if !ok || discovery == nil || len(discovery.Tools) == 0 {
			result.Invalid = append(result.Invalid, name)
			continue
		}
		content, err := a.preloadedContextDiscoveryContent(ctx, discovery, seenDiscoveryContent)
		if err != nil {
			result.Err = err
			return result
		}
		if strings.TrimSpace(content) != "" {
			discoveryContent = append(discoveryContent, content)
		}
		injected, existing := a.rememberPreloadedDiscovery(ctx, session, discovery, seenInjected)
		result.Injected = append(result.Injected, injected...)
		result.Existing = append(result.Existing, existing...)
		if tagName != "" {
			a.persistToolTags(ctx, session, []string{tagName})
		}
		remove[i] = true
	}
	if len(result.Injected) == 0 && len(result.Existing) == 0 {
		return result
	}
	result.Text = directive.StripToolMatches(text, matches, remove)
	if len(discoveryContent) > 0 {
		if strings.TrimSpace(result.Text) == "" {
			result.Text = strings.Join(discoveryContent, "\n\n")
		} else {
			result.Text = strings.TrimSpace(result.Text) + "\n\n" + strings.Join(discoveryContent, "\n\n")
		}
	}
	return result
}

func (a *Agent) preloadedContextDiscoveryContent(ctx context.Context, discovery *tool.DiscoveryResult, seen map[string]bool) (string, error) {
	if discovery == nil || a.toolRuntime.registry == nil {
		return "", nil
	}
	parts := []string{}
	for _, discovered := range discovery.Tools {
		name := strings.TrimSpace(discovered.Info.Name)
		if discovered.Schema == nil || name == "" || seen[name] {
			continue
		}
		target, ok := a.toolRuntime.registry.Get(name)
		if !ok {
			continue
		}
		if _, ok := target.(tool.ContextDiscoveryContentProvider); !ok {
			continue
		}
		seen[name] = true
		content, _, _, err := tool.LoadDiscoveryContent(ctx, target)
		if err != nil {
			return "", fmt.Errorf("load discovery content for %s: %w", name, err)
		}
		if strings.TrimSpace(content) != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func (a *Agent) applySkillDirectives(ctx context.Context, session *storage.Session, text string) skillDirectiveResult {
	result := skillDirectiveResult{Text: text}
	if session == nil || a.toolRuntime.registry == nil || !containsAny(text, directive.SkillPrefix, directive.SkillFullPrefix, directive.SkillShortPrefix, directive.SkillShortFull) {
		return result
	}
	matches := directive.SkillMatches(text)
	if len(matches) == 0 {
		return result
	}
	if session.Mode != storage.SessionModeWork {
		result.ModeBlocked = true
		remove := make([]bool, len(matches))
		for i, match := range matches {
			result.Invalid = append(result.Invalid, strings.TrimSpace(match.Name))
			remove[i] = true
		}
		result.Text = directive.StripToolMatches(text, matches, remove)
		return result
	}
	ctx = tool.WithShownRuleCardFormats(ctx, decodeSessionMetadata(session.Metadata).ShownRuleCardFormats)
	policy := a.securityPolicy
	if policy == nil {
		policy = security.DefaultPolicy()
	}
	actor := a.actor(ctx)
	remove := make([]bool, len(matches))
	seenSkills := map[string]bool{}
	seenInjected := map[string]bool{}
	blocks := []tool.DetailBlock{}
	for i, match := range matches {
		name := strings.TrimSpace(match.Name)
		candidate, ok := a.toolRuntime.registry.Get(name)
		if !ok || !a.canPreloadSkill(actor, policy, candidate) {
			result.Invalid = append(result.Invalid, name)
			continue
		}
		detailer := candidate.(tool.DetailProvider)
		if !seenSkills[name] {
			block, err := skillDetailBlock(security.WithActor(ctx, actor), candidate, detailer)
			if err != nil {
				result.Invalid = append(result.Invalid, name)
				a.audit("skill_preload_failed", "session_id", session.ID, "tool", name, "error", err, "result", logging.ResultFailed)
				continue
			}
			seenSkills[name] = true
			result.Skills = append(result.Skills, name)
			blocks = append(blocks, block)
		}
		for _, wrapper := range detailer.ActivateTools() {
			injected, existing := a.preloadSkillWrapper(ctx, session, wrapper, actor, policy, seenInjected)
			result.InjectedWrappers = append(result.InjectedWrappers, injected...)
			result.ExistingWrappers = append(result.ExistingWrappers, existing...)
		}
		remove[i] = true
	}
	if len(result.Skills) == 0 {
		return result
	}
	stripped := directive.StripToolMatches(text, matches, remove)
	if detailText := tool.RenderDetailBlocksWithContext(ctx, blocks); detailText != "" {
		if strings.TrimSpace(stripped) == "" {
			stripped = detailText
		} else {
			stripped = strings.TrimSpace(stripped) + "\n\n" + detailText
		}
	}
	a.persistShownRuleCardFormats(ctx, session, tool.NewRuleCardFormatsFromContext(ctx))
	result.Text = stripped
	return result
}

func (a *Agent) discoveryForToolDirective(ctx context.Context, value string) (*tool.DiscoveryResult, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || a.toolRuntime.registry == nil {
		return nil, "", false
	}
	policy := a.securityPolicy
	if policy == nil {
		policy = security.DefaultPolicy()
	}
	actor := a.actor(ctx)
	if root, ok := a.toolRuntime.registry.Get(value); ok {
		if !a.canPreloadToolRoot(actor, policy, root) {
			return nil, "", false
		}
		discovery, ok := a.discoveryForToolNames(ctx, []string{value}, actor, policy)
		return discovery, "", ok
	}
	tagName := normalizeToolTag(value)
	names := a.namesByToolTag(ctx, tagName, func(candidate tool.Tool) bool {
		return a.canPreloadToolRoot(actor, policy, candidate)
	})
	if len(names) == 0 {
		return nil, "", false
	}
	discovery, ok := a.discoveryForToolNames(ctx, names, actor, policy)
	return discovery, tagName, ok
}

func (a *Agent) preloadToolNames(ctx context.Context, session *storage.Session, names []string) []string {
	if session == nil || session.Mode != storage.SessionModeWork || a.toolRuntime.registry == nil {
		return nil
	}
	policy := a.securityPolicy
	if policy == nil {
		policy = security.DefaultPolicy()
	}
	actor := a.actor(ctx)
	seenInjected := map[string]bool{}
	injected := []string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		root, ok := a.toolRuntime.registry.Get(name)
		if !ok {
			a.audit("tool_preload_skipped", "session_id", session.ID, "tool", name, "reason", tool.ToolReasonNotFound, "result", logging.ResultSkipped)
			continue
		}
		if reason := preloadRootReason(actor, policy, root); reason != "" {
			a.audit("tool_preload_skipped", "session_id", session.ID, "tool", name, "reason", reason, "result", preloadSkipResult(reason))
			continue
		}
		discovery, ok := a.discoveryForToolNames(ctx, []string{name}, actor, policy)
		if !ok || discovery == nil || len(discovery.Tools) == 0 {
			a.audit("tool_preload_skipped", "session_id", session.ID, "tool", name, "reason", tool.ToolReasonNoSchema, "result", logging.ResultSkipped)
			continue
		}
		newTools, _ := a.rememberPreloadedDiscovery(ctx, session, discovery, seenInjected)
		injected = append(injected, newTools...)
	}
	return injected
}

func (a *Agent) rememberPreloadedDiscovery(ctx context.Context, session *storage.Session, discovery *tool.DiscoveryResult, seen map[string]bool) ([]string, []string) {
	cachedBefore := a.cachedToolNameSet(ctx, session)
	a.rememberCachedTools(ctx, session, toolrun.NativeCachedToolsFromDiscovery(discovery))
	injected := []string{}
	existing := []string{}
	for _, discovered := range discovery.Tools {
		if discovered.Schema == nil || discovered.Info.Name == "" || seen[discovered.Info.Name] {
			continue
		}
		seen[discovered.Info.Name] = true
		if cachedBefore[discovered.Info.Name] {
			existing = append(existing, discovered.Info.Name)
		} else {
			injected = append(injected, discovered.Info.Name)
		}
	}
	return injected, existing
}

func (a *Agent) canPreloadToolRoot(actor security.Actor, policy *security.Policy, candidate tool.Tool) bool {
	return preloadRootReason(actor, policy, candidate) == ""
}

// preloadRootReason 报告 @tool: 预载路径拒绝根工具的原因，可用时返回空字符串。
// discover_tool 不通过预载暴露（它自己负责发现），因此按隐藏工具归类。
func preloadRootReason(actor security.Actor, policy *security.Policy, candidate tool.Tool) string {
	info := candidate.Info()
	if info.Name == "discover_tool" {
		return tool.ToolReasonHidden
	}
	if reason := tool.ToolAccessReason(actor, policy, info); reason != "" {
		return reason
	}
	if info.Hidden {
		return tool.ToolReasonHidden
	}
	_, isSkillLike := candidate.(tool.DetailProvider)
	if isSkillLike {
		return tool.ToolReasonHidden
	}
	return ""
}

func (a *Agent) canPreloadSkill(actor security.Actor, policy *security.Policy, candidate tool.Tool) bool {
	return preloadSkillRootReason(actor, policy, candidate) == ""
}

// preloadSkillRootReason 报告 @skill: 根 Skill 被拒绝的原因，可用时返回空字符串。
// 隐藏的 Skill 不通过 @skill: 暴露，因此按隐藏工具归类。
func preloadSkillRootReason(actor security.Actor, policy *security.Policy, candidate tool.Tool) string {
	info := candidate.Info()
	if reason := tool.ToolAccessReason(actor, policy, info); reason != "" {
		return reason
	}
	if info.Hidden {
		return tool.ToolReasonHidden
	}
	if _, isSkillLike := candidate.(tool.DetailProvider); !isSkillLike {
		return toolReasonNotSkillLike
	}
	return ""
}

// preloadSkillWrapperReason 报告 Skill 包装工具被拒绝的原因，可用时返回空字符串。
//
// 这里**不**按 Hidden 拒绝：隐藏正是包装工具的常态，它们由 Skill 的 activate 列表显式
// 声明后才注入（discover_tool 无法发现隐藏工具）。判定与最初的 preloadSkillWrapper
// 一致：存在性 + 上下文可用性（由调用方先查）+ 策略，然后才是 skill-like。
func preloadSkillWrapperReason(actor security.Actor, policy *security.Policy, candidate tool.Tool) string {
	info := candidate.Info()
	if reason := tool.ToolAccessReason(actor, policy, info); reason != "" {
		return reason
	}
	if _, isSkillLike := candidate.(tool.DetailProvider); isSkillLike {
		return toolReasonSkillHasNoSchema
	}
	return ""
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func skillDetailBlock(ctx context.Context, candidate tool.Tool, detailer tool.DetailProvider) (tool.DetailBlock, error) {
	if loader, ok := candidate.(tool.LazyDetailProvider); ok {
		return loader.LoadDetail(ctx)
	}
	if structured, ok := candidate.(tool.StructuredDetailProvider); ok {
		return structured.DetailBlock(), nil
	}
	return tool.DetailBlock{Content: detailer.Detail()}, nil
}

func (a *Agent) preloadSkillWrapper(ctx context.Context, session *storage.Session, name string, actor security.Actor, policy *security.Policy, seen map[string]bool) ([]string, []string) {
	name = strings.TrimSpace(name)
	if name == "" || name == "discover_tool" || a.toolRuntime.registry == nil {
		return nil, nil
	}
	candidate, ok := a.toolRuntime.registry.Get(name)
	if !ok {
		a.audit("skill_wrapper_preload_skipped", "session_id", session.ID, "tool", name, "reason", tool.ToolReasonNotFound, "result", logging.ResultSkipped)
		return nil, nil
	}
	if reason := preloadSkillWrapperReason(actor, policy, candidate); reason != "" {
		a.audit("skill_wrapper_preload_skipped", "session_id", session.ID, "tool", name, "reason", reason, "result", preloadSkipResult(reason))
		return nil, nil
	}
	schema := candidate.Schema()
	if schema.Function.Name == "" {
		a.audit("skill_wrapper_preload_skipped", "session_id", session.ID, "tool", name, "reason", tool.ToolReasonNoSchema, "result", logging.ResultSkipped)
		return nil, nil
	}
	info := candidate.Info()
	discovery := &tool.DiscoveryResult{Tools: []tool.DiscoveredTool{{Info: tool.PublicInfo{Name: info.Name, Description: info.Description, Source: string(info.Source), ForegroundOnly: info.ForegroundOnly}, Schema: &schema}}}
	return a.rememberPreloadedDiscovery(ctx, session, discovery, seen)
}

func (a *Agent) discoveryForToolNames(ctx context.Context, names []string, actor security.Actor, policy *security.Policy) (*tool.DiscoveryResult, bool) {
	details, _ := a.toolRuntime.registry.DiscoverDetails(security.WithActor(ctx, actor), names, func(candidate tool.Tool) bool {
		info := candidate.Info()
		return tool.InfoAvailableInContext(ctx, info) && tool.CanAccessTool(actor, policy, info)
	})
	if len(details) == 0 {
		return nil, false
	}
	out := &tool.DiscoveryResult{}
	for _, discovered := range details {
		if discovered.Schema == nil || discovered.Info.Name == "" || discovered.Detail != "" {
			continue
		}
		out.Tools = append(out.Tools, discovered)
	}
	return out, len(out.Tools) > 0
}

func (a *Agent) notifyToolDirectiveResult(ctx context.Context, result toolDirectiveResult) {
	parts := []string{}
	if len(result.Injected) > 0 {
		parts = append(parts, "已注入工具："+strings.Join(sortedUnique(result.Injected), ", "))
	}
	if len(result.Existing) > 0 {
		parts = append(parts, "已存在工具："+strings.Join(sortedUnique(result.Existing), ", "))
	}
	if len(result.Invalid) > 0 && !result.ModeBlocked {
		parts = append(parts, "未找到或不可用的工具："+strings.Join(sortedUnique(result.Invalid), ", "))
	}
	if result.ModeBlocked {
		parts = append(parts, fmt.Sprintf("工具预载仅在 work 模式可用，当前是 chat 模式。发送 %swork 切换后再试。", a.commandPrefix()))
	}
	if len(parts) == 0 {
		return
	}
	a.sendChat(ctx, strings.Join(parts, "\n"))
}

func (a *Agent) notifySkillDirectiveResult(ctx context.Context, result skillDirectiveResult) {
	parts := []string{}
	if len(result.Skills) > 0 {
		parts = append(parts, "已注入 Skill："+strings.Join(sortedUnique(result.Skills), ", "))
	}
	if len(result.InjectedWrappers) > 0 {
		parts = append(parts, "已注入 Skill 工具："+strings.Join(sortedUnique(result.InjectedWrappers), ", "))
	}
	if len(result.ExistingWrappers) > 0 {
		parts = append(parts, "已存在 Skill 工具："+strings.Join(sortedUnique(result.ExistingWrappers), ", "))
	}
	if len(result.Invalid) > 0 && !result.ModeBlocked {
		parts = append(parts, "未找到或不可用的 Skill："+strings.Join(sortedUnique(result.Invalid), ", "))
	}
	if result.ModeBlocked {
		parts = append(parts, fmt.Sprintf("Skill 预载仅在 work 模式可用，当前是 chat 模式。发送 %swork 切换后再试。", a.commandPrefix()))
	}
	if len(parts) == 0 {
		return
	}
	a.sendChat(ctx, strings.Join(parts, "\n"))
}
