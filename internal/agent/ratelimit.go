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

func (a *Agent) allowInbound(ctx context.Context) (bool, time.Duration) {
	if a == nil || (a.rateLimitUser == nil && a.rateLimitGroup == nil) {
		return true, 0
	}
	if actor := a.actor(ctx); actor.Role == security.RoleSuperadmin {
		return true, 0
	}
	limiter, key := a.rateLimitTarget(ctx)
	if limiter == nil || key == "" {
		return true, 0
	}
	decision := limiter.Allow(key)
	if decision.Allowed {
		a.rateLimitAllowed.Add(1)
		return true, 0
	}
	a.rateLimitRejected.Add(1)
	return false, decision.RetryAfter
}

func (a *Agent) rateLimitTarget(ctx context.Context) (*ratelimit.Limiter, string) {
	scope := a.scope(ctx)
	platformName := strings.TrimSpace(scope.Platform)
	if platformName == "" {
		platformName = "unknown"
	}
	group := false
	if msg, ok := platform.MessageContextFrom(ctx); ok {
		group = msg.ConversationKind == platform.ConversationGroup
	}
	if !group {
		scopeID := strings.TrimSpace(scope.PlatformScopeID)
		group = strings.HasPrefix(scopeID, "group:") || strings.HasPrefix(scopeID, "supergroup:")
	}
	if group && a.rateLimitGroup != nil {
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

// RateLimitStats returns counters and the number of tracked rate-limit keys.
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
