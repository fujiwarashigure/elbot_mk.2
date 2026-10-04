package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elbot/internal/agent"
	"elbot/internal/config"
)

func asrBoolPtr(value bool) *bool { return &value }

func TestBuildAudioTranscriberGating(t *testing.T) {
	base := config.Default()
	if value, err := buildAudioTranscriber(context.Background(), base); value != nil || err != nil {
		t.Fatalf("disabled asr = %#v err=%v", value, err)
	}
	base.ASR = config.ASRConfig{Enabled: asrBoolPtr(true)}
	if _, err := buildAudioTranscriber(context.Background(), base); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete asr err = %v", err)
	}
	base.ASR = config.ASRConfig{Enabled: asrBoolPtr(true), Provider: "missing", Model: "whisper"}
	if _, err := buildAudioTranscriber(context.Background(), base); err == nil || !strings.Contains(err.Error(), "no [providers.missing]") {
		t.Fatalf("missing provider err = %v", err)
	}
	base.Providers["p"] = config.ProviderConfig{
		BaseURL:      "https://example.com/v1",
		ModelConfigs: map[string]config.ModelConfig{"declared-off": {Audio: asrBoolPtr(false)}},
	}
	base.ASR = config.ASRConfig{Enabled: asrBoolPtr(true), Provider: "p", Model: "declared-off"}
	if _, err := buildAudioTranscriber(context.Background(), base); err == nil || !strings.Contains(err.Error(), "audio = false") {
		t.Fatalf("declared audio=false err = %v", err)
	}
}

func TestBuildAudioTranscriberUploads(t *testing.T) {
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		gotModel = r.FormValue("model")
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "适配器转写"})
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Providers["local"] = config.ProviderConfig{BaseURL: server.URL + "/v1"}
	cfg.ASR = config.ASRConfig{Enabled: asrBoolPtr(true), Provider: "local", Model: "whisper-local"}
	transcriber, err := buildAudioTranscriber(context.Background(), cfg)
	if err != nil {
		t.Fatalf("buildAudioTranscriber: %v", err)
	}
	result, err := transcriber.Transcribe(context.Background(), agent.AudioTranscriptionRequest{
		MediaID:  "media:test",
		Data:     []byte("audio"),
		Name:     "voice.amr",
		MIMEType: "audio/amr",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if result.Text != "适配器转写" || result.Provider != "local" || result.Model != "whisper-local" {
		t.Fatalf("result = %#v", result)
	}
	if gotModel != "whisper-local" {
		t.Fatalf("model = %q", gotModel)
	}
}
