package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

type Kind string

const (
	KindText     Kind = "text"
	KindEmoticon Kind = "emoticon"
	KindImage    Kind = "image"
	KindFile     Kind = "file"
	KindRecord   Kind = "record"
	KindAt       Kind = "at"
	KindReply    Kind = "reply"
)

type Notice struct {
	Target  Target
	Outputs []Output
	Level   slog.Level
}

type Target struct {
	Platform      string `json:"platform,omitempty"`
	ScopeID       string `json:"scope_id,omitempty"`
	PrivateUserID string `json:"private_user_id,omitempty"`
	GroupID       string `json:"group_id,omitempty"`
	Superadmins   bool   `json:"superadmins,omitempty"`
}

const (
	MetaHookPoint      = "hook.point"
	MetaHookName       = "hook.name"
	MetaHookMode       = "hook.mode"
	MetaDeliveryTiming = "delivery.timing"
)

const (
	DeliveryImmediate      = "immediate"
	DeliveryAfterAssistant = "after_assistant"
)

type temporaryConnectionKey struct{}

func WithTemporaryConnection(ctx context.Context) context.Context {
	return context.WithValue(ctx, temporaryConnectionKey{}, true)
}

func UseTemporaryConnection(ctx context.Context) bool {
	value, _ := ctx.Value(temporaryConnectionKey{}).(bool)
	return value
}

func (t Target) Empty() bool {
	return strings.TrimSpace(t.Platform) == "" && strings.TrimSpace(t.ScopeID) == "" && strings.TrimSpace(t.PrivateUserID) == "" && strings.TrimSpace(t.GroupID) == "" && !t.Superadmins
}

type Source struct {
	URL      string
	Path     string
	MediaID  string `json:"media,omitempty"`
	MIMEType string
	Data     []byte
}

// IsHTTPMediaSource reports whether value is an HTTP(S) media source.
func IsHTTPMediaSource(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

type Output struct {
	Kind                     Kind
	Text                     string
	Name                     string
	EmoticonID               string
	AltText                  string
	ReplyToPlatformMessageID string
	Source                   Source
	Target                   Target
	Meta                     map[string]any
}

// ToolPreviewPrefix 是工具调用进度预览的固定前缀。Agent 在组织预览正文时写入它
// （`formatToolPreview`），平台适配器与历史写入用它识别"这条通知不是用户可见回答"。
// 这条规则此前在 OneBot、QQ 官方、hook 出站记录三处各写一遍，改一处会漏掉其余两处，
// 因此把前缀与判定集中在交付层，只留"是否群聊目标"由各平台自己决定。
const ToolPreviewPrefix = "[tool] "

// IsToolPreview 报告这条输出是否是工具调用进度预览。判定使用完整前缀（含分隔空格），
// 因此只有前缀、没有正文的 "[tool]" 不算预览；Agent 的 formatToolPreview 一定写出
// "前缀 + 正文"。适配器负责各自"整条通知只有一条文本输出"的检查。
func (out Output) IsToolPreview() bool {
	return out.Kind == KindText && strings.HasPrefix(strings.TrimSpace(out.Text), ToolPreviewPrefix)
}

// IsToolPreviewNotice 报告这条通知是否是"单条工具调用进度预览"。平台的群聊过滤规则
// 都用这个形状判断（一条文本输出 + 工具预览前缀），因此把它收敛到交付层。
func IsToolPreviewNotice(outputs []Output) bool {
	return len(outputs) == 1 && outputs[0].IsToolPreview()
}

// ShouldDropGroupToolPreview 是"群聊里不投递工具调用进度预览"这条平台跳过规则的唯一入口。
// 规则由三个条件组成：通知没有显式目标（发给当前会话）、当前上下文是群聊、且这条通知就是
// 工具调用进度预览——群聊里的进度预览是噪声，丢弃时也不算失败。
//
// 唯一按平台变化的部分是"当前上下文是不是群聊"：各适配器的上下文键类型不同（OneBot 用
// 消息类型，QQ 官方用 sendTarget.Kind），因此由调用方传入，其余判定不再各写一份。
// Telegram 与本地 CLI 不跳过预览：前者没有这条规则，后者的预览是本地终端输出。
func ShouldDropGroupToolPreview(target Target, outputs []Output, groupContext bool) bool {
	return target.Empty() && groupContext && IsToolPreviewNotice(outputs)
}

func WithDeliveryTiming(out Output, timing string) Output {
	timing = strings.TrimSpace(timing)
	if timing == "" || timing == DeliveryImmediate {
		return out
	}
	if out.Meta == nil {
		out.Meta = map[string]any{}
	}
	out.Meta[MetaDeliveryTiming] = timing
	return out
}

func DeliveryTiming(out Output) string {
	timing := outputMetaString(out, MetaDeliveryTiming)
	if timing == "" {
		return DeliveryImmediate
	}
	return timing
}

func ValidateDeliveryTiming(timing string) error {
	switch strings.TrimSpace(timing) {
	case "", DeliveryImmediate, DeliveryAfterAssistant:
		return nil
	default:
		return fmt.Errorf("unsupported timing %q", timing)
	}
}

func SplitByDeliveryTiming(outputs []Output) ([]Output, []Output) {
	if len(outputs) == 0 {
		return nil, nil
	}
	immediate := make([]Output, 0, len(outputs))
	deferred := make([]Output, 0)
	for _, out := range outputs {
		if DeliveryTiming(out) == DeliveryAfterAssistant {
			deferred = append(deferred, out)
			continue
		}
		immediate = append(immediate, out)
	}
	return immediate, deferred
}

func Text(text string) Output {
	return Output{Kind: KindText, Text: text}
}

func Emoticon(id, name, text string) Output {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	out := Output{Kind: KindEmoticon, EmoticonID: id, Name: name, Text: strings.TrimSpace(text)}
	if name != "" {
		out.AltText = "[表情: " + name + "]"
	} else if out.Text != "" {
		out.AltText = out.Text
	}
	return out
}

func ImagePath(path string) Output {
	return Output{Kind: KindImage, Source: Source{Path: path}}
}

func FilePath(path string) Output {
	return Output{Kind: KindFile, Source: Source{Path: path}}
}

func RecordPath(path string) Output {
	return Output{Kind: KindRecord, Source: Source{Path: path}}
}

func At(userID string) Output {
	userID = strings.TrimSpace(userID)
	out := Output{Kind: KindAt, Name: userID}
	if userID != "" {
		out.AltText = "@" + userID
	}
	return out
}

func Reply(platformMessageID, text string) Output {
	return Output{Kind: KindReply, Text: text, ReplyToPlatformMessageID: strings.TrimSpace(platformMessageID)}
}

// Receipt describes platform messages produced by a send operation.
type SentMessage struct {
	PlatformMessageID string
	Platform          string
	ScopeID           string
	OutputIndexes     []int
}

type Receipt struct {
	PlatformMessageIDs []string
	SentMessages       []SentMessage
	// Failed reports that the send attempt stopped before every requested
	// page/segment was accepted by the platform. PlatformMessageIDs and
	// SentMessages still describe the deliveries that did succeed, so a caller
	// can track and avoid re-sending those again.
	Failed bool
	// Failure is a short, non-secret reason for Failed. It is intentionally
	// not a wrapped error so the receipt stays serializable.
	Failure string
}

// MarkPartialFailure annotates a receipt that already contains successful
// deliveries with the failure that stopped the remaining ones.
func (r Receipt) MarkPartialFailure(err error) Receipt {
	if err == nil {
		return r
	}
	r.Failed = true
	if r.Failure == "" {
		r.Failure = err.Error()
	}
	return r
}

// Merge appends the successful deliveries from another receipt and preserves
// its partial-failure marker. Multi-output and multi-target send loops use
// this so the outer receipt does not lose a page-level failure recorded by an
// inner adapter call.
func (r Receipt) Merge(other Receipt) Receipt {
	r.PlatformMessageIDs = append(r.PlatformMessageIDs, other.PlatformMessageIDs...)
	r.SentMessages = append(r.SentMessages, other.SentMessages...)
	if other.Failed {
		r.Failed = true
		if r.Failure == "" {
			r.Failure = other.Failure
		}
	}
	return r
}

// StreamingMessageSender is an optional platform capability for editable streaming delivery.
// Platforms can implement it with terminal replacement, message editing, or any equivalent mechanism.
type StreamingMessageSender interface {
	StartStream(ctx context.Context) (MessageStream, error)
}

// MessageStream represents one assistant message that can be appended while streaming
// and replaced with the final post-hook content.
type MessageStream interface {
	Append(ctx context.Context, text string) error
	Replace(ctx context.Context, text string) (Receipt, error)
	Finish(ctx context.Context) (Receipt, error)
}

// MessageSender sends one logical message, represented by ordered output segments.
type MessageSender interface {
	SendChat(ctx context.Context, outputs []Output) (Receipt, error)
	SendNotice(ctx context.Context, notice Notice) (Receipt, error)
}

// ContextSender can send a reply using routing information carried by ctx.
type ContextSender interface {
	MessageSender
}

type Sender = MessageSender

type Manager struct {
	Sender Sender
	Logger *slog.Logger
}

func NewManager(sender Sender, logger *slog.Logger) Manager {
	return Manager{Sender: sender, Logger: logger}
}

func (m Manager) SendNotices(ctx context.Context, outputs []Output) error {
	_, err := m.SendNoticesWithReceipt(ctx, outputs)
	return err
}

// SendNoticesWithReceipt is the receipt-preserving form of SendNotices. When a
// multi-output send stops after some outputs already reached the platform, the
// returned receipt still describes those successful deliveries so callers can
// audit or reconcile the partial send instead of assuming nothing was sent.
func (m Manager) SendNoticesWithReceipt(ctx context.Context, outputs []Output) (Receipt, error) {
	return m.SendNotice(ctx, Notice{Outputs: outputs})
}

func (m Manager) SendChat(ctx context.Context, outputs []Output) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if m.Sender == nil {
		return Receipt{}, fmt.Errorf("output sender is not configured")
	}
	if err := ValidateOutputs(outputs); err != nil {
		return Receipt{}, err
	}
	receipt, err := m.Sender.SendChat(ctx, outputs)
	if err != nil {
		// Adapters already mark paginated sends; mark defensively here so a
		// platform that only accumulates IDs is not reported as a clean failure.
		return receipt.MarkPartialFailure(err), err
	}
	return receipt, nil
}

func (m Manager) SendNotice(ctx context.Context, notice Notice) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if m.Sender == nil || len(notice.Outputs) == 0 {
		return Receipt{}, nil
	}
	if err := ValidateOutputs(notice.Outputs); err != nil {
		return Receipt{}, err
	}
	configuredTarget, err := ValidateOutputsTarget(notice.Outputs)
	if err != nil {
		return Receipt{}, err
	}
	if notice.Target.Empty() {
		notice.Target = configuredTarget
	}
	receipt, err := m.Sender.SendNotice(ctx, notice)
	if err != nil {
		if m.Logger != nil {
			attrs := outputLogAttrs(notice.Outputs[0], "platform", notice.Target.Platform, "error", err.Error())
			m.Logger.WarnContext(ctx, "notice output failed", attrs...)
		}
		return receipt.MarkPartialFailure(err), wrapOutputSourceError(notice.Outputs[0], err)
	}
	return receipt, nil
}

func outputLogAttrs(out Output, attrs ...any) []any {
	if hookName := outputMetaString(out, MetaHookName); hookName != "" {
		attrs = append(attrs, "hook", hookName)
	}
	if hookPoint := outputMetaString(out, MetaHookPoint); hookPoint != "" {
		attrs = append(attrs, "hook_point", hookPoint)
	}
	if hookMode := outputMetaString(out, MetaHookMode); hookMode != "" {
		attrs = append(attrs, "hook_mode", hookMode)
	}
	return attrs
}

func wrapOutputSourceError(out Output, err error) error {
	if err == nil {
		return nil
	}
	if hookName := outputMetaString(out, MetaHookName); hookName != "" {
		return fmt.Errorf("hook %s output: %w", hookName, err)
	}
	if hookPoint := outputMetaString(out, MetaHookPoint); hookPoint != "" {
		return fmt.Errorf("hook %s output: %w", hookPoint, err)
	}
	return err
}

func outputMetaString(out Output, key string) string {
	if out.Meta == nil {
		return ""
	}
	value, ok := out.Meta[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func FallbackOutput(outputs []Output) Output {
	parts := make([]string, 0, len(outputs))
	for _, out := range outputs {
		if text := strings.TrimSpace(FallbackText(out)); text != "" {
			parts = append(parts, text)
		}
	}
	return Text(strings.Join(parts, "\n"))
}

func ValidateOutputsTarget(outputs []Output) (Target, error) {
	var target Target
	for _, out := range outputs {
		if out.Target.Empty() {
			continue
		}
		if target.Empty() {
			target = out.Target
			continue
		}
		if target != out.Target {
			return Target{}, fmt.Errorf("outputs in one batch must use the same target")
		}
	}
	return target, nil
}

func ValidateOutputs(outputs []Output) error {
	for i, out := range outputs {
		if err := ValidateDeliveryTiming(DeliveryTiming(out)); err != nil {
			return fmt.Errorf("outputs[%d]: %w", i, err)
		}
		sourceCount := 0
		if strings.TrimSpace(out.Source.Path) != "" {
			sourceCount++
		}
		if strings.TrimSpace(out.Source.MediaID) != "" {
			sourceCount++
		}
		if strings.TrimSpace(out.Source.URL) != "" {
			sourceCount++
		}
		if len(out.Source.Data) > 0 {
			sourceCount++
		}
		switch out.Kind {
		case KindText:
			if sourceCount != 0 {
				return fmt.Errorf("outputs[%d]: text output cannot have a media source", i)
			}
		case KindImage, KindFile, KindRecord:
			if sourceCount != 1 {
				return fmt.Errorf("outputs[%d]: image/file/record output must have exactly one media source", i)
			}
			if path := strings.TrimSpace(out.Source.Path); strings.Contains(path, "://") {
				return fmt.Errorf("outputs[%d]: media path must be a filesystem path, not a URI", i)
			}
			if value := strings.TrimSpace(out.Source.URL); value != "" {
				u, err := url.Parse(value)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return fmt.Errorf("outputs[%d]: media URL must be an absolute HTTP(S) URL", i)
				}
			}
		case KindEmoticon:
			if sourceCount != 0 {
				return fmt.Errorf("outputs[%d]: emoticon output cannot have a media source", i)
			}
			if strings.TrimSpace(out.EmoticonID) == "" {
				return fmt.Errorf("outputs[%d]: emoticon output requires an emoticon ID", i)
			}
		case KindAt:
			if strings.TrimSpace(out.Name) == "" {
				return fmt.Errorf("outputs[%d]: at output requires a user ID", i)
			}
		case KindReply:
			if strings.TrimSpace(out.ReplyToPlatformMessageID) == "" {
				return fmt.Errorf("outputs[%d]: reply output requires a message ID", i)
			}
		default:
			return fmt.Errorf("outputs[%d]: unsupported output kind %q", i, out.Kind)
		}
	}
	_, err := ValidateOutputsTarget(outputs)
	return err
}

func FallbackText(out Output) string {
	if out.AltText != "" {
		return out.AltText
	}
	switch out.Kind {
	case KindText:
		return out.Text
	case KindEmoticon:
		name := strings.TrimSpace(out.Name)
		if name == "" {
			name = strings.TrimSpace(out.Text)
		}
		if name == "" {
			return ""
		}
		return fmt.Sprintf("[表情: %s]", name)
	case KindAt:
		name := strings.TrimSpace(out.Name)
		if name == "" {
			name = strings.TrimSpace(out.Text)
		}
		if name == "" {
			return ""
		}
		return fmt.Sprintf("@%s", name)
	case KindReply:
		replyID := strings.TrimSpace(out.ReplyToPlatformMessageID)
		if replyID == "" {
			return out.Text
		}
		return fmt.Sprintf("[引用消息 %s]\n%s", replyID, out.Text)
	case KindImage:
		label := firstNonEmpty(out.Name, out.Source.URL, out.Source.Path, out.Text)
		if label == "" {
			return "[图片]"
		}
		return fmt.Sprintf("[图片: %s]", label)
	case KindFile:
		label := firstNonEmpty(out.Name, out.Source.URL, out.Source.Path, out.Text)
		if label == "" {
			return "[文件]"
		}
		return fmt.Sprintf("[文件: %s]", label)
	case KindRecord:
		label := firstNonEmpty(out.Name, out.Source.URL, out.Source.Path, out.Text)
		if label == "" {
			return "[语音]"
		}
		return fmt.Sprintf("[语音: %s]", label)
	default:
		return firstNonEmpty(out.Text, out.Name, out.AltText)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
