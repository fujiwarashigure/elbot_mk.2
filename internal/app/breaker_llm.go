package app

import (
	"context"
	"errors"
	"fmt"

	"elbot/internal/health"
	"elbot/internal/llm"
	"elbot/internal/llm/breaker"
)

type breakerLLM struct {
	provider         string
	inner            llm.LLM
	fallback         llm.LLM
	fallbackProvider string
	fallbackModel    string
	breaker          *breaker.Breaker
	state            *health.State
}

func wrapBreakerLLM(provider string, inner llm.LLM, fallback llm.LLM, fallbackProvider, fallbackModel string, br *breaker.Breaker, state *health.State) llm.LLM {
	if inner == nil || br == nil || !br.Enabled() {
		return inner
	}
	return &breakerLLM{
		provider:         provider,
		inner:            inner,
		fallback:         fallback,
		fallbackProvider: fallbackProvider,
		fallbackModel:    fallbackModel,
		breaker:          br,
		state:            state,
	}
}

func (l *breakerLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	client := l.inner
	usingFallback := false
	if !l.breaker.Allow() {
		if l.fallback == nil {
			err := fmt.Errorf("%w: provider=%s", breaker.ErrOpen, l.provider)
			l.recordFailure(err)
			return nil, err
		}
		client = l.fallback
		usingFallback = true
		if l.fallbackModel != "" {
			req.Model = l.fallbackModel
		}
	}
	stream, err := client.ChatStream(ctx, req)
	if err != nil {
		if !usingFallback {
			l.recordFailure(err)
		}
		return nil, err
	}
	if stream == nil {
		err := fmt.Errorf("provider %q returned a nil stream", l.provider)
		if !usingFallback {
			l.recordFailure(err)
		}
		return nil, err
	}

	out := make(chan llm.StreamChunk)
	go func() {
		defer close(out)
		recordedSuccess := false
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-stream:
				if !ok {
					return
				}
				if chunk.Error != nil {
					if !usingFallback {
						l.recordFailure(chunk.Error)
						recordedSuccess = false
					}
				} else if !usingFallback && !recordedSuccess {
					l.breaker.Success()
					recordedSuccess = true
				}
				select {
				case out <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (l *breakerLLM) ListModels(ctx context.Context) ([]string, error) {
	if l.breaker.Allow() {
		models, err := l.inner.ListModels(ctx)
		if err != nil {
			l.recordFailure(err)
			return nil, err
		}
		l.breaker.Success()
		return models, nil
	}
	if l.fallback != nil {
		return l.fallback.ListModels(ctx)
	}
	err := fmt.Errorf("%w: provider=%s", breaker.ErrOpen, l.provider)
	l.recordFailure(err)
	return nil, err
}

// SetRetryNotifier forwards optional retry notifications to the primary client.
func (l *breakerLLM) SetRetryNotifier(fn func(context.Context, llm.RetryEvent)) {
	if notifier, ok := l.inner.(llm.RetryNotifier); ok {
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

func (l *breakerLLM) recordFailure(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	l.breaker.Failure()
	if l.state != nil {
		l.state.RecordModelError(l.provider, err)
	}
}
