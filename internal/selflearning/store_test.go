package selflearning

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestObserveMineReviewAndApprovedContext(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "self-learning.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := NewService(store)

	for i := 0; i < 3; i++ {
		if err := service.Observe(ctx, "qqonebot", "group:1", "u1", "这个梗真好用 这个梗真好用"); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	created, err := service.Mine(ctx, "qqonebot", "group:1", 2, 50)
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	if created == 0 {
		t.Fatal("Mine created no candidates")
	}
	pending, err := service.Review(ctx, "qqonebot", "group:1", StatusPending, 50)
	if err != nil || len(pending) == 0 {
		t.Fatalf("Review pending = %#v, %v", pending, err)
	}
	chosen := pending[0]
	if err := service.Decide(ctx, chosen.ID, StatusApproved, "群内常用表达"); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	text, err := service.Context(ctx, "qqonebot", "group:1", chosen.Pattern, 10)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if !strings.Contains(text, "<self_learning_context>") || !strings.Contains(text, chosen.Pattern) {
		t.Fatalf("Context = %q", text)
	}
}
