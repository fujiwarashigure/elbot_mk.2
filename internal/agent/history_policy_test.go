package agent

import (
	"context"
	"testing"

	"elbot/internal/config"
	"elbot/internal/platform"
	"elbot/internal/security"
	"elbot/internal/storage"
)

func TestHistoryOffStopsTranscriptPersistenceButKeepsTurnInMemory(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qqonebot",
		PlatformUserID:   "member",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
		GroupRole:        security.GroupRoleMember,
	})
	off := false
	a.setGroupPolicyForScope(a.scope(ctx), config.GroupPolicyConfig{History: &off})
	if a.historyEnabled(ctx) {
		t.Fatal("history should be disabled for the group")
	}
	session := &storage.Session{OwnerID: "member", Platform: "qqonebot", PlatformScopeID: "group:9", Mode: storage.SessionModeWork, Status: storage.SessionStatusActive}
	if err := a.store.Sessions().Create(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	user := &storage.Message{SessionID: session.ID, Role: storage.RoleUser, Content: "secret transcript"}
	if err := a.persistTurnMessage(ctx, user, "test_disabled_history"); err != nil {
		t.Fatalf("persist disabled history: %v", err)
	}
	assistant := &storage.Message{SessionID: session.ID, Role: storage.RoleAssistant, Content: "secret answer"}
	if err := a.persistTurnMessage(ctx, assistant, "test_disabled_history"); err != nil {
		t.Fatalf("persist disabled assistant: %v", err)
	}
	messages, err := a.store.Messages().ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("history=off persisted %d session messages: %#v", len(messages), messages)
	}
}

func TestCanceledTurnOutputGateBlocksLateSend(t *testing.T) {
	p := &fakePlatform{}
	a := New(p, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := withTurnRequestID(context.Background(), "turn-1")
	a.markTurnCanceled("turn-1")
	if a.turnOutputAllowed(ctx) {
		t.Fatal("canceled turn should not be allowed to send")
	}
	a.sendChat(ctx, "late output must not be sent")
	if got := p.out.String(); got != "" {
		t.Fatalf("late output reached platform: %q", got)
	}
	a.clearTurnState("turn-1")
	if !a.turnOutputAllowed(ctx) {
		t.Fatal("cleared turn should be allowed again")
	}
}
