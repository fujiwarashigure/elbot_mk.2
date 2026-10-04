package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"elbot/internal/angelmemory"
	"elbot/internal/security"
	"elbot/internal/session"
)

func resolveScopedMemory(ctx context.Context, service *angelmemory.Service, platform, scopeID, prefix string, filter angelmemory.SourceFilter) (*angelmemory.Memory, error) {
	if service == nil || !service.Ready() {
		return nil, fmt.Errorf("angel memory 未启用或未配置")
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, fmt.Errorf("请提供记忆 id")
	}
	id, err := service.ResolveID(ctx, platform, scopeID, prefix, filter)
	switch {
	case errors.Is(err, angelmemory.ErrNotFound):
		return nil, fmt.Errorf("没有找到 id 前缀为 %q 的记忆", prefix)
	case errors.Is(err, angelmemory.ErrAmbiguousID):
		return nil, fmt.Errorf("id 前缀 %q 不唯一，请提供更多字符", prefix)
	case err != nil:
		return nil, err
	}
	return service.Get(ctx, platform, scopeID, id)
}

func commandAudit(deps Deps, event string, attrs ...any) {
	if deps.Audit == nil {
		return
	}
	deps.Audit(event, attrs...)
}

// memoryVisibleFilter returns the source filter that limits an ordinary group
// member to memories created from their own messages. Private scopes and
// group/superadmins get the empty filter (whole current scope).
func memoryVisibleFilter(ctx context.Context, scope session.Scope) angelmemory.SourceFilter {
	if !isGroupScope(scope) {
		return angelmemory.SourceFilter{}
	}
	actor, _ := security.ActorFromContext(ctx)
	if actor.Role == security.RoleSuperadmin || actor.GroupRole == security.GroupRoleOwner || actor.GroupRole == security.GroupRoleAdmin {
		return angelmemory.SourceFilter{}
	}
	actorID := strings.TrimSpace(actor.ID)
	if actorID == "" {
		return angelmemory.SourceFilter{ActorID: "\x00"}
	}
	return angelmemory.SourceFilter{ActorID: actorID}
}

func memoryCanManageAll(ctx context.Context, scope session.Scope) bool {
	if !isGroupScope(scope) {
		return true
	}
	actor, _ := security.ActorFromContext(ctx)
	return actor.Role == security.RoleSuperadmin || actor.GroupRole == security.GroupRoleOwner || actor.GroupRole == security.GroupRoleAdmin
}

func memoryCanDelete(ctx context.Context, scope session.Scope, memory *angelmemory.Memory) bool {
	if memory == nil {
		return false
	}
	if memoryCanManageAll(ctx, scope) {
		return true
	}
	actor, _ := security.ActorFromContext(ctx)
	return strings.TrimSpace(actor.ID) != "" && strings.TrimSpace(actor.ID) == strings.TrimSpace(memory.SourceActorID)
}

func isGroupScope(scope session.Scope) bool {
	scopeID := strings.TrimSpace(scope.PlatformScopeID)
	return strings.HasPrefix(scopeID, "group:") || strings.HasPrefix(scopeID, "supergroup:")
}

func shortMemoryID(id string) string {
	id = strings.TrimSpace(id)
	runes := []rune(id)
	if len(runes) <= 12 {
		return id
	}
	return string(runes[:12])
}

func memoryEntryLine(index int, memory angelmemory.Memory) string {
	content := strings.Join(strings.Fields(memory.Content), " ")
	runes := []rune(content)
	if len(runes) > 80 {
		content = string(runes[:80]) + "…"
	}
	return fmt.Sprintf("%d. [%d] id=%s %s%s", index, memory.Strength, shortMemoryID(memory.ID), content, memorySourceText(memory))
}

func memorySourceText(memory angelmemory.Memory) string {
	parts := make([]string, 0, 4)
	if kind := strings.TrimSpace(memory.SourceKind); kind != "" {
		parts = append(parts, "kind="+kind)
	}
	if actorID := strings.TrimSpace(memory.SourceActorID); actorID != "" {
		parts = append(parts, "actor="+actorID)
	}
	if messageID := strings.TrimSpace(memory.SourceMessageID); messageID != "" {
		parts = append(parts, "message="+messageID)
	}
	if sessionID := strings.TrimSpace(memory.SourceSessionID); sessionID != "" {
		parts = append(parts, "session="+sessionID)
	}
	if len(parts) == 0 {
		if label := strings.TrimSpace(memory.Source); label != "" {
			parts = append(parts, "source="+label)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " ") + ")"
}

func memoryEntryDetail(memory angelmemory.Memory) string {
	lines := []string{
		fmt.Sprintf("id：%s", memory.ID),
		fmt.Sprintf("强度：%d", memory.Strength),
		fmt.Sprintf("标签：%s", firstNonEmpty(memory.Tags, "(无)")),
		fmt.Sprintf("内容：%s", memory.Content),
	}
	if source := strings.TrimSpace(memorySourceText(memory)); source != "" {
		lines = append(lines, "来源："+strings.TrimSpace(strings.Trim(source, " ()")))
	}
	return strings.Join(lines, "\n")
}

func memoryParseLimit(raw string, fallback, max int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	value := 0
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil || value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
