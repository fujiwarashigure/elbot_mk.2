package breaker

import (
	"testing"
	"time"
)

func TestBreakerOpenCooldownAndHalfOpenRecovery(t *testing.T) {
	now := time.Unix(0, 0)
	b := New(Config{FailureThreshold: 2, OpenCooldown: time.Second, HalfOpenMax: 1})
	b.now = func() time.Time { return now }

	if !b.Allow() {
		t.Fatal("closed breaker should allow")
	}
	b.Failure()
	if !b.Allow() {
		t.Fatal("one failure should not open")
	}
	b.Failure()
	if b.Allow() {
		t.Fatal("breaker should be open")
	}
	now = now.Add(time.Second)
	if !b.Allow() {
		t.Fatal("breaker should permit one half-open probe")
	}
	if b.Allow() {
		t.Fatal("breaker should allow only one half-open probe")
	}
	b.Success()
	if !b.Allow() {
		t.Fatal("successful probe should close breaker")
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	now := time.Unix(0, 0)
	b := New(Config{FailureThreshold: 1, OpenCooldown: time.Second})
	b.now = func() time.Time { return now }
	b.Failure()
	if b.Allow() {
		t.Fatal("breaker should be open")
	}
	now = now.Add(time.Second)
	if !b.Allow() {
		t.Fatal("half-open probe should be allowed")
	}
	b.Failure()
	if b.Allow() {
		t.Fatal("failed probe should reopen breaker")
	}
}

func TestBreakerDisabled(t *testing.T) {
	b := New(Config{FailureThreshold: 0})
	for i := 0; i < 10; i++ {
		if !b.Allow() {
			t.Fatal("disabled breaker should allow")
		}
		b.Failure()
	}
	if b.State() != "disabled" {
		t.Fatalf("state = %q", b.State())
	}
}
