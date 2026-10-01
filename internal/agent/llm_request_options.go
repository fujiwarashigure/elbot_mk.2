package agent

import (
	"strings"

	"elbot/internal/hook"
)

// llmRequestOptions carries temporary request-scoped changes returned by Hook
// handlers. None of these values are persisted into session history.
type llmRequestOptions struct {
	SystemAppend []string
	Temperature  *float64
	MaxTokens    *int
	ExtraBody    map[string]any
}

func llmRequestOptionsFromPayload(payload hook.LLMPayload) llmRequestOptions {
	options := llmRequestOptions{
		Temperature: payload.Temperature,
		MaxTokens:   payload.MaxTokens,
		ExtraBody:   cloneAnyMap(payload.ExtraBody),
	}
	for _, part := range payload.SystemAppend {
		if part = strings.TrimSpace(part); part != "" {
			options.SystemAppend = append(options.SystemAppend, part)
		}
	}
	return options
}

func mergeLLMRequestOptions(base, overlay llmRequestOptions) llmRequestOptions {
	merged := base
	if len(overlay.SystemAppend) > 0 {
		merged.SystemAppend = append(append([]string(nil), base.SystemAppend...), overlay.SystemAppend...)
	}
	if overlay.Temperature != nil {
		merged.Temperature = overlay.Temperature
	}
	if overlay.MaxTokens != nil {
		merged.MaxTokens = overlay.MaxTokens
	}
	if len(overlay.ExtraBody) > 0 {
		merged.ExtraBody = make(map[string]any, len(base.ExtraBody)+len(overlay.ExtraBody))
		for key, value := range base.ExtraBody {
			merged.ExtraBody[key] = value
		}
		for key, value := range overlay.ExtraBody {
			merged.ExtraBody[key] = value
		}
	}
	return merged
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
