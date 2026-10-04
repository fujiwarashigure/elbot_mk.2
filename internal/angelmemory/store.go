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
	// ErrNotFound is returned when a scoped memory row does not exist.
	ErrNotFound = errors.New("angel memory not found")
	// ErrAmbiguousID is returned when a memory id prefix matches more than one row.
	ErrAmbiguousID = errors.New("angel memory id prefix is ambiguous")
)

// Memory is one reviewable, scope-local memory record.
type Memory struct {
	ID              string
	Platform        string
	ScopeID         string
	Content         string
	Tags            string
	Strength        int
	Source          string
	SourceKind      string
	SourceActorID   string
	SourceMessageID string
	SourceSessionID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastAccessedAt  time.Time
}

// SourceFilter selects memory rows by structured provenance. All non-empty
// fields are ANDed; an empty filter matches the whole scope.
type SourceFilter struct {
	ActorID   string
	MessageID string
	SessionID string
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
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate angel memory sqlite: %w", err)
	}
	return &Store{
		db:     db,
		limits: StoreLimits{MaxContentRunes: defaultMaxContentRunes, MaxPerScope: defaultMaxPerScope},
	}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    platform TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    content TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    strength INTEGER NOT NULL DEFAULT 50,
    source TEXT NOT NULL DEFAULT '',
    source_kind TEXT NOT NULL DEFAULT '',
    source_actor_id TEXT NOT NULL DEFAULT '',
    source_message_id TEXT NOT NULL DEFAULT '',
    source_session_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_accessed_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	for _, column := range []struct{ name, ddl string }{
		{"source_kind", "source_kind TEXT NOT NULL DEFAULT ''"},
		{"source_actor_id", "source_actor_id TEXT NOT NULL DEFAULT ''"},
		{"source_message_id", "source_message_id TEXT NOT NULL DEFAULT ''"},
		{"source_session_id", "source_session_id TEXT NOT NULL DEFAULT ''"},
	} {
		exists, err := columnExists(ctx, db, "memories", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.ExecContext(ctx, `ALTER TABLE memories ADD COLUMN `+column.ddl); err != nil {
			return fmt.Errorf("add angel memory column %s: %w", column.name, err)
		}
	}
	for _, statement := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_angel_memories_scope_content ON memories(platform, scope_id, content)`,
		`CREATE INDEX IF NOT EXISTS idx_angel_memories_scope_strength ON memories(platform, scope_id, strength)`,
		`CREATE INDEX IF NOT EXISTS idx_angel_memories_source_message ON memories(platform, scope_id, source_message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_angel_memories_source_actor ON memories(platform, scope_id, source_actor_id)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("inspect angel memory columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultV   sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultV, &primaryKey); err != nil {
			return false, fmt.Errorf("scan angel memory columns: %w", err)
		}
		if strings.EqualFold(strings.TrimSpace(name), column) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate angel memory columns: %w", err)
	}
	return false, nil
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
	memory.Source = truncateSourceText(memory.Source, 200)
	memory.SourceKind = truncateSourceText(memory.SourceKind, 40)
	memory.SourceActorID = truncateSourceText(memory.SourceActorID, 200)
	memory.SourceMessageID = truncateSourceText(memory.SourceMessageID, 200)
	memory.SourceSessionID = truncateSourceText(memory.SourceSessionID, 200)
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
INSERT INTO memories (id, platform, scope_id, content, tags, strength, source, source_kind, source_actor_id, source_message_id, source_session_id, created_at, updated_at, last_accessed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, scope_id, content) DO UPDATE SET
    tags = excluded.tags,
    strength = MAX(memories.strength, excluded.strength),
    source = excluded.source,
    source_kind = excluded.source_kind,
    source_actor_id = excluded.source_actor_id,
    source_message_id = excluded.source_message_id,
    source_session_id = excluded.source_session_id,
    updated_at = excluded.updated_at,
    last_accessed_at = excluded.last_accessed_at`,
		memory.ID,
		memory.Platform,
		memory.ScopeID,
		memory.Content,
		memory.Tags,
		memory.Strength,
		memory.Source,
		memory.SourceKind,
		memory.SourceActorID,
		memory.SourceMessageID,
		memory.SourceSessionID,
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
	const selectColumns = `SELECT id, platform, scope_id, content, tags, strength, source, source_kind, source_actor_id, source_message_id, source_session_id, created_at, updated_at, last_accessed_at FROM memories`

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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMemory(scanner rowScanner) (Memory, error) {
	var memory Memory
	var createdAt, updatedAt, lastAccessedAt string
	if err := scanner.Scan(
		&memory.ID,
		&memory.Platform,
		&memory.ScopeID,
		&memory.Content,
		&memory.Tags,
		&memory.Strength,
		&memory.Source,
		&memory.SourceKind,
		&memory.SourceActorID,
		&memory.SourceMessageID,
		&memory.SourceSessionID,
		&createdAt,
		&updatedAt,
		&lastAccessedAt,
	); err != nil {
		return Memory{}, err
	}
	memory.CreatedAt, _ = storage.ParseTime(createdAt)
	memory.UpdatedAt, _ = storage.ParseTime(updatedAt)
	memory.LastAccessedAt, _ = storage.ParseTime(lastAccessedAt)
	return memory, nil
}

func scanMemories(rows *sql.Rows) ([]Memory, error) {
	defer rows.Close()
	memories := []Memory{}
	for rows.Next() {
		memory, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan angel memory: %w", err)
		}
		memories = append(memories, memory)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate angel memory: %w", err)
	}
	return memories, nil
}

const selectMemoryColumns = `SELECT id, platform, scope_id, content, tags, strength, source, source_kind, source_actor_id, source_message_id, source_session_id, created_at, updated_at, last_accessed_at FROM memories`

func sourceFilterClause(filter SourceFilter) (string, []any) {
	parts := []string{}
	params := []any{}
	if actorID := strings.TrimSpace(filter.ActorID); actorID != "" {
		parts = append(parts, `source_actor_id = ?`)
		params = append(params, actorID)
	}
	if messageID := strings.TrimSpace(filter.MessageID); messageID != "" {
		parts = append(parts, `source_message_id = ?`)
		params = append(params, messageID)
	}
	if sessionID := strings.TrimSpace(filter.SessionID); sessionID != "" {
		parts = append(parts, `source_session_id = ?`)
		params = append(params, sessionID)
	}
	if len(parts) == 0 {
		return "", params
	}
	return " AND " + strings.Join(parts, " AND "), params
}

// Get returns one scoped memory by exact id.
func (s *Store) Get(ctx context.Context, platform, scopeID, id string) (*Memory, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("angel memory store is not open")
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	id = strings.TrimSpace(id)
	if platform == "" || scopeID == "" || id == "" {
		return nil, fmt.Errorf("angel memory platform, scope and id are required")
	}
	row := s.db.QueryRowContext(ctx, selectMemoryColumns+` WHERE platform = ? AND scope_id = ? AND id = ?`, platform, scopeID, id)
	memory, err := scanMemory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get angel memory: %w", err)
	}
	return &memory, nil
}

// List returns scoped memories, optionally filtered by structured source.
func (s *Store) List(ctx context.Context, platform, scopeID string, filter SourceFilter, limit int) ([]Memory, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("angel memory store is not open")
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	if platform == "" || scopeID == "" {
		return nil, fmt.Errorf("angel memory list platform and scope are required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 1000 {
		limit = 1000
	}
	clause, params := sourceFilterClause(filter)
	args := append([]any{platform, scopeID}, params...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, selectMemoryColumns+` WHERE platform = ? AND scope_id = ?`+clause+` ORDER BY strength DESC, updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list angel memory: %w", err)
	}
	return scanMemories(rows)
}

// ResolveID turns a unique id prefix into the full scoped id.
func (s *Store) ResolveID(ctx context.Context, platform, scopeID, prefix string, filter SourceFilter) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("angel memory store is not open")
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	prefix = strings.TrimSpace(prefix)
	if platform == "" || scopeID == "" || prefix == "" {
		return "", fmt.Errorf("angel memory platform, scope and id prefix are required")
	}
	clause, params := sourceFilterClause(filter)
	args := append([]any{platform, scopeID}, params...)
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM memories WHERE platform = ? AND scope_id = ?`+clause+` AND id LIKE ? ESCAPE '\' ORDER BY id LIMIT 20`, append(args, likePrefixPattern(prefix))...)
	if err != nil {
		return "", fmt.Errorf("resolve angel memory id: %w", err)
	}
	defer rows.Close()
	var matches []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("scan angel memory id: %w", err)
		}
		matches = append(matches, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate angel memory ids: %w", err)
	}
	if len(matches) == 0 {
		return "", ErrNotFound
	}
	if len(matches) > 1 {
		return "", ErrAmbiguousID
	}
	return matches[0], nil
}

func likePrefixPattern(prefix string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(strings.TrimSpace(prefix)) + "%"
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

// DeleteScoped deletes one memory only when it belongs to the given scope.
func (s *Store) DeleteScoped(ctx context.Context, platform, scopeID, id string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("angel memory store is not open")
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	id = strings.TrimSpace(id)
	if platform == "" || scopeID == "" || id == "" {
		return false, fmt.Errorf("angel memory platform, scope and id are required")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ? AND platform = ? AND scope_id = ?`, id, platform, scopeID)
	if err != nil {
		return false, fmt.Errorf("delete angel memory: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("angel memory rows affected: %w", err)
	}
	return affected > 0, nil
}

// DeleteBySource deletes all scoped memories matching the structured source
// filter. At least one filter field is required so a malformed call cannot
// clear an entire scope.
func (s *Store) DeleteBySource(ctx context.Context, platform, scopeID string, filter SourceFilter) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("angel memory store is not open")
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	if platform == "" || scopeID == "" {
		return 0, fmt.Errorf("angel memory delete platform and scope are required")
	}
	clause, params := sourceFilterClause(filter)
	if clause == "" {
		return 0, fmt.Errorf("angel memory source filter is required")
	}
	args := append([]any{platform, scopeID}, params...)
	result, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE platform = ? AND scope_id = ?`+clause, args...)
	if err != nil {
		return 0, fmt.Errorf("delete angel memory by source: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("angel memory rows affected: %w", err)
	}
	return int(affected), nil
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

// legacyToolSourceLabel is the free-text source every row written before
// structured provenance existed carries. The only production writer of that era
// passed "tool", so the label identifies the writer without guessing.
const legacyToolSourceLabel = "tool"

// LegacySourceBackfill describes what a legacy-provenance migration can and
// cannot recover. Rows without structured source columns keep that state: their
// actor, message and session were never recorded, so they must stay unmatched by
// source-based deletion instead of being guessed from text.
type LegacySourceBackfill struct {
	// Total is the number of memory rows in the database.
	Total int
	// Linked counts rows that already carry a structured source id.
	Linked int
	// Backfillable counts legacy rows whose source_kind is recoverable.
	Backfillable int
}

// LegacySourceBackfillStats reports the database-wide provenance coverage.
func (s *Store) LegacySourceBackfillStats(ctx context.Context) (LegacySourceBackfill, error) {
	if s == nil || s.db == nil {
		return LegacySourceBackfill{}, fmt.Errorf("angel memory store is not open")
	}
	var stats LegacySourceBackfill
	var linked, backfillable sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       SUM(CASE WHEN source_actor_id <> '' OR source_message_id <> '' OR source_session_id <> '' THEN 1 ELSE 0 END),
       SUM(CASE WHEN source_kind = '' AND source = ? THEN 1 ELSE 0 END)
FROM memories`, legacyToolSourceLabel).Scan(&stats.Total, &linked, &backfillable)
	if err != nil {
		return LegacySourceBackfill{}, fmt.Errorf("count angel memory provenance: %w", err)
	}
	stats.Linked = int(linked.Int64)
	stats.Backfillable = int(backfillable.Int64)
	return stats, nil
}

// BackfillLegacySourceKind recovers source_kind for legacy rows. It only moves a
// label whose meaning is fixed by the writer that produced it, and never invents
// an actor, message or session id, so the rows stay excluded from source-based
// deletion exactly as before. It returns the number of rows updated.
func (s *Store) BackfillLegacySourceKind(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("angel memory store is not open")
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE memories SET source_kind = ?
WHERE source_kind = '' AND source = ?`, legacyToolSourceLabel, legacyToolSourceLabel)
	if err != nil {
		return 0, fmt.Errorf("backfill angel memory source kind: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("angel memory rows affected: %w", err)
	}
	return int(affected), nil
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

func truncateSourceText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
