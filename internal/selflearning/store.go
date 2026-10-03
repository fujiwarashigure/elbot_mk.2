package selflearning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"elbot/internal/storage"
	"elbot/internal/textmatch"

	_ "modernc.org/sqlite"
)

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
	KindExpression = "expression"
	KindJargon     = "jargon"
)

var (
	// ErrCandidateNotFound means the review target does not exist in the
	// requested platform/scope.
	ErrCandidateNotFound = errors.New("self learning candidate not found")
	// ErrObservationTooLong is returned by the store when one observation
	// exceeds the configured rune budget.
	ErrObservationTooLong = errors.New("self learning observation is too long")
	// ErrObservationLimit is returned when a scope reached its stored
	// observation count limit.
	ErrObservationLimit = errors.New("self learning observation scope limit reached")
)

type Candidate struct {
	ID         string
	Kind       string
	Platform   string
	ScopeID    string
	Pattern    string
	Meaning    string
	Count      int
	UserCount  int
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ReviewedAt time.Time
	ReviewedBy string
}

// ReviewRecord is one entry in a candidate's append-only audit trail.
type ReviewRecord struct {
	ID          string
	CandidateID string
	Platform    string
	ScopeID     string
	FromStatus  string
	ToStatus    string
	Meaning     string
	Reviewer    string
	CreatedAt   time.Time
}

// MineStats separates real inserts from pending-row refreshes and unchanged or
// already-decided candidates, and reports how much of the corpus was scanned.
type MineStats struct {
	Created    int
	Updated    int
	Skipped    int
	Scanned    int
	TotalChars int
	Truncated  bool
	TimedOut   bool
}

func (m MineStats) Total() int { return m.Created + m.Updated + m.Skipped }

// DecideRequest identifies one candidate and the scope that owns it.
type DecideRequest struct {
	ID       string
	Platform string
	ScopeID  string
	Status   string
	Meaning  string
	Reviewer string
}

// StoreLimits are write-side limits enforced directly by the SQLite store.
type StoreLimits struct {
	MaxObservationRunes     int
	MaxObservationsPerScope int
	MaxMineChars            int
}

// SetLimits applies write-side limits. It is intended to be called once while
// the store is being constructed, before concurrent use.
func (s *Store) SetLimits(limits StoreLimits) {
	if s == nil {
		return
	}
	if limits.MaxObservationRunes <= 0 {
		limits.MaxObservationRunes = defaultMaxObservationRunes
	}
	if limits.MaxObservationsPerScope <= 0 {
		limits.MaxObservationsPerScope = defaultMaxObservationsPerScope
	}
	if limits.MaxMineChars <= 0 {
		limits.MaxMineChars = defaultMaxMineChars
	}
	s.limits = limits
}

type Store struct {
	db     *sql.DB
	limits StoreLimits
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
    user_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    reviewed_at TEXT NOT NULL DEFAULT '',
    reviewed_by TEXT NOT NULL DEFAULT '',
    UNIQUE(kind, platform, scope_id, pattern)
);
CREATE INDEX IF NOT EXISTS idx_selflearning_candidates_scope_status
ON candidates(kind, platform, scope_id, status, count);

CREATE TABLE IF NOT EXISTS candidate_reviews (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL,
    platform TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    from_status TEXT NOT NULL DEFAULT '',
    to_status TEXT NOT NULL,
    meaning TEXT NOT NULL DEFAULT '',
    reviewer TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_selflearning_reviews_candidate
ON candidate_reviews(candidate_id, created_at);
`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate self learning sqlite: %w", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE candidates ADD COLUMN user_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE candidates ADD COLUMN reviewed_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE candidates ADD COLUMN reviewed_by TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			_ = db.Close()
			return nil, fmt.Errorf("migrate self learning candidates: %w", err)
		}
	}
	return &Store{
		db: db,
		limits: StoreLimits{
			MaxObservationRunes:     defaultMaxObservationRunes,
			MaxObservationsPerScope: defaultMaxObservationsPerScope,
			MaxMineChars:            defaultMaxMineChars,
		},
	}, nil
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
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	userID = strings.TrimSpace(userID)
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if platform == "" || scopeID == "" {
		return fmt.Errorf("self learning observation platform and scope are required")
	}
	if max := s.limits.MaxObservationRunes; max > 0 && len([]rune(text)) > max {
		return fmt.Errorf("%w: %d runes (max %d)", ErrObservationTooLong, len([]rune(text)), max)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin self learning observation write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if max := s.limits.MaxObservationsPerScope; max > 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM observations WHERE platform = ? AND scope_id = ?`,
			platform, scopeID).Scan(&count); err != nil {
			return fmt.Errorf("count self learning observations: %w", err)
		}
		if count >= max {
			return fmt.Errorf("%w: %d rows (max %d)", ErrObservationLimit, count, max)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO observations (id, platform, scope_id, user_id, text, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		storage.NewID(), platform, scopeID, userID, text, storage.FormatTime(storage.Now())); err != nil {
		return fmt.Errorf("observe self learning message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit self learning observation: %w", err)
	}
	return nil
}

// Mine scans recent observations and creates reviewable frequent-pattern
// candidates. CJK n-grams 2-4 run only inside contiguous letter/digit runs,
// ASCII words length >= 3 are extracted once per message, each message
// contributes at most one count per token, and a candidate must be supported by
// at least minUsers distinct users. The scan is bounded by row count, total
// characters and the caller's context.
func (s *Store) Mine(ctx context.Context, platform, scopeID string, minCount, minUsers, limit int) (MineStats, error) {
	var stats MineStats
	if s == nil || s.db == nil {
		return stats, fmt.Errorf("self learning store is not open")
	}
	if minCount <= 0 {
		minCount = defaultMinCount
	}
	if minUsers <= 0 {
		minUsers = defaultMinUsers
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	rows, err := s.db.QueryContext(ctx, `
SELECT text, user_id FROM observations
WHERE platform = ? AND scope_id = ?
ORDER BY created_at DESC
LIMIT 1000`, platform, scopeID)
	if err != nil {
		return stats, fmt.Errorf("load self learning observations: %w", err)
	}
	defer rows.Close()

	type tokenStat struct {
		count int
		users map[string]struct{}
	}
	counts := map[string]*tokenStat{}
	budget := s.limits.MaxMineChars
	for rows.Next() {
		var text, userID string
		if err := rows.Scan(&text, &userID); err != nil {
			return stats, fmt.Errorf("scan self learning observation: %w", err)
		}
		text = strings.TrimSpace(text)
		size := len([]rune(text))
		if budget > 0 && stats.TotalChars+size > budget && stats.Scanned > 0 {
			stats.Truncated = true
			break
		}
		stats.TotalChars += size
		stats.Scanned++
		userID = strings.TrimSpace(userID)
		seen := map[string]struct{}{}
		for _, token := range extractCandidates(text) {
			if _, dup := seen[token]; dup {
				continue
			}
			seen[token] = struct{}{}
			entry := counts[token]
			if entry == nil {
				entry = &tokenStat{users: map[string]struct{}{}}
				counts[token] = entry
			}
			entry.count++
			if userID != "" {
				entry.users[userID] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return stats, fmt.Errorf("iterate self learning observations: %w", err)
	}
	// Release the single SQLite connection before the upsert transactions
	// below, especially when the character budget broke out of the row loop
	// early. database/sql only auto-closes on a complete iteration.
	_ = rows.Close()

	type ranked struct {
		token string
		count int
		users int
	}
	ordered := make([]ranked, 0, len(counts))
	for token, entry := range counts {
		if entry.count < minCount || len(entry.users) < minUsers || isStopCandidate(token) {
			continue
		}
		ordered = append(ordered, ranked{token: token, count: entry.count, users: len(entry.users)})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].count != ordered[j].count {
			return ordered[i].count > ordered[j].count
		}
		if ordered[i].users != ordered[j].users {
			return ordered[i].users > ordered[j].users
		}
		return ordered[i].token < ordered[j].token
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	for _, item := range ordered {
		kind := KindExpression
		if isASCIIWord(item.token) {
			kind = KindJargon
		}
		result, err := s.upsertCandidate(ctx, kind, platform, scopeID, item.token, item.count, item.users)
		if err != nil {
			return stats, err
		}
		switch result {
		case upsertCreated:
			stats.Created++
		case upsertUpdated:
			stats.Updated++
		default:
			stats.Skipped++
		}
	}
	return stats, nil
}

type upsertResult int

const (
	upsertSkipped upsertResult = iota
	upsertCreated
	upsertUpdated
)

func (s *Store) upsertCandidate(ctx context.Context, kind, platform, scopeID, pattern string, count, userCount int) (upsertResult, error) {
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	// The SELECT and INSERT/UPDATE run in one transaction so two concurrent
	// mining passes cannot both decide a candidate is missing and then race to
	// insert it. SetMaxOpenConns(1) serialises statements, not this sequence.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return upsertSkipped, fmt.Errorf("begin self learning candidate upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var existingID, existingStatus string
	var existingCount, existingUserCount int
	err = tx.QueryRowContext(ctx, `
SELECT id, status, count, user_count FROM candidates
WHERE kind = ? AND platform = ? AND scope_id = ? AND pattern = ?`,
		kind, platform, scopeID, pattern).Scan(&existingID, &existingStatus, &existingCount, &existingUserCount)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		now := storage.FormatTime(storage.Now())
		if _, err := tx.ExecContext(ctx, `
INSERT INTO candidates (id, kind, platform, scope_id, pattern, meaning, count, user_count, status, created_at, updated_at, reviewed_at, reviewed_by)
VALUES (?, ?, ?, ?, ?, '', ?, ?, 'pending', ?, ?, '', '')`,
			storage.NewID(), kind, platform, scopeID, pattern, count, userCount, now, now); err != nil {
			return upsertSkipped, fmt.Errorf("insert self learning candidate: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return upsertSkipped, fmt.Errorf("commit self learning candidate insert: %w", err)
		}
		return upsertCreated, nil
	case err != nil:
		return upsertSkipped, fmt.Errorf("load self learning candidate: %w", err)
	}
	if existingStatus != StatusPending {
		return upsertSkipped, nil
	}
	if count == existingCount && userCount == existingUserCount {
		return upsertSkipped, nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE candidates SET count = ?, user_count = ?, updated_at = ? WHERE id = ?`,
		count, userCount, storage.FormatTime(storage.Now()), existingID); err != nil {
		return upsertSkipped, fmt.Errorf("update self learning candidate: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return upsertSkipped, fmt.Errorf("commit self learning candidate update: %w", err)
	}
	return upsertUpdated, nil
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
SELECT id, kind, platform, scope_id, pattern, meaning, count, user_count, status, created_at, updated_at, reviewed_at, reviewed_by
FROM candidates
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY count DESC, updated_at DESC
LIMIT ?`, params...)
	if err != nil {
		return nil, fmt.Errorf("list self learning candidates: %w", err)
	}
	return scanCandidates(rows)
}

// Decide updates one candidate only when it belongs to the requested scope.
func (s *Store) Decide(ctx context.Context, req DecideRequest) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("self learning store is not open")
	}
	status := strings.TrimSpace(strings.ToLower(req.Status))
	switch status {
	case StatusApproved, StatusRejected, StatusPending:
	default:
		return fmt.Errorf("unsupported review status %q", status)
	}
	id := strings.TrimSpace(req.ID)
	platform := strings.TrimSpace(req.Platform)
	scopeID := strings.TrimSpace(req.ScopeID)
	if id == "" || platform == "" || scopeID == "" {
		return fmt.Errorf("self learning decide requires id, platform and scope")
	}
	meaning := strings.TrimSpace(req.Meaning)
	reviewer := strings.TrimSpace(req.Reviewer)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin self learning decide: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var fromStatus string
	if err := tx.QueryRowContext(ctx, `
SELECT status FROM candidates WHERE id = ? AND platform = ? AND scope_id = ?`,
		id, platform, scopeID).Scan(&fromStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCandidateNotFound
		}
		return fmt.Errorf("load self learning candidate for decide: %w", err)
	}
	now := storage.FormatTime(storage.Now())
	if _, err := tx.ExecContext(ctx, `
UPDATE candidates
SET status = ?, meaning = ?, reviewed_at = ?, reviewed_by = ?, updated_at = ?
WHERE id = ? AND platform = ? AND scope_id = ?`,
		status, meaning, now, reviewer, now, id, platform, scopeID); err != nil {
		return fmt.Errorf("decide self learning candidate: %w", err)
	}
	if err := insertReviewRecord(ctx, tx, ReviewRecord{
		CandidateID: id,
		Platform:    platform,
		ScopeID:     scopeID,
		FromStatus:  fromStatus,
		ToStatus:    status,
		Meaning:     meaning,
		Reviewer:    reviewer,
		CreatedAt:   storage.Now(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit self learning decide: %w", err)
	}
	return nil
}

func insertReviewRecord(ctx context.Context, tx *sql.Tx, record ReviewRecord) error {
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = storage.Now()
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO candidate_reviews (id, candidate_id, platform, scope_id, from_status, to_status, meaning, reviewer, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		storage.NewID(), record.CandidateID, record.Platform, record.ScopeID, record.FromStatus, record.ToStatus, record.Meaning, record.Reviewer, storage.FormatTime(createdAt)); err != nil {
		return fmt.Errorf("record self learning review: %w", err)
	}
	return nil
}

// History returns the review trail for one candidate inside the requested
// scope, newest first.
func (s *Store) History(ctx context.Context, platform, scopeID, candidateID string, limit int) ([]ReviewRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("self learning store is not open")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, candidate_id, platform, scope_id, from_status, to_status, meaning, reviewer, created_at
FROM candidate_reviews
WHERE platform = ? AND scope_id = ? AND candidate_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ?`,
		strings.TrimSpace(platform), strings.TrimSpace(scopeID), strings.TrimSpace(candidateID), limit)
	if err != nil {
		return nil, fmt.Errorf("list self learning review history: %w", err)
	}
	defer rows.Close()
	out := []ReviewRecord{}
	for rows.Next() {
		var record ReviewRecord
		var createdAt string
		if err := rows.Scan(&record.ID, &record.CandidateID, &record.Platform, &record.ScopeID, &record.FromStatus, &record.ToStatus, &record.Meaning, &record.Reviewer, &createdAt); err != nil {
			return nil, fmt.Errorf("scan self learning review history: %w", err)
		}
		record.CreatedAt, _ = storage.ParseTime(createdAt)
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate self learning review history: %w", err)
	}
	return out, nil
}

func (s *Store) ApprovedContext(ctx context.Context, platform, scopeID, query string, limit int) ([]Candidate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("self learning store is not open")
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	platform = strings.TrimSpace(platform)
	scopeID = strings.TrimSpace(scopeID)
	const selectColumns = `SELECT id, kind, platform, scope_id, pattern, meaning, count, user_count, status, created_at, updated_at, reviewed_at, reviewed_by FROM candidates`
	terms := textmatch.Keywords(query)
	load := func(statement string, params ...any) ([]Candidate, error) {
		rows, err := s.db.QueryContext(ctx, statement, params...)
		if err != nil {
			return nil, fmt.Errorf("load approved self learning context: %w", err)
		}
		return scanCandidates(rows)
	}
	if len(terms) == 0 {
		return load(selectColumns+`
WHERE platform = ? AND scope_id = ? AND status = 'approved'
ORDER BY count DESC, updated_at DESC
LIMIT ?`, platform, scopeID, limit)
	}

	whereParts := make([]string, 0, len(terms))
	scoreParts := make([]string, 0, len(terms))
	whereParams := make([]any, 0, len(terms)*2)
	scoreParams := make([]any, 0, len(terms)*2)
	for _, term := range terms {
		pattern := textmatch.LikePattern(term)
		whereParts = append(whereParts, `(pattern LIKE ? ESCAPE '\' OR meaning LIKE ? ESCAPE '\')`)
		whereParams = append(whereParams, pattern, pattern)
		scoreParts = append(scoreParts, `(CASE WHEN pattern LIKE ? ESCAPE '\' THEN 2 ELSE 0 END + CASE WHEN meaning LIKE ? ESCAPE '\' THEN 1 ELSE 0 END)`)
		scoreParams = append(scoreParams, pattern, pattern)
	}
	params := make([]any, 0, 2+len(whereParams)+len(scoreParams)+1)
	params = append(params, platform, scopeID)
	params = append(params, whereParams...)
	params = append(params, scoreParams...)
	params = append(params, limit)
	statement := selectColumns + `
WHERE platform = ? AND scope_id = ? AND status = 'approved' AND (` + strings.Join(whereParts, " OR ") + `)
ORDER BY (` + strings.Join(scoreParts, " + ") + `) DESC, count DESC, updated_at DESC
LIMIT ?`
	candidates, err := load(statement, params...)
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 {
		return candidates, nil
	}
	return load(selectColumns+`
WHERE platform = ? AND scope_id = ? AND status = 'approved'
ORDER BY count DESC, updated_at DESC
LIMIT ?`, platform, scopeID, limit)
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
	if _, err := s.db.ExecContext(ctx, `DELETE FROM candidate_reviews WHERE created_at < ?`, formatted); err != nil {
		return 0, 0, fmt.Errorf("delete old self learning review history: %w", err)
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
		var createdAt, updatedAt, reviewedAt string
		if err := rows.Scan(
			&candidate.ID,
			&candidate.Kind,
			&candidate.Platform,
			&candidate.ScopeID,
			&candidate.Pattern,
			&candidate.Meaning,
			&candidate.Count,
			&candidate.UserCount,
			&candidate.Status,
			&createdAt,
			&updatedAt,
			&reviewedAt,
			&candidate.ReviewedBy,
		); err != nil {
			return nil, fmt.Errorf("scan self learning candidate: %w", err)
		}
		candidate.CreatedAt, _ = storage.ParseTime(createdAt)
		candidate.UpdatedAt, _ = storage.ParseTime(updatedAt)
		if strings.TrimSpace(reviewedAt) != "" {
			candidate.ReviewedAt, _ = storage.ParseTime(reviewedAt)
		}
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

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isASCIIWord(token string) bool {
	if token == "" {
		return false
	}
	hasLetter := false
	for _, r := range token {
		if r > unicode.MaxASCII || !isWordRune(r) {
			return false
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
	}
	return hasLetter
}

func extractCandidates(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0)
	add := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}

	runes := []rune(text)
	for i := 0; i < len(runes); {
		if !isWordRune(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && isWordRune(runes[j]) {
			j++
		}
		segment := runes[i:j]
		for start := 0; start < len(segment); start++ {
			for size := 2; size <= 4 && start+size <= len(segment); size++ {
				part := string(segment[start : start+size])
				if containsCJK(part) {
					add(part)
				}
			}
		}
		i = j
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !isWordRune(r)
	}) {
		word = strings.ToLower(strings.TrimSpace(word))
		if len([]rune(word)) >= 3 && isASCIIWord(word) {
			add(word)
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

// Undo moves a candidate back to pending while preserving its current meaning.
func (s *Store) Undo(ctx context.Context, req DecideRequest) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("self learning store is not open")
	}
	id := strings.TrimSpace(req.ID)
	platform := strings.TrimSpace(req.Platform)
	scopeID := strings.TrimSpace(req.ScopeID)
	if id == "" || platform == "" || scopeID == "" {
		return fmt.Errorf("self learning undo requires id, platform and scope")
	}
	reviewer := strings.TrimSpace(req.Reviewer)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin self learning undo: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var fromStatus string
	if err := tx.QueryRowContext(ctx, `
SELECT status FROM candidates WHERE id = ? AND platform = ? AND scope_id = ?`,
		id, platform, scopeID).Scan(&fromStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCandidateNotFound
		}
		return fmt.Errorf("load self learning candidate for undo: %w", err)
	}
	now := storage.FormatTime(storage.Now())
	if _, err := tx.ExecContext(ctx, `
UPDATE candidates
SET status = 'pending', reviewed_at = ?, reviewed_by = ?, updated_at = ?
WHERE id = ? AND platform = ? AND scope_id = ?`,
		now, reviewer, now, id, platform, scopeID); err != nil {
		return fmt.Errorf("undo self learning candidate: %w", err)
	}
	if err := insertReviewRecord(ctx, tx, ReviewRecord{
		CandidateID: id,
		Platform:    platform,
		ScopeID:     scopeID,
		FromStatus:  fromStatus,
		ToStatus:    StatusPending,
		Reviewer:    reviewer,
		CreatedAt:   storage.Now(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit self learning undo: %w", err)
	}
	return nil
}
