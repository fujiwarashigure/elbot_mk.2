package app

import (
	"context"
	"errors"
	"fmt"

	"elbot/internal/health"
	"elbot/internal/llm"
)

type healthLLM struct {
	provider string
	inner    llm.LLM
	state    *health.State
}

func wrapHealthLLM(provider string, inner llm.LLM, state *health.State) llm.LLM {
	if inner == nil || state == nil {
		return inner
	}
	return &healthLLM{provider: provider, inner: inner, state: state}
}

func (l *healthLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	stream, err := l.inner.ChatStream(ctx, req)
	if err != nil {
		l.recordError(err)
		return nil, err
	}
	if stream == nil {
		err := fmt.Errorf("provider %q returned a nil stream", l.provider)
		l.recordError(err)
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
					l.recordError(chunk.Error)
					recordedSuccess = false
				} else if !recordedSuccess {
					l.state.RecordModelSuccess(l.provider)
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

func (l *healthLLM) ListModels(ctx context.Context) ([]string, error) {
	models, err := l.inner.ListModels(ctx)
	if err != nil {
		l.recordError(err)
		return nil, err
	}
	l.state.RecordModelSuccess(l.provider)
	return models, nil
}

// SetRetryNotifier forwards optional retry notifications to the wrapped client.
func (l *healthLLM) SetRetryNotifier(fn func(context.Context, llm.RetryEvent)) {
	if notifier, ok := l.inner.(llm.RetryNotifier); ok {
		notifier.SetRetryNotifier(fn)
	}
}

// ListModelMetadata forwards optional model metadata to the wrapped client.
// Providers that do not implement the optional interface return an error and
// let callers fall back to manually configured/default context windows.
func (l *healthLLM) ListModelMetadata(ctx context.Context) ([]llm.ModelMetadata, error) {
	provider, ok := l.inner.(llm.ModelMetadataProvider)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support model metadata", l.provider)
	}
	return provider.ListModelMetadata(ctx)
}

func (l *healthLLM) recordError(err error) {
	if l == nil || l.state == nil || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	l.state.RecordModelError(l.provider, err)
}
