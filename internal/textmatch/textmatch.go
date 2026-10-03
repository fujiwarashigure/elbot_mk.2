// Package textmatch provides deterministic keyword extraction and safe LIKE
// helpers shared by the clean-room memory and learning search paths.
package textmatch

import (
	"strings"
	"unicode"
)

const (
	maxKeywords = 24
	// maxInputRunes bounds the text actually scanned for candidates. Longer
	// inputs are sampled from both ends so an entity at the end of a long
	// message is not silently dropped.
	maxInputRunes = 1200
	// maxCandidates bounds CJK n-gram generation; maxASCIIWords does the same
	// for ASCII words. They cap the temporary strings and map entries created
	// for an adversarial query.
	maxCandidates = 256
	maxASCIIWords = 64
	// maxASCIIKeywords stops English words from crowding out CJK terms when
	// both are present in one query.
	maxASCIIKeywords = 8
)

// Keywords extracts a bounded, deduplicated set of search terms from a user
// query. CJK runs produce 2-4 rune n-grams; ASCII words of two or more
// letters/digits are kept as whole words. Input and candidate generation are
// both bounded, CJK and ASCII terms are interleaved so neither starves the
// other, and long inputs are sampled from the head and tail.
func Keywords(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	set := &keywordSet{seen: map[string]struct{}{}}
	parts := sampleInput(value)
	for _, part := range parts {
		collectCJK([]rune(part), set)
	}
	for _, part := range parts {
		collectASCII(part, set)
	}
	out := set.keywords()
	if len(out) == 0 {
		// A query made of a single CJK character (for example "猫") should
		// still be searchable. Multi-rune queries intentionally avoid single
		// character terms because they are too noisy.
		for _, r := range []rune(value) {
			if unicode.Is(unicode.Han, r) {
				if set.add(string(r)) {
					out = append(out, string(r))
				}
				if len(out) >= maxKeywords {
					break
				}
			}
		}
	}
	return out
}

type keywordSet struct {
	cjk            []string
	ascii          []string
	seen           map[string]struct{}
	cjkGenerated   int
	asciiGenerated int
}

func (s *keywordSet) add(term string) bool {
	term = strings.TrimSpace(term)
	if term == "" {
		return false
	}
	key := strings.ToLower(term)
	if _, ok := s.seen[key]; ok {
		return false
	}
	s.seen[key] = struct{}{}
	return true
}

// keywords merges the two pools. CJK terms are emitted first and ASCII words
// are capped at maxASCIIKeywords until the CJK pool is exhausted, so a long
// English tail cannot consume every slot.
func (s *keywordSet) keywords() []string {
	if len(s.cjk) == 0 && len(s.ascii) == 0 {
		return nil
	}
	asciiCap := maxKeywords
	if len(s.cjk) > 0 {
		asciiCap = maxASCIIKeywords
	}
	out := make([]string, 0, maxKeywords)
	ci, ai := 0, 0
	for len(out) < maxKeywords && (ci < len(s.cjk) || ai < len(s.ascii)) {
		if ci < len(s.cjk) {
			out = append(out, s.cjk[ci])
			ci++
			if len(out) == maxKeywords {
				break
			}
		}
		if ai < len(s.ascii) {
			if ai < asciiCap {
				out = append(out, s.ascii[ai])
				ai++
			} else if ci >= len(s.cjk) {
				out = append(out, s.ascii[ai])
				ai++
			}
		}
	}
	return out
}

// sampleInput returns the whole value for short inputs, or the head and tail
// halves for long ones. Keeping the tail matters for questions that put the
// subject at the end ("...还记得我喜欢吃什么吗").
func sampleInput(value string) []string {
	runes := []rune(value)
	if len(runes) <= maxInputRunes {
		return []string{value}
	}
	half := maxInputRunes / 2
	return []string{string(runes[:half]), string(runes[len(runes)-half:])}
}

func collectCJK(runes []rune, set *keywordSet) {
	for i := 0; i < len(runes) && set.cjkGenerated < maxCandidates; {
		if !isWordRune(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && isWordRune(runes[j]) {
			j++
		}
		segment := runes[i:j]
		for start := 0; start < len(segment) && set.cjkGenerated < maxCandidates; start++ {
			for size := 2; size <= 4 && start+size <= len(segment) && set.cjkGenerated < maxCandidates; size++ {
				part := string(segment[start : start+size])
				if !containsCJK(part) {
					continue
				}
				// Only distinct terms count against the budget, so a runaway
				// repetition of the same phrase cannot exhaust it before the
				// tail of the message is scanned. Input length is already
				// bounded, so the raw generation work stays bounded too.
				if set.add(part) {
					set.cjk = append(set.cjk, part)
					set.cjkGenerated++
				}
			}
		}
		i = j
	}
}

func collectASCII(value string, set *keywordSet) {
	for _, word := range strings.FieldsFunc(value, func(r rune) bool {
		return !isWordRune(r)
	}) {
		if set.asciiGenerated >= maxASCIIWords {
			return
		}
		if !isASCIIWord(word) || len([]rune(word)) < 2 {
			continue
		}
		lower := strings.ToLower(word)
		if set.add(lower) {
			set.ascii = append(set.ascii, lower)
			set.asciiGenerated++
		}
	}
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
