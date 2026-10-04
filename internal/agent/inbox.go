package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"elbot/internal/platform"
	"elbot/internal/storage"
	"elbot/internal/turn"
)

// sessionInbox serializes ordinary chat turns for one Session and merges
// consecutive messages from the same actor inside a short quiet window.
//
// The inbox is opt-in: it is only used when a group enables a merge window or
// shared thread mode. Submitters intentionally block until their batch has run,
// so the platform adapter still sees one synchronous HandleMessage call per
// inbound message and the existing error/recall semantics stay intact.
const maxInboxItemsPerSession = 64

type sessionInbox struct {
	mu     sync.Mutex
	queues map[string]*inboxQueue
}

type inboxQueue struct {
	mu           sync.Mutex
	batches      []*inboxBatch
	running      bool
	fullNotified bool
}

type inboxItem struct {
	input      turn.Input
	ctx        context.Context
	messageKey string
}

type inboxBatch struct {
	actorID  string
	fairKey  string
	scopeKey string
	items    []inboxItem
	deadline time.Time
	sealed   bool
	done     chan struct{}
	err      error
	waiters  int

	finishOnce sync.Once
}

func (b *inboxBatch) finish(err error) {
	if b == nil {
		return
	}
	b.finishOnce.Do(func() {
		b.err = err
		close(b.done)
	})
}

func newSessionInbox() *sessionInbox {
	return &sessionInbox{queues: map[string]*inboxQueue{}}
}

func (a *Agent) ensureInbox() *sessionInbox {
	if a == nil {
		return nil
	}
	a.inboxMu.Lock()
	defer a.inboxMu.Unlock()
	if a.inbox == nil {
		a.inbox = newSessionInbox()
	}
	return a.inbox
}

func (a *Agent) inboxSubmit(ctx context.Context, sessionID string, input turn.Input, window time.Duration, fairKey, scopeKey, messageKey string) error {
	if a == nil {
		return errors.New("agent is nil")
	}
	inbox := a.ensureInbox()
	if inbox == nil {
		return errors.New("agent inbox is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	actorID := strings.TrimSpace(input.Speaker.ActorID)
	if actorID == "" {
		actorID = strings.TrimSpace(input.Speaker.UserID)
	}
	if actorID == "" {
		// Never merge messages with no stable actor identity; the fallback
		// also keeps the batch's fair-key/recall bookkeeping deterministic.
		actorID = "anonymous:" + storage.NewID()
	}

	queue := a.inboxQueue(sessionID)
	batch := &inboxBatch{
		actorID:  actorID,
		fairKey:  strings.TrimSpace(fairKey),
		scopeKey: strings.TrimSpace(scopeKey),
		deadline: time.Now().Add(window),
		done:     make(chan struct{}),
	}
	item := inboxItem{input: input, ctx: ctx, messageKey: strings.TrimSpace(messageKey)}

	queue.mu.Lock()
	queuedItems := 0
	for _, pendingBatch := range queue.batches {
		if pendingBatch != nil {
			queuedItems += len(pendingBatch.items)
		}
	}
	if queuedItems >= maxInboxItemsPerSession {
		notify := !queue.fullNotified
		queue.fullNotified = true
		queue.mu.Unlock()
		a.audit("inbox_queue_full", "session_id", sessionID, "actor_id", actorID, "queued", queuedItems, "limit", maxInboxItemsPerSession)
		if notify {
			a.sendChat(ctx, "当前会话待处理消息过多，请稍后再试。")
		}
		return markUserNotified(errors.New("session inbox queue is full"))
	}
	queue.fullNotified = false
	merged := false
	if window > 0 && len(queue.batches) > 0 {
		tail := queue.batches[len(queue.batches)-1]
		if tail != nil && !tail.sealed && tail.actorID == actorID {
			tail.items = append(tail.items, item)
			tail.deadline = time.Now().Add(window)
			batch = tail
			merged = true
		}
	}
	if merged {
		batch.waiters++
		waiter := batch.waiters
		queue.mu.Unlock()
		return waitInboxBatch(ctx, batch, waiter)
	}
	batch.items = append(batch.items, item)
	batch.waiters = 1
	queue.batches = append(queue.batches, batch)
	if !queue.running {
		queue.running = true
		go a.runInboxQueue(sessionID, queue)
	}
	queue.mu.Unlock()
	return waitInboxBatch(ctx, batch, 1)
}

func waitInboxBatch(ctx context.Context, batch *inboxBatch, waiter int) error {
	select {
	case <-batch.done:
		if batch.err != nil && waiter > 1 {
			// Only the first waiter reports the turn error to the user; the
			// additional merged messages must not trigger duplicate notices.
			return markUserNotified(batch.err)
		}
		return batch.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Agent) inboxQueue(sessionID string) *inboxQueue {
	inbox := a.ensureInbox()
	if inbox == nil {
		return &inboxQueue{}
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if inbox.queues == nil {
		inbox.queues = map[string]*inboxQueue{}
	}
	queue := inbox.queues[sessionID]
	if queue == nil {
		queue = &inboxQueue{}
		inbox.queues[sessionID] = queue
	}
	return queue
}

func (a *Agent) runInboxQueue(sessionID string, queue *inboxQueue) {
	for {
		queue.mu.Lock()
		if len(queue.batches) == 0 {
			queue.running = false
			queue.mu.Unlock()
			return
		}
		head := queue.batches[0]
		wait := time.Until(head.deadline)
		if wait > 0 {
			queue.mu.Unlock()
			time.Sleep(wait)
			continue
		}
		head.sealed = true
		queue.batches = queue.batches[1:]
		queue.mu.Unlock()

		a.runInboxBatch(sessionID, head)
	}
}

func (a *Agent) runInboxBatch(sessionID string, batch *inboxBatch) {
	if batch == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			batch.finish(fmt.Errorf("inbox turn panic: %v", recovered))
		}
	}()
	items := make([]inboxItem, 0, len(batch.items))
	for _, item := range batch.items {
		if item.ctx != nil && item.ctx.Err() != nil {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		batch.finish(context.Canceled)
		return
	}
	inputs := make([]turn.Input, 0, len(items))
	for _, item := range items {
		inputs = append(inputs, item.input)
	}
	merged := turn.MergeInputs(inputs)
	runCtx := items[0].ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	messageKeys := make([]string, 0, len(items))
	for _, item := range items {
		if item.messageKey != "" {
			messageKeys = append(messageKeys, item.messageKey)
		}
	}
	runCtx = withMessageWorkKeys(runCtx, messageKeys)

	session, err := a.inboxSession(runCtx, sessionID)
	if err != nil {
		batch.finish(err)
		return
	}
	if session == nil {
		batch.finish(storage.ErrNotFound)
		return
	}
	if err := a.waitInboxIdle(runCtx, session.ID); err != nil {
		batch.finish(err)
		return
	}
	runCtx = withInboundTurnInput(runCtx, merged)
	batch.finish(a.handleSessionInput(runCtx, session, merged.Text))
}

// inboxSession prefers the scope's current Session so a compaction that ran
// during the previous turn is followed by queued messages. It falls back to
// the exact queued Session when there is no current Session (for example after
// the current pointer was reset).
func (a *Agent) inboxSession(ctx context.Context, sessionID string) (*storage.Session, error) {
	scope := a.scope(ctx)
	current, err := a.sessions.Current(ctx, scope)
	if err == nil && current != nil {
		return current, nil
	}
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	if a.store == nil {
		return nil, storage.ErrNotFound
	}
	return a.store.Sessions().Get(ctx, sessionID)
}

func (a *Agent) waitInboxIdle(ctx context.Context, sessionID string) error {
	delay := 10 * time.Millisecond
	for {
		if a.turns == nil || a.turns.Snapshot(sessionID).Phase == turn.PhaseIdle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

func (a *Agent) useInboundInbox(ctx context.Context) bool {
	if a == nil {
		return false
	}
	scope := a.scope(ctx)
	if scope.Shared {
		return true
	}
	return a.groupMergeWindow(ctx) > 0
}

func (a *Agent) submitInbound(ctx context.Context, sessionID, text string) error {
	input := a.turnInputForMessage(ctx, text)
	window := a.groupMergeWindow(ctx)
	if window > 0 {
		if inboundReplyMessageID(ctx) != "" {
			window = 0
		}
		if msg, ok := platform.MessageContextFrom(ctx); ok {
			if strings.TrimSpace(msg.ResumeSessionID) != "" || strings.TrimSpace(msg.ForkFromMessageID) != "" {
				window = 0
			}
		}
	}
	return a.inboxSubmit(ctx, sessionID, input, window, a.requestFairKey(ctx), a.requestScopeKey(ctx), inboundMessageWorkKey(ctx))
}

func inboundMessageWorkKey(ctx context.Context) string {
	msg, ok := platform.MessageContextFrom(ctx)
	if !ok {
		return ""
	}
	return messageWorkKey(msg.Platform, msg.ScopeID, msg.PlatformMessageID)
}

func (a *Agent) inboxPendingForFairKey(fairKey string) int {
	fairKey = strings.TrimSpace(fairKey)
	if a == nil || fairKey == "" {
		return 0
	}
	inbox := a.ensureInbox()
	if inbox == nil {
		return 0
	}
	inbox.mu.Lock()
	queues := make([]*inboxQueue, 0, len(inbox.queues))
	for _, queue := range inbox.queues {
		queues = append(queues, queue)
	}
	inbox.mu.Unlock()

	total := 0
	for _, queue := range queues {
		queue.mu.Lock()
		for _, batch := range queue.batches {
			if batch == nil || batch.fairKey != fairKey {
				continue
			}
			total += len(batch.items)
		}
		queue.mu.Unlock()
	}
	return total
}

func (a *Agent) cancelInboxSession(sessionID string) int {
	sessionID = strings.TrimSpace(sessionID)
	if a == nil || sessionID == "" {
		return 0
	}
	return a.cancelInboxQueues(func(_ string, batch *inboxBatch) bool {
		return true
	}, sessionID)
}

func (a *Agent) cancelInboxFairKey(fairKey string) int {
	fairKey = strings.TrimSpace(fairKey)
	if a == nil || fairKey == "" {
		return 0
	}
	return a.cancelInboxQueues(func(_ string, batch *inboxBatch) bool {
		return batch.fairKey == fairKey
	}, "")
}

func (a *Agent) cancelInboxScope(scopeKey string) int {
	scopeKey = strings.TrimSpace(scopeKey)
	if a == nil || scopeKey == "" {
		return 0
	}
	return a.cancelInboxQueues(func(_ string, batch *inboxBatch) bool {
		return batch.scopeKey == scopeKey
	}, "")
}

func (a *Agent) cancelInboxMessage(messageKey string) int {
	messageKey = strings.TrimSpace(messageKey)
	if a == nil || messageKey == "" {
		return 0
	}
	return a.cancelInboxQueues(func(_ string, batch *inboxBatch) bool {
		return inboxBatchHasMessage(batch, messageKey)
	}, "")
}

func inboxBatchHasMessage(batch *inboxBatch, messageKey string) bool {
	if batch == nil {
		return false
	}
	for _, item := range batch.items {
		if item.messageKey == messageKey {
			return true
		}
	}
	return false
}

func (a *Agent) cancelInboxQueues(match func(sessionID string, batch *inboxBatch) bool, onlySessionID string) int {
	inbox := a.ensureInbox()
	if a == nil || inbox == nil {
		return 0
	}
	inbox.mu.Lock()
	queues := make(map[string]*inboxQueue, len(inbox.queues))
	for key, queue := range inbox.queues {
		queues[key] = queue
	}
	inbox.mu.Unlock()

	canceled := 0
	for sessionID, queue := range queues {
		if onlySessionID != "" && sessionID != onlySessionID {
			continue
		}
		queue.mu.Lock()
		kept := queue.batches[:0]
		for _, batch := range queue.batches {
			if batch == nil || batch.sealed || !match(sessionID, batch) {
				kept = append(kept, batch)
				continue
			}
			batch.finish(context.Canceled)
			canceled++
		}
		queue.batches = kept
		queue.mu.Unlock()
	}
	return canceled
}
