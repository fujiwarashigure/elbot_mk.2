package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/llm/openai"
)

func TestDefaultModelFactoryBuildsEveryProvider(t *testing.T) {
	events := []string{}
	foundation := &FoundationComponents{
		Config: &config.Config{Providers: map[string]config.ProviderConfig{
			"first":  {BaseURL: "https://first.example/v1"},
			"second": {BaseURL: "https://second.example/v1"},
		}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	clients, err := (defaultModelFactory{}).Build(ModelRequest{Foundation: foundation, Profiler: profilerStub{events: &events}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(clients.ByProvider) != 2 || clients.ByProvider["first"] == nil || clients.ByProvider["second"] == nil {
		t.Fatalf("clients = %#v", clients.ByProvider)
	}
}

func TestDefaultModelFactoryReportsProviderForInvalidProxy(t *testing.T) {
	events := []string{}
	foundation := &FoundationComponents{
		Config: &config.Config{Providers: map[string]config.ProviderConfig{
			"broken": {BaseURL: "https://example.invalid/v1", Proxy: "://bad proxy"},
		}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	_, err := (defaultModelFactory{}).Build(ModelRequest{Foundation: foundation, Profiler: profilerStub{events: &events}})
	if err == nil || !strings.Contains(err.Error(), `create provider "broken" client`) || !strings.Contains(err.Error(), "invalid proxy URL") {
		t.Fatalf("Build() error = %v", err)
	}
}

// protocolServer answers both protocols and records the paths it was asked for.
func protocolServer(paths *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		switch r.URL.Path {
		case "/v1/chat/completions":
			io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"chat-answer"},"finish_reason":"stop"}]}`+"\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
		case "/v1/responses":
			io.WriteString(w, `data: {"type":"response.output_text.delta","delta":"responses-answer"}`+"\n\n")
			io.WriteString(w, `data: {"type":"response.completed","response":{"status":"completed"}}`+"\n\n")
		default:
			http.NotFound(w, r)
		}
		w.(http.Flusher).Flush()
	}))
}

func providerText(t *testing.T, client llm.LLM, model string) string {
	t.Helper()
	stream, err := client.ChatStream(context.Background(), llm.ChatRequest{
		Model:    model,
		Messages: []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hi")}},
	})
	if err != nil {
		t.Fatalf("ChatStream(%s): %v", model, err)
	}
	var text strings.Builder
	for chunk := range stream {
		if chunk.Error != nil {
			t.Fatalf("stream(%s) error: %v", model, chunk.Error)
		}
		text.WriteString(chunk.DeltaContent)
	}
	return text.String()
}

func TestProviderLLMRoutesEachModelToItsProtocol(t *testing.T) {
	var paths []string
	srv := protocolServer(&paths)
	defer srv.Close()

	client, err := newProviderLLM("mixed", config.ProviderConfig{
		BaseURL: srv.URL + "/v1",
		APIMode: "chat",
		ModelConfigs: map[string]config.ModelConfig{
			"resp-model": {APIMode: "response"},
		},
	}, openai.RequestOptions{})
	if err != nil {
		t.Fatalf("newProviderLLM: %v", err)
	}

	if got := providerText(t, client, "plain-model"); got != "chat-answer" {
		t.Errorf("plain model answered %q, want chat-answer", got)
	}
	if got := providerText(t, client, "resp-model"); got != "responses-answer" {
		t.Errorf("responses model answered %q, want responses-answer", got)
	}
	if len(paths) != 2 || paths[0] != "/v1/chat/completions" || paths[1] != "/v1/responses" {
		t.Fatalf("request paths = %#v, want chat then responses", paths)
	}
}

// 协议能力必须按模型回答：一个 provider 混用两种协议时，只有实际用哪套适配器才决定它能不能
// 用服务端能力，配置里的默认 api_mode 不能代表所有模型。
func TestProviderLLMReportsProtocolCapabilitiesPerModel(t *testing.T) {
	var paths []string
	srv := protocolServer(&paths)
	defer srv.Close()

	client, err := newProviderLLM("mixed", config.ProviderConfig{
		BaseURL: srv.URL + "/v1",
		APIMode: "chat",
		ModelConfigs: map[string]config.ModelConfig{
			"resp-model": {APIMode: "response"},
		},
	}, openai.RequestOptions{})
	if err != nil {
		t.Fatalf("newProviderLLM: %v", err)
	}
	if caps := llm.ProtocolCapabilitiesOf(client, "plain-model"); caps.Protocol != llm.ProtocolChatCompletions {
		t.Fatalf("chat model protocol = %q", caps.Protocol)
	}
	responses := llm.ProtocolCapabilitiesOf(client, "resp-model")
	if responses.Protocol != llm.ProtocolResponses {
		t.Fatalf("responses model protocol = %q", responses.Protocol)
	}
	// fork 的 Responses 适配器是协议翻译层，服务端能力必须如实报告为 false。
	if responses.ServerSideConversation || responses.NativeCompaction || responses.IncrementalTools || responses.ServerSideStore {
		t.Fatalf("fork responses adapter must not claim server-side capabilities: %#v", responses)
	}
}

func TestProviderLLMUsesResponsesDefaultWithChatModelOverride(t *testing.T) {
	var paths []string
	srv := protocolServer(&paths)
	defer srv.Close()

	client, err := newProviderLLM("mixed", config.ProviderConfig{
		BaseURL: srv.URL + "/v1",
		APIMode: "response",
		ModelConfigs: map[string]config.ModelConfig{
			"legacy-model": {APIMode: "chat"},
		},
	}, openai.RequestOptions{})
	if err != nil {
		t.Fatalf("newProviderLLM: %v", err)
	}

	if got := providerText(t, client, "gpt-5.1"); got != "responses-answer" {
		t.Errorf("default model answered %q, want responses-answer", got)
	}
	if got := providerText(t, client, "legacy-model"); got != "chat-answer" {
		t.Errorf("override model answered %q, want chat-answer", got)
	}
	if len(paths) != 2 || paths[0] != "/v1/responses" || paths[1] != "/v1/chat/completions" {
		t.Fatalf("request paths = %#v, want responses then chat", paths)
	}
}

func TestProviderLLMStaysChatOnlyWithoutResponsesConfig(t *testing.T) {
	var paths []string
	srv := protocolServer(&paths)
	defer srv.Close()

	client, err := newProviderLLM("plain", config.ProviderConfig{BaseURL: srv.URL + "/v1"}, openai.RequestOptions{})
	if err != nil {
		t.Fatalf("newProviderLLM: %v", err)
	}
	if _, isRouter := client.(*protocolRouter); isRouter {
		t.Fatal("a chat-only provider must not pay for a routing client")
	}
	if got := providerText(t, client, "any-model"); got != "chat-answer" {
		t.Errorf("answer = %q, want chat-answer", got)
	}
}

func TestDefaultModelFactoryBuildsResponsesProvider(t *testing.T) {
	events := []string{}
	foundation := &FoundationComponents{
		Config: &config.Config{Providers: map[string]config.ProviderConfig{
			"openai_resp": {BaseURL: "https://api.openai.com/v1", APIMode: "response"},
		}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	clients, err := (defaultModelFactory{}).Build(ModelRequest{Foundation: foundation, Profiler: profilerStub{events: &events}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	client := clients.ByProvider["openai_resp"]
	if client == nil {
		t.Fatal("responses provider client is missing")
	}
	// The breaker/health wrappers keep forwarding the optional interfaces, so
	// retry notices and /models metadata must survive the routing client.
	if _, ok := client.(llm.RetryNotifier); !ok {
		t.Fatal("wrapped client must keep RetryNotifier")
	}
	if _, ok := client.(llm.ModelMetadataProvider); !ok {
		t.Fatal("wrapped client must keep ModelMetadataProvider")
	}
}
