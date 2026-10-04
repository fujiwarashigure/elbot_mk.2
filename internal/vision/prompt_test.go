package vision

import (
	"strings"
	"testing"
)

func TestCleanTextStripsCodeFence(t *testing.T) {
	for input, want := range map[string]string{
		"plain prompt":            "plain prompt",
		"```\nfenced prompt\n```": "fenced prompt",
		"  ```prompt```  ":        "prompt",
		"":                        "",
	} {
		if got := CleanText(input); got != want {
			t.Fatalf("CleanText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestImagePromptTemplateFollowsTargetAndLanguage(t *testing.T) {
	zh := ImagePrompt("general", "zh")
	if zh.Version != ImagePromptTemplateVersion {
		t.Fatalf("version = %q", zh.Version)
	}
	if !strings.Contains(zh.User, "general") || !strings.Contains(zh.User, "中文") {
		t.Fatalf("general/zh user text = %q", zh.User)
	}
	sdxl := ImagePrompt("sdxl", "en")
	if !strings.Contains(sdxl.User, "SDXL") || !strings.Contains(sdxl.User, "English") {
		t.Fatalf("sdxl/en user text = %q", sdxl.User)
	}
	flux := ImagePrompt("flux", "zh")
	if !strings.Contains(flux.User, "Flux") {
		t.Fatalf("flux user text = %q", flux.User)
	}
	if sdxl.User == zh.User || flux.User == zh.User {
		t.Fatal("targets must produce distinct instructions")
	}
	if sdxl.System != zh.System {
		t.Fatal("system prompt must not depend on the target")
	}
}
