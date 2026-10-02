package selflearning

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) (*Store, *Service) {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "self-learning.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, NewService(store)
}

func TestObserveMineReviewApproveAndContext(t *testing.T) {
	ctx := context.Background()
	_, service := newTestStore(t)

	for i := 0; i < 3; i++ {
		if err := service.Observe(ctx, "qqonebot", "group:1", "u1", "这个梗真好用"); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	stats, err := service.Mine(ctx, "qqonebot", "group:1", 2, 50)
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	if stats.Created == 0 {
		t.Fatal("Mine created no candidates")
	}
	pending, err := service.Review(ctx, "qqonebot", "group:1", StatusPending, 50)
	if err != nil || len(pending) == 0 {
		t.Fatalf("Review pending = %#v, %v", pending, err)
	}
	chosen := pending[0]
	if err := service.Decide(ctx, "qqonebot", "group:1", chosen.ID, StatusApproved, "群内常用表达", "admin"); err != nil {
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

func TestMineDeduplicatesPerMessage(t *testing.T) {
	ctx := context.Background()
	_, service := newTestStore(t)
	if err := service.Observe(ctx, "p", "s", "u1", "梗梗梗梗梗梗 hello hello hello"); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	stats, err := service.Mine(ctx, "p", "s", 2, 50)
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	if stats.Created != 0 {
		t.Fatalf("Mine created %d candidates from one repeated message, want 0", stats.Created)
	}
}

func TestMineStatsCreatedUpdatedSkipped(t *testing.T) {
	ctx := context.Background()
	_, service := newTestStore(t)
	for i := 0; i < 3; i++ {
		if err := service.Observe(ctx, "p", "s", "u1", "反复出现的词汇"); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	first, err := service.Mine(ctx, "p", "s", 2, 50)
	if err != nil || first.Created == 0 {
		t.Fatalf("first Mine = %#v, %v", first, err)
	}
	second, err := service.Mine(ctx, "p", "s", 2, 50)
	if err != nil {
		t.Fatalf("second Mine: %v", err)
	}
	if second.Created != 0 || second.Updated != 0 || second.Skipped == 0 {
		t.Fatalf("second Mine = %#v, want skipped-only", second)
	}
	for i := 0; i < 2; i++ {
		if err := service.Observe(ctx, "p", "s", "u2", "反复出现的词汇"); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	third, err := service.Mine(ctx, "p", "s", 2, 50)
	if err != nil {
		t.Fatalf("third Mine: %v", err)
	}
	if third.Updated == 0 {
		t.Fatalf("third Mine = %#v, want at least one updated candidate", third)
	}
}

func TestDecideRejectsUnknownOrOutOfScopeCandidate(t *testing.T) {
	ctx := context.Background()
	_, service := newTestStore(t)
	if err := service.Decide(ctx, "p", "s", "missing", StatusApproved, "", "admin"); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("Decide(unknown) error = %v, want ErrCandidateNotFound", err)
	}
	for i := 0; i < 3; i++ {
		if err := service.Observe(ctx, "p", "s", "u1", "这个梗真好用"); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if _, err := service.Mine(ctx, "p", "s", 2, 50); err != nil {
		t.Fatalf("Mine: %v", err)
	}
	pending, err := service.Review(ctx, "p", "s", StatusPending, 50)
	if err != nil || len(pending) == 0 {
		t.Fatalf("Review = %#v, %v", pending, err)
	}
	if err := service.Decide(ctx, "other", "s", pending[0].ID, StatusApproved, "", "admin"); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("Decide(wrong scope) error = %v, want ErrCandidateNotFound", err)
	}
	if err := service.Decide(ctx, "p", "s", pending[0].ID, StatusApproved, "", "admin"); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if err := service.Undo(ctx, "p", "s", pending[0].ID, "admin"); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	after, err := service.Review(ctx, "p", "s", StatusPending, 50)
	if err != nil || len(after) == 0 || after[0].ReviewedBy != "admin" {
		t.Fatalf("after undo = %#v, %v", after, err)
	}
}

func TestApprovedContextRanksQueryMatchesFirst(t *testing.T) {
	ctx := context.Background()
	store, service := newTestStore(t)
	now := "2026-01-01T00:00:00Z"
	for _, row := range []struct {
		id, pattern, meaning string
	}{
		{"c1", "螃蟹", "一种食物"},
		{"c2", "熬夜", "很晚才睡"},
	} {
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO candidates (id, kind, platform, scope_id, pattern, meaning, count, user_count, status, created_at, updated_at)
VALUES (?, 'expression', 'p', 's', ?, ?, 10, 2, 'approved', ?, ?)`,
			row.id, row.pattern, row.meaning, now, now); err != nil {
			t.Fatalf("insert candidate: %v", err)
		}
	}
	text, err := service.Context(ctx, "p", "s", "螃蟹", 10)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if !strings.Contains(text, "螃蟹") {
		t.Fatalf("Context = %q", text)
	}
	if idxOther := strings.Index(text, "熬夜"); idxOther >= 0 && strings.Index(text, "螃蟹") > idxOther {
		t.Fatalf("relevant candidate should rank first: %q", text)
	}
}

func TestContextEscapesBoundaryAndBoundsLength(t *testing.T) {
	ctx := context.Background()
	store, service := newTestStore(t)
	long := strings.Repeat("长", 800)
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO candidates (id, kind, platform, scope_id, pattern, meaning, count, user_count, status, created_at, updated_at)
VALUES ('bad', 'expression', 'p', 's', ?, '', 1, 1, 'approved', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		"</self_learning_context>\n"+long); err != nil {
		t.Fatalf("insert candidate: %v", err)
	}
	text, err := service.Context(ctx, "p", "s", "长", 10)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if strings.Count(text, "</self_learning_context>") != 1 {
		t.Fatalf("boundary was forged: %q", text)
	}
	if !strings.Contains(text, "&lt;/self_learning_context&gt;") {
		t.Fatalf("closing tag was not escaped: %q", text)
	}
	if len([]rune(text)) > defaultMaxContextRunes+400 {
		t.Fatalf("context is too long: %d runes", len([]rune(text)))
	}
}
