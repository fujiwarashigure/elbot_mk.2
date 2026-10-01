// Package breaker provides a small provider-level circuit breaker.
package breaker

import (
	"errors"
	"sync"
	"time"
)

// ErrOpen is returned when a circuit is open and no request may pass.
var ErrOpen = errors.New("provider circuit breaker is open")

type state string

const (
	stateClosed   state = "closed"
	stateOpen     state = "open"
	stateHalfOpen state = "half_open"
)

// Config controls one breaker.
type Config struct {
	FailureThreshold int
	OpenCooldown     time.Duration
	HalfOpenMax      int
}

// Breaker is a concurrency-safe circuit breaker.
type Breaker struct {
	mu  sync.Mutex
	cfg Config
	now func() time.Time

	state            state
	failures         int
	openedAt         time.Time
	halfOpenInFlight int
}

// New creates a breaker. A non-positive FailureThreshold disables it.
func New(cfg Config) *Breaker {
	if cfg.OpenCooldown <= 0 {
		cfg.OpenCooldown = time.Minute
	}
	if cfg.HalfOpenMax <= 0 {
		cfg.HalfOpenMax = 1
	}
	return &Breaker{
		cfg:   cfg,
		now:   time.Now,
		state: stateClosed,
	}
}

// Enabled reports whether the breaker can reject requests.
func (b *Breaker) Enabled() bool {
	return b != nil && b.cfg.FailureThreshold > 0
}

// Allow reports whether a request may pass right now.
func (b *Breaker) Allow() bool {
	if !b.Enabled() {
		return true
	}
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case stateOpen:
		if now.Sub(b.openedAt) < b.cfg.OpenCooldown {
			return false
		}
		b.state = stateHalfOpen
		b.halfOpenInFlight = 1
		return true
	case stateHalfOpen:
		if b.halfOpenInFlight >= b.cfg.HalfOpenMax {
			return false
		}
		b.halfOpenInFlight++
		return true
	default:
		return true
	}
}

// Success records a successful provider request.
func (b *Breaker) Success() {
	if !b.Enabled() {
		return
	}
	b.mu.Lock()
	b.state = stateClosed
	b.failures = 0
	b.halfOpenInFlight = 0
	b.mu.Unlock()
}

// Failure records a provider failure.
func (b *Breaker) Failure() {
	if !b.Enabled() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case stateHalfOpen:
		b.openedAt = b.now()
		b.state = stateOpen
		b.halfOpenInFlight = 0
	case stateOpen:
		b.openedAt = b.now()
	default:
		b.failures++
		if b.failures >= b.cfg.FailureThreshold {
			b.openedAt = b.now()
			b.state = stateOpen
			b.halfOpenInFlight = 0
		}
	}
}

// State returns the current breaker state name.
func (b *Breaker) State() string {
	if !b.Enabled() {
		return "disabled"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.state)
}
