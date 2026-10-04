package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/groupkb"
	"elbot/internal/session"
)

// tryGroupKnowledgeAnswer checks the local, deterministic FAQ before any
// session/LLM work. A hit is answered locally and consumed; a miss returns
// false so the normal chat path continues.
func (a *Agent) tryGroupKnowledgeAnswer(ctx context.Context, text string) bool {
	if a == nil || !a.isGroupScope(ctx) {
		return false
	}
	scope := a.scope(ctx)
	if !a.groupKnowledgeEnabledForScope(scope) {
		return false
	}
	entry, ok := groupkb.Match(a.groupKnowledgeForScope(scope), text, a.groupKnowledgeCfg.MaxMatchRunes)
	if !ok {
		return false
	}
	answer := strings.TrimSpace(entry.Answer)
	if answer == "" {
		return false
	}
	a.audit("group_knowledge_answer", "platform", scope.Platform, "scope", scope.PlatformScopeID, "actor_id", a.actor(ctx).ID, "knowledge_id", entry.ID)
	if _, err := a.sendChatWithReceipt(ctx, answer); err != nil {
		a.audit("group_knowledge_send_error", "platform", scope.Platform, "scope", scope.PlatformScopeID, "knowledge_id", entry.ID, "error", err.Error())
	}
	return true
}

func (a *Agent) groupKnowledgeEnabledForScope(scope session.Scope) bool {
	if a == nil || !a.groupKnowledgeCfg.IsEnabled() {
		return false
	}
	return a.groupPolicyForScope(scope).IsKnowledgeEnabled()
}

func cloneGroupKnowledge(input map[string][]config.GroupKnowledgeEntry) map[string][]config.GroupKnowledgeEntry {
	if len(input) == 0 {
		return map[string][]config.GroupKnowledgeEntry{}
	}
	out := make(map[string][]config.GroupKnowledgeEntry, len(input))
	for key, entries := range input {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = append([]config.GroupKnowledgeEntry(nil), entries...)
	}
	return out
}

func (a *Agent) setGroupKnowledgeSnapshot(snapshot map[string][]config.GroupKnowledgeEntry) {
	if a == nil {
		return
	}
	a.groupKnowledgeMu.Lock()
	a.groupKnowledge = cloneGroupKnowledge(snapshot)
	a.groupKnowledgeMu.Unlock()
}

func (a *Agent) groupKnowledgeSnapshot() map[string][]config.GroupKnowledgeEntry {
	if a == nil {
		return map[string][]config.GroupKnowledgeEntry{}
	}
	a.groupKnowledgeMu.RLock()
	defer a.groupKnowledgeMu.RUnlock()
	return cloneGroupKnowledge(a.groupKnowledge)
}

func (a *Agent) groupKnowledgeForScope(scope session.Scope) []config.GroupKnowledgeEntry {
	if a == nil {
		return nil
	}
	key := contextOverflowKey(scope)
	a.groupKnowledgeMu.RLock()
	defer a.groupKnowledgeMu.RUnlock()
	return append([]config.GroupKnowledgeEntry(nil), a.groupKnowledge[key]...)
}

func (a *Agent) setGroupKnowledgeForScope(scope session.Scope, entries []config.GroupKnowledgeEntry) {
	if a == nil {
		return
	}
	key := contextOverflowKey(scope)
	a.groupKnowledgeMu.Lock()
	defer a.groupKnowledgeMu.Unlock()
	if a.groupKnowledge == nil {
		a.groupKnowledge = map[string][]config.GroupKnowledgeEntry{}
	}
	if len(entries) == 0 {
		delete(a.groupKnowledge, key)
		return
	}
	a.groupKnowledge[key] = append([]config.GroupKnowledgeEntry(nil), entries...)
}

// GroupKnowledgeList returns a copy of the current group's entries.
func (a *Agent) GroupKnowledgeList(ctx context.Context) ([]config.GroupKnowledgeEntry, error) {
	if err := a.requireGroupKnowledge(ctx); err != nil {
		return nil, err
	}
	return a.groupKnowledgeForScope(a.scope(ctx)), nil
}

// GroupKnowledgeAdd creates a deterministic entry in the current group scope.
func (a *Agent) GroupKnowledgeAdd(ctx context.Context, question, answer, match string, aliases, keywords []string) (config.GroupKnowledgeEntry, error) {
	if err := a.requireGroupKnowledge(ctx); err != nil {
		return config.GroupKnowledgeEntry{}, err
	}
	scope := a.scope(ctx)
	cfg := a.groupKnowledgeCfg.Normalized()
	question = strings.TrimSpace(question)
	answer = strings.TrimSpace(answer)
	if question == "" || answer == "" {
		return config.GroupKnowledgeEntry{}, fmt.Errorf("问题和答案不能为空")
	}
	if limit := cfg.MaxQuestionRunes; limit > 0 && runeLen(question) > limit {
		return config.GroupKnowledgeEntry{}, fmt.Errorf("问题超过 %d 个字符", limit)
	}
	if limit := cfg.MaxAnswerRunes; limit > 0 && runeLen(answer) > limit {
		return config.GroupKnowledgeEntry{}, fmt.Errorf("答案超过 %d 个字符", limit)
	}
	cleanAliases, err := normalizeKnowledgeList(aliases, cfg.MaxAliases, "别名", cfg.MaxQuestionRunes)
	if err != nil {
		return config.GroupKnowledgeEntry{}, err
	}
	cleanKeywords, err := normalizeKnowledgeList(keywords, cfg.MaxKeywords, "关键词", cfg.MaxQuestionRunes)
	if err != nil {
		return config.GroupKnowledgeEntry{}, err
	}
	mode := groupkb.NormalizeMatchMode(match)
	if mode == groupkb.MatchKeywords && len(cleanKeywords) == 0 {
		return config.GroupKnowledgeEntry{}, fmt.Errorf("keywords 模式至少需要一个关键词")
	}

	a.groupKnowledgeWriteMu.Lock()
	defer a.groupKnowledgeWriteMu.Unlock()
	previous := a.groupKnowledgeSnapshot()
	entries := append([]config.GroupKnowledgeEntry(nil), a.groupKnowledgeForScope(scope)...)
	if len(entries) >= cfg.MaxEntriesPerScope {
		return config.GroupKnowledgeEntry{}, fmt.Errorf("当前群的群知识库已达到 %d 条上限", cfg.MaxEntriesPerScope)
	}
	entry := config.GroupKnowledgeEntry{
		ID:        nextKnowledgeID(entries),
		Question:  question,
		Answer:    answer,
		Aliases:   cleanAliases,
		Keywords:  cleanKeywords,
		Match:     mode,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	entries = append(entries, entry)
	a.setGroupKnowledgeForScope(scope, entries)
	if err := a.saveRuntimeState(); err != nil {
		a.setGroupKnowledgeSnapshot(previous)
		return config.GroupKnowledgeEntry{}, err
	}
	return entry, nil
}

// GroupKnowledgeRemove deletes one entry by ID. IDs are case-insensitive.
func (a *Agent) GroupKnowledgeRemove(ctx context.Context, id string) (bool, error) {
	if err := a.requireGroupKnowledge(ctx); err != nil {
		return false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false, fmt.Errorf("知识 ID 不能为空")
	}
	scope := a.scope(ctx)
	a.groupKnowledgeWriteMu.Lock()
	defer a.groupKnowledgeWriteMu.Unlock()
	previous := a.groupKnowledgeSnapshot()
	entries := append([]config.GroupKnowledgeEntry(nil), a.groupKnowledgeForScope(scope)...)
	kept := entries[:0]
	removed := false
	for _, entry := range entries {
		if strings.EqualFold(strings.TrimSpace(entry.ID), id) {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return false, nil
	}
	a.setGroupKnowledgeForScope(scope, kept)
	if err := a.saveRuntimeState(); err != nil {
		a.setGroupKnowledgeSnapshot(previous)
		return false, err
	}
	return true, nil
}

// GroupKnowledgeClear removes every entry in the current group scope.
func (a *Agent) GroupKnowledgeClear(ctx context.Context) (int, error) {
	if err := a.requireGroupKnowledge(ctx); err != nil {
		return 0, err
	}
	scope := a.scope(ctx)
	a.groupKnowledgeWriteMu.Lock()
	defer a.groupKnowledgeWriteMu.Unlock()
	previous := a.groupKnowledgeSnapshot()
	entries := a.groupKnowledgeForScope(scope)
	if len(entries) == 0 {
		return 0, nil
	}
	a.setGroupKnowledgeForScope(scope, nil)
	if err := a.saveRuntimeState(); err != nil {
		a.setGroupKnowledgeSnapshot(previous)
		return 0, err
	}
	return len(entries), nil
}

// GroupKnowledgeTest runs the deterministic matcher without sending an answer.
func (a *Agent) GroupKnowledgeTest(ctx context.Context, text string) (config.GroupKnowledgeEntry, bool, error) {
	if err := a.requireGroupKnowledge(ctx); err != nil {
		return config.GroupKnowledgeEntry{}, false, err
	}
	scope := a.scope(ctx)
	entry, ok := groupkb.Match(a.groupKnowledgeForScope(scope), text, a.groupKnowledgeCfg.MaxMatchRunes)
	return entry, ok, nil
}

func (a *Agent) requireGroupKnowledge(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("agent is nil")
	}
	if !a.groupKnowledgeCfg.IsEnabled() {
		return fmt.Errorf("群知识库已全局关闭")
	}
	if !a.isGroupScope(ctx) {
		return fmt.Errorf("群知识库只适用于群聊")
	}
	return nil
}

func normalizeKnowledgeList(values []string, max int, label string, maxRunes int) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if maxRunes > 0 && runeLen(value) > maxRunes {
			return nil, fmt.Errorf("%s超过 %d 个字符", label, maxRunes)
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
		if max > 0 && len(out) > max {
			return nil, fmt.Errorf("%s超过 %d 个上限", label, max)
		}
	}
	return out, nil
}

func nextKnowledgeID(entries []config.GroupKnowledgeEntry) string {
	used := map[string]bool{}
	for _, entry := range entries {
		used[strings.ToLower(strings.TrimSpace(entry.ID))] = true
	}
	for i := 1; ; i++ {
		id := "k" + strconv.Itoa(i)
		if !used[id] {
			return id
		}
	}
}

func normalizeGroupKnowledgeSnapshot(input map[string][]config.GroupKnowledgeEntry, cfg config.GroupKnowledgeConfig) map[string][]config.GroupKnowledgeEntry {
	cfg = cfg.Normalized()
	out := make(map[string][]config.GroupKnowledgeEntry, len(input))
	for key, entries := range input {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		clean := make([]config.GroupKnowledgeEntry, 0, minInt(len(entries), cfg.MaxEntriesPerScope))
		for _, entry := range entries {
			normalized, ok := normalizeGroupKnowledgeEntry(entry, cfg)
			if !ok {
				continue
			}
			if normalized.ID == "" {
				normalized.ID = nextKnowledgeID(clean)
			}
			clean = append(clean, normalized)
			if len(clean) >= cfg.MaxEntriesPerScope {
				break
			}
		}
		if len(clean) > 0 {
			out[key] = clean
		}
	}
	return out
}

func normalizeGroupKnowledgeEntry(entry config.GroupKnowledgeEntry, cfg config.GroupKnowledgeConfig) (config.GroupKnowledgeEntry, bool) {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Question = truncateRunes(strings.TrimSpace(entry.Question), cfg.MaxQuestionRunes)
	entry.Answer = truncateRunes(strings.TrimSpace(entry.Answer), cfg.MaxAnswerRunes)
	entry.Match = groupkb.NormalizeMatchMode(entry.Match)
	entry.Aliases = truncateKnowledgeList(entry.Aliases, cfg.MaxAliases, cfg.MaxQuestionRunes)
	entry.Keywords = truncateKnowledgeList(entry.Keywords, cfg.MaxKeywords, cfg.MaxQuestionRunes)
	entry.UpdatedAt = strings.TrimSpace(entry.UpdatedAt)
	if entry.Question == "" || entry.Answer == "" {
		return config.GroupKnowledgeEntry{}, false
	}
	if entry.Match == groupkb.MatchKeywords && len(entry.Keywords) == 0 {
		return config.GroupKnowledgeEntry{}, false
	}
	return entry, true
}

func truncateKnowledgeList(values []string, max, maxRunes int) []string {
	out := make([]string, 0, minInt(len(values), max))
	seen := map[string]bool{}
	for _, value := range values {
		value = truncateRunes(strings.TrimSpace(value), maxRunes)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func runeLen(value string) int {
	return len([]rune(value))
}

func truncateRunes(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
