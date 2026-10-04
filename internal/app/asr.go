package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"elbot/internal/agent"
	"elbot/internal/asr"
	"elbot/internal/config"
)

// asrTranscriberAdapter adapts the shared asr service to the agent interface.
// It keeps the provider/upload policy in one place and gives the agent only the
// stable request/result shape it needs.
type asrTranscriberAdapter struct {
	service *asr.Service
}

func (a asrTranscriberAdapter) Transcribe(ctx context.Context, req agent.AudioTranscriptionRequest) (agent.AudioTranscriptionResult, error) {
	if a.service == nil {
		return agent.AudioTranscriptionResult{}, fmt.Errorf("asr service is not configured")
	}
	result, err := a.service.Transcribe(ctx, asr.Request{
		MediaID:  req.MediaID,
		Data:     req.Data,
		Name:     req.Name,
		MIMEType: req.MIMEType,
		Language: req.Language,
	})
	if err != nil {
		return agent.AudioTranscriptionResult{}, err
	}
	return agent.AudioTranscriptionResult{
		Text:     result.Text,
		Language: result.Language,
		Provider: result.Provider,
		Model:    result.Model,
	}, nil
}

// audioCapabilityError turns an explicit "audio = false" declaration into a
// startup error for the transcription role. Unknown keeps working unchanged.
func audioCapabilityError(provider, model string, cfg *config.Config) error {
	if cfg.AudioSupportFor(provider, model) != config.AudioUnsupported {
		return nil
	}
	return fmt.Errorf("[asr] provider %q model %q is declared audio = false (in [providers.%s].audio or [providers.%s.model_configs.%q]); enable transcription for that model or configure a capable one", provider, model, provider, provider, model)
}

// buildAudioTranscriber returns nil when ASR is disabled. An explicitly enabled
// but incomplete or unknown configuration returns an error so a typo fails
// loudly at startup instead of silently disabling transcription.
func buildAudioTranscriber(baseCtx context.Context, cfg *config.Config) (agent.AudioTranscriber, error) {
	if cfg == nil || !cfg.ASR.IsEnabled() {
		return nil, nil
	}
	asrCfg := cfg.ASR.Normalized()
	provider := strings.TrimSpace(asrCfg.Provider)
	model := strings.TrimSpace(asrCfg.Model)
	if provider == "" || model == "" {
		return nil, fmt.Errorf("[asr] is enabled but incomplete: provider=%q model=%q; set both, or set enabled=false", provider, model)
	}
	providerCfg, ok := cfg.Providers[provider]
	if !ok {
		return nil, fmt.Errorf("[asr] references provider %q, but no [providers.%s] client is configured", provider, provider)
	}
	if strings.TrimSpace(providerCfg.BaseURL) == "" {
		return nil, fmt.Errorf("[asr] references provider %q, but [providers.%s].base_url is empty", provider, provider)
	}
	if err := audioCapabilityError(provider, model, cfg); err != nil {
		return nil, err
	}
	service := asr.New(asr.Options{
		BaseURL:           providerCfg.BaseURL,
		APIKey:            providerCfg.APIKey,
		Model:             model,
		Provider:          provider,
		Language:          asrCfg.Language,
		Prompt:            asrCfg.Prompt,
		Timeout:           time.Duration(asrCfg.TimeoutSeconds) * time.Second,
		MaxAudioBytes:     asrCfg.MaxAudioBytes,
		MaxConcurrentJobs: asrCfg.MaxConcurrent,
		MaxJobQueue:       asrCfg.QueueSize,
		CacheTTL:          time.Duration(asrCfg.CacheTTLSeconds) * time.Second,
		CacheMaxEntries:   asrCfg.CacheMaxEntries,
		NegativeTTL:       time.Duration(asrCfg.NegativeCacheTTLSeconds) * time.Second,
		NegativeMax:       asrCfg.NegativeCacheMaxEntries,
		MaxRetries:        asrCfg.MaxRetries,
		RetryInitialDelay: time.Duration(asrCfg.RetryInitialDelaySeconds) * time.Second,
		Proxy:             strings.TrimSpace(providerCfg.Proxy),
		BaseContext:       baseCtx,
	})
	return asrTranscriberAdapter{service: service}, nil
}
