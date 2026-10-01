package agent

import (
	"context"
	"testing"

	"elbot/internal/security"
)

func TestAllowInboundStacksUserAndGroupLimits(t *testing.T) {
	a := &Agent{
		platform:       &fakePlatform{},
		securityPolicy: security.DefaultPolicy(),
		actorID:        "u1",
		scopeID:        "group:g1",
	}
	a.rateLimitUser = newRateLimiter(60, 1, 600)
	a.rateLimitGroup = newRateLimiter(60, 1, 600)

	if allowed, _, reason := a.allowInboundDetailed(context.Background()); !allowed || reason != "" {
		t.Fatalf("first message allowed=%v reason=%q", allowed, reason)
	}
	allowed, _, reason := a.allowInboundDetailed(context.Background())
	if allowed || reason != "user" {
		t.Fatalf("second message allowed=%v reason=%q, want user rejection", allowed, reason)
	}
	if status := a.RateLimitStatus(); status.UserRejected != 1 || status.GroupRejected != 0 || status.LastReason != "user" {
		t.Fatalf("rate limit status = %#v", status)
	}
}

func TestAllowInboundGroupQuotaProtectsWholeGroup(t *testing.T) {
	a := &Agent{
		platform:       &fakePlatform{},
		securityPolicy: security.DefaultPolicy(),
		actorID:        "u1",
		scopeID:        "group:g1",
	}
	// The active user still has personal quota, but the group bucket is spent.
	a.rateLimitUser = newRateLimiter(60, 2, 600)
	a.rateLimitGroup = newRateLimiter(60, 1, 600)

	if allowed, _, reason := a.allowInboundDetailed(context.Background()); !allowed || reason != "" {
		t.Fatalf("first message allowed=%v reason=%q", allowed, reason)
	}
	allowed, _, reason := a.allowInboundDetailed(context.Background())
	if allowed || reason != "group" {
		t.Fatalf("second message allowed=%v reason=%q, want group rejection", allowed, reason)
	}
	if status := a.RateLimitStatus(); status.GroupRejected != 1 || status.UserRejected != 0 || status.LastReason != "group" {
		t.Fatalf("rate limit status = %#v", status)
	}
}
