package config

import (
	"strings"
	"testing"
)

func TestDefaultServicesTOMLDocumentsVision(t *testing.T) {
	for _, needle := range []string{"[vision]", "[image_to_prompt]", "[image_generation]", "# enabled = false"} {
		if !strings.Contains(defaultServicesTOML, needle) {
			t.Fatalf("default services template is missing %q", needle)
		}
	}
}

func TestVisionConfigIsOptIn(t *testing.T) {
	cfg := Default()
	if cfg.Vision.IsEnabled() {
		t.Fatal("the automatic vision fallback must be disabled by default")
	}
	enabled := true
	cfg.Vision.Enabled = &enabled
	if !cfg.Vision.IsEnabled() {
		t.Fatal("an explicit enabled=true must switch the fallback on")
	}
}

func TestVisionConfigDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Vision.MaxTokens != 400 || cfg.Vision.MaxEdge != 1536 || cfg.Vision.TimeoutSeconds != 90 {
		t.Fatalf("vision defaults = %#v", cfg.Vision)
	}
	if cfg.Vision.MaxImageBytes != 12*1024*1024 || cfg.Vision.CacheTTLSeconds != 1800 || cfg.Vision.NegativeCacheTTLSeconds != 30 {
		t.Fatalf("vision cache defaults = %#v", cfg.Vision)
	}
	if cfg.Vision.TemperatureValue() != 0.2 {
		t.Fatalf("vision temperature = %v", cfg.Vision.TemperatureValue())
	}
	if cfg.Vision.LanguageValue() != "zh" {
		t.Fatalf("default language = %q", cfg.Vision.LanguageValue())
	}
	cfg.Vision.Language = "EN"
	if cfg.Vision.LanguageValue() != "en" {
		t.Fatalf("language normalization = %q", cfg.Vision.LanguageValue())
	}
}

func TestVisionInheritsImageToPromptBackend(t *testing.T) {
	cfg := Default()
	cfg.ImageToPrompt.Provider = "openai"
	cfg.ImageToPrompt.Model = "gpt-4o-mini"
	cfg.applyAppDefaults()
	if cfg.Vision.Provider != "openai" || cfg.Vision.Model != "gpt-4o-mini" {
		t.Fatalf("inherited backend = %q/%q", cfg.Vision.Provider, cfg.Vision.Model)
	}

	// An explicit [vision] backend always wins over the inherited one.
	explicit := Default()
	explicit.ImageToPrompt.Provider = "openai"
	explicit.ImageToPrompt.Model = "gpt-4o-mini"
	explicit.Vision.Provider = "local"
	explicit.Vision.Model = "qwen-vl"
	explicit.applyAppDefaults()
	if explicit.Vision.Provider != "local" || explicit.Vision.Model != "qwen-vl" {
		t.Fatalf("explicit backend = %q/%q", explicit.Vision.Provider, explicit.Vision.Model)
	}
}
