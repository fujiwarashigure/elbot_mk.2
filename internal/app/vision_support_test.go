package app

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/llm"
)

// TestVisionBuildersHonorDeclaredCapability proves a model explicitly marked
// vision = false cannot be wired as the image_to_prompt backend or the chat
// vision fallback: a guaranteed-to-fail configuration fails at startup instead
// of at the first image.
func TestVisionBuildersHonorDeclaredCapability(t *testing.T) {
	unsupported := false
	supported := true
	newConfig := func() *config.Config {
		cfg := config.Default()
		cfg.Providers = map[string]config.ProviderConfig{"openai": {}}
		cfg.ImageToPrompt.Provider = "openai"
		cfg.ImageToPrompt.Model = "vision-1"
		cfg.Vision.Enabled = &supported
		cfg.Vision.Provider = "openai"
		cfg.Vision.Model = "vision-1"
		return cfg
	}
	models := ModelClients{ByProvider: map[string]llm.LLM{"openai": &recordingVisionLLM{}}}

	cfg := newConfig()
	cfg.Providers["openai"] = config.ProviderConfig{ModelConfigs: map[string]config.ModelConfig{"vision-1": {Vision: &unsupported}}}
	if _, err := buildImagePromptService(context.Background(), cfg, models, nil); err == nil || !strings.Contains(err.Error(), "vision = false") {
		t.Fatalf("image_to_prompt error = %v, want a vision = false failure", err)
	}
	if _, err := buildVisionDescriber(context.Background(), cfg, models, nil); err == nil || !strings.Contains(err.Error(), "vision = false") {
		t.Fatalf("vision error = %v, want a vision = false failure", err)
	}

	// A provider-wide default can be overridden per model.
	cfg = newConfig()
	cfg.Providers["openai"] = config.ProviderConfig{
		Vision:       &unsupported,
		ModelConfigs: map[string]config.ModelConfig{"vision-1": {Vision: &supported}},
	}
	if _, err := buildImagePromptService(context.Background(), cfg, models, nil); err != nil {
		t.Fatalf("per-model override was ignored: %v", err)
	}
	if _, err := buildVisionDescriber(context.Background(), cfg, models, nil); err != nil {
		t.Fatalf("per-model override was ignored: %v", err)
	}

	// Unknown keeps working unchanged.
	cfg = newConfig()
	if _, err := buildImagePromptService(context.Background(), cfg, models, nil); err != nil {
		t.Fatalf("unknown capability must stay permissive: %v", err)
	}
	if _, err := buildVisionDescriber(context.Background(), cfg, models, nil); err != nil {
		t.Fatalf("unknown capability must stay permissive: %v", err)
	}
}
