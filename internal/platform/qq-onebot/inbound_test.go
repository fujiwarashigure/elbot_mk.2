package qqonebot

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestInboundDeduperSuppressesReplays(t *testing.T) {
	d := newInboundDeduper(time.Minute, 16)
	now := time.Unix(1000, 0)
	d.now = func() time.Time { return now }

	if state, duplicate := d.begin("scope|7"); duplicate || state != inboundDedupStateProcessing {
		t.Fatalf("first begin = (%q, %v)", state, duplicate)
	}
	if state, duplicate := d.begin("scope|7"); !duplicate || state != inboundDedupStateProcessing {
		t.Fatalf("in-flight duplicate = (%q, %v)", state, duplicate)
	}
	d.finish("scope|7", inboundDedupStateCompleted, "")
	if state, duplicate := d.begin("scope|7"); !duplicate || state != inboundDedupStateCompleted {
		t.Fatalf("completed duplicate = (%q, %v)", state, duplicate)
	}

	now = now.Add(2 * time.Minute)
	if _, duplicate := d.begin("scope|7"); duplicate {
		t.Fatal("expired entry should not suppress a replay")
	}
}

func TestInboundDeduperKeepsFailedTerminal(t *testing.T) {
	d := newInboundDeduper(time.Hour, 16)
	d.begin("scope|8")
	d.finish("scope|8", inboundDedupStateFailed, "boom")
	state, duplicate := d.begin("scope|8")
	if !duplicate || state != inboundDedupStateFailed {
		t.Fatalf("failed duplicate = (%q, %v)", state, duplicate)
	}
}

func TestInboundDeduperIsBounded(t *testing.T) {
	d := newInboundDeduper(time.Hour, 2)
	now := time.Unix(1000, 0)
	d.now = func() time.Time { return now }
	d.begin("a")
	now = now.Add(time.Second)
	d.begin("b")
	now = now.Add(time.Second)
	d.begin("c")

	d.mu.Lock()
	size := len(d.entries)
	d.mu.Unlock()
	if size > 2 {
		t.Fatalf("dedup entries = %d, want <= 2", size)
	}
}

type countingMessageHandler struct {
	texts chan string
}

func (h *countingMessageHandler) HandleMessage(_ context.Context, text string) error {
	h.texts <- text
	return nil
}

func TestDispatchMessageEventSuppressesReplayThroughWorkers(t *testing.T) {
	adapter := New(Config{Enabled: true}, nil, nil, nil)
	handler := &countingMessageHandler{texts: make(chan string, 16)}
	dispatcher := newEventDispatcher(adapter, context.Background(), handler, 1, 4, 1, 4)
	defer dispatcher.stop()

	event := Event{
		PostType:    "message",
		MessageType: "group",
		MessageID:   7,
		GroupID:     9,
		UserID:      1,
		SelfID:      1000,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"hello"}}]`),
	}
	for i := 0; i < 10; i++ {
		adapter.dispatchMessageEvent(dispatcher, event)
	}

	select {
	case got := <-handler.texts:
		if got != "hello" {
			t.Fatalf("handler text = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not called")
	}
	select {
	case got := <-handler.texts:
		t.Fatalf("duplicate replay reached handler: %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEventDispatcherNormalQueueIsNonBlockingAndBounded(t *testing.T) {
	d := &eventDispatcher{normal: make(chan outboundEventJob, 2)}
	if !d.enqueueNormal(outboundEventJob{}) {
		t.Fatal("first enqueue should succeed")
	}
	if !d.enqueueNormal(outboundEventJob{}) {
		t.Fatal("second enqueue should succeed")
	}
	if d.enqueueNormal(outboundEventJob{}) {
		t.Fatal("third enqueue should be rejected instead of blocking")
	}
}
