package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"elbot/internal/health"
	"elbot/internal/llm"
	"elbot/internal/llm/breaker"
)

type countingLLM struct {
	calls     int
	streamErr error
	chunks    []llm.StreamChunk
}

func (c *countingLLM) ChatStream(context.Context, llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	c.calls++
	if c.streamErr != nil {
		return nil, c.streamErr
	}
	ch := make(chan llm.StreamChunk, len(c.chunks))
	for _, chunk := range c.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (c *countingLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestBreakerLLMUsesFallbackWhenOpen(t *testing.T) {
	primaryErr := errors.New("primary down")
	primary := &countingLLM{streamErr: primaryErr}
	fallback := &countingLLM{chunks: []llm.StreamChunk{{DeltaContent: "fallback"}}}
	state := health.NewState(health.Options{})
	br := breaker.New(breaker.Config{FailureThreshold: 1, OpenCooldown: time.Minute})
	client := wrapBreakerLLM("primary", primary, fallback, "fallback", "fallback-model", br, state)

	if _, err := client.ChatStream(context.Background(), llm.ChatRequest{}); !errors.Is(err, primaryErr) {
		t.Fatalf("first error = %v", err)
	}
	stream, err := client.ChatStream(context.Background(), llm.ChatRequest{Model: "primary-model"})
	if err != nil {
		t.Fatalf("fallback ChatStream error = %v", err)
	}
	for range stream {
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("calls primary=%d fallback=%d", primary.calls, fallback.calls)
	}
}

func TestBreakerLLMWithoutFallbackReturnsOpen(t *testing.T) {
	primaryErr := errors.New("primary down")
	primary := &countingLLM{streamErr: primaryErr}
	br := breaker.New(breaker.Config{FailureThreshold: 1, OpenCooldown: time.Minute})
	client := wrapBreakerLLM("primary", primary, nil, "", "", br, nil)

	if _, err := client.ChatStream(context.Background(), llm.ChatRequest{}); !errors.Is(err, primaryErr) {
		t.Fatalf("first error = %v", err)
	}
	_, err := client.ChatStream(context.Background(), llm.ChatRequest{})
	if !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("second error = %v, want ErrOpen", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
}
func TestBreakerLLMImmediateFallbackOnError(t *testing.T) {
	primaryErr := errors.New("primary down")
	primary := &countingLLM{streamErr: primaryErr}
	fallback := &countingLLM{chunks: []llm.StreamChunk{{DeltaContent: "fallback"}}}
	// fallback_on_error must work even when the circuit breaker is disabled.
	br := breaker.New(breaker.Config{FailureThreshold: 0})
	client := wrapBreakerLLM("primary", primary, fallback, "fallback", "fallback-model", br, nil, withFallbackOnError(true))

	stream, err := client.ChatStream(context.Background(), llm.ChatRequest{Model: "primary-model"})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	var text string
	for chunk := range stream {
		if chunk.Error != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Error)
		}
		text += chunk.DeltaContent
	}
	if text != "fallback" {
		t.Fatalf("stream text = %q, want fallback", text)
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("calls primary=%d fallback=%d", primary.calls, fallback.calls)
	}
}

type blockingLLM struct{ calls int }

func (b *blockingLLM) ChatStream(ctx context.Context, _ llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	b.calls++
	ch := make(chan llm.StreamChunk)
	go func() {
		<-ctx.Done()
	}()
	return ch, nil
}

func (b *blockingLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestBreakerLLMTotalTimeoutEmitsError(t *testing.T) {
	primary := &blockingLLM{}
	br := breaker.New(breaker.Config{FailureThreshold: 3, OpenCooldown: time.Minute})
	client := wrapBreakerLLM("primary", primary, nil, "", "", br, nil, withFallbackTotalTimeout(20*time.Millisecond))

	stream, err := client.ChatStream(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	var gotErr error
	for chunk := range stream {
		if chunk.Error != nil {
			gotErr = chunk.Error
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "total timeout") {
		t.Fatalf("stream error = %v, want total timeout", gotErr)
	}
}
