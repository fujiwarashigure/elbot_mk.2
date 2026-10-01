package contextmgr

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/llm"
)

func TestNewPromptBudget(t *testing.T) {
	budget := NewPromptBudget(100, 0.8, 0.5, 0)
	if budget.InputLimit != 80 {
		t.Fatalf("InputLimit = %d, want 80", budget.InputLimit)
	}
	if budget.SingleMessageLimit != 50 {
		t.Fatalf("SingleMessageLimit = %d, want 50", budget.SingleMessageLimit)
	}
	if budget.ReserveOutputTokens != 20 {
		t.Fatalf("ReserveOutputTokens = %d, want 20", budget.ReserveOutputTokens)
	}
}

func TestEstimateTextTokensAndTruncate(t *testing.T) {
	if got := EstimateTextTokens("hello world"); got < 3 || got > 10 {
		t.Fatalf("ascii tokens = %d", got)
	}
	if got := EstimateTextTokens("你好世界"); got < 4 || got > 10 {
		t.Fatalf("cjk tokens = %d", got)
	}
	text := strings.Repeat("你好", 100)
	got := TruncateTextToTokens(text, 50)
	if got == "" || EstimateTextTokens(got) > 50 {
		t.Fatalf("truncated tokens = %d text = %q", EstimateTextTokens(got), got)
	}
	if !strings.Contains(TruncateTextWithMarker(text, 10, "|cut"), "|cut") {
		t.Fatal("marker was not appended")
	}
}

type captureLLM struct {
	requests []llm.ChatRequest
	replies  []string
}

func (c *captureLLM) ChatStream(_ context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	c.requests = append(c.requests, req)
	reply := "summary"
	if len(c.replies) > 0 {
		reply = c.replies[0]
		c.replies = c.replies[1:]
	}
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{DeltaContent: reply}
	close(ch)
	return ch, nil
}

func (c *captureLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestCompressorCapsUserInputs(t *testing.T) {
	client := &captureLLM{replies: []string{"summary"}}
	compressor := Compressor{ClientFor: func(string) llm.LLM { return client }}
	_, err := compressor.Compact(context.Background(), CompactRequest{
		Provider:          "p",
		Model:             "m",
		Messages:          []CompactMessage{{Role: "user", Content: strings.Repeat("x", 100)}},
		UserInputs:        []string{strings.Repeat("y", 100)},
		UserInputMaxRunes: 10,
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d", len(client.requests))
	}
	prompt := llm.SegmentsContentText(client.requests[0].Messages[1].Segments)
	if strings.Contains(prompt, strings.Repeat("x", 11)) || strings.Contains(prompt, strings.Repeat("y", 11)) {
		t.Fatalf("long message was not capped: %q", prompt)
	}
	if !strings.Contains(prompt, "用户原话过长") {
		t.Fatalf("cap marker missing: %q", prompt)
	}
}

func TestCompressorSummarizeText(t *testing.T) {
	client := &captureLLM{replies: []string{"S1", "S2", "S3"}}
	compressor := Compressor{ClientFor: func(string) llm.LLM { return client }}
	result, err := compressor.SummarizeText(context.Background(), SummarizeRequest{
		Provider:         "p",
		Model:            "m",
		Text:             strings.Repeat("很长的一段消息。", 200),
		MaxInputTokens:   80,
		ChunkInputTokens: 80,
	})
	if err != nil {
		t.Fatalf("SummarizeText: %v", err)
	}
	if strings.TrimSpace(result.Summary) == "" {
		t.Fatal("empty summary")
	}
	if len(client.requests) == 0 {
		t.Fatal("no summary requests")
	}
}
