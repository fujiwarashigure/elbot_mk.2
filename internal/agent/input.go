package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"elbot/internal/character"
	"elbot/internal/command"
	"elbot/internal/hook"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/security"
	"elbot/internal/session"
	"elbot/internal/storage"
	"elbot/internal/turn"
)

const defaultUserConfirmationTimeout = 10 * time.Minute

func (a *Agent) confirmationWaitTimeout(ctx context.Context) time.Duration {
	actor := a.actor(ctx)
	isSuperadmin := actor.Role == security.RoleSuperadmin
	ttlMinutes := a.idleExpiration.TTLMinutes(a.scope(ctx), isSuperadmin)
	var sessionTimeout time.Duration
	if ttlMinutes > 0 {
		sessionTimeout = time.Duration(ttlMinutes) * time.Minute
	}
	if isSuperadmin {
		return sessionTimeout
	}
	userTimeout := a.userConfirmationTimeout
	if userTimeout <= 0 {
		userTimeout = defaultUserConfirmationTimeout
	}
	if sessionTimeout > 0 && sessionTimeout < userTimeout {
		return sessionTimeout
	}
	return userTimeout
}

func confirmationWaitDurationText(timeout time.Duration) string {
	if timeout%time.Minute == 0 {
		return fmt.Sprintf("%d 分钟", int(timeout/time.Minute))
	}
	return timeout.String()
}

func appendConfirmPromptText(timeout time.Duration) string {
	text := "已停止当前处理。是否追加这条消息并重新发送？\n发送 $ / 是 / y / yes 确认；发送 取消 / 否 / n / no 放弃。\n也可以继续发送内容，发送完后再确认。"
	if timeout > 0 {
		text += fmt.Sprintf("\n超过 %s 没有继续发送内容或确认，将自动放弃。", confirmationWaitDurationText(timeout))
	}
	return text
}

func (a *Agent) handleAppendConfirmationInput(ctx context.Context, session *storage.Session, text string) error {
	switch {
	case turn.IsConfirm(text):
		merged, ok := a.turns.ConfirmAppendInput(session.ID)
		if !ok || (merged.Text == "" && len(merged.Segments) == 0) {
			return nil
		}
		ctx = withInboundTurnInput(ctx, merged)
		return a.startChat(ctx, session, merged.Text)
	case turn.IsCancel(text):
		a.turns.CancelAppend(session.ID)
		a.sendChat(ctx, "已取消追加，本轮处理已停止。")
		return nil
	default:
		a.turns.AppendPendingInput(session.ID, a.turnInputForMessage(ctx, text))
		return nil
	}
}

func (e *commandExecutor) compactCommandBlockedText(command string) string {
	return fmt.Sprintf("正在压缩当前会话，暂不执行 %s。请等待压缩完成，或先使用 %sstop 取消。", command, e.router.PrimaryPrefix())
}

func (e *commandExecutor) activeTurnCommandBlockedText() string {
	return fmt.Sprintf("当前会话处理中，暂不支持切换。如有必要，请先使用 %sstop 结束当前处理。", e.router.PrimaryPrefix())
}

func (a *Agent) handleInput(ctx context.Context, text string) error {
	if allowed, retryAfter := a.allowInbound(ctx); !allowed {
		a.sendChat(ctx, rateLimitRetryText(retryAfter))
		return nil
	}
	if a.tryGroupKnowledgeAnswer(ctx, text) {
		return nil
	}
	session, err := a.sessionForInput(ctx, text)
	if err != nil {
		return err
	}
	if a.useInboundInbox(ctx) {
		phase := a.turns.Snapshot(session.ID).Phase
		switch phase {
		case turn.PhaseAwaitRiskConfirm, turn.PhaseAwaitAppendConfirm, turn.PhaseCompact:
			// Confirmation and compaction notices must be handled immediately;
			// queueing them behind the turn they are answering would deadlock.
			return a.handleSessionInput(ctx, session, text)
		case turn.PhaseTool:
			// A per-user tool turn can safely absorb same-actor pending input.
			// Shared threads must queue instead, so another member's text never
			// runs under the current turn's actor permissions.
			if !a.scope(ctx).Shared {
				return a.handleSessionInput(ctx, session, text)
			}
		}
		return a.submitInbound(ctx, session.ID, text)
	}
	return a.handleSessionInput(ctx, session, text)
}

func (a *Agent) continueCommandInput(ctx context.Context, continuation command.Continuation) error {
	session, err := a.sessions.Resume(ctx, a.scope(ctx), continuation.SessionID)
	if err != nil {
		return err
	}
	segments := replaceInboundTextSegments(ctx, continuation.Text)
	ctx = withInboundSegments(ctx, segments)
	return a.handleSessionInput(ctx, session, continuation.Text)
}

func (a *Agent) handleSessionInput(ctx context.Context, session *storage.Session, text string) error {
	ctx = a.withToolCapabilities(ctx, session)
	// Compaction admission comes before the input hook and directive handling:
	// a message that will be refused must not preload tools or rewrite the
	// session tool cache.
	if a.compactActive(session.ID) {
		a.sendChat(ctx, fmt.Sprintf("正在压缩上下文，请稍后再发送。可使用 %sstop 取消当前请求。", a.commandPrefix()))
		return nil
	}
	event, err := a.runHook(ctx, hook.Event{Point: hook.PointAgentInputPrepared, Session: a.hookSession(session), Message: hook.MessagePayload{Role: string(llm.RoleUser), Segments: inboundSegments(ctx, text)}})
	if err != nil {
		return err
	}
	ctx = withInboundSegments(ctx, event.Message.Segments)
	text = llm.SegmentsTextOnly(event.Message.Segments)

	if session.ArchivedAt != nil {
		a.sendChat(ctx, "当前会话已归档，不能继续聊天。若要继续，请先使用 "+a.commandPrefix()+"unarchive。")
		return nil
	}

	snapshot := a.turns.Snapshot(session.ID)
	if snapshot.Phase != turn.PhaseAwaitRiskConfirm {
		directives := a.applyToolDirectives(ctx, session, text)
		if directives.Err != nil {
			return directives.Err
		}
		if len(directives.Injected) > 0 || len(directives.Existing) > 0 || len(directives.Invalid) > 0 || directives.ModeBlocked {
			a.notifyToolDirectiveResult(ctx, directives)
		}
		text = directives.Text
		skillDirectives := a.applySkillDirectives(ctx, session, text)
		if len(skillDirectives.Skills) > 0 || len(skillDirectives.InjectedWrappers) > 0 || len(skillDirectives.ExistingWrappers) > 0 || len(skillDirectives.Invalid) > 0 || skillDirectives.ModeBlocked {
			a.notifySkillDirectiveResult(ctx, skillDirectives)
		}
		text = skillDirectives.Text
		characterDirectives := a.applyCharacterDirectives(ctx, text)
		if len(characterDirectives.Applied) > 0 || len(characterDirectives.Invalid) > 0 {
			a.notifyCharacterDirectiveResult(ctx, characterDirectives)
		}
		if len(characterDirectives.Applied) > 0 {
			ctx = character.WithActive(ctx, characterDirectives.Applied...)
		}
		text = characterDirectives.Text
		var turnOverride turnDirectiveResult
		ctx, turnOverride = a.applyTurnOverrides(ctx, text)
		if len(turnOverride.Applied) > 0 || len(turnOverride.Invalid) > 0 || len(turnOverride.Denied) > 0 {
			a.notifyTurnOverrideResult(ctx, turnOverride)
		}
		text = turnOverride.Text
		ctx = withInboundSegments(ctx, replaceInboundTextSegments(ctx, text))
		if strings.TrimSpace(text) == "" && !hasInboundNonTextSegment(ctx) {
			if len(directives.Injected) > 0 || len(skillDirectives.InjectedWrappers) > 0 {
				if latest, err := a.store.Sessions().Get(ctx, session.ID); err == nil {
					*session = *latest
				}
			}
			return nil
		}
	}

	switch snapshot.Phase {
	case turn.PhaseAwaitRiskConfirm:
		return a.handleRiskConfirmationInput(ctx, session.ID, text)
	case turn.PhaseAwaitAppendConfirm:
		return a.handleAppendConfirmationInput(ctx, session, text)
	case turn.PhaseLLM:
		for _, requestID := range a.requests.SessionIDs(session.ID) {
			a.markTurnCanceled(requestID)
		}
		a.requests.CancelSession(session.ID)
		a.cancelInboxSession(session.ID)
		if !a.turns.InterruptLLMInput(session.ID, a.turnInputForMessage(ctx, text)) {
			return nil
		}
		timeout := a.confirmationWaitTimeout(ctx)
		a.sendChat(ctx, appendConfirmPromptText(timeout))
		if timeout > 0 {
			waitCtx := context.WithoutCancel(ctx)
			go func() {
				if a.turns.AwaitAppendExpiration(session.ID, timeout) {
					a.sendChat(waitCtx, "追加确认已过期，待追加内容已丢弃，本轮处理已停止。")
				}
			}()
		}
		return nil
	case turn.PhaseTool:
		a.turns.AppendPendingInput(session.ID, a.turnInputForMessage(ctx, text))
		a.sendChat(ctx, fmt.Sprintf("已追加，将在当前流程下一次模型调用时带上。发送 %sstop 可打断当前流程。", a.commandPrefix()))
		return nil
	case turn.PhaseCompact:
		a.sendChat(ctx, fmt.Sprintf("正在压缩上下文，请稍后再发送。可使用 %sstop 取消当前请求。", a.commandPrefix()))
		return nil
	default:
		return a.startChat(ctx, session, text)
	}
}

func (a *Agent) expireIdleCurrentSession(ctx context.Context) error {
	current, err := a.sessions.Current(ctx, a.scope(ctx))
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if a.turns.Snapshot(current.ID).Phase != turn.PhaseIdle {
		return nil
	}
	actor := a.actor(ctx)
	result, err := a.sessions.ExpireIdleCurrent(ctx, session.ExpireIdleRequest{
		Scope:        a.scope(ctx),
		IsSuperadmin: actor.Role == security.RoleSuperadmin,
		Config:       a.idleExpiration,
		Now:          time.Now(),
	})
	if err != nil {
		return err
	}
	if result.Expired {
		a.audit("session_idle_expired", "session_id", result.SessionID, "actor_id", actor.ID, "ttl_minutes", result.TTLMinutes)
	}
	return nil
}

func hasForkFromMessage(ctx context.Context) bool {
	msg, ok := platform.MessageContextFrom(ctx)
	return ok && msg.ForkFromMessageID != ""
}

func (a *Agent) sessionForInput(ctx context.Context, text string) (*storage.Session, error) {
	if msg, ok := platform.MessageContextFrom(ctx); ok {
		if msg.ResumeSessionID != "" {
			session, err := a.sessions.Resume(ctx, a.scope(ctx), msg.ResumeSessionID)
			if err == nil {
				return session, nil
			}
			// A reply to a Session created before shared thread mode was
			// enabled cannot be resumed into the shared scope. Continue in the
			// current shared Session instead of failing the message.
			if !a.scope(ctx).Shared {
				return nil, err
			}
		}
		if msg.ForkFromMessageID != "" {
			return a.sessions.Fork(ctx, a.scope(ctx), msg.ForkFromMessageID)
		}
	}
	mode := a.groupDefaultSessionMode(ctx)
	return a.sessions.GetOrCreateCurrentWithMode(ctx, a.scope(ctx), text, mode)
}

func (a *Agent) handleRiskConfirmationInput(ctx context.Context, sessionID, text string) error {
	confirmation, hasConfirmation := a.turns.PendingRiskConfirmation(sessionID)
	// 风险确认等待期间只接受确认/拒绝/详情/停止类命令。
	// 普通文本不能混入当前 turn，避免被误当作高风险工具的隐式确认
	// 或污染下一次 LLM 调用上下文。
	if !a.commands.IsCommand(text) {
		a.logRiskConfirmationAction(sessionID, "invalid_text", confirmation, "")
		a.sendChat(ctx, riskConfirmationWaitingText(a.commandPrefix()))
		return nil
	}

	parsed := a.commands.Parse(text)
	switch parsed.Name {
	case "detail", "details":
		if !a.turns.RefreshRiskConfirmation(sessionID) {
			return nil
		}
		a.logRiskConfirmationAction(sessionID, "detail", confirmation, "")
		a.sendChat(ctx, riskConfirmationDetailText(confirmation))
	case "confirm", "c":
		a.logRiskConfirmationAction(sessionID, "confirm", confirmation, parsed.Args)
		a.turns.ResolveRiskConfirmation(sessionID, turn.RiskConfirmationResponse{Confirmed: true, Extra: parsed.Args})
	case "confirmtool", "ct":
		a.logRiskConfirmationAction(sessionID, "confirmtool", confirmation, parsed.Args)
		a.turns.ResolveRiskConfirmation(sessionID, turn.RiskConfirmationResponse{Confirmed: true, ConfirmTool: true, Extra: parsed.Args})
	case "confirmall", "ca":
		a.logRiskConfirmationAction(sessionID, "confirmall", confirmation, parsed.Args)
		a.turns.ResolveRiskConfirmation(sessionID, turn.RiskConfirmationResponse{Confirmed: true, ConfirmAll: true, Extra: parsed.Args})

	case "reject":
		a.logRiskConfirmationAction(sessionID, "reject", confirmation, parsed.Args)
		a.turns.ResolveRiskConfirmation(sessionID, turn.RiskConfirmationResponse{Rejected: true, Reason: parsed.Args})
	case "stop":
		a.logRiskConfirmationAction(sessionID, "stop", confirmation, "")
		a.requests.CancelSession(sessionID)
		a.turns.ResolveRiskConfirmation(sessionID, turn.RiskConfirmationResponse{Stopped: true})
		a.sendChat(ctx, "stopped")
	default:
		if hasConfirmation {
			a.logRiskConfirmationAction(sessionID, "invalid_command", confirmation, parsed.Name)
		}
		a.sendChat(ctx, riskConfirmationWaitingText(a.commandPrefix()))

	}
	return nil
}

func (a *Agent) logRiskConfirmationAction(sessionID, action string, confirmation turn.RiskConfirmation, extra string) {
	a.audit("risk_confirmation_command", "session_id", sessionID, "action", action, "tool", confirmation.ToolName, "risk", confirmation.Risk, "extra", extra)
}
