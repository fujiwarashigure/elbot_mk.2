package angelmemory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elbot/internal/storage"
	"elbot/internal/textmatch"

	_ "modernc.org/sqlite"
)

const defaultRecallLimit = 5

var (
	// ErrContentTooLong is returned when one memory exceeds the configured
	// per-entry rune budget.
	ErrContentTooLong = errors.New("angel memory content is too long")
	// ErrScopeFull is returned when a scope is at its configured memory count
	// limit and the new content would create another entry.
	ErrScopeFull = errors.New("angel memory scope limit reached")
)

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
	Platform      string
	ScopeID       string
	Text          string
	Limit         int
	AllowFallback bool
}

type Store struct {
	db     *sql.DB
	limits StoreLimits
}

// SetLimits applies write-side limits. It is intended to be called once while
// the store is being constructed, before concurrent use.
func (s *Store) SetLimits(limits StoreLimits) {
	if s == nil {
		return
	}
	if limits.MaxContentRunes <= 0 {
		limits.MaxContentRunes = defaultMaxContentRunes
	}
	if limits.MaxPerScope <= 0 {
		limits.MaxPerScope = defaultMaxPerScope
	}
	s.limits = limits
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
	return &Store{
		db:     db,
		limits: StoreLimits{MaxContentRunes: defaultMaxContentRunes, MaxPerScope: defaultMaxPerScope},
	}, nil
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
	if max := s.limits.MaxContentRunes; max > 0 && len([]rune(memory.Content)) > max {
		return nil, fmt.Errorf("%w: %d runes (max %d)", ErrContentTooLong, len([]rune(memory.Content)), max)
	}
	// The existence check, scope count and insert must be one atomic unit:
	// SetMaxOpenConns(1) only serialises individual statements, so without a
	// transaction two concurrent writers could both observe "one slot left".
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin angel memory write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if max := s.limits.MaxPerScope; max > 0 {
		var existingID string
		checkErr := tx.QueryRowContext(ctx, `
SELECT id FROM memories WHERE platform = ? AND scope_id = ? AND content = ?`,
			memory.Platform, memory.ScopeID, memory.Content).Scan(&existingID)
		switch {
		case checkErr == nil:
			// Updating an existing entry is always allowed.
		case errors.Is(checkErr, sql.ErrNoRows):
			var count int
			if countErr := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memories WHERE platform = ? AND scope_id = ?`,
				memory.Platform, memory.ScopeID).Scan(&count); countErr != nil {
				return nil, fmt.Errorf("count angel memory scope: %w", countErr)
			}
			if count >= max {
				return nil, fmt.Errorf("%w: %d entries (max %d)", ErrScopeFull, count, max)
			}
		default:
			return nil, fmt.Errorf("check angel memory entry: %w", checkErr)
		}
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

	if _, err := tx.ExecContext(ctx, `
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
	); err != nil {
		return nil, fmt.Errorf("remember angel memory: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit angel memory write: %w", err)
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
	terms := textmatch.Keywords(text)
	const selectColumns = `SELECT id, platform, scope_id, content, tags, strength, source, created_at, updated_at, last_accessed_at FROM memories`

	var memories []Memory
	if len(terms) == 0 {
		rows, err := s.db.QueryContext(ctx, selectColumns+`
WHERE platform = ? AND scope_id = ?
ORDER BY strength DESC, updated_at DESC
LIMIT ?`, platform, scopeID, limit)
		if err != nil {
			return nil, fmt.Errorf("recall angel memory: %w", err)
		}
		memories, err = scanMemories(rows)
		if err != nil {
			return nil, err
		}
	} else {
		whereParts := make([]string, 0, len(terms))
		scoreParts := make([]string, 0, len(terms))
		whereParams := make([]any, 0, len(terms)*2)
		scoreParams := make([]any, 0, len(terms)*2)
		for _, term := range terms {
			pattern := textmatch.LikePattern(term)
			whereParts = append(whereParts, `(content LIKE ? ESCAPE '\' OR tags LIKE ? ESCAPE '\')`)
			whereParams = append(whereParams, pattern, pattern)
			scoreParts = append(scoreParts, `(CASE WHEN content LIKE ? ESCAPE '\' THEN 2 ELSE 0 END + CASE WHEN tags LIKE ? ESCAPE '\' THEN 1 ELSE 0 END)`)
			scoreParams = append(scoreParams, pattern, pattern)
		}
		params := make([]any, 0, 2+len(whereParams)+len(scoreParams)+1)
		params = append(params, platform, scopeID)
		params = append(params, whereParams...)
		params = append(params, scoreParams...)
		params = append(params, limit)
		statement := selectColumns + `
WHERE platform = ? AND scope_id = ? AND (` + strings.Join(whereParts, " OR ") + `)
ORDER BY (` + strings.Join(scoreParts, " + ") + `) DESC, strength DESC, updated_at DESC
LIMIT ?`
		rows, err := s.db.QueryContext(ctx, statement, params...)
		if err != nil {
			return nil, fmt.Errorf("recall angel memory: %w", err)
		}
		memories, err = scanMemories(rows)
		if err != nil {
			return nil, err
		}
	}

	if len(memories) == 0 && query.AllowFallback && text != "" && len(terms) > 0 {
		fallbackLimit := limit
		if fallbackLimit > 3 {
			fallbackLimit = 3
		}
		rows, err := s.db.QueryContext(ctx, selectColumns+`
WHERE platform = ? AND scope_id = ? AND strength >= 60
ORDER BY strength DESC, updated_at DESC
LIMIT ?`, platform, scopeID, fallbackLimit)
		if err != nil {
			return nil, fmt.Errorf("recall angel memory fallback: %w", err)
		}
		memories, err = scanMemories(rows)
		if err != nil {
			return nil, err
		}
	}

	if len(memories) > 0 {
		if err := s.touch(ctx, memories); err != nil {
			return memories, nil
		}
	}
	return memories, nil
}

func scanMemories(rows *sql.Rows) ([]Memory, error) {
	defer rows.Close()
	memories := []Memory{}
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
