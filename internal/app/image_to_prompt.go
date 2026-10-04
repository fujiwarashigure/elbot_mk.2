package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/vision"
)

// buildImagePromptService returns the shared vision engine used by the built-in
// image_to_prompt tool, or nil when the section is absent or explicitly
// disabled. An explicitly enabled but incomplete or unknown configuration
// returns an error instead of silently disappearing, so a typo in
// services.toml fails loudly at startup rather than looking like "the model
// just never calls the tool".
//
// The tool no longer owns a private cache or single-flight layer: it uses the
// same vision.Service as the automatic chat fallback, so caching, coalescing,
// output caps, negative caching and timeout handling live in exactly one place.
// baseCtx is the process lifecycle context, so shutting down cancels in-flight
// description jobs instead of letting them run to their shared timeout.
func buildImagePromptService(baseCtx context.Context, cfg *config.Config, models ModelClients, metrics vision.Metrics) (*vision.Service, error) {
	if cfg == nil || !cfg.ImageToPrompt.IsEnabled() {
		return nil, nil
	}
	provider := strings.TrimSpace(cfg.ImageToPrompt.Provider)
	model := strings.TrimSpace(cfg.ImageToPrompt.Model)
	if provider == "" || model == "" {
		return nil, fmt.Errorf("[image_to_prompt] is enabled but incomplete: provider=%q model=%q; set both, or remove the section / set enabled=false", provider, model)
	}
	client := models.ByProvider[provider]
	if client == nil {
		return nil, fmt.Errorf("[image_to_prompt] references provider %q, but no [providers.%s] client is configured", provider, provider)
	}
	if err := visionCapabilityError("image_to_prompt", provider, model, cfg); err != nil {
		return nil, err
	}
	return vision.New(vision.Options{
		Client:        client,
		Provider:      provider,
		Endpoint:      strings.TrimSpace(cfg.Providers[provider].BaseURL),
		Model:         model,
		MaxTokens:     cfg.ImageToPrompt.MaxTokens,
		Temperature:   cfg.ImageToPrompt.TemperatureValue(),
		MaxEdge:       cfg.ImageToPrompt.MaxEdge,
		MaxImageBytes: cfg.ImageToPrompt.MaxImageBytes,
		Timeout:       time.Duration(cfg.ImageToPrompt.TimeoutSeconds) * time.Second,
		BaseContext:   baseCtx,
		Metrics:       metrics,
	}), nil
}
