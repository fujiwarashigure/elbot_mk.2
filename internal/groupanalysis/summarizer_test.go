package groupanalysis

import (
	"context"
	"testing"

	"elbot/internal/llm"
)

type fakeLLM struct {
	last llm.ChatRequest
}

func (f *fakeLLM) ChatStream(_ context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	f.last = req
	ch := make(chan llm.StreamChunk, 2)
	ch <- llm.StreamChunk{DeltaContent: "活跃度"}
	ch <- llm.StreamChunk{DeltaContent: "正常"}
	close(ch)
	return ch, nil
}

func (f *fakeLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestLLMSummarizerUsesPrompt(t *testing.T) {
	client := &fakeLLM{}
	summarizer := LLMSummarizer{Client: client, Model: "test-model"}
	got, err := summarizer.Summarize(context.Background(), Report{Platform: "qqonebot", ScopeID: "group:1", InboundMessages: 3})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got != "活跃度正常" {
		t.Fatalf("got %q", got)
	}
	if client.last.Model != "test-model" || len(client.last.Messages) != 1 {
		t.Fatalf("request = %#v", client.last)
	}
}
