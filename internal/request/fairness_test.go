package request

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagerFairQueuePrefersIdleFairKey(t *testing.T) {
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTurn: 1}, QueueConfig{MaxQueue: 10, WaitTimeout: time.Second, WaitKinds: map[Kind]bool{KindTurn: true}})
	a1, _, a1Done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A"})
	if err != nil {
		t.Fatalf("start A1: %v", err)
	}
	bStarted := make(chan struct{})
	a2Started := make(chan struct{})
	go func() {
		_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A"})
		if err == nil {
			close(a2Started)
			done()
		}
	}()
	time.Sleep(10 * time.Millisecond)
	go func() {
		_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "B"})
		if err == nil {
			close(bStarted)
			done()
		}
	}()
	time.Sleep(10 * time.Millisecond)
	a1Done()
	select {
	case <-bStarted:
	case <-a2Started:
		t.Fatal("A2 was granted before idle B key")
	case <-time.After(time.Second):
		t.Fatal("B was not granted")
	}
	select {
	case <-a2Started:
	case <-time.After(time.Second):
		t.Fatal("A2 was not granted after B completed")
	}
	if a1.ID == "" {
		t.Fatal("missing request id")
	}
}

func TestCancelFairKeyCancelsOnlyMatchingBucket(t *testing.T) {
	m := NewManager(time.Minute)
	_, aCtx, aDone, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A"})
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	defer aDone()
	_, bCtx, bDone, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "B"})
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	defer bDone()
	if count := m.CancelFairKey("A"); count != 1 {
		t.Fatalf("CancelFairKey(A) = %d, want 1", count)
	}
	select {
	case <-aCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("A context was not canceled")
	}
	select {
	case <-bCtx.Done():
		t.Fatal("B context was canceled by CancelFairKey(A)")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancelFairKeyRemovesQueuedWaiters(t *testing.T) {
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTurn: 1}, QueueConfig{MaxQueue: 10, WaitTimeout: time.Second, WaitKinds: map[Kind]bool{KindTurn: true}})
	_, a1Ctx, a1Done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A"})
	if err != nil {
		t.Fatalf("start A1: %v", err)
	}
	defer a1Done()
	a2Err := make(chan error, 1)
	go func() {
		_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A"})
		if err == nil {
			done()
		}
		a2Err <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if count := m.CancelFairKey("A"); count != 2 {
		t.Fatalf("CancelFairKey(A) = %d, want 2 (active + queued)", count)
	}
	select {
	case <-a1Ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("active A1 context was not canceled")
	}
	select {
	case err := <-a2Err:
		if !errors.Is(err, ErrQueueCancelled) {
			t.Fatalf("queued A2 error = %v, want ErrQueueCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued A2 was not released")
	}
}

func TestQueuePerFairKeyAndScopeCaps(t *testing.T) {
	queue := QueueConfig{
		MaxQueue:           10,
		MaxQueuePerFairKey: 1,
		MaxQueuePerScope:   2,
		WaitTimeout:        time.Second,
		WaitKinds:          map[Kind]bool{KindTurn: true},
	}
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTurn: 1}, queue)
	_, _, a1Done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A", ScopeKey: "group:1"})
	if err != nil {
		t.Fatalf("start A1: %v", err)
	}
	defer a1Done()

	started := make(chan struct{})
	go func() {
		close(started)
		_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A", ScopeKey: "group:1"})
		if err == nil {
			done()
		}
		_ = err
	}()
	<-started
	deadline := time.Now().Add(time.Second)
	for m.Snapshot().QueuedByKind[KindTurn] == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if _, _, _, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A", ScopeKey: "group:1"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("second queued A error = %v, want ErrQueueFull", err)
	}
}

func TestQueuePerScopeCapAcrossFairKeys(t *testing.T) {
	queue := QueueConfig{
		MaxQueue:         10,
		MaxQueuePerScope: 1,
		WaitTimeout:      time.Second,
		WaitKinds:        map[Kind]bool{KindTurn: true},
	}
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTurn: 1}, queue)
	_, _, a1Done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A", ScopeKey: "group:1"})
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	defer a1Done()

	started := make(chan struct{})
	go func() {
		close(started)
		_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "B", ScopeKey: "group:1"})
		if err == nil {
			done()
		}
		_ = err
	}()
	<-started
	deadline := time.Now().Add(time.Second)
	for m.Snapshot().QueuedByKind[KindTurn] == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, _, _, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "C", ScopeKey: "group:1"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third scope waiter error = %v, want ErrQueueFull", err)
	}
}

func TestQueueSnapshotMetricsRecordRejectionsAndWaiters(t *testing.T) {
	queue := QueueConfig{
		MaxQueue:           10,
		MaxQueuePerFairKey: 1,
		WaitTimeout:        time.Second,
		WaitKinds:          map[Kind]bool{KindTurn: true},
	}
	m := NewManagerWithLimitsAndQueue(time.Minute, Limits{KindTurn: 1}, queue)
	_, _, done, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A"})
	if err != nil {
		t.Fatalf("start active: %v", err)
	}
	defer done()

	waiting := make(chan struct{})
	go func() {
		close(waiting)
		_, _, waiterDone, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A"})
		if err == nil {
			waiterDone()
		}
	}()
	<-waiting
	deadline := time.Now().Add(time.Second)
	for m.Snapshot().QueuedByKind[KindTurn] == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, _, _, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, FairKey: "A"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue full error = %v, want ErrQueueFull", err)
	}
	snapshot := m.Snapshot()
	if snapshot.QueueFullRejected < 1 {
		t.Fatalf("queue full metric = %d, want >=1", snapshot.QueueFullRejected)
	}
	if snapshot.QueuedByKind[KindTurn] != 1 {
		t.Fatalf("queued metric = %d, want 1", snapshot.QueuedByKind[KindTurn])
	}
}
func TestCancelScopeCancelsOnlyMatchingScope(t *testing.T) {
	m := NewManager(time.Minute)
	_, aCtx, aDone, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, ScopeKey: "qqonebot:group:9"})
	if err != nil {
		t.Fatalf("start group 9: %v", err)
	}
	defer aDone()
	_, bCtx, bDone, err := m.Start(context.Background(), StartRequest{Kind: KindTurn, ScopeKey: "qqonebot:group:10"})
	if err != nil {
		t.Fatalf("start group 10: %v", err)
	}
	defer bDone()
	if got := m.CancelScope("qqonebot:group:9"); got != 1 {
		t.Fatalf("CancelScope = %d, want 1", got)
	}
	select {
	case <-aCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("matching scope context was not canceled")
	}
	select {
	case <-bCtx.Done():
		t.Fatal("other scope was canceled")
	case <-time.After(50 * time.Millisecond):
	}
}
