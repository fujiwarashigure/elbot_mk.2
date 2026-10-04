package platform

import (
	"context"
	"encoding/json"
	"time"

	"elbot/internal/delivery"
	"elbot/internal/security"
)

// PlatformAdapter is the interface for message platform adapters (CLI, QQ, etc.).
type PlatformAdapter interface {
	Name() string
	Run(ctx context.Context, handler PlatformHandler) error
	delivery.MessageSender
}

// PlatformHandler processes incoming messages from a platform.
type PlatformHandler interface {
	HandleMessage(ctx context.Context, text string) error
}

// EventKind is the platform-neutral class of a non-message event.
type EventKind string

const (
	EventNotice    EventKind = "notice"
	EventRequest   EventKind = "request"
	EventMetaEvent EventKind = "meta_event"
)

// PlatformEvent is the platform-neutral view of a notice, request or lifecycle
// event. It is intentionally small: adapters keep platform-specific fields in
// Meta/Raw, while the local event policy only needs stable routing fields.
type PlatformEvent struct {
	Platform  string
	Kind      EventKind
	Type      string
	ScopeID   string
	UserID    string
	MessageID string
	Raw       json.RawMessage
	Meta      map[string]any
}

// PlatformEventHandler is implemented by handlers that want deterministic
// notice/request/lifecycle events in addition to chat messages.
type PlatformEventHandler interface {
	HandlePlatformEvent(ctx context.Context, event PlatformEvent) error
}

// ConnectNotifier is implemented by adapters that can report successful platform connections.
type ConnectNotifier interface {
	SetConnectNotifier(func(context.Context, string))
}

// Runtime is the lifecycle and send surface shared by platform adapters.
type Runtime interface {
	Name() string
	Run(ctx context.Context, handler PlatformHandler) error
	delivery.MessageSender
}

type MessageSegmentType string

const (
	SegmentText  MessageSegmentType = "text"
	SegmentImage MessageSegmentType = "image"
	SegmentFile  MessageSegmentType = "file"
	SegmentAt    MessageSegmentType = "at"
)

// MessageSegment is one typed part parsed from an inbound platform message.
type MessageSegment struct {
	Type           MessageSegmentType `json:"type"`
	Text           string             `json:"text,omitempty"`
	UserID         string             `json:"user_id,omitempty"`
	URL            string             `json:"url,omitempty"`
	MediaID        string             `json:"media,omitempty"`
	PlatformFileID string             `json:"platform_file_id,omitempty"`
	MIMEType       string             `json:"mime_type,omitempty"`
	Name           string             `json:"name,omitempty"`
	Size           int64              `json:"size,omitempty"`
}

type MediaResolver interface {
	ResolveMedia(context.Context, MessageSegment, int64) (delivery.Source, error)
}

// GroupInfo is the platform-neutral subset used by group analysis.
type GroupInfo struct {
	ID          string
	Name        string
	MemberCount int
}

// GroupMember is the platform-neutral subset used by group analysis.
type GroupMember struct {
	UserID      string
	Nickname    string
	GroupCard   string
	DisplayName string
	Role        string
}

// HistoryMessage is a platform-neutral history row returned by an optional
// GroupHistoryProvider. Adapters that cannot fetch remote history may leave the
// interface unimplemented and callers should fall back to local chat history.
type HistoryMessage struct {
	PlatformMessageID string
	SenderID          string
	SenderName        string
	Text              string
	Segments          []MessageSegment
	CreatedAt         time.Time
}

// GroupHistoryProvider is implemented by platform adapters that can fetch
// remote group history. Callers must treat it as an optional capability.
type GroupHistoryProvider interface {
	FetchGroupHistory(ctx context.Context, groupID string, since, until time.Time, limit int) ([]HistoryMessage, error)
}

// GroupDirectoryProvider is implemented by platform adapters that can resolve
// group metadata and member lists.
type GroupDirectoryProvider interface {
	GetGroupInfo(ctx context.Context, groupID string) (GroupInfo, error)
	GetGroupMemberList(ctx context.Context, groupID string) ([]GroupMember, error)
}

// UserAvatarProvider is implemented by platform adapters that can resolve a
// user avatar URL.
type UserAvatarProvider interface {
	GetUserAvatarURL(ctx context.Context, userID string, size int) (string, error)
}

// GroupAssetProvider covers optional group file and album uploads.
type GroupAssetProvider interface {
	UploadGroupFile(ctx context.Context, groupID, filePath, filename string) error
	UploadGroupAlbum(ctx context.Context, groupID, imagePath, albumID, albumName string) error
}

type ReplyContext struct {
	MessageID  string
	SenderID   string
	SenderName string
	Text       string
	Segments   []MessageSegment
}

type ConversationKind string

const (
	ConversationUnknown ConversationKind = "unknown"
	ConversationPrivate ConversationKind = "private"
	ConversationGroup   ConversationKind = "group"
	ConversationChannel ConversationKind = "channel"
)

type Identity struct {
	UserID   string
	Username string
}

type Mention struct {
	UserID   string
	Username string
	Text     string
}

// MessageContext carries per-message platform routing and actor data.
type MessageContext struct {
	Platform              string
	ActorID               string
	PlatformUserID        string
	Nickname              string
	GroupCard             string
	DisplayName           string
	GroupRole             security.GroupRole
	ScopeID               string
	ConversationKind      ConversationKind
	PlatformMessageID     string
	ReplyToMessageID      string
	ReplyToSenderID       string
	Sender                delivery.ContextSender
	BufferAssistantOutput bool
	ForkFromMessageID     string
	ResumeSessionID       string
	Segments              []MessageSegment
	ContextText           string
	ContextSegments       []MessageSegment
	Reply                 ReplyContext
	Meta                  map[string]any
	RawText               string
	// MatchText is the wake/command matching view. It excludes untrusted
	// forwarded content. MatchTextSet distinguishes a deliberate empty match
	// view (forward-only message) from an adapter that does not provide one.
	MatchText       string
	MatchTextSet    bool
	PlatformMessage json.RawMessage
	Bot             Identity
	Mentions        []Mention
	TriggerKeywords []string
	MediaResolver   MediaResolver
}

type messageContextKey struct{}

func WithMessageContext(ctx context.Context, msg MessageContext) context.Context {
	return context.WithValue(ctx, messageContextKey{}, msg)
}

func MessageContextFrom(ctx context.Context) (MessageContext, bool) {
	msg, ok := ctx.Value(messageContextKey{}).(MessageContext)
	return msg, ok
}
