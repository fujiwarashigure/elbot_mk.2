package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"elbot/internal/llm"
)

// fakeLLM is a scripted llm.LLM. It records the number of ChatStream calls so
// tests can assert cache/coalescing behaviour without any network.
type fakeLLM struct {
	mu      sync.Mutex
	calls   int
	stream  []llm.StreamChunk
	err     error
	panicOn bool
	release chan struct{}
	started chan struct{}
	lastReq llm.ChatRequest
}

func (f *fakeLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	f.mu.Lock()
	f.calls++
	f.lastReq = req
	release := f.release
	started := f.started
	stream := append([]llm.StreamChunk(nil), f.stream...)
	err := f.err
	panicOn := f.panicOn
	f.mu.Unlock()

	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if panicOn {
		panic("fake llm panic")
	}
	if err != nil {
		return nil, err
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ch := make(chan llm.StreamChunk, len(stream))
	for _, chunk := range stream {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (f *fakeLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func (f *fakeLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeLLM) lastRequest() llm.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

func testImage(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode test image: %v", err)
	}
	return buffer.Bytes()
}

func testRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		MediaID:  "media:" + strings.Repeat("a", 64),
		Data:     testImage(t, 8, 8),
		MIMEType: "image/png",
		Prompt:   ImagePrompt("general", "zh"),
	}
}

func newTestService(t *testing.T, client llm.LLM, counters *Counters, mutate ...func(*Options)) *Service {
	t.Helper()
	opts := Options{
		Client:        client,
		Provider:      "test",
		Model:         "vision-1",
		MaxTokens:     128,
		MaxEdge:       256,
		MaxImageBytes: 1 << 20,
		Timeout:       5 * time.Second,
		Metrics:       counters,
	}
	for _, apply := range mutate {
		apply(&opts)
	}
	return New(opts)
}

func TestServiceDownscalesAndReencodesLargeImage(t *testing.T) {
	client := &fakeLLM{stream: []llm.StreamChunk{{DeltaContent: "prompt"}}}
	service := newTestService(t, client, NewCounters(), func(opts *Options) {
		opts.MaxEdge = 64
	})
	req := testRequest(t)
	req.Data = testImage(t, 200, 100)
	if _, err := service.Describe(context.Background(), req); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	request := client.lastRequest()
	if len(request.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(request.Messages))
	}
	var imageSegment llm.MessageSegment
	for _, segment := range request.Messages[1].Segments {
		if segment.Type == llm.SegmentImage {
			imageSegment = segment
			break
		}
	}
	if imageSegment.Type != llm.SegmentImage {
		t.Fatalf("no image segment in %#v", request.Messages[1].Segments)
	}
	if imageSegment.MIMEType != "image/jpeg" || !strings.HasPrefix(imageSegment.URL, "data:image/jpeg;base64,") {
		t.Fatalf("downscaled segment = mime %q url %.40q", imageSegment.MIMEType, imageSegment.URL)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(imageSegment.URL, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatalf("decode data url: %v", err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decode downscaled image: %v", err)
	}
	if config.Width > 64 || config.Height > 64 {
		t.Fatalf("downscaled bounds = %dx%d, want the long edge capped at 64", config.Width, config.Height)
	}
}

func TestServiceCachesSuccessfulDescription(t *testing.T) {
	client := &fakeLLM{stream: []llm.StreamChunk{{DeltaContent: "a cat"}, {FinishReason: "stop"}}}
	counters := NewCounters()
	service := newTestService(t, client, counters)
	req := testRequest(t)

	first, err := service.Describe(context.Background(), req)
	if err != nil {
		t.Fatalf("first Describe: %v", err)
	}
	if first.Text != "a cat" || first.Cached {
		t.Fatalf("first result = %+v", first)
	}
	second, err := service.Describe(context.Background(), req)
	if err != nil {
		t.Fatalf("second Describe: %v", err)
	}
	if !second.Cached || second.Text != "a cat" {
		t.Fatalf("second result = %+v", second)
	}
	if got := client.callCount(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
	snapshot := counters.Snapshot()
	if snapshot.Cache["miss"] != 1 || snapshot.Cache["hit"] != 1 {
		t.Fatalf("cache counters = %#v", snapshot.Cache)
	}
}

func TestServiceFingerprintSeparatesPromptAndModel(t *testing.T) {
	client := &fakeLLM{stream: []llm.StreamChunk{{DeltaContent: "x"}}}
	service := newTestService(t, client, NewCounters())
	req := testRequest(t)

	baseKey, err := service.cacheKey(req)
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	sameKey, err := service.cacheKey(req)
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	if baseKey != sameKey {
		t.Fatal("identical requests must produce the same cache key")
	}

	otherPrompt := req
	otherPrompt.Prompt = ChatDescription("zh")
	promptKey, err := service.cacheKey(otherPrompt)
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	if promptKey == baseKey {
		t.Fatal("a different prompt must change the cache key")
	}

	otherModel := newTestService(t, client, NewCounters(), func(opts *Options) { opts.Model = "vision-2" })
	modelKey, err := otherModel.cacheKey(req)
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	if modelKey == baseKey {
		t.Fatal("a different model must change the cache key")
	}

	otherMax := newTestService(t, client, NewCounters(), func(opts *Options) { opts.MaxTokens = 256 })
	maxKey, err := otherMax.cacheKey(req)
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	if maxKey == baseKey {
		t.Fatal("a different max_tokens must change the cache key")
	}
}

func TestServiceCoalescesConcurrentIdenticalRequests(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	client := &fakeLLM{
		stream:  []llm.StreamChunk{{DeltaContent: "shared"}},
		release: release,
		started: started,
	}
	counters := NewCounters()
	service := newTestService(t, client, counters)
	req := testRequest(t)

	type outcome struct {
		result Result
		err    error
	}
	firstCh := make(chan outcome, 1)
	go func() {
		result, err := service.Describe(context.Background(), req)
		firstCh <- outcome{result: result, err: err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("leader never reached the upstream")
	}

	// The second caller arrives while the leader is still blocked upstream and
	// must join the same job instead of starting another.
	secondCh := make(chan outcome, 1)
	go func() {
		result, err := service.Describe(context.Background(), req)
		secondCh <- outcome{result: result, err: err}
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)

	for name, ch := range map[string]chan outcome{"first": firstCh, "second": secondCh} {
		select {
		case got := <-ch:
			if got.err != nil || got.result.Text != "shared" {
				t.Fatalf("%s result = %+v, err = %v", name, got.result, got.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s caller did not finish", name)
		}
	}
	if got := client.callCount(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
	if coalesced := counters.Snapshot().Coalesced; coalesced == 0 {
		t.Fatal("expected at least one coalesced caller")
	}
}

func TestServiceDoesNotNegativeCacheTransientFailure(t *testing.T) {
	client := &fakeLLM{err: &llm.APIError{StatusCode: 429, Message: "rate limited"}}
	service := newTestService(t, client, NewCounters())
	req := testRequest(t)

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := service.Describe(context.Background(), req); err == nil {
			t.Fatalf("attempt %d: expected an error", attempt)
		}
	}
	if got := client.callCount(); got != 2 {
		t.Fatalf("transient failure was cached: calls = %d, want 2", got)
	}
}

func TestServiceNegativeCachesDeterministicFailure(t *testing.T) {
	client := &fakeLLM{err: &llm.APIError{StatusCode: 404, Code: "model_not_found", Message: "missing model"}}
	counters := NewCounters()
	service := newTestService(t, client, counters)
	req := testRequest(t)

	if _, err := service.Describe(context.Background(), req); err == nil {
		t.Fatal("first call: expected an error")
	}
	_, err := service.Describe(context.Background(), req)
	if err == nil {
		t.Fatal("second call: expected the cached error")
	}
	apiErr, ok := llm.AsAPIError(err)
	if !ok || apiErr.Code != "model_not_found" {
		t.Fatalf("negative cache returned %v, want the original APIError", err)
	}
	if got := client.callCount(); got != 1 {
		t.Fatalf("deterministic failure was not cached: calls = %d, want 1", got)
	}
	if hit := counters.Snapshot().Cache["negative_hit"]; hit != 1 {
		t.Fatalf("negative_hit = %d, want 1", hit)
	}
}

func TestServiceDoesNotCacheEmptyOrTruncatedResults(t *testing.T) {
	cases := []struct {
		name   string
		stream []llm.StreamChunk
	}{
		{"empty", nil},
		{"truncated", []llm.StreamChunk{{DeltaContent: "partial"}, {FinishReason: "length"}}},
	}
	for _, testCase := range cases {
		client := &fakeLLM{stream: testCase.stream}
		service := newTestService(t, client, NewCounters())
		req := testRequest(t)
		if _, err := service.Describe(context.Background(), req); err == nil {
			t.Fatalf("%s: expected an error", testCase.name)
		}
		if _, err := service.Describe(context.Background(), req); err == nil {
			t.Fatalf("%s: expected the retry to fail too", testCase.name)
		}
		if got := client.callCount(); got != 2 {
			t.Fatalf("%s: bad result was cached: calls = %d, want 2", testCase.name, got)
		}
	}
}

func TestServiceEnforcesOutputCap(t *testing.T) {
	client := &fakeLLM{stream: []llm.StreamChunk{{DeltaContent: strings.Repeat("x", 64)}}}
	service := newTestService(t, client, NewCounters(), func(opts *Options) { opts.MaxOutputBytes = 16 })
	if _, err := service.Describe(context.Background(), testRequest(t)); err == nil {
		t.Fatal("expected the output cap to fail the call")
	}
}

func TestServiceRecoversFromPanic(t *testing.T) {
	client := &fakeLLM{panicOn: true}
	service := newTestService(t, client, NewCounters())
	req := testRequest(t)

	if _, err := service.Describe(context.Background(), req); err == nil {
		t.Fatal("expected the panic to be reported as an error")
	}
	// The panicking job must be cleared from in-flight so a later call retries.
	if _, err := service.Describe(context.Background(), req); err == nil {
		t.Fatal("expected the retry to fail too")
	}
	if got := client.callCount(); got != 2 {
		t.Fatalf("panic leaked an in-flight entry: calls = %d, want 2", got)
	}
}

func TestServiceSurfacesPreprocessFailure(t *testing.T) {
	client := &fakeLLM{}
	// Undecodable payloads pass through when they fit the byte budget, so force
	// an over-budget payload to exercise the preprocessing failure path.
	service := newTestService(t, client, NewCounters(), func(opts *Options) { opts.MaxImageBytes = 4 })
	req := testRequest(t)
	req.Data = []byte("not an image")

	if _, err := service.Describe(context.Background(), req); err == nil {
		t.Fatal("expected an error for an oversized undecodable payload")
	}
	if got := client.callCount(); got != 0 {
		t.Fatalf("upstream should not be called for bad input: calls = %d", got)
	}
}

func TestServiceRequiresModelAndMediaID(t *testing.T) {
	service := New(Options{})
	if _, err := service.Describe(context.Background(), testRequest(t)); err == nil {
		t.Fatal("expected an error without a client")
	}
	client := &fakeLLM{}
	service = newTestService(t, client, NewCounters())
	if _, err := service.Describe(context.Background(), Request{Data: testImage(t, 4, 4), Prompt: ImagePrompt("general", "zh")}); err == nil {
		t.Fatal("expected an error without a media id")
	}
}

// blockingVisionLLM blocks inside ChatStream until its context is canceled,
// which lets tests observe who is allowed to cancel the shared job.
type blockingVisionLLM struct {
	started chan struct{}
	done    chan struct{}
}

func (b *blockingVisionLLM) ChatStream(ctx context.Context, _ llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	close(b.started)
	<-ctx.Done()
	close(b.done)
	return nil, ctx.Err()
}

func (b *blockingVisionLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestServiceLeaderCancelDoesNotCancelSharedJob(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	client := &fakeLLM{
		stream:  []llm.StreamChunk{{DeltaContent: "shared"}},
		release: release,
		started: started,
	}
	service := newTestService(t, client, NewCounters())
	req := testRequest(t)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderCh := make(chan error, 1)
	go func() {
		_, err := service.Describe(leaderCtx, req)
		leaderCh <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("leader never reached the upstream")
	}

	waiterCh := make(chan Result, 1)
	waiterErr := make(chan error, 1)
	go func() {
		result, err := service.Describe(context.Background(), req)
		waiterCh <- result
		waiterErr <- err
	}()
	// Give the waiter time to join the in-flight call before the leader leaves.
	time.Sleep(20 * time.Millisecond)

	cancelLeader()
	if err := <-leaderCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v, want context.Canceled", err)
	}
	close(release)

	if err := <-waiterErr; err != nil {
		t.Fatalf("waiter error = %v", err)
	}
	if result := <-waiterCh; result.Text != "shared" {
		t.Fatalf("waiter result = %+v", result)
	}
	if got := client.callCount(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestServiceBaseContextCancelStopsSharedJob(t *testing.T) {
	base, cancelBase := context.WithCancel(context.Background())
	client := &blockingVisionLLM{started: make(chan struct{}), done: make(chan struct{})}
	service := newTestService(t, client, NewCounters(), func(opts *Options) {
		opts.BaseContext = base
		opts.Timeout = time.Minute
		opts.SharedTimeout = time.Minute
	})

	resultCh := make(chan error, 1)
	go func() {
		_, err := service.Describe(context.Background(), testRequest(t))
		resultCh <- err
	}()
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never started")
	}

	cancelBase()
	select {
	case <-client.done:
	case <-time.After(2 * time.Second):
		t.Fatal("service shutdown did not cancel the shared job")
	}
	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatal("expected an error after shutdown")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Describe did not return after shutdown")
	}
}

func TestServiceSharedJobStopsAtSharedTimeout(t *testing.T) {
	client := &blockingVisionLLM{started: make(chan struct{}), done: make(chan struct{})}
	service := newTestService(t, client, NewCounters(), func(opts *Options) {
		opts.Timeout = time.Minute
		opts.SharedTimeout = 50 * time.Millisecond
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, _ = service.Describe(ctx, testRequest(t)) }()
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never started")
	}

	// Every caller leaves, yet the detached job must still stop on its own
	// bounded timeout instead of running forever.
	cancel()
	select {
	case <-client.done:
	case <-time.After(2 * time.Second):
		t.Fatal("shared job outlived its shared timeout")
	}
}

func TestServiceSharedJobHonorsCallerDeadlineAfterCallersLeave(t *testing.T) {
	client := &blockingVisionLLM{started: make(chan struct{}), done: make(chan struct{})}
	service := newTestService(t, client, NewCounters(), func(opts *Options) {
		opts.Timeout = time.Minute
		opts.SharedTimeout = time.Minute
	})

	// The caller's own deadline is much shorter than the shared timeout. Once
	// it fires every caller is gone, yet the detached job must not keep the
	// upstream request alive until the shared timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	go func() { _, _ = service.Describe(ctx, testRequest(t)) }()
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never started")
	}
	select {
	case <-client.done:
	case <-time.After(2 * time.Second):
		t.Fatal("shared job ignored the caller deadline after its callers left")
	}
}

// infiniteStreamLLM keeps producing chunks until its context is canceled, so a
// consumer that stops reading must cancel the context to release the producer.
type infiniteStreamLLM struct {
	canceled chan struct{}
}

func (s *infiniteStreamLLM) ChatStream(ctx context.Context, _ llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	go func() {
		defer close(s.canceled)
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case ch <- llm.StreamChunk{DeltaContent: "chunk"}:
			}
		}
	}()
	return ch, nil
}

func (s *infiniteStreamLLM) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestServiceOutputCapStopsUpstreamStream(t *testing.T) {
	client := &infiniteStreamLLM{canceled: make(chan struct{})}
	service := newTestService(t, client, NewCounters(), func(opts *Options) {
		opts.MaxOutputBytes = 8
		opts.Timeout = 10 * time.Second
	})
	if _, err := service.Describe(context.Background(), testRequest(t)); err == nil {
		t.Fatal("expected the output cap to fail the call")
	}
	select {
	case <-client.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("output cap did not stop the upstream stream")
	}
}

func TestServiceLargeResultIsReturnedInFullAndNotCached(t *testing.T) {
	// Bigger than the 16 KiB cache-entry budget but smaller than the 64 KiB
	// output cap: the caller must get the full text, and a later call must not
	// serve a shorter cached copy.
	payload := strings.Repeat("好", 7*1024)
	client := &fakeLLM{stream: []llm.StreamChunk{{DeltaContent: payload}, {FinishReason: "stop"}}}
	service := newTestService(t, client, NewCounters())
	req := testRequest(t)

	first, err := service.Describe(context.Background(), req)
	if err != nil {
		t.Fatalf("first Describe: %v", err)
	}
	if first.Text != payload {
		t.Fatalf("first result was truncated: got %d bytes, want %d", len(first.Text), len(payload))
	}
	second, err := service.Describe(context.Background(), req)
	if err != nil {
		t.Fatalf("second Describe: %v", err)
	}
	if second.Cached {
		t.Fatal("an over-budget result must not be cached")
	}
	if second.Text != payload {
		t.Fatalf("second result differs from the first: got %d bytes", len(second.Text))
	}
	if got := client.callCount(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 (result must not be cached)", got)
	}
}
