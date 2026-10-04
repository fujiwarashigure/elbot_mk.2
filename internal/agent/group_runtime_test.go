package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/delivery"
	"elbot/internal/platform"
)

func TestSelfMutePausesGroupCallsOutputAndPersists(t *testing.T) {
	adapter := &fakePlatform{}
	a := New(adapter, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	a.RegisterPlatformSender("qqonebot", adapter)
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qqonebot",
		PlatformUserID:   "member",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
	})
	event := platform.PlatformEvent{
		Platform: "qqonebot",
		Kind:     platform.EventNotice,
		Type:     "group_ban",
		ScopeID:  "group:9",
		UserID:   "1000",
		Meta: map[string]any{
			"qq_onebot.self_id":     "1000",
			"qq_onebot.notice_type": "group_ban",
			"qq_onebot.sub_type":    "ban",
		},
	}
	if err := a.HandlePlatformEvent(ctx, event); err != nil {
		t.Fatalf("HandlePlatformEvent mild mute: %v", err)
	}
	if !a.groupRuntimeBlocked(ctx) {
		t.Fatal("self mute should block the group")
	}
	if a.turnOutputAllowed(ctx) {
		t.Fatal("self mute should stop turn output")
	}
	if err := a.authorizeExecutionModelSelection(ctx, config.ModelSelection{Provider: "default", Model: "test-model"}); err == nil {
		t.Fatal("self mute should stop new model calls")
	}
	if err := a.sendNoticeBlockedForTest(context.Background(), "qqonebot", "group:9"); err != nil {
		t.Fatalf("explicit group notice: %v", err)
	}
	// The mute transition notice is sent once; the explicit blocked notice is
	// dropped. The fake adapter records only the transition text.
	if got := strings.Count(adapter.out.String(), "运行状态变为 muted"); got != 1 {
		t.Fatalf("mute notices = %d, output = %q", got, adapter.out.String())
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got := state.GroupRuntime["qqonebot:group:9"].State; got != groupRuntimeMuted {
		t.Fatalf("persisted state = %q", got)
	}

	// Replaying the same ban event is idempotent and must not notify again.
	if err := a.HandlePlatformEvent(ctx, event); err != nil {
		t.Fatalf("HandlePlatformEvent duplicate mute: %v", err)
	}
	if got := strings.Count(adapter.out.String(), "运行状态变为 muted"); got != 1 {
		t.Fatalf("duplicate mute notified again: %d", got)
	}

	lift := event
	lift.Meta = map[string]any{
		"qq_onebot.self_id":     "1000",
		"qq_onebot.notice_type": "group_ban",
		"qq_onebot.sub_type":    "lift_ban",
	}
	if err := a.HandlePlatformEvent(ctx, lift); err != nil {
		t.Fatalf("HandlePlatformEvent unmute: %v", err)
	}
	if a.groupRuntimeBlocked(ctx) {
		t.Fatal("unmute should restore the group")
	}
	state, err = config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState after unmute: %v", err)
	}
	if _, ok := state.GroupRuntime["qqonebot:group:9"]; ok {
		t.Fatalf("active state should be removed, got %#v", state.GroupRuntime["qqonebot:group:9"])
	}
}

func TestSelfRemovalBlocksGroup(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qqonebot",
		ScopeID:          "group:11",
		ConversationKind: platform.ConversationGroup,
	})
	event := platform.PlatformEvent{
		Platform: "qqonebot",
		Kind:     platform.EventNotice,
		Type:     "group_decrease",
		ScopeID:  "group:11",
		UserID:   "1000",
		Meta: map[string]any{
			"qq_onebot.self_id":     "1000",
			"qq_onebot.notice_type": "group_decrease",
			"qq_onebot.sub_type":    "kick_me",
		},
	}
	if err := a.HandlePlatformEvent(ctx, event); err != nil {
		t.Fatalf("HandlePlatformEvent self kick: %v", err)
	}
	if !a.groupRuntimeBlocked(ctx) {
		t.Fatal("self removal should block the group")
	}
	if state := a.groupRuntimeForScope(a.scope(ctx)).State; state != groupRuntimeRemoved {
		t.Fatalf("state = %q", state)
	}
}
func (a *Agent) sendNoticeBlockedForTest(ctx context.Context, platformName, scopeID string) error {
	_, err := a.SendNotice(ctx, delivery.Notice{
		Target:  delivery.Target{Platform: platformName, ScopeID: scopeID},
		Outputs: []delivery.Output{delivery.Text("should be dropped")},
	})
	return err
}
