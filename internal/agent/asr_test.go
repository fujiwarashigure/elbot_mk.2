package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"elbot/internal/config"
	"elbot/internal/media"
	"elbot/internal/platform"
)

type fakeAudioTranscriber struct {
	mu       sync.Mutex
	calls    int
	reply    string
	err      error
	requests []AudioTranscriptionRequest
}

func (f *fakeAudioTranscriber) Transcribe(_ context.Context, req AudioTranscriptionRequest) (AudioTranscriptionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.requests = append(f.requests, req)
	if f.err != nil {
		return AudioTranscriptionResult{}, f.err
	}
	return AudioTranscriptionResult{Text: f.reply, Provider: "test", Model: "whisper-test"}, nil
}

func (f *fakeAudioTranscriber) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newASRTestAgent(t *testing.T, data ...[]byte) (*Agent, []string) {
	t.Helper()
	store := newTestStore(t)
	root := t.TempDir()
	center := media.NewManager(store, root, &media.LocalBackend{Root: root})
	ids := make([]string, 0, len(data))
	for _, body := range data {
		item, err := center.ImportBytes(context.Background(), body, media.Input{Name: "voice.amr", MIMEType: "audio/amr"})
		if err != nil {
			t.Fatalf("import voice: %v", err)
		}
		ids = append(ids, item.ID)
	}
	return &Agent{platform: &fakePlatform{}, media: center}, ids
}

func asrMessageContext(segments []platform.MessageSegment) context.Context {
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:  "cli",
		ScopeID:   "private:test",
		Segments:  segments,
		MatchText: "[语音]",
	})
	return ctx
}

func voiceSegment(mediaID string) platform.MessageSegment {
	return platform.MessageSegment{Type: platform.SegmentFile, Text: "语音", MediaID: mediaID, Name: "voice.amr", MIMEType: "audio/amr"}
}

func TestTranscribePlatformAudioReplacesVoiceSegment(t *testing.T) {
	agent, ids := newASRTestAgent(t, []byte("voice-bytes"))
	fake := &fakeAudioTranscriber{reply: "你好，世界"}
	agent.asr = fake
	agent.asrSelection = testASRSelection()
	ctx := asrMessageContext([]platform.MessageSegment{{Type: platform.SegmentText, Text: "听"}, voiceSegment(ids[0])})

	out := agent.transcribePlatformAudio(ctx)
	msg, ok := platform.MessageContextFrom(out)
	if !ok {
		t.Fatal("missing message context")
	}
	if len(msg.Segments) != 2 || msg.Segments[1].Type != platform.SegmentText {
		t.Fatalf("segments = %#v", msg.Segments)
	}
	if !strings.Contains(msg.Segments[1].Text, "你好，世界") || !strings.Contains(msg.Segments[1].Text, "自动转写") {
		t.Fatalf("transcription text = %q", msg.Segments[1].Text)
	}
	if fake.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", fake.callCount())
	}
	if got := string(fake.requests[0].Data); got != "voice-bytes" {
		t.Fatalf("transcriber data = %q", got)
	}
}

func TestTranscribePlatformAudioKeepsSegmentOnFailure(t *testing.T) {
	agent, ids := newASRTestAgent(t, []byte("voice-bytes"))
	agent.asr = &fakeAudioTranscriber{err: errors.New("asr down")}
	agent.asrSelection = testASRSelection()
	ctx := asrMessageContext([]platform.MessageSegment{voiceSegment(ids[0])})

	out := agent.transcribePlatformAudio(ctx)
	msg, _ := platform.MessageContextFrom(out)
	if len(msg.Segments) != 1 || msg.Segments[0].Type != platform.SegmentFile || msg.Segments[0].MediaID != ids[0] {
		t.Fatalf("segments = %#v", msg.Segments)
	}
}

func TestTranscribePlatformAudioHonorsSegmentLimit(t *testing.T) {
	agent, ids := newASRTestAgent(t, []byte("one"), []byte("two"))
	fake := &fakeAudioTranscriber{reply: "ok"}
	agent.asr = fake
	agent.asrSelection = testASRSelection()
	agent.asrMaxSegments = 1
	ctx := asrMessageContext([]platform.MessageSegment{voiceSegment(ids[0]), voiceSegment(ids[1])})

	out := agent.transcribePlatformAudio(ctx)
	msg, _ := platform.MessageContextFrom(out)
	if fake.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", fake.callCount())
	}
	if len(msg.Segments) != 2 || msg.Segments[0].Type != platform.SegmentText || msg.Segments[1].Type != platform.SegmentFile {
		t.Fatalf("segments = %#v", msg.Segments)
	}
}

func TestTranscribePlatformAudioBudgetDenialSkipsProvider(t *testing.T) {
	agent, ids := newASRTestAgent(t, []byte("one"), []byte("two"))
	fake := &fakeAudioTranscriber{reply: "ok"}
	agent.asr = fake
	agent.asrSelection = testASRSelection()
	agent.budgetLimits.GlobalASRDaily = 1
	agent.budgetReservations = map[string]int64{}
	agent.budgetDigests = map[string]string{}
	first := asrMessageContext([]platform.MessageSegment{voiceSegment(ids[0])})
	if out := agent.transcribePlatformAudio(first); len(platformMessageSegments(t, out)) != 1 || fake.callCount() != 1 {
		t.Fatalf("first call segments=%v calls=%d", platformMessageSegments(t, out), fake.callCount())
	}
	second := asrMessageContext([]platform.MessageSegment{voiceSegment(ids[1])})
	before := fake.callCount()
	agent.transcribePlatformAudio(second)
	if fake.callCount() != before {
		t.Fatalf("provider was called after budget denial: before=%d after=%d", before, fake.callCount())
	}
}

func TestTranscribePlatformAudioHonorsGroupOffSwitch(t *testing.T) {
	agent, ids := newASRTestAgent(t, []byte("voice-bytes"))
	fake := &fakeAudioTranscriber{reply: "should not run"}
	agent.asr = fake
	agent.asrSelection = testASRSelection()
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qq-onebot",
		ScopeID:          "group:10001",
		ConversationKind: platform.ConversationGroup,
		Segments:         []platform.MessageSegment{voiceSegment(ids[0])},
	})
	agent.setGroupPolicyForScope(agent.scope(ctx), config.GroupPolicyConfig{ASR: testBoolPtr(false)})
	out := agent.transcribePlatformAudio(ctx)
	if fake.callCount() != 0 {
		t.Fatalf("provider called %d times for a disabled group", fake.callCount())
	}
	msg, _ := platform.MessageContextFrom(out)
	if len(msg.Segments) != 1 || msg.Segments[0].Type != platform.SegmentFile {
		t.Fatalf("segments = %#v", msg.Segments)
	}
}

func testASRSelection() config.ModelSelection {
	return config.ModelSelection{Provider: "test", Model: "whisper-test"}
}

func platformMessageSegments(t *testing.T, ctx context.Context) []platform.MessageSegment {
	t.Helper()
	msg, ok := platform.MessageContextFrom(ctx)
	if !ok {
		t.Fatal("missing message context")
	}
	return msg.Segments
}
