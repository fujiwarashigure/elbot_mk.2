// Package groupkb implements deterministic, local-only matching for the
// per-group FAQ/knowledge base. It never calls a model and never interprets
// the matched text as an instruction.
package groupkb

import (
	"strings"
	"unicode"

	"elbot/internal/config"
	"golang.org/x/text/width"
)

const (
	MatchExact    = "exact"
	MatchContains = "contains"
	MatchKeywords = "keywords"
)

// NormalizeMatchMode returns a supported match mode, defaulting to exact.
func NormalizeMatchMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case MatchContains:
		return MatchContains
	case MatchKeywords, "keyword", "all_keywords", "all-keywords":
		return MatchKeywords
	default:
		return MatchExact
	}
}

// Normalize builds the deterministic comparison key used by all match modes:
// full-width Latin/punctuation is narrowed, ASCII case is folded, whitespace is
// collapsed, and common sentence punctuation is removed. It is intentionally a
// display-independent transformation, not a language model.
func Normalize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = width.Narrow.String(value)
	value = strings.ToLower(value)
	var b strings.Builder
	b.Grow(len(value))
	lastSpace := false
	for _, r := range value {
		if unicode.IsSpace(r) {
			if b.Len() > 0 && !lastSpace {
				b.WriteByte(' ')
			}
			lastSpace = true
			continue
		}
		if isSentencePunctuation(r) {
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}

func isSentencePunctuation(r rune) bool {
	switch r {
	case '.', ',', '!', '?', ';', ':', '、', '，', '。', '！', '？', '；', '：',
		'…', '—', '～', '(', ')', '（', '）', '[', ']', '【', '】',
		'"', '\'', '“', '”', '‘', '’':
		return true
	default:
		return false
	}
}

// Match returns the highest-priority enabled entry that matches the bounded
// text: exact entries first, then contains, then keywords. Entries keep their
// stored order inside each mode.
func Match(entries []config.GroupKnowledgeEntry, text string, maxScanRunes int) (config.GroupKnowledgeEntry, bool) {
	normalized := Normalize(limitRunes(text, maxScanRunes))
	if normalized == "" {
		return config.GroupKnowledgeEntry{}, false
	}
	for _, mode := range []string{MatchExact, MatchContains, MatchKeywords} {
		for _, entry := range entries {
			if !entry.IsEnabled() || NormalizeMatchMode(entry.Match) != mode {
				continue
			}
			switch mode {
			case MatchContains:
				if containsAny(normalized, entry.Question, entry.Aliases) {
					return entry, true
				}
			case MatchKeywords:
				if matchAllKeywords(normalized, entry.Keywords) {
					return entry, true
				}
			default:
				if equalsAny(normalized, entry.Question, entry.Aliases) {
					return entry, true
				}
			}
		}
	}
	return config.GroupKnowledgeEntry{}, false
}

func equalsAny(normalized, primary string, aliases []string) bool {
	if key := Normalize(primary); key != "" && normalized == key {
		return true
	}
	for _, alias := range aliases {
		if key := Normalize(alias); key != "" && normalized == key {
			return true
		}
	}
	return false
}

func containsAny(normalized, primary string, aliases []string) bool {
	if key := Normalize(primary); key != "" && strings.Contains(normalized, key) {
		return true
	}
	for _, alias := range aliases {
		if key := Normalize(alias); key != "" && strings.Contains(normalized, key) {
			return true
		}
	}
	return false
}

func matchAllKeywords(normalized string, keywords []string) bool {
	found := false
	for _, keyword := range keywords {
		key := Normalize(keyword)
		if key == "" {
			continue
		}
		if !strings.Contains(normalized, key) {
			return false
		}
		found = true
	}
	return found
}

func limitRunes(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}
