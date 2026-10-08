package fileops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// RollbackMaxBytes bounds the pre-edit content kept in memory.
	RollbackMaxBytes int64 = 256 * 1024 * 1024
	// RollbackMaxRecords bounds how many files can be rolled back at once.
	RollbackMaxRecords = 1024
)

var (
	// ErrRollbackNotFound means there is no usable record: it was never
	// created, was already used, was replaced by a newer edit or was evicted.
	ErrRollbackNotFound = errors.New("no rollback record: it may have been consumed, replaced, or evicted")
	// ErrRollbackConflict means the file changed after the recorded edit, so
	// rolling back would discard a change made by shell or another program.
	ErrRollbackConflict = errors.New("file changed after the recorded edit; rollback would discard external changes")
)

// RollbackInfo describes one rollback record without exposing file contents.
type RollbackInfo struct {
	ID             uint64
	Path           string
	Created        bool
	EditedAt       time.Time
	RevisionBefore string
	RevisionAfter  string
	Bytes          int64
}

type rollbackRecord struct {
	RollbackInfo
	sessionID string
	before    []byte
	mode      os.FileMode
	// existed is false when the recorded edit created the file, so rolling back
	// removes it instead of restoring bytes.
	existed bool
}

// RollbackStore keeps the content that existed before the most recent edit of
// each file, per Session. It is process-local: restarting ElBot drops every
// record. Only the latest edit of one file can be undone.
type RollbackStore struct {
	mu         sync.Mutex
	scopes     map[string]map[string]*rollbackRecord
	records    map[uint64]*rollbackRecord
	nextID     uint64
	bytes      int64
	maxBytes   int64
	maxRecords int
}

func NewRollbackStore() *RollbackStore {
	return &RollbackStore{
		scopes:     map[string]map[string]*rollbackRecord{},
		records:    map[uint64]*rollbackRecord{},
		maxBytes:   RollbackMaxBytes,
		maxRecords: RollbackMaxRecords,
	}
}

// Record publishes the pre-edit state of one successful edit. A newer record for
// the same file replaces the previous one.
func (s *RollbackStore) Record(sessionID, path string, before []byte, mode os.FileMode, existed bool, revisionBefore, revisionAfter string) (RollbackInfo, bool) {
	if s == nil {
		return RollbackInfo{}, false
	}
	sessionID = strings.TrimSpace(sessionID)
	path = strings.TrimSpace(path)
	if sessionID == "" || path == "" {
		return RollbackInfo{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := s.scopes[sessionID]
	if scope == nil {
		scope = map[string]*rollbackRecord{}
		s.scopes[sessionID] = scope
	}
	if previous := scope[path]; previous != nil {
		s.removeLocked(previous)
	}
	s.nextID++
	record := &rollbackRecord{
		RollbackInfo: RollbackInfo{
			ID:             s.nextID,
			Path:           path,
			Created:        !existed,
			EditedAt:       time.Now(),
			RevisionBefore: revisionBefore,
			RevisionAfter:  revisionAfter,
			Bytes:          int64(len(before)),
		},
		sessionID: sessionID,
		before:    append([]byte(nil), before...),
		mode:      mode,
		existed:   existed,
	}
	s.records[record.ID] = record
	scope[path] = record
	s.bytes += record.Bytes
	available := true
	for s.bytes > s.maxBytes || len(s.records) > s.maxRecords {
		if !s.evictOldestLocked() {
			break
		}
		if _, ok := s.records[record.ID]; !ok {
			available = false
		}
	}
	return record.RollbackInfo, available
}

// List returns one Session's records ordered by age.
func (s *RollbackStore) List(sessionID string) []RollbackInfo {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := s.scopes[strings.TrimSpace(sessionID)]
	out := make([]RollbackInfo, 0, len(scope))
	for _, record := range scope {
		out = append(out, record.RollbackInfo)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get resolves one record of a Session by its number.
func (s *RollbackStore) Get(sessionID string, id uint64) (RollbackInfo, bool) {
	if s == nil {
		return RollbackInfo{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.scopes[strings.TrimSpace(sessionID)][s.recordKeyLocked(sessionID, id)]
	if record == nil {
		return RollbackInfo{}, false
	}
	return record.RollbackInfo, true
}

// Forget drops every record of one Session, e.g. after switching it away.
func (s *RollbackStore) Forget(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	for _, record := range s.scopes[sessionID] {
		s.removeLocked(record)
	}
	delete(s.scopes, sessionID)
}

// Restore puts the recorded content back and consumes the record. It refuses to
// overwrite a file that changed after the edit (shell, another program or an
// external editor), so only ElBot's own last edit can be undone.
func (s *RollbackStore) Restore(ctx context.Context, sessionID string, id uint64) (RollbackInfo, error) {
	if s == nil {
		return RollbackInfo{}, ErrRollbackNotFound
	}
	if err := ctx.Err(); err != nil {
		return RollbackInfo{}, err
	}
	s.mu.Lock()
	record := s.scopes[strings.TrimSpace(sessionID)][s.recordKeyLocked(sessionID, id)]
	s.mu.Unlock()
	if record == nil {
		return RollbackInfo{}, ErrRollbackNotFound
	}

	if record.existed {
		current, err := os.ReadFile(record.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return RollbackInfo{}, ErrRollbackNotFound
			}
			return RollbackInfo{}, fmt.Errorf("read file before rollback: %w", err)
		}
		if ContentRevision(current) != record.RevisionAfter {
			return RollbackInfo{}, ErrRollbackConflict
		}
		mode := record.mode
		if mode == 0 {
			mode = 0o644
		}
		if err := AtomicWriteFile(record.Path, record.before, mode); err != nil {
			return RollbackInfo{}, err
		}
	} else {
		if _, err := os.Lstat(record.Path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return RollbackInfo{}, ErrRollbackNotFound
			}
			return RollbackInfo{}, fmt.Errorf("check created file before rollback: %w", err)
		}
		if err := os.Remove(record.Path); err != nil {
			return RollbackInfo{}, fmt.Errorf("remove created file: %w", err)
		}
	}

	s.mu.Lock()
	s.removeLocked(record)
	s.mu.Unlock()
	return record.RollbackInfo, nil
}

// recordKeyLocked maps one record number back to its file inside a Session.
func (s *RollbackStore) recordKeyLocked(sessionID string, id uint64) string {
	for path, record := range s.scopes[strings.TrimSpace(sessionID)] {
		if record.ID == id {
			return path
		}
	}
	return ""
}

func (s *RollbackStore) evictOldestLocked() bool {
	var oldest *rollbackRecord
	for _, candidate := range s.records {
		if oldest == nil || candidate.ID < oldest.ID {
			oldest = candidate
		}
	}
	if oldest == nil {
		return false
	}
	s.removeLocked(oldest)
	return true
}

func (s *RollbackStore) removeLocked(record *rollbackRecord) {
	delete(s.records, record.ID)
	if scope := s.scopes[record.sessionID]; scope != nil {
		if scope[record.Path] == record {
			delete(scope, record.Path)
		}
		if len(scope) == 0 {
			delete(s.scopes, record.sessionID)
		}
	}
	s.bytes -= record.Bytes
	if s.bytes < 0 {
		s.bytes = 0
	}
}
