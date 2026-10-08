package agent

import (
	"context"
	"fmt"
	"math"
	"strings"

	"elbot/internal/config"
	"elbot/internal/contextmgr"
	"elbot/internal/llm"
	"elbot/internal/storage"
)

func (a *Agent) ContextStatus(ctx context.Context, session *storage.Session) string {
	usage := a.usageForSession(session)
	return a.contextRuntime.status(ctx, session.ID, usage, a.modelForMode(session.Mode))
}

func (r *contextRuntimeState) status(ctx context.Context, sessionID string, usage *llm.Usage, selection config.ModelSelection) string {
	r.mu.Lock()
	resolver := r.windowResolver
	metadata := r.modelMetadata
	ctxCfg := r.config
	r.mu.Unlock()

	window := 0
	if resolver != nil {
		window = resolver.Resolve(ctx, selection.Provider, selection.Model)
	}
	if window <= 0 {
		window = metadata.DefaultContextWindow
	}
	threshold := ctxCfg.CompactTriggerRatio
	if threshold == 0 {
		threshold = 0.8
	}
	var sb strings.Builder
	sb.WriteString("  ")
	sb.WriteString(contextmgr.FormatTokens(usage))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  context window: %d\n", window))
	if usage == nil || usage.TotalTokens <= 0 || window <= 0 {
		sb.WriteString("  context usage: unknown\n")
		sb.WriteString(fmt.Sprintf("  compact threshold: %.0f%%\n", threshold*100))
		status := "unknown"
		if !ctxCfg.CompactEnabled {
			status = "disabled"
		}
		sb.WriteString(fmt.Sprintf("  compact status: %s\n", status))
		return sb.String()
	}
	ratio := float64(usage.TotalTokens) / float64(window)
	sb.WriteString(fmt.Sprintf("  context usage: %.1f%%\n", ratio*100))
	sb.WriteString(fmt.Sprintf("  compact threshold: %.0f%%\n", threshold*100))
	status := "ok"
	if !ctxCfg.CompactEnabled {
		status = "disabled"
	} else if ratio >= threshold {
		status = "will compact before next request"
	} else if ratio >= math.Max(0, threshold-0.1) {
		status = "near threshold"
	}
	sb.WriteString(fmt.Sprintf("  compact status: %s\n", status))
	return sb.String()
}

func (a *Agent) recordUsage(sessionID string, usage *llm.Usage) {
	if usage == nil {
		return
	}
	a.contextRuntime.recordUsage(sessionID, usage)
	a.persistUsage(context.Background(), sessionID, usage)
}

func (r *contextRuntimeState) recordUsage(sessionID string, usage *llm.Usage) {
	r.mu.Lock()
	r.lastUsage[sessionID] = usage
	r.mu.Unlock()
}

func (a *Agent) usageForSession(session *storage.Session) *llm.Usage {
	if session == nil {
		return nil
	}
	if usage := a.contextRuntime.usage(session.ID); usage != nil {
		return usage
	}
	metadata := decodeSessionMetadata(session.Metadata)
	if metadata.LastUsage == nil {
		return nil
	}
	a.contextRuntime.recordUsage(session.ID, metadata.LastUsage)
	return metadata.LastUsage
}

func (r *contextRuntimeState) usage(sessionID string) *llm.Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastUsage[sessionID]
}

func (a *Agent) persistUsage(ctx context.Context, sessionID string, usage *llm.Usage) {
	if a.store == nil || usage == nil || sessionID == "" {
		return
	}
	err := a.mutateSessionMetadata(ctx, &storage.Session{ID: sessionID}, func(metadata *sessionMetadata) bool {
		metadata.LastUsage = usage
		return true
	})
	if err != nil && a.logger != nil {
		a.logger.Warn("persist usage failed", "session_id", sessionID, "error", err)
	}
}

// recordLLMOrigin 在会话 metadata 里记录"这段对话由哪套协议产生"。它先与内存里的会话行比对，
// 值没变就完全不做写事务，因此正常情况下每个会话只在换协议/换模型时写一次。
//
// 记录的是适配器**实际**的协议（`llm.ProtocolCapabilitiesOf`），不是配置里声明的 api_mode：
// 一个 provider 可以按模型混用两种协议，只有实际用的那套才决定这段历史将来能不能续链。
func (a *Agent) recordLLMOrigin(ctx context.Context, session *storage.Session, selection config.ModelSelection) {
	if a.store == nil || session == nil || session.ID == "" || selection.Provider == "" || selection.Model == "" {
		return
	}
	origin := llmOriginState{
		Protocol: string(llm.ProtocolCapabilitiesOf(a.clientForProvider(selection.Provider), selection.Model).Protocol),
		Provider: selection.Provider,
		Model:    selection.Model,
	}
	if decodeSessionMetadata(session.Metadata).LLMOrigin == origin {
		return
	}
	err := a.mutateSessionMetadata(ctx, session, func(metadata *sessionMetadata) bool {
		if metadata.LLMOrigin == origin {
			return false
		}
		metadata.LLMOrigin = origin
		return true
	})
	if err != nil && a.logger != nil {
		a.logger.Warn("persist llm origin failed", "session_id", session.ID, "error", err)
	}
}

func (a *Agent) shouldCompact(ctx context.Context, session *storage.Session, selection config.ModelSelection) bool {
	if !a.historyEnabled(ctx) {
		return false
	}
	return session != nil && a.contextRuntime.reachedCompactThreshold(ctx, a.usageForSession(session), selection)
}

func (r *contextRuntimeState) reachedCompactThreshold(ctx context.Context, usage *llm.Usage, selection config.ModelSelection) bool {
	r.mu.Lock()
	ctxCfg := r.config
	resolver := r.windowResolver
	r.mu.Unlock()
	if !ctxCfg.CompactEnabled || usage == nil || usage.TotalTokens <= 0 || resolver == nil {
		return false
	}
	window := resolver.Resolve(ctx, selection.Provider, selection.Model)
	state := contextmgr.UsageState{Usage: usage, ContextWindow: window, TriggerRatio: ctxCfg.CompactTriggerRatio}
	return state.ReachedThreshold()
}
