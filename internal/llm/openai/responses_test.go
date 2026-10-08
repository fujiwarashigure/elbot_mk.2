package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elbot/internal/llm"
)

type capturedRequest struct {
	path string
	body []byte
}

func mustNewResponses(t *testing.T, baseURL, apiKey string, extraPayload map[string]any, modelExtraPayloads map[string]map[string]any, opts RequestOptions) *ResponsesAdapter {
	t.Helper()
	adapter, err := NewResponsesWithOptions(baseURL, apiKey, extraPayload, modelExtraPayloads, opts)
	if err != nil {
		t.Fatalf("NewResponsesWithOptions: %v", err)
	}
	return adapter
}

// responsesSSEServer streams the given SSE payloads and records the request.
func responsesSSEServer(events []string, capture *capturedRequest) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			capture.path = r.URL.Path
			capture.body, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			io.WriteString(w, event+"\n\n")
			w.(http.Flusher).Flush()
		}
	}))
}

type collectedStream struct {
	content   strings.Builder
	reasoning strings.Builder
	toolCalls []llm.ToolCallDelta
	usage     *llm.Usage
	finish    string
	err       error
}

func collectStream(ch <-chan llm.StreamChunk) collectedStream {
	var out collectedStream
	for chunk := range ch {
		if chunk.Error != nil {
			out.err = chunk.Error
			continue
		}
		out.content.WriteString(chunk.DeltaContent)
		out.reasoning.WriteString(chunk.DeltaReasoningContent)
		out.toolCalls = append(out.toolCalls, chunk.ToolCallDeltas...)
		if chunk.Usage != nil {
			out.usage = chunk.Usage
		}
		if chunk.FinishReason != "" {
			out.finish = chunk.FinishReason
		}
	}
	return out
}

func decodeBody(t *testing.T, capture *capturedRequest) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(capture.body, &body); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}
	return body
}

func TestResponsesChatStream_TextUsageAndRequestShape(t *testing.T) {
	capture := &capturedRequest{}
	srv := responsesSSEServer([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		`data: {"type":"response.output_text.delta","delta":" world"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7,"input_tokens_details":{"cached_tokens":3}}}}`,
	}, capture)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL+"/v1", "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model: "gpt-5.1",
		Messages: []llm.LLMMessage{
			{Role: llm.RoleSystem, Segments: llm.TextSegments("be brief")},
			{Role: llm.RoleUser, Segments: llm.TextSegments("Hi")},
		},
		Tools: []llm.ToolSchema{{
			Type:     "function",
			Function: llm.ToolFunctionSchema{Name: "shell", Description: "run a command", Parameters: map[string]any{"type": "object"}},
		}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	result := collectStream(ch)
	if result.err != nil {
		t.Fatalf("stream error: %v", result.err)
	}
	if got := result.content.String(); got != "Hello world" {
		t.Errorf("content = %q, want %q", got, "Hello world")
	}
	if result.finish != "stop" {
		t.Errorf("finish reason = %q, want stop", result.finish)
	}
	if result.usage == nil {
		t.Fatal("expected usage in the terminal chunk")
	}
	if result.usage.PromptTokens != 5 || result.usage.CompletionTokens != 2 || result.usage.TotalTokens != 7 || result.usage.CacheHitTokens != 3 {
		t.Errorf("usage = %+v, want input 5 / output 2 / total 7 / cached 3", *result.usage)
	}
	if capture.path != "/v1/responses" {
		t.Errorf("endpoint path = %q, want /v1/responses", capture.path)
	}

	body := decodeBody(t, capture)
	if body["model"] != "gpt-5.1" {
		t.Errorf("model = %#v", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %#v, want true", body["stream"])
	}
	if body["store"] != false {
		t.Errorf("store = %#v, want false", body["store"])
	}
	if body["instructions"] != "be brief" {
		t.Errorf("instructions = %#v, want the system prompt", body["instructions"])
	}
	if _, ok := body["stream_options"]; ok {
		t.Errorf("stream_options must not be sent to the responses endpoint: %#v", body["stream_options"])
	}

	input, ok := body["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v, want one user message", body["input"])
	}
	message, _ := input[0].(map[string]any)
	if message["type"] != "message" || message["role"] != "user" {
		t.Fatalf("input[0] = %#v", message)
	}
	parts, _ := message["content"].([]any)
	if len(parts) != 1 {
		t.Fatalf("input[0].content = %#v", message["content"])
	}
	part, _ := parts[0].(map[string]any)
	if part["type"] != "input_text" || part["text"] != "Hi" {
		t.Errorf("input[0].content[0] = %#v, want input_text Hi", part)
	}

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "shell" || tool["description"] != "run a command" {
		t.Errorf("tools[0] = %#v, want a flattened function tool", tool)
	}
	if _, nested := tool["function"]; nested {
		t.Errorf("tools[0] must not nest the function envelope: %#v", tool)
	}
}

func TestResponsesChatStream_ToolCallEmittedOnce(t *testing.T) {
	capture := &capturedRequest{}
	srv := responsesSSEServer([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":""}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"cmd\""}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":":\"ls\"}"}`,
		`data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_1","arguments":"{\"cmd\":\"ls\"}"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{\"cmd\":\"ls\"}"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_2","status":"completed","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`,
	}, capture)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "test",
		Messages: []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("list files")}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	result := collectStream(ch)
	if result.err != nil {
		t.Fatalf("stream error: %v", result.err)
	}
	if len(result.toolCalls) != 1 {
		t.Fatalf("tool calls = %#v, want exactly one", result.toolCalls)
	}
	call := result.toolCalls[0]
	if call.ID != "call_1" || call.Name != "shell" || call.Args != `{"cmd":"ls"}` {
		t.Errorf("tool call = %#v, want call_1/shell with the complete arguments", call)
	}
	if result.finish != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", result.finish)
	}
}

func TestResponsesChatStream_ToolTurnRequestShape(t *testing.T) {
	capture := &capturedRequest{}
	srv := responsesSSEServer([]string{
		`data: {"type":"response.completed","response":{"id":"resp_3","status":"completed"}}`,
	}, capture)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL+"/v1", "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model: "test",
		Messages: []llm.LLMMessage{
			{Role: llm.RoleSystem, Segments: llm.TextSegments("be brief")},
			{Role: llm.RoleUser, Segments: llm.TextSegments("list files")},
			{
				Role:      llm.RoleAssistant,
				Segments:  llm.TextSegments("on it"),
				ToolCalls: []llm.ToolCallRequest{{ID: "call_1", Name: "shell", Arguments: `{"cmd":"ls"}`}},
			},
			{Role: llm.RoleTool, Name: "shell", ToolCallID: "call_1", Segments: llm.TextSegments("a.txt")},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if result := collectStream(ch); result.err != nil {
		t.Fatalf("stream error: %v", result.err)
	}

	body := decodeBody(t, capture)
	input, _ := body["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input = %#v, want user, assistant, function_call and function_call_output", input)
	}
	assistant, _ := input[1].(map[string]any)
	if assistant["type"] != "message" || assistant["role"] != "assistant" {
		t.Fatalf("input[1] = %#v", assistant)
	}
	parts, _ := assistant["content"].([]any)
	if len(parts) != 1 {
		t.Fatalf("assistant content = %#v", assistant["content"])
	}
	part, _ := parts[0].(map[string]any)
	if part["type"] != "output_text" || part["text"] != "on it" {
		t.Errorf("assistant content[0] = %#v, want output_text", part)
	}
	call, _ := input[2].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call_1" || call["name"] != "shell" || call["arguments"] != `{"cmd":"ls"}` {
		t.Errorf("input[2] = %#v, want a function_call item", call)
	}
	output, _ := input[3].(map[string]any)
	if output["type"] != "function_call_output" || output["call_id"] != "call_1" || output["output"] != "a.txt" {
		t.Errorf("input[3] = %#v, want a function_call_output item", output)
	}
}

func TestResponsesChatStream_ImageBecomesInputImage(t *testing.T) {
	capture := &capturedRequest{}
	srv := responsesSSEServer([]string{
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
	}, capture)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model: "test",
		Messages: []llm.LLMMessage{{
			Role: llm.RoleUser,
			Segments: []llm.MessageSegment{
				{Type: llm.SegmentText, Text: "what is this"},
				{Type: llm.SegmentImage, URL: "https://example.invalid/cat.png"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if result := collectStream(ch); result.err != nil {
		t.Fatalf("stream error: %v", result.err)
	}

	body := decodeBody(t, capture)
	input, _ := body["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input = %#v", input)
	}
	message, _ := input[0].(map[string]any)
	parts, _ := message["content"].([]any)
	if len(parts) != 3 {
		t.Fatalf("content = %#v, want text reference, text and image parts", message["content"])
	}
	image, _ := parts[2].(map[string]any)
	if image["type"] != "input_image" || image["image_url"] != "https://example.invalid/cat.png" {
		t.Errorf("content[2] = %#v, want an input_image part", image)
	}
}

func TestResponsesChatStream_ReasoningDelta(t *testing.T) {
	srv := responsesSSEServer([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
		`data: {"type":"response.output_text.delta","delta":"answer"}`,
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
	}, nil)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "test",
		Messages: []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hi")}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	result := collectStream(ch)
	if result.err != nil {
		t.Fatalf("stream error: %v", result.err)
	}
	if got := result.reasoning.String(); got != "thinking" {
		t.Errorf("reasoning = %q, want thinking", got)
	}
	if got := result.content.String(); got != "answer" {
		t.Errorf("content = %q, want answer", got)
	}
}

func TestResponsesChatStream_FailedEventCarriesUpstreamError(t *testing.T) {
	srv := responsesSSEServer([]string{
		`data: {"type":"response.failed","response":{"id":"resp_4","status":"failed","error":{"code":"model_not_found","type":"invalid_request_error","message":"model gpt-x does not exist"}}}`,
	}, nil)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "gpt-x",
		Messages: []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hi")}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	result := collectStream(ch)
	if result.err == nil {
		t.Fatal("expected a stream error")
	}
	apiErr, ok := llm.AsAPIError(result.err)
	if !ok {
		t.Fatalf("error = %T (%v), want *llm.APIError", result.err, result.err)
	}
	if apiErr.Code != "model_not_found" || apiErr.Category != llm.ErrorCategoryModelNotFound {
		t.Errorf("api error = %+v, want the upstream model_not_found code and category", apiErr)
	}
}

func TestResponsesChatStream_ErrorEventCarriesMessage(t *testing.T) {
	srv := responsesSSEServer([]string{
		`data: {"type":"error","code":"server_error","message":"upstream exploded","param":null,"sequence_number":2}`,
	}, nil)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "test",
		Messages: []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hi")}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	result := collectStream(ch)
	if result.err == nil || !strings.Contains(result.err.Error(), "upstream exploded") {
		t.Fatalf("error = %v, want the upstream message", result.err)
	}
	apiErr, ok := llm.AsAPIError(result.err)
	if !ok || apiErr.Code != "server_error" {
		t.Fatalf("api error = %#v, want the upstream code", result.err)
	}
}

func TestResponsesChatStream_NestedErrorEventIsAccepted(t *testing.T) {
	srv := responsesSSEServer([]string{
		`data: {"type":"error","error":{"type":"server_error","message":"nested failure"}}`,
	}, nil)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "test",
		Messages: []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hi")}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	result := collectStream(ch)
	if result.err == nil || !strings.Contains(result.err.Error(), "nested failure") {
		t.Fatalf("error = %v, want the nested upstream message", result.err)
	}
}

func TestResponsesChatStream_MissingTerminalEventIsAnUnexpectedEOF(t *testing.T) {
	srv := responsesSSEServer([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
	}, nil)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "test",
		Messages: []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hi")}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	result := collectStream(ch)
	if !errors.Is(result.err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want io.ErrUnexpectedEOF", result.err)
	}
}

func TestResponsesChatStream_DoneSentinelEndsStream(t *testing.T) {
	srv := responsesSSEServer([]string{
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		`data: [DONE]`,
	}, nil)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "test",
		Messages: []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hi")}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	result := collectStream(ch)
	if result.err != nil {
		t.Fatalf("stream error: %v", result.err)
	}
	if got := result.content.String(); got != "Hello" {
		t.Errorf("content = %q, want Hello", got)
	}
}

func TestResponsesChatStream_HTTPErrorKeepsVisionCategory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"Invalid content type for image input","type":"invalid_request_error","param":"input[0].content[1]"}}`)
	}))
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", nil, nil, RequestOptions{})
	_, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model: "test",
		Messages: []llm.LLMMessage{{
			Role:     llm.RoleUser,
			Segments: []llm.MessageSegment{{Type: llm.SegmentImage, URL: "https://example.invalid/cat.png"}},
		}},
	})
	apiErr, ok := llm.AsAPIError(err)
	if !ok {
		t.Fatalf("error = %T (%v), want *llm.APIError", err, err)
	}
	if apiErr.Category != llm.ErrorCategoryVisionUnsupported {
		t.Fatalf("category = %q, want %q so the vision fallback can retry", apiErr.Category, llm.ErrorCategoryVisionUnsupported)
	}
}

func TestResponsesChatStream_ExtraPayloadCannotReplaceReservedFields(t *testing.T) {
	capture := &capturedRequest{}
	srv := responsesSSEServer([]string{
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
	}, capture)
	defer srv.Close()

	adapter := mustNewResponses(t, srv.URL, "test-key", map[string]any{
		"stream":            false,
		"input":             []any{},
		"instructions":      "hijacked",
		"reasoning":         map[string]any{"effort": "low"},
		"store":             true,
		"max_output_tokens": 512,
	}, map[string]map[string]any{
		"test": {"stream": false},
	}, RequestOptions{})
	ch, err := adapter.ChatStream(context.Background(), llm.ChatRequest{
		Model: "test",
		Messages: []llm.LLMMessage{
			{Role: llm.RoleSystem, Segments: llm.TextSegments("be brief")},
			{Role: llm.RoleUser, Segments: llm.TextSegments("hi")},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if result := collectStream(ch); result.err != nil {
		t.Fatalf("stream error: %v", result.err)
	}

	body := decodeBody(t, capture)
	if body["stream"] != true {
		t.Errorf("stream = %#v, want true", body["stream"])
	}
	if body["instructions"] != "be brief" {
		t.Errorf("instructions = %#v, want the caller's system prompt", body["instructions"])
	}
	input, _ := body["input"].([]any)
	if len(input) != 1 {
		t.Errorf("input = %#v, want the caller's messages", body["input"])
	}
	// Non-reserved extras still pass through, including the store escape hatch.
	if body["store"] != true {
		t.Errorf("store = %#v, want the extra payload value", body["store"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "low" {
		t.Errorf("reasoning = %#v, want the extra payload", body["reasoning"])
	}
	if body["max_output_tokens"] != float64(512) {
		t.Errorf("max_output_tokens = %#v, want the extra payload value", body["max_output_tokens"])
	}
}

func TestResponsesReservedRequestFields(t *testing.T) {
	for _, field := range []string{"model", "input", "instructions", "stream", "tools"} {
		if _, reserved := responsesReservedRequestFields[field]; !reserved {
			t.Fatalf("%s should be reserved", field)
		}
	}
	if _, reserved := responsesReservedRequestFields["store"]; reserved {
		t.Fatal("store must stay overridable through extra_payload")
	}
}
