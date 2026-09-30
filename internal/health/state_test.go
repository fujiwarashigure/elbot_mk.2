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
