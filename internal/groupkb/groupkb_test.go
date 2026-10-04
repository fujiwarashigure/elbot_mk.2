package groupkb

import (
	"testing"

	"elbot/internal/config"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  怎么绑定？ ":      "怎么绑定",
		"Hello, WORLD!": "hello world",
		"ＡＢＣ　１２３":       "abc 123",
		"a   b\n\tc":    "a b c",
		"（测试）":          "测试",
	}
	for input, want := range cases {
		if got := Normalize(input); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMatchExactAndAliases(t *testing.T) {
	entries := []config.GroupKnowledgeEntry{
		{ID: "k1", Question: "怎么绑定？", Answer: "在设置里绑定", Aliases: []string{"如何绑定", "绑定方法"}},
	}
	for _, text := range []string{"怎么绑定", " 如何绑定 ", "绑定方法！"} {
		entry, ok := Match(entries, text, 100)
		if !ok || entry.ID != "k1" {
			t.Fatalf("Match(%q) = %#v, %v", text, entry, ok)
		}
	}
	if _, ok := Match(entries, "怎么绑定账号", 100); ok {
		t.Fatal("exact match must not match extra text")
	}
}

func TestMatchContainsAndKeywords(t *testing.T) {
	entries := []config.GroupKnowledgeEntry{
		{ID: "contains", Question: "绑定", Answer: "A", Match: MatchContains},
		{ID: "keywords", Question: "退款", Answer: "B", Match: MatchKeywords, Keywords: []string{"退款", "流程"}},
	}
	if entry, ok := Match(entries, "这个绑定怎么操作？", 100); !ok || entry.ID != "contains" {
		t.Fatalf("contains = %#v, %v", entry, ok)
	}
	if _, ok := Match(entries, "我想退款", 100); ok {
		t.Fatal("keywords must require every keyword")
	}
	if entry, ok := Match(entries, "退款流程是什么？", 100); !ok || entry.ID != "keywords" {
		t.Fatalf("keywords = %#v, %v", entry, ok)
	}
}

func TestMatchSkipsDisabledAndBoundsScan(t *testing.T) {
	disabled := false
	entries := []config.GroupKnowledgeEntry{
		{ID: "off", Question: "绑定", Answer: "A", Enabled: &disabled},
		{ID: "on", Question: "绑定", Answer: "B"},
	}
	if entry, ok := Match(entries, "绑定", 100); !ok || entry.ID != "on" {
		t.Fatalf("disabled entry was not skipped: %#v, %v", entry, ok)
	}
	if _, ok := Match(entries, "前缀绑定", 1); ok {
		t.Fatal("match must not scan past maxScanRunes")
	}
}
