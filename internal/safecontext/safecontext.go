// Package safecontext contains shared helpers for rendering untrusted text
// inside delimited prompt-context blocks.
package safecontext

import (
	"strings"
	"unicode"
)

// EscapeBoundary neutralizes characters that could forge or close a
// surrounding XML-ish boundary tag. Ampersand is escaped first so an input
// such as "&lt;/angel_memory&gt;" cannot survive as an entity that a model
// might decode back into a closing tag.
func EscapeBoundary(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(value)
}

// NormalizeInline folds untrusted text into one visible line. All Unicode
// whitespace runs become a single ASCII space, control/format characters are
// dropped, and zero-width/bidirectional invisibles are removed.
func NormalizeInline(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	pendingSpace := false
	for _, r := range value {
		if unicode.IsSpace(r) {
			pendingSpace = true
			continue
		}
		if isInvisible(r) {
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// Inline normalizes value to one line and escapes boundary characters.
func Inline(value string) string {
	return EscapeBoundary(NormalizeInline(value))
}

// TruncateRunes shortens value to at most max runes, adding an ellipsis when
// truncation occurs. max <= 0 returns an empty string.
func TruncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

func isInvisible(r rune) bool {
	if unicode.IsControl(r) {
		return true
	}
	switch r {
	case 0x200B, 0x200C, 0x200D, 0x2060, 0xFEFF:
		return true
	}
	return unicode.In(r, unicode.Cf, unicode.Co, unicode.Cs)
}
