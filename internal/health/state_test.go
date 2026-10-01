package health

import (
	"errors"
	"testing"
	"time"
)

func TestStateLiveness(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state := NewState(Options{LiveStale: 10 * time.Second, Now: func() time.Time { return now }})

	if state.IsLive() {
		t.Fatal("state should not be live before the first heartbeat")
	}
	state.Beat()
	if !state.IsLive() {
		t.Fatal("state should be live after a heartbeat")
	}
	now = now.Add(11 * time.Second)
	if state.IsLive() {
		t.Fatal("state should be stale after the live window")
	}
}

func TestStatePlatformReadiness(t *testing.T) {
	state := NewState(Options{})
	state.SetReady(true)
	if ready, _ := state.PlatformReadiness(); !ready {
		t.Fatal("platforms should be ready when none are expected")
	}
	state.ExpectPlatform("qqonebot")
	if ready, _ := state.PlatformReadiness(); ready {
		t.Fatal("platforms should not be ready before expected platform connects")
	}
	state.MarkPlatformConnected("qqonebot")
	if ready, checks := state.PlatformReadiness(); !ready || checks["platform:qqonebot"] != "connected" {
		t.Fatalf("platform readiness = %v, %#v", ready, checks)
	}
	state.MarkPlatformDisconnected("qqonebot", errors.New("closed"))
	if ready, checks := state.PlatformReadiness(); ready || checks["platform:qqonebot"] != "not connected" {
		t.Fatalf("platform readiness after disconnect = %v, %#v", ready, checks)
	}
}

func TestStateModelHealth(t *testing.T) {
	state := NewState(Options{})
	state.Beat()
	state.SetReady(true)
	state.RecordModelError("deepseek", errors.New("timeout"))
	snapshot := state.Snapshot()
	if snapshot.Status != "degraded" || !snapshot.Degraded {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(snapshot.Models) != 1 || snapshot.Models[0].Provider != "deepseek" || snapshot.Models[0].Status != "degraded" {
		t.Fatalf("models = %#v", snapshot.Models)
	}
	state.RecordModelSuccess("deepseek")
	snapshot = state.Snapshot()
	if snapshot.Status != "ok" || snapshot.Degraded {
		t.Fatalf("snapshot after recovery = %#v", snapshot)
	}
}
func TestStateProcessLiveIndependentFromHeartbeat(t *testing.T) {
	state := NewState(Options{LiveStale: time.Second})
	if !state.IsProcessLive() {
		t.Fatal("new state should be process-live before any heartbeat")
	}
	if state.SchedulerKnown() || state.SchedulerLive() {
		t.Fatal("scheduler should be unknown before the first heartbeat")
	}
	state.SetShuttingDown(true)
	if state.IsProcessLive() {
		t.Fatal("shutting down state must not report process-live")
	}
}

func TestStateSchedulerStaleDegradesButKeepsProcessLive(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state := NewState(Options{LiveStale: 10 * time.Second, Now: func() time.Time { return now }})
	state.Beat()
	state.SetReady(true)
	now = now.Add(11 * time.Second)

	snapshot := state.Snapshot()
	if !snapshot.Live || !snapshot.SchedulerKnown || snapshot.SchedulerLive {
		t.Fatalf("snapshot liveness fields = %#v", snapshot)
	}
	if !snapshot.Degraded || snapshot.Status != "not_ready" || snapshot.Ready {
		t.Fatalf("snapshot should be not_ready on stale scheduler, got %#v", snapshot)
	}
	if ready, _ := state.PlatformReadiness(); !ready {
		t.Fatal("platform readiness should not be affected by scheduler heartbeat")
	}
}

func TestStateRestartReason(t *testing.T) {
	state := NewState(Options{})
	if got := state.LastRestartReason(); got != "" {
		t.Fatalf("initial restart reason = %q", got)
	}
	state.SetLastRestartReason("  watchdog restart  ")
	if got := state.LastRestartReason(); got != "watchdog restart" {
		t.Fatalf("restart reason = %q", got)
	}
	snapshot := state.Snapshot()
	if snapshot.LastRestartReason != "watchdog restart" {
		t.Fatalf("snapshot restart reason = %q", snapshot.LastRestartReason)
	}
}
