package completion

import (
	"context"
	"strings"

	"elbot/internal/directive"
)

const KindCharacterDirective = "character_directive"

// CharacterOption is one selectable character for @char: completion.
type CharacterOption struct {
	ID          string
	Name        string
	Description string
}

type CharacterLister func(ctx context.Context) []CharacterOption

// CharacterDirectiveSource completes @char:<id> / @c:<id>.
type CharacterDirectiveSource struct {
	Characters CharacterLister
}

func (s CharacterDirectiveSource) Complete(ctx context.Context, req Request) []Item {
	if s.Characters == nil {
		return nil
	}
	cursor := req.CursorOrEnd()
	token := directive.ParseCharacterCompletionToken(req.Text, cursor)
	if !token.OK {
		return nil
	}
	if token.PrefixOnly {
		return []Item{{Text: token.Prefix, Label: token.Prefix, Kind: KindCharacterDirective, ReplaceStart: token.Start, ReplaceEnd: cursor}}
	}
	query := strings.ToLower(strings.TrimSpace(token.Query))
	out := []Item{}
	for _, option := range s.Characters(ctx) {
		if query != "" && !strings.Contains(strings.ToLower(option.ID), query) && !strings.Contains(strings.ToLower(option.Name), query) {
			continue
		}
		description := option.Name
		if option.Description != "" {
			description = option.Name + " · " + option.Description
		}
		out = append(out, Item{Text: token.Prefix + option.ID, Label: option.ID, Description: description, Kind: KindCharacterDirective, ReplaceStart: token.Start, ReplaceEnd: cursor})
	}
	return out
}
