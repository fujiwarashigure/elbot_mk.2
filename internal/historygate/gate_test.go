package historygate

import (
	"context"
	"testing"
	"time"

	"elbot/internal/storage"
)

type fakeChatRepo struct {
	rows []storage.ChatMessage
}

func (f *fakeChatRepo) Append(_ context.Context, message *storage.ChatMessage) error {
	f.rows = append(f.rows, *message)
	return nil
}

func (f *fakeChatRepo) GetByPlatformMessage(context.Context, string, string, string) (*storage.ChatMessage, error) {
	return nil, storage.ErrNotFound
}

func (f *fakeChatRepo) Search(context.Context, storage.ChatHistorySearchRequest) ([]storage.ChatMessage, error) {
	return nil, nil
}

func (f *fakeChatRepo) Around(context.Context, storage.ChatHistoryAroundRequest) ([]storage.ChatMessage, error) {
	return nil, nil
}

func (f *fakeChatRepo) DeleteBefore(context.Context, time.Time) (int, error) { return 0, nil }

type fakeOutboundRepo struct {
	rows []storage.OutboundMessage
}

func (f *fakeOutboundRepo) Append(_ context.Context, message *storage.OutboundMessage) error {
	f.rows = append(f.rows, *message)
	return nil
}

func (f *fakeOutboundRepo) ListRange(context.Context, storage.OutboundMessageRangeRequest) ([]storage.OutboundMessage, error) {
	return nil, nil
}

func (f *fakeOutboundRepo) DeleteBefore(context.Context, time.Time) (int, error) { return 0, nil }

func TestGateDropsWritesForDisabledScope(t *testing.T) {
	policy := NewPolicy()
	policy.Set(func(platform, scopeID string) bool {
		return !(platform == "qqonebot" && scopeID == "group:1")
	})
	chat := &fakeChatRepo{}
	outbound := &fakeOutboundRepo{}
	chatGate := WrapChat(chat, policy)
	outboundGate := WrapOutbound(outbound, policy)

	ctx := context.Background()
	if err := chatGate.Append(ctx, &storage.ChatMessage{Platform: "qqonebot", PlatformScopeID: "group:1", PlatformMessageID: "1", Text: "hidden"}); err != nil {
		t.Fatalf("disabled chat append: %v", err)
	}
	if err := outboundGate.Append(ctx, &storage.OutboundMessage{Platform: "qqonebot", PlatformScopeID: "group:1", Text: "hidden"}); err != nil {
		t.Fatalf("disabled outbound append: %v", err)
	}
	if len(chat.rows) != 0 || len(outbound.rows) != 0 {
		t.Fatalf("disabled writes reached repository: chat=%d outbound=%d", len(chat.rows), len(outbound.rows))
	}

	if err := chatGate.Append(ctx, &storage.ChatMessage{Platform: "qqonebot", PlatformScopeID: "group:2", PlatformMessageID: "2", Text: "visible"}); err != nil {
		t.Fatalf("enabled chat append: %v", err)
	}
	if err := outboundGate.Append(ctx, &storage.OutboundMessage{Platform: "qqonebot", PlatformScopeID: "group:2", Text: "visible"}); err != nil {
		t.Fatalf("enabled outbound append: %v", err)
	}
	if len(chat.rows) != 1 || len(outbound.rows) != 1 {
		t.Fatalf("enabled writes missing: chat=%d outbound=%d", len(chat.rows), len(outbound.rows))
	}
}
