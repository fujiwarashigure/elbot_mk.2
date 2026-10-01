package health

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures a health State.
type Options struct {
	Version   string
	LiveStale time.Duration
	Now       func() time.Time
}

// State tracks liveness, readiness, platform connectivity and model health.
type State struct {
	version   string
	liveStale time.Duration
	now       func() time.Time
	startedAt time.Time

	lastHeartbeat  atomic.Int64
	ready          atomic.Bool
	shuttingDown   atomic.Bool
	messagesOK     atomic.Int64
	messagesFailed atomic.Int64
	lastMessageAt  atomic.Int64

	mu                sync.RWMutex
	expectedPlatforms map[string]struct{}
	platforms         map[string]PlatformStatus
	models            map[string]ModelStatus
	lastRestartReason string
}

// ModelStatus describes one provider's current health.
type ModelStatus struct {
	Provider      string     `json:"provider"`
	Status        string     `json:"status"`
	Circuit       string     `json:"circuit,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
}

// PlatformStatus describes one platform's connection state.
type PlatformStatus struct {
	Name               string     `json:"name"`
	Connected          bool       `json:"connected"`
	ConnectCount       int        `json:"connect_count"`
	LastConnectedAt    *time.Time `json:"last_connected_at,omitempty"`
	LastDisconnectedAt *time.Time `json:"last_disconnected_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
}

// Snapshot is a point-in-time view of the health state.
type Snapshot struct {
	Status            string            `json:"status"`
	Live              bool              `json:"live"`
	Ready             bool              `json:"ready"`
	Degraded          bool              `json:"degraded"`
	SchedulerLive     bool              `json:"scheduler_live"`
	SchedulerKnown    bool              `json:"scheduler_known"`
	Version           string            `json:"version,omitempty"`
	StartedAt         time.Time         `json:"started_at"`
	UptimeSeconds     int64             `json:"uptime_seconds"`
	LastHeartbeat     time.Time         `json:"last_heartbeat,omitempty"`
	MessagesOK        int64             `json:"messages_ok"`
	MessagesFailed    int64             `json:"messages_failed"`
	LastMessageAt     *time.Time        `json:"last_message_at,omitempty"`
	Checks            map[string]string `json:"checks,omitempty"`
	Models            []ModelStatus     `json:"models,omitempty"`
	Platforms         []PlatformStatus  `json:"platforms,omitempty"`
	LastRestartReason string            `json:"last_restart_reason,omitempty"`
}

// NewState creates a health state. When LiveStale is not positive it defaults to 90s.
func NewState(opts Options) *State {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	stale := opts.LiveStale
	if stale <= 0 {
		stale = 90 * time.Second
	}
	nowTime := now()
	return &State{
		version:           opts.Version,
		liveStale:         stale,
		now:               now,
		startedAt:         nowTime,
		expectedPlatforms: map[string]struct{}{},
		platforms:         map[string]PlatformStatus{},
		models:            map[string]ModelStatus{},
	}
}

func (s *State) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// Beat records that a critical scheduling loop is still progressing.
func (s *State) Beat() {
	if s == nil {
		return
	}
	s.lastHeartbeat.Store(s.clock().UnixNano())
}

// SetReady marks the process as initialized and ready to accept work.
func (s *State) SetReady(ready bool) {
	if s == nil {
		return
	}
	s.ready.Store(ready)
}

// SetShuttingDown marks the process as draining.
func (s *State) SetShuttingDown(shuttingDown bool) {
	if s == nil {
		return
	}
	s.shuttingDown.Store(shuttingDown)
}

// RecordMessageHandled records a successfully processed inbound message.
func (s *State) RecordMessageHandled() {
	if s == nil {
		return
	}
	s.messagesOK.Add(1)
	s.lastMessageAt.Store(s.clock().UnixNano())
}

// RecordMessageError records a failed inbound message.
func (s *State) RecordMessageError() {
	if s == nil {
		return
	}
	s.messagesFailed.Add(1)
}

// IsLive reports whether a scheduling heartbeat was observed recently.
func (s *State) IsLive() bool {
	if s == nil {
		return false
	}
	last := s.lastHeartbeat.Load()
	if last <= 0 {
		return false
	}
	return s.clock().Sub(time.Unix(0, last)) <= s.liveStale
}

// IsProcessLive reports whether the process itself is running. This is the
// semantic used by /live: a running HTTP server is enough; scheduler heartbeat,
// platform connectivity and model health are reported separately.
func (s *State) IsProcessLive() bool {
	if s == nil {
		return false
	}
	return !s.shuttingDown.Load()
}

// SchedulerKnown reports whether at least one scheduling heartbeat was seen.
func (s *State) SchedulerKnown() bool {
	if s == nil {
		return false
	}
	return s.lastHeartbeat.Load() > 0
}

// SchedulerLive reports whether the scheduling heartbeat is fresh.
func (s *State) SchedulerLive() bool {
	return s.IsLive()
}

// IsReady reports the process-level readiness flag.
func (s *State) IsReady() bool {
	if s == nil {
		return false
	}
	return s.ready.Load() && !s.shuttingDown.Load()
}

// ShuttingDown reports whether the process is draining.
func (s *State) ShuttingDown() bool {
	if s == nil {
		return true
	}
	return s.shuttingDown.Load()
}

// ExpectPlatform records a platform that is reported through /healthz and for
// which a disconnect should degrade the aggregate status.
func (s *State) ExpectPlatform(name string) {
	if s == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expectedPlatforms == nil {
		s.expectedPlatforms = map[string]struct{}{}
	}
	if s.platforms == nil {
		s.platforms = map[string]PlatformStatus{}
	}
	s.expectedPlatforms[name] = struct{}{}
	if _, ok := s.platforms[name]; !ok {
		s.platforms[name] = PlatformStatus{Name: name}
	}
}

// MarkPlatformConnected updates platform connectivity after a successful connect.
func (s *State) MarkPlatformConnected(name string) {
	if s == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.platforms == nil {
		s.platforms = map[string]PlatformStatus{}
	}
	status := s.platforms[name]
	status.Name = name
	status.Connected = true
	status.ConnectCount++
	status.LastConnectedAt = &now
	status.LastDisconnectedAt = nil
	status.LastError = ""
	s.platforms[name] = status
}

// MarkPlatformDisconnected marks a platform as disconnected.
func (s *State) MarkPlatformDisconnected(name string, err error) {
	if s == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.platforms == nil {
		s.platforms = map[string]PlatformStatus{}
	}
	status := s.platforms[name]
	status.Name = name
	status.Connected = false
	status.LastDisconnectedAt = &now
	if err != nil {
		status.LastError = RedactSecrets(err.Error())
	}
	s.platforms[name] = status
}

// RecordModelError marks a provider as degraded after an API error.
func (s *State) RecordModelError(provider string, err error) {
	if s == nil {
		return
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "unknown"
	}
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.models == nil {
		s.models = map[string]ModelStatus{}
	}
	status := s.models[provider]
	status.Provider = provider
	status.Status = "degraded"
	if err != nil {
		status.LastError = RedactSecrets(err.Error())
	}
	status.LastFailureAt = &now
	s.models[provider] = status
}

// SetLastRestartReason records the reason reported by the external watchdog or
// deployment agent for the most recent restart.
func (s *State) SetLastRestartReason(reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.lastRestartReason = RedactSecrets(reason)
	s.mu.Unlock()
}

// LastRestartReason returns the recorded restart reason.
func (s *State) LastRestartReason() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRestartReason
}

// SetModelCircuit records the current provider-level circuit breaker state.
func (s *State) SetModelCircuit(provider string, circuit string) {
	if s == nil {
		return
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "unknown"
	}
	circuit = strings.TrimSpace(circuit)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.models == nil {
		s.models = map[string]ModelStatus{}
	}
	status := s.models[provider]
	status.Provider = provider
	if circuit != "" {
		status.Circuit = circuit
	}
	s.models[provider] = status
}

// RecordModelSuccess marks a provider as healthy after a successful API call.
func (s *State) RecordModelSuccess(provider string) {
	if s == nil {
		return
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "unknown"
	}
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.models == nil {
		s.models = map[string]ModelStatus{}
	}
	status := s.models[provider]
	status.Provider = provider
	status.Status = "up"
	status.LastError = ""
	status.LastSuccessAt = &now
	s.models[provider] = status
}

// PlatformReadiness returns whether every expected platform has connected.
func (s *State) PlatformReadiness() (bool, map[string]string) {
	if s == nil {
		return true, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	checks := map[string]string{}
	ready := true
	names := make([]string, 0, len(s.expectedPlatforms))
	for name := range s.expectedPlatforms {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		status := s.platforms[name]
		if !status.Connected {
			ready = false
			checks["platform:"+name] = "not connected"
			continue
		}
		checks["platform:"+name] = "connected"
	}
	if len(checks) == 0 {
		checks["platforms"] = "none configured"
	}
	return ready, checks
}

// Snapshot returns a point-in-time health view. It does not execute external checks.
func (s *State) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{Status: "not_ready", Checks: map[string]string{"state": "nil"}}
	}
	live := s.IsProcessLive()
	schedulerKnown := s.SchedulerKnown()
	schedulerLive := s.SchedulerLive()
	ready := s.IsReady() && !(schedulerKnown && !schedulerLive)
	platformReady, platformChecks := s.PlatformReadiness()

	s.mu.RLock()
	models := make([]ModelStatus, 0, len(s.models))
	degraded := false
	for _, status := range s.models {
		if status.Status == "degraded" {
			degraded = true
		}
		models = append(models, status)
	}
	platforms := make([]PlatformStatus, 0, len(s.platforms))
	for _, status := range s.platforms {
		platforms = append(platforms, status)
	}
	s.mu.RUnlock()
	sort.Slice(models, func(i, j int) bool { return models[i].Provider < models[j].Provider })
	sort.Slice(platforms, func(i, j int) bool { return platforms[i].Name < platforms[j].Name })
	if !platformReady {
		degraded = true
	}
	if schedulerKnown && !schedulerLive {
		degraded = true
	}

	checks := map[string]string{}
	for name, value := range platformChecks {
		checks[name] = value
	}
	switch {
	case !schedulerKnown:
		checks["scheduler"] = "not started"
	case schedulerLive:
		checks["scheduler"] = "ok"
	default:
		checks["scheduler"] = "heartbeat stale"
	}

	status := "ok"
	switch {
	case !live:
		status = "not_live"
	case !ready:
		status = "not_ready"
	case degraded:
		status = "degraded"
	}
	now := s.clock()
	last := s.lastHeartbeat.Load()
	lastHeartbeat := time.Time{}
	if last > 0 {
		lastHeartbeat = time.Unix(0, last)
	}
	var lastMessage *time.Time
	if lastMessageNS := s.lastMessageAt.Load(); lastMessageNS > 0 {
		value := time.Unix(0, lastMessageNS)
		lastMessage = &value
	}
	return Snapshot{
		Status:            status,
		Live:              live,
		Ready:             ready,
		Degraded:          degraded,
		SchedulerLive:     schedulerLive,
		SchedulerKnown:    schedulerKnown,
		Version:           s.version,
		StartedAt:         s.startedAt,
		UptimeSeconds:     int64(now.Sub(s.startedAt).Seconds()),
		LastHeartbeat:     lastHeartbeat,
		MessagesOK:        s.messagesOK.Load(),
		MessagesFailed:    s.messagesFailed.Load(),
		LastMessageAt:     lastMessage,
		Checks:            checks,
		Models:            models,
		Platforms:         platforms,
		LastRestartReason: s.LastRestartReason(),
	}
}
