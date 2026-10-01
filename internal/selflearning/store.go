package selflearning

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"elbot/internal/storage"

	_ "modernc.org/sqlite"
)

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
	KindExpression = "expression"
	KindJargon     = "jargon"
)

type Candidate struct {
	ID        string
	Kind      string
	Platform  string
	ScopeID   string
	Pattern   string
	Meaning   string
	Count     int
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("self learning sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create self learning directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open self learning sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable self learning foreign keys: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping self learning sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS observations (
    id TEXT PRIMARY KEY,
    platform TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_selflearning_observations_scope_created
ON observations(platform, scope_id, created_at);

CREATE TABLE IF NOT EXISTS candidates (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    platform TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    pattern TEXT NOT NULL,
    meaning TEXT NOT NULL DEFAULT '',
    count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(kind, platform, scope_id, pattern)
);
CREATE INDEX IF NOT EXISTS idx_selflearning_candidates_scope_status
ON candidates(kind, platform, scope_id, status, count);
`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate self learning sqlite: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Observe(ctx context.Context, platform, scopeID, userID, text string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("self learning store is not open")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO observations (id, platform, scope_id, user_id, text, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		storage.NewID(), strings.TrimSpace(platform), strings.TrimSpace(scopeID), strings.TrimSpace(userID), text, storage.FormatTime(storage.Now()))
	if err != nil {
		return fmt.Errorf("observe self learning message: %w", err)
	}
	return nil
}

// Mine scans recent observations and creates reviewable frequent-pattern
// candidates. It is deliberately simple and deterministic: CJK n-grams 2-4 and
// ASCII words length >= 3, excluding common stop phrases.
func (s *Store) Mine(ctx context.Context, platform, scopeID string, minCount, limit int) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("self learning store is not open")
	}
	if minCount <= 0 {
		minCount = 3
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT text FROM observations
WHERE platform = ? AND scope_id = ?
ORDER BY created_at DESC
LIMIT 1000`, strings.TrimSpace(platform), strings.TrimSpace(scopeID))
	if err != nil {
		return 0, fmt.Errorf("load self learning observations: %w", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return 0, fmt.Errorf("scan self learning observation: %w", err)
		}
		for _, token := range extractCandidates(text) {
			counts[token]++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate self learning observations: %w", err)
	}
	type pair struct {
		token string
		count int
	}
	ordered := make([]pair, 0, len(counts))
	for token, count := range counts {
		if count < minCount || isStopCandidate(token) {
			continue
		}
		ordered = append(ordered, pair{token: token, count: count})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].count != ordered[j].count {
			return ordered[i].count > ordered[j].count
		}
		return ordered[i].token < ordered[j].token
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	created := 0
	for _, item := range ordered {
		kind := KindExpression
		if isASCIIWord(item.token) {
			kind = KindJargon
		}
		if err := s.upsertCandidate(ctx, kind, platform, scopeID, item.token, item.count); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func (s *Store) upsertCandidate(ctx context.Context, kind, platform, scopeID, pattern string, count int) error {
	now := storage.FormatTime(storage.Now())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO candidates (id, kind, platform, scope_id, pattern, meaning, count, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, '', ?, 'pending', ?, ?)
ON CONFLICT(kind, platform, scope_id, pattern) DO UPDATE SET
    count = excluded.count,
    updated_at = excluded.updated_at
WHERE candidates.status = 'pending'`,
		storage.NewID(), kind, strings.TrimSpace(platform), strings.TrimSpace(scopeID), pattern, count, now, now)
	if err != nil {
		return fmt.Errorf("upsert self learning candidate: %w", err)
	}
	return nil
}

func (s *Store) ListCandidates(ctx context.Context, platform, scopeID, status string, limit int) ([]Candidate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("self learning store is not open")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	conditions := []string{"platform = ?", "scope_id = ?"}
	params := []any{strings.TrimSpace(platform), strings.TrimSpace(scopeID)}
	if strings.TrimSpace(status) != "" {
		conditions = append(conditions, "status = ?")
		params = append(params, strings.TrimSpace(status))
	}
	params = append(params, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, platform, scope_id, pattern, meaning, count, status, created_at, updated_at
FROM candidates
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY count DESC, updated_at DESC
LIMIT ?`, params...)
	if err != nil {
		return nil, fmt.Errorf("list self learning candidates: %w", err)
	}
	return scanCandidates(rows)
}

func (s *Store) Decide(ctx context.Context, id, status, meaning string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("self learning store is not open")
	}
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case StatusApproved, StatusRejected, StatusPending:
	default:
		return fmt.Errorf("unsupported review status %q", status)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE candidates SET status = ?, meaning = ?, updated_at = ? WHERE id = ?`,
		status, strings.TrimSpace(meaning), storage.FormatTime(storage.Now()), strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("decide self learning candidate: %w", err)
	}
	return nil
}

func (s *Store) ApprovedContext(ctx context.Context, platform, scopeID, query string, limit int) ([]Candidate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("self learning store is not open")
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, platform, scope_id, pattern, meaning, count, status, created_at, updated_at
FROM candidates
WHERE platform = ? AND scope_id = ? AND status = 'approved'
ORDER BY count DESC, updated_at DESC
LIMIT ?`, strings.TrimSpace(platform), strings.TrimSpace(scopeID), limit)
	if err != nil {
		return nil, fmt.Errorf("load approved self learning context: %w", err)
	}
	return scanCandidates(rows)
}

func (s *Store) Counts(ctx context.Context, platform, scopeID string) (pending int, approved int, err error) {
	if s == nil || s.db == nil {
		return 0, 0, nil
	}
	err = s.db.QueryRowContext(ctx, `SELECT
  COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN status = 'approved' THEN 1 ELSE 0 END), 0)
FROM candidates WHERE platform = ? AND scope_id = ?`, strings.TrimSpace(platform), strings.TrimSpace(scopeID)).Scan(&pending, &approved)
	if err != nil {
		return 0, 0, fmt.Errorf("count self learning candidates: %w", err)
	}
	return pending, approved, nil
}

func (s *Store) DeleteBefore(ctx context.Context, cutoff time.Time) (int, int, error) {
	if s == nil || s.db == nil {
		return 0, 0, nil
	}
	formatted := storage.FormatTime(cutoff)
	candidates, err := s.db.ExecContext(ctx, `DELETE FROM candidates WHERE updated_at < ?`, formatted)
	if err != nil {
		return 0, 0, fmt.Errorf("delete old self learning candidates: %w", err)
	}
	observations, err := s.db.ExecContext(ctx, `DELETE FROM observations WHERE created_at < ?`, formatted)
	if err != nil {
		return 0, 0, fmt.Errorf("delete old self learning observations: %w", err)
	}
	candidateRows, _ := candidates.RowsAffected()
	observationRows, _ := observations.RowsAffected()
	return int(candidateRows), int(observationRows), nil
}

func scanCandidates(rows *sql.Rows) ([]Candidate, error) {
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		var candidate Candidate
		var createdAt, updatedAt string
		if err := rows.Scan(&candidate.ID, &candidate.Kind, &candidate.Platform, &candidate.ScopeID, &candidate.Pattern, &candidate.Meaning, &candidate.Count, &candidate.Status, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan self learning candidate: %w", err)
		}
		candidate.CreatedAt, _ = storage.ParseTime(createdAt)
		candidate.UpdatedAt, _ = storage.ParseTime(updatedAt)
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate self learning candidates: %w", err)
	}
	return out, nil
}

var stopCandidates = map[string]bool{
	"这个": true, "那个": true, "什么": true, "怎么": true, "不是": true, "可以": true, "没有": true,
	"就是": true, "因为": true, "所以": true, "但是": true, "如果": true, "现在": true, "今天": true,
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "you": true,
}

func isStopCandidate(token string) bool {
	return stopCandidates[strings.ToLower(token)]
}

func isASCIIWord(token string) bool {
	for _, r := range token {
		if r > unicode.MaxASCII || !unicode.IsLetter(r) {
			return false
		}
	}
	return token != ""
}

func extractCandidates(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	out := []string{}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
			continue
		}
		for size := 2; size <= 4 && i+size <= len(runes); size++ {
			part := string(runes[i : i+size])
			if !containsCJK(part) && len(part) < 3 {
				continue
			}
			out = append(out, part)
		}
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		word = strings.ToLower(strings.TrimSpace(word))
		if len(word) >= 3 && isASCIIWord(word) {
			out = append(out, word)
		}
	}
	return out
}

func containsCJK(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
