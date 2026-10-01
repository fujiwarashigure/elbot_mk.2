package angelmemory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elbot/internal/storage"

	_ "modernc.org/sqlite"
)

const defaultRecallLimit = 5

// Memory is one reviewable, scope-local memory record.
type Memory struct {
	ID             string
	Platform       string
	ScopeID        string
	Content        string
	Tags           string
	Strength       int
	Source         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastAccessedAt time.Time
}

type RecallQuery struct {
	Platform string
	ScopeID  string
	Text     string
	Limit    int
}

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("angel memory sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create angel memory directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open angel memory sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable angel memory foreign keys: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping angel memory sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS memories (
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
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_angel_memories_scope_content
ON memories(platform, scope_id, content);
CREATE INDEX IF NOT EXISTS idx_angel_memories_scope_strength
ON memories(platform, scope_id, strength);
`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate angel memory sqlite: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Remember(ctx context.Context, memory *Memory) (*Memory, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("angel memory store is not open")
	}
	if memory == nil {
		return nil, fmt.Errorf("angel memory is required")
	}
	memory.Platform = strings.TrimSpace(memory.Platform)
	memory.ScopeID = strings.TrimSpace(memory.ScopeID)
	memory.Content = strings.TrimSpace(memory.Content)
	memory.Tags = normalizeTags(memory.Tags)
	if memory.Platform == "" || memory.ScopeID == "" || memory.Content == "" {
		return nil, fmt.Errorf("angel memory platform, scope and content are required")
	}
	if memory.Strength <= 0 {
		memory.Strength = 50
	}
	if memory.Strength > 100 {
		memory.Strength = 100
	}
	if memory.ID == "" {
		memory.ID = storage.NewID()
	}
	now := storage.Now()
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = now
	}
	if memory.UpdatedAt.IsZero() {
		memory.UpdatedAt = now
	}
	if memory.LastAccessedAt.IsZero() {
		memory.LastAccessedAt = now
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO memories (id, platform, scope_id, content, tags, strength, source, created_at, updated_at, last_accessed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, scope_id, content) DO UPDATE SET
    tags = excluded.tags,
    strength = MAX(memories.strength, excluded.strength),
    source = excluded.source,
    updated_at = excluded.updated_at,
    last_accessed_at = excluded.last_accessed_at`,
		memory.ID,
		memory.Platform,
		memory.ScopeID,
		memory.Content,
		memory.Tags,
		memory.Strength,
		strings.TrimSpace(memory.Source),
		storage.FormatTime(memory.CreatedAt),
		storage.FormatTime(memory.UpdatedAt),
		storage.FormatTime(memory.LastAccessedAt),
	)
	if err != nil {
		return nil, fmt.Errorf("remember angel memory: %w", err)
	}
	rows, err := s.Recall(ctx, RecallQuery{Platform: memory.Platform, ScopeID: memory.ScopeID, Text: memory.Content, Limit: 1})
	if err != nil {
		return memory, nil
	}
	for i := range rows {
		if rows[i].Content == memory.Content {
			return &rows[i], nil
		}
	}
	return memory, nil
}

func (s *Store) Recall(ctx context.Context, query RecallQuery) ([]Memory, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("angel memory store is not open")
	}
	platform := strings.TrimSpace(query.Platform)
	scopeID := strings.TrimSpace(query.ScopeID)
	if platform == "" || scopeID == "" {
		return nil, fmt.Errorf("angel memory recall platform and scope are required")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = defaultRecallLimit
	}
	if limit > 50 {
		limit = 50
	}
	text := strings.TrimSpace(query.Text)
	conditions := []string{"platform = ?", "scope_id = ?"}
	params := []any{platform, scopeID}
	if text != "" {
		conditions = append(conditions, "(content LIKE ? OR tags LIKE ?)")
		pattern := "%" + text + "%"
		params = append(params, pattern, pattern)
	}
	params = append(params, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, platform, scope_id, content, tags, strength, source, created_at, updated_at, last_accessed_at
FROM memories
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY strength DESC, updated_at DESC
LIMIT ?`, params...)
	if err != nil {
		return nil, fmt.Errorf("recall angel memory: %w", err)
	}
	defer rows.Close()
	memories := make([]Memory, 0, limit)
	for rows.Next() {
		var memory Memory
		var createdAt, updatedAt, lastAccessedAt string
		if err := rows.Scan(&memory.ID, &memory.Platform, &memory.ScopeID, &memory.Content, &memory.Tags, &memory.Strength, &memory.Source, &createdAt, &updatedAt, &lastAccessedAt); err != nil {
			return nil, fmt.Errorf("scan angel memory: %w", err)
		}
		memory.CreatedAt, _ = storage.ParseTime(createdAt)
		memory.UpdatedAt, _ = storage.ParseTime(updatedAt)
		memory.LastAccessedAt, _ = storage.ParseTime(lastAccessedAt)
		memories = append(memories, memory)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate angel memory: %w", err)
	}
	if len(memories) > 0 {
		if err := s.touch(ctx, memories); err != nil {
			return memories, nil
		}
	}
	return memories, nil
}

func (s *Store) touch(ctx context.Context, memories []Memory) error {
	now := storage.FormatTime(storage.Now())
	for _, memory := range memories {
		if _, err := s.db.ExecContext(ctx, `UPDATE memories SET last_accessed_at = ? WHERE id = ?`, now, memory.ID); err != nil {
			return fmt.Errorf("touch angel memory: %w", err)
		}
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("angel memory store is not open")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("delete angel memory: %w", err)
	}
	return nil
}

func (s *Store) DeleteBefore(ctx context.Context, cutoff time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE updated_at < ?`, storage.FormatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("delete old angel memory: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("angel memory rows affected: %w", err)
	}
	return int(rows), nil
}

func (s *Store) Count(ctx context.Context, platform, scopeID string) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories WHERE platform = ? AND scope_id = ?`, strings.TrimSpace(platform), strings.TrimSpace(scopeID)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count angel memory: %w", err)
	}
	return count, nil
}

func normalizeTags(tags string) string {
	fields := strings.FieldsFunc(tags, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\n' || r == '\t'
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return strings.Join(out, ",")
}
