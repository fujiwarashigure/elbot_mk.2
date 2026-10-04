package agent

import (
	"errors"
	"testing"

	"elbot/internal/llm"
)

func imageMessages() []llm.LLMMessage {
	return []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{
		{Type: llm.SegmentText, Text: "看看"},
		{Type: llm.SegmentImage, URL: "https://example.com/a.jpg"},
	}}}
}

// The fallback decision must be driven by the adapter-controlled Category.
// Everything below simulates what internal/llm/openai sets after parsing a real
// response; the prose dialects themselves are covered by that package's tests.
func TestShouldFallbackVisionOnVisionUnsupportedCategory(t *testing.T) {
	err := &llm.APIError{
		StatusCode: 400,
		Type:       "invalid_request_error",
		Param:      "messages",
		Message:    "model gpt-text does not support image input",
		Category:   llm.ErrorCategoryVisionUnsupported,
	}
	if !shouldFallbackVision(imageMessages(), err) {
		t.Fatal("expected fallback for an image-unsupported category")
	}
}

func TestShouldFallbackVisionOnStructuredNotFoundSignal(t *testing.T) {
	err := &llm.APIError{
		StatusCode: 404,
		Code:       "unsupported_image_input",
		Message:    "no image route",
		Category:   llm.ErrorCategoryVisionUnsupported,
	}
	if !shouldFallbackVision(imageMessages(), err) {
		t.Fatal("an explicit structured 404 image signal should trigger the fallback")
	}
}

func TestShouldFallbackVisionIgnoresUnrelatedParameter(t *testing.T) {
	// Same status, same type, but the adapter classified it as a request-field
	// failure: stripping the image would be wrong.
	err := &llm.APIError{
		StatusCode: 400,
		Type:       "invalid_request_error",
		Param:      "temperature",
		Message:    "temperature must be less than or equal to 2",
		Category:   llm.ErrorCategoryInvalidRequest,
	}
	if shouldFallbackVision(imageMessages(), err) {
		t.Fatal("an unrelated 400 must not strip images and retry")
	}
}

func TestShouldFallbackVisionIgnoresNotFoundModelNameWithImage(t *testing.T) {
	// "image" here is part of the model name, not evidence that vision failed.
	// The classification never reads Message, so this cannot misfire.
	err := &llm.APIError{
		StatusCode: 404,
		Message:    "model image-chat-v2 not found",
		Category:   llm.ErrorCategoryModelNotFound,
	}
	if shouldFallbackVision(imageMessages(), err) {
		t.Fatal("a 404 whose model name contains 'image' must not trigger the fallback")
	}
}

func TestShouldFallbackVisionIgnoresRetryableStatus(t *testing.T) {
	// Defensive: a rate limit must stay a rate limit even if a gateway tagged it
	// with the vision category.
	err := &llm.APIError{StatusCode: 429, Message: "rate limited while sending image", Category: llm.ErrorCategoryVisionUnsupported}
	if shouldFallbackVision(imageMessages(), err) {
		t.Fatal("retryable statuses must not be treated as a vision capability error")
	}
	for _, status := range []int{401, 403, 408, 500, 503} {
		err := &llm.APIError{StatusCode: status, Category: llm.ErrorCategoryVisionUnsupported}
		if shouldFallbackVision(imageMessages(), err) {
			t.Fatalf("status %d must not trigger the vision fallback", status)
		}
	}
}

func TestShouldFallbackVisionRequiresImageSegment(t *testing.T) {
	textOnly := []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hi")}}
	err := &llm.APIError{StatusCode: 400, Category: llm.ErrorCategoryVisionUnsupported}
	if shouldFallbackVision(textOnly, err) {
		t.Fatal("a request without images must never trigger the vision fallback")
	}
}

func TestShouldFallbackVisionIgnoresUncategorizedErrors(t *testing.T) {
	plain := errors.New("HTTP 400: the provided messages input is invalid [unexpected item type in content]")
	if shouldFallbackVision(imageMessages(), plain) {
		t.Fatal("unclassified text errors must not trigger the fallback; the adapter owns that mapping")
	}
	uncategorized := &llm.APIError{StatusCode: 400, Message: "model gpt-text does not support image input"}
	if shouldFallbackVision(imageMessages(), uncategorized) {
		t.Fatal("an APIError without a category must not trigger the fallback")
	}
}
