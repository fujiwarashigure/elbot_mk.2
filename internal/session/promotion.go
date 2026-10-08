package session

import (
	"encoding/json"
	"errors"
	"strings"

	"elbot/internal/storage"
)

// ErrForegroundSession 表示该 Session 已被前台接管：后台任务不能再把它当作自己的
// 任务 Session 继续写入。
var ErrForegroundSession = errors.New("会话已由前台接管")

// ForegroundOrigin 记录 Session 被前台接管前的后台来源，便于诊断“这个 session 原本
// 是哪个后台任务的”。
type ForegroundOrigin struct {
	Kind     string `json:"kind,omitempty"`
	OwnerID  string `json:"owner_id,omitempty"`
	Platform string `json:"platform,omitempty"`
	ScopeID  string `json:"scope_id,omitempty"`
}

const (
	metadataKeyForegroundOrigin = "foreground_origin"
	metadataKeyBackgroundKind   = "background_kind"
)

// WasPromoted 报告该 Session 是否已被前台接管。接管标记随 metadata 持久化，进程重启
// 后依然生效。
func WasPromoted(row *storage.Session) bool {
	if row == nil {
		return false
	}
	raw, ok := sessionMetadataFields(row.Metadata)[metadataKeyForegroundOrigin]
	if !ok || len(raw) == 0 {
		return false
	}
	var origin ForegroundOrigin
	if err := json.Unmarshal(raw, &origin); err != nil {
		return false
	}
	return origin.Kind != "" || origin.ScopeID != "" || origin.Platform != "" || origin.OwnerID != ""
}

// IsBackground 报告该 Session 是否属于后台任务（cron / elnis）。已被前台接管的
// Session 已不再是后台 Session。
func IsBackground(row *storage.Session) bool {
	if row == nil || WasPromoted(row) {
		return false
	}
	if backgroundKind(row) != "" {
		return true
	}
	scopeID := strings.TrimSpace(row.PlatformScopeID)
	return strings.HasPrefix(scopeID, "cron:") || strings.HasPrefix(scopeID, "elnis:")
}

func backgroundKind(row *storage.Session) string {
	if row == nil {
		return ""
	}
	raw, ok := sessionMetadataFields(row.Metadata)[metadataKeyBackgroundKind]
	if !ok || len(raw) == 0 {
		return ""
	}
	var kind string
	if err := json.Unmarshal(raw, &kind); err != nil {
		return ""
	}
	return strings.TrimSpace(kind)
}

// promoteToForeground 把后台 Session 原地变成当前前台的普通 Session：记录
// foreground_origin、清掉 background_kind、切到当前 scope 并回到 work 模式。它必须
// 在 store 的事务内调用。
func promoteToForeground(row *storage.Session, scope Scope) error {
	fields := sessionMetadataFields(row.Metadata)
	origin := ForegroundOrigin{
		Kind:     backgroundKind(row),
		OwnerID:  row.OwnerID,
		Platform: row.Platform,
		ScopeID:  row.PlatformScopeID,
	}
	encoded, err := json.Marshal(origin)
	if err != nil {
		return err
	}
	fields[metadataKeyForegroundOrigin] = encoded
	delete(fields, metadataKeyBackgroundKind)
	merged, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	row.Metadata = string(merged)
	row.OwnerID = sessionOwnerID(scope)
	row.Platform = scope.Platform
	row.PlatformScopeID = scope.PlatformScopeID
	row.Mode = storage.SessionModeWork
	return nil
}

// sessionMetadataFields 把 metadata 解成原始键值，未知键原样保留。
func sessionMetadataFields(raw string) map[string]json.RawMessage {
	fields := map[string]json.RawMessage{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fields
	}
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return map[string]json.RawMessage{}
	}
	return fields
}
