package qqonebot

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"elbot/internal/platform"
	"elbot/internal/platform/refcontext"
	"elbot/internal/security"
)

// Inbound preprocessing is deliberately separated from the Agent request
// queue: resolving @ handles, quoted messages and merged forwards performs
// network calls, so a burst of hostile or accidental forwards must not create
// unbounded goroutines or block recall/admin events behind chat traffic.
//
// Deduplication is also independent of chat history: history=off must not make
// OneBot reconnect replays wake the model twice.

const (
	inboundDedupStateProcessing = "processing"
	inboundDedupStateCompleted  = "completed"
	inboundDedupStateFailed     = "failed"

	defaultInboundDedupTTL     = 30 * time.Minute
	defaultInboundDedupMax     = 4096
	defaultPreprocessWorkers   = 4
	defaultPreprocessQueue     = 256
	defaultHighPriorityWorkers = 1
	defaultHighPriorityQueue   = 128
)

type inboundDedupEntry struct {
	state string
	at    time.Time
	note  string
}

// inboundDeduper is a small, bounded, TTL map keyed by
// platform|bot|scope|message-id. The failed state is terminal inside the TTL:
// replaying the same platform message is suppressed even if the first attempt
// failed, which is what prevents reconnect storms from multiplying paid calls.
type inboundDeduper struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	now     func() time.Time
	entries map[string]inboundDedupEntry
}

func newInboundDeduper(ttl time.Duration, max int) *inboundDeduper {
	if ttl <= 0 {
		ttl = defaultInboundDedupTTL
	}
	if max <= 0 {
		max = defaultInboundDedupMax
	}
	return &inboundDeduper{
		ttl:     ttl,
		max:     max,
		now:     time.Now,
		entries: map[string]inboundDedupEntry{},
	}
}

func (d *inboundDeduper) begin(key string) (state string, duplicate bool) {
	key = strings.TrimSpace(key)
	if d == nil || key == "" {
		return "", false
	}
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pruneLocked(now)
	if entry, ok := d.entries[key]; ok {
		return entry.state, true
	}
	d.entries[key] = inboundDedupEntry{state: inboundDedupStateProcessing, at: now}
	if len(d.entries) > d.max {
		d.evictOldestLocked()
	}
	return inboundDedupStateProcessing, false
}

func (d *inboundDeduper) finish(key, state, note string) {
	key = strings.TrimSpace(key)
	if d == nil || key == "" {
		return
	}
	if state == "" {
		state = inboundDedupStateCompleted
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, ok := d.entries[key]
	if !ok {
		return
	}
	entry.state = state
	entry.note = strings.TrimSpace(note)
	entry.at = d.now()
	d.entries[key] = entry
}

func (d *inboundDeduper) pruneLocked(now time.Time) {
	for key, entry := range d.entries {
		if now.Sub(entry.at) > d.ttl {
			delete(d.entries, key)
		}
	}
}

func (d *inboundDeduper) evictOldestLocked() {
	for len(d.entries) > d.max {
		oldestKey := ""
		var oldest time.Time
		for key, entry := range d.entries {
			if oldestKey == "" || entry.at.Before(oldest) {
				oldestKey = key
				oldest = entry.at
			}
		}
		if oldestKey == "" {
			return
		}
		delete(d.entries, oldestKey)
	}
}

// inboundKey binds one platform message to a stable identity. It uses the
// adapter-visible bot id when available so two bots connected to the same
// account layout cannot suppress each other.
func (a *Adapter) inboundKey(event Event) string {
	if a == nil || event.MessageID == 0 {
		return ""
	}
	scope := eventScopeID(event)
	if scope == "" {
		return ""
	}
	parts := []string{
		a.Name(),
		strconv.FormatInt(event.SelfID, 10),
		scope,
		strconv.FormatInt(event.MessageID, 10),
	}
	return strings.Join(parts, "|")
}

// InboundStats is a small, read-only view of the inbound preprocessing path.
type InboundStats struct {
	DedupHits         int64
	QueueRejected     int64
	LastQueueRejectAt time.Time
}

type inboundRuntime struct {
	dedup         *inboundDeduper
	dedupHits     atomic.Int64
	queueRejected atomic.Int64
	lastRejectMu  sync.Mutex
	lastRejectAt  time.Time
}

func newInboundRuntime(cfg Config) *inboundRuntime {
	if cfg.InboundDedupEnabled != nil && !*cfg.InboundDedupEnabled {
		return &inboundRuntime{}
	}
	return &inboundRuntime{dedup: newInboundDeduper(time.Duration(cfg.InboundDedupTTLSeconds)*time.Second, cfg.InboundDedupMaxEntries)}
}

func (r *inboundRuntime) recordReject(now time.Time) {
	if r == nil {
		return
	}
	r.queueRejected.Add(1)
	r.lastRejectMu.Lock()
	r.lastRejectAt = now
	r.lastRejectMu.Unlock()
}

func (a *Adapter) InboundStats() InboundStats {
	if a == nil || a.inbound == nil {
		return InboundStats{}
	}
	r := a.inbound
	stats := InboundStats{
		DedupHits:     r.dedupHits.Load(),
		QueueRejected: r.queueRejected.Load(),
	}
	r.lastRejectMu.Lock()
	stats.LastQueueRejectAt = r.lastRejectAt
	r.lastRejectMu.Unlock()
	return stats
}

func (a *Adapter) beginInboundJob(key string) (duplicate bool, state string) {
	if a == nil || a.inbound == nil || a.inbound.dedup == nil || strings.TrimSpace(key) == "" {
		return false, ""
	}
	state, duplicate = a.inbound.dedup.begin(key)
	if duplicate {
		a.inbound.dedupHits.Add(1)
	}
	return duplicate, state
}

func (a *Adapter) finishInboundJob(key, state, note string) {
	if a == nil || a.inbound == nil || a.inbound.dedup == nil {
		return
	}
	a.inbound.dedup.finish(key, state, note)
}

type outboundEventJob struct {
	event      Event
	messageKey string
}

// eventDispatcher owns the bounded normal-message preprocessing pool and the
// dedicated high-priority notice/request channel. High-priority events are
// never queued behind normal chat messages; if the small high-priority queue
// is saturated the read loop executes the event inline instead of dropping it.
type eventDispatcher struct {
	adapter  *Adapter
	handler  platform.PlatformHandler
	lifeCtx  context.Context
	workCtx  context.Context
	cancel   context.CancelFunc
	normal   chan outboundEventJob
	high     chan outboundEventJob
	normalWg sync.WaitGroup
	highWg   sync.WaitGroup
}

func newEventDispatcher(adapter *Adapter, lifeCtx context.Context, handler platform.PlatformHandler, workers, queueSize, highWorkers, highQueueSize int) *eventDispatcher {
	if workers <= 0 {
		workers = defaultPreprocessWorkers
	}
	if queueSize <= 0 {
		queueSize = defaultPreprocessQueue
	}
	if highWorkers <= 0 {
		highWorkers = defaultHighPriorityWorkers
	}
	if highQueueSize <= 0 {
		highQueueSize = defaultHighPriorityQueue
	}
	workCtx, cancel := context.WithCancel(lifeCtx)
	d := &eventDispatcher{
		adapter: adapter,
		handler: handler,
		lifeCtx: lifeCtx,
		workCtx: workCtx,
		cancel:  cancel,
		normal:  make(chan outboundEventJob, queueSize),
		high:    make(chan outboundEventJob, highQueueSize),
	}
	for i := 0; i < workers; i++ {
		d.normalWg.Add(1)
		go d.normalWorker()
	}
	for i := 0; i < highWorkers; i++ {
		d.highWg.Add(1)
		go d.highWorker()
	}
	return d
}

func (d *eventDispatcher) stop() {
	if d == nil {
		return
	}
	d.cancel()
	close(d.normal)
	close(d.high)
	d.normalWg.Wait()
	d.highWg.Wait()
}

func (d *eventDispatcher) enqueueNormal(job outboundEventJob) bool {
	if d == nil {
		return false
	}
	select {
	case d.normal <- job:
		return true
	default:
		return false
	}
}

func (d *eventDispatcher) enqueueHigh(job outboundEventJob) bool {
	if d == nil {
		return false
	}
	select {
	case d.high <- job:
		return true
	default:
		return false
	}
}

func (d *eventDispatcher) normalWorker() {
	defer d.normalWg.Done()
	for job := range d.normal {
		select {
		case <-d.workCtx.Done():
			return
		default:
		}
		d.prepareAndDispatch(job)
	}
}

func (d *eventDispatcher) highWorker() {
	defer d.highWg.Done()
	for job := range d.high {
		d.adapter.handlePlatformEvent(d.lifeCtx, d.handler, job.event)
	}
}

// prepareInbound performs every network-backed preprocessing step and returns
// the context and text the Agent should process. It does not touch the Agent
// queue, so the caller decides whether to run the turn synchronously (tests) or
// hand it to the request manager (read loop).
func (a *Adapter) prepareInbound(prepCtx, baseCtx context.Context, event Event) (platform.MessageContext, context.Context, string, bool) {
	normalized := normalizeMessageWithLimits(event.Message, event.RawMessage, event.SelfID, a.forwardLimits())
	normalized = a.resolveAtSegments(prepCtx, event, normalized)
	normalized = a.resolveForwardSegments(prepCtx, event, normalized)
	if event.MessageType != "private" && event.MessageType != "group" {
		a.recordChatMessage(baseCtx, event, normalized, platform.ReplyContext{})
		return platform.MessageContext{}, nil, "", false
	}
	text := normalized.Text
	currentSegments := normalized.Segments
	messageCtx := platform.MessageContext{
		Platform:              a.Name(),
		PlatformUserID:        strconv.FormatInt(event.UserID, 10),
		Nickname:              strings.TrimSpace(event.Sender.Nickname),
		GroupCard:             strings.TrimSpace(event.Sender.Card),
		DisplayName:           displayName(event.Sender, event.UserID),
		GroupRole:             oneBotGroupRole(event),
		ScopeID:               scopeID(event),
		ConversationKind:      oneBotConversationKind(event),
		PlatformMessageID:     strconv.FormatInt(event.MessageID, 10),
		ReplyToMessageID:      normalized.ReplyID,
		ReplyToSenderID:       a.replyToSenderID(prepCtx, event, normalized.ReplyID),
		MediaResolver:         a,
		Sender:                a,
		BufferAssistantOutput: true,
		Segments:              finalMessageSegments(text, currentSegments, nil),
		RawText:               normalized.Text,
		MatchText:             normalized.MatchText,
		MatchTextSet:          true,
		PlatformMessage:       append(json.RawMessage(nil), event.Message...),
		Bot:                   platform.Identity{UserID: strconv.FormatInt(event.SelfID, 10)},
		Mentions:              append([]platform.Mention(nil), normalized.Mentions...),
		TriggerKeywords:       append([]string(nil), a.cfg.TriggerKeywords...),
		Meta: map[string]any{
			"qq_onebot.message_id":   strconv.FormatInt(event.MessageID, 10),
			"qq_onebot.message_type": event.MessageType,
			"qq_onebot.group_id":     strconv.FormatInt(event.GroupID, 10),
			"qq_onebot.user_id":      strconv.FormatInt(event.UserID, 10),
		},
	}
	msgCtx := platform.WithMessageContext(baseCtx, messageCtx)
	msgCtx = context.WithValue(msgCtx, targetKey{}, target{MessageType: event.MessageType, UserID: event.UserID, GroupID: event.GroupID})

	var referenceSegments []platform.MessageSegment
	if normalized.ReplyID != "" {
		ref := refcontext.Apply(prepCtx, refcontext.Options{
			Store:           a.store,
			ChatHistory:     a.chatHistory,
			Platform:        a.Name(),
			ScopeID:         messageCtx.ScopeID,
			ActorID:         security.ActorID(a.Name(), strconv.FormatInt(event.UserID, 10)),
			IsSuperadmin:    isConfiguredSuperadmin(a.cfg.Superadmins, strconv.FormatInt(event.UserID, 10)),
			ReplyID:         normalized.ReplyID,
			Text:            text,
			CommandPrefixes: a.cfg.CommandPrefixes,
			Fetch:           a.referenceFetcher(event),
		})
		messageCtx.ForkFromMessageID = ref.ForkFromMessageID
		messageCtx.ResumeSessionID = ref.ResumeSessionID
		messageCtx.ContextText = ref.Text
		messageCtx.Reply = ref.Reply
		referenceSegments = ref.ReferenceSegments
		if strings.TrimSpace(ref.Text) != "" || len(referenceSegments) > 0 {
			messageCtx.ContextSegments = finalMessageSegments(ref.Text, currentSegments, referenceSegments)
		}
	}
	a.recordChatMessage(baseCtx, event, normalized, messageCtx.Reply)
	messageCtx.Segments = finalMessageSegments(text, currentSegments, nil)
	msgCtx = platform.WithMessageContext(baseCtx, messageCtx)
	msgCtx = context.WithValue(msgCtx, targetKey{}, target{MessageType: event.MessageType, UserID: event.UserID, GroupID: event.GroupID})
	if strings.TrimSpace(text) == "" && len(currentSegments) == 0 {
		return messageCtx, msgCtx, "", false
	}
	return messageCtx, msgCtx, text, true
}

// prepareAndDispatch runs preprocessing in a bounded worker, then hands the
// prepared turn to the Agent on a separate goroutine so a long model turn does
// not hold a preprocessing worker. The Agent request manager remains the fair,
// capacity-bounded queue for actual turns.
func (d *eventDispatcher) prepareAndDispatch(job outboundEventJob) {
	_, msgCtx, text, ok := d.adapter.prepareInbound(d.workCtx, d.lifeCtx, job.event)
	if !ok {
		d.adapter.finishInboundJob(job.messageKey, inboundDedupStateCompleted, "preprocess_empty")
		return
	}
	turnText := text
	go func() {
		err := d.handler.HandleMessage(msgCtx, turnText)
		if err != nil {
			d.adapter.finishInboundJob(job.messageKey, inboundDedupStateFailed, err.Error())
			d.adapter.logWarn("handle qq message failed", "error", err, "message_id", job.event.MessageID)
			return
		}
		d.adapter.finishInboundJob(job.messageKey, inboundDedupStateCompleted, "")
	}()
}
