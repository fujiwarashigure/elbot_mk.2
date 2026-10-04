package vision

import "testing"

func TestCacheIdentityKeyIsStableAndVersioned(t *testing.T) {
	base := CacheIdentity{
		SchemaVersion:     CacheSchemaVersion,
		MediaID:           "media:abc",
		Provider:          "openai",
		Endpoint:          "https://example.test/v1",
		Model:             "gpt-4o-mini",
		PromptVersion:     "image-to-prompt.v1",
		PromptHash:        "deadbeef",
		PreprocessVersion: PreprocessVersion,
		RequestHash:       "cafebabe",
		CredentialEpoch:   7,
	}
	key, err := base.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if len(key) != 64 {
		t.Fatalf("key length = %d, want 64 hex chars", len(key))
	}
	again, err := base.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if key != again {
		t.Fatal("the same identity must produce the same key")
	}

	changes := map[string]func(*CacheIdentity){
		"schema":      func(id *CacheIdentity) { id.SchemaVersion++ },
		"media":       func(id *CacheIdentity) { id.MediaID = "media:def" },
		"provider":    func(id *CacheIdentity) { id.Provider = "other" },
		"endpoint":    func(id *CacheIdentity) { id.Endpoint = "https://other.test/v1" },
		"model":       func(id *CacheIdentity) { id.Model = "other" },
		"prompt":      func(id *CacheIdentity) { id.PromptVersion = "image-to-prompt.v2" },
		"prompt_hash": func(id *CacheIdentity) { id.PromptHash = "other" },
		"preprocess":  func(id *CacheIdentity) { id.PreprocessVersion = "vision-image-v2" },
		"request":     func(id *CacheIdentity) { id.RequestHash = "other" },
		"credential":  func(id *CacheIdentity) { id.CredentialEpoch++ },
	}
	for name, mutate := range changes {
		variant := base
		mutate(&variant)
		variantKey, err := variant.Key()
		if err != nil {
			t.Fatalf("%s: Key: %v", name, err)
		}
		if variantKey == key {
			t.Fatalf("%s: change did not affect the cache key", name)
		}
	}
}

func TestPromptHashTracksContent(t *testing.T) {
	if hashText("a\x00b") == hashText("ab") {
		t.Fatal("the separator must keep prompt fields distinct")
	}
	if hashText("same") != hashText("same") {
		t.Fatal("hashText must be deterministic")
	}
	if hashText("one") == hashText("two") {
		t.Fatal("different text must hash differently")
	}
}

func TestImageAndChatPromptsAreDistinct(t *testing.T) {
	image := ImagePrompt("general", "zh")
	chat := ChatDescription("zh")
	if image.Version == chat.Version {
		t.Fatal("drawing-prompt and chat-description templates must not share a version")
	}
	if image.System == chat.System {
		t.Fatal("templates must not share the same system prompt")
	}
}

func TestNormalizeEnums(t *testing.T) {
	if got := NormalizeImagePromptTarget(" SDXL "); got != "sdxl" {
		t.Fatalf("target = %q", got)
	}
	if got := NormalizeImagePromptTarget("unknown"); got != "general" {
		t.Fatalf("target = %q", got)
	}
	if got := NormalizeImagePromptLanguage("EN"); got != "en" {
		t.Fatalf("language = %q", got)
	}
	if got := NormalizeImagePromptLanguage("fr"); got != "zh" {
		t.Fatalf("language = %q", got)
	}
}
