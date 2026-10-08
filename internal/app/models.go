package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/llm/breaker"
	"elbot/internal/llm/openai"
)

type defaultModelFactory struct{}

func (defaultModelFactory) Build(req ModelRequest) (ModelClients, error) {
	cfg := req.Foundation.Config
	logger := req.Foundation.Logger
	rawClients := make(map[string]llm.LLM, len(cfg.Providers))
	for name, provider := range cfg.Providers {
		client, err := newProviderLLM(name, provider, appLLMRequestOptions(cfg.LLMRequest, provider.Proxy))
		if err != nil {
			return ModelClients{}, fmt.Errorf("create provider %q client: %w", name, err)
		}
		setClientLogger(client, logger)
		rawClients[name] = client
	}

	healthClients := make(map[string]llm.LLM, len(rawClients))
	for name, client := range rawClients {
		healthClients[name] = wrapHealthLLM(name, client, req.Health)
	}

	clients := make(map[string]llm.LLM, len(healthClients))
	for name, provider := range cfg.Providers {
		healthClient := healthClients[name]
		if healthClient == nil {
			continue
		}
		var fallback llm.LLM
		if fallbackName := provider.FallbackProvider; fallbackName != "" && fallbackName != name {
			fallback = healthClients[fallbackName]
		}
		br := breaker.New(breaker.Config{
			FailureThreshold: cfg.Ops.CircuitBreakerFailureThreshold,
			OpenCooldown:     time.Duration(cfg.Ops.CircuitBreakerOpenCooldownSeconds) * time.Second,
			HalfOpenMax:      cfg.Ops.CircuitBreakerHalfOpenMax,
		})
		clients[name] = wrapBreakerLLM(name, healthClient, fallback, provider.FallbackProvider, provider.FallbackModel, br, req.Health,
			withFallbackOnError(provider.UsesFallbackOnError()),
			withFallbackTotalTimeout(time.Duration(provider.FallbackTimeoutSeconds)*time.Second),
		)
	}
	req.Profiler.Mark("llm adapters")
	return ModelClients{ByProvider: clients}, nil
}

func appLLMRequestOptions(cfg config.LLMRequestConfig, proxy string) openai.RequestOptions {
	return openai.RequestOptions{
		FirstChunkTimeout: time.Duration(cfg.FirstChunkTimeoutSeconds) * time.Second,
		StreamIdleTimeout: time.Duration(cfg.StreamIdleTimeoutSeconds) * time.Second,
		MaxRetries:        cfg.MaxRetries,
		RetryInitialDelay: time.Duration(cfg.RetryInitialDelaySeconds) * time.Second,
		Proxy:             proxy,
	}
}

func modelExtraPayloads(modelConfigs map[string]config.ModelConfig) map[string]map[string]any {
	out := map[string]map[string]any{}
	for model, cfg := range modelConfigs {
		if cfg.ExtraPayload != nil {
			out[model] = cfg.ExtraPayload
		}
	}
	return out
}

// newProviderLLM builds the adapter for one provider, honoring the configured
// protocol ([providers.<name>].api_mode and the per-model override). A provider
// that mixes protocols across models gets a router so every request reaches the
// endpoint matching its model.
func newProviderLLM(name string, provider config.ProviderConfig, opts openai.RequestOptions) (llm.LLM, error) {
	modelExtras := modelExtraPayloads(provider.ModelConfigs)
	chat, err := openai.NewWithOptions(provider.BaseURL, provider.APIKey, provider.ExtraPayload, modelExtras, opts)
	if err != nil {
		return nil, err
	}
	if !provider.UsesResponsesAPI() {
		return chat, nil
	}
	responses, err := openai.NewResponsesWithOptions(provider.BaseURL, provider.APIKey, provider.ExtraPayload, modelExtras, opts)
	if err != nil {
		return nil, err
	}
	return &protocolRouter{provider: name, chat: chat, responses: responses, protocol: provider.APIProtocolFor}, nil
}

// setClientLogger attaches the logger to adapters that accept one. It stays
// optional so wrapping a client that does not log cannot fail the build.
func setClientLogger(client llm.LLM, logger *slog.Logger) {
	if setter, ok := client.(interface{ SetLogger(*slog.Logger) }); ok {
		setter.SetLogger(logger)
	}
}

// protocolRouter dispatches one provider's requests to the chat or the
// responses adapter, depending on the model of the request.
type protocolRouter struct {
	provider  string
	chat      llm.LLM
	responses llm.LLM
	protocol  func(model string) config.APIProtocol
}

func (r *protocolRouter) pick(model string) llm.LLM {
	if r.responses != nil && r.protocol != nil && r.protocol(model) == config.APIProtocolResponses {
		return r.responses
	}
	return r.chat
}

func (r *protocolRouter) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return r.pick(req.Model).ChatStream(ctx, req)
}

// ListModels is protocol-independent (/models), so the chat adapter answers and
// the responses adapter is only a fallback for gateways that reject /models.
func (r *protocolRouter) ListModels(ctx context.Context) ([]string, error) {
	models, err := r.chat.ListModels(ctx)
	if err == nil {
		return models, nil
	}
	if r.responses != nil {
		if fallback, fallbackErr := r.responses.ListModels(ctx); fallbackErr == nil {
			return fallback, nil
		}
	}
	return nil, err
}

func (r *protocolRouter) ListModelMetadata(ctx context.Context) ([]llm.ModelMetadata, error) {
	metadata, err := clientModelMetadata(ctx, r.chat)
	if err == nil {
		return metadata, nil
	}
	if r.responses != nil {
		if fallback, fallbackErr := clientModelMetadata(ctx, r.responses); fallbackErr == nil {
			return fallback, nil
		}
	}
	return nil, fmt.Errorf("provider %q does not support model metadata", r.provider)
}

func clientModelMetadata(ctx context.Context, client llm.LLM) ([]llm.ModelMetadata, error) {
	provider, ok := client.(llm.ModelMetadataProvider)
	if !ok {
		return nil, fmt.Errorf("client does not support model metadata")
	}
	return provider.ListModelMetadata(ctx)
}

// SetLogger forwards to both adapters so the protocol actually used keeps its
// debug logging.
func (r *protocolRouter) SetLogger(logger *slog.Logger) {
	setClientLogger(r.chat, logger)
	setClientLogger(r.responses, logger)
}

// SetRetryNotifier forwards to both adapters so retry notices are not lost when
// the provider mixes protocols.
func (r *protocolRouter) SetRetryNotifier(fn func(context.Context, llm.RetryEvent)) {
	for _, client := range []llm.LLM{r.chat, r.responses} {
		if notifier, ok := client.(llm.RetryNotifier); ok {
			notifier.SetRetryNotifier(fn)
		}
	}
}
