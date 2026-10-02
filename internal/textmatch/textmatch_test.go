package textmatch

import (
	"reflect"
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
