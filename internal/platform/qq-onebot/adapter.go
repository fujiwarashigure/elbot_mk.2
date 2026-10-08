package qqonebot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"elbot/internal/config"
	"elbot/internal/delivery"
	"elbot/internal/platform"
	"elbot/internal/platform/refcontext"
	"elbot/internal/security"
	"elbot/internal/storage"
)

const (
	qqTextPageRunes     = 3000
	sendFileModeBase64  = "base64"
	sendFileModeFileURI = "file_uri"
)

type Config struct {
	Enabled                    bool     `toml:"enabled"`
	URL                        string   `toml:"ws_url"`
	AccessToken                string   `toml:"access_token"`
	AccessTokenEnv             string   `toml:"access_token_env"`
	ReconnectIntervalSeconds   int      `toml:"reconnect_interval_seconds"`
	APITimeoutSeconds          int      `toml:"api_timeout_seconds"`
	TriggerKeywords            []string `toml:"trigger_keywords"`
	SendFileMode               string   `toml:"send_file_mode"`
	ForwardMaxNodes            int      `toml:"forward_max_nodes"`
	ForwardMaxRunes            int      `toml:"forward_max_runes"`
	ForwardMaxDepth            int      `toml:"forward_max_depth"`
	ForwardMaxFetches          int      `toml:"forward_max_fetches"`
	ForwardMaxResultBytes      int      `toml:"forward_max_result_bytes"`
	ForwardMaxNonText          int      `toml:"forward_max_non_text"`
	ForwardFetchTimeoutSeconds int      `toml:"forward_fetch_timeout_seconds"`
	// InboundDedupEnabled defaults to true when unset. It suppresses OneBot
	// reconnect replays using platform+bot+scope+message-id, independent of
	// chat history.
	InboundDedupEnabled    *bool `toml:"inbound_dedup_enabled"`
	InboundDedupTTLSeconds int   `toml:"inbound_dedup_ttl_seconds"`
	InboundDedupMaxEntries int   `toml:"inbound_dedup_max_entries"`
	// Preprocess workers bound @ / quote / merged-forward network lookups.
	// The normal queue is non-blocking: when it is full the duplicate-prone
	// message is rejected locally instead of spawning unbounded goroutines.
	PreprocessWorkers     int      `toml:"preprocess_workers"`
	PreprocessQueueSize   int      `toml:"preprocess_queue_size"`
	HighPriorityWorkers   int      `toml:"high_priority_workers"`
	HighPriorityQueueSize int      `toml:"high_priority_queue_size"`
	AttachmentDir         string   `toml:"-"`
	MaxReceiveFileBytes   int64    `toml:"-"`
	DownloadTimeoutSecs   int      `toml:"-"`
	Superadmins           []string `toml:"-"`
	CommandPrefixes       []string `toml:"-"`
}

// qqTextPages splits one long text into page-sized chunks. Pagination markers
// are gone: a multi-page answer is delivered as one merged-forward message.
func qqTextPages(text string) []string {
	runes := []rune(text)
	if len(runes) <= qqTextPageRunes {
		return []string{text}
	}
	pages := make([]string, 0, (len(runes)+qqTextPageRunes-1)/qqTextPageRunes)
	for start := 0; start < len(runes); start += qqTextPageRunes {
		end := min(start+qqTextPageRunes, len(runes))
		pages = append(pages, string(runes[start:end]))
	}
	return pages
}

// qqForwardNodes wraps every page in one node of a merged-forward message, so a
// long answer arrives as a single expandable card instead of many bubbles.
func qqForwardNodes(pages []string) []Segment {
	nodes := make([]Segment, 0, len(pages))
	for _, page := range pages {
		nodes = append(nodes, Segment{Type: "node", Data: map[string]any{
			"content": []Segment{{Type: "text", Data: map[string]any{"text": page}}},
		}})
	}
	return nodes
}

func (a *Adapter) sendQQText(ctx context.Context, t target, text string) (string, error) {
	switch t.MessageType {
	case "private":
		return a.transport.SendPrivateMessage(ctx, t.UserID, text)
	case "group":
		return a.transport.SendGroupMessage(ctx, t.GroupID, text)
	default:
		return "", fmt.Errorf("unsupported message target %q", t.MessageType)
	}
}

type Adapter struct {
	cfg         Config
	store       storage.Store
	chatHistory storage.ChatHistoryRepository
	transport   *Transport
	logger      *slog.Logger
	notify      func(context.Context, string)
	inbound     *inboundRuntime
}

type target struct {
	MessageType string
	UserID      int64
	GroupID     int64
}

type targetKey struct{}

func NewFromPlatformConfig(raw map[string]any, store storage.Store, chatHistory storage.ChatHistoryRepository, logger *slog.Logger, superadmins []string, commandPrefixes []string, configEnvDir, attachmentDir string, maxReceiveFileBytes int64, downloadTimeoutSecs int) (*Adapter, error) {
	var cfg Config
	if err := platform.DecodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode qqonebot config: %w", err)
	}
	cfg.Superadmins = superadmins
	cfg.CommandPrefixes = append([]string(nil), commandPrefixes...)
	cfg.AttachmentDir = strings.TrimSpace(attachmentDir)
	cfg.MaxReceiveFileBytes = maxReceiveFileBytes
	cfg.DownloadTimeoutSecs = downloadTimeoutSecs
	applyDefaults(&cfg)
	if err := validateSendFileMode(cfg.SendFileMode); err != nil {
		return nil, err
	}
	envName := strings.TrimSpace(cfg.AccessTokenEnv)
	if strings.TrimSpace(cfg.AccessToken) == "" && envName != "" {
		value, _, err := config.ConfigEnv(envName, configEnvDir)
		if err != nil {
			return nil, fmt.Errorf("resolve qqonebot access token from %s: %w", envName, err)
		}
		cfg.AccessToken = strings.TrimSpace(value)
	}
	return New(cfg, store, chatHistory, logger), nil
}

func applyDefaults(cfg *Config) {
	if cfg.URL == "" {
		cfg.URL = "ws://127.0.0.1:6700/"
	}
	if cfg.ReconnectIntervalSeconds <= 0 {
		cfg.ReconnectIntervalSeconds = 3
	}
	if cfg.APITimeoutSeconds <= 0 {
		cfg.APITimeoutSeconds = 15
	}
	cfg.SendFileMode = strings.ToLower(strings.TrimSpace(cfg.SendFileMode))
	if cfg.SendFileMode == "" {
		cfg.SendFileMode = sendFileModeBase64
	}
	if len(cfg.CommandPrefixes) == 0 {
		cfg.CommandPrefixes = []string{"/"}
	}
	if cfg.MaxReceiveFileBytes <= 0 {
		cfg.MaxReceiveFileBytes = 100 * 1024 * 1024
	}
	if cfg.DownloadTimeoutSecs <= 0 {
		cfg.DownloadTimeoutSecs = 60
	}
	if cfg.ForwardMaxNodes <= 0 {
		cfg.ForwardMaxNodes = defaultForwardMaxNodes
	}
	if cfg.ForwardMaxRunes <= 0 {
		cfg.ForwardMaxRunes = defaultForwardMaxRunes
	}
	if cfg.ForwardMaxDepth <= 0 {
		cfg.ForwardMaxDepth = defaultForwardMaxDepth
	}
	if cfg.ForwardMaxFetches <= 0 {
		cfg.ForwardMaxFetches = defaultForwardMaxFetches
	}
	if cfg.ForwardMaxResultBytes <= 0 {
		cfg.ForwardMaxResultBytes = defaultForwardMaxResultBytes
	}
	if cfg.ForwardMaxNonText <= 0 {
		cfg.ForwardMaxNonText = defaultForwardMaxNonText
	}
	if cfg.ForwardFetchTimeoutSeconds <= 0 {
		cfg.ForwardFetchTimeoutSeconds = int(defaultForwardFetchTimeout / time.Second)
	}
	if cfg.InboundDedupEnabled == nil {
		enabled := true
		cfg.InboundDedupEnabled = &enabled
	}
	if cfg.InboundDedupTTLSeconds <= 0 {
		cfg.InboundDedupTTLSeconds = int(defaultInboundDedupTTL / time.Second)
	}
	if cfg.InboundDedupMaxEntries <= 0 {
		cfg.InboundDedupMaxEntries = defaultInboundDedupMax
	}
	if cfg.PreprocessWorkers <= 0 {
		cfg.PreprocessWorkers = defaultPreprocessWorkers
	}
	if cfg.PreprocessQueueSize <= 0 {
		cfg.PreprocessQueueSize = defaultPreprocessQueue
	}
	if cfg.HighPriorityWorkers <= 0 {
		cfg.HighPriorityWorkers = defaultHighPriorityWorkers
	}
	if cfg.HighPriorityQueueSize <= 0 {
		cfg.HighPriorityQueueSize = defaultHighPriorityQueue
	}
}

func (a *Adapter) forwardLimits() ForwardLimits {
	return ForwardLimits{
		MaxNodes:       a.cfg.ForwardMaxNodes,
		MaxRunes:       a.cfg.ForwardMaxRunes,
		MaxDepth:       a.cfg.ForwardMaxDepth,
		MaxFetches:     a.cfg.ForwardMaxFetches,
		MaxResultBytes: a.cfg.ForwardMaxResultBytes,
		MaxNonText:     a.cfg.ForwardMaxNonText,
		FetchTimeout:   time.Duration(a.cfg.ForwardFetchTimeoutSeconds) * time.Second,
	}
}

func validateSendFileMode(mode string) error {
	switch mode {
	case sendFileModeBase64, sendFileModeFileURI:
		return nil
	default:
		return fmt.Errorf("qqonebot send_file_mode must be %q or %q, got %q", sendFileModeBase64, sendFileModeFileURI, mode)
	}
}

func New(cfg Config, store storage.Store, chatHistory storage.ChatHistoryRepository, logger *slog.Logger) *Adapter {
	applyDefaults(&cfg)

	timeout := time.Duration(cfg.APITimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Adapter{
		cfg:         cfg,
		store:       store,
		chatHistory: chatHistory,
		transport: &Transport{
			URL:            cfg.URL,
			AccessToken:    cfg.AccessToken,
			Timeout:        timeout,
			ReadLimitBytes: int64(cfg.ForwardMaxResultBytes),
			logger:         logger,
		},
		logger:  logger,
		inbound: newInboundRuntime(cfg),
	}
}

func (a *Adapter) Name() string { return "qqonebot" }

func (a *Adapter) Enabled() bool { return a.cfg.Enabled }

func (a *Adapter) SetConnectNotifier(notify func(context.Context, string)) {
	a.notify = notify
}

func (a *Adapter) notifyConnected(ctx context.Context) {
	if a.notify != nil {
		a.notify(ctx, a.Name())
	}
}

func (a *Adapter) Run(ctx context.Context, handler platform.PlatformHandler) error {
	if !a.cfg.Enabled {
		return nil
	}
	interval := time.Duration(a.cfg.ReconnectIntervalSeconds) * time.Second
	backoff := platform.NewBackoff(interval, 10*time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.transport.Connect(ctx); err != nil {
			if backoff.ShouldWarn() {
				a.logWarn("onebot connect failed", "error", err)
			}
			if !sleepContext(ctx, backoff.Delay()) {
				return ctx.Err()
			}
			continue
		}
		backoff.Reset()
		a.logInfo("onebot connected", "url", a.cfg.URL)
		go a.notifyConnected(ctx)
		err := a.readLoop(ctx, handler)
		a.transport.Close(websocket.StatusNormalClosure, "reconnect")
		if err != nil && !errors.Is(err, context.Canceled) {
			a.logWarn("onebot disconnected", "error", err)
		}
		if !sleepContext(ctx, backoff.Delay()) {
			return ctx.Err()
		}
	}
}

func (a *Adapter) SendChat(ctx context.Context, outputs []delivery.Output) (delivery.Receipt, error) {
	if text, ok := textOutputs(outputs); ok {
		return a.sendContextText(ctx, text)
	}
	t, ok := ctx.Value(targetKey{}).(target)
	if !ok {
		return delivery.Receipt{}, fmt.Errorf("qq send target missing")
	}
	segments, err := outputSegments(a.cfg.SendFileMode, outputs...)
	if err != nil {
		return delivery.Receipt{}, err
	}
	receipt, err := a.sendSegments(ctx, t, segments)
	return oneBotMediaReceipt(receipt, t, outputs), err
}

func (a *Adapter) CallPlatformAPI(ctx context.Context, api string, params map[string]any) (json.RawMessage, error) {
	if a.transport == nil {
		return nil, fmt.Errorf("qqonebot transport is not configured")
	}
	resp, err := a.transport.Call(ctx, strings.TrimSpace(api), params)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (a *Adapter) sendTemporaryNotice(ctx context.Context, notice delivery.Notice) (delivery.Receipt, error) {
	transport := &Transport{URL: a.cfg.URL, AccessToken: a.cfg.AccessToken, Timeout: time.Duration(a.cfg.APITimeoutSeconds) * time.Second, logger: a.logger}
	if err := transport.Connect(ctx); err != nil {
		return delivery.Receipt{}, err
	}
	defer transport.Close(websocket.StatusNormalClosure, "temporary elnis delivery done")
	go transport.readResponses(ctx)
	t, err := targetToQQ(notice.Target)
	if err != nil {
		return delivery.Receipt{}, err
	}
	segments, err := outputSegments(a.cfg.SendFileMode, notice.Outputs...)
	if err != nil {
		return delivery.Receipt{}, err
	}
	var id string
	switch t.MessageType {
	case "private":
		id, err = transport.SendPrivateSegments(ctx, t.UserID, segments)
	case "group":
		id, err = transport.SendGroupSegments(ctx, t.GroupID, segments)
	default:
		err = fmt.Errorf("unsupported message target %q", t.MessageType)
	}
	receipt := receiptWithMessageID(id)
	return oneBotMediaReceipt(receipt, t, notice.Outputs), err
}

func (a *Adapter) SendNotice(ctx context.Context, notice delivery.Notice) (delivery.Receipt, error) {
	if delivery.UseTemporaryConnection(ctx) {
		return a.sendTemporaryNotice(ctx, notice)
	}
	outTarget := notice.Target
	outputs := notice.Outputs
	if outTarget.Empty() && isGroupToolPreviewNotice(ctx, outputs) {
		return delivery.Receipt{}, nil
	}
	if outTarget.Empty() {
		return a.SendChat(ctx, outputs)
	}
	if outTarget.Superadmins {
		if len(a.cfg.Superadmins) == 0 {
			return delivery.Receipt{}, fmt.Errorf("qqonebot superadmins are not configured")
		}
		var receipt delivery.Receipt
		for _, id := range a.cfg.Superadmins {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			copyTarget := outTarget
			copyTarget.Superadmins = false
			copyTarget.PrivateUserID = id
			copyTarget.GroupID = ""
			copyTarget.ScopeID = ""
			notice.Target = copyTarget
			sent, err := a.SendNotice(ctx, notice)
			receipt = receipt.Merge(sent)
			if err != nil {
				return receipt.MarkPartialFailure(err), err
			}
		}
		return receipt, nil
	}
	t, err := targetToQQ(outTarget)
	if err != nil {
		return delivery.Receipt{}, err
	}
	segments, err := outputSegments(a.cfg.SendFileMode, outputs...)
	if err != nil {
		return delivery.Receipt{}, err
	}
	receipt, err := a.sendSegments(ctx, t, segments)
	return oneBotMediaReceipt(receipt, t, outputs), err
}

func textOutputs(outputs []delivery.Output) (string, bool) {
	var text strings.Builder
	for _, out := range outputs {
		if out.Kind != delivery.KindText {
			return "", false
		}
		text.WriteString(out.Text)
	}
	return text.String(), true
}
func isGroupToolPreviewNotice(ctx context.Context, outputs []delivery.Output) bool {
	if len(outputs) != 1 || outputs[0].Kind != delivery.KindText || !strings.HasPrefix(strings.TrimSpace(outputs[0].Text), "[tool]") {
		return false
	}
	t, ok := ctx.Value(targetKey{}).(target)
	return ok && t.MessageType == "group"
}

func (a *Adapter) sendContextText(ctx context.Context, text string) (delivery.Receipt, error) {
	if strings.TrimSpace(text) == "" {
		return delivery.Receipt{}, nil
	}
	t, ok := ctx.Value(targetKey{}).(target)
	if !ok {
		return delivery.Receipt{}, fmt.Errorf("qq send target missing")
	}
	pages := qqTextPages(text)
	if len(pages) > 1 {
		return a.sendForwardPages(ctx, t, pages)
	}
	id, err := a.sendQQText(ctx, t, text)
	if err != nil {
		return delivery.Receipt{}, err
	}
	return receiptWithMessageID(id), nil
}

// sendForwardPages sends one merged-forward message; OneBot answers with a
// single message id, so the receipt has no per-page failure state.
func (a *Adapter) sendForwardPages(ctx context.Context, t target, pages []string) (delivery.Receipt, error) {
	nodes := qqForwardNodes(pages)
	var id string
	var err error
	switch t.MessageType {
	case "private":
		id, err = a.transport.SendPrivateForwardMessage(ctx, t.UserID, nodes)
	case "group":
		id, err = a.transport.SendGroupForwardMessage(ctx, t.GroupID, nodes)
	default:
		err = fmt.Errorf("unsupported message target %q", t.MessageType)
	}
	if err != nil {
		return delivery.Receipt{}, err
	}
	return receiptWithMessageID(id), nil
}

func (a *Adapter) sendContextOutput(ctx context.Context, out delivery.Output) (delivery.Receipt, error) {
	return a.SendChat(ctx, []delivery.Output{out})
}

func (a *Adapter) sendSegments(ctx context.Context, t target, segments []Segment) (delivery.Receipt, error) {
	switch t.MessageType {
	case "private":
		id, err := a.transport.SendPrivateSegments(ctx, t.UserID, segments)
		return receiptWithMessageID(id), err
	case "group":
		id, err := a.transport.SendGroupSegments(ctx, t.GroupID, segments)
		return receiptWithMessageID(id), err
	default:
		return delivery.Receipt{}, fmt.Errorf("unsupported message target %q", t.MessageType)
	}
}

func (a *Adapter) sendTarget(ctx context.Context, outTarget delivery.Target, out delivery.Output) (delivery.Receipt, error) {
	return a.SendNotice(ctx, delivery.Notice{Target: outTarget, Outputs: []delivery.Output{out}})
}

func receiptWithMessageID(id string) delivery.Receipt {
	id = strings.TrimSpace(id)
	if id == "" {
		return delivery.Receipt{}
	}
	return delivery.Receipt{PlatformMessageIDs: []string{id}}
}

func oneBotMediaReceipt(receipt delivery.Receipt, target target, outputs []delivery.Output) delivery.Receipt {
	if len(receipt.PlatformMessageIDs) != 1 {
		return receipt
	}
	indexes := make([]int, 0, len(outputs))
	for i, out := range outputs {
		if out.Kind == delivery.KindImage || out.Kind == delivery.KindFile || out.Kind == delivery.KindRecord {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) == 0 {
		return receipt
	}
	receipt.SentMessages = append(receipt.SentMessages, delivery.SentMessage{PlatformMessageID: receipt.PlatformMessageIDs[0], Platform: "qqonebot", ScopeID: oneBotTargetScope(target), OutputIndexes: indexes})
	return receipt
}

func oneBotTargetScope(target target) string {
	if target.MessageType == "group" {
		return "group:" + strconv.FormatInt(target.GroupID, 10)
	}
	if target.MessageType == "private" {
		return "private:" + strconv.FormatInt(target.UserID, 10)
	}
	return ""
}
func targetToQQ(outTarget delivery.Target) (target, error) {
	if strings.TrimSpace(outTarget.PrivateUserID) == "" && strings.TrimSpace(outTarget.GroupID) == "" {
		scope := strings.TrimSpace(outTarget.ScopeID)
		if strings.HasPrefix(scope, "private:") {
			outTarget.PrivateUserID = strings.TrimPrefix(scope, "private:")
		} else if strings.HasPrefix(scope, "group:") {
			outTarget.GroupID = strings.TrimPrefix(scope, "group:")
		}
	}
	if userID := strings.TrimSpace(outTarget.PrivateUserID); userID != "" {
		id, err := strconv.ParseInt(userID, 10, 64)
		if err != nil {
			return target{}, fmt.Errorf("parse qqonebot private user id: %w", err)
		}
		return target{MessageType: "private", UserID: id}, nil
	}
	if groupID := strings.TrimSpace(outTarget.GroupID); groupID != "" {
		id, err := strconv.ParseInt(groupID, 10, 64)
		if err != nil {
			return target{}, fmt.Errorf("parse qqonebot group id: %w", err)
		}
		return target{MessageType: "group", GroupID: id}, nil
	}
	return target{}, fmt.Errorf("qqonebot target missing private_user_id, group_id or scope_id")
}

func outputSegments(sendFileMode string, outputs ...delivery.Output) ([]Segment, error) {
	segments := make([]Segment, 0, len(outputs))
	for _, out := range outputs {
		switch out.Kind {
		case delivery.KindText:
			segments = append(segments, Segment{Type: "text", Data: map[string]any{"text": out.Text}})
		case delivery.KindReply:
			replyID := strings.TrimSpace(out.ReplyToPlatformMessageID)
			if replyID == "" {
				return nil, fmt.Errorf("reply target message id is empty")
			}
			segments = append(segments, Segment{Type: "reply", Data: map[string]any{"id": replyID}}, Segment{Type: "text", Data: map[string]any{"text": out.Text}})
		case delivery.KindEmoticon:
			id := strings.TrimSpace(out.EmoticonID)
			if id == "" {
				return nil, fmt.Errorf("emoticon id is empty")
			}
			segments = append(segments, Segment{Type: "face", Data: map[string]any{"id": id}})
		case delivery.KindImage:
			file, err := oneBotSourceFile(out.Source, "image", sendFileMode)
			if err != nil {
				return nil, err
			}
			segments = append(segments, Segment{Type: "image", Data: map[string]any{"file": file}})
		case delivery.KindFile:
			file, err := oneBotSourceFile(out.Source, "file", sendFileMode)
			if err != nil {
				return nil, err
			}
			data := map[string]any{"file": file}
			if name := strings.TrimSpace(out.Name); name != "" {
				data["name"] = name
			}
			segments = append(segments, Segment{Type: "file", Data: data})
		case delivery.KindRecord:
			file, err := oneBotSourceFile(out.Source, "record", sendFileMode)
			if err != nil {
				return nil, err
			}
			segments = append(segments, Segment{Type: "record", Data: map[string]any{"file": file}})
		case delivery.KindAt:
			qq := strings.TrimSpace(out.Name)
			if qq == "" {
				qq = strings.TrimSpace(out.Text)
			}
			if qq == "" {
				return nil, fmt.Errorf("at target is empty")
			}
			segments = append(segments, Segment{Type: "at", Data: map[string]any{"qq": qq}})
		default:
			return nil, fmt.Errorf("unsupported output kind %q", out.Kind)
		}
	}
	return segments, nil
}

func oneBotGroupRole(event Event) security.GroupRole {
	if event.MessageType != "group" {
		return security.GroupRoleUnknown
	}
	return security.ParseGroupRole(event.Sender.Role)
}

func oneBotSourceFile(source delivery.Source, label, sendFileMode string) (string, error) {
	if len(source.Data) > 0 {
		return "base64://" + base64.StdEncoding.EncodeToString(source.Data), nil
	}
	if sourceURL := strings.TrimSpace(source.URL); sourceURL != "" {
		return sourceURL, nil
	}
	path := strings.TrimSpace(source.Path)
	if path == "" {
		return "", fmt.Errorf("%s path is empty", label)
	}
	switch sendFileMode {
	case sendFileModeBase64:
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s path %q: %w", label, path, err)
		}
		return "base64://" + base64.StdEncoding.EncodeToString(data), nil
	case sendFileModeFileURI:
		return localPathFileURI(path, label)
	default:
		return "", fmt.Errorf("unsupported qqonebot send_file_mode %q", sendFileMode)
	}
}

func localPathFileURI(path, label string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s path %q: %w", label, path, err)
	}
	slashPath := filepath.ToSlash(abs)
	if strings.HasPrefix(slashPath, "//") {
		trimmed := strings.TrimPrefix(slashPath, "//")
		host, rest, ok := strings.Cut(trimmed, "/")
		if ok {
			return (&url.URL{Scheme: "file", Host: host, Path: "/" + rest}).String(), nil
		}
	}
	if filepath.VolumeName(abs) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String(), nil
}

func (a *Adapter) readLoop(ctx context.Context, handler platform.PlatformHandler) error {
	dispatcher := newEventDispatcher(a, ctx, handler, a.cfg.PreprocessWorkers, a.cfg.PreprocessQueueSize, a.cfg.HighPriorityWorkers, a.cfg.HighPriorityQueueSize)
	defer dispatcher.stop()
	for {
		event, err := a.transport.Read(ctx)
		if err != nil {
			return err
		}
		if event.PostType == "" {
			continue
		}
		if a.isMessageEvent(event) {
			a.dispatchMessageEvent(dispatcher, event)
			continue
		}
		switch event.PostType {
		case "notice", "request", "meta_event":
			job := outboundEventJob{event: event}
			if !dispatcher.enqueueHigh(job) {
				// Recall, member and admin events must not be dropped or
				// delayed behind ordinary chat traffic. Running inline is the
				// bounded fallback: the read loop waits for this one event
				// instead of spawning an unbounded goroutine.
				a.handlePlatformEvent(ctx, handler, event)
			}
		}
	}
}

func (a *Adapter) dispatchMessageEvent(dispatcher *eventDispatcher, event Event) {
	if dispatcher == nil {
		return
	}
	key := a.inboundKey(event)
	if key != "" {
		duplicate, state := a.beginInboundJob(key)
		if duplicate {
			a.logInfo("duplicate qq message ignored", "message_id", event.MessageID, "state", state)
			return
		}
	}
	if dispatcher.enqueueNormal(outboundEventJob{event: event, messageKey: key}) {
		return
	}
	a.finishInboundJob(key, inboundDedupStateFailed, "preprocess_queue_full")
	if a.inbound != nil {
		a.inbound.recordReject(time.Now())
	}
	a.logWarn("qq inbound preprocess queue full, message dropped", "message_id", event.MessageID)
}

func (a *Adapter) handlePlatformEvent(ctx context.Context, handler platform.PlatformHandler, event Event) {
	eventHandler, ok := handler.(platform.PlatformEventHandler)
	if !ok {
		return
	}
	noticeType := strings.ToLower(strings.TrimSpace(event.NoticeType))
	userID := firstNonZero(event.OperatorID, event.UserID)
	if strings.Contains(noticeType, "decrease") || strings.Contains(noticeType, "ban") {
		userID = firstNonZero(event.UserID, event.TargetID)
	}
	messageID := ""
	if event.MessageID != 0 {
		messageID = strconv.FormatInt(event.MessageID, 10)
	}
	platformEvent := platform.PlatformEvent{
		Platform:  a.Name(),
		Kind:      platform.EventKind(event.PostType),
		Type:      firstNonEmpty(event.NoticeType, event.RequestType, event.PostType),
		ScopeID:   eventScopeID(event),
		UserID:    strconv.FormatInt(userID, 10),
		MessageID: messageID,
		Meta: map[string]any{
			"qq_onebot.post_type":    event.PostType,
			"qq_onebot.message_type": event.MessageType,
			"qq_onebot.sub_type":     event.SubType,
			"qq_onebot.notice_type":  event.NoticeType,
			"qq_onebot.request_type": event.RequestType,
			"qq_onebot.group_id":     strconv.FormatInt(event.GroupID, 10),
			"qq_onebot.user_id":      strconv.FormatInt(event.UserID, 10),
			"qq_onebot.operator_id":  strconv.FormatInt(event.OperatorID, 10),
			"qq_onebot.target_id":    strconv.FormatInt(event.TargetID, 10),
			"qq_onebot.self_id":      strconv.FormatInt(event.SelfID, 10),
		},
	}
	if data, err := json.Marshal(event); err == nil {
		platformEvent.Raw = data
	}
	if err := eventHandler.HandlePlatformEvent(ctx, platformEvent); err != nil {
		a.logWarn("handle qq platform event failed", "post_type", event.PostType, "type", platformEvent.Type, "error", err)
	}
}

func (a *Adapter) handleEvent(ctx context.Context, handler platform.PlatformHandler, event Event) error {
	_, msgCtx, text, ok := a.prepareInbound(ctx, ctx, event)
	if !ok {
		return nil
	}
	if err := handler.HandleMessage(msgCtx, text); err != nil {
		a.logWarn("handle qq message failed", "error", err, "message_id", event.MessageID)
		return err
	}
	return nil
}

func (a *Adapter) resolveForwardSegments(ctx context.Context, event Event, msg NormalizedMessage) NormalizedMessage {
	if len(msg.ForwardIDs) == 0 {
		return msg
	}
	if a.transport == nil {
		return msg
	}
	state := newForwardParseState(a.forwardLimits())
	var sb strings.Builder
	sb.WriteString(msg.Text)
	seen := map[string]bool{}
	for _, id := range msg.ForwardIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if seen[id] {
			writeForwardFallback(&sb, "[重复转发引用 id:"+id+"，已省略]")
			continue
		}
		seen[id] = true
		if state.fetches >= state.limits.MaxFetches {
			state.truncated = true
			writeForwardFallback(&sb, "[转发拉取次数达到上限，剩余转发已省略]")
			break
		}
		state.fetches++
		fetchCtx := ctx
		cancel := func() {}
		if state.limits.FetchTimeout > 0 {
			fetchCtx, cancel = context.WithTimeout(ctx, state.limits.FetchTimeout)
		}
		raw, err := a.transport.GetForwardMsg(fetchCtx, id)
		cancel()
		if err != nil {
			a.logWarn("get qq forward message failed", "message_id", id, "error", err)
			writeForwardFallback(&sb, "[转发消息 id:"+id+" 拉取失败]")
			continue
		}
		if len(raw) > state.limits.MaxResultBytes {
			state.truncated = true
			writeForwardFallback(&sb, "[转发消息 id:"+id+" 超过体积上限，已省略]")
			continue
		}
		body, parsedID, _ := renderForwardData(raw, state)
		if strings.TrimSpace(body) == "" && parsedID != "" {
			body = "[转发消息 id:" + parsedID + "]"
		}
		if strings.TrimSpace(body) == "" {
			writeForwardFallback(&sb, "[转发消息 id:"+id+" 无可展示文本]")
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(forwardTrustMarker)
		sb.WriteString("\n")
		sb.WriteString(body)
	}
	if state.truncated {
		sb.WriteString(forwardTruncatedMarker)
	}
	msg.Text = cleanText(sb.String())
	if len(msg.Segments) == 0 && msg.Text != "" {
		msg.Segments = []platform.MessageSegment{{Type: platform.SegmentText, Text: msg.Text}}
	}
	return msg
}

func writeForwardFallback(sb *strings.Builder, text string) {
	if sb == nil || strings.TrimSpace(text) == "" {
		return
	}
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString(text)
}

func (a *Adapter) resolveAtSegments(ctx context.Context, event Event, msg NormalizedMessage) NormalizedMessage {
	if event.MessageType != "group" || event.GroupID == 0 || a.transport == nil || len(msg.Segments) == 0 {
		return msg
	}
	updated := append([]platform.MessageSegment(nil), msg.Segments...)
	replacements := map[string]string{}
	changed := false
	for i := range updated {
		if updated[i].Type != platform.SegmentAt || updated[i].UserID == "" {
			continue
		}
		userID, err := strconv.ParseInt(updated[i].UserID, 10, 64)
		if err != nil || userID == event.SelfID {
			continue
		}
		sender, err := a.transport.GetGroupMemberInfo(ctx, event.GroupID, userID)
		if err != nil {
			a.logWarn("get qq group member info failed", "group_id", event.GroupID, "user_id", userID, "error", err)
			continue
		}
		text := atText(updated[i].UserID, senderName(sender))
		if text == updated[i].Text {
			continue
		}
		replacements[updated[i].Text] = text
		updated[i].Text = text
		changed = true
	}
	if !changed {
		return msg
	}
	for oldText, newText := range replacements {
		msg.Text = strings.ReplaceAll(msg.Text, oldText, newText)
	}
	msg.Segments = updated
	return msg
}

func oneBotConversationKind(event Event) platform.ConversationKind {
	switch event.MessageType {
	case "private":
		return platform.ConversationPrivate
	case "group":
		return platform.ConversationGroup
	default:
		return platform.ConversationUnknown
	}
}

func (a *Adapter) replyToSenderID(ctx context.Context, event Event, replyID string) string {
	replyID = strings.TrimSpace(replyID)
	if replyID == "" {
		return ""
	}
	if a.store != nil {
		if msg, err := a.store.Messages().FindByPlatformMessage(ctx, a.Name(), scopeID(event), replyID); err == nil && msg.Role == storage.RoleAssistant {
			return strconv.FormatInt(event.SelfID, 10)
		}
		if outputs, err := a.store.Media().FindOutputs(ctx, a.Name(), scopeID(event), replyID, time.Now()); err == nil && len(outputs) > 0 {
			return strconv.FormatInt(event.SelfID, 10)
		}
	}
	if a.chatHistory != nil {
		if msg, err := a.chatHistory.GetByPlatformMessage(ctx, a.Name(), scopeID(event), replyID); err == nil {
			return msg.SenderID
		}
	}
	if a.transport == nil {
		return ""
	}
	data, err := a.transport.GetMessage(ctx, replyID)
	if err != nil || data.UserID == 0 {
		return ""
	}
	return strconv.FormatInt(data.UserID, 10)
}

func (a *Adapter) referenceFetcher(event Event) func(context.Context, string) (refcontext.ReferencedMessage, bool) {
	return func(ctx context.Context, replyID string) (refcontext.ReferencedMessage, bool) {
		if a.transport == nil {
			return refcontext.ReferencedMessage{}, false
		}
		data, err := a.transport.GetMessage(ctx, replyID)
		if err != nil {
			return refcontext.ReferencedMessage{}, false
		}
		ref := normalizeMessage(data.Message, data.RawMessage, event.SelfID)
		// A merged forward quoted in a reply must reach the model too. Only this
		// quoted level is expanded, so a hostile quote cannot pull in an
		// unbounded forward tree.
		if len(ref.ForwardIDs) > 0 {
			ref = a.resolveForwardSegments(ctx, event, ref)
		}
		label := "引用"
		if data.UserID != 0 {
			label = "引用：" + displayName(data.Sender, data.UserID)
		}
		return refcontext.ReferencedMessage{SenderID: strconv.FormatInt(data.UserID, 10), SenderName: displayName(data.Sender, data.UserID), Label: label, Text: ref.Text, Segments: ref.Segments}, true
	}
}

func (a *Adapter) recordChatMessage(ctx context.Context, event Event, normalized NormalizedMessage, reply platform.ReplyContext) {
	if a.chatHistory == nil || (strings.TrimSpace(normalized.Text) == "" && len(normalized.Segments) == 0) || event.MessageID == 0 {
		return
	}
	createdAt := storage.Now()
	if event.Time > 0 {
		createdAt = time.Unix(event.Time, 0)
	}
	message := &storage.ChatMessage{
		Platform:                 a.Name(),
		PlatformScopeID:          scopeID(event),
		ScopeType:                event.MessageType,
		PlatformMessageID:        strconv.FormatInt(event.MessageID, 10),
		SenderID:                 strconv.FormatInt(event.UserID, 10),
		SenderName:               senderName(event.Sender),
		Text:                     normalized.Text,
		Raw:                      normalized.Text,
		Segments:                 platform.MarshalChatSegments(normalized.Segments),
		ReplyToPlatformMessageID: normalized.ReplyID,
		Metadata:                 refcontext.MarshalChatMetadata(reply),
		CreatedAt:                createdAt,
	}
	if err := a.chatHistory.Append(ctx, message); err != nil {
		a.logWarn("record qq chat message failed", "error", err, "message_id", event.MessageID)
	}
}

func finalMessageSegments(text string, current, referenced []platform.MessageSegment) []platform.MessageSegment {
	out := make([]platform.MessageSegment, 0, 1+len(current)+len(referenced))
	if strings.TrimSpace(text) != "" {
		out = append(out, platform.MessageSegment{Type: platform.SegmentText, Text: text})
	}
	out = appendNonTextSegments(out, current)
	out = appendNonTextSegments(out, referenced)
	return out
}

func appendNonTextSegments(out []platform.MessageSegment, segments []platform.MessageSegment) []platform.MessageSegment {
	for _, segment := range segments {
		if segment.Type != platform.SegmentText {
			out = append(out, segment)
		}
	}
	return out
}

func (a *Adapter) isMessageEvent(event Event) bool {
	return event.PostType == "message" && (event.MessageType == "private" || event.MessageType == "group")
}

func scopeID(event Event) string {
	if event.MessageType == "group" {
		return fmt.Sprintf("group:%d", event.GroupID)
	}
	return fmt.Sprintf("private:%d", event.UserID)
}

func eventScopeID(event Event) string {
	if event.GroupID != 0 {
		return fmt.Sprintf("group:%d", event.GroupID)
	}
	if event.UserID != 0 {
		return fmt.Sprintf("private:%d", event.UserID)
	}
	return ""
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func isConfiguredSuperadmin(superadmins []string, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, candidate := range superadmins {
		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "qqonebot:"))
		if candidate == id {
			return true
		}
	}
	return false
}

func (a *Adapter) logInfo(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Info(msg, args...)
	}
}

func (a *Adapter) logWarn(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Warn(msg, args...)
	}
}
