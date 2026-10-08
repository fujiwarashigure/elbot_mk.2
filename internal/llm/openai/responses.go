package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"elbot/internal/llm"
)

// responsesReservedRequestFields are the Responses JSON fields owned by the
// adapter, for the same reason as chatReservedRequestFields: an extra payload
// may add provider-specific parameters but must not turn streaming off or swap
// the model/input behind the caller's back.
//
// "store" is deliberately not reserved: the adapter sends store=false (ElBot
// replays the whole history and never reads a stored response back, so
// server-side retention is pointless), but a gateway that needs a different
// value can still set it through extra_payload.
var responsesReservedRequestFields = map[string]struct{}{
	"model":        {},
	"input":        {},
	"instructions": {},
	"stream":       {},
	"tools":        {},
}

// ResponsesAdapter implements llm.LLM for the OpenAI Responses API
// (POST {base_url}/responses). Transport, retry, model listing and error
// classification are shared with the chat adapter; only the request envelope
// and the streaming event translation differ.
type ResponsesAdapter struct {
	*Adapter
}

// NewResponsesWithOptions builds a Responses adapter with the same options as
// the chat adapter.
func NewResponsesWithOptions(baseURL, apiKey string, extraPayload map[string]any, modelExtraPayloads map[string]map[string]any, opts RequestOptions) (*ResponsesAdapter, error) {
	adapter, err := NewWithOptions(baseURL, apiKey, extraPayload, modelExtraPayloads, opts)
	if err != nil {
		return nil, err
	}
	return &ResponsesAdapter{Adapter: adapter}, nil
}

func (a *ResponsesAdapter) endpoint() string {
	return strings.TrimRight(a.baseURL, "/") + "/responses"
}

// ChatStream sends one streaming Responses request and returns its chunks.
func (a *ResponsesAdapter) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if err := a.validateBaseURL(); err != nil {
		return nil, err
	}

	instructions, input := toResponsesInput(req.Messages)
	body := map[string]any{
		"model":  req.Model,
		"input":  input,
		"stream": true,
		"store":  false,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		body["tools"] = toResponsesTools(req.Tools)
	}

	// Merge provider-, model- and request-level extra payloads (later wins).
	// Reserved protocol fields are never replaced: see responsesReservedRequestFields.
	dropped := mergeExtraFields(body, a.extraPayload, responsesReservedRequestFields)
	dropped = append(dropped, mergeExtraFields(body, a.modelExtraPayloads[req.Model], responsesReservedRequestFields)...)
	dropped = append(dropped, mergeExtraFields(body, req.ExtraBody, responsesReservedRequestFields)...)
	a.logDroppedExtraFields(dropped)

	bodyBytes, err := marshalJSONNoEscape(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	a.logResponsesRequest(req, bodyBytes)

	responseCtx, cancel := context.WithCancel(ctx)

	resp, err := a.doWithRetry(responseCtx, func(ctx context.Context) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint(), bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		return httpReq, nil
	})
	if err != nil {
		cancel()
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		err := parseError(resp)
		cancel()
		return nil, err
	}

	streamBody, err := prepareStreamBody(responseCtx, resp, a.firstChunkTimeout)
	if err != nil {
		_ = resp.Body.Close()
		cancel()
		return nil, err
	}

	ch := make(chan llm.StreamChunk)
	go a.readResponsesStream(responseCtx, cancel, streamBody, ch)
	return ch, nil
}

func (a *ResponsesAdapter) logResponsesRequest(req llm.ChatRequest, bodyBytes []byte) {
	a.logRequest("openai responses request", a.endpoint(), req, bodyBytes)
}

// --- request types ---

// toResponsesInput converts ElBot messages into the Responses `instructions`
// string plus the ordered `input` items. System messages become instructions;
// assistant tool calls become function_call items and tool results become
// function_call_output items, because the Responses protocol replays a tool
// turn as separate items instead of nested tool_calls.
func toResponsesInput(msgs []llm.LLMMessage) (string, []map[string]any) {
	input := make([]map[string]any, 0, len(msgs))
	instructions := make([]string, 0, 1)
	for i := 0; i < len(msgs); {
		message := msgs[i]
		switch message.Role {
		case llm.RoleSystem:
			if text := llm.SegmentsContentText(message.Segments); strings.TrimSpace(text) != "" {
				instructions = append(instructions, text)
			}
			i++
		case llm.RoleTool:
			var imageCarrier []llm.MessageSegment
			for i < len(msgs) && msgs[i].Role == llm.RoleTool {
				toolMessage := msgs[i]
				content, images := openAIToolContent(toolMessage.Segments)
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": toolMessage.ToolCallID,
					"output":  content,
				})
				if len(images) > 0 {
					imageCarrier = append(imageCarrier, llm.MessageSegment{
						Type: llm.SegmentText,
						Text: fmt.Sprintf("以下图片来自工具 %s（tool_call_id: %s）：", toolMessage.Name, toolMessage.ToolCallID),
					})
					imageCarrier = append(imageCarrier, withOpenAIImageReferences(images)...)
				}
				i++
			}
			// Images cannot travel inside a function_call_output, so they follow
			// as a user message, exactly like the chat adapter.
			if len(imageCarrier) > 0 {
				input = append(input, responsesContentMessage(string(llm.RoleUser), imageCarrier))
			}
		case llm.RoleAssistant:
			segments := withOpenAIImageReferences(message.Segments)
			if parts := toResponsesContent(segments, string(llm.RoleAssistant)); len(parts) > 0 {
				input = append(input, map[string]any{
					"type":    "message",
					"role":    string(llm.RoleAssistant),
					"content": parts,
				})
			}
			for _, call := range message.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   call.ID,
					"name":      call.Name,
					"arguments": call.Arguments,
				})
			}
			i++
		default:
			input = append(input, responsesContentMessage(string(message.Role), withOpenAIImageReferences(message.Segments)))
			i++
		}
	}
	return strings.Join(instructions, "\n\n"), input
}

func responsesContentMessage(role string, segments []llm.MessageSegment) map[string]any {
	parts := toResponsesContent(segments, role)
	if len(parts) == 0 {
		// An empty content list is rejected upstream; an empty string is the
		// documented way to send a message without content.
		return map[string]any{"type": "message", "role": role, "content": ""}
	}
	return map[string]any{"type": "message", "role": role, "content": parts}
}

// toResponsesContent maps message segments to Responses content parts. Assistant
// history uses output_text, user/system history uses input_text; images are only
// valid on user messages, so assistant images stay as text references.
func toResponsesContent(segments []llm.MessageSegment, role string) []map[string]any {
	textType := "input_text"
	if role == string(llm.RoleAssistant) {
		textType = "output_text"
	}
	parts := make([]map[string]any, 0, len(segments))
	for _, segment := range segments {
		switch segment.Type {
		case llm.SegmentText:
			if segment.Text != "" {
				parts = append(parts, map[string]any{"type": textType, "text": segment.Text})
			}
		case llm.SegmentImage:
			if segment.URL == "" {
				continue
			}
			if role == string(llm.RoleAssistant) {
				continue
			}
			parts = append(parts, map[string]any{"type": "input_image", "image_url": segment.URL})
		case llm.SegmentFile:
			// TODO: 后续按厂商能力支持文件输入；当前与 chat 适配器一致，统一作为文本描述发送。
			if text := llm.SegmentsContentText([]llm.MessageSegment{segment}); text != "" {
				parts = append(parts, map[string]any{"type": textType, "text": text})
			}
		}
	}
	return parts
}

// toResponsesTools flattens the chat tool envelope: the Responses protocol puts
// name/description/parameters next to type instead of nesting them in
// "function".
func toResponsesTools(tools []llm.ToolSchema) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		toolType := tool.Type
		if toolType == "" {
			toolType = "function"
		}
		out = append(out, map[string]any{
			"type":        toolType,
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		})
	}
	return out
}

// --- stream types ---

type responsesStreamEvent struct {
	Type string `json:"type"`
	// The top-level error event carries code/message/param directly; some
	// gateways nest the same fields under "error" instead. Both are read.
	Code        any                `json:"code"`
	Message     string             `json:"message"`
	Param       any                `json:"param"`
	Delta       string             `json:"delta"`
	Arguments   string             `json:"arguments"`
	OutputIndex int                `json:"output_index"`
	Item        responsesItem      `json:"item"`
	Response    responsesResponse  `json:"response"`
	Error       *responsesAPIError `json:"error"`
}

type responsesItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesResponse struct {
	Status            string                     `json:"status"`
	Usage             *openAIUsage               `json:"usage"`
	Error             *responsesAPIError         `json:"error"`
	IncompleteDetails *responsesIncompleteDetail `json:"incomplete_details"`
}

type responsesIncompleteDetail struct {
	Reason string `json:"reason"`
}

type responsesAPIError struct {
	Code    any    `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Param   any    `json:"param"`
}

type responsesCallAccum struct {
	callID    string
	name      string
	arguments strings.Builder
}

// --- stream reading ---

func (a *ResponsesAdapter) readResponsesStream(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser, ch chan<- llm.StreamChunk) {
	defer close(ch)
	defer body.Close()
	defer cancel()

	lines := make(chan streamLine, 1)
	go scanStreamLines(ctx, body, lines)

	calls := map[int]*responsesCallAccum{}
	var callOrder []int
	seenData := false
	seenTerminal := false
	timeout := a.firstChunkTimeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if seenData {
				ch <- llm.StreamChunk{Error: fmt.Errorf("LLM stream idle timeout after %s", a.streamIdleTimeout)}
			} else {
				ch <- llm.StreamChunk{Error: fmt.Errorf("LLM first stream chunk timeout after %s", a.firstChunkTimeout)}
			}
			return
		case item, ok := <-lines:
			if !ok {
				if !seenTerminal {
					ch <- llm.StreamChunk{Error: fmt.Errorf("read stream: %w", io.ErrUnexpectedEOF)}
				}
				return
			}
			if item.err != nil {
				ch <- llm.StreamChunk{Error: fmt.Errorf("read stream: %w", item.err)}
				return
			}

			line := item.line
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// The Responses stream is terminated by response.completed rather
			// than by the chat-completions sentinel, but gateways in front of it
			// sometimes still append one.
			if line == "data: [DONE]" {
				return
			}

			const prefix = "data: "
			if !strings.HasPrefix(line, prefix) {
				// Named "event: <type>" lines carry no payload of their own.
				continue
			}
			data := strings.TrimPrefix(line, prefix)
			seenData = true
			timeout = a.streamIdleTimeout
			resetTimer(timer, timeout)

			var event responsesStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				ch <- llm.StreamChunk{Error: fmt.Errorf("parse stream chunk: %w", err)}
				return
			}

			switch event.Type {
			case "response.output_text.delta":
				if event.Delta != "" {
					ch <- llm.StreamChunk{DeltaContent: event.Delta}
				}
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				if event.Delta != "" {
					ch <- llm.StreamChunk{DeltaReasoningContent: event.Delta}
				}
			case "response.output_item.added", "response.output_item.done":
				accumulateResponsesCall(calls, &callOrder, event)
			case "response.function_call_arguments.delta":
				accumulateResponsesArguments(calls, &callOrder, event.OutputIndex, event.Delta, false)
			case "response.function_call_arguments.done":
				// The done event repeats the complete arguments, so it replaces
				// whatever the deltas accumulated instead of extending it.
				accumulateResponsesArguments(calls, &callOrder, event.OutputIndex, event.Arguments, true)
			case "response.completed", "response.incomplete":
				seenTerminal = true
				ch <- responsesTerminalChunk(event, calls, callOrder)
				return
			case "response.failed":
				seenTerminal = true
				ch <- llm.StreamChunk{Error: responsesFailureError(event)}
				return
			case "error":
				seenTerminal = true
				ch <- llm.StreamChunk{Error: responsesStreamError(event)}
				return
			default:
				// response.created / in_progress / output_text.done and other
				// lifecycle events carry nothing ElBot consumes incrementally.
			}
		}
	}
}

func accumulateResponsesCall(calls map[int]*responsesCallAccum, order *[]int, event responsesStreamEvent) {
	if event.Item.Type != "function_call" {
		return
	}
	acc := responsesCall(calls, order, event.OutputIndex)
	if event.Item.CallID != "" {
		acc.callID = event.Item.CallID
	}
	if event.Item.Name != "" {
		acc.name = event.Item.Name
	}
	// output_item.done repeats the complete arguments, which are authoritative
	// over anything accumulated from deltas.
	if event.Item.Arguments != "" {
		acc.arguments.Reset()
		acc.arguments.WriteString(event.Item.Arguments)
	}
}

func accumulateResponsesArguments(calls map[int]*responsesCallAccum, order *[]int, index int, arguments string, replace bool) {
	acc := responsesCall(calls, order, index)
	if replace {
		acc.arguments.Reset()
	}
	acc.arguments.WriteString(arguments)
}

func responsesCall(calls map[int]*responsesCallAccum, order *[]int, index int) *responsesCallAccum {
	acc := calls[index]
	if acc == nil {
		acc = &responsesCallAccum{}
		calls[index] = acc
		*order = append(*order, index)
	}
	return acc
}

// responsesTerminalChunk emits the accumulated tool calls exactly once, with the
// finish reason the rest of the pipeline logs. Emitting them earlier (per
// output_item.done) would make the agent execute the same call twice.
func responsesTerminalChunk(event responsesStreamEvent, calls map[int]*responsesCallAccum, order []int) llm.StreamChunk {
	chunk := llm.StreamChunk{}
	if event.Response.Usage != nil {
		chunk.Usage = toUsage(event.Response.Usage)
	}
	for _, index := range order {
		acc := calls[index]
		if acc == nil {
			continue
		}
		chunk.ToolCallDeltas = append(chunk.ToolCallDeltas, llm.ToolCallDelta{
			Index: index,
			ID:    acc.callID,
			Name:  acc.name,
			Args:  acc.arguments.String(),
		})
	}
	switch {
	case event.Type == "response.incomplete":
		chunk.FinishReason = responsesIncompleteFinishReason(event)
	case len(chunk.ToolCallDeltas) > 0:
		chunk.FinishReason = "tool_calls"
	default:
		chunk.FinishReason = "stop"
	}
	return chunk
}

// responsesIncompleteFinishReason keeps the truncation visible in logs: the
// output was cut off, so it is not a normal "stop".
func responsesIncompleteFinishReason(event responsesStreamEvent) string {
	if event.Response.IncompleteDetails != nil && strings.EqualFold(event.Response.IncompleteDetails.Reason, "max_output_tokens") {
		return "length"
	}
	return "incomplete"
}

func responsesFailureError(event responsesStreamEvent) error {
	if event.Response.Error != nil {
		return responsesAPIErrorToLLM(event.Response.Error)
	}
	if event.Error != nil {
		return responsesAPIErrorToLLM(event.Error)
	}
	return errors.New("responses stream failed without an error payload")
}

// responsesStreamError reads the "error" event, which carries code/message/param
// at the top level of the event (gateways that nest them under "error" are also
// accepted).
func responsesStreamError(event responsesStreamEvent) error {
	if event.Error != nil {
		return responsesAPIErrorToLLM(event.Error)
	}
	if strings.TrimSpace(event.Message) != "" || event.Code != nil {
		return responsesAPIErrorToLLM(&responsesAPIError{Code: event.Code, Message: event.Message, Param: event.Param})
	}
	return errors.New("responses stream reported an error without details")
}

// responsesAPIErrorToLLM keeps the upstream code/type/param so callers classify
// the failure without matching on message text. There is no HTTP status on a
// stream-level event, so classification relies on the machine-readable fields.
func responsesAPIErrorToLLM(raw *responsesAPIError) error {
	if raw == nil {
		return errors.New("empty responses error payload")
	}
	code := stringifyErrorField(raw.Code)
	typeName := strings.TrimSpace(raw.Type)
	param := stringifyErrorField(raw.Param)
	message := strings.TrimSpace(raw.Message)
	if message == "" {
		message = "responses request failed"
	}
	return &llm.APIError{
		Code:     code,
		Type:     typeName,
		Param:    param,
		Message:  responseSummary([]byte(message)),
		Category: classifyAPIError(0, code, typeName, param, message),
	}
}
