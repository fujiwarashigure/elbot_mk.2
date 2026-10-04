package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"elbot/internal/llm/openai"
	"elbot/internal/media"
	"elbot/internal/storage/sqlite"
	"elbot/internal/tool"
	"elbot/internal/tool/builtin"
	"elbot/internal/vision"
)

// TestImageToPromptSmokeEndToEnd wires the real OpenAI-compatible adapter, the
// describer and the built-in tool against a local SSE server. It proves the Go
// path works without touching any external endpoint: image bytes are read from
// Media Center, downscaled, sent as a data URL, and the prompt comes back.
func TestImageToPromptSmokeEndToEnd(t *testing.T) {
	var (
		mu       sync.Mutex
		payloads []map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			mu.Lock()
			payloads = append(payloads, payload)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"id":"1","choices":[{"index":0,"delta":{"content":"a lone lighthouse"},"finish_reason":null}]}`+"\n\n")
		io.WriteString(w, `data: {"id":"2","choices":[{"index":0,"delta":{"content":" at dusk, cinematic lighting"},"finish_reason":"stop"}]}`+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	client, err := openai.NewWithOptions(server.URL, "test-key", nil, nil, openai.RequestOptions{})
	if err != nil {
		t.Fatalf("new openai adapter: %v", err)
	}
	service := vision.New(vision.Options{
		Client:      client,
		Provider:    "test",
		Model:       "vision-1",
		MaxTokens:   200,
		Temperature: 0.2,
		MaxEdge:     64,
	})

	ctx := context.Background()
	store, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	root := t.TempDir()
	center := media.NewManager(store, root, &media.LocalBackend{Root: root})
	item, err := center.ImportBytes(ctx, appTestPNG(t, 200, 100), media.Input{Name: "ref.png", MIMEType: "image/png"})
	if err != nil {
		t.Fatalf("import media: %v", err)
	}

	value := builtin.NewImageToPromptTool(center, service)
	args, _ := json.Marshal(map[string]string{"image": item.ID, "target": "flux", "language": "en"})
	result, err := value.Call(ctx, tool.CallRequest{Name: builtin.ImageToPromptName, Arguments: args})
	if err != nil {
		t.Fatalf("call image_to_prompt: %v", err)
	}
	if result.Content != "a lone lighthouse at dusk, cinematic lighting" {
		t.Fatalf("content = %q", result.Content)
	}

	mu.Lock()
	count := len(payloads)
	var payload map[string]any
	if count > 0 {
		payload = payloads[0]
	}
	mu.Unlock()
	if count != 1 {
		t.Fatalf("vision requests = %d, want 1", count)
	}
	if payload["model"] != "vision-1" {
		t.Fatalf("model = %#v", payload["model"])
	}
	if url := firstImageURL(t, payload); !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("image url = %q", url)
	}

	// The same media + target + language must be served from the tool cache.
	if _, err := value.Call(ctx, tool.CallRequest{Name: builtin.ImageToPromptName, Arguments: args}); err != nil {
		t.Fatalf("cached call: %v", err)
	}
	mu.Lock()
	count = len(payloads)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("cached call reached the vision server: %d requests", count)
	}
}

func firstImageURL(t *testing.T, payload map[string]any) string {
	t.Helper()
	messages, ok := payload["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %#v", payload["messages"])
	}
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok || part["type"] != "image_url" {
				continue
			}
			imageURL, ok := part["image_url"].(map[string]any)
			if !ok {
				continue
			}
			if value, ok := imageURL["url"].(string); ok {
				return value
			}
		}
	}
	t.Fatalf("no image_url part in %#v", payload)
	return ""
}
