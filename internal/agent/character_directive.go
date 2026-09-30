package agent

import (
	"context"
	"strings"

	"elbot/internal/character"
	"elbot/internal/directive"
	"elbot/internal/security"
)

const characterDirectivePromptRunes = 12000

type characterDirectiveResult struct {
	Text    string
	Applied []string
	Invalid []string
}

// applyCharacterDirectives resolves @char:<id> / @c:<id> into a one-turn persona
// block prefixed to the current user message. It never persists session state.
func (a *Agent) applyCharacterDirectives(ctx context.Context, text string) characterDirectiveResult {
	result := characterDirectiveResult{Text: text}
	if a.characters == nil || !a.characters.Enabled() {
		return result
	}
	if !containsAny(text, directive.CharacterPrefix, directive.CharacterFullPrefix, directive.CharacterShortPrefix, directive.CharacterShortFull) {
		return result
	}
	matches := directive.CharacterMatches(text)
	if len(matches) == 0 {
		return result
	}
	viewer := a.characterViewer(ctx)
	remove := make([]bool, len(matches))
	blocks := make([]string, 0, len(matches))
	for i, match := range matches {
		id := strings.TrimSpace(match.Name)
		item, err := a.characters.GetVisible(ctx, id, viewer)
		if err != nil {
			result.Invalid = append(result.Invalid, id)
			continue
		}
		blocks = append(blocks, item.Prompt(characterDirectivePromptRunes))
		result.Applied = append(result.Applied, item.ID)
		remove[i] = true
	}
	if len(result.Applied) == 0 {
		return result
	}
	stripped := strings.TrimSpace(directive.StripToolMatches(text, matches, remove))
	block := strings.Join(blocks, "\n\n")
	if stripped == "" {
		result.Text = block
	} else {
		result.Text = block + "\n\n" + stripped
	}
	return result
}

func (a *Agent) characterViewer(ctx context.Context) character.Viewer {
	actor := a.actor(ctx)
	return character.Viewer{Platform: actor.Platform, ActorID: actor.ID, Superadmin: actor.Role == security.RoleSuperadmin}
}

func (a *Agent) notifyCharacterDirectiveResult(ctx context.Context, result characterDirectiveResult) {
	parts := []string{}
	if len(result.Applied) > 0 {
		parts = append(parts, "已启用角色："+strings.Join(sortedUnique(result.Applied), ", "))
	}
	if len(result.Invalid) > 0 {
		parts = append(parts, "未找到或不可用的角色："+strings.Join(sortedUnique(result.Invalid), ", "))
	}
	if len(parts) == 0 {
		return
	}
	a.sendChat(ctx, strings.Join(parts, "\n"))
}
