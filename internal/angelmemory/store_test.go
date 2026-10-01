package angelmemory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRememberRecallAndCount(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.Remember(ctx, &Memory{Platform: "qqonebot", ScopeID: "group:1", Content: "用户喜欢猫", Tags: "偏好 宠物"}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if _, err := store.Remember(ctx, &Memory{Platform: "qqonebot", ScopeID: "group:1", Content: "用户在杭州", Tags: "地点"}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	got, err := store.Recall(ctx, RecallQuery{Platform: "qqonebot", ScopeID: "group:1", Text: "猫", Limit: 5})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Content, "猫") {
		t.Fatalf("Recall = %#v", got)
	}
	count, err := store.Count(ctx, "qqonebot", "group:1")
	if err != nil || count != 2 {
		t.Fatalf("Count = %d, %v", count, err)
	}
}

func TestServiceContextWrapsTrustBoundary(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := NewService(store)
	if _, err := service.Remember(ctx, "qqonebot", "group:1", "用户喜欢猫", "", "test"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	text, err := service.Context(ctx, "qqonebot", "group:1", "猫", 5)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if !strings.Contains(text, "<angel_memory>") || !strings.Contains(text, "用户喜欢猫") {
		t.Fatalf("Context = %q", text)
	}
}

func TestDeleteBefore(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "old", UpdatedAt: time.Now().AddDate(0, 0, -30)}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	deleted, err := store.DeleteBefore(ctx, time.Now().AddDate(0, 0, -1))
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteBefore = %d, %v", deleted, err)
	}
}
