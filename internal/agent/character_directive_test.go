package agent

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/character"
	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/security"
	"elbot/internal/session"
	"elbot/internal/storage"
)

func seedCharacter(t *testing.T, store *character.Store, id, name, profile string) {
	t.Helper()
	description := "测试角色"
	if _, err := store.Write(context.Background(), character.WriteRequest{
		ID:          id,
		Name:        name,
		Description: &description,
		Visibility:  "public",
		Docs:        map[string]*string{"profile": &profile},
	}, character.Viewer{Platform: "cli", ActorID: "cli:local", Superadmin: true}); err != nil {
		t.Fatalf("seed character: %v", err)
	}
}

func TestCharacterDirectiveInjectsOneTurnPersona(t *testing.T) {
	ctx := context.Background()
	p := &fakePlatform{}
	store := newTestStore(t)
	f := &fakeLLM{replies: []string{"done"}}
	a := New(p, f, "test-model", config.ProviderConfig{}, store)
	a.SetSecurityPolicy(security.NewPolicy("low", "critical", map[string][]string{"cli": {"local"}}))

	characters := character.NewStore(t.TempDir())
	seedCharacter(t, characters, "catgirl", "猫娘", "你是一只猫娘，说话带喵。")
	a.characters = characters

	// The directive works in chat mode too, because it only injects text.
	if _, err := a.sessions.Create(ctx, a.scope(ctx), session.CreateRequest{Title: "chat", Mode: storage.SessionModeChat}); err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	if err := a.HandleMessage(ctx, "@char:catgirl 你好"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if notice := p.out.String(); !strings.Contains(notice, "已启用角色：catgirl") {
		t.Fatalf("notice = %q", notice)
	}
	requests := f.chatRequests()
	if len(requests) != 1 {
		t.Fatalf("chat requests = %d", len(requests))
	}
	latest := llm.SegmentsContentText(requests[0].Messages[len(requests[0].Messages)-1].Segments)
	for _, want := range []string{"[角色设定]", "你是一只猫娘，说话带喵。", "你好"} {
		if !strings.Contains(latest, want) {
			t.Fatalf("latest = %q, want %q", latest, want)
		}
	}
	if strings.Contains(latest, "@char:catgirl") {
		t.Fatalf("directive should be stripped: %q", latest)
	}

	// A second turn without the directive is not affected.
	if err := a.HandleMessage(ctx, "第二个问题"); err != nil {
		t.Fatalf("second HandleMessage: %v", err)
	}
	requests = f.chatRequests()
	last := llm.SegmentsContentText(requests[len(requests)-1].Messages[len(requests[len(requests)-1].Messages)-1].Segments)
	if strings.Contains(last, "[角色设定]") {
		t.Fatalf("character leaked into later turn: %q", last)
	}
}

func TestCharacterDirectiveUnknownCharacterNotifies(t *testing.T) {
	ctx := context.Background()
	p := &fakePlatform{}
	store := newTestStore(t)
	f := &fakeLLM{replies: []string{"done"}}
	a := New(p, f, "test-model", config.ProviderConfig{}, store)
	a.SetSecurityPolicy(security.NewPolicy("low", "critical", map[string][]string{"cli": {"local"}}))
	a.characters = character.NewStore(t.TempDir())

	if err := a.HandleMessage(ctx, "@char:missing 你好"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if notice := p.out.String(); !strings.Contains(notice, "未找到或不可用的角色：missing") {
		t.Fatalf("notice = %q", notice)
	}
}
