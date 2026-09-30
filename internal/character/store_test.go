package character

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreWriteVisibilitySearchAndDelete(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := NewStore(root)

	owner := Viewer{Platform: "qqonebot", ActorID: "qqonebot:1001"}
	other := Viewer{Platform: "qqonebot", ActorID: "qqonebot:1002"}
	admin := Viewer{Platform: "cli", ActorID: "cli:local", Superadmin: true}

	description := "冷面剑客"
	profile := "你是一位沉默寡言的剑客，只在该说话时说话。"
	world := "架空武侠世界。"
	note := "口头禅：剑不问出处。"
	item, err := store.Write(ctx, WriteRequest{
		ID:          "SwordMan",
		Name:        "剑客",
		Aliases:     []string{"剑士"},
		Tags:        []string{"wuxia"},
		Description: &description,
		Docs: map[string]*string{
			"profile":  &profile,
			"world":    &world,
			"notes/招式": &note,
		},
	}, owner)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if item.ID != "swordman" {
		t.Fatalf("id = %q, want normalized swordman", item.ID)
	}
	if item.Visibility != VisibilityPrivate {
		t.Fatalf("visibility = %q, want private", item.Visibility)
	}
	if item.OwnerPlatform != owner.Platform || item.OwnerID != owner.ActorID {
		t.Fatalf("owner = %s/%s", item.OwnerPlatform, item.OwnerID)
	}
	for _, name := range []string{"character.toml", "profile.md", "world.md", filepath.Join("notes", "招式.md")} {
		if _, err := os.Stat(filepath.Join(root, "swordman", name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if strings.TrimSpace(item.Docs["profile"]) != profile || strings.TrimSpace(item.Docs["notes/招式"]) != note {
		t.Fatalf("docs = %#v", item.Docs)
	}

	if _, err := store.GetVisible(ctx, "swordman", other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other should not read private character, got %v", err)
	}
	if _, err := store.GetVisible(ctx, "swordman", admin); err != nil {
		t.Fatalf("superadmin should read private character: %v", err)
	}

	// Only the owner can update.
	if _, err := store.Write(ctx, WriteRequest{ID: "swordman", Name: "hacked"}, other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other should not update, got %v", err)
	}

	// Superadmin creates a public character visible to everyone.
	publicName := "公共助手"
	public, err := store.Write(ctx, WriteRequest{ID: "assistant", Name: publicName, Visibility: "public"}, admin)
	if err != nil {
		t.Fatalf("write public: %v", err)
	}
	if public.Visibility != VisibilityPublic {
		t.Fatalf("public visibility = %q", public.Visibility)
	}
	if _, err := store.GetVisible(ctx, "assistant", other); err != nil {
		t.Fatalf("other should read public character: %v", err)
	}

	// Search covers name, alias, tag and body, and hides private characters.
	results, err := store.Search(ctx, "剑客", "", "", 5, other)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, result := range results {
		if result.ID == "swordman" {
			t.Fatalf("private character leaked into search for other: %#v", result)
		}
	}
	results, err = store.Search(ctx, "口头禅", "", "", 5, owner)
	if err != nil {
		t.Fatalf("search owner: %v", err)
	}
	if len(results) == 0 || results[0].ID != "swordman" {
		t.Fatalf("owner search = %#v", results)
	}
	results, err = store.Search(ctx, "wuxia", "", "", 5, other)
	if err != nil {
		t.Fatalf("search tag: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("tag search should not leak private character: %#v", results)
	}

	// Images: original bytes plus Media Center reference.
	mediaID := "media:" + strings.Repeat("a", 64)
	image, err := store.AddImage(ctx, "swordman", "avatar.png", "image/png", mediaID, []byte("png-bytes"), owner)
	if err != nil {
		t.Fatalf("add image: %v", err)
	}
	if image.Name != "avatar.png" || image.MediaID != mediaID {
		t.Fatalf("image = %#v", image)
	}
	if _, err := os.Stat(filepath.Join(root, "swordman", "images", "avatar.png")); err != nil {
		t.Fatalf("image file missing: %v", err)
	}
	loaded, err := store.Get(ctx, "swordman")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(loaded.Images) != 1 || loaded.Images[0].MediaID != mediaID {
		t.Fatalf("loaded images = %#v", loaded.Images)
	}
	if err := store.RemoveImage(ctx, "swordman", "avatar.png", owner); err != nil {
		t.Fatalf("remove image: %v", err)
	}
	loaded, _ = store.Get(ctx, "swordman")
	if len(loaded.Images) != 0 {
		t.Fatalf("images after remove = %#v", loaded.Images)
	}

	// Prompt contains the persona text.
	prompt := loaded.Prompt(0)
	if !strings.Contains(prompt, profile) || !strings.Contains(prompt, world) {
		t.Fatalf("prompt = %q", prompt)
	}

	// Delete is owner-only.
	if err := store.Delete(ctx, "swordman", other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other delete = %v", err)
	}
	if err := store.Delete(ctx, "swordman", owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := store.Get(ctx, "swordman"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted character still present: %v", err)
	}
}

func TestStoreRejectsInvalidID(t *testing.T) {
	store := NewStore(t.TempDir())
	admin := Viewer{Platform: "cli", ActorID: "cli:local", Superadmin: true}
	if _, err := store.Write(context.Background(), WriteRequest{ID: "../escape"}, admin); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err = %v, want ErrInvalidID", err)
	}
}
