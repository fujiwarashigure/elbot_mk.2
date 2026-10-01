package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"elbot/internal/ops/ratelimit"
	"elbot/internal/platform"
	"elbot/internal/security"
)

// RateLimitStatus is the observability view of the inbound rate limiter.
type RateLimitStatus struct {
	Allowed             int64      `json:"allowed"`
	Rejected            int64      `json:"rejected"`
	UserRejected        int64      `json:"user_rejected"`
	GroupRejected       int64      `json:"group_rejected"`
	Keys                int        `json:"keys"`
	UserMessagesPerMin  int        `json:"user_messages_per_minute"`
	UserBurst           int        `json:"user_burst"`
	GroupMessagesPerMin int        `json:"group_messages_per_minute"`
	GroupBurst          int        `json:"group_burst"`
	LastReason          string     `json:"last_reason,omitempty"`
	LastRejectedAt      *time.Time `json:"last_rejected_at,omitempty"`
}

func (a *Agent) allowInbound(ctx context.Context) (bool, time.Duration) {
	allowed, retryAfter, _ := a.allowInboundDetailed(ctx)
	return allowed, retryAfter
}

// allowInboundDetailed applies user quota first, then group quota. A group
// quota protects the whole group from being exhausted by one active member,
// while the user quota stops any single member from monopolising the group
// allowance.
func (a *Agent) allowInboundDetailed(ctx context.Context) (bool, time.Duration, string) {
	if a == nil || (a.rateLimitUser == nil && a.rateLimitGroup == nil) {
		return true, 0, ""
	}
	if actor := a.actor(ctx); actor.Role == security.RoleSuperadmin {
		return true, 0, ""
	}
	scope := a.scope(ctx)
	platformName := strings.TrimSpace(scope.Platform)
	if platformName == "" {
		platformName = "unknown"
	}
	actorID := strings.TrimSpace(scope.ActorID)
	if actorID == "" {
		actorID = "unknown"
	}

	if a.rateLimitUser != nil {
		decision := a.rateLimitUser.Allow(platformName + ":user:" + actorID)
		if !decision.Allowed {
			a.recordRateLimitRejection("user", decision.RetryAfter)
			return false, decision.RetryAfter, "user"
		}
	}
	if a.isGroupContext(ctx) && a.rateLimitGroup != nil {
		scopeID := strings.TrimSpace(scope.PlatformScopeID)
		if scopeID == "" {
			scopeID = "unknown"
		}
		decision := a.rateLimitGroup.Allow(platformName + ":group:" + scopeID)
		if !decision.Allowed {
			a.recordRateLimitRejection("group", decision.RetryAfter)
			return false, decision.RetryAfter, "group"
		}
	}
	a.rateLimitAllowed.Add(1)
	return true, 0, ""
}

func (a *Agent) isGroupContext(ctx context.Context) bool {
	if msg, ok := platform.MessageContextFrom(ctx); ok && msg.ConversationKind == platform.ConversationGroup {
		return true
	}
	scopeID := strings.TrimSpace(a.scope(ctx).PlatformScopeID)
	return strings.HasPrefix(scopeID, "group:") || strings.HasPrefix(scopeID, "supergroup:")
}

func (a *Agent) recordRateLimitRejection(reason string, retryAfter time.Duration) {
	a.rateLimitRejected.Add(1)
	switch reason {
	case "user":
		a.rateLimitUserRejected.Add(1)
	case "group":
		a.rateLimitGroupRejected.Add(1)
	}
	a.rateLimitMu.Lock()
	a.rateLimitLastReason = reason
	a.rateLimitLastRejectedAt = time.Now()
	a.rateLimitMu.Unlock()
	_ = retryAfter
}

// rateLimitTarget is kept for callers/tests that need the legacy single-key
// selection. New code should use allowInboundDetailed, which checks both keys.
func (a *Agent) rateLimitTarget(ctx context.Context) (*ratelimit.Limiter, string) {
	scope := a.scope(ctx)
	platformName := strings.TrimSpace(scope.Platform)
	if platformName == "" {
		platformName = "unknown"
	}
	if a.isGroupContext(ctx) && a.rateLimitGroup != nil {
		return a.rateLimitGroup, platformName + ":group:" + strings.TrimSpace(scope.PlatformScopeID)
	}
	if a.rateLimitUser == nil {
		return nil, ""
	}
	actorID := strings.TrimSpace(scope.ActorID)
	if actorID == "" {
		actorID = "unknown"
	}
	return a.rateLimitUser, platformName + ":private:" + actorID
}

func rateLimitRetryText(retryAfter time.Duration) string {
	if retryAfter <= 0 {
		return "消息发送过于频繁，请稍后再试。"
	}
	seconds := int(retryAfter.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("消息发送过于频繁，请在 %d 秒后重试。", seconds)
}

// RateLimitStats returns legacy aggregate counters and the number of tracked keys.
func (a *Agent) RateLimitStats() (allowed, rejected int64, keys int) {
	if a == nil {
		return 0, 0, 0
	}
	if a.rateLimitUser != nil {
		keys += a.rateLimitUser.Keys()
	}
	if a.rateLimitGroup != nil {
		keys += a.rateLimitGroup.Keys()
	}
	return a.rateLimitAllowed.Load(), a.rateLimitRejected.Load(), keys
}

// RateLimitStatus returns detailed counters plus configured thresholds.
func (a *Agent) RateLimitStatus() RateLimitStatus {
	if a == nil {
		return RateLimitStatus{}
	}
	status := RateLimitStatus{
		Allowed:             a.rateLimitAllowed.Load(),
		Rejected:            a.rateLimitRejected.Load(),
		UserRejected:        a.rateLimitUserRejected.Load(),
		GroupRejected:       a.rateLimitGroupRejected.Load(),
		UserMessagesPerMin:  a.rateLimitUserPerMinute,
		UserBurst:           a.rateLimitUserBurst,
		GroupMessagesPerMin: a.rateLimitGroupPerMinute,
		GroupBurst:          a.rateLimitGroupBurst,
	}
	if a.rateLimitUser != nil {
		status.Keys += a.rateLimitUser.Keys()
	}
	if a.rateLimitGroup != nil {
		status.Keys += a.rateLimitGroup.Keys()
	}
	a.rateLimitMu.Lock()
	status.LastReason = a.rateLimitLastReason
	if !a.rateLimitLastRejectedAt.IsZero() {
		last := a.rateLimitLastRejectedAt
		status.LastRejectedAt = &last
	}
	a.rateLimitMu.Unlock()
	return status
}
