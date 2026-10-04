package asr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestService(t *testing.T, baseURL string, mutate func(*Options)) *Service {
	t.Helper()
	opts := Options{
		BaseURL:           baseURL,
		APIKey:            "test-key",
		Model:             "whisper-test",
		Provider:          "test",
		Language:          "zh",
		Prompt:            "人名：小明",
		RetryInitialDelay: time.Millisecond,
		BaseContext:       context.Background(),
	}
	if mutate != nil {
		mutate(&opts)
	}
	service := New(opts)
	t.Cleanup(service.Close)
	return service
}

func transcribeRequest(data []byte) Request {
	return Request{MediaID: "media:test", Data: data, Name: "voice.amr", MIMEType: "audio/amr"}
}

func TestTranscribeSendsMultipartAndParsesResult(t *testing.T) {
	var gotModel, gotLanguage, gotPrompt, gotFile string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("content type = %q %v", r.Header.Get("Content-Type"), err)
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			body, _ := io.ReadAll(part)
			switch part.FormName() {
			case "model":
				gotModel = string(body)
			case "language":
				gotLanguage = string(body)
			case "prompt":
				gotPrompt = string(body)
			case "file":
				gotFile = string(body)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"text": " 你好 \n世界 ", "language": "zh", "duration": 1.5})
	}))
	defer server.Close()

	service := newTestService(t, server.URL+"/v1", nil)
	result, err := service.Transcribe(context.Background(), transcribeRequest([]byte("audio-bytes")))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if result.Text != "你好 世界" || result.Language != "zh" || result.Provider != "test" || result.Model != "whisper-test" {
		t.Fatalf("result = %#v", result)
	}
	if result.Duration != 1500*time.Millisecond {
		t.Fatalf("duration = %v", result.Duration)
	}
	if gotModel != "whisper-test" || gotLanguage != "zh" || gotPrompt != "人名：小明" || gotFile != "audio-bytes" {
		t.Fatalf("multipart model=%q language=%q prompt=%q file=%q", gotModel, gotLanguage, gotPrompt, gotFile)
	}
}

func TestTranscribeCachesSuccessfulResult(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "缓存内容"})
	}))
	defer server.Close()

	service := newTestService(t, server.URL+"/v1", nil)
	first, err := service.Transcribe(context.Background(), transcribeRequest([]byte("audio")))
	if err != nil || first.Cached {
		t.Fatalf("first = %#v err=%v", first, err)
	}
	second, err := service.Transcribe(context.Background(), transcribeRequest([]byte("audio")))
	if err != nil || !second.Cached || second.Text != "缓存内容" {
		t.Fatalf("second = %#v err=%v", second, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestTranscribeRetriesTransientFailure(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, `{"error":{"message":"busy"}}`, http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "重试成功"})
	}))
	defer server.Close()

	service := newTestService(t, server.URL+"/v1", func(opts *Options) {
		opts.MaxRetries = 1
		opts.RetryInitialDelay = time.Millisecond
	})
	result, err := service.Transcribe(context.Background(), transcribeRequest([]byte("audio")))
	if err != nil || result.Text != "重试成功" {
		t.Fatalf("result = %#v err=%v", result, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
}

func TestTranscribeCachesDeterministicFailure(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_api_key","message":"bad key"}}`))
	}))
	defer server.Close()

	service := newTestService(t, server.URL+"/v1", nil)
	for i := 0; i < 2; i++ {
		_, err := service.Transcribe(context.Background(), transcribeRequest([]byte("audio")))
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized || !strings.Contains(httpErr.Message, "invalid_api_key") {
			t.Fatalf("call %d error = %v", i, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestTranscribeRejectsOversizedAudioBeforeUpload(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	service := newTestService(t, server.URL+"/v1", func(opts *Options) {
		opts.MaxAudioBytes = 4
	})
	if _, err := service.Transcribe(context.Background(), transcribeRequest([]byte("12345"))); err == nil || !strings.Contains(err.Error(), "over the 4 byte limit") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
}

func TestTranscribeRejectsEmptyTranscript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "  \n "})
	}))
	defer server.Close()

	service := newTestService(t, server.URL+"/v1", nil)
	if _, err := service.Transcribe(context.Background(), transcribeRequest([]byte("audio"))); err == nil || !strings.Contains(err.Error(), "empty transcript") {
		t.Fatalf("err = %v", err)
	}
}

func TestTranscribeBoundsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"text":"` + strings.Repeat("雨", 64) + `"}`))
	}))
	defer server.Close()

	service := newTestService(t, server.URL+"/v1", func(opts *Options) {
		opts.MaxResponseBytes = 16
	})
	if _, err := service.Transcribe(context.Background(), transcribeRequest([]byte("audio"))); err == nil || !strings.Contains(err.Error(), "response exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestTranscribeLanguageOverrideAndFilenameSanitization(t *testing.T) {
	var gotLanguage, gotFilename string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		_ = mediaType
		reader := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if part.FormName() == "language" {
				body, _ := io.ReadAll(part)
				gotLanguage = string(body)
			}
			if part.FormName() == "file" {
				gotFilename = part.FileName()
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "ok"})
	}))
	defer server.Close()

	service := newTestService(t, server.URL+"/v1", nil)
	req := transcribeRequest([]byte("audio"))
	req.Language = "en"
	req.Name = `../../bad"name`
	if _, err := service.Transcribe(context.Background(), req); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotLanguage != "en" {
		t.Fatalf("language = %q", gotLanguage)
	}
	if strings.ContainsAny(gotFilename, `/\`) || strings.Contains(gotFilename, `"`) {
		t.Fatalf("filename = %q", gotFilename)
	}
}
