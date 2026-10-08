package agent

import (
	"context"
	"sort"

	"elbot/internal/llm"
	"elbot/internal/storage"
	"elbot/internal/tool"
	"elbot/internal/toolrun"
)

func (a *Agent) rememberDiscoveredTools(ctx context.Context, session *storage.Session, result *tool.DiscoveryResult) {
	if result == nil || session == nil || session.ID == "" {
		return
	}
	a.rememberCachedTools(ctx, session, toolrun.NativeCachedToolsFromDiscovery(result))
}

func (a *Agent) rememberCachedTools(ctx context.Context, session *storage.Session, cached []toolrun.CachedTool) {
	if session == nil || session.ID == "" || len(cached) == 0 {
		return
	}
	a.autoConfirmMu.Lock()
	if a.discoveredTools == nil {
		a.discoveredTools = map[string]map[string]llm.ToolSchema{}
	}
	if a.discoveredTools[session.ID] == nil {
		a.discoveredTools[session.ID] = map[string]llm.ToolSchema{}
	}
	if a.toolRuntime.manager == nil {
		a.toolRuntime.manager = toolrun.NewManager(a.toolRuntime.registry, a.securityPolicy)
	}
	if a.toolRuntime.manager != nil {
		for _, item := range cached {
			if item.Name == "" {
				continue
			}
			a.discoveredTools[session.ID][item.Name] = item.Schema
		}
	}
	a.autoConfirmMu.Unlock()
	a.persistCachedTools(ctx, session, cached)
}

func (a *Agent) discoveredToolSchemas(session *storage.Session) []llm.ToolSchema {
	if session == nil {
		return nil
	}
	a.restoreDiscoveredToolsFromMetadata(session)
	a.autoConfirmMu.Lock()
	defer a.autoConfirmMu.Unlock()
	tools := a.discoveredTools[session.ID]
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]llm.ToolSchema, 0, len(names))
	for _, name := range names {
		out = append(out, tools[name])
	}
	return out
}

func (a *Agent) restoreDiscoveredToolsFromMetadata(session *storage.Session) {
	if session == nil {
		return
	}
	metadata := decodeSessionMetadata(session.Metadata)
	if len(metadata.ToolCache) == 0 && len(metadata.DiscoveredTools) == 0 {
		return
	}
	a.autoConfirmMu.Lock()
	if a.discoveredTools == nil {
		a.discoveredTools = map[string]map[string]llm.ToolSchema{}
	}
	if a.discoveredTools[session.ID] == nil {
		a.discoveredTools[session.ID] = map[string]llm.ToolSchema{}
	}
	if a.toolRuntime.manager == nil {
		a.toolRuntime.manager = toolrun.NewManager(a.toolRuntime.registry, a.securityPolicy)
	}
	for _, name := range metadata.DiscoveredTools {
		if _, ok := a.discoveredTools[session.ID][name]; ok {
			continue
		}
		if a.toolRuntime.registry == nil {
			continue
		}
		if t, ok := a.toolRuntime.registry.Get(name); ok {
			a.discoveredTools[session.ID][name] = t.Schema()
		}
	}
	for _, item := range metadata.ToolCache {
		if item.Name == "" || item.Source == toolrun.SourceKindELwisp {
			continue
		}
		if _, ok := a.discoveredTools[session.ID][item.Name]; ok {
			continue
		}
		a.discoveredTools[session.ID][item.Name] = item.Schema
	}
	a.autoConfirmMu.Unlock()
}

// mutateSessionMetadata applies one metadata change to the stored row inside a
// single transaction, then refreshes the caller's copy. Read-modify-write from a
// stale snapshot would silently drop the fields a concurrent writer (activity,
// workspace, naming, another discovery) changed in the meantime.
func (a *Agent) mutateSessionMetadata(ctx context.Context, session *storage.Session, mutate func(metadata *sessionMetadata) bool) error {
	if session == nil || session.ID == "" || mutate == nil {
		return nil
	}
	updated, err := a.store.Sessions().Mutate(ctx, session.ID, func(current *storage.Session) error {
		metadata := decodeSessionMetadata(current.Metadata)
		if !mutate(&metadata) {
			return nil
		}
		encoded := encodeSessionMetadataInto(current.Metadata, metadata)
		if encoded == current.Metadata {
			return nil
		}
		current.Metadata = encoded
		current.UpdatedAt = storage.Now()
		return nil
	})
	if err != nil {
		return err
	}
	session.Metadata = updated.Metadata
	return nil
}

func (a *Agent) persistCachedTools(ctx context.Context, session *storage.Session, cached []toolrun.CachedTool) {
	err := a.mutateSessionMetadata(ctx, session, func(metadata *sessionMetadata) bool {
		metadata.ToolCache = toolrun.MergeCachedTools(metadata.ToolCache, cached)
		metadata.DiscoveredTools = sortedUnique(append(metadata.DiscoveredTools, cachedToolNames(cached)...))
		return true
	})
	if err != nil && a.logger != nil {
		a.logger.Warn("persist cached tools failed", "session_id", session.ID, "error", err)
	}
}

func cachedToolNames(items []toolrun.CachedTool) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		if item.Name != "" {
			names = append(names, item.Name)
		}
	}
	return names
}

func (a *Agent) cachedToolNameSet(ctx context.Context, session *storage.Session) map[string]bool {
	out := map[string]bool{}
	if session == nil {
		return out
	}
	metadataRaw := session.Metadata
	if session.ID != "" {
		if latest, err := a.store.Sessions().Get(ctx, session.ID); err == nil {
			metadataRaw = latest.Metadata
		}
	}
	metadata := decodeSessionMetadata(metadataRaw)
	for _, name := range metadata.DiscoveredTools {
		if name != "" {
			out[name] = true
		}
	}
	for _, item := range metadata.ToolCache {
		if item.Name != "" {
			out[item.Name] = true
		}
	}
	a.autoConfirmMu.Lock()
	for name := range a.discoveredTools[session.ID] {
		if name != "" {
			out[name] = true
		}
	}
	a.autoConfirmMu.Unlock()
	return out
}

func (a *Agent) persistToolTags(ctx context.Context, session *storage.Session, tags []string) {
	if session == nil || session.ID == "" || len(tags) == 0 {
		return
	}
	err := a.mutateSessionMetadata(ctx, session, func(metadata *sessionMetadata) bool {
		metadata.ToolTags = sortedUnique(append(metadata.ToolTags, tags...))
		return true
	})
	if err != nil && a.logger != nil {
		a.logger.Warn("persist tool tags failed", "session_id", session.ID, "error", err)
	}
}

func (a *Agent) persistShownRuleCardFormats(ctx context.Context, session *storage.Session, formats []string) {
	if session == nil || session.ID == "" || len(formats) == 0 {
		return
	}
	err := a.mutateSessionMetadata(ctx, session, func(metadata *sessionMetadata) bool {
		metadata.ShownRuleCardFormats = sortedUnique(append(metadata.ShownRuleCardFormats, formats...))
		return true
	})
	if err != nil && a.logger != nil {
		a.logger.Warn("persist rule card formats failed", "session_id", session.ID, "error", err)
	}
}
