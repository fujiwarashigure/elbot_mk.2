package builtin

import (
	"context"
	"strings"

	"elbot/internal/character"
	"elbot/internal/platform"
	"elbot/internal/storage"
)

const (
	imageContextMaxRunes = 2400
	imageContextMsgRunes = 200
)

// ImagePromptRewriteRequest is the input for the optional LLM rewritter.
type ImagePromptRewriteRequest struct {
	Scene           string
	CharacterName   string
	CharacterPrompt string
	ContextText     string
	LibraryHints    string
	MaxRunes        int
}

// ImagePromptRewriter rewrites a scene description into a stronger image prompt.
type ImagePromptRewriter interface {
	RewriteImagePrompt(ctx context.Context, req ImagePromptRewriteRequest) (string, error)
}

// resolveImageCharacter picks the character for this image request:
// explicit id > active @char:<id> > explicit query > name/alias found in the prompt.
func (t ImageGenerateTool) resolveImageCharacter(ctx context.Context, args imageGenerateArgs, prompt string, viewer character.Viewer, auto bool) (*character.Character, error) {
	if t.characters == nil || !t.characters.Enabled() {
		return nil, nil
	}
	id := strings.TrimSpace(args.CharacterID)
	if id != "" && !strings.EqualFold(id, "auto") {
		item, err := t.characters.GetVisible(ctx, id, viewer)
		if err != nil {
			return nil, imageCharacterError(id, err)
		}
		return item, nil
	}
	if active := character.ActiveIDs(ctx); len(active) > 0 {
		if item, err := t.characters.GetVisible(ctx, active[0], viewer); err == nil {
			return item, nil
		}
	}
	query := strings.TrimSpace(args.CharacterQuery)
	if !auto && !strings.EqualFold(id, "auto") && query == "" {
		return nil, nil
	}
	if item := t.matchCharacterByName(ctx, prompt, viewer); item != nil {
		return item, nil
	}
	if query == "" {
		query = prompt
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	results, err := t.characters.Search(ctx, query, "", "", 3, viewer)
	if err != nil || len(results) == 0 {
		return nil, nil
	}
	item, err := t.characters.GetVisible(ctx, results[0].ID, viewer)
	if err != nil {
		return nil, nil
	}
	return item, nil
}

// matchCharacterByName finds a visible character whose name or alias appears in
// the prompt. The longest match wins so short aliases do not shadow full names.
func (t ImageGenerateTool) matchCharacterByName(ctx context.Context, prompt string, viewer character.Viewer) *character.Character {
	items, err := t.characters.List(ctx, viewer)
	if err != nil {
		return nil
	}
	folded := strings.ToLower(prompt)
	var best *character.Character
	bestLen := 0
	for _, item := range items {
		names := append([]string{item.Name}, item.Aliases...)
		for _, name := range names {
			name = strings.ToLower(strings.TrimSpace(name))
			if len([]rune(name)) < 2 || !strings.Contains(folded, name) {
				continue
			}
			if length := len([]rune(name)); length > bestLen {
				best = item
				bestLen = length
			}
		}
	}
	return best
}

func imageCharacterError(id string, err error) error {
	switch err {
	case character.ErrNotFound:
		return &imageToolMessage{"没有找到角色 " + id + "，可先用 character_list 查询。"}
	case character.ErrForbidden:
		return &imageToolMessage{"角色 " + id + " 不可访问。"}
	default:
		return err
	}
}

func characterName(item *character.Character) string {
	if item == nil {
		return ""
	}
	return item.Name
}

func characterImagePrompt(item *character.Character) string {
	if item == nil {
		return ""
	}
	return item.ImagePrompt()
}

// imageToolMessage wraps a user-facing message as a normal error so callers can
// surface it without failing the tool call.
type imageToolMessage struct{ text string }

func (m *imageToolMessage) Error() string { return m.text }

// imageContextQuery decides whether to pull group chat context.
func (t ImageGenerateTool) imageContextQuery(args imageGenerateArgs, prompt string, auto bool) (string, bool) {
	raw := strings.TrimSpace(args.ContextQuery)
	if raw == "" {
		if !auto || !t.cfg.AutoContext || !looksLikeImageContext(prompt) {
			return "", false
		}
		raw = "auto"
	}
	switch strings.ToLower(raw) {
	case "off", "none", "false", "0":
		return "", false
	case "auto":
		return prompt, true
	default:
		return raw, true
	}
}

var imageContextTriggers = []string{
	"刚才", "上一条", "那条", "那张", "上一张", "之前", "上面", "群里", "大家", "聊天", "记录", "他说的", "她说的", "图里", "原图",
	"this image", "the previous", "earlier", "above", "group chat",
}

func looksLikeImageContext(prompt string) bool {
	folded := strings.ToLower(prompt)
	for _, trigger := range imageContextTriggers {
		if strings.Contains(folded, strings.ToLower(trigger)) {
			return true
		}
	}
	return false
}

// searchImageContext queries the current chat history.
func (t ImageGenerateTool) searchImageContext(ctx context.Context, query string, limit int) []storage.ChatMessage {
	if t.history == nil {
		return nil
	}
	msg, ok := platform.MessageContextFrom(ctx)
	if !ok || strings.TrimSpace(msg.Platform) == "" || strings.TrimSpace(msg.ScopeID) == "" {
		return nil
	}
	if limit <= 0 {
		limit = t.cfg.ContextDefaultLimit
	}
	rows, err := t.history.Search(ctx, storage.ChatHistorySearchRequest{
		Platform:        msg.Platform,
		PlatformScopeID: msg.ScopeID,
		QueryTerms:      splitImageContextTerms(query),
		QueryMode:       "or",
		Limit:           limit,
	})
	if err != nil {
		return nil
	}
	return rows
}

var imageContextStopTerms = map[string]bool{
	"刚才": true, "那张": true, "上一": true, "一条": true, "群里": true, "之前": true,
	"上面": true, "聊天": true, "记录": true, "大家": true, "说的": true, "图里": true,
	"原图": true, "这个": true, "那个": true, "一下": true, "帮我": true, "画一": true,
	"生成": true, "图片": true, "我这": true, "我们": true,
}

// splitImageContextTerms builds LIKE-friendly terms. Chinese queries are split
// into bigrams because the history search uses a plain substring match.
func splitImageContextTerms(query string) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(term string) {
		term = strings.ToLower(strings.TrimSpace(term))
		if len([]rune(term)) < 2 || seen[term] || imageContextStopTerms[term] {
			return
		}
		seen[term] = true
		out = append(out, term)
	}
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', '，', '。', '!', '！', '?', '？', ';', '；', '|', '、', '"', '\'', '(', ')', '（', '）':
			return true
		}
		return false
	})
	for _, field := range fields {
		runes := []rune(field)
		if !hasCJK(field) {
			add(field)
			continue
		}
		for i := 0; i+1 < len(runes); i++ {
			if !isCJKRune(runes[i]) || !isCJKRune(runes[i+1]) {
				continue
			}
			add(string(runes[i : i+2]))
			if len(out) >= 8 {
				return out
			}
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func hasCJK(text string) bool {
	for _, r := range text {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

func isCJKRune(r rune) bool {
	return (r >= 0x3400 && r <= 0x4DBF) || (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0xF900 && r <= 0xFAFF)
}

// formatImageContext renders retrieved messages into a compact block.
func formatImageContext(rows []storage.ChatMessage) string {
	if len(rows) == 0 {
		return ""
	}
	lines := []string{"[参考对话]"}
	total := 0
	for _, row := range rows {
		text := strings.Join(strings.Fields(strings.TrimSpace(row.Text)), " ")
		if text == "" {
			continue
		}
		runes := []rune(text)
		if len(runes) > imageContextMsgRunes {
			text = string(runes[:imageContextMsgRunes]) + "..."
		}
		name := strings.TrimSpace(row.SenderName)
		if name == "" {
			name = row.SenderID
		}
		line := "- " + row.CreatedAt.Format("01-02 15:04") + " " + name + ": " + text
		total += len([]rune(line))
		if total > imageContextMaxRunes {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// shouldRewriteImagePrompt reports whether the LLM rewrite pass should run.
func (t ImageGenerateTool) shouldRewriteImagePrompt(args imageGenerateArgs, prompt, contextText string) bool {
	mode := strings.ToLower(strings.TrimSpace(t.cfg.OptimizeRewrite))
	if args.Rewrite != nil {
		if *args.Rewrite {
			mode = "always"
		} else {
			mode = "off"
		}
	}
	switch mode {
	case "always":
		return true
	case "auto":
		return len([]rune(strings.TrimSpace(prompt))) < t.cfg.OptimizeRewriteMinRunes || strings.TrimSpace(contextText) != ""
	default:
		return false
	}
}
