package session

import (
	"encoding/json"
	"strings"

	"elbot/internal/storage"
)

// sharedThreadOwnerPrefix namespaces the synthetic owner used by a group
// thread Session. Using a stable owner keeps the existing storage schema and
// owner index unchanged while letting every member of one platform scope
// resolve the same current Session.
const sharedThreadOwnerPrefix = "group-thread:"

func sharedThreadOwnerID(scope Scope) string {
	platform := strings.TrimSpace(scope.Platform)
	scopeID := strings.TrimSpace(scope.PlatformScopeID)
	return sharedThreadOwnerPrefix + platform + ":" + scopeID
}

func sessionOwnerID(scope Scope) string {
	if scope.Shared {
		return sharedThreadOwnerID(scope)
	}
	return scope.ActorID
}

func sharedThreadMetadataForScope(scope Scope, raw string) string {
	metadata := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &metadata)
	}
	if scope.Shared {
		metadata["thread_mode"] = "group"
	}
	if len(metadata) == 0 {
		return ""
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return raw
	}
	return string(data)
}

func isSharedThreadSession(session *storage.Session) bool {
	if session == nil || strings.TrimSpace(session.Metadata) == "" {
		return false
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(session.Metadata), &metadata); err != nil {
		return false
	}
	mode, _ := metadata["thread_mode"].(string)
	return strings.EqualFold(strings.TrimSpace(mode), "group")
}
