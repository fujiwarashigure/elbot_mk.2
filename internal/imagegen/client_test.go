package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateBase64Response(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	var gotPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		gotPrompt, _ = body["prompt"].(string)
		if body["model"] != "gpt-image-2.5" {
			t.Errorf("model = %v", body["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
	defer server.Close()

	client := New(Config{
		Enabled:   true,
		BaseURL:   server.URL + "/v1",
		APIKeyEnv: "TEST_IMAGE_KEY",
		Model:     "gpt-image-2.5",
	}, func(name string) (string, bool) {
		if name == "TEST_IMAGE_KEY" {
			return "test-key", true
		}
		return "", false
	})
	result, err := client.Generate(context.Background(), Request{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotPrompt != "a cat" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	if string(result.Data) != string(png) || result.MIMEType != "image/png" {
		t.Fatalf("result = %#v mime=%q", result.Data, result.MIMEType)
	}
}

func TestGenerateURLFallbackAndError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"url": "http://" + r.Host + "/image.png"}},
			})
		case "/image.png":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg-bytes"))
		case "/fail":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "bad prompt"}})
		}
	}))
	defer server.Close()

	client := New(Config{Enabled: true, BaseURL: server.URL + "/v1", APIKey: "k", OutputFormat: "jpeg"}, nil)
	result, err := client.Generate(context.Background(), Request{Prompt: "x"})
	if err != nil {
		t.Fatalf("Generate url: %v", err)
	}
	if string(result.Data) != "jpeg-bytes" || result.MIMEType != "image/jpeg" {
		t.Fatalf("result = %q %q", result.Data, result.MIMEType)
	}

	failing := New(Config{Enabled: true, Endpoint: server.URL + "/fail", APIKey: "k"}, nil)
	_, err = failing.Generate(context.Background(), Request{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "bad prompt") {
		t.Fatalf("err = %v", err)
	}
}

func TestGenerateRequiresAPIKey(t *testing.T) {
	client := New(Config{Enabled: true, BaseURL: "http://127.0.0.1:1/v1"}, nil)
	if _, err := client.Generate(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("err = %v", err)
	}
}
