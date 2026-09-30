package concurrency

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterAcquireQueueAndRelease(t *testing.T) {
	l := New(Config{Max: 1, QueueSize: 1, WaitTimeout: time.Second})
	release1, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	type result struct {
		release func()
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		release, err := l.Acquire(context.Background())
		ch <- result{release: release, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		_, waiting := l.Stats()
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second Acquire did not enter queue")
		}
		time.Sleep(time.Millisecond)
	}
	release1()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("second Acquire: %v", got.err)
		}
		got.release()
	case <-time.After(time.Second):
		t.Fatal("queued Acquire was not granted")
	}
}

func TestLimiterQueueFullAndTimeout(t *testing.T) {
	l := New(Config{Max: 1, QueueSize: 1, WaitTimeout: 10 * time.Millisecond})
	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if _, err := l.Acquire(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("second Acquire error = %v, want ErrTimeout", err)
	}
	if _, err := l.Acquire(context.Background()); !errors.Is(err, ErrTimeout) {
		// The first timed-out waiter was removed, so the queue is available again.
		t.Fatalf("third Acquire error = %v, want ErrTimeout", err)
	}
	release()
}

func TestLimiterCancelAndDisabled(t *testing.T) {
	disabled := New(Config{Max: 0})
	release, err := disabled.Acquire(context.Background())
	if err != nil {
		t.Fatalf("disabled Acquire: %v", err)
	}
	release()

	l := New(Config{Max: 1, QueueSize: 1, WaitTimeout: time.Minute})
	release, err = l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Acquire error = %v", err)
	}
	release()
}
