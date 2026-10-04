package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/vision"
)

type recordingVisionLLM struct {
	requests []llm.ChatRequest
	chunks   []llm.StreamChunk
	err      error
}

func (r *recordingVisionLLM) ChatStream(_ context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	r.requests = append(r.requests, req)
	if r.err != nil {
		return nil, r.err
	}
	ch := make(chan llm.StreamChunk, len(r.chunks))
	for _, chunk := range r.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (r *recordingVisionLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func appTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 64, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, source); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return output.Bytes()
}

func TestBuildImagePromptServiceGating(t *testing.T) {
	model := &recordingVisionLLM{}
	models := ModelClients{ByProvider: map[string]llm.LLM{"openai": model}}

	cfg := config.Default()
	if value, err := buildImagePromptService(context.Background(), cfg, models, nil); value != nil || err != nil {
		t.Fatalf("unconfigured image_to_prompt = %v, %v", value, err)
	}
	cfg.ImageToPrompt.Provider = "openai"
	cfg.ImageToPrompt.Model = "gpt-4o-mini"
	if value, err := buildImagePromptService(context.Background(), cfg, models, nil); value == nil || err != nil {
		t.Fatalf("configured image_to_prompt = %v, %v", value, err)
	}
	if _, err := buildImagePromptService(context.Background(), cfg, ModelClients{}, nil); err == nil {
		t.Fatal("unknown provider should fail loudly instead of silently disabling the tool")
	}
	disabled := false
	cfg.ImageToPrompt.Enabled = &disabled
	if value, err := buildImagePromptService(context.Background(), cfg, models, nil); value != nil || err != nil {
		t.Fatalf("enabled=false = %v, %v", value, err)
	}
	enabled := true
	cfg.ImageToPrompt.Enabled = &enabled
	cfg.ImageToPrompt.Model = ""
	if _, err := buildImagePromptService(context.Background(), cfg, models, nil); err == nil {
		t.Fatal("explicitly enabled but incomplete config should fail")
	}
}

// TestImagePromptServiceSendsDrawingPromptRequest pins the wiring between the
// image_to_prompt config and the shared vision engine: the configured model,
// token/temperature budget, the drawing-prompt template and the downscaled
// data URL must all arrive at the provider.
func TestImagePromptServiceSendsDrawingPromptRequest(t *testing.T) {
	client := &recordingVisionLLM{chunks: []llm.StreamChunk{{DeltaContent: "a red cube"}, {DeltaContent: " on a wooden table"}}}
	cfg := config.Default()
	cfg.Providers = map[string]config.ProviderConfig{"openai": {}}
	cfg.ImageToPrompt.Provider = "openai"
	cfg.ImageToPrompt.Model = "vision-1"
	cfg.ImageToPrompt.MaxTokens = 321
	temperature := 0.15
	cfg.ImageToPrompt.Temperature = &temperature
	cfg.ImageToPrompt.MaxEdge = 64
	service, err := buildImagePromptService(context.Background(), cfg, ModelClients{ByProvider: map[string]llm.LLM{"openai": client}}, nil)
	if err != nil {
		t.Fatalf("buildImagePromptService: %v", err)
	}

	input := appTestPNG(t, 200, 100)
	result, err := service.Describe(context.Background(), vision.Request{
		MediaID:  "media:" + strings.Repeat("a", 64),
		Data:     input,
		MIMEType: "image/png",
		Prompt:   vision.ImagePrompt("sdxl", "en"),
	})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if result.Text != "a red cube on a wooden table" {
		t.Fatalf("text = %q", result.Text)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	request := client.requests[0]
	if request.Model != "vision-1" || request.MaxTokens != 321 || request.Temperature != 0.15 {
		t.Fatalf("request = %#v", request)
	}
	if len(request.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(request.Messages))
	}
	if text := llm.SegmentsTextOnly(request.Messages[0].Segments); !strings.Contains(text, "提示词") {
		t.Fatalf("system prompt = %q", text)
	}
	user := request.Messages[1]
	if text := llm.SegmentsTextOnly(user.Segments); !strings.Contains(text, "SDXL") || !strings.Contains(text, "English") {
		t.Fatalf("user instruction = %q", text)
	}
	imageSegment := imageSegmentOf(t, user.Segments)
	if imageSegment.MIMEType != "image/jpeg" || !strings.HasPrefix(imageSegment.URL, "data:image/jpeg;base64,") {
		t.Fatalf("image segment = mime %q url %q", imageSegment.MIMEType, imageSegment.URL)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(imageSegment.URL, "data:image/jpeg;base64,"))
	if err != nil || len(decoded) == 0 {
		t.Fatalf("decode image data url: err=%v len=%d", err, len(decoded))
	}
}

func imageSegmentOf(t *testing.T, segments []llm.MessageSegment) llm.MessageSegment {
	t.Helper()
	for _, segment := range segments {
		if segment.Type == llm.SegmentImage {
			return segment
		}
	}
	t.Fatalf("no image segment in %#v", segments)
	return llm.MessageSegment{}
}
