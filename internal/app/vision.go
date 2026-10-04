package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"elbot/internal/agent"
	"elbot/internal/config"
	"elbot/internal/vision"
)

// visionChatDescriber adapts the shared vision service to the agent's
// chat-fallback interface. It owns the description prompt and its language so
// the agent stays unaware of prompts and providers.
type visionChatDescriber struct {
	service  *vision.Service
	language string
}

func (d visionChatDescriber) DescribeImage(ctx context.Context, mediaID string, data []byte, mimeType string) (string, error) {
	result, err := d.service.Describe(ctx, vision.Request{
		MediaID:  mediaID,
		Data:     data,
		MIMEType: mimeType,
		Prompt:   vision.ChatDescription(d.language),
	})
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// visionCapabilityError turns an explicit "vision = false" declaration into a
// startup error for a role that needs image input. Providers and single models
// can both declare the capability, so a provider-wide default can be overridden
// per model. Unknown keeps working unchanged.
func visionCapabilityError(section, provider, model string, cfg *config.Config) error {
	if cfg.VisionSupportFor(provider, model) != config.VisionUnsupported {
		return nil
	}
	return fmt.Errorf("[%s] provider %q model %q is declared vision = false (in [providers.%s].vision or [providers.%s.model_configs.%q]); enable image input for that model or configure a vision-capable one", section, provider, model, provider, provider, model)
}

// buildVisionDescriber returns nil when the automatic fallback is disabled. An
// explicitly enabled but incomplete or unknown configuration returns an error
// so a typo fails loudly at startup instead of silently disabling the fallback.
//
// baseCtx is the process lifecycle context: it cancels in-flight shared vision
// jobs on shutdown instead of letting them run to the shared timeout.
func buildVisionDescriber(baseCtx context.Context, cfg *config.Config, models ModelClients, metrics vision.Metrics) (agent.VisionDescriber, error) {
	if cfg == nil || !cfg.Vision.IsEnabled() {
		return nil, nil
	}
	provider := strings.TrimSpace(cfg.Vision.Provider)
	model := strings.TrimSpace(cfg.Vision.Model)
	if provider == "" || model == "" {
		return nil, fmt.Errorf("[vision] is enabled but incomplete: provider=%q model=%q; set both, or set enabled=false", provider, model)
	}
	client := models.ByProvider[provider]
	if client == nil {
		return nil, fmt.Errorf("[vision] references provider %q, but no [providers.%s] client is configured", provider, provider)
	}
	if err := visionCapabilityError("vision", provider, model, cfg); err != nil {
		return nil, err
	}
	service := vision.New(vision.Options{
		Client:        client,
		Provider:      provider,
		Endpoint:      strings.TrimSpace(cfg.Providers[provider].BaseURL),
		Model:         model,
		MaxTokens:     cfg.Vision.MaxTokens,
		Temperature:   cfg.Vision.TemperatureValue(),
		MaxEdge:       cfg.Vision.MaxEdge,
		MaxImageBytes: cfg.Vision.MaxImageBytes,
		Timeout:       time.Duration(cfg.Vision.TimeoutSeconds) * time.Second,
		CacheTTL:      time.Duration(cfg.Vision.CacheTTLSeconds) * time.Second,
		NegativeTTL:   time.Duration(cfg.Vision.NegativeCacheTTLSeconds) * time.Second,
		BaseContext:   baseCtx,
		Metrics:       metrics,
	})
	return visionChatDescriber{service: service, language: cfg.Vision.LanguageValue()}, nil
}
