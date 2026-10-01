package resident

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pelletier/go-toml/v2"

	"elbot/internal/security"
	"elbot/internal/session"
	"elbot/internal/storage"
)

const (
	DefaultCoreMaxUnits   = 200
	DefaultNormalMaxUnits = 300
)

var ErrNotFound = errors.New("resident memory not found")

// errFileChanged reports that the on-disk memories file was modified between
// the load and the save of one update. The caller reloads and retries instead
// of overwriting the external change.
var errFileChanged = errors.New("resident memory file changed during update")

// maxUpdateAttempts bounds the optimistic-concurrency retry loop. A conflict
// only happens when an external writer touches memories.toml in the tiny
// window between our load and rename, so a few retries are ample.
const maxUpdateAttempts = 3

type Limits struct {
	Core   int
	Normal int
}

// NormalWritePolicy controls normal memory writes. Zero values disable the
// corresponding guard. The app runtime enables safe defaults from app.toml.
//
// Normal is stored as newline-separated entries (one fact per line). MaxLines
// therefore bounds both the number of lines and the number of entries.
type NormalWritePolicy struct {
	MinInterval              time.Duration
	Window                   time.Duration
	MaxWrites                int
	MaxLines                 int
	MaxUnitsPerEntry         int
	BlockInstructionPatterns bool
}

// Normalized removes invalid/partial rate-limit settings.
func (p NormalWritePolicy) Normalized() NormalWritePolicy {
	if p.MinInterval < 0 {
		p.MinInterval = 0
	}
	if p.Window <= 0 || p.MaxWrites <= 0 {
		p.Window = 0
		p.MaxWrites = 0
	}
	if p.MaxLines < 0 {
		p.MaxLines = 0
	}
	if p.MaxUnitsPerEntry < 0 {
		p.MaxUnitsPerEntry = 0
	}
	return p
}

// normalInstructionPatterns is a conservative, best-effort filter. It cannot
// replace the trust boundary around injected memory, but it blocks the most
// explicit "memory as instruction" payloads before they are persisted.
var normalInstructionPatterns = []string{
	"ignore previous instructions",
	"ignore all previous instructions",
	"ignore the above instructions",
	"disregard previous instructions",
	"forget previous instructions",
	"ignore system prompt",
	"override system prompt",
	"system prompt override",
	"developer message",
	"jailbreak",
	"you are now",
	"from now on you",
	"忽略之前的指令",
	"忽略以上指令",
	"忽略前面的指令",
	"忽略前面的要求",
	"无视之前的指令",
	"忘记之前的指令",
	"忽略系统提示词",
	"忽略系统指令",
	"覆盖系统提示",
	"越狱",
	"从现在开始你",
	"接下来你要",
	"不要遵守",
}

type Memory struct {
	Core   string `json:"core"`
	Normal string `json:"normal"`
}

func (m Memory) Empty() bool {
	return strings.TrimSpace(m.Core) == "" && strings.TrimSpace(m.Normal) == ""
}

func (m Memory) Text() string {
	parts := []string{}
	if core := strings.TrimSpace(m.Core); core != "" {
		parts = append(parts, core)
	}
	if normal := strings.TrimSpace(m.Normal); normal != "" {
		entries := splitNormalEntries(normal)
		for i := range entries {
			entries[i] = "- " + entries[i]
		}
		parts = append(parts, strings.Join(entries, "\n"))
	}
	return strings.Join(parts, "\n")
}

type ResidentMemory struct {
	Platform  string `toml:"platform"`
	ActorID   string `toml:"actor_id"`
	Core      string `toml:"core"`
	Normal    string `toml:"normal"`
	CreatedAt string `toml:"created_at"`
	UpdatedAt string `toml:"updated_at"`
}

type tomlFile struct {
	ResidentMemories []ResidentMemory `toml:"resident_memories"`
}

type Store struct {
	Path         string
	Limits       Limits
	NormalPolicy NormalWritePolicy
	mu           sync.Mutex
	cache        storeCache
	normalWrites map[string]normalWriteState
}

type normalWriteState struct {
	last   time.Time
	recent []time.Time
}

type storeCache struct {
	loaded bool
	file   tomlFile
	state  fileState
}

type fileState struct {
	exists  bool
	size    int64
	modTime time.Time
}

func NewStore(path string) *Store {
	return NewStoreWithOptions(path, Limits{}, NormalWritePolicy{})
}

func NewStoreWithLimits(path string, limits Limits) *Store {
	return NewStoreWithOptions(path, limits, NormalWritePolicy{})
}

func NewStoreWithOptions(path string, limits Limits, policy NormalWritePolicy) *Store {
	return &Store{
		Path:         path,
		Limits:       normalizeLimits(limits),
		NormalPolicy: policy.Normalized(),
	}
}

func ActorScope(actor security.Actor) session.Scope {
	return session.Scope{ActorID: actor.ID, Platform: actor.Platform}
}

func (s *Store) Read(ctx context.Context, scope session.Scope) (Memory, error) {
	if err := ctx.Err(); err != nil {
		return Memory{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return Memory{}, err
	}
	idx := findMemory(file.ResidentMemories, scope)
	if idx < 0 {
		return Memory{}, ErrNotFound
	}
	memory := file.ResidentMemories[idx]
	return Memory{Core: strings.TrimSpace(memory.Core), Normal: strings.TrimSpace(memory.Normal)}, nil
}

func (s *Store) WriteCore(ctx context.Context, scope session.Scope, content string) error {
	return s.update(ctx, scope, false, func(memory *ResidentMemory) {
		memory.Core = strings.TrimSpace(content)
	})
}

func (s *Store) WriteNormal(ctx context.Context, scope session.Scope, content string) error {
	return s.update(ctx, scope, true, func(memory *ResidentMemory) {
		memory.Normal = strings.TrimSpace(content)
	})
}

func (s *Store) AppendNormal(ctx context.Context, scope session.Scope, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("resident memory normal content is required")
	}
	return s.update(ctx, scope, true, func(memory *ResidentMemory) {
		existing := strings.TrimSpace(memory.Normal)
		if existing == "" {
			memory.Normal = content
		} else {
			memory.Normal = existing + "\n" + content
		}
	})
}

func (s *Store) DeleteNormal(ctx context.Context, scope session.Scope) error {
	return s.WriteNormal(ctx, scope, "")
}

func (s *Store) LimitsOrDefault() Limits {
	return normalizeLimits(s.Limits)
}

func (s *Store) update(ctx context.Context, scope session.Scope, normal bool, update func(*ResidentMemory)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateScope(scope); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for attempt := 0; attempt < maxUpdateAttempts; attempt++ {
		file, err := s.loadLocked()
		if err != nil {
			return err
		}
		loadedState := s.cache.state
		nowTime := storage.Now()
		now := storage.FormatTime(nowTime)
		idx := findMemory(file.ResidentMemories, scope)
		if idx < 0 {
			file.ResidentMemories = append(file.ResidentMemories, ResidentMemory{Platform: scope.Platform, ActorID: scope.ActorID, CreatedAt: now})
			idx = len(file.ResidentMemories) - 1
		}
		memory := file.ResidentMemories[idx]
		update(&memory)
		memory.Core = strings.TrimSpace(memory.Core)
		if normal {
			memory.Normal = normalizeNormalEntries(memory.Normal)
		} else {
			memory.Normal = strings.TrimSpace(memory.Normal)
		}
		if normal {
			if err := validateNormal(memory, s.LimitsOrDefault(), s.NormalPolicy); err != nil {
				return err
			}
			if err := s.checkNormalWritePolicy(scope, nowTime); err != nil {
				return err
			}
		} else if err := validateCore(memory, s.LimitsOrDefault()); err != nil {
			return err
		}
		if memory.Core == "" && memory.Normal == "" {
			file.ResidentMemories = append(file.ResidentMemories[:idx], file.ResidentMemories[idx+1:]...)
		} else {
			memory.UpdatedAt = now
			if memory.CreatedAt == "" {
				memory.CreatedAt = now
			}
			file.ResidentMemories[idx] = memory
		}
		if err := s.saveLocked(file, loadedState); err != nil {
			if errors.Is(err, errFileChanged) {
				s.cache = storeCache{}
				continue
			}
			return err
		}
		if normal {
			s.recordNormalWrite(scope, nowTime)
		}
		return nil
	}
	return fmt.Errorf("resident memory update conflicted with concurrent file changes, please retry")
}

func validateCore(memory ResidentMemory, limits Limits) error {
	if units := CountUnits(memory.Core); units > limits.Core {
		return fmt.Errorf("resident memory core is too long: %d/%d units", units, limits.Core)
	}
	return nil
}

func validateNormal(memory ResidentMemory, limits Limits, policy NormalWritePolicy) error {
	if units := CountUnits(memory.Normal); units > limits.Normal {
		return fmt.Errorf("resident memory normal is too long: %d/%d units", units, limits.Normal)
	}
	return validateNormalContent(memory.Normal, policy)
}

func validateNormalContent(content string, policy NormalWritePolicy) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	for _, r := range content {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("resident memory normal content contains an unsupported control character")
		}
	}
	entries := splitNormalEntries(content)
	if policy.MaxLines > 0 && len(entries) > policy.MaxLines {
		return fmt.Errorf("resident memory normal has too many entries: %d/%d", len(entries), policy.MaxLines)
	}
	for _, entry := range entries {
		if policy.MaxUnitsPerEntry > 0 {
			if units := CountUnits(entry); units > policy.MaxUnitsPerEntry {
				return fmt.Errorf("resident memory normal entry is too long: %d/%d units", units, policy.MaxUnitsPerEntry)
			}
		}
		if !policy.BlockInstructionPatterns {
			continue
		}
		lower := strings.ToLower(entry)
		for _, pattern := range normalInstructionPatterns {
			if strings.Contains(lower, strings.ToLower(pattern)) {
				return fmt.Errorf("resident memory normal content looks like an instruction and was rejected: %q", pattern)
			}
		}
	}
	return nil
}

// normalizeNormalEntries turns arbitrary normal content into the canonical
// one-fact-per-line form: blank lines removed, list markers stripped,
// whitespace collapsed, exact duplicates dropped (case-insensitively), and
// entries joined with "\n". It is applied to every normal write so the stored
// value, the read result and the injected prompt stay consistent.
func normalizeNormalEntries(content string) string {
	entries := splitNormalEntries(content)
	if len(entries) == 0 {
		return ""
	}
	out := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		entry = stripNormalEntryPrefix(entry)
		entry = strings.Join(strings.Fields(entry), " ")
		if entry == "" {
			continue
		}
		key := strings.ToLower(entry)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	return strings.Join(out, "\n")
}

func splitNormalEntries(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	raw := strings.Split(content, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// stripNormalEntryPrefix removes a single leading markdown/numbered list marker
// so "1. 用户喜欢短回复" and "- 用户喜欢短回复" normalize to the same entry.
func stripNormalEntryPrefix(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"-", "*", "•", "·", "—"} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	i := 0
	runes := []rune(line)
	for i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
		i++
	}
	if i > 0 && i < len(runes) {
		switch runes[i] {
		case '.', ')', '、', '．':
			return strings.TrimSpace(string(runes[i+1:]))
		}
	}
	return line
}

func (s *Store) checkNormalWritePolicy(scope session.Scope, now time.Time) error {
	policy := s.NormalPolicy
	if policy.MinInterval <= 0 && (policy.Window <= 0 || policy.MaxWrites <= 0) {
		return nil
	}
	state := s.normalWrites[scopeKey(scope)]
	if policy.MinInterval > 0 && !state.last.IsZero() {
		elapsed := now.Sub(state.last)
		if elapsed < policy.MinInterval {
			remaining := policy.MinInterval - elapsed
			return fmt.Errorf("resident memory normal write too frequent: wait %s", remaining.Round(time.Second))
		}
	}
	if policy.Window > 0 && policy.MaxWrites > 0 {
		cutoff := now.Add(-policy.Window)
		count := 0
		for _, writtenAt := range state.recent {
			if writtenAt.After(cutoff) {
				count++
			}
		}
		if count >= policy.MaxWrites {
			return fmt.Errorf("resident memory normal write limit reached: max %d per %s", policy.MaxWrites, policy.Window)
		}
	}
	return nil
}

func (s *Store) recordNormalWrite(scope session.Scope, now time.Time) {
	policy := s.NormalPolicy
	if policy.MinInterval <= 0 && (policy.Window <= 0 || policy.MaxWrites <= 0) {
		return
	}
	if s.normalWrites == nil {
		s.normalWrites = map[string]normalWriteState{}
	}
	key := scopeKey(scope)
	state := s.normalWrites[key]
	state.last = now
	if policy.Window > 0 && policy.MaxWrites > 0 {
		cutoff := now.Add(-policy.Window)
		pruned := state.recent[:0]
		for _, writtenAt := range state.recent {
			if writtenAt.After(cutoff) {
				pruned = append(pruned, writtenAt)
			}
		}
		state.recent = append(pruned, now)
	}
	s.normalWrites[key] = state
}

func scopeKey(scope session.Scope) string {
	return strings.TrimSpace(scope.Platform) + "\x00" + strings.TrimSpace(scope.ActorID)
}

func CountUnits(content string) int {
	units := 0
	inWord := false
	for _, r := range content {
		switch {
		case isCJK(r):
			units++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				units++
				inWord = true
			}
		default:
			inWord = false
		}
	}
	return units
}

func isCJK(r rune) bool {
	return (r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}

func (s *Store) loadLocked() (tomlFile, error) {
	path := strings.TrimSpace(s.Path)
	if path == "" {
		return tomlFile{}, fmt.Errorf("resident memory path is required")
	}
	state, err := currentFileState(path)
	if err != nil {
		return tomlFile{}, err
	}
	if s.cache.loaded && sameFileState(s.cache.state, state) {
		return cloneFile(s.cache.file), nil
	}
	if !state.exists {
		file := tomlFile{}
		s.cache = storeCache{loaded: true, file: file, state: state}
		return file, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return tomlFile{}, fmt.Errorf("read resident memory %q: %w", path, err)
	}
	var file tomlFile
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := toml.Unmarshal(data, &file); err != nil {
			return tomlFile{}, fmt.Errorf("parse resident memory %q: %w", path, err)
		}
	}
	s.cache = storeCache{loaded: true, file: cloneFile(file), state: state}
	return file, nil
}

func (s *Store) saveLocked(file tomlFile, expected fileState) error {
	path := strings.TrimSpace(s.Path)
	if path == "" {
		return fmt.Errorf("resident memory path is required")
	}
	current, err := currentFileState(path)
	if err != nil {
		return err
	}
	if !sameFileState(expected, current) {
		return errFileChanged
	}
	data, err := toml.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshal resident memory: %w", err)
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("write resident memory %q: %w", path, err)
	}
	state, err := currentFileState(path)
	if err != nil {
		return err
	}
	s.cache = storeCache{loaded: true, file: cloneFile(file), state: state}
	return nil
}

func currentFileState(path string) (fileState, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileState{}, nil
	}
	if err != nil {
		return fileState{}, fmt.Errorf("stat resident memory %q: %w", path, err)
	}
	return fileState{exists: true, size: info.Size(), modTime: info.ModTime()}, nil
}

func sameFileState(left, right fileState) bool {
	return left.exists == right.exists && left.size == right.size && left.modTime.Equal(right.modTime)
}

func cloneFile(file tomlFile) tomlFile {
	out := tomlFile{ResidentMemories: make([]ResidentMemory, len(file.ResidentMemories))}
	copy(out.ResidentMemories, file.ResidentMemories)
	return out
}

func findMemory(memories []ResidentMemory, scope session.Scope) int {
	platform := strings.TrimSpace(scope.Platform)
	actorID := strings.TrimSpace(scope.ActorID)
	for i, memory := range memories {
		if memory.Platform == platform && memory.ActorID == actorID {
			return i
		}
	}
	return -1
}

func validateScope(scope session.Scope) error {
	if strings.TrimSpace(scope.Platform) == "" {
		return fmt.Errorf("resident memory platform scope is required")
	}
	if strings.TrimSpace(scope.ActorID) == "" {
		return fmt.Errorf("resident memory actor scope is required")
	}
	return nil
}

func normalizeLimits(limits Limits) Limits {
	if limits.Core <= 0 {
		limits.Core = DefaultCoreMaxUnits
	}
	if limits.Normal <= 0 {
		limits.Normal = DefaultNormalMaxUnits
	}
	return limits
}
