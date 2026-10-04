package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/storage"
)

func groupThreadContext(userID, name, scopeID string) context.Context {
	return platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qqonebot",
		PlatformUserID:   userID,
		Nickname:         name,
		ScopeID:          scopeID,
		ConversationKind: platform.ConversationGroup,
	})
}

func enableSharedThread(t *testing.T, a *Agent, ctx context.Context, mergeMS int) {
	t.Helper()
	scope := a.baseScope(ctx)
	a.setGroupPolicyForScope(scope, config.GroupPolicyConfig{
		ThreadMode:    config.ThreadModeGroup,
		ResponseMode:  "all",
		MergeWindowMS: mergeMS,
	})
}

func TestSharedGroupThreadUsesOneSessionAndSpeakerMarkers(t *testing.T) {
	fake := &fakeLLM{replies: []string{"ok-1", "ok-2"}}
	a := New(&fakePlatform{}, fake, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctxA := groupThreadContext("1001", "张三", "group:9")
	ctxB := groupThreadContext("1002", "李四", "group:9")
	enableSharedThread(t, a, ctxA, 0)

	if err := a.HandleMessage(ctxA, "A 你好"); err != nil {
		t.Fatalf("HandleMessage A: %v", err)
	}
	first, err := a.sessions.Current(context.Background(), a.scope(ctxA))
	if err != nil {
		t.Fatalf("current after A: %v", err)
	}
	if !a.scope(ctxA).Shared {
		t.Fatal("group thread scope must be marked shared")
	}

	if err := a.HandleMessage(ctxB, "B 你好"); err != nil {
		t.Fatalf("HandleMessage B: %v", err)
	}
	second, err := a.sessions.Current(context.Background(), a.scope(ctxB))
	if err != nil {
		t.Fatalf("current after B: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("shared sessions differ: %s vs %s", first.ID, second.ID)
	}

	rows, err := a.store.Messages().ListBySession(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var userMessages []storage.Message
	for _, row := range rows {
		if row.Role == storage.RoleUser {
			userMessages = append(userMessages, row)
		}
	}
	if len(userMessages) != 2 {
		t.Fatalf("user messages = %d, want 2", len(userMessages))
	}
	if !strings.Contains(userMessages[0].Metadata, "张三") {
		t.Fatalf("first user metadata = %q, want speaker 张三", userMessages[0].Metadata)
	}
	if !strings.Contains(userMessages[1].Metadata, "李四") {
		t.Fatalf("second user metadata = %q, want speaker 李四", userMessages[1].Metadata)
	}
	if !strings.Contains(userMessages[0].Content, "[发言成员：张三(id:1001)]") {
		t.Fatalf("first user content = %q, want speaker marker", userMessages[0].Content)
	}
	if !strings.Contains(userMessages[1].Content, "[发言成员：李四(id:1002)]") {
		t.Fatalf("second user content = %q, want speaker marker", userMessages[1].Content)
	}

	requests := fake.chatRequests()
	if len(requests) != 2 {
		t.Fatalf("chat requests = %d, want 2", len(requests))
	}
	last := requests[1]
	text := llm.SegmentsContentText(last.Messages[len(last.Messages)-1].Segments)
	if !strings.Contains(text, "B 你好") || !strings.Contains(text, "李四") {
		t.Fatalf("last prompt = %q, want B message and speaker marker", text)
	}
}

func TestSharedGroupThreadSerializesDifferentActors(t *testing.T) {
	block := fakeLLMBlock{started: make(chan struct{}), release: make(chan struct{})}
	fake := &fakeLLM{replies: []string{"first", "second"}, chatBlocks: []fakeLLMBlock{block}}
	a := New(&fakePlatform{}, fake, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctxA := groupThreadContext("1001", "张三", "group:9")
	ctxB := groupThreadContext("1002", "李四", "group:9")
	enableSharedThread(t, a, ctxA, 0)

	doneA := make(chan error, 1)
	go func() { doneA <- a.HandleMessage(ctxA, "A 第一条") }()
	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not start")
	}

	doneB := make(chan error, 1)
	go func() { doneB <- a.HandleMessage(ctxB, "B 第一条") }()
	// Give B a moment to enqueue behind A. The assertion is on ordering, not
	// on a fixed queue length, so a slow scheduler only makes this weaker.
	time.Sleep(50 * time.Millisecond)
	if got := len(fake.chatRequests()); got != 1 {
		t.Fatalf("chat requests while A is blocked = %d, want 1", got)
	}
	close(block.release)
	if err := <-doneA; err != nil {
		t.Fatalf("HandleMessage A: %v", err)
	}
	if err := <-doneB; err != nil {
		t.Fatalf("HandleMessage B: %v", err)
	}
	requests := fake.chatRequests()
	if len(requests) != 2 {
		t.Fatalf("chat requests = %d, want 2", len(requests))
	}
	firstText := llm.SegmentsContentText(requests[0].Messages[len(requests[0].Messages)-1].Segments)
	secondText := llm.SegmentsContentText(requests[1].Messages[len(requests[1].Messages)-1].Segments)
	if !strings.Contains(firstText, "A 第一条") || strings.Contains(firstText, "B 第一条") {
		t.Fatalf("first prompt = %q, want only A", firstText)
	}
	if !strings.Contains(secondText, "B 第一条") {
		t.Fatalf("second prompt = %q, want B", secondText)
	}
}

func TestSharedGroupThreadRestrictsSessionMutationToGroupAdmins(t *testing.T) {
	platformAdapter := &fakePlatform{}
	a := New(platformAdapter, &fakeLLM{replies: []string{"ok"}}, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "cli",
		PlatformUserID:   "1001",
		Nickname:         "张三",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
	})
	enableSharedThread(t, a, ctx, 0)
	if err := a.HandleMessage(ctx, "你好"); err != nil {
		t.Fatalf("HandleMessage seed: %v", err)
	}
	platformAdapter.out.Reset()
	if err := a.HandleMessage(ctx, "/new"); err != nil {
		t.Fatalf("HandleMessage /new: %v", err)
	}
	out := platformAdapter.out.String()
	if !strings.Contains(out, "仅限群主") {
		t.Fatalf("output = %q, want shared-session admin restriction", out)
	}
}

func TestSharedGroupThreadRecallDropsQueuedMessage(t *testing.T) {
	block := fakeLLMBlock{started: make(chan struct{}), release: make(chan struct{})}
	fake := &fakeLLM{replies: []string{"first"}, chatBlocks: []fakeLLMBlock{block}}
	a := New(&fakePlatform{}, fake, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctxA := groupThreadContext("1001", "张三", "group:9")
	ctxB := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:          "qqonebot",
		PlatformUserID:    "1002",
		Nickname:          "李四",
		ScopeID:           "group:9",
		PlatformMessageID: "m-b",
		ConversationKind:  platform.ConversationGroup,
	})
	enableSharedThread(t, a, ctxA, 0)

	doneA := make(chan error, 1)
	go func() { doneA <- a.HandleMessage(ctxA, "A 第一条") }()
	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not start")
	}
	doneB := make(chan error, 1)
	go func() { doneB <- a.HandleMessage(ctxB, "B 第一条") }()
	time.Sleep(50 * time.Millisecond)
	if dropped := a.cancelInboxMessage(messageWorkKey("qqonebot", "group:9", "m-b")); dropped != 1 {
		t.Fatalf("queued recall dropped = %d, want 1", dropped)
	}
	close(block.release)
	if err := <-doneA; err != nil {
		t.Fatalf("HandleMessage A: %v", err)
	}
	if err := <-doneB; !errors.Is(err, context.Canceled) {
		t.Fatalf("HandleMessage B error = %v, want context.Canceled", err)
	}
	if got := len(fake.chatRequests()); got != 1 {
		t.Fatalf("chat requests = %d, want 1", got)
	}
}

func TestSharedGroupThreadMergesConsecutiveSameActorMessages(t *testing.T) {
	fake := &fakeLLM{replies: []string{"merged"}}
	a := New(&fakePlatform{}, fake, "test-model", config.ProviderConfig{}, newTestStore(t))
	ctx := groupThreadContext("1001", "张三", "group:9")
	enableSharedThread(t, a, ctx, 800)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	run := func(text string, delay time.Duration) {
		defer wg.Done()
		time.Sleep(delay)
		errs <- a.HandleMessage(ctx, text)
	}
	wg.Add(2)
	go run("第一部分", 0)
	go run("第二部分", 100*time.Millisecond)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	}

	requests := fake.chatRequests()
	if len(requests) != 1 {
		t.Fatalf("chat requests = %d, want 1 merged request", len(requests))
	}
	last := requests[0]
	text := llm.SegmentsContentText(last.Messages[len(last.Messages)-1].Segments)
	if !strings.Contains(text, "第一部分") || !strings.Contains(text, "第二部分") {
		t.Fatalf("merged prompt = %q, want both parts", text)
	}
}
