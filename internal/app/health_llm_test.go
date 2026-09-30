package app

import (
	"context"
	"errors"
	"testing"

	"elbot/internal/health"
	"elbot/internal/llm"
)

type fakeHealthLLM struct {
	streamErr error
	listErr   error
	chunks    []llm.StreamChunk
	models    []string
}

func (f *fakeHealthLLM) ChatStream(context.Context, llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	ch := make(chan llm.StreamChunk, len(f.chunks))
	for _, chunk := range f.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (f *fakeHealthLLM) ListModels(context.Context) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]string(nil), f.models...), nil
}

func TestHealthLLMRecordsProviderErrorsAndRecovery(t *testing.T) {
	state := health.NewState(health.Options{})
	state.Beat()
	state.SetReady(true)

	providerErr := errors.New("upstream timeout")
	client := wrapHealthLLM("deepseek", &fakeHealthLLM{streamErr: providerErr}, state)
	if _, err := client.ChatStream(context.Background(), llm.ChatRequest{}); !errors.Is(err, providerErr) {
		t.Fatalf("ChatStream() error = %v", err)
	}
	snapshot := state.Snapshot()
	if snapshot.Status != "degraded" || !snapshot.Degraded {
		t.Fatalf("snapshot after error = %#v", snapshot)
	}

	client = wrapHealthLLM("deepseek", &fakeHealthLLM{chunks: []llm.StreamChunk{{DeltaContent: "ok"}}}, state)
	stream, err := client.ChatStream(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	for range stream {
	}
	snapshot = state.Snapshot()
	if snapshot.Status != "ok" || snapshot.Degraded {
		t.Fatalf("snapshot after success = %#v", snapshot)
	}
}
