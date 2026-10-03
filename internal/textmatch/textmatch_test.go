package textmatch

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestKeywordsExtractsCJKNGramsAndASCIIWords(t *testing.T) {
	got := Keywords("还记得我喜欢吃什么吗 neo4j")
	for _, want := range []string{"喜欢", "喜欢吃", "neo4j"} {
		if !contains(got, want) {
			t.Fatalf("Keywords() = %#v, missing %q", got, want)
		}
	}
	if len(got) > maxKeywords {
		t.Fatalf("Keywords() returned %d terms, want <= %d", len(got), maxKeywords)
	}
}

func TestKeywordsFallsBackToSingleHanCharacter(t *testing.T) {
	got := Keywords("猫")
	if len(got) != 1 || got[0] != "猫" {
		t.Fatalf("Keywords(\"猫\") = %#v, want [猫]", got)
	}
}

func TestKeywordsDeduplicatesAndBounds(t *testing.T) {
	got := Keywords("喵喵喵喵喵喵喵喵喵喵 word word WORD")
	seen := map[string]bool{}
	for _, term := range got {
		key := term
		if seen[key] {
			t.Fatalf("duplicate keyword %q in %#v", term, got)
		}
		seen[key] = true
	}
	if len(got) > maxKeywords {
		t.Fatalf("Keywords() returned %d terms, want <= %d", len(got), maxKeywords)
	}
}

func TestKeywordsKeepsEntityAtTailOfLongInput(t *testing.T) {
	value := strings.Repeat("无关填充", 400) + "关键实体在句尾"
	got := Keywords(value)
	if !contains(got, "关键") {
		t.Fatalf("Keywords() = %#v, tail entity was dropped", got)
	}
}

func TestKeywordsDoesNotLetASCIIStarveCJK(t *testing.T) {
	words := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		words = append(words, fmt.Sprintf("word%02d", i))
	}
	value := strings.Join(words, " ") + " 关键实体在句尾"
	got := Keywords(value)
	if !contains(got, "关键") {
		t.Fatalf("Keywords() = %#v, CJK term was crowded out by ASCII words", got)
	}
	if !contains(got, "word00") {
		t.Fatalf("Keywords() = %#v, ASCII word missing", got)
	}
	if len(got) > maxKeywords {
		t.Fatalf("Keywords() returned %d terms, want <= %d", len(got), maxKeywords)
	}
}

func TestKeywordsBoundsVeryLongInput(t *testing.T) {
	value := strings.Repeat("很长很长的输入文本", 20000)
	got := Keywords(value)
	if len(got) == 0 || len(got) > maxKeywords {
		t.Fatalf("Keywords() returned %d terms, want 1..%d", len(got), maxKeywords)
	}
}

func BenchmarkKeywordsLongInput(b *testing.B) {
	value := strings.Repeat("这是一个用于压测的较长句子，包含中文和 english words 以及标点。", 40)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Keywords(value)
	}
}

func TestLikePatternEscapesWildcards(t *testing.T) {
	if got := LikePattern(`a%b_c\d`); got != `%a\%b\_c\\d%` {
		t.Fatalf("LikePattern() = %q", got)
	}
	if got := LikePattern(""); got != "%" {
		t.Fatalf("LikePattern(empty) = %q", got)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if reflect.DeepEqual(value, want) {
			return true
		}
	}
	return false
}
