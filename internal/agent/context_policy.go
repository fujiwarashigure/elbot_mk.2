package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"elbot/internal/config"
	"elbot/internal/contextmgr"
	"elbot/internal/llm"
	"elbot/internal/session"
	"elbot/internal/storage"
)

var errPromptOverflow = errors.New("prompt overflow")

type promptOverflowInfo struct {
	Budget          contextmgr.PromptBudget
	EstimatedTokens int
	CurrentTokens   int
	Reason          string
}

func (i promptOverflowInfo) TotalExceeded() bool {
	return i.Budget.InputLimit > 0 && i.EstimatedTokens > i.Budget.InputLimit
}

func (i promptOverflowInfo) SingleMessageExceeded() bool {
	return i.Budget.SingleMessageLimit > 0 && i.CurrentTokens > i.Budget.SingleMessageLimit
}

func (i promptOverflowInfo) Exceeded() bool {
	return i.TotalExceeded() || i.SingleMessageExceeded()
}

func contextOverflowKey(scope session.Scope) string {
	platform := strings.TrimSpace(scope.Platform)
	scopeID := strings.TrimSpace(scope.PlatformScopeID)
	if platform == "" {
		platform = "unknown"
	}
	if scopeID == "" {
		return platform
	}
	return platform + ":" + scopeID
}

func cloneContextOverflow(input map[string]config.ContextOverflowConfig) map[string]config.ContextOverflowConfig {
	if len(input) == 0 {
		return map[string]config.ContextOverflowConfig{}
	}
	out := make(map[string]config.ContextOverflowConfig, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func (a *Agent) setContextOverflowSnapshot(snapshot map[string]config.ContextOverflowConfig) {
	a.contextOverflowMu.Lock()
	a.contextOverflow = cloneContextOverflow(snapshot)
	a.contextOverflowMu.Unlock()
}

func (a *Agent) contextOverflowSnapshot() map[string]config.ContextOverflowConfig {
	a.contextOverflowMu.RLock()
	defer a.contextOverflowMu.RUnlock()
	return cloneContextOverflow(a.contextOverflow)
}

func (a *Agent) contextOverflowForScope(scope session.Scope) (config.ContextOverflowConfig, bool) {
	key := contextOverflowKey(scope)
	a.contextOverflowMu.RLock()
	defer a.contextOverflowMu.RUnlock()
	value, ok := a.contextOverflow[key]
	return value, ok
}

func (a *Agent) setContextOverflowForScope(scope session.Scope, value config.ContextOverflowConfig) {
	key := contextOverflowKey(scope)
	a.contextOverflowMu.Lock()
	defer a.contextOverflowMu.Unlock()
	if a.contextOverflow == nil {
		a.contextOverflow = map[string]config.ContextOverflowConfig{}
	}
	if strings.TrimSpace(value.Chat) == "" && strings.TrimSpace(value.Work) == "" {
		delete(a.contextOverflow, key)
		return
	}
	a.contextOverflow[key] = value
}

func (a *Agent) contextOverflowMode(ctx context.Context, session *storage.Session) string {
	mode := storage.SessionModeWork
	if session != nil && session.Mode != "" {
		mode = session.Mode
	}
	if override, ok := a.contextOverflowForScope(a.scope(ctx)); ok {
		value := override.Work
		if mode == storage.SessionModeChat {
			value = override.Chat
		}
		if validOverflowMode(value) {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	if value := a.contextRuntime.overflowMode(); validOverflowMode(value) {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return "reject"
}

func validOverflowMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "reject", "truncate", "summarize":
		return true
	default:
		return false
	}
}

func (r *contextRuntimeState) overflowMode() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config.OverflowMode
}

func (r *contextRuntimeState) userOriginalMaxRunes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config.UserOriginalMaxRunes
}

func (r *contextRuntimeState) promptBudget(ctx context.Context, selection config.ModelSelection) contextmgr.PromptBudget {
	r.mu.Lock()
	cfg := r.config
	resolver := r.windowResolver
	metadata := r.modelMetadata
	r.mu.Unlock()

	window := 0
	if resolver != nil {
		window = resolver.Resolve(ctx, selection.Provider, selection.Model)
	}
	if window <= 0 {
		window = metadata.DefaultContextWindow
	}
	if window <= 0 {
		window = config.DefaultContextWindow
	}
	return contextmgr.NewPromptBudget(window, cfg.MaxPromptRatio, cfg.SingleMessageMaxRatio, cfg.ReserveOutputTokens)
}

func (r *contextRuntimeState) summarizeText(ctx context.Context, selection config.ModelSelection, text string, maxTokens int) (string, error) {
	r.mu.Lock()
	compressor := r.compactor
	timeout := r.compressTimeout
	r.mu.Unlock()

	summarizeCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		summarizeCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	chunkLimit := maxTokens
	if budget := r.promptBudget(ctx, selection); budget.InputLimit > 0 {
		chunkLimit = budget.InputLimit
	}
	result, err := compressor.SummarizeText(summarizeCtx, contextmgr.SummarizeRequest{
		Provider:         selection.Provider,
		Model:            selection.Model,
		Text:             text,
		MaxInputTokens:   maxTokens,
		ChunkInputTokens: chunkLimit,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Summary), nil
}

func (a *Agent) detectPromptOverflow(ctx context.Context, session *storage.Session, selection config.ModelSelection, messages []llm.LLMMessage, tools []llm.ToolSchema) (promptOverflowInfo, bool) {
	budget := a.contextRuntime.promptBudget(ctx, selection)
	if budget.Window <= 0 {
		return promptOverflowInfo{}, false
	}
	info := promptOverflowInfo{
		Budget:          budget,
		EstimatedTokens: contextmgr.EstimateMessagesTokens(messages) + contextmgr.EstimateToolsTokens(tools),
	}
	if len(messages) > 0 {
		info.CurrentTokens = contextmgr.EstimateMessageTokens(messages[len(messages)-1])
	}
	if info.SingleMessageExceeded() {
		info.Reason = "single_message"
		return info, true
	}
	if info.TotalExceeded() {
		info.Reason = "total_prompt"
		return info, true
	}
	return info, false
}

func (a *Agent) handleInitialPromptOverflow(ctx context.Context, session *storage.Session, selection config.ModelSelection, messages *[]llm.LLMMessage, tools []llm.ToolSchema, out turnOutput) (bool, bool, error) {
	if messages == nil || len(*messages) == 0 {
		return true, false, nil
	}
	info, exceeded := a.detectPromptOverflow(ctx, session, selection, *messages, tools)
	if !exceeded {
		return true, false, nil
	}
	mode := a.contextOverflowMode(ctx, session)
	latestIndex := len(*messages) - 1
	oldSegments := (*messages)[latestIndex].Segments
	text := llm.SegmentsContentText(oldSegments)

	otherTokens := info.EstimatedTokens - info.CurrentTokens
	available := info.Budget.InputLimit - otherTokens
	if available < 0 {
		available = 0
	}
	currentLimit := available
	if info.Budget.SingleMessageLimit > 0 && info.Budget.SingleMessageLimit < currentLimit {
		currentLimit = info.Budget.SingleMessageLimit
	}
	if currentLimit > contextmgr.MessageTokenOverhead {
		currentLimit -= contextmgr.MessageTokenOverhead
	} else {
		currentLimit = 0
	}
	if !info.SingleMessageExceeded() {
		mode = "reject"
	}

	switch mode {
	case "truncate":
		marker := "\n\n...[原始消息过长，已自动截断；请分段发送以完整处理]"
		budget := currentLimit - contextmgr.EstimateTextTokens(marker)
		if budget <= 0 {
			break
		}
		truncated := contextmgr.TruncateTextToTokens(text, budget)
		if strings.TrimSpace(truncated) == "" {
			break
		}
		(*messages)[latestIndex].Segments = llm.SetSegmentText(oldSegments, truncated+marker)
		out.SendNotice(ctx, slog.LevelWarn, a.overflowAlertText(session, selection, info, mode, "已自动截断后继续处理"))
		return true, true, nil
	case "summarize":
		if currentLimit <= 0 {
			break
		}
		compactSelection := a.compactSelectionForSession(session)
		if compactSelection.Provider == "" || compactSelection.Model == "" {
			compactSelection = selection
		}
		summary, err := a.contextRuntime.summarizeText(ctx, compactSelection, text, currentLimit)
		if err != nil || strings.TrimSpace(summary) == "" {
			out.SendNotice(ctx, slog.LevelWarn, a.overflowAlertText(session, selection, info, "reject", "自动摘要失败，已拒绝发送"))
			return false, false, nil
		}
		(*messages)[latestIndex].Segments = llm.SetSegmentText(oldSegments, summary)
		out.SendNotice(ctx, slog.LevelWarn, a.overflowAlertText(session, selection, info, mode, "已自动摘要后继续处理"))
		return true, true, nil
	}

	out.SendNotice(ctx, slog.LevelWarn, a.overflowAlertText(session, selection, info, "reject", "未发送给模型，原文未写入会话"))
	return false, false, nil
}

func (a *Agent) guardPromptBeforeCall(ctx context.Context, sessionID string, selection config.ModelSelection, messages []llm.LLMMessage, tools []llm.ToolSchema, out turnOutput) error {
	session, _ := a.loadSession(ctx, sessionID)
	info, exceeded := a.detectPromptOverflow(ctx, session, selection, messages, tools)
	if !exceeded {
		return nil
	}
	out.SendNotice(ctx, slog.LevelWarn, a.overflowAlertText(session, selection, info, "reject", "工具阶段上下文超限，已停止本轮请求"))
	return fmt.Errorf("%w: estimated=%d limit=%d", errPromptOverflow, info.EstimatedTokens, info.Budget.InputLimit)
}

func (a *Agent) loadSession(ctx context.Context, sessionID string) (*storage.Session, error) {
	if a.store == nil || a.store.Sessions() == nil {
		return nil, nil
	}
	return a.store.Sessions().Get(ctx, sessionID)
}

func (a *Agent) overflowAlertText(session *storage.Session, selection config.ModelSelection, info promptOverflowInfo, mode, action string) string {
	modeName := storage.SessionModeWork
	if session != nil && session.Mode != "" {
		modeName = session.Mode
	}
	var sb strings.Builder
	sb.WriteString("⚠️ 长消息保护已触发\n")
	sb.WriteString(fmt.Sprintf("模式：%s / 策略：%s\n", modeName, mode))
	sb.WriteString(fmt.Sprintf("模型：%s/%s\n", selection.Provider, selection.Model))
	sb.WriteString(fmt.Sprintf("模型窗口：%d tokens；可用输入：%d tokens\n", info.Budget.Window, info.Budget.InputLimit))
	sb.WriteString(fmt.Sprintf("估算输入：%d tokens；本条消息：%d tokens\n", info.EstimatedTokens, info.CurrentTokens))
	if info.Budget.SingleMessageLimit > 0 {
		sb.WriteString(fmt.Sprintf("单条消息上限：%d tokens\n", info.Budget.SingleMessageLimit))
	}
	sb.WriteString("处理：" + action + "\n")
	if mode == "reject" {
		sb.WriteString(fmt.Sprintf("建议：拆分为多条发送；管理员可发送 %soverflow --chat summarize 或 %soverflow --work summarize 启用自动摘要；也可先发送 %scompact 压缩历史。", a.commandPrefix(), a.commandPrefix(), a.commandPrefix()))
	}
	return sb.String()
}

func (a *Agent) ContextPolicyStatus(ctx context.Context) string {
	scope := a.scope(ctx)
	key := contextOverflowKey(scope)
	currentMode := a.currentMode(ctx)
	override, _ := a.contextOverflowForScope(scope)
	global := a.contextRuntime.overflowMode()
	if !validOverflowMode(global) {
		global = "reject"
	}
	chat := global
	work := global
	if strings.TrimSpace(override.Chat) != "" {
		chat = strings.ToLower(strings.TrimSpace(override.Chat))
	}
	if strings.TrimSpace(override.Work) != "" {
		work = strings.ToLower(strings.TrimSpace(override.Work))
	}
	chatSource := "全局默认"
	workSource := "全局默认"
	if strings.TrimSpace(override.Chat) != "" {
		chatSource = "当前群覆盖"
	}
	if strings.TrimSpace(override.Work) != "" {
		workSource = "当前群覆盖"
	}
	var sb strings.Builder
	sb.WriteString("长消息保护策略\n")
	sb.WriteString(fmt.Sprintf("作用域：%s\n", key))
	sb.WriteString(fmt.Sprintf("当前模式：%s\n", currentMode))
	sb.WriteString(fmt.Sprintf("chat：%s（%s）\n", chat, chatSource))
	sb.WriteString(fmt.Sprintf("work：%s（%s）\n", work, workSource))
	sb.WriteString(fmt.Sprintf("全局默认：%s\n", global))
	sb.WriteString(fmt.Sprintf("可选值：reject | truncate | summarize\n修改：%soverflow [--chat|--work|--all] <值>；重置：%soverflow reset [--chat|--work|--all]", a.commandPrefix(), a.commandPrefix()))
	return sb.String()
}

func (a *Agent) SetContextPolicy(ctx context.Context, target, value string) (string, error) {
	target = normalizeOverflowTarget(target)
	value = strings.ToLower(strings.TrimSpace(value))
	if !validOverflowMode(value) {
		return "", fmt.Errorf("无效的长消息保护策略 %q，可选：reject、truncate、summarize", value)
	}
	scope := a.scope(ctx)
	previous := a.contextOverflowSnapshot()
	override, _ := a.contextOverflowForScope(scope)
	if target == "chat" || target == "all" {
		override.Chat = value
	}
	if target == "work" || target == "all" {
		override.Work = value
	}
	a.setContextOverflowForScope(scope, override)
	if err := a.saveRuntimeState(); err != nil {
		a.setContextOverflowSnapshot(previous)
		return "", err
	}
	return fmt.Sprintf("长消息保护策略已更新：%s = %s", target, value), nil
}

func (a *Agent) ResetContextPolicy(ctx context.Context, target string) (string, error) {
	target = normalizeOverflowTarget(target)
	scope := a.scope(ctx)
	previous := a.contextOverflowSnapshot()
	override, _ := a.contextOverflowForScope(scope)
	if target == "all" {
		override = config.ContextOverflowConfig{}
	} else if target == "chat" {
		override.Chat = ""
	} else if target == "work" {
		override.Work = ""
	}
	a.setContextOverflowForScope(scope, override)
	if err := a.saveRuntimeState(); err != nil {
		a.setContextOverflowSnapshot(previous)
		return "", err
	}
	return fmt.Sprintf("长消息保护策略已重置：%s 使用全局默认", target), nil
}

func normalizeOverflowTarget(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat":
		return "chat"
	case "work":
		return "work"
	default:
		return "all"
	}
}
