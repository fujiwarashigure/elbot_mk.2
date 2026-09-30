package request

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	Timeout   time.Duration
}

// ErrConcurrencyLimit is returned when a kind has reached its configured limit
// and queueing is not allowed for that kind.
var ErrConcurrencyLimit = errors.New("request concurrency limit reached")

// ErrQueueFull is returned when the waiting queue is full.
var ErrQueueFull = errors.New("request queue full")

// ErrQueueTimeout is returned when a request waited too long for a slot.
var ErrQueueTimeout = errors.New("request queue wait timeout")

// Limits caps active requests by kind. A non-positive value means unlimited.
type Limits map[Kind]int

// QueueConfig controls bounded waiting for saturated request kinds.
type QueueConfig struct {
	MaxQueue    int
	WaitTimeout time.Duration
	WaitKinds   map[Kind]bool
}

type Manager struct {
	mu             sync.Mutex
	defaultTimeout time.Duration
	limits         Limits
	queue          QueueConfig
	active         map[string]*activeRequest
	pending        map[Kind]int
	waiters        map[Kind][]*waiter
}

type waiter struct {
	ready   chan struct{}
	granted bool
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

	if err := m.acquire(parent, start.Kind); err != nil {
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

func (m *Manager) acquire(ctx context.Context, kind Kind) error {
	if kind == "" {
		kind = KindLLM
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.canRunLocked(kind) {
		m.pending[kind]++
		m.mu.Unlock()
		return nil
	}
	if !m.queue.WaitKinds[kind] || m.queue.MaxQueue <= 0 {
		m.mu.Unlock()
		return fmt.Errorf("%w: kind=%s limit=%d", ErrConcurrencyLimit, kind, m.limits[kind])
	}
	if len(m.waiters[kind]) >= m.queue.MaxQueue {
		m.mu.Unlock()
		return fmt.Errorf("%w: kind=%s queue=%d", ErrQueueFull, kind, m.queue.MaxQueue)
	}
	w := &waiter{ready: make(chan struct{})}
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
		return nil
	case <-ctx.Done():
		waitErr = ctx.Err()
	case <-timeout:
		waitErr = ErrQueueTimeout
	}

	m.mu.Lock()
	if w.granted {
		m.mu.Unlock()
		return nil
	}
	m.removeWaiterLocked(kind, w)
	m.mu.Unlock()
	if waitErr == nil {
		waitErr = ErrQueueTimeout
	}
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

func (m *Manager) grantWaitersLocked(kind Kind) {
	for len(m.waiters[kind]) > 0 && m.canRunLocked(kind) {
		w := m.waiters[kind][0]
		m.waiters[kind] = m.waiters[kind][1:]
		w.granted = true
		m.pending[kind]++
		close(w.ready)
	}
}

// Snapshot is a point-in-time view of active requests.
type Snapshot struct {
	Active          []Request        `json:"active"`
	CountByKind     map[Kind]int     `json:"count_by_kind,omitempty"`
	OldestStartedAt *time.Time       `json:"oldest_started_at,omitempty"`
	OldestAge       time.Duration    `json:"oldest_age"`
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
		Active:      make([]Request, 0, len(m.active)),
		CountByKind: map[Kind]int{},
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
