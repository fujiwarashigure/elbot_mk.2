package agent

import (
	"context"
	"strings"

	"elbot/internal/hook"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/security"
	"elbot/internal/storage"
)

type messageWakeupContextKey struct{}

func withMessageWakeup(ctx context.Context, woken bool) context.Context {
	return context.WithValue(ctx, messageWakeupContextKey{}, woken)
}

func messageWakeupFromContext(ctx context.Context) (bool, bool) {
	woken, ok := ctx.Value(messageWakeupContextKey{}).(bool)
	return woken, ok
}

func (a *Agent) hookWakeup(ctx context.Context, event hook.Event) bool {
	if woken, ok := messageWakeupFromContext(ctx); ok {
		return woken
	}
	text := strings.TrimSpace(llm.SegmentsTextOnly(event.Message.Segments))
	if text == "" {
		text = inboundRawText(ctx)
	}
	return a.messageWakeup(ctx, text)
}

func (a *Agent) messageWakeup(ctx context.Context, text string) bool {
	msg, ok := platform.MessageContextFrom(ctx)
	if !ok {
		return true
	}
	if msg.ConversationKind == "" || msg.ConversationKind == platform.ConversationUnknown || msg.ConversationKind == platform.ConversationPrivate {
		return true
	}
	if a.commands != nil && a.commands.IsCommand(text) {
		return true
	}
	matchText := text
	if msg.MatchTextSet {
		matchText = msg.MatchText
	}
	policy := a.groupPolicyForScope(a.scope(ctx))
	keywords := append(append([]string(nil), msg.TriggerKeywords...), policy.WakeKeywordsValue()...)
	// Quiet hours suppress ordinary group chatter and @-mentions, but never
	// slash commands (handled above) or a superadmin's operational message.
	if a.groupQuietHoursActive(ctx) && a.actor(ctx).Role != security.RoleSuperadmin {
		return false
	}
	switch policy.ResponseModeValue() {
	case "all":
		return strings.TrimSpace(text) != "" || len(msg.Segments) > 0
	case "keyword":
		_, ok := platform.StripTriggerKeyword(matchText, keywords)
		return ok
	case "reply":
		return a.isReplyToBot(ctx, msg)
	case "off":
		return false
	}
	if _, ok := platform.StripTriggerKeyword(matchText, keywords); ok {
		return true
	}
	if mentionsBot(msg) {
		return true
	}
	return a.isReplyToBot(ctx, msg)
}

func (a *Agent) stripWakeupPrefix(ctx context.Context, text string) string {
	if msg, ok := platform.MessageContextFrom(ctx); ok {
		keywords := append(append([]string(nil), msg.TriggerKeywords...), a.groupPolicyForScope(a.scope(ctx)).WakeKeywordsValue()...)
		msg.TriggerKeywords = keywords
		return stripWakeupPrefixFromText(text, msg, a.commandPrefixes())
	}
	return text
}

func stripWakeupPrefixFromText(text string, msg platform.MessageContext, prefixes []string) string {
	if stripped, ok := platform.StripTriggerKeyword(text, msg.TriggerKeywords); ok {
		return stripped
	}
	if before, after, ok := strings.Cut(text, "\n\n"); ok {
		stripped := stripWakeupPrefixFromText(after, msg, prefixes)
		if stripped != after {
			return before + "\n\n" + stripped
		}
	}
	return stripBotMention(text, msg, prefixes)
}

func inboundRawText(ctx context.Context) string {
	if msg, ok := platform.MessageContextFrom(ctx); ok {
		return strings.TrimSpace(msg.RawText)
	}
	return ""
}

func mentionsBot(msg platform.MessageContext) bool {
	botUserID := strings.TrimSpace(msg.Bot.UserID)
	botUsername := strings.TrimPrefix(strings.TrimSpace(msg.Bot.Username), "@")
	for _, mention := range msg.Mentions {
		if botUserID != "" && strings.TrimSpace(mention.UserID) == botUserID {
			return true
		}
		if botUsername != "" && strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(mention.Username), "@"), botUsername) {
			return true
		}
	}
	return false
}

func stripBotMention(text string, msg platform.MessageContext, prefixes []string) string {
	botUsername := strings.TrimPrefix(strings.TrimSpace(msg.Bot.Username), "@")
	if botUsername == "" || text == "" {
		return text
	}
	// Keep the original whitespace/newlines. Only the mention token itself is
	// removed, so code blocks and pasted logs are not reflowed by wakeup
	// stripping.
	if start, end, ok := firstTokenBounds(text); ok {
		token := text[start:end]
		if name, mention, found := strings.Cut(token, "@"); found && strings.EqualFold(mention, botUsername) && startsWithCommandPrefix(name, prefixes) {
			return text[:start] + name + text[end:]
		}
	}
	needle := "@" + botUsername
	lowerNeedle := strings.ToLower(needle)
	searchFrom := 0
	for searchFrom <= len(text) {
		lowerText := strings.ToLower(text)
		if searchFrom > len(lowerText) {
			break
		}
		rel := strings.Index(lowerText[searchFrom:], lowerNeedle)
		if rel < 0 {
			break
		}
		idx := searchFrom + rel
		beforeOK := idx == 0 || isSpaceByte(text[idx-1])
		end := idx + len(needle)
		afterOK := end >= len(text) || isSpaceByte(text[end])
		if !beforeOK || !afterOK {
			// The textual match is part of a larger token (for example an
			// email or URL). Skip it without rewriting the token.
			searchFrom = idx + 1
			continue
		}
		text = text[:idx] + text[end:]
		searchFrom = idx
	}
	return strings.TrimSpace(text)
}

func firstTokenBounds(text string) (int, int, bool) {
	start := 0
	for start < len(text) && isSpaceByte(text[start]) {
		start++
	}
	if start >= len(text) {
		return 0, 0, false
	}
	end := start
	for end < len(text) && !isSpaceByte(text[end]) {
		end++
	}
	return start, end, true
}

func isSpaceByte(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func startsWithCommandPrefix(token string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

func (a *Agent) isReplyToBot(ctx context.Context, msg platform.MessageContext) bool {
	if strings.TrimSpace(msg.ReplyToSenderID) != "" && strings.TrimSpace(msg.ReplyToSenderID) == strings.TrimSpace(msg.Bot.UserID) {
		return true
	}
	replyID := strings.TrimSpace(msg.ReplyToMessageID)
	if replyID == "" || a.store == nil || a.store.Messages() == nil {
		return false
	}
	mapped, err := a.store.Messages().FindByPlatformMessage(ctx, msg.Platform, msg.ScopeID, replyID)
	if err == nil && mapped.Role == storage.RoleAssistant {
		return true
	}
	if a.store.Media() != nil {
		outputs, err := a.store.Media().FindOutputs(ctx, msg.Platform, msg.ScopeID, replyID, storage.Now())
		return err == nil && len(outputs) > 0
	}
	return false
}
