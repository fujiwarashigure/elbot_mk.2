package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"elbot/internal/platform"
	"elbot/internal/storage"
)

func TestChatHistoryQueriesIncludeMediaOnlyMessages(t *testing.T) {
	ctx := context.Background()
	store, err := NewChatHistory(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("NewChatHistory: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo := store.Repository()

	messages := []*storage.ChatMessage{
		{Platform: "qqonebot", PlatformScopeID: "group:1", PlatformMessageID: "text", SenderID: "user", Text: "hello"},
		{Platform: "qqonebot", PlatformScopeID: "group:1", PlatformMessageID: "media", SenderID: "user", Segments: platform.MarshalChatSegments([]platform.MessageSegment{{Type: platform.SegmentImage, Name: "sticker.jpg"}})},
		{Platform: "qqonebot", PlatformScopeID: "group:1", PlatformMessageID: "empty", SenderID: "user"},
	}
	for _, message := range messages {
		if err := repo.Append(ctx, message); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := repo.Search(ctx, storage.ChatHistorySearchRequest{Platform: "qqonebot", PlatformScopeID: "group:1", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 || got[0].PlatformMessageID != "text" || got[1].PlatformMessageID != "media" {
		t.Fatalf("Search messages = %#v", got)
	}

	got, err = repo.Around(ctx, storage.ChatHistoryAroundRequest{Platform: "qqonebot", PlatformScopeID: "group:1", PlatformMessageID: "text", After: 10})
	if err != nil {
		t.Fatalf("Around: %v", err)
	}
	if len(got) != 2 || got[0].PlatformMessageID != "text" || got[1].PlatformMessageID != "media" {
		t.Fatalf("Around messages = %#v", got)
	}
}

func TestChatHistoryRangeAndOutboundMessages(t *testing.T) {
	ctx := context.Background()
	store, err := NewChatHistory(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("NewChatHistory: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, ok := store.Repository().(storage.ChatHistoryRangeRepository)
	if !ok {
		t.Fatal("ChatHistoryRepository does not implement ChatHistoryRangeRepository")
	}
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.Local)
	for i, message := range []*storage.ChatMessage{
		{Platform: "qqonebot", PlatformScopeID: "group:1", PlatformMessageID: "1", SenderID: "u1", Text: "one", CreatedAt: base},
		{Platform: "qqonebot", PlatformScopeID: "group:1", PlatformMessageID: "2", SenderID: "u2", Text: "two", CreatedAt: base.Add(time.Hour)},
		{Platform: "qqonebot", PlatformScopeID: "group:1", PlatformMessageID: "3", SenderID: "u1", Text: "three", CreatedAt: base.Add(3 * time.Hour)},
	} {
		if err := store.Repository().Append(ctx, message); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	since := base.Add(30 * time.Minute)
	until := base.Add(2 * time.Hour)
	got, err := repo.ListRange(ctx, storage.ChatHistoryRangeRequest{Platform: "qqonebot", PlatformScopeID: "group:1", Since: &since, Until: &until, Limit: 10})
	if err != nil {
		t.Fatalf("ListRange: %v", err)
	}
	if len(got) != 1 || got[0].PlatformMessageID != "2" {
		t.Fatalf("ListRange = %#v", got)
	}

	outbound := store.Outbound()
	if err := outbound.Append(ctx, &storage.OutboundMessage{Platform: "qqonebot", PlatformScopeID: "group:1", Text: "收到", CreatedAt: base.Add(90 * time.Minute)}); err != nil {
		t.Fatalf("Outbound Append: %v", err)
	}
	out, err := outbound.ListRange(ctx, storage.OutboundMessageRangeRequest{Platform: "qqonebot", PlatformScopeID: "group:1", Since: &since, Until: &until, Limit: 10})
	if err != nil {
		t.Fatalf("Outbound ListRange: %v", err)
	}
	if len(out) != 1 || out[0].Text != "收到" {
		t.Fatalf("Outbound ListRange = %#v", out)
	}
}
