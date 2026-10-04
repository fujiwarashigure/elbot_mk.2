package agent

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"elbot/internal/llm"
	"elbot/internal/media"
)

type fakeVisionDescriber struct {
	mu    sync.Mutex
	calls int
	reply string
	err   error
}

func (f *fakeVisionDescriber) DescribeImage(context.Context, string, []byte, string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

func (f *fakeVisionDescriber) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func agentTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 32, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}

func newVisionTestAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	ctx := context.Background()
	store := newTestStore(t)
	root := t.TempDir()
	center := media.NewManager(store, root, &media.LocalBackend{Root: root})
	item, err := center.ImportBytes(ctx, agentTestPNG(t), media.Input{})
	if err != nil {
		t.Fatalf("import image: %v", err)
	}
	return &Agent{media: center}, item.ID
}

func TestDescribeVisionMessagesReplacesImages(t *testing.T) {
	agent, mediaID := newVisionTestAgent(t)
	agent.vision = &fakeVisionDescriber{reply: "一只白色的猫"}
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{
		{Type: llm.SegmentText, Text: "看图"},
		{Type: llm.SegmentImage, MediaID: mediaID, Name: "cat.png"},
	}}}

	out, ok := agent.describeVisionMessages(context.Background(), messages)
	if !ok {
		t.Fatal("expected images to be described")
	}
	if llm.MessagesHaveImageSegment(out) {
		t.Fatalf("described messages still carry images: %#v", out)
	}
	text := out[0].Segments[1].Text
	if !strings.Contains(text, "一只白色的猫") || !strings.Contains(text, "cat.png") {
		t.Fatalf("description text = %q", text)
	}
	if !strings.Contains(text, "不可信") {
		t.Fatalf("description must be marked as untrusted image content: %q", text)
	}
	if got := agent.vision.(*fakeVisionDescriber).callCount(); got != 1 {
		t.Fatalf("describer calls = %d, want 1", got)
	}
}

func TestDescribeVisionMessagesFallsBackWhenDescriberFails(t *testing.T) {
	agent, mediaID := newVisionTestAgent(t)
	agent.vision = &fakeVisionDescriber{err: errors.New("vision down")}
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{
		{Type: llm.SegmentImage, MediaID: mediaID, Name: "cat.png"},
	}}}

	if _, ok := agent.describeVisionMessages(context.Background(), messages); ok {
		t.Fatal("a failing describer must not be reported as success")
	}
	// visionFallbackMessages degrades to the plain text reference.
	fallback := agent.visionFallbackMessages(context.Background(), messages)
	if llm.MessagesHaveImageSegment(fallback) {
		t.Fatalf("fallback kept images: %#v", fallback)
	}
	if !strings.Contains(fallback[0].Segments[0].Text, "[图片 1") {
		t.Fatalf("fallback text = %q", fallback[0].Segments[0].Text)
	}
}

func TestDescribeVisionMessagesWithoutDescriberIsNoop(t *testing.T) {
	agent := &Agent{}
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{{Type: llm.SegmentImage, MediaID: "media:" + strings.Repeat("a", 64)}}}}
	if _, ok := agent.describeVisionMessages(context.Background(), messages); ok {
		t.Fatal("no describer must report false")
	}
}

func TestDescribeVisionMessagesRejectsUnresolvableImage(t *testing.T) {
	agent, _ := newVisionTestAgent(t)
	agent.vision = &fakeVisionDescriber{reply: "x"}
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{{Type: llm.SegmentImage, MediaID: "not-a-media-id"}}}}
	if _, ok := agent.describeVisionMessages(context.Background(), messages); ok {
		t.Fatal("an invalid media id must report false")
	}
	if got := agent.vision.(*fakeVisionDescriber).callCount(); got != 0 {
		t.Fatalf("describer calls = %d, want 0", got)
	}
}

func TestDescribeVisionMessagesPartialFailureDropsAllImages(t *testing.T) {
	agent, mediaID := newVisionTestAgent(t)
	agent.vision = &fakeVisionDescriber{reply: "ok"}
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{
		{Type: llm.SegmentImage, MediaID: mediaID, Name: "a.png"},
		{Type: llm.SegmentText, Text: "对比"},
		// The second image is not resolvable, so the whole batch must degrade to
		// text references instead of mixing a description with a live image.
		{Type: llm.SegmentImage, MediaID: "media:" + strings.Repeat("b", 64), Name: "b.png"},
	}}}

	if _, ok := agent.describeVisionMessages(context.Background(), messages); ok {
		t.Fatal("a partially described batch must not report success")
	}
	fallback := agent.visionFallbackMessages(context.Background(), messages)
	if llm.MessagesHaveImageSegment(fallback) {
		t.Fatalf("fallback kept image segments: %#v", fallback)
	}
	if content := llm.SegmentsContentText(fallback[0].Segments); !strings.Contains(content, "a.png") || !strings.Contains(content, "b.png") {
		t.Fatalf("fallback lost an image reference: %q", content)
	}
	if !llm.MessagesHaveImageSegment(messages) {
		t.Fatal("describeVisionMessages mutated the caller's messages")
	}
}

// probingVisionDescriber records how many descriptions run at the same time.
type probingVisionDescriber struct {
	mu         sync.Mutex
	current    int
	max        int
	calls      int
	saturateAt int
	release    chan struct{}
	ready      chan struct{}
	readyOnce  sync.Once
}

func newProbingVisionDescriber(saturateAt int) *probingVisionDescriber {
	return &probingVisionDescriber{saturateAt: saturateAt, release: make(chan struct{}), ready: make(chan struct{})}
}

func (p *probingVisionDescriber) DescribeImage(ctx context.Context, _ string, _ []byte, _ string) (string, error) {
	p.mu.Lock()
	p.calls++
	p.current++
	if p.current > p.max {
		p.max = p.current
	}
	if p.current >= p.saturateAt {
		p.readyOnce.Do(func() { close(p.ready) })
	}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.current--
		p.mu.Unlock()
	}()
	select {
	case <-p.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return "described", nil
}

func (p *probingVisionDescriber) stats() (max, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.max, p.calls
}

func TestDescribeVisionMessagesDescribesImagesInParallel(t *testing.T) {
	agent, mediaID := newVisionTestAgent(t)
	agent.visionParallelism = 2
	describer := newProbingVisionDescriber(2)
	agent.vision = describer
	segments := make([]llm.MessageSegment, 0, 4)
	for i := 0; i < 4; i++ {
		segments = append(segments, llm.MessageSegment{Type: llm.SegmentImage, MediaID: mediaID, Name: "cat.png"})
	}
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: segments}}

	type outcome struct {
		messages []llm.LLMMessage
		ok       bool
	}
	done := make(chan outcome, 1)
	go func() {
		out, ok := agent.describeVisionMessages(context.Background(), messages)
		done <- outcome{messages: out, ok: ok}
	}()
	select {
	case <-describer.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("images were never described in parallel")
	}
	if max, _ := describer.stats(); max != 2 {
		t.Fatalf("concurrent descriptions = %d, want the configured 2", max)
	}
	close(describer.release)
	select {
	case result := <-done:
		if !result.ok {
			t.Fatal("expected the batch to succeed")
		}
		if llm.MessagesHaveImageSegment(result.messages) {
			t.Fatal("described messages still carry images")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("describeVisionMessages did not finish")
	}
	if max, calls := describer.stats(); max != 2 || calls != 4 {
		t.Fatalf("max/calls = %d/%d, want 2/4", max, calls)
	}
}

func TestDescribeVisionMessagesDegradesAboveImageLimit(t *testing.T) {
	agent, mediaID := newVisionTestAgent(t)
	agent.visionMaxImages = 2
	describer := &fakeVisionDescriber{reply: "ok"}
	agent.vision = describer
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{
		{Type: llm.SegmentImage, MediaID: mediaID},
		{Type: llm.SegmentImage, MediaID: mediaID},
		{Type: llm.SegmentImage, MediaID: mediaID},
	}}}
	if _, ok := agent.describeVisionMessages(context.Background(), messages); ok {
		t.Fatal("a batch above the image limit must degrade to text references")
	}
	if got := describer.callCount(); got != 0 {
		t.Fatalf("describer calls = %d, want 0 (cap checked before any call)", got)
	}
}

// blockingVisionDescriber waits for its context, like a provider that never
// answers.
type blockingVisionDescriber struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockingVisionDescriber) DescribeImage(ctx context.Context, _ string, _ []byte, _ string) (string, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return "", ctx.Err()
}

func TestDescribeVisionMessagesHonorsBatchBudget(t *testing.T) {
	agent, mediaID := newVisionTestAgent(t)
	agent.visionBudget = 60 * time.Millisecond
	describer := &blockingVisionDescriber{started: make(chan struct{})}
	agent.vision = describer
	messages := []llm.LLMMessage{{Role: llm.RoleUser, Segments: []llm.MessageSegment{{Type: llm.SegmentImage, MediaID: mediaID}}}}

	start := time.Now()
	if _, ok := agent.describeVisionMessages(context.Background(), messages); ok {
		t.Fatal("a batch that never finishes must degrade to text references")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("batch budget was not honoured, took %s", elapsed)
	}
}
