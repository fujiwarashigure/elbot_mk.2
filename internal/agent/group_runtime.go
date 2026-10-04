package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/delivery"
	"elbot/internal/platform"
	"elbot/internal/session"
)

const (
	groupRuntimeActive      = "active"
	groupRuntimeMuted       = "muted"
	groupRuntimeRemoved     = "removed"
	groupRuntimeUnavailable = "unavailable"
)

func cloneGroupRuntime(input map[string]config.GroupRuntimeConfig) map[string]config.GroupRuntimeConfig {
	if len(input) == 0 {
		return map[string]config.GroupRuntimeConfig{}
	}
	out := make(map[string]config.GroupRuntimeConfig, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value.State = normalizeGroupRuntimeState(value.State)
		if value.State == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func normalizeGroupRuntimeState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case groupRuntimeActive:
		return groupRuntimeActive
	case groupRuntimeMuted:
		return groupRuntimeMuted
	case groupRuntimeRemoved:
		return groupRuntimeRemoved
	case groupRuntimeUnavailable:
		return groupRuntimeUnavailable
	default:
		return ""
	}
}

type groupRuntimeNoticeBypassKey struct{}

func withGroupRuntimeNoticeBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, groupRuntimeNoticeBypassKey{}, true)
}

func groupRuntimeNoticeBypass(ctx context.Context) bool {
	value, _ := ctx.Value(groupRuntimeNoticeBypassKey{}).(bool)
	return value
}

func groupRuntimeBlockedState(state string) bool {
	switch normalizeGroupRuntimeState(state) {
	case groupRuntimeMuted, groupRuntimeRemoved, groupRuntimeUnavailable:
		return true
	default:
		return false
	}
}

func (a *Agent) setGroupRuntimeSnapshot(snapshot map[string]config.GroupRuntimeConfig) {
	a.groupRuntimeMu.Lock()
	a.groupRuntime = cloneGroupRuntime(snapshot)
	a.groupRuntimeMu.Unlock()
}

func (a *Agent) groupRuntimeSnapshot() map[string]config.GroupRuntimeConfig {
	a.groupRuntimeMu.RLock()
	defer a.groupRuntimeMu.RUnlock()
	return cloneGroupRuntime(a.groupRuntime)
}

func (a *Agent) groupRuntimeForScope(scope session.Scope) config.GroupRuntimeConfig {
	key := contextOverflowKey(scope)
	a.groupRuntimeMu.RLock()
	defer a.groupRuntimeMu.RUnlock()
	return a.groupRuntime[key]
}

// transitionGroupRuntimeForScope atomically applies one runtime state and
// returns the previous state plus whether it changed. The compare-and-swap is
// under the same write lock so two simultaneous ban events cannot both send a
// transition notice.
func (a *Agent) transitionGroupRuntimeForScope(scope session.Scope, value config.GroupRuntimeConfig) (string, bool) {
	key := contextOverflowKey(scope)
	value.State = normalizeGroupRuntimeState(value.State)
	a.groupRuntimeMu.Lock()
	defer a.groupRuntimeMu.Unlock()
	if a.groupRuntime == nil {
		a.groupRuntime = map[string]config.GroupRuntimeConfig{}
	}
	previous := normalizeGroupRuntimeState(a.groupRuntime[key].State)
	if previous == value.State {
		return previous, false
	}
	if value.State == "" || value.State == groupRuntimeActive {
		delete(a.groupRuntime, key)
	} else {
		a.groupRuntime[key] = value
	}
	return previous, true
}

// groupRuntimeBlocked reports whether the group scope attached to ctx is in a
// state where new paid calls and outbound messages must stop. Private scopes
// and contexts without a group scope are never blocked here.
func (a *Agent) groupRuntimeBlocked(ctx context.Context) bool {
	if a == nil {
		return false
	}
	if ctx != nil {
		if msg, ok := platform.MessageContextFrom(ctx); ok {
			return a.groupRuntimeBlockedScope(session.Scope{Platform: msg.Platform, PlatformScopeID: msg.ScopeID})
		}
	}
	return false
}

func (a *Agent) groupRuntimeBlockedScope(scope session.Scope) bool {
	scopeID := strings.TrimSpace(scope.PlatformScopeID)
	if !strings.HasPrefix(scopeID, "group:") && !strings.HasPrefix(scopeID, "supergroup:") {
		return false
	}
	return groupRuntimeBlockedState(a.groupRuntimeForScope(scope).State)
}

// applyGroupRuntimeState records one transition, cancels existing work and
// sends one aggregated superadmin notice. Repeated identical events are
// idempotent and do not notify again.
func (a *Agent) applyGroupRuntimeState(scope session.Scope, state, reason string) bool {
	state = normalizeGroupRuntimeState(state)
	if state == "" {
		return false
	}
	_, changed := a.transitionGroupRuntimeForScope(scope, config.GroupRuntimeConfig{
		State:     state,
		Reason:    strings.TrimSpace(reason),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if !changed {
		return false
	}
	if groupRuntimeBlockedState(state) {
		a.cancelScopeWork(scope, state, reason)
	}
	a.notifyGroupRuntimeChange(scope, state, reason)
	if err := a.saveRuntimeState(); err != nil && a.logger != nil {
		a.logger.Warn("persist group runtime state failed", "scope", contextOverflowKey(scope), "state", state, "error", err.Error())
	}
	a.audit("group_runtime_state", "platform", scope.Platform, "scope", scope.PlatformScopeID, "state", state, "reason", strings.TrimSpace(reason))
	return true
}

func (a *Agent) cancelScopeWork(scope session.Scope, state, reason string) {
	if a == nil || a.requests == nil {
		return
	}
	scopeKey := requestScopeKeyFromScope(scope)
	for _, requestID := range a.requests.ScopeIDs(scopeKey) {
		a.markTurnCanceled(requestID)
	}
	count := a.requests.CancelScope(scopeKey)
	count += a.cancelInboxScope(scopeKey)
	a.audit("group_runtime_cancel", "platform", scope.Platform, "scope", scope.PlatformScopeID, "state", state, "reason", strings.TrimSpace(reason), "count", count)
}

func (a *Agent) notifyGroupRuntimeChange(scope session.Scope, state, reason string) {
	if a == nil {
		return
	}
	platformName := strings.TrimSpace(scope.Platform)
	if platformName == "" && a.platform != nil {
		platformName = a.platform.Name()
	}
	if platformName == "" {
		return
	}
	text := fmt.Sprintf("群 %s 运行状态变为 %s（%s）。已暂停该群新请求；在途输出已取消。", strings.TrimSpace(scope.PlatformScopeID), state, strings.TrimSpace(reason))
	_, err := a.SendNotice(withGroupRuntimeNoticeBypass(context.Background()), delivery.Notice{
		Target:  delivery.Target{Platform: platformName, ScopeID: scope.PlatformScopeID, Superadmins: true},
		Outputs: []delivery.Output{delivery.Text(text)},
		Level:   slog.LevelWarn,
	})
	if err != nil && a.logger != nil {
		a.logger.Warn("notify group runtime state failed", "scope", contextOverflowKey(scope), "state", state, "error", err.Error())
	}
}

// noticeTargetBlocked reports whether an explicit notice target points at a
// paused group scope. Context-routed notices are already covered by
// turnOutputAllowed; this covers cron, reminder and hook notices that carry an
// explicit group target.
func (a *Agent) noticeTargetBlocked(target delivery.Target) bool {
	if a == nil {
		return false
	}
	scopeID := strings.TrimSpace(target.ScopeID)
	if scopeID == "" {
		if groupID := strings.TrimSpace(target.GroupID); groupID != "" {
			scopeID = "group:" + groupID
		}
	}
	if !isGroupScopeID(scopeID) {
		return false
	}
	return a.groupRuntimeBlockedScope(session.Scope{Platform: strings.TrimSpace(target.Platform), PlatformScopeID: scopeID})
}

func isGroupScopeID(scopeID string) bool {
	scopeID = strings.TrimSpace(scopeID)
	return strings.HasPrefix(scopeID, "group:") || strings.HasPrefix(scopeID, "supergroup:")
}

// updateGroupRuntimeFromEvent maps OneBot self-targeted notice events to the
// group runtime state machine. It returns true when the event was consumed.
func (a *Agent) updateGroupRuntimeFromEvent(event platform.PlatformEvent) bool {
	if a == nil {
		return false
	}
	scopeID := strings.TrimSpace(event.ScopeID)
	if !isGroupScopeID(scopeID) {
		return false
	}
	selfID := strings.TrimSpace(fmt.Sprint(event.Meta["qq_onebot.self_id"]))
	if selfID == "" || selfID == "<nil>" {
		return false
	}
	noticeType := strings.ToLower(strings.TrimSpace(fmt.Sprint(event.Meta["qq_onebot.notice_type"])))
	if noticeType == "" || noticeType == "<nil>" {
		noticeType = strings.ToLower(strings.TrimSpace(event.Type))
	}
	subType := strings.ToLower(strings.TrimSpace(fmt.Sprint(event.Meta["qq_onebot.sub_type"])))
	targetID := strings.TrimSpace(fmt.Sprint(event.Meta["qq_onebot.target_id"]))
	userID := strings.TrimSpace(event.UserID)
	if userID != selfID && targetID != selfID {
		return false
	}
	scope := session.Scope{Platform: strings.TrimSpace(event.Platform), PlatformScopeID: scopeID}
	switch {
	case strings.Contains(noticeType, "ban"):
		if subType == "lift_ban" {
			return a.applyGroupRuntimeState(scope, groupRuntimeActive, "self_unmuted")
		}
		return a.applyGroupRuntimeState(scope, groupRuntimeMuted, "self_muted")
	case strings.Contains(noticeType, "decrease"):
		return a.applyGroupRuntimeState(scope, groupRuntimeRemoved, "self_removed")
	case strings.Contains(noticeType, "increase"):
		return a.applyGroupRuntimeState(scope, groupRuntimeActive, "self_joined")
	default:
		return false
	}
}
