package protocol

import (
	"encoding/json"
	"testing"

	"elbot/internal/hook"
)

func TestFrameRoundTripAndIDValidation(t *testing.T) {
	ok := true
	want := Frame{Type: "response", ID: "host:event", OK: &ok, Result: json.RawMessage(`{"status":"completed"}`)}
	data, err := EncodeFrame(want)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	got, err := DecodeFrame(data)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if got.Type != want.Type || got.ID != want.ID || got.OK == nil || !*got.OK || string(got.Result) != string(want.Result) {
		t.Fatalf("Frame = %#v", got)
	}
	if err := ValidateID(got.ID, "host:"); err != nil {
		t.Fatalf("ValidateID: %v", err)
	}
	if err := ValidateID("plugin:event", "host:"); err == nil {
		t.Fatal("ValidateID accepted wrong prefix")
	}
}

func TestNewRequestEncodesParams(t *testing.T) {
	request, err := NewRequest("host:event", "event.handle", map[string]string{"value": "ok"})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if request.Type != "request" || request.ID != "host:event" || request.Method != "event.handle" || string(request.Params) != `{"value":"ok"}` {
		t.Fatalf("request = %#v", request)
	}
}

func TestApplyLLMResultMergesTemporaryFields(t *testing.T) {
	temp := 0.7
	maxTokens := 512
	event := hook.Event{LLM: hook.LLMPayload{SystemAppend: []string{"old"}}}
	event = ApplyLLMResult(event, &LLMResult{
		SystemAppend: []string{"new", " "},
		Temperature:  &temp,
		MaxTokens:    &maxTokens,
		ExtraBody:    map[string]any{"reasoning_effort": "low"},
	})
	if len(event.LLM.SystemAppend) != 2 || event.LLM.SystemAppend[1] != "new" {
		t.Fatalf("SystemAppend = %#v", event.LLM.SystemAppend)
	}
	if event.LLM.Temperature == nil || *event.LLM.Temperature != temp {
		t.Fatalf("Temperature = %#v", event.LLM.Temperature)
	}
	if event.LLM.MaxTokens == nil || *event.LLM.MaxTokens != maxTokens {
		t.Fatalf("MaxTokens = %#v", event.LLM.MaxTokens)
	}
	if event.LLM.ExtraBody["reasoning_effort"] != "low" {
		t.Fatalf("ExtraBody = %#v", event.LLM.ExtraBody)
	}
}
