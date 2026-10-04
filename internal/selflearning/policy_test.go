package selflearning

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestServicePolicyGatesFullLearningLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "learning.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	service := NewService(store)
	service.SetScopeEnabledPolicy(func(platform, scopeID string) bool {
		return !(platform == "qqonebot" && scopeID == "group:1")
	})

	if err := service.Observe(ctx, "qqonebot", "group:1", "u1", "这个梗真好用"); err != nil {
		t.Fatalf("disabled observe: %v", err)
	}
	pending, approved, err := service.Stats(ctx, "qqonebot", "group:1")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if pending != 0 || approved != 0 {
		t.Fatalf("disabled observation was persisted: pending=%d approved=%d", pending, approved)
	}
	if _, err := service.Mine(ctx, "qqonebot", "group:1", 1, 10); !errors.Is(err, ErrLearningDisabled) {
		t.Fatalf("disabled mine error = %v, want ErrLearningDisabled", err)
	}
	if text, err := service.Context(ctx, "qqonebot", "group:1", "", 8); err != nil || text != "" {
		t.Fatalf("disabled context = %q err=%v, want empty/nil", text, err)
	}
}
