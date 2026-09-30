package imagegen

import (
	"sort"
	"strings"
)

// LibraryMatch is one search hit from the embedded prompt library.
type LibraryMatch struct {
	Scope   string
	Title   string
	Detail  string
	Anchors []string
	Terms   []string
	Score   int
}

// LibraryScopes lists the supported search scopes.
var LibraryScopes = []string{"entry", "keyword", "negative", "preset", "tip"}

// Search looks up the embedded library by free text. Empty scopes means all.
func (l *PromptLibrary) Search(query string, scopes []string, limit int) []LibraryMatch {
	if l == nil {
		return nil
	}
	tokens := splitTagWords(query)
	if len(tokens) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	allowed := map[string]bool{}
	if len(scopes) == 0 {
		for _, scope := range LibraryScopes {
			allowed[scope] = true
		}
	} else {
		for _, scope := range scopes {
			scope = strings.ToLower(strings.TrimSpace(scope))
			if scope != "" {
				allowed[scope] = true
			}
		}
	}

	score := func(text string) int {
		lower := strings.ToLower(text)
		total := 0
		for _, token := range tokens {
			if strings.Contains(lower, token) {
				total += len([]rune(token))
			}
		}
		return total
	}

	out := []LibraryMatch{}
	if allowed["entry"] {
		for _, entry := range l.Entries {
			text := strings.Join([]string{entry.Category, entry.Scene, entry.Description, entry.Prompt}, " ")
			if value := score(text); value > 0 {
				out = append(out, LibraryMatch{
					Scope:   "entry",
					Title:   entry.Category + " / " + entry.Scene,
					Detail:  entry.Prompt,
					Anchors: entry.Anchors,
					Terms:   entry.Terms,
					Score:   value,
				})
			}
		}
	}
	if allowed["keyword"] {
		for name, items := range l.Keywords {
			joined := strings.Join(items, ", ")
			if value := score(name + " " + joined); value > 0 {
				out = append(out, LibraryMatch{Scope: "keyword", Title: name, Detail: joined, Score: value})
			}
		}
	}
	if allowed["negative"] {
		for _, group := range l.Negatives {
			joined := strings.Join(group.Items, ", ")
			if value := score(group.Category + " " + joined); value > 0 {
				out = append(out, LibraryMatch{Scope: "negative", Title: group.Category, Detail: joined, Score: value})
			}
		}
	}
	if allowed["preset"] {
		for _, preset := range l.Presets {
			text := strings.Join([]string{preset.Use, preset.Ratio, preset.Scene, preset.Supplement}, " ")
			if value := score(text); value > 0 {
				out = append(out, LibraryMatch{Scope: "preset", Title: preset.Use, Detail: preset.Ratio + " | " + preset.Supplement, Score: value})
			}
		}
	}
	if allowed["tip"] {
		for _, tip := range l.Tips {
			text := strings.Join([]string{tip.Module, tip.Title, tip.Detail, tip.Example}, " ")
			if value := score(text); value > 0 {
				out = append(out, LibraryMatch{Scope: "tip", Title: tip.Module + " / " + tip.Title, Detail: tip.Detail + " | " + tip.Example, Score: value})
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Title < out[j].Title
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// LibraryHints renders compact hints for the LLM rewriter.
func (l *PromptLibrary) LibraryHints(scene string, limit int) string {
	matches := l.Search(scene, nil, limit)
	if len(matches) == 0 {
		return ""
	}
	lines := []string{}
	for _, match := range matches {
		line := "- [" + match.Scope + "] " + match.Title
		switch match.Scope {
		case "entry":
			if len(match.Anchors) > 0 {
				line += " → anchors: " + strings.Join(match.Anchors, ", ")
			} else {
				line += " → " + match.Detail
			}
		case "keyword", "negative", "preset":
			line += " → " + match.Detail
		case "tip":
			line += " → " + match.Detail
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
