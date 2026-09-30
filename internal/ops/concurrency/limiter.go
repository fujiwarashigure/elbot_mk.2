// Package concurrency provides a small bounded concurrency limiter with an
// optional waiting queue.
package concurrency

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrFull is returned when the waiting queue is full.
var ErrFull = errors.New("concurrency queue full")

// ErrTimeout is returned when waiting for a slot times out.
var ErrTimeout = errors.New("concurrency wait timeout")

// Config controls a limiter. Max <= 0 disables limiting.
type Config struct {
	Max         int
	QueueSize   int
	WaitTimeout time.Duration
}

type waiter struct {
	ready   chan struct{}
	granted bool
}

// Limiter bounds concurrent work and optionally queues waiting callers.
type Limiter struct {
	mu      sync.Mutex
	cfg     Config
	active  int
	waiters []*waiter
}

// New creates a limiter.
func New(cfg Config) *Limiter {
	return &Limiter{cfg: cfg}
}

// Acquire waits for a slot and returns a release function.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	if l == nil || l.cfg.Max <= 0 {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	if l.active < l.cfg.Max {
		l.active++
		l.mu.Unlock()
		return l.releaseFunc(), nil
	}
	if l.cfg.QueueSize <= 0 || len(l.waiters) >= l.cfg.QueueSize {
		l.mu.Unlock()
		return nil, ErrFull
	}
	w := &waiter{ready: make(chan struct{})}
	l.waiters = append(l.waiters, w)
	l.mu.Unlock()

	var timer *time.Timer
	var timeout <-chan time.Time
	if l.cfg.WaitTimeout > 0 {
		timer = time.NewTimer(l.cfg.WaitTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	var waitErr error
	select {
	case <-w.ready:
		return l.releaseFunc(), nil
	case <-ctx.Done():
		waitErr = ctx.Err()
	case <-timeout:
		waitErr = ErrTimeout
	}

	l.mu.Lock()
	if w.granted {
		l.mu.Unlock()
		return l.releaseFunc(), nil
	}
	l.removeWaiterLocked(w)
	l.mu.Unlock()
	if waitErr == nil {
		waitErr = ErrTimeout
	}
	return nil, waitErr
}

func (l *Limiter) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(l.release)
	}
}

func (l *Limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.waiters) > 0 {
		w := l.waiters[0]
		l.waiters = l.waiters[1:]
		w.granted = true
		close(w.ready)
		return
	}
	if l.active > 0 {
		l.active--
	}
}

func (l *Limiter) removeWaiterLocked(target *waiter) {
	for i, w := range l.waiters {
		if w == target {
			l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
			return
		}
	}
}

// Stats returns active and waiting counts.
func (l *Limiter) Stats() (active, waiting int) {
	if l == nil {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active, len(l.waiters)
}
