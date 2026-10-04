package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elbot/internal/llm"
	"elbot/internal/llm/openai"
)

// TestShouldFallbackVisionEndToEndThroughAdapter proves the whole chain: a
// gateway that only describes the image rejection in prose is classified by the
// OpenAI adapter and then accepted by the agent without either layer
// pattern-matching text on its own.
func TestShouldFallbackVisionEndToEndThroughAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"<400> InternalError.Algo.InvalidParameter: The provided messages input is invalid. The error info is [Unexpected item type in content.]","type":"invalid_parameter_error","code":"invalid_parameter","param":null}}`)
	}))
	defer server.Close()
	client, err := openai.NewWithOptions(server.URL, "test-key", nil, nil, openai.RequestOptions{})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	_, err = client.ChatStream(context.Background(), llm.ChatRequest{Model: "vision-1"})
	if err == nil {
		t.Fatal("expected the gateway rejection to surface as an error")
	}
	apiErr, ok := llm.AsAPIError(err)
	if !ok || apiErr.Category != llm.ErrorCategoryVisionUnsupported {
		t.Fatalf("adapter category = %#v (%v)", apiErr, err)
	}
	if !shouldFallbackVision(imageMessages(), err) {
		t.Fatal("expected vision fallback for the classified gateway rejection")
	}
}

func TestFallbackVisionMessagesDerivesSingleImageReference(t *testing.T) {
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{
		{Type: llm.SegmentText, Text: "看看"},
		{Type: llm.SegmentImage, URL: "https://example.com/a.jpg", Name: "a.jpg"},
	}}}
	got := fallbackVisionMessages(messages)
	if len(got) != 1 || len(got[0].Segments) != 1 || got[0].Segments[0].Type != llm.SegmentText {
		t.Fatalf("fallback messages = %#v", got)
	}
	content := got[0].Segments[0].Text
	if strings.Count(content, "[图片 1") != 1 || !strings.Contains(content, "引用 URL：https://example.com/a.jpg") {
		t.Fatalf("fallback content = %q", content)
	}
	if llm.MessagesHaveImageSegment(got) {
		t.Fatalf("fallback kept image segments: %#v", got)
	}
}

func TestVisionFallbackTransparent(t *testing.T) {
	cases := []struct {
		name        string
		assistant   int
		toolCalls   int
		reasoning   bool
		wantAllowed bool
	}{
		{"nothing emitted", 0, 0, false, true},
		{"answer text already streamed", 1, 0, false, false},
		{"tool-call delta already seen", 0, 1, false, false},
		{"reasoning already streamed", 0, 0, true, false},
		{"text and tool call", 5, 2, false, false},
	}
	for _, testCase := range cases {
		if got := visionFallbackTransparent(testCase.assistant, testCase.toolCalls, testCase.reasoning); got != testCase.wantAllowed {
			t.Fatalf("%s: visionFallbackTransparent = %v, want %v", testCase.name, got, testCase.wantAllowed)
		}
	}
}

func TestVisionFallbackAttemptGuard(t *testing.T) {
	ctx := context.Background()
	if visionFallbackAttempted(ctx) {
		t.Fatal("a fresh context must not look attempted")
	}
	once := withVisionFallbackAttempt(ctx)
	if !visionFallbackAttempted(once) {
		t.Fatal("a marked context should report attempted")
	}
	if !visionFallbackAttempted(withVisionFallbackAttempt(once)) {
		t.Fatal("re-marking must stay attempted")
	}
	if visionFallbackAttempted(ctx) {
		t.Fatal("marking a child must not mutate the parent context")
	}
}
