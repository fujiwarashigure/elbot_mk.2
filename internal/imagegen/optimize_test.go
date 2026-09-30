package imagegen

import (
	"strings"
	"testing"
)

func mustLibrary(t *testing.T) *PromptLibrary {
	t.Helper()
	library, err := PromptLib()
	if err != nil {
		t.Fatalf("PromptLib: %v", err)
	}
	return library
}

func TestOptimizeAvatarUseCase(t *testing.T) {
	library := mustLibrary(t)
	result := library.Optimize("给猫娘画一个头像", OptimizeOptions{})
	if !strings.HasPrefix(result.Prompt, "给猫娘画一个头像") {
		t.Fatalf("original prompt must stay first: %q", result.Prompt)
	}
	if !strings.Contains(result.Matched, "use=头像") {
		t.Fatalf("matched = %q", result.Matched)
	}
	for _, want := range []string{"1:1", "Square avatar"} {
		if !strings.Contains(result.Prompt, want) {
			t.Fatalf("prompt %q missing %q", result.Prompt, want)
		}
	}
	for _, want := range []string{"low quality", "extra fingers"} {
		if !strings.Contains(result.Negative, want) {
			t.Fatalf("negative %q missing %q", result.Negative, want)
		}
	}
}

func TestOptimizeKeepsExistingRatio(t *testing.T) {
	library := mustLibrary(t)
	result := library.Optimize("赛博朋克城市夜景，16:9", OptimizeOptions{})
	if strings.Contains(result.Prompt, "widescreen composition") {
		t.Fatalf("existing aspect ratio should not be duplicated: %q", result.Prompt)
	}
	if !strings.Contains(result.Prompt, "neon signs") {
		t.Fatalf("expected cyberpunk anchors: %q", result.Prompt)
	}
}

func TestOptimizePosterUsesPrintPreset(t *testing.T) {
	library := mustLibrary(t)
	result := library.Optimize("一张海报，标题是 SUMMER", OptimizeOptions{})
	if !strings.Contains(result.Matched, "印刷海报") {
		t.Fatalf("matched = %q", result.Matched)
	}
	if !strings.Contains(result.Prompt, "Print-ready") {
		t.Fatalf("prompt = %q", result.Prompt)
	}
	if !strings.Contains(result.Negative, "watermark") {
		t.Fatalf("negative = %q", result.Negative)
	}
}

func TestOptimizeEditGuard(t *testing.T) {
	library := mustLibrary(t)
	result := library.Optimize("局部修改：把背景换成沙滩", OptimizeOptions{})
	if !strings.Contains(result.Prompt, "do not redraw the whole image") {
		t.Fatalf("edit guard missing: %q", result.Prompt)
	}
}

func TestOptimizeTruncatesAddedText(t *testing.T) {
	library := mustLibrary(t)
	result := library.Optimize("水彩风格的森林小路", OptimizeOptions{MaxAddedRunes: 20})
	if !strings.HasPrefix(result.Prompt, "水彩风格的森林小路") {
		t.Fatalf("prompt = %q", result.Prompt)
	}
	added := strings.TrimPrefix(result.Prompt, "水彩风格的森林小路")
	if len([]rune(strings.TrimSpace(added))) > 20 {
		t.Fatalf("added text not truncated: %q", added)
	}
}

func TestOptimizeEmptyInput(t *testing.T) {
	library := mustLibrary(t)
	if result := library.Optimize("   ", OptimizeOptions{}); result.Prompt != "" || result.Negative != "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestLibrarySplitsTagsToWords(t *testing.T) {
	library := mustLibrary(t)
	for name, items := range library.Keywords {
		for _, item := range items {
			if strings.Contains(item, ",") {
				t.Fatalf("keyword bank %q still has an unsplit item: %q", name, item)
			}
		}
	}
	for _, group := range library.Negatives {
		for _, item := range group.Items {
			if strings.Contains(item, ",") {
				t.Fatalf("negative group %q still has an unsplit item: %q", group.Category, item)
			}
		}
	}
	for _, entry := range library.Entries {
		for _, term := range entry.Terms {
			if strings.ContainsAny(term, " ,") {
				t.Fatalf("entry %q term is not a single word: %q", entry.Scene, term)
			}
		}
	}
	if len(library.KeywordTerms) == 0 || len(library.Vocabulary) < 100 {
		t.Fatalf("word-level index missing: keyword_terms=%d vocabulary=%d", len(library.KeywordTerms), len(library.Vocabulary))
	}
}

func TestOptimizeMatchesEnglishWordLevel(t *testing.T) {
	library := mustLibrary(t)
	result := library.Optimize("a cyberpunk street at night", OptimizeOptions{})
	if !strings.Contains(result.Matched, "赛博朋克城市") {
		t.Fatalf("matched = %q", result.Matched)
	}
	if !strings.Contains(result.Prompt, "neon signs") {
		t.Fatalf("prompt = %q", result.Prompt)
	}
}

func TestOptimizeTagMode(t *testing.T) {
	library := mustLibrary(t)
	result := library.Optimize("a cyberpunk street at night", OptimizeOptions{TermMode: "tag", MaxTags: 8})
	if !strings.HasPrefix(result.Prompt, "a cyberpunk street at night") {
		t.Fatalf("prompt = %q", result.Prompt)
	}
	if len(result.Tags) == 0 || len(result.Tags) > 8 {
		t.Fatalf("tags = %#v", result.Tags)
	}
	for _, tag := range result.Tags {
		if strings.ContainsAny(tag, " ,") {
			t.Fatalf("tag is not atomic: %q", tag)
		}
	}
	for _, want := range []string{"neon", "ultra-detailed"} {
		if !strings.Contains(result.Prompt, want) {
			t.Fatalf("prompt %q missing tag %q", result.Prompt, want)
		}
	}

	avatar := library.Optimize("给猫娘画一个头像", OptimizeOptions{TermMode: "tag", MaxTags: 6})
	if !strings.Contains(strings.Join(avatar.Tags, ","), "1:1") {
		t.Fatalf("ratio tag missing: %#v", avatar.Tags)
	}

	edited := library.Optimize("局部修改：把背景换成沙滩", OptimizeOptions{TermMode: "tag"})
	if !strings.Contains(edited.Prompt, "do not redraw the whole image") {
		t.Fatalf("edit guard missing: %q", edited.Prompt)
	}
	if len(edited.Tags) != 0 {
		t.Fatalf("edit-only request should not add random tags: %#v", edited.Tags)
	}
}
