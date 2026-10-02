// Package textmatch provides deterministic keyword extraction and safe LIKE
// helpers shared by the clean-room memory and learning search paths.
package textmatch

import (
	"strings"
	"unicode"
)

const maxKeywords = 24

// Keywords extracts a bounded, deduplicated set of search terms from a user
// query. CJK runs produce 2-4 rune n-grams in source order; ASCII words of two
// or more letters/digits are kept as whole words. The first maxKeywords terms
// are retained, which keeps full coverage for normal chat-sized queries while
// still bounding the generated SQL.
func Keywords(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, maxKeywords)
	add := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		key := strings.ToLower(term)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, term)
	}

	for _, word := range strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if isASCIIWord(word) && len([]rune(word)) >= 2 {
			add(strings.ToLower(word))
		}
	}

	runes := []rune(value)
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

	if len(out) > maxKeywords {
		out = out[:maxKeywords]
	}
	if len(out) == 0 {
		// A query made of a single CJK character (for example "猫") should
		// still be searchable. Multi-rune queries intentionally avoid single
		// character terms because they are too noisy.
		for _, r := range []rune(value) {
			if unicode.Is(unicode.Han, r) {
				add(string(r))
			}
		}
	}
	return out
}

// LikePattern escapes LIKE wildcards in term and wraps it for a substring
// match. Callers must use "LIKE ? ESCAPE '\\'".
func LikePattern(term string) string {
	term = strings.TrimSpace(term)
	if term == "" {
		return "%"
	}
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(term)
	return "%" + escaped + "%"
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isASCIIWord(value string) bool {
	if value == "" {
		return false
	}
	hasLetter := false
	for _, r := range value {
		if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r)) {
			return false
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
	}
	return hasLetter
}

func containsCJK(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
