package request

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"elbot/internal/storage"
)

type Kind string

const (
	KindTurn     Kind = "turn"
	KindLLM      Kind = "llm"
	KindTool     Kind = "tool"
	KindHook     Kind = "hook"
	KindCompress Kind = "compress"
	KindSubAgent Kind = "sub_agent"
)

type Request struct {
	ID         string
	ParentID   string
	SessionID  string
	Kind       Kind
	Label      string
	Stage      string
	FairKey    string
	ScopeKey   string
	StartedAt  time.Time
	ProgressAt time.Time
	Deadline   *time.Time
}

type StartRequest struct {
	ParentID  string
	SessionID string
	Kind      Kind
	Label     string
	Stage     string
	FairKey   string
	// ScopeKey identifies the trusted group/private bucket for per-scope
	// queue caps. It is intentionally separate from FairKey (which includes
	// the actor) so one user cannot fill the whole group queue.
	ScopeKey string
	Timeout  time.Duration
}

// ErrConcurrencyLimit is returned when a kind has reached its configured limit
// and queueing is not allowed for that kind.
var ErrConcurrencyLimit = errors.New("request concurrency limit reached")

// ErrQueueFull is returned when the waiting queue is full.
var ErrQueueFull = errors.New("request queue full")

// ErrQueueTimeout is returned when a request waited too long for a slot.
var ErrQueueTimeout = errors.New("request queue wait timeout")

// ErrQueueCancelled is returned when a queued request is removed by a
// targeted cancellation such as a member leave/mute event.
var ErrQueueCancelled = errors.New("request queue entry cancelled")

// Limits caps active requests by kind. A non-positive value means unlimited.
type Limits map[Kind]int

// QueueConfig controls bounded waiting for saturated request kinds.
type QueueConfig struct {
	MaxQueue           int
	MaxQueuePerFairKey int
	MaxQueuePerScope   int
	WaitTimeout        time.Duration
	WaitKinds          map[Kind]bool
}

type Manager struct {
	mu             sync.Mutex
	defaultTimeout time.Duration
	limits         Limits
	queue          QueueConfig
	active         map[string]*activeRequest
	pending        map[Kind]int
	waiters        map[Kind][]*waiter
	lastFairKey    map[Kind]string
	timeouts       atomic.Int64

	queueBusyRejected      atomic.Int64
	queueFullRejected      atomic.Int64
	queueTimeoutRejected   atomic.Int64
	queueCancelledRejected atomic.Int64
	queueAdmitted          atomic.Int64
	queueWaitNanos         atomic.Int64
}

type waiter struct {
	ready     chan struct{}
	granted   bool
	cancelled bool
	fairKey   string
	scopeKey  string
	queuedAt  time.Time
}

type activeRequest struct {
	request Request
	cancel  context.CancelFunc
}

func NewManager(defaultTimeout time.Duration) *Manager {
	return NewManagerWithLimitsAndQueue(defaultTimeout, nil, QueueConfig{})
}

func NewManagerWithLimits(defaultTimeout time.Duration, limits Limits) *Manager {
	return NewManagerWithLimitsAndQueue(defaultTimeout, limits, QueueConfig{})
}

func NewManagerWithLimitsAndQueue(defaultTimeout time.Duration, limits Limits, queue QueueConfig) *Manager {
	if queue.WaitKinds == nil {
		queue.WaitKinds = map[Kind]bool{}
	}
	return &Manager{
		defaultTimeout: defaultTimeout,
		limits:         limits,
		queue:          queue,
		active:         map[string]*activeRequest{},
		pending:        map[Kind]int{},
		waiters:        map[Kind][]*waiter{},
		lastFairKey:    map[Kind]string{},
	}
}

func (m *Manager) Start(parent context.Context, start StartRequest) (Request, context.Context, func(), error) {
	if parent == nil {
		parent = context.Background()
	}

	timeout := start.Timeout
	if timeout == 0 {
		timeout = m.defaultTimeout
	}

	if err := m.acquire(parent, start.Kind, start.FairKey, start.ScopeKey); err != nil {
		return Request{}, parent, func() {}, err
	}

	startedAt := time.Now()
	var (
		ctx      context.Context
		cancel   context.CancelFunc
		deadline *time.Time
	)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
		d := startedAt.Add(timeout)
		deadline = &d
	} else {
		ctx, cancel = context.WithCancel(parent)
	}

	req := Request{
		ID:         storage.NewID(),
		ParentID:   start.ParentID,
		SessionID:  start.SessionID,
		Kind:       start.Kind,
		Label:      start.Label,
		Stage:      start.Stage,
		FairKey:    strings.TrimSpace(start.FairKey),
		ScopeKey:   strings.TrimSpace(start.ScopeKey),
		StartedAt:  startedAt,
		ProgressAt: startedAt,
		Deadline:   deadline,
	}
	if req.Kind == "" {
		req.Kind = KindLLM
	}
	if req.Stage == "" {
		req.Stage = string(req.Kind)
	}

	m.mu.Lock()
	if m.pending[req.Kind] > 0 {
		m.pending[req.Kind]--
	}
	m.active[req.ID] = &activeRequest{request: req, cancel: cancel}
	m.mu.Unlock()

	done := sync.OnceFunc(func() {
		m.finish(req.ID, true)
	})
	go func() {
		<-ctx.Done()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			m.timeouts.Add(1)
		}
		m.finish(req.ID, false)
	}()

	return req, ctx, done, nil
}

func (m *Manager) List() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Request, 0, len(m.active))
	for _, active := range m.active {
		out = append(out, active.request)
	}
	return out
}

func (m *Manager) ListBySession(sessionID string) []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Request{}
	for _, active := range m.active {
		if active.request.SessionID == sessionID {
			out = append(out, active.request)
		}
	}
	return out
}

func (m *Manager) Get(id string) (Request, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	active, ok := m.active[id]
	if !ok {
		return Request{}, false
	}
	return active.request, true
}

func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	active, ok := m.active[id]
	if ok {
		delete(m.active, id)
		m.grantWaitersLocked(active.request.Kind)
	}
	m.mu.Unlock()
	if ok {
		active.cancel()
	}
	return ok
}

// SessionIDs returns active request IDs for one session. The Agent uses this
// to mark turns terminal before CancelSession so late provider/tool results do
// not become new output.
func (m *Manager) SessionIDs(sessionID string) []string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, 4)
	for id, active := range m.active {
		if active.request.SessionID == sessionID {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *Manager) CancelSession(sessionID string) int {
	m.mu.Lock()
	active := []*activeRequest{}
	kinds := map[Kind]bool{}
	for id, req := range m.active {
		if req.request.SessionID == sessionID {
			delete(m.active, id)
			active = append(active, req)
			kinds[req.request.Kind] = true
		}
	}
	for kind := range kinds {
		m.grantWaitersLocked(kind)
	}
	m.mu.Unlock()
	for _, req := range active {
		req.cancel()
	}
	return len(active)
}

// FairKeyIDs returns the active request IDs for one fairness bucket. It is
// used by the Agent to mark those turns terminal before CancelFairKey so a
// provider/tool that ignores context cancellation cannot emit a late message.
func (m *Manager) FairKeyIDs(fairKey string) []string {
	fairKey = strings.TrimSpace(fairKey)
	if fairKey == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, 4)
	for id, active := range m.active {
		if active.request.FairKey == fairKey {
			ids = append(ids, id)
		}
	}
	return ids
}

// CancelFairKey cancels every active request whose fairness bucket matches
// fairKey. It is used for member-scoped lifecycle events (leave/kick/mute) so
// one member's cancellation never stops another member's work.
func (m *Manager) CancelFairKey(fairKey string) int {
	fairKey = strings.TrimSpace(fairKey)
	if fairKey == "" {
		return 0
	}
	m.mu.Lock()
	active := []*activeRequest{}
	kinds := map[Kind]bool{}
	for id, req := range m.active {
		if req.request.FairKey != fairKey {
			continue
		}
		delete(m.active, id)
		active = append(active, req)
		kinds[req.request.Kind] = true
	}
	cancelledWaiters := 0
	for kind, list := range m.waiters {
		kept := list[:0]
		for _, w := range list {
			if w.fairKey == fairKey {
				w.cancelled = true
				close(w.ready)
				cancelledWaiters++
				continue
			}
			kept = append(kept, w)
		}
		m.waiters[kind] = kept
	}
	for kind := range kinds {
		m.grantWaitersLocked(kind)
	}
	m.mu.Unlock()
	for _, req := range active {
		req.cancel()
	}
	return len(active) + cancelledWaiters
}

func (m *Manager) CancelAll() int {
	m.mu.Lock()
	active := make([]*activeRequest, 0, len(m.active))
	kinds := map[Kind]bool{}
	for id, req := range m.active {
		delete(m.active, id)
		active = append(active, req)
		kinds[req.request.Kind] = true
	}
	for kind := range kinds {
		m.grantWaitersLocked(kind)
	}
	m.mu.Unlock()
	for _, req := range active {
		req.cancel()
	}
	return len(active)
}

func (m *Manager) acquire(ctx context.Context, kind Kind, fairKey, scopeKey string) error {
	if kind == "" {
		kind = KindLLM
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fairKey = strings.TrimSpace(fairKey)
	scopeKey = strings.TrimSpace(scopeKey)
	m.mu.Lock()
	if m.canRunLocked(kind) {
		m.pending[kind]++
		m.lastFairKey[kind] = fairKey
		m.mu.Unlock()
		return nil
	}
	if !m.queue.WaitKinds[kind] || m.queue.MaxQueue <= 0 {
		m.mu.Unlock()
		m.queueBusyRejected.Add(1)
		return fmt.Errorf("%w: kind=%s limit=%d", ErrConcurrencyLimit, kind, m.limits[kind])
	}
	if len(m.waiters[kind]) >= m.queue.MaxQueue {
		m.mu.Unlock()
		m.queueFullRejected.Add(1)
		return fmt.Errorf("%w: kind=%s queue=%d", ErrQueueFull, kind, m.queue.MaxQueue)
	}
	if m.queue.MaxQueuePerFairKey > 0 && fairKey != "" && m.queuedFairKeyLocked(kind, fairKey) >= m.queue.MaxQueuePerFairKey {
		m.mu.Unlock()
		m.queueFullRejected.Add(1)
		return fmt.Errorf("%w: kind=%s fair_key=%s queue=%d", ErrQueueFull, kind, fairKey, m.queue.MaxQueuePerFairKey)
	}
	if m.queue.MaxQueuePerScope > 0 && scopeKey != "" && m.queuedScopeLocked(kind, scopeKey) >= m.queue.MaxQueuePerScope {
		m.mu.Unlock()
		m.queueFullRejected.Add(1)
		return fmt.Errorf("%w: kind=%s scope=%s queue=%d", ErrQueueFull, kind, scopeKey, m.queue.MaxQueuePerScope)
	}
	w := &waiter{ready: make(chan struct{}), fairKey: fairKey, scopeKey: scopeKey, queuedAt: time.Now()}
	m.waiters[kind] = append(m.waiters[kind], w)
	m.mu.Unlock()

	var timer *time.Timer
	var timeout <-chan time.Time
	if m.queue.WaitTimeout > 0 {
		timer = time.NewTimer(m.queue.WaitTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	var waitErr error
	select {
	case <-w.ready:
		m.mu.Lock()
		cancelled := w.cancelled
		granted := w.granted
		m.mu.Unlock()
		switch {
		case granted:
			m.queueAdmitted.Add(1)
			m.queueWaitNanos.Add(time.Since(w.queuedAt).Nanoseconds())
			return nil
		case cancelled:
			m.queueCancelledRejected.Add(1)
			return ErrQueueCancelled
		default:
			m.queueTimeoutRejected.Add(1)
			return ErrQueueTimeout
		}
	case <-ctx.Done():
		waitErr = ctx.Err()
	case <-timeout:
		waitErr = ErrQueueTimeout
	}

	m.mu.Lock()
	if w.granted {
		m.mu.Unlock()
		m.queueAdmitted.Add(1)
		m.queueWaitNanos.Add(time.Since(w.queuedAt).Nanoseconds())
		return nil
	}
	if w.cancelled {
		m.removeWaiterLocked(kind, w)
		m.mu.Unlock()
		m.queueCancelledRejected.Add(1)
		return ErrQueueCancelled
	}
	m.removeWaiterLocked(kind, w)
	m.mu.Unlock()
	if waitErr == nil || errors.Is(waitErr, ErrQueueTimeout) {
		m.queueTimeoutRejected.Add(1)
		return ErrQueueTimeout
	}
	m.queueCancelledRejected.Add(1)
	return waitErr
}

func (m *Manager) canRunLocked(kind Kind) bool {
	limit := m.limits[kind]
	if limit <= 0 {
		return true
	}
	count := m.pending[kind]
	for _, active := range m.active {
		if active.request.Kind == kind {
			count++
		}
	}
	return count < limit
}

func (m *Manager) removeWaiterLocked(kind Kind, target *waiter) {
	list := m.waiters[kind]
	for i, w := range list {
		if w == target {
			m.waiters[kind] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

func (m *Manager) queuedFairKeyLocked(kind Kind, fairKey string) int {
	count := 0
	for _, w := range m.waiters[kind] {
		if w.fairKey == fairKey {
			count++
		}
	}
	return count
}

func (m *Manager) queuedScopeLocked(kind Kind, scopeKey string) int {
	count := 0
	for _, w := range m.waiters[kind] {
		if w.scopeKey == scopeKey {
			count++
		}
	}
	return count
}

func (m *Manager) grantWaitersLocked(kind Kind) {
	for len(m.waiters[kind]) > 0 && m.canRunLocked(kind) {
		index := m.nextFairWaiterIndexLocked(kind)
		w := m.waiters[kind][index]
		m.waiters[kind] = append(m.waiters[kind][:index], m.waiters[kind][index+1:]...)
		w.granted = true
		m.pending[kind]++
		m.lastFairKey[kind] = w.fairKey
		close(w.ready)
	}
}

// nextFairWaiterIndexLocked chooses the queued request whose fair key has the
// fewest active requests. Ties prefer a key that was not admitted most
// recently, then the oldest waiter, so a continuously arriving stream of new
// keys cannot indefinitely starve an older queued key. An empty fair key is
// treated as one shared bucket and preserves the old FIFO behavior.
func (m *Manager) nextFairWaiterIndexLocked(kind Kind) int {
	if len(m.waiters[kind]) == 0 {
		return 0
	}
	activeByKey := map[string]int{}
	for _, active := range m.active {
		if active.request.Kind != kind {
			continue
		}
		activeByKey[active.request.FairKey]++
	}
	lastKey := m.lastFairKey[kind]
	bestIndex := 0
	bestCount := -1
	bestIsLast := false
	var bestQueuedAt time.Time
	for index, waiter := range m.waiters[kind] {
		count := activeByKey[waiter.fairKey]
		isLast := waiter.fairKey == lastKey
		better := false
		switch {
		case bestCount < 0 || count < bestCount:
			better = true
		case count == bestCount && bestIsLast && !isLast:
			better = true
		case count == bestCount && bestIsLast == isLast && (bestQueuedAt.IsZero() || waiter.queuedAt.Before(bestQueuedAt)):
			better = true
		}
		if better {
			bestIndex = index
			bestCount = count
			bestIsLast = isLast
			bestQueuedAt = waiter.queuedAt
		}
	}
	return bestIndex
}

// Snapshot is a point-in-time view of active requests.
type Snapshot struct {
	Active             []Request     `json:"active"`
	CountByKind        map[Kind]int  `json:"count_by_kind,omitempty"`
	PendingByKind      map[Kind]int  `json:"pending_by_kind,omitempty"`
	QueuedByKind       map[Kind]int  `json:"queued_by_kind,omitempty"`
	Timeouts           int64         `json:"timeouts"`
	OldestStartedAt    *time.Time    `json:"oldest_started_at,omitempty"`
	OldestAge          time.Duration `json:"oldest_age"`
	QueueBusyRejected  int64         `json:"queue_busy_rejected"`
	QueueFullRejected  int64         `json:"queue_full_rejected"`
	QueueTimeoutReject int64         `json:"queue_timeout_rejected"`
	QueueCancelReject  int64         `json:"queue_cancel_rejected"`
	QueueAdmitted      int64         `json:"queue_admitted"`
	QueueAverageWaitMS int64         `json:"queue_average_wait_ms"`
	OldestQueuedWait   time.Duration `json:"oldest_queued_wait"`
}

// SetFairKey updates the fairness bucket for an active request. It is useful
// for callers that create a request before the platform scope is resolved.
func (m *Manager) SetFairKey(id, fairKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	active, ok := m.active[id]
	if !ok {
		return false
	}
	active.request.FairKey = strings.TrimSpace(fairKey)
	return true
}

// Touch updates a request's progress timestamp.
func (m *Manager) Touch(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	active, ok := m.active[id]
	if !ok {
		return false
	}
	active.request.ProgressAt = time.Now()
	return true
}

// SetStage updates a request's stage and progress timestamp.
func (m *Manager) SetStage(id, stage string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	active, ok := m.active[id]
	if !ok {
		return false
	}
	stage = strings.TrimSpace(stage)
	if stage == "" {
		return false
	}
	active.request.Stage = stage
	active.request.ProgressAt = time.Now()
	return true
}

// Snapshot returns active requests and aggregate counts.
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := Snapshot{
		Active:             make([]Request, 0, len(m.active)),
		CountByKind:        map[Kind]int{},
		PendingByKind:      map[Kind]int{},
		QueuedByKind:       map[Kind]int{},
		Timeouts:           m.timeouts.Load(),
		QueueBusyRejected:  m.queueBusyRejected.Load(),
		QueueFullRejected:  m.queueFullRejected.Load(),
		QueueTimeoutReject: m.queueTimeoutRejected.Load(),
		QueueCancelReject:  m.queueCancelledRejected.Load(),
		QueueAdmitted:      m.queueAdmitted.Load(),
	}
	for kind, count := range m.pending {
		if count > 0 {
			out.PendingByKind[kind] = count
		}
	}
	for kind, waiters := range m.waiters {
		if len(waiters) > 0 {
			out.QueuedByKind[kind] = len(waiters)
			for _, w := range waiters {
				if out.OldestQueuedWait == 0 || time.Since(w.queuedAt) > out.OldestQueuedWait {
					out.OldestQueuedWait = time.Since(w.queuedAt)
				}
			}
		}
	}
	if admitted := out.QueueAdmitted; admitted > 0 {
		out.QueueAverageWaitMS = m.queueWaitNanos.Load() / admitted / int64(time.Millisecond)
	}
	now := time.Now()
	var oldest time.Time
	for _, active := range m.active {
		request := active.request
		out.Active = append(out.Active, request)
		out.CountByKind[request.Kind]++
		if oldest.IsZero() || request.StartedAt.Before(oldest) {
			oldest = request.StartedAt
		}
	}
	if !oldest.IsZero() {
		oldestCopy := oldest
		out.OldestStartedAt = &oldestCopy
		out.OldestAge = now.Sub(oldest)
	}
	return out
}

func (m *Manager) finish(id string, cancel bool) {
	m.mu.Lock()
	active, ok := m.active[id]
	if ok {
		delete(m.active, id)
		m.grantWaitersLocked(active.request.Kind)
	}
	m.mu.Unlock()
	if ok && cancel {
		active.cancel()
	}
}
