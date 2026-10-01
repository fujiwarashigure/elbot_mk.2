package protocol

import (
	"encoding/json"
	"fmt"
	"strings"

	"elbot/internal/hook"
	hookoutput "elbot/internal/hook/output"
)

const Version = "hook.v2"

type Frame struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	OK     *bool           `json:"ok,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type EventResultBase struct {
	Status string `json:"status"`
	hookoutput.Group
	PassThrough *bool          `json:"pass_through,omitempty"`
	Message     *MessageResult `json:"message,omitempty"`
	LLM         *LLMResult     `json:"llm,omitempty"`
}

type MessageResult struct {
	Text     *string                      `json:"text,omitempty"`
	Segments *[]hookoutput.MessageSegment `json:"segments,omitempty"`
}

// LLMResult carries request-scoped, temporary LLM changes. The fields are only
// consumed at llm.turn.prepared and llm.request.prepared, and are never
// persisted into session history.
type LLMResult struct {
	SystemAppend []string       `json:"system_append,omitempty"`
	Temperature  *float64       `json:"temperature,omitempty"`
	MaxTokens    *int           `json:"max_tokens,omitempty"`
	ExtraBody    map[string]any `json:"extra_body,omitempty"`
}

// ApplyLLMResult merges process-Hook LLM changes into an event.
func ApplyLLMResult(event hook.Event, result *LLMResult) hook.Event {
	if result == nil {
		return event
	}
	for _, part := range result.SystemAppend {
		if strings.TrimSpace(part) != "" {
			event.LLM.SystemAppend = append(event.LLM.SystemAppend, part)
		}
	}
	if result.Temperature != nil {
		event.LLM.Temperature = result.Temperature
	}
	if result.MaxTokens != nil {
		event.LLM.MaxTokens = result.MaxTokens
	}
	if len(result.ExtraBody) > 0 {
		if event.LLM.ExtraBody == nil {
			event.LLM.ExtraBody = map[string]any{}
		}
		for key, value := range result.ExtraBody {
			event.LLM.ExtraBody[key] = value
		}
	}
	return event
}

type EventHandleParams struct {
	Event hook.Event        `json:"event"`
	Match hook.MatchContext `json:"match,omitempty"`
}

func NewRequest(id, method string, params any) (Frame, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: "request", ID: id, Method: method, Params: raw}, nil
}

func DecodeFrame(data []byte) (Frame, error) {
	var frame Frame
	if err := json.Unmarshal(data, &frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func EncodeFrame(frame any) ([]byte, error) {
	return json.Marshal(frame)
}

func ValidateID(id, prefix string) error {
	if !strings.HasPrefix(id, prefix) {
		return fmt.Errorf("frame id %q must use %s prefix", id, prefix)
	}
	return nil
}
