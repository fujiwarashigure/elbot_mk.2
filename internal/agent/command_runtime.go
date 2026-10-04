package agent

import (
	"context"
	"fmt"

	"elbot/internal/command"
	"elbot/internal/security"
	"elbot/internal/session"
	"elbot/internal/storage"
	"elbot/internal/turn"
)

type commandExecutor struct {
	router          *command.Router
	sessions        *session.Service
	turns           *turn.Manager
	scope           func(context.Context) session.Scope
	compactActive   func(string) bool
	sendChat        func(context.Context, string)
	sendNotice      func(context.Context, string) error
	audit           func(string, ...any)
	handleAppend    func(context.Context, *storage.Session, string) error
	handleRisk      func(context.Context, string, string) error
	continueInput   func(context.Context, command.Continuation) error
	groupAdminGrant func(context.Context, string) bool
}

func (e *commandExecutor) Handle(ctx context.Context, text string) (bool, error) {
	if e == nil || e.router == nil || !e.router.IsCommand(text) {
		return false, nil
	}

	parsed := e.router.Parse(text)
	info, hasInfo := e.router.CommandInfo(parsed.Name)
	sessionRow, sessionErr := e.sessions.Current(ctx, e.scope(ctx))
	snapshot := turn.Snapshot{Phase: turn.PhaseIdle}
	if sessionErr == nil {
		snapshot = e.turns.Snapshot(sessionRow.ID)
	}
	if sessionErr == nil && snapshot.Phase == turn.PhaseAwaitAppendConfirm && (turn.IsConfirm(text) || turn.IsCancel(text)) {
		return true, e.handleAppend(ctx, sessionRow, text)
	}
	if sessionErr == nil && snapshot.Phase == turn.PhaseAwaitRiskConfirm && isRiskConfirmationCommand(text, e.router) {
		return true, e.handleRisk(ctx, sessionRow.ID, text)
	}
	if sessionErr == nil && hasInfo && snapshot.Phase != turn.PhaseIdle && blocksDuringActiveTurn(info.SessionEffect) {
		e.sendChat(ctx, e.activeTurnCommandBlockedText())
		return true, nil
	}
	if sessionErr == nil && hasInfo && e.compactActive(sessionRow.ID) && blocksDuringCompact(info.SessionEffect) {
		e.sendChat(ctx, e.compactCommandBlockedText(text))
		return true, nil
	}

	actor, _ := security.ActorFromContext(ctx)
	if hasInfo && e.scope(ctx).Shared && info.SessionEffect&(command.SessionEffectSwitchCurrent|command.SessionEffectMutate) != 0 {
		if actor.Role != security.RoleSuperadmin && actor.GroupRole != security.GroupRoleOwner && actor.GroupRole != security.GroupRoleAdmin {
			e.sendChat(ctx, "当前群使用共享会话，会话切换/修改命令仅限群主、群管理员或机器人超级管理员。")
			return true, nil
		}
	}
	allowed := hasInfo && command.CanAccess(info, actor)
	if hasInfo && info.GroupAdminNeedsGrant {
		// Group admin access is explicitly delegated per group. Do not let
		// AllowGroupAdmin alone grant this command; only the server-side grant
		// callback (or superadmin) may unlock it.
		allowed = actor.Role == security.RoleSuperadmin || (e.groupAdminGrant != nil && e.groupAdminGrant(ctx, parsed.Name))
	}
	if hasInfo && !allowed {
		e.audit("permission_denied", "actor_id", actor.ID, "command", text, "reason", "slash_command_requires_superadmin")
		required := "超级管理员"
		if info.GroupAdminNeedsGrant {
			required = "超级管理员，或已由超级管理员显式授权的当前群管理员"
		} else if info.AllowGroupAdmin {
			required = "超级管理员或群管理员"
		}
		e.sendChat(ctx, fmt.Sprintf("命令 %s%s 需要%s权限。", parsed.Prefix, parsed.Name, required))
		return true, nil
	}

	result, err := e.router.Dispatch(ctx, text)
	if err != nil {
		return true, err
	}
	if result == nil {
		return true, nil
	}
	if result.Content != "" {
		if err := e.sendNotice(ctx, result.Content); err != nil {
			return true, err
		}
	}
	if result.Continuation != nil {
		return true, e.continueInput(ctx, *result.Continuation)
	}
	return true, nil
}

func blocksDuringActiveTurn(effect command.SessionEffect) bool {
	return effect&command.SessionEffectSwitchCurrent != 0
}

func blocksDuringCompact(effect command.SessionEffect) bool {
	return effect&(command.SessionEffectSwitchCurrent|command.SessionEffectMutate) != 0
}
