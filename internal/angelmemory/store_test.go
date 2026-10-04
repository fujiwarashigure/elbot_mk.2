package angelmemory

import (
	"context"
	"database/sql"
	"errors"
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

func TestRememberStoresStructuredSourceAndScopedDelete(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	memory, err := store.Remember(ctx, &Memory{
		Platform:        "qqonebot",
		ScopeID:         "group:1",
		Content:         "用户喜欢猫",
		SourceKind:      "tool",
		SourceActorID:   "qqonebot:2001",
		SourceMessageID: "m-1",
		SourceSessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if memory.ID == "" {
		t.Fatal("memory ID is empty")
	}

	got, err := store.Get(ctx, "qqonebot", "group:1", memory.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SourceKind != "tool" || got.SourceActorID != "qqonebot:2001" || got.SourceMessageID != "m-1" || got.SourceSessionID != "sess-1" {
		t.Fatalf("source = %#v", got)
	}

	actorMatches, err := store.List(ctx, "qqonebot", "group:1", SourceFilter{ActorID: "qqonebot:2001"}, 10)
	if err != nil || len(actorMatches) != 1 {
		t.Fatalf("list actor = %#v, %v", actorMatches, err)
	}
	otherMatches, err := store.List(ctx, "qqonebot", "group:1", SourceFilter{ActorID: "qqonebot:2002"}, 10)
	if err != nil || len(otherMatches) != 0 {
		t.Fatalf("list other actor = %#v, %v", otherMatches, err)
	}

	prefixID, err := store.ResolveID(ctx, "qqonebot", "group:1", memory.ID[:8], SourceFilter{})
	if err != nil || prefixID != memory.ID {
		t.Fatalf("ResolveID = %q, %v", prefixID, err)
	}
	if _, err := store.ResolveID(ctx, "qqonebot", "group:2", memory.ID[:8], SourceFilter{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveID wrong scope error = %v, want ErrNotFound", err)
	}

	deleted, err := store.DeleteScoped(ctx, "qqonebot", "group:2", memory.ID)
	if err != nil || deleted {
		t.Fatalf("DeleteScoped wrong scope = %v, %v; want false, nil", deleted, err)
	}
	deleted, err = store.DeleteScoped(ctx, "qqonebot", "group:1", memory.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteScoped = %v, %v; want true, nil", deleted, err)
	}
	if _, err := store.Get(ctx, "qqonebot", "group:1", memory.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete error = %v, want ErrNotFound", err)
	}
}

func TestDeleteBySourceOnlyMatchesStructuredLink(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Remember(ctx, &Memory{
		Platform:        "p",
		ScopeID:         "s",
		Content:         "linked",
		SourceActorID:   "actor-1",
		SourceMessageID: "m-1",
	}); err != nil {
		t.Fatalf("Remember linked: %v", err)
	}
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "unlinked"}); err != nil {
		t.Fatalf("Remember unlinked: %v", err)
	}
	if _, err := store.DeleteBySource(ctx, "p", "s", SourceFilter{}); err == nil {
		t.Fatal("DeleteBySource with empty filter succeeded")
	}
	count, err := store.DeleteBySource(ctx, "p", "s", SourceFilter{MessageID: "m-1"})
	if err != nil || count != 1 {
		t.Fatalf("DeleteBySource = %d, %v; want 1, nil", count, err)
	}
	remaining, err := store.List(ctx, "p", "s", SourceFilter{}, 10)
	if err != nil || len(remaining) != 1 || remaining[0].Content != "unlinked" {
		t.Fatalf("remaining = %#v, %v", remaining, err)
	}
}

func TestLegacySourceKindBackfillStaysDeterministic(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "angel-memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Legacy row: only the free-text label, as written before structured
	// provenance existed.
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "旧记忆", Source: "tool"}); err != nil {
		t.Fatalf("Remember legacy: %v", err)
	}
	if _, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "新记忆", SourceKind: "tool", SourceActorID: "actor-1", SourceMessageID: "m-1"}); err != nil {
		t.Fatalf("Remember linked: %v", err)
	}

	stats, err := store.LegacySourceBackfillStats(ctx)
	if err != nil {
		t.Fatalf("LegacySourceBackfillStats: %v", err)
	}
	if stats.Total != 2 || stats.Linked != 1 || stats.Backfillable != 1 {
		t.Fatalf("stats = %#v, want total 2, linked 1, backfillable 1", stats)
	}

	updated, err := store.BackfillLegacySourceKind(ctx)
	if err != nil || updated != 1 {
		t.Fatalf("BackfillLegacySourceKind = %d, %v; want 1, nil", updated, err)
	}
	updated, err = store.BackfillLegacySourceKind(ctx)
	if err != nil || updated != 0 {
		t.Fatalf("second backfill = %d, %v; want 0, nil (idempotent)", updated, err)
	}

	memories, err := store.List(ctx, "p", "s", SourceFilter{}, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, memory := range memories {
		if memory.Content != "旧记忆" {
			continue
		}
		if memory.SourceKind != "tool" {
			t.Fatalf("legacy source_kind = %q, want tool", memory.SourceKind)
		}
		// The backfill must not invent provenance it never had.
		if memory.SourceActorID != "" || memory.SourceMessageID != "" || memory.SourceSessionID != "" {
			t.Fatalf("legacy provenance was guessed: %#v", memory)
		}
	}
	// Legacy rows stay invisible to source-based deletion, which is the whole
	// point of "never delete the wrong memory".
	count, err := store.DeleteBySource(ctx, "p", "s", SourceFilter{MessageID: "m-1"})
	if err != nil || count != 1 {
		t.Fatalf("DeleteBySource = %d, %v; want 1, nil", count, err)
	}
}

func TestOpenMigratesLegacyMemorySchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "angel-memory.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    platform TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    content TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    strength INTEGER NOT NULL DEFAULT 50,
    source TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_accessed_at TEXT NOT NULL
)`); err != nil {
		_ = legacy.Close()
		t.Fatalf("create legacy table: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open migrated: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	memory, err := store.Remember(ctx, &Memory{Platform: "p", ScopeID: "s", Content: "x", SourceMessageID: "m-2"})
	if err != nil {
		t.Fatalf("Remember after migration: %v", err)
	}
	if memory.SourceMessageID != "m-2" {
		t.Fatalf("SourceMessageID = %q, want m-2", memory.SourceMessageID)
	}
}
