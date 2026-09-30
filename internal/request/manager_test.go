package request

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagerStartListAndDone(t *testing.T) {
	m := NewManager(time.Minute)
	req, _, done, err := m.Start(context.Background(), StartRequest{SessionID: "s1", Kind: KindLLM, Label: "chat"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	requests := m.List()
	if len(requests) != 1 || requests[0].ID != req.ID {
		t.Fatalf("List = %#v, want request %s", requests, req.ID)
	}
	if requests[0].Deadline == nil {
		t.Fatal("expected deadline from default timeout")
	}

	done()
	done()
	if got := len(m.List()); got != 0 {
		t.Fatalf("active requests = %d, want 0", got)
	}
}

func TestManagerCancel(t *testing.T) {
	m := NewManager(time.Minute)
	req, ctx, _, err := m.Start(context.Background(), StartRequest{SessionID: "s1", Kind: KindLLM})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, ok := m.Get(req.ID)
	if !ok || got.ID != req.ID {
		t.Fatalf("Get = %#v, %v; want request %s", got, ok, req.ID)
	}

	if !m.Cancel(req.ID) {
		t.Fatal("Cancel returned false")
	}
	assertCanceled(t, ctx)
	if got := len(m.List()); got != 0 {
		t.Fatalf("active requests = %d, want 0", got)
	}
	if m.Cancel(req.ID) {
		t.Fatal("Cancel returned true for missing request")
	}
}

func TestManagerCancelSession(t *testing.T) {
	m := NewManager(time.Minute)
	parent, ctx1, _, _ := m.Start(context.Background(), StartRequest{SessionID: "s1", Kind: KindTurn})
	child, ctx2, _, _ := m.Start(ctx1, StartRequest{ParentID: parent.ID, SessionID: "s1", Kind: KindTool})
	_, ctx3, _, _ := m.Start(context.Background(), StartRequest{SessionID: "s2", Kind: KindLLM})
	if child.ParentID != parent.ID {
		t.Fatalf("child parent = %q, want %q", child.ParentID, parent.ID)
	}

	if got := m.CancelSession("s1"); got != 2 {
		t.Fatalf("CancelSession = %d, want 2", got)
	}
	assertCanceled(t, ctx1)
	assertCanceled(t, ctx2)
	select {
	case <-ctx3.Done():
		t.Fatal("s2 request was canceled")
	default:
	}
	if got := len(m.ListBySession("s2")); got != 1 {
		t.Fatalf("s2 active requests = %d, want 1", got)
	}
}

func TestManagerCancelAll(t *testing.T) {
	m := NewManager(time.Minute)
	_, ctx1, _, _ := m.Start(context.Background(), StartRequest{SessionID: "s1", Kind: KindLLM})
	_, ctx2, _, _ := m.Start(context.Background(), StartRequest{SessionID: "s2", Kind: KindTool})

	if got := m.CancelAll(); got != 2 {
		t.Fatalf("CancelAll = %d, want 2", got)
	}
	assertCanceled(t, ctx1)
	assertCanceled(t, ctx2)
	if got := len(m.List()); got != 0 {
		t.Fatalf("active requests = %d, want 0", got)
	}
}

func TestManagerTimeoutCleansRequest(t *testing.T) {
	m := NewManager(10 * time.Millisecond)
	_, ctx, _, err := m.Start(context.Background(), StartRequest{SessionID: "s1", Kind: KindLLM})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	assertCanceled(t, ctx)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(m.List()) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for request cleanup")
}

func TestManagerConcurrencyLimit(t *testing.T) {
	m := NewManagerWithLimits(time.Minute, Limits{KindTool: 1})
	_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTool, Label: "shell"})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, _, _, err := m.Start(context.Background(), StartRequest{Kind: KindTool, Label: "shell"}); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("second Start error = %v, want ErrConcurrencyLimit", err)
	}
	done()
	if _, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTool, Label: "shell"}); err != nil {
		t.Fatalf("Start after done: %v", err)
	} else {
		done()
	}
}

func TestManagerSnapshotAndStage(t *testing.T) {
	m := NewManager(time.Minute)
	turn, _, _, err := m.Start(context.Background(), StartRequest{SessionID: "s1", Kind: KindTurn, Label: "chat", Stage: "preparing"})
	if err != nil {
		t.Fatalf("Start turn: %v", err)
	}
	_, _, _, err = m.Start(context.Background(), StartRequest{SessionID: "s1", Kind: KindTool, Label: "shell", Stage: "running"})
	if err != nil {
		t.Fatalf("Start tool: %v", err)
	}
	if !m.SetStage(turn.ID, "llm") {
		t.Fatal("SetStage returned false")
	}
	if !m.Touch(turn.ID) {
		t.Fatal("Touch returned false")
	}
	snapshot := m.Snapshot()
	if len(snapshot.Active) != 2 || snapshot.CountByKind[KindTurn] != 1 || snapshot.CountByKind[KindTool] != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.OldestStartedAt == nil || snapshot.OldestAge < 0 {
		t.Fatalf("oldest = %#v", snapshot)
	}
	for _, req := range snapshot.Active {
		if req.ID == turn.ID && req.Stage != "llm" {
			t.Fatalf("turn stage = %q, want llm", req.Stage)
		}
	}
}

func TestManagerQueueWaitAndGrant(t *testing.T) {
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTool: 1}, QueueConfig{
		MaxQueue:    1,
		WaitTimeout: time.Second,
		WaitKinds:   map[Kind]bool{KindTool: true},
	})
	_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTool, Label: "first"})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	type result struct {
		ctx  context.Context
		done func()
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		_, ctx, done, err := m.Start(context.Background(), StartRequest{Kind: KindTool, Label: "second"})
		ch <- result{ctx: ctx, done: done, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		waiting := len(m.waiters[KindTool])
		m.mu.Unlock()
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second request did not enter queue")
		}
		time.Sleep(time.Millisecond)
	}
	done()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("second Start: %v", got.err)
		}
		got.done()
	case <-time.After(time.Second):
		t.Fatal("queued request was not granted")
	}
}

func TestManagerQueueFull(t *testing.T) {
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTool: 1}, QueueConfig{
		MaxQueue:    1,
		WaitTimeout: time.Second,
		WaitKinds:   map[Kind]bool{KindTool: true},
	})
	_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTool})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	waitingDone := make(chan struct{})
	go func() {
		_, _, done2, err := m.Start(context.Background(), StartRequest{Kind: KindTool})
		if err == nil {
			done2()
		}
		close(waitingDone)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		waiting := len(m.waiters[KindTool])
		m.mu.Unlock()
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second request did not enter queue")
		}
		time.Sleep(time.Millisecond)
	}
	if _, _, _, err := m.Start(context.Background(), StartRequest{Kind: KindTool}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third Start error = %v, want ErrQueueFull", err)
	}
	done()
	<-waitingDone
}

func TestManagerQueueTimeout(t *testing.T) {
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTool: 1}, QueueConfig{
		MaxQueue:    1,
		WaitTimeout: 10 * time.Millisecond,
		WaitKinds:   map[Kind]bool{KindTool: true},
	})
	_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTool})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, _, _, err := m.Start(context.Background(), StartRequest{Kind: KindTool}); !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("queued Start error = %v, want ErrQueueTimeout", err)
	}
	done()
}

func TestManagerQueueContextCancel(t *testing.T) {
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTool: 1}, QueueConfig{
		MaxQueue:    1,
		WaitTimeout: time.Minute,
		WaitKinds:   map[Kind]bool{KindTool: true},
	})
	_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTool})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := m.Start(ctx, StartRequest{Kind: KindTool}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled queue Start error = %v, want context.Canceled", err)
	}
	done()
}

func assertCanceled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled")
	}
}
