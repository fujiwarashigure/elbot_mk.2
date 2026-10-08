package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/contextmgr"
	"elbot/internal/llm"
	"elbot/internal/session"
	"elbot/internal/storage"
)

// nativeCompactionClient 是一个"协议声称能做服务端压缩"的客户端：fork 没有对应后端，
// 因此分派点必须显式回退到客户端压缩并让调用方记一条审计。
type nativeCompactionClient struct {
	*fakeLLM
}

func (c nativeCompactionClient) ProtocolCapabilitiesFor(string) llm.ProtocolCapabilities {
	return llm.ProtocolCapabilities{
		Protocol:               llm.ProtocolResponses,
		ServerSideConversation: true,
		NativeCompaction:       true,
	}
}

func TestCompactorForKeepsClientBackendWhenProtocolOnlyAdvertisesNative(t *testing.T) {
	native := nativeCompactionClient{fakeLLM: &fakeLLM{}}
	runtime := contextRuntimeState{
		compactor: contextmgr.Compressor{ClientFor: func(string) llm.LLM { return native }},
		clientFor: func(string) llm.LLM { return native },
	}
	compactor, choice := runtime.compactorFor("default", "resp-model")
	if compactor == nil {
		t.Fatal("a backend must always be selected")
	}
	if choice.Backend != "client_summary" {
		t.Fatalf("backend = %q", choice.Backend)
	}
	if choice.FallbackReason != "protocol_advertises_native_compaction_without_backend" {
		t.Fatalf("fallback reason = %q", choice.FallbackReason)
	}
}

func TestCompactorForHasNoFallbackForClientSideProtocol(t *testing.T) {
	plain := &fakeLLM{}
	runtime := contextRuntimeState{
		compactor: contextmgr.Compressor{ClientFor: func(string) llm.LLM { return plain }},
		clientFor: func(string) llm.LLM { return plain },
	}
	compactor, choice := runtime.compactorFor("default", "chat-model")
	if compactor == nil || choice.Backend != "client_summary" || choice.FallbackReason != "" {
		t.Fatalf("compactor/choice = %#v/%#v", compactor, choice)
	}
}

func TestCompactorForWithoutClientReportsNoFallback(t *testing.T) {
	runtime := contextRuntimeState{compactor: contextmgr.Compressor{}}
	compactor, choice := runtime.compactorFor("default", "chat-model")
	if compactor == nil || choice.FallbackReason != "" {
		t.Fatalf("compactor/choice = %#v/%#v", compactor, choice)
	}
}

// 压缩实际走的后端必须能从审计里看出来：否则"服务端压缩到底接上没有"只能靠猜。
func TestAutoCompactAuditsBackendAndFallback(t *testing.T) {
	ctx := context.Background()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	client := nativeCompactionClient{fakeLLM: &fakeLLM{replies: []string{"K", "answer J"}}}
	store := newTestStore(t)
	a := New(&fakePlatform{}, client, "test-model", config.ProviderConfig{}, store)
	a.SetLogManager(fakeLogManager{runtime: logger, audit: logger})
	source, err := a.sessions.Create(ctx, a.scope(ctx), session.CreateRequest{Title: "compact"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, message := range []*storage.Message{
		{SessionID: source.ID, Role: storage.RoleUser, Content: "B"},
		{SessionID: source.ID, Role: storage.RoleAssistant, Content: "H"},
	} {
		if err := store.Messages().Append(ctx, message); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}
	a.SetContextOptions(config.ContextConfig{CompactEnabled: true, CompactTriggerRatio: 0.8}, config.ModelMetadataConfig{DefaultContextWindow: 100}, nil, config.ModelSelection{})
	a.recordUsage(source.ID, &llm.Usage{TotalTokens: 80})
	if err := a.HandleMessage(ctx, "J"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	text := logs.String()
	if !strings.Contains(text, "event=context_compaction_backend") {
		t.Fatalf("compaction backend audit missing:\n%s", text)
	}
	if !strings.Contains(text, "backend=client_summary") {
		t.Fatalf("compaction backend name missing:\n%s", text)
	}
	if !strings.Contains(text, "reason=protocol_advertises_native_compaction_without_backend") {
		t.Fatalf("compaction fallback reason missing:\n%s", text)
	}
	if !strings.Contains(text, "result=succeeded") {
		t.Fatalf("compaction backend audit lost its result:\n%s", text)
	}
}

func TestRecordLLMOriginPersistsProtocolOnce(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	client := nativeCompactionClient{fakeLLM: &fakeLLM{}}
	a := New(&fakePlatform{}, client, "test-model", config.ProviderConfig{}, store)
	session, err := a.sessions.Create(ctx, a.scope(ctx), session.CreateRequest{Title: "origin"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	selection := config.ModelSelection{Provider: "default", Model: "resp-model"}
	a.recordLLMOrigin(ctx, session, selection)
	stored, err := store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	origin := decodeSessionMetadata(stored.Metadata).LLMOrigin
	if origin.Protocol != string(llm.ProtocolResponses) || origin.Provider != "default" || origin.Model != "resp-model" {
		t.Fatalf("llm origin = %#v", origin)
	}

	// 值没变时不再写事务：updated_at 必须保持不变。
	before := stored.UpdatedAt
	a.recordLLMOrigin(ctx, stored, selection)
	again, err := store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if again.UpdatedAt != before {
		t.Fatalf("unchanged origin rewrote the row: %s -> %s", before, again.UpdatedAt)
	}

	// 换模型必须重写成新的来源。
	a.recordLLMOrigin(ctx, again, config.ModelSelection{Provider: "default", Model: "other-model"})
	updated, err := store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if decodeSessionMetadata(updated.Metadata).LLMOrigin.Model != "other-model" {
		t.Fatalf("llm origin was not updated: %#v", decodeSessionMetadata(updated.Metadata).LLMOrigin)
	}
}

func TestRecordLLMOriginWithoutSessionIsNoop(t *testing.T) {
	a := &Agent{}
	a.recordLLMOrigin(context.Background(), nil, config.ModelSelection{Provider: "default", Model: "m"})
	a.recordLLMOrigin(context.Background(), &storage.Session{ID: "s"}, config.ModelSelection{})
}
