package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"elbot/internal/health"
	"elbot/internal/llm"
	"elbot/internal/llm/breaker"
)

var errFallbackTotalTimeout = errors.New("LLM total timeout")

type breakerLLMOption func(*breakerLLM)

func withFallbackOnError(enabled bool) breakerLLMOption {
	return func(l *breakerLLM) {
		l.fallbackOnError = enabled
	}
}

func withFallbackTotalTimeout(timeout time.Duration) breakerLLMOption {
	return func(l *breakerLLM) {
		l.totalTimeout = timeout
	}
}

type breakerLLM struct {
	provider         string
	inner            llm.LLM
	fallback         llm.LLM
	fallbackProvider string
	fallbackModel    string
	breaker          *breaker.Breaker
	state            *health.State
	fallbackOnError  bool
	totalTimeout     time.Duration
}

func wrapBreakerLLM(provider string, inner llm.LLM, fallback llm.LLM, fallbackProvider, fallbackModel string, br *breaker.Breaker, state *health.State, opts ...breakerLLMOption) llm.LLM {
	if inner == nil {
		return inner
	}
	if br == nil {
		br = breaker.New(breaker.Config{})
	}
	client := &breakerLLM{
		provider:         provider,
		inner:            inner,
		fallback:         fallback,
		fallbackProvider: fallbackProvider,
		fallbackModel:    fallbackModel,
		breaker:          br,
		state:            state,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	// A disabled circuit breaker is transparent unless the provider explicitly
	// opts into on-error fallback or a total timeout.
	if !br.Enabled() && !client.fallbackOnError && client.totalTimeout <= 0 {
		return inner
	}
	return client
}

func (l *breakerLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if l == nil {
		return nil, fmt.Errorf("breaker llm is nil")
	}
	streamCtx := ctx
	cancel := func() {}
	if l.totalTimeout > 0 {
		streamCtx, cancel = context.WithTimeoutCause(ctx, l.totalTimeout, errFallbackTotalTimeout)
	}

	useFallback := !l.breaker.Allow()
	l.recordCircuit()
	if useFallback && l.fallback == nil {
		cancel()
		err := fmt.Errorf("%w: provider=%s", breaker.ErrOpen, l.provider)
		l.recordFailure(err)
		return nil, err
	}

	stream, usingFallback, err := l.openStream(streamCtx, req, useFallback)
	if err != nil {
		if !usingFallback {
			l.recordFailure(err)
			if l.fallbackOnError && l.fallback != nil {
				fallbackStream, _, fallbackErr := l.openStream(streamCtx, req, true)
				if fallbackErr == nil {
					return l.pipe(streamCtx, fallbackStream, req, true, cancel), nil
				}
				err = fmt.Errorf("provider %s failed: %v; fallback provider %s failed: %w", l.provider, err, l.fallbackProviderName(), fallbackErr)
			}
		}
		cancel()
		return nil, err
	}
	return l.pipe(streamCtx, stream, req, usingFallback, cancel), nil
}

func (l *breakerLLM) openStream(ctx context.Context, req llm.ChatRequest, fallback bool) (<-chan llm.StreamChunk, bool, error) {
	client := l.inner
	provider := l.provider
	model := req.Model
	if fallback {
		client = l.fallback
		provider = l.fallbackProviderName()
		if l.fallbackModel != "" {
			model = l.fallbackModel
		}
	}
	if client == nil {
		return nil, fallback, fmt.Errorf("provider %q is unavailable", provider)
	}
	req.Model = model
	stream, err := client.ChatStream(ctx, req)
	if err != nil {
		return nil, fallback, err
	}
	if stream == nil {
		return nil, fallback, fmt.Errorf("provider %q returned a nil stream", provider)
	}
	return stream, fallback, nil
}

func (l *breakerLLM) pipe(ctx context.Context, first <-chan llm.StreamChunk, req llm.ChatRequest, usingFallback bool, cancel context.CancelFunc) <-chan llm.StreamChunk {
	out := make(chan llm.StreamChunk)
	go func() {
		defer close(out)
		defer cancel()

		current := first
		activeFallback := usingFallback
		recordedSuccess := false
		deliveredContent := false
		for current != nil {
			select {
			case <-ctx.Done():
				if errors.Is(context.Cause(ctx), errFallbackTotalTimeout) {
					timeoutErr := fmt.Errorf("%w after %s", errFallbackTotalTimeout, l.totalTimeout)
					_ = sendStreamChunk(context.Background(), out, llm.StreamChunk{Error: timeoutErr}, 100*time.Millisecond)
				}
				return
			case chunk, ok := <-current:
				if !ok {
					current = nil
					continue
				}
				if chunk.Error != nil {
					if !activeFallback {
						l.recordFailure(chunk.Error)
						if l.fallbackOnError && l.fallback != nil && !deliveredContent {
							fallbackStream, _, fallbackErr := l.openStream(ctx, req, true)
							if fallbackErr == nil {
								current = fallbackStream
								activeFallback = true
								recordedSuccess = false
								continue
							}
							chunk.Error = fmt.Errorf("provider %s failed: %v; fallback provider %s failed: %w", l.provider, chunk.Error, l.fallbackProviderName(), fallbackErr)
						}
					}
				} else {
					if !activeFallback && !recordedSuccess {
						l.breaker.Success()
						l.recordCircuit()
						recordedSuccess = true
					}
					if chunk.DeltaContent != "" || chunk.DeltaReasoningContent != "" || len(chunk.ToolCallDeltas) > 0 {
						deliveredContent = true
					}
				}
				if !sendStreamChunk(ctx, out, chunk, 0) {
					return
				}
			}
		}
	}()
	return out
}

func sendStreamChunk(ctx context.Context, out chan<- llm.StreamChunk, chunk llm.StreamChunk, maxWait time.Duration) bool {
	if maxWait <= 0 {
		select {
		case out <- chunk:
			return true
		case <-ctx.Done():
			return false
		}
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case out <- chunk:
		return true
	case <-timer.C:
		return false
	}
}

func (l *breakerLLM) ListModels(ctx context.Context) ([]string, error) {
	if l == nil {
		return nil, fmt.Errorf("breaker llm is nil")
	}
	if l.breaker.Allow() {
		models, err := l.inner.ListModels(ctx)
		if err != nil {
			l.recordFailure(err)
			if l.fallbackOnError && l.fallback != nil {
				return l.fallback.ListModels(ctx)
			}
			return nil, err
		}
		l.breaker.Success()
		l.recordCircuit()
		return models, nil
	}
	return l.listFallbackModels(ctx)
}

func (l *breakerLLM) listFallbackModels(ctx context.Context) ([]string, error) {
	if l.fallback != nil {
		return l.fallback.ListModels(ctx)
	}
	err := fmt.Errorf("%w: provider=%s", breaker.ErrOpen, l.provider)
	l.recordFailure(err)
	return nil, err
}

// SetRetryNotifier forwards optional retry notifications to the wrapped clients.
func (l *breakerLLM) SetRetryNotifier(fn func(context.Context, llm.RetryEvent)) {
	if notifier, ok := l.inner.(llm.RetryNotifier); ok {
		notifier.SetRetryNotifier(fn)
	}
	if notifier, ok := l.fallback.(llm.RetryNotifier); ok {
		notifier.SetRetryNotifier(fn)
	}
}

// ListModelMetadata forwards optional model metadata to the primary client.
func (l *breakerLLM) ListModelMetadata(ctx context.Context) ([]llm.ModelMetadata, error) {
	provider, ok := l.inner.(llm.ModelMetadataProvider)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support model metadata", l.provider)
	}
	return provider.ListModelMetadata(ctx)
}

func (l *breakerLLM) fallbackProviderName() string {
	if l.fallbackProvider != "" {
		return l.fallbackProvider
	}
	return "fallback"
}

func (l *breakerLLM) recordFailure(err error) {
	if l == nil || err == nil || errors.Is(err, context.Canceled) {
		return
	}
	l.breaker.Failure()
	l.recordCircuit()
	if l.state != nil {
		l.state.RecordModelError(l.provider, err)
	}
}

func (l *breakerLLM) recordCircuit() {
	if l == nil || l.breaker == nil || l.state == nil || !l.breaker.Enabled() {
		return
	}
	l.state.SetModelCircuit(l.provider, l.breaker.State())
}
