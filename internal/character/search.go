package character

import (
	"context"
	"sort"
	"strings"
)

// Search limits.
const (
	DefaultSearchLimit = 5
	MaxSearchLimit     = 20
	snippetRunes       = 160
)

// Search performs a lightweight in-memory search over the character index.
// matchMode is "or" (default) or "and"; tag optionally filters by tag.
func (s *Store) Search(ctx context.Context, query, matchMode, tag string, limit int, viewer Viewer) ([]SearchResult, error) {
	terms := splitQueryTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	matchAll := strings.EqualFold(strings.TrimSpace(matchMode), "and")
	tag = strings.ToLower(strings.TrimSpace(tag))
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]SearchResult, 0)
	for _, id := range s.order {
		item := s.entries[id]
		if !item.VisibleTo(viewer) {
			continue
		}
		if tag != "" && !hasTag(item.Tags, tag) {
			continue
		}
		score, snippet := item.matchTerms(terms, matchAll)
		if score == 0 {
			continue
		}
		results = append(results, SearchResult{ID: item.ID, Name: item.Name, Tags: append([]string(nil), item.Tags...), Snippet: snippet, Score: score})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), want) {
			return true
		}
	}
	return false
}

func splitQueryTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', '，', '|', ';', '；':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

func (c *Character) matchTerms(terms []string, matchAll bool) (int, string) {
	meta := strings.ToLower(strings.Join([]string{
		c.ID,
		c.Name,
		c.Description,
		strings.Join(c.Aliases, " "),
		strings.Join(c.Tags, " "),
	}, "\n"))
	kinds := make([]string, 0, len(c.Docs))
	for kind := range c.Docs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	score := 0
	matched := 0
	snippetKind := ""
	snippetText := ""
	for _, term := range terms {
		count := strings.Count(meta, term) * 3
		found := strings.Contains(meta, term)
		for _, kind := range kinds {
			content := c.Docs[kind]
			lower := strings.ToLower(content)
			if n := strings.Count(lower, term); n > 0 {
				found = true
				count += n
				if snippetText == "" {
					snippetKind = kind
					snippetText = content
				}
			}
		}
		if !found {
			if matchAll {
				return 0, ""
			}
			continue
		}
		matched++
		score += count
	}
	if matched == 0 {
		return 0, ""
	}
	snippet := strings.TrimSpace(c.Description)
	if snippetText != "" {
		snippet = makeSnippet(snippetText, terms)
	}
	if snippetKind != "" {
		snippet = snippetKind + ": " + snippet
	}
	return score, snippet
}

func makeSnippet(text string, terms []string) string {
	lower := strings.ToLower(text)
	runes := []rune(text)
	idx := -1
	for _, term := range terms {
		byteIndex := strings.Index(lower, term)
		if byteIndex < 0 {
			continue
		}
		idx = len([]rune(lower[:byteIndex]))
		break
	}
	if idx < 0 {
		idx = 0
	}
	start := idx - snippetRunes/3
	if start < 0 {
		start = 0
	}
	end := start + snippetRunes
	if end > len(runes) {
		end = len(runes)
		if end-snippetRunes > 0 {
			start = end - snippetRunes
		}
	}
	clipped := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if start > 0 {
		clipped = "..." + clipped
	}
	if end < len(runes) {
		clipped += "..."
	}
	return clipped
}
