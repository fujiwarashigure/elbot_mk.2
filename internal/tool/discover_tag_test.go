package tool

import (
	"strings"
	"testing"
)

func TestExpandDiscoveryTagNames(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(fakeTool{name: "search_chat_history", tags: []string{"chat"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fakeTool{name: "get_media", tags: []string{"chat"}, hidden: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fakeTool{name: "web_search", tags: []string{"web"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fakeTool{name: "shell", tags: []string{"chat"}, superadminOnly: true}); err != nil {
		t.Fatal(err)
	}

	names := expandDiscoveryTagNames(registry, []string{"chat", "web_search", "missing"}, func(candidate Tool) bool {
		return !candidate.Info().SuperadminOnly
	})
	got := strings.Join(names, ",")
	want := "get_media,search_chat_history,web_search,missing"
	if got != want {
		t.Fatalf("expanded names = %q, want %q", got, want)
	}
}

func TestPublicInfoExposesTags(t *testing.T) {
	info := publicInfo(Info{Name: "search_chat_history", Tags: []string{"chat", " CHAT ", "bad tag"}})
	if strings.Join(info.Tags, ",") != "chat" {
		t.Fatalf("public tags = %#v", info.Tags)
	}
}
