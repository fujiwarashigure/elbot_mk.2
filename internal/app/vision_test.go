package app

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/llm"
)

func TestBuildVisionDescriberGating(t *testing.T) {
	model := &recordingVisionLLM{}
	models := ModelClients{ByProvider: map[string]llm.LLM{"openai": model}}

	cfg := config.Default()
	if value, err := buildVisionDescriber(context.Background(), cfg, models, nil); value != nil || err != nil {
		t.Fatalf("disabled vision fallback = %v, %v", value, err)
	}

	enabled := true
	cfg.Vision.Enabled = &enabled
	cfg.Vision.Provider = "openai"
	cfg.Vision.Model = "gpt-4o-mini"
	value, err := buildVisionDescriber(context.Background(), cfg, models, nil)
	if value == nil || err != nil {
		t.Fatalf("configured vision fallback = %v, %v", value, err)
	}
	if _, err := buildVisionDescriber(context.Background(), cfg, ModelClients{}, nil); err == nil {
		t.Fatal("an unknown provider should fail loudly")
	}
	cfg.Vision.Model = ""
	if _, err := buildVisionDescriber(context.Background(), cfg, models, nil); err == nil {
		t.Fatal("an enabled but incomplete config should fail")
	}
}

func TestVisionChatDescriberDescribesImage(t *testing.T) {
	model := &recordingVisionLLM{chunks: []llm.StreamChunk{{DeltaContent: "一只猫"}, {FinishReason: "stop"}}}
	models := ModelClients{ByProvider: map[string]llm.LLM{"openai": model}}
	cfg := config.Default()
	enabled := true
	cfg.Vision.Enabled = &enabled
	cfg.Vision.Provider = "openai"
	cfg.Vision.Model = "gpt-4o-mini"

	describer, err := buildVisionDescriber(context.Background(), cfg, models, nil)
	if err != nil {
		t.Fatalf("buildVisionDescriber: %v", err)
	}
	mediaID := "media:" + strings.Repeat("a", 64)
	text, err := describer.DescribeImage(context.Background(), mediaID, appTestPNG(t, 8, 8), "image/png")
	if err != nil {
		t.Fatalf("DescribeImage: %v", err)
	}
	if text != "一只猫" {
		t.Fatalf("description = %q", text)
	}
	if len(model.requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(model.requests))
	}
	request := model.requests[0]
	if request.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q", request.Model)
	}
	found := false
	for _, segment := range request.Messages[1].Segments {
		if segment.Type == llm.SegmentImage && strings.HasPrefix(segment.URL, "data:image/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("request did not carry an image data URL: %#v", request.Messages)
	}
}
