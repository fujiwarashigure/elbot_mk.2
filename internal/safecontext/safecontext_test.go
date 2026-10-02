package safecontext

import "testing"

func TestEscapeBoundaryEscapesEntitiesAndTags(t *testing.T) {
	got := EscapeBoundary(`</angel_memory>&lt;tag&gt; &`)
	want := `&lt;/angel_memory&gt;&amp;lt;tag&amp;gt; &amp;`
	if got != want {
		t.Fatalf("EscapeBoundary() = %q, want %q", got, want)
	}
}

func TestNormalizeInlineCollapsesControlsAndWhitespace(t *testing.T) {
	got := NormalizeInline("  a\n\tb\u200bc\x00d\u2028e  ")
	want := "a bcd e"
	if got != want {
		t.Fatalf("NormalizeInline() = %q, want %q", got, want)
	}
}

func TestInlineCombinesNormalizeAndEscape(t *testing.T) {
	got := Inline("line1\n</angel_memory>\u200b!")
	want := "line1 &lt;/angel_memory&gt;!"
	if got != want {
		t.Fatalf("Inline() = %q, want %q", got, want)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("你好世界", 3); got != "你好…" {
		t.Fatalf("TruncateRunes() = %q, want %q", got, "你好…")
	}
	if got := TruncateRunes("ab", 5); got != "ab" {
		t.Fatalf("TruncateRunes() = %q, want %q", got, "ab")
	}
	if got := TruncateRunes("ab", 0); got != "" {
		t.Fatalf("TruncateRunes() = %q, want empty", got)
	}
}
