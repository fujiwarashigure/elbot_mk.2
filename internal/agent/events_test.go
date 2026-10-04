package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"elbot/internal/angelmemory"
	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/request"
	"elbot/internal/session"
	"elbot/internal/storage"
)

func TestRecallCancelsOnlyBoundMessageWork(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	base := platform.MessageContext{
		Platform: "qqonebot", ScopeID: "group:9", ConversationKind: platform.ConversationGroup,
	}
	ctxA := platform.WithMessageContext(context.Background(), func() platform.MessageContext {
		msg := base
		msg.PlatformUserID = "2001"
		msg.PlatformMessageID = "m-a"
		return msg
	}())
	ctxB := platform.WithMessageContext(context.Background(), func() platform.MessageContext {
		msg := base
		msg.PlatformUserID = "2002"
		msg.PlatformMessageID = "m-b"
		return msg
	}())

	reqA, reqCtxA, doneA, err := a.requests.Start(ctxA, request.StartRequest{SessionID: "s-a", Kind: request.KindTurn, FairKey: a.requestFairKey(ctxA)})
	if err != nil {
		t.Fatalf("start request A: %v", err)
	}
	defer doneA()
	reqB, reqCtxB, doneB, err := a.requests.Start(ctxB, request.StartRequest{SessionID: "s-b", Kind: request.KindTurn, FairKey: a.requestFairKey(ctxB)})
	if err != nil {
		t.Fatalf("start request B: %v", err)
	}
	defer doneB()
	a.registerMessageWork(ctxA, "s-a", reqA.ID)
	a.registerMessageWork(ctxB, "s-b", reqB.ID)

	if err := a.HandlePlatformEvent(ctxA, platform.PlatformEvent{
		Platform: "qqonebot", Kind: platform.EventNotice, Type: "group_recall", ScopeID: "group:9", MessageID: "m-a",
	}); err != nil {
		t.Fatalf("handle recall: %v", err)
	}
	select {
	case <-reqCtxA.Done():
	case <-time.After(time.Second):
		t.Fatal("recalled message request was not canceled")
	}
	select {
	case <-reqCtxB.Done():
		t.Fatal("another user's request was canceled by an unrelated recall")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRecallArrivingBeforeRegistrationCancelsLateWork(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform: "qqonebot", PlatformUserID: "2001", ScopeID: "group:9",
		ConversationKind: platform.ConversationGroup, PlatformMessageID: "m-late",
	})
	req, reqCtx, done, err := a.requests.Start(ctx, request.StartRequest{SessionID: "s-late", Kind: request.KindTurn, FairKey: a.requestFairKey(ctx)})
	if err != nil {
		t.Fatalf("start request: %v", err)
	}
	defer done()
	if err := a.HandlePlatformEvent(ctx, platform.PlatformEvent{
		Platform: "qqonebot", Kind: platform.EventNotice, Type: "group_recall", ScopeID: "group:9", MessageID: "m-late",
	}); err != nil {
		t.Fatalf("handle recall: %v", err)
	}
	if !a.registerMessageWork(ctx, "s-late", req.ID) {
		t.Fatal("late registration did not consume the recall tombstone")
	}
	select {
	case <-reqCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("late-bound request was not canceled")
	}
}

func TestRecallForgetsMemoriesLinkedToMessage(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			store, err := angelmemory.Open(context.Background(), filepath.Join(t.TempDir(), "angel-memory.db"))
			if err != nil {
				t.Fatalf("open angel memory: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			service := angelmemory.NewService(store)
			configValue := enabled
			a := mustNewWithOptions(t, Options{
				Platform:        &fakePlatform{},
				Clients:         map[string]llm.LLM{"default": &fakeLLM{}},
				ModeModels:      map[string]config.ModelSelection{storage.SessionModeWork: {Provider: "default", Model: "test"}},
				Providers:       map[string]config.ProviderConfig{"default": {}},
				Store:           newTestStore(t),
				CommandPrefixes: []string{"/"},
				SessionConfig:   session.Config{DefaultMode: storage.SessionModeWork},
				AngelMemory:     service,
				AngelMemoryConfig: config.AngelMemoryConfig{
					ForgetOnRecall: &configValue,
				},
			})
			ctx := context.Background()
			memory, err := service.RememberWithSource(ctx, "qqonebot", "group:9", "被撤回的消息", "", angelmemory.Source{
				Kind:      "tool",
				ActorID:   "qqonebot:2001",
				MessageID: "m-recall",
			})
			if err != nil {
				t.Fatalf("remember: %v", err)
			}
			if err := a.HandlePlatformEvent(ctx, platform.PlatformEvent{
				Platform:  "qqonebot",
				Kind:      platform.EventNotice,
				Type:      "group_recall",
				ScopeID:   "group:9",
				MessageID: "m-recall",
			}); err != nil {
				t.Fatalf("handle recall: %v", err)
			}
			_, err = service.Get(ctx, "qqonebot", "group:9", memory.ID)
			if enabled {
				if !errors.Is(err, angelmemory.ErrNotFound) {
					t.Fatalf("memory after recall error = %v, want ErrNotFound", err)
				}
			} else if err != nil {
				t.Fatalf("memory after disabled recall error = %v, want nil", err)
			}
		})
	}
}
