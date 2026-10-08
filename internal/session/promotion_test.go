package session

import (
	"context"
	"encoding/json"
	"testing"

	"elbot/internal/storage"
)

func newBackgroundSession(t *testing.T, store storage.Store, scopeID, kind, metadata string) *storage.Session {
	t.Helper()
	if metadata == "" {
		metadata = `{"background_kind":"` + kind + `"}`
	}
	row := &storage.Session{
		OwnerID:         "u1",
		Platform:        "qq",
		PlatformScopeID: scopeID,
		Mode:            storage.SessionModeWork,
		Status:          storage.SessionStatusActive,
		Title:           "background",
		Metadata:        metadata,
	}
	if err := store.Sessions().Create(context.Background(), row); err != nil {
		t.Fatalf("create background session: %v", err)
	}
	return row
}

func TestIsBackgroundDetectsScopeAndMetadata(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	byKind := newBackgroundSession(t, store, "group:1", "cron", `{"background_kind":"cron"}`)
	byScope := newBackgroundSession(t, store, "elnis:watcher", "elnis", "{}")
	plain := &storage.Session{OwnerID: "u1", Platform: "qq", PlatformScopeID: "group:1", Mode: storage.SessionModeWork}
	if err := store.Sessions().Create(ctx, plain); err != nil {
		t.Fatalf("create plain session: %v", err)
	}

	for name, row := range map[string]*storage.Session{"metadata kind": byKind, "scope prefix": byScope, "plain": plain} {
		want := name != "plain"
		if got := IsBackground(row); got != want {
			t.Fatalf("IsBackground(%s) = %v, want %v", name, got, want)
		}
		if WasPromoted(row) {
			t.Fatalf("WasPromoted(%s) should be false", name)
		}
	}
}

func TestResumePromotesBackgroundSessionIntoForegroundScope(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	background := newBackgroundSession(t, store, "cron:report", "cron", `{"background_kind":"cron","cron_job_name":"report","unknown_key":{"nested":1}}`)
	scope := Scope{ActorID: "u1", Platform: "qq", PlatformScopeID: "group:1"}

	if !IsBackground(background) {
		t.Fatal("cron session should start as background")
	}

	promoted, err := svc.Resume(ctx, scope, background.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !WasPromoted(promoted) {
		t.Fatalf("session was not marked as promoted: %#v", promoted)
	}
	if IsBackground(promoted) {
		t.Fatal("promoted session must not be treated as background")
	}
	if promoted.OwnerID != "u1" || promoted.Platform != "qq" || promoted.PlatformScopeID != "group:1" {
		t.Fatalf("scope not switched: %#v", promoted)
	}
	if promoted.Mode != storage.SessionModeWork {
		t.Fatalf("mode = %q", promoted.Mode)
	}

	fields := sessionMetadataFields(promoted.Metadata)
	if _, ok := fields["background_kind"]; ok {
		t.Fatalf("background_kind must be cleared: %s", promoted.Metadata)
	}
	var origin ForegroundOrigin
	if err := json.Unmarshal(fields["foreground_origin"], &origin); err != nil {
		t.Fatalf("decode foreground_origin %q: %v", promoted.Metadata, err)
	}
	want := ForegroundOrigin{Kind: "cron", OwnerID: "u1", Platform: "qq", ScopeID: "cron:report"}
	if origin != want {
		t.Fatalf("foreground_origin = %#v, want %#v", origin, want)
	}
	if got := string(fields["cron_job_name"]); got != `"report"` {
		t.Fatalf("cron_job_name = %s", got)
	}
	if got := string(fields["unknown_key"]); got != `{"nested":1}` {
		t.Fatalf("unknown metadata key lost: %s", got)
	}

	// The promotion is persisted and survives a reload.
	reloaded, err := store.Sessions().Get(ctx, background.ID)
	if err != nil {
		t.Fatalf("get promoted: %v", err)
	}
	if !WasPromoted(reloaded) || reloaded.PlatformScopeID != "group:1" {
		t.Fatalf("promotion did not persist: %#v", reloaded)
	}
	current, err := svc.Current(ctx, scope)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.ID != background.ID {
		t.Fatalf("current = %s, want %s", current.ID, background.ID)
	}
}

func TestResumePromotionIsIdempotentAndScopeBound(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	background := newBackgroundSession(t, store, "cron:report", "cron", "")
	scope := Scope{ActorID: "u1", Platform: "qq", PlatformScopeID: "group:1"}

	first, err := svc.Resume(ctx, scope, background.ID)
	if err != nil {
		t.Fatalf("first Resume: %v", err)
	}
	second, err := svc.Resume(ctx, scope, background.ID)
	if err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	var firstOrigin, secondOrigin ForegroundOrigin
	if err := json.Unmarshal(sessionMetadataFields(first.Metadata)["foreground_origin"], &firstOrigin); err != nil {
		t.Fatalf("decode first origin: %v", err)
	}
	if err := json.Unmarshal(sessionMetadataFields(second.Metadata)["foreground_origin"], &secondOrigin); err != nil {
		t.Fatalf("decode second origin: %v", err)
	}
	if firstOrigin != secondOrigin || secondOrigin.ScopeID != "cron:report" {
		t.Fatalf("repeated promotion rewrote the origin: %#v / %#v", firstOrigin, secondOrigin)
	}

	// A promoted session now belongs to the foreground scope: another group and
	// another actor must not be able to resume it.
	otherGroup := Scope{ActorID: "u1", Platform: "qq", PlatformScopeID: "group:2"}
	if _, err := svc.Resume(ctx, otherGroup, background.ID); err == nil {
		t.Fatal("promoted session stayed reachable from another group scope")
	}
	otherActor := Scope{ActorID: "u2", Platform: "qq", PlatformScopeID: "group:1"}
	if _, err := svc.Resume(ctx, otherActor, background.ID); err == nil {
		t.Fatal("promoted session stayed reachable for another actor")
	}
}

func TestResumeKeepsNormalSessionMetadata(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	plain := &storage.Session{
		OwnerID:         "u1",
		Platform:        "cli",
		PlatformScopeID: "local",
		Mode:            storage.SessionModeChat,
		Status:          storage.SessionStatusActive,
		Title:           "plain",
		Metadata:        `{"tool_tags":["web"]}`,
	}
	if err := store.Sessions().Create(ctx, plain); err != nil {
		t.Fatalf("create plain session: %v", err)
	}
	scope := Scope{ActorID: "u1", Platform: "cli", PlatformScopeID: "local", IsCLI: true}

	resumed, err := svc.Resume(ctx, scope, plain.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if WasPromoted(resumed) || IsBackground(resumed) {
		t.Fatalf("plain session was promoted: %#v", resumed)
	}
	if resumed.Metadata != `{"tool_tags":["web"]}` {
		t.Fatalf("metadata changed: %s", resumed.Metadata)
	}
	if resumed.Mode != storage.SessionModeChat {
		t.Fatalf("mode = %q", resumed.Mode)
	}
}

func TestUnarchivePromotesBackgroundSession(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	archivedAt := storage.Now()
	background := newBackgroundSession(t, store, "elnis:watcher", "elnis", "")
	background.ArchivedAt = &archivedAt
	if err := store.Sessions().Update(ctx, background); err != nil {
		t.Fatalf("archive session: %v", err)
	}
	scope := Scope{ActorID: "u1", Platform: "qq", PlatformScopeID: "group:1"}

	restored, err := svc.Unarchive(ctx, scope, background.ID)
	if err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if restored.ArchivedAt != nil {
		t.Fatalf("session still archived: %#v", restored)
	}
	if !WasPromoted(restored) || IsBackground(restored) {
		t.Fatalf("unarchive did not promote the background session: %#v", restored)
	}
	if restored.PlatformScopeID != "group:1" || restored.Mode != storage.SessionModeWork {
		t.Fatalf("unarchive did not switch the scope: %#v", restored)
	}
	current, err := svc.Current(ctx, scope)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.ID != background.ID {
		t.Fatalf("current = %s, want %s", current.ID, background.ID)
	}
}
