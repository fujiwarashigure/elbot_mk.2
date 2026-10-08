package agent

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/storage"
	"elbot/internal/toolrun"
)

func newToolCacheTestAgent(t *testing.T) (*Agent, *storage.Session) {
	t.Helper()
	p := &fakePlatform{}
	store := newTestStore(t)
	a := New(p, &fakeLLM{}, "test-model", config.ProviderConfig{}, store)
	session := &storage.Session{
		OwnerID:         "cli:local",
		Platform:        p.Name(),
		PlatformScopeID: "local",
		Title:           "tool cache",
	}
	if err := store.Sessions().Create(context.Background(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return a, session
}

func testCachedTools(names ...string) []toolrun.CachedTool {
	out := make([]toolrun.CachedTool, 0, len(names))
	for _, name := range names {
		out = append(out, toolrun.CachedTool{
			Name:   name,
			Source: toolrun.SourceKindNative,
			Schema: llm.ToolSchema{Type: "function", Function: llm.ToolFunctionSchema{Name: name}},
		})
	}
	return out
}

// Tool discovery and inline preloading keep a session snapshot across turns and
// persist the tool cache from it. Writing that snapshot back as a whole row told
// the database that the fields a concurrent writer changed in the meantime
// (workspace dir, usage, another discovery) never happened. persistCachedTools
// must merge into the freshly loaded row instead.
func TestPersistCachedToolsKeepsConcurrentlyWrittenMetadata(t *testing.T) {
	a, session := newToolCacheTestAgent(t)
	ctx := context.Background()

	// Stale snapshot, as held by a running turn.
	stale := *session

	// Concurrent writer: the user switched the workspace directory.
	if _, err := a.store.Sessions().Mutate(ctx, session.ID, func(current *storage.Session) error {
		current.Metadata = `{"workspace_dir":"D:\\work"}`
		current.UpdatedAt = storage.Now()
		return nil
	}); err != nil {
		t.Fatalf("concurrent mutate: %v", err)
	}

	a.persistCachedTools(ctx, &stale, testCachedTools("web_search", "web_extract"))

	persisted, err := a.store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	metadata := decodeSessionMetadata(persisted.Metadata)
	if metadata.WorkspaceDir != `D:\work` {
		t.Fatalf("concurrent workspace_dir dropped: %q", persisted.Metadata)
	}
	// Cached tools are stored sorted by name.
	if got := strings.Join(cachedToolNames(metadata.ToolCache), ","); got != "web_extract,web_search" {
		t.Fatalf("persisted tool cache = %s (%#v)", got, metadata.ToolCache)
	}
	if got := strings.Join(metadata.DiscoveredTools, ","); got != "web_extract,web_search" {
		t.Fatalf("persisted discovered tools = %s", got)
	}

	// The caller's snapshot is refreshed, so the next turn sees what was written.
	if !strings.Contains(stale.Metadata, "workspace_dir") || !strings.Contains(stale.Metadata, "web_search") {
		t.Fatalf("caller snapshot not refreshed: %q", stale.Metadata)
	}
}

func TestPersistToolTagsAndRuleCardsKeepToolCache(t *testing.T) {
	a, session := newToolCacheTestAgent(t)
	ctx := context.Background()

	if _, err := a.store.Sessions().Mutate(ctx, session.ID, func(current *storage.Session) error {
		current.Metadata = `{"tool_cache":[{"name":"web_search","source":"native"}]}`
		current.UpdatedAt = storage.Now()
		return nil
	}); err != nil {
		t.Fatalf("seed tool cache: %v", err)
	}

	stale := *session
	a.persistToolTags(ctx, &stale, []string{"web"})
	a.persistShownRuleCardFormats(ctx, &stale, []string{"rule-card"})

	persisted, err := a.store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	metadata := decodeSessionMetadata(persisted.Metadata)
	if len(metadata.ToolCache) != 1 || metadata.ToolCache[0].Name != "web_search" {
		t.Fatalf("tool cache dropped: %q", persisted.Metadata)
	}
	if len(metadata.ToolTags) != 1 || metadata.ToolTags[0] != "web" {
		t.Fatalf("tool tags = %#v", metadata.ToolTags)
	}
	if len(metadata.ShownRuleCardFormats) != 1 || metadata.ShownRuleCardFormats[0] != "rule-card" {
		t.Fatalf("rule card formats = %#v", metadata.ShownRuleCardFormats)
	}
}

// The workspace writer is the other half of the same row: switching the
// workspace must not drop the tool cache a discovery recorded concurrently.
func TestWorkspaceDirWriteKeepsToolCache(t *testing.T) {
	a, session := newToolCacheTestAgent(t)
	ctx := context.Background()

	if _, err := a.store.Sessions().Mutate(ctx, session.ID, func(current *storage.Session) error {
		current.Metadata = `{"tool_cache":[{"name":"web_search","source":"native"}]}`
		current.UpdatedAt = storage.Now()
		return nil
	}); err != nil {
		t.Fatalf("seed tool cache: %v", err)
	}

	stale := *session
	workspace := sessionWorkspaceStore{agent: a, session: &stale}
	if err := workspace.SetWorkspaceDirWithAgentNotice(ctx, `D:\repo`, true); err != nil {
		t.Fatalf("set workspace dir: %v", err)
	}

	persisted, err := a.store.Sessions().Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	metadata := decodeSessionMetadata(persisted.Metadata)
	if metadata.WorkspaceDir != `D:\repo` {
		t.Fatalf("workspace dir = %q", metadata.WorkspaceDir)
	}
	if len(metadata.WorkspaceAgentNoticeDirs) != 1 || metadata.WorkspaceAgentNoticeDirs[0] != `D:\repo` {
		t.Fatalf("workspace notice dirs = %#v", metadata.WorkspaceAgentNoticeDirs)
	}
	if len(metadata.ToolCache) != 1 || metadata.ToolCache[0].Name != "web_search" {
		t.Fatalf("tool cache dropped: %q", persisted.Metadata)
	}
}
