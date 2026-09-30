package agent

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"elbot/internal/config"
	"elbot/internal/imagegen"
	"elbot/internal/llm"
	"elbot/internal/security"
	"elbot/internal/tool"
)

type turnModelOverrideKey struct{}
type turnToolProfileKey struct{}

func withTurnModelOverride(ctx context.Context, selection config.ModelSelection) context.Context {
	return context.WithValue(ctx, turnModelOverrideKey{}, selection)
}

func turnModelOverride(ctx context.Context) (config.ModelSelection, bool) {
	selection, ok := ctx.Value(turnModelOverrideKey{}).(config.ModelSelection)
	return selection, ok
}

func withTurnToolProfile(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, turnToolProfileKey{}, name)
}

func turnToolProfile(ctx context.Context) string {
	name, _ := ctx.Value(turnToolProfileKey{}).(string)
	return strings.TrimSpace(name)
}

type turnDirectiveResult struct {
	Text    string
	Applied []string
	Invalid []string
	Denied  []string
}

type turnDirectiveMatch struct {
	Kind  string
	Name  string
	Start int
	End   int
}

// applyTurnOverrides resolves model/image/tool profile declarations for the
// current turn only. They are stripped from the message and never persisted.
func (a *Agent) applyTurnOverrides(ctx context.Context, text string) (context.Context, turnDirectiveResult) {
	result := turnDirectiveResult{Text: text}
	if a == nil || text == "" {
		return ctx, result
	}
	matches := parseTurnDirectives(text, a.turnDirectives.Normalized())
	if len(matches) == 0 {
		return ctx, result
	}
	actor := a.actor(ctx)
	if actor.Role != security.RoleSuperadmin {
		for _, match := range matches {
			result.Denied = append(result.Denied, match.Kind+":"+match.Name)
		}
		result.Text = stripTurnDirectives(text, matches, nil)
		return ctx, result
	}

	remove := make([]bool, len(matches))
	for i, match := range matches {
		switch match.Kind {
		case "model":
			name := resolveTurnAlias(a.modelAliases, match.Name)
			selection, ok := a.modelProfiles[name]
			if !ok || a.modelRuntime.clients[selection.Provider] == nil {
				result.Invalid = append(result.Invalid, "model:"+match.Name)
				continue
			}
			ctx = withTurnModelOverride(ctx, selection)
			result.Applied = append(result.Applied, "model:"+name)
			remove[i] = true
		case "image":
			name := resolveTurnAlias(a.imageAliases, match.Name)
			if !a.imageProfiles[name] {
				result.Invalid = append(result.Invalid, "image:"+match.Name)
				continue
			}
			ctx = imagegen.WithProfile(ctx, name)
			result.Applied = append(result.Applied, "image:"+name)
			remove[i] = true
		case "tool":
			name := resolveTurnAlias(a.toolAliases, match.Name)
			if len(a.toolProfiles[name]) == 0 {
				result.Invalid = append(result.Invalid, "use:"+match.Name)
				continue
			}
			ctx = withTurnToolProfile(ctx, name)
			result.Applied = append(result.Applied, "use:"+name)
			remove[i] = true
		}
	}
	result.Text = stripTurnDirectives(text, matches, remove)
	return ctx, result
}

func (a *Agent) notifyTurnOverrideResult(ctx context.Context, result turnDirectiveResult) {
	parts := []string{}
	if len(result.Applied) > 0 {
		parts = append(parts, "本轮已启用："+strings.Join(sortedUnique(result.Applied), ", ")+"（仅本轮有效，下条消息需重新声明）")
	}
	if len(result.Invalid) > 0 {
		parts = append(parts, "未配置的 profile："+strings.Join(sortedUnique(result.Invalid), ", "))
	}
	if len(result.Denied) > 0 {
		parts = append(parts, "仅超级管理员可以声明："+strings.Join(sortedUnique(result.Denied), ", "))
	}
	if len(parts) == 0 {
		return
	}
	a.sendChat(ctx, strings.Join(parts, "\n"))
}

// turnToolSchemas resolves the @use: profile into tool schemas for this turn.
func (a *Agent) turnToolSchemas(ctx context.Context, name string) []llm.ToolSchema {
	names := a.toolProfiles[name]
	if len(names) == 0 || a.toolRuntime.registry == nil {
		return nil
	}
	actor := a.actor(ctx)
	policy := a.securityPolicy
	out := make([]llm.ToolSchema, 0, len(names))
	for _, toolName := range names {
		entry, ok := a.toolRuntime.registry.Get(toolName)
		if !ok {
			continue
		}
		info := entry.Info()
		if !tool.InfoAvailableInContext(ctx, info) || !tool.CanAccessTool(actor, policy, info) {
			continue
		}
		out = append(out, entry.Schema())
	}
	return out
}

type turnKeyword struct {
	text string
	kind string
}

// parseTurnDirectives scans text for <prefix><keyword><separator><name> using
// the configured prefixes and keywords (ASCII and Chinese both supported).
func parseTurnDirectives(text string, cfg config.TurnDirectivesConfig) []turnDirectiveMatch {
	prefixes := make([]string, 0, len(cfg.Prefixes))
	for _, prefix := range cfg.Prefixes {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	if len(prefixes) == 0 {
		return nil
	}
	sort.SliceStable(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })

	keywords := make([]turnKeyword, 0, 12)
	for _, keyword := range cfg.ModelKeywords {
		keywords = append(keywords, turnKeyword{text: strings.TrimSpace(keyword), kind: "model"})
	}
	for _, keyword := range cfg.ImageKeywords {
		keywords = append(keywords, turnKeyword{text: strings.TrimSpace(keyword), kind: "image"})
	}
	for _, keyword := range cfg.ToolKeywords {
		keywords = append(keywords, turnKeyword{text: strings.TrimSpace(keyword), kind: "tool"})
	}
	filtered := make([]turnKeyword, 0, len(keywords))
	for _, keyword := range keywords {
		if keyword.text != "" {
			filtered = append(filtered, keyword)
		}
	}
	keywords = filtered
	sort.SliceStable(keywords, func(i, j int) bool { return len(keywords[i].text) > len(keywords[j].text) })

	out := []turnDirectiveMatch{}
	for i := 0; i < len(text); {
		matched := false
		for _, prefix := range prefixes {
			if !strings.HasPrefix(text[i:], prefix) {
				continue
			}
			rest := text[i+len(prefix):]
			lowerRest := strings.ToLower(rest)
			for _, keyword := range keywords {
				if !strings.HasPrefix(lowerRest, strings.ToLower(keyword.text)) {
					continue
				}
				after := rest[len(keyword.text):]
				separator := turnSeparatorLen(after)
				if separator == 0 {
					continue
				}
				name, nameLen := readTurnName(after[separator:])
				if nameLen == 0 {
					continue
				}
				end := i + len(prefix) + len(keyword.text) + separator + nameLen
				out = append(out, turnDirectiveMatch{Kind: keyword.kind, Name: name, Start: i, End: end})
				i = end
				matched = true
				break
			}
			if matched {
				break
			}
		}
		if matched {
			continue
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		if size <= 0 {
			size = 1
		}
		i += size
	}
	return out
}

func turnSeparatorLen(text string) int {
	if text == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(text)
	if r != ':' && r != '：' {
		return 0
	}
	return size
}

func readTurnName(text string) (string, int) {
	end := 0
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if !isTurnNameRune(r) {
			break
		}
		end += size
	}
	if end == 0 {
		return "", 0
	}
	return text[:end], end
}

func isTurnNameRune(r rune) bool {
	if r == '_' || r == '-' || r == '.' {
		return true
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func stripTurnDirectives(text string, matches []turnDirectiveMatch, remove []bool) string {
	var b strings.Builder
	last := 0
	for i, match := range matches {
		drop := true
		if remove != nil {
			if i >= len(remove) {
				break
			}
			drop = remove[i]
		}
		if !drop {
			continue
		}
		b.WriteString(text[last:match.Start])
		last = match.End
	}
	b.WriteString(text[last:])
	return strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
}

func resolveTurnAlias(aliases map[string]string, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if name, ok := aliases[strings.ToLower(raw)]; ok {
		return name
	}
	return raw
}
