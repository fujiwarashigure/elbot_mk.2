package config

import (
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func boolPtr(value bool) *bool { return &value }

func TestVisionSupportResolution(t *testing.T) {
	cfg := &Config{Providers: map[string]ProviderConfig{
		"silent":       {},
		"declared":     {Vision: boolPtr(true)},
		"denied":       {Vision: boolPtr(false)},
		"model-wins":   {Vision: boolPtr(false), ModelConfigs: map[string]ModelConfig{"vl": {Vision: boolPtr(true)}}},
		"model-denies": {Vision: boolPtr(true), ModelConfigs: map[string]ModelConfig{"text": {Vision: boolPtr(false)}}},
	}}

	cases := []struct {
		name     string
		provider string
		model    string
		want     VisionSupport
	}{
		{"undeclared provider", "silent", "anything", VisionUnknown},
		{"provider declares support", "declared", "anything", VisionSupported},
		{"provider declares no support", "denied", "anything", VisionUnsupported},
		{"model override enables", "model-wins", "vl", VisionSupported},
		{"model override disables", "model-denies", "text", VisionUnsupported},
		{"unlisted model falls back to provider", "model-denies", "other", VisionSupported},
		{"missing provider", "ghost", "anything", VisionUnknown},
	}
	for _, testCase := range cases {
		if got := cfg.VisionSupportFor(testCase.provider, testCase.model); got != testCase.want {
			t.Fatalf("%s: VisionSupportFor(%q, %q) = %v, want %v",
				testCase.name, testCase.provider, testCase.model, got, testCase.want)
		}
	}

	var nilCfg *Config
	if got := nilCfg.VisionSupportFor("declared", "anything"); got != VisionUnknown {
		t.Fatalf("nil config = %v, want VisionUnknown", got)
	}
}

func TestVisionSupportParsesTOML(t *testing.T) {
	var provider ProviderConfig
	if err := toml.Unmarshal([]byte("vision = false\n[model_configs.vl]\nvision = true\n"), &provider); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if provider.Vision == nil || *provider.Vision {
		t.Fatalf("provider vision = %v, want explicit false", provider.Vision)
	}
	if got := provider.VisionSupport("vl"); got != VisionSupported {
		t.Fatalf("model override = %v, want VisionSupported", got)
	}
	if got := provider.VisionSupport("other"); got != VisionUnsupported {
		t.Fatalf("provider default = %v, want VisionUnsupported", got)
	}
}
