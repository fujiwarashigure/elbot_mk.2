package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"elbot/internal/angelmemory"
	"elbot/internal/logging"
	"elbot/internal/platform"
)

// HandlePlatformEvent handles deterministic platform lifecycle events without
// invoking the LLM. Message events continue to go through HandleMessage.
func (a *Agent) HandlePlatformEvent(ctx context.Context, event platform.PlatformEvent) error {
	if a == nil {
		return nil
	}
	eventCtx := platformContextForEvent(ctx, event)
	scope := a.scope(eventCtx)
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	a.audit("platform_event", "platform", event.Platform, "kind", string(event.Kind), "type", eventType, "scope", scope.PlatformScopeID, "actor", scope.ActorID)

	// Bot self mute/leave/unmute is a local runtime state, not a user work
	// cancellation. Consume it before the member-level notices below.
	if a.updateGroupRuntimeFromEvent(event) {
		return nil
	}

	switch string(event.Kind) {
	case string(platform.EventNotice):
		switch {
		case strings.Contains(eventType, "recall"):
			// Recall is bound to one platform message, not the whole group.
			a.cancelMessageWork(eventCtx, event)
			a.forgetMemoriesForRecalledMessage(eventCtx, event)
			return nil
		case strings.Contains(eventType, "decrease"):
			// Member leave/kick: cancel only that member's in-flight work in
			// this scope. Other members and other scopes are untouched.
			a.cancelUserWork(eventCtx, event)
			return nil
		case strings.Contains(eventType, "ban"):
			subType := strings.ToLower(strings.TrimSpace(fmt.Sprint(event.Meta["qq_onebot.sub_type"])))
			if subType == "lift_ban" {
				return nil
			}
			a.cancelUserWork(eventCtx, event)
			return nil
		default:
			// Member in/out, admin changes and bot mute/leave are observable
			// local events. They never wake the model and never auto-approve
			// requests.
			return nil
		}
	case string(platform.EventRequest):
		// Join/friend requests require a separate superadmin-approved workflow;
		// an ordinary group message or notice must not approve them.
		return nil
	default:
		return nil
	}
}

type messageWorkRef struct {
	Platform  string
	ScopeID   string
	SessionID string
	RequestID string
}

func messageWorkKey(platformName, scopeID, messageID string) string {
	return strings.Join([]string{strings.TrimSpace(platformName), strings.TrimSpace(scopeID), strings.TrimSpace(messageID)}, "\x00")
}

type messageWorkKeysContextKey struct{}

func withMessageWorkKeys(ctx context.Context, keys []string) context.Context {
	if len(keys) == 0 {
		return ctx
	}
	return context.WithValue(ctx, messageWorkKeysContextKey{}, append([]string(nil), keys...))
}

func messageWorkKeysFromContext(ctx context.Context) []string {
	keys, _ := ctx.Value(messageWorkKeysContextKey{}).([]string)
	return keys
}

func withoutMessageWorkKeys(ctx context.Context) context.Context {
	return context.WithValue(ctx, messageWorkKeysContextKey{}, []string(nil))
}

// registerMessageWork binds the current platform message (and any extra
// messages merged into the same turn) to the turn request that is processing
// them. Recall can then cancel exactly that request.
func (a *Agent) registerMessageWork(ctx context.Context, sessionID, requestID string) bool {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return false
	}
	msg, ok := platform.MessageContextFrom(ctx)
	if !ok {
		return false
	}
	keys := make([]string, 0, 1+len(messageWorkKeysFromContext(ctx)))
	if strings.TrimSpace(msg.PlatformMessageID) != "" {
		keys = append(keys, messageWorkKey(msg.Platform, msg.ScopeID, msg.PlatformMessageID))
	}
	keys = append(keys, messageWorkKeysFromContext(ctx)...)
	if len(keys) == 0 {
		return false
	}
	seen := map[string]bool{}
	a.messageWorkMu.Lock()
	if a.messageWork == nil {
		a.messageWork = map[string]messageWorkRef{}
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		a.messageWork[key] = messageWorkRef{Platform: msg.Platform, ScopeID: msg.ScopeID, SessionID: sessionID, RequestID: requestID}
	}
	a.messageWorkMu.Unlock()
	for key := range seen {
		if !a.consumeRecentRecall(key) {
			continue
		}
		// The recall arrived before this message finished registering (for
		// example after a reconnect). Cancel the just-bound request and tell
		// the caller not to continue producing output for it.
		a.markTurnCanceled(requestID)
		if a.requests != nil {
			a.requests.Cancel(requestID)
		}
		a.audit("platform_recall_cancel_late", "platform", msg.Platform, "scope", msg.ScopeID, "message_id", msg.PlatformMessageID, "request_id", requestID, "result", logging.ResultCanceled)
		return true
	}
	return false
}

func (a *Agent) unregisterMessageWork(requestID string) {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	a.messageWorkMu.Lock()
	for key, ref := range a.messageWork {
		if ref.RequestID == requestID {
			delete(a.messageWork, key)
		}
	}
	a.messageWorkMu.Unlock()
	a.clearTurnState(requestID)
}

func (a *Agent) cancelMessageWork(ctx context.Context, event platform.PlatformEvent) {
	key := messageWorkKey(event.Platform, event.ScopeID, event.MessageID)
	if a.cancelInboxMessage(key) > 0 {
		a.audit("platform_recall_cancel_queued", "platform", event.Platform, "scope", event.ScopeID, "message_id", event.MessageID, "result", logging.ResultCanceled)
		return
	}
	a.messageWorkMu.Lock()
	ref, ok := a.messageWork[key]
	if ok {
		delete(a.messageWork, key)
	}
	a.messageWorkMu.Unlock()
	if !ok {
		a.rememberRecentRecall(key)
		a.audit("platform_recall_noop", "platform", event.Platform, "scope", event.ScopeID, "message_id", event.MessageID, "result", logging.ResultSkipped)
		return
	}
	a.markTurnCanceled(ref.RequestID)
	if a.requests != nil {
		a.requests.Cancel(ref.RequestID)
	}
	a.audit("platform_recall_cancel", "platform", event.Platform, "scope", event.ScopeID, "message_id", event.MessageID, "session_id", ref.SessionID, "request_id", ref.RequestID, "result", logging.ResultCanceled)
}

func (a *Agent) recallTombstoneTTL() time.Duration {
	ttl := a.responseTimeout
	if ttl < time.Minute {
		ttl = time.Minute
	}
	if ttl > 15*time.Minute {
		ttl = 15 * time.Minute
	}
	return ttl
}

func (a *Agent) rememberRecentRecall(key string) {
	if a == nil || strings.TrimSpace(key) == "" {
		return
	}
	now := time.Now()
	ttl := a.recallTombstoneTTL()
	a.recallMu.Lock()
	if a.recentRecalls == nil {
		a.recentRecalls = map[string]time.Time{}
	}
	for existing, at := range a.recentRecalls {
		if now.Sub(at) > ttl {
			delete(a.recentRecalls, existing)
		}
	}
	a.recentRecalls[key] = now
	a.recallMu.Unlock()
}

func (a *Agent) consumeRecentRecall(key string) bool {
	if a == nil || strings.TrimSpace(key) == "" {
		return false
	}
	a.recallMu.Lock()
	defer a.recallMu.Unlock()
	at, ok := a.recentRecalls[key]
	if !ok {
		return false
	}
	delete(a.recentRecalls, key)
	return time.Since(at) <= a.recallTombstoneTTL()
}

func (a *Agent) cancelUserWork(ctx context.Context, event platform.PlatformEvent) {
	if a.requests == nil {
		return
	}
	userID := strings.TrimSpace(event.UserID)
	if userID == "" {
		return
	}
	fairKey := a.requestFairKey(ctx)
	for _, requestID := range a.requests.FairKeyIDs(fairKey) {
		a.markTurnCanceled(requestID)
	}
	count := a.requests.CancelFairKey(fairKey)
	count += a.cancelInboxFairKey(fairKey)
	a.audit("platform_user_cancel", "platform", event.Platform, "scope", event.ScopeID, "user_id", userID, "fair_key", fairKey, "count", count, "result", logging.ResultCanceled)
}

func (a *Agent) forgetMemoriesForRecalledMessage(ctx context.Context, event platform.PlatformEvent) {
	if a == nil || a.angelMemory == nil || !a.angelMemory.Ready() || !a.angelMemoryForgetOnRecall {
		return
	}
	platformName := strings.TrimSpace(event.Platform)
	scopeID := strings.TrimSpace(event.ScopeID)
	messageID := strings.TrimSpace(event.MessageID)
	if platformName == "" || scopeID == "" || messageID == "" {
		return
	}
	count, err := a.angelMemory.DeleteBySource(ctx, platformName, scopeID, angelmemory.SourceFilter{MessageID: messageID})
	if err != nil {
		if a.logger != nil {
			a.logger.WarnContext(ctx, "forget recalled angel memory failed", "platform", platformName, "scope", scopeID, "message_id", messageID, "error", err.Error())
		}
		a.audit("angel_memory_forget_failed", "platform", platformName, "scope", scopeID, "message_id", messageID, "error", err.Error(), "result", logging.ResultFailed)
		return
	}
	if count > 0 {
		a.audit("angel_memory_forget_recalled", "platform", platformName, "scope", scopeID, "message_id", messageID, "count", count, "result", logging.ResultSucceeded)
	}
}

func platformContextForEvent(ctx context.Context, event platform.PlatformEvent) context.Context {
	kind := platform.ConversationUnknown
	switch {
	case strings.HasPrefix(event.ScopeID, "group:"), strings.HasPrefix(event.ScopeID, "supergroup:"):
		kind = platform.ConversationGroup
	case strings.HasPrefix(event.ScopeID, "private:"), strings.HasPrefix(event.ScopeID, "c2c:"):
		kind = platform.ConversationPrivate
	}
	return platform.WithMessageContext(ctx, platform.MessageContext{
		Platform:          event.Platform,
		PlatformUserID:    strings.TrimSpace(event.UserID),
		ScopeID:           strings.TrimSpace(event.ScopeID),
		ConversationKind:  kind,
		PlatformMessageID: strings.TrimSpace(event.MessageID),
	})
}
