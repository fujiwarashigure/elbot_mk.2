package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"elbot/internal/logging"
	"elbot/internal/platform"
)

// Defaults for the inbound voice transcription path. They bound one message so
// a burst of recordings cannot open one provider connection per file or keep a
// turn busy for an unbounded time.
const (
	// DefaultASRParallelism is how many recordings are transcribed at once.
	DefaultASRParallelism = 2
	// DefaultASRMaxSegments is the largest batch one message may transcribe.
	DefaultASRMaxSegments = 4
	// DefaultASRMaxAudioBytes is the default read cap per recording.
	DefaultASRMaxAudioBytes = 20 * 1024 * 1024
)

// AudioTranscriber turns one inbound audio recording into text. The app layer
// owns the provider, upload format and retry policy so this interface stays
// small and easy to fake in tests.
type AudioTranscriber interface {
	Transcribe(ctx context.Context, req AudioTranscriptionRequest) (AudioTranscriptionResult, error)
}

// AudioTranscriptionRequest is one stored recording ready to upload.
type AudioTranscriptionRequest struct {
	MediaID  string
	Data     []byte
	Name     string
	MIMEType string
	Language string
}

// AudioTranscriptionResult is one successful transcription.
type AudioTranscriptionResult struct {
	Text     string
	Language string
	Provider string
	Model    string
}

type asrSlot struct {
	index   int
	segment platform.MessageSegment
}

// transcribePlatformAudio replaces current-message voice segments with a text
// transcription segment. It is a no-op when ASR is disabled, there is no
// current platform message, or the message has no bound recording.
//
// A failed transcription leaves the original [语音] marker in place so the
// message is never dropped. Budget reservations happen before any provider
// connection and are idempotent per turn/media, matching the vision path.
func (a *Agent) transcribePlatformAudio(ctx context.Context) context.Context {
	if a == nil || a.asr == nil || a.media == nil {
		return ctx
	}
	msg, ok := platform.MessageContextFrom(ctx)
	if !ok || len(msg.Segments) == 0 {
		return ctx
	}
	if a.isGroupScope(ctx) && !a.groupPolicyForScope(a.scope(ctx)).IsASREnabled() {
		return ctx
	}
	slots := make([]asrSlot, 0, 1)
	skipped := 0
	limit := a.asrSegmentLimit()
	for index, segment := range msg.Segments {
		if !isTranscribableAudioSegment(segment) || strings.TrimSpace(segment.MediaID) == "" {
			continue
		}
		if len(slots) >= limit {
			skipped++
			continue
		}
		slots = append(slots, asrSlot{index: index, segment: segment})
	}
	if len(slots) == 0 {
		return ctx
	}
	if skipped > 0 {
		a.audit("asr_skipped", "reason", "batch_limit", "limit", limit, "skipped", skipped, "scope", contextOverflowKey(a.scope(ctx)), "result", logging.ResultSkipped)
	}
	if strings.TrimSpace(a.asrSelection.Provider) != "" && strings.TrimSpace(a.asrSelection.Model) != "" {
		if err := a.authorizeExecutionModelSelection(ctx, a.asrSelection); err != nil {
			a.audit("model_denied", "kind", "asr", "provider", a.asrSelection.Provider, "model", a.asrSelection.Model, "reason", err.Error(), "scope", contextOverflowKey(a.scope(ctx)), "result", logging.ResultRejected)
			return ctx
		}
	}
	reservationRequests := make([]budgetReservationRequest, 0, len(slots))
	for index, slot := range slots {
		mediaID := strings.TrimSpace(slot.segment.MediaID)
		turnID := turnRequestIDFromContext(ctx)
		if strings.TrimSpace(turnID) == "" {
			turnID = shortHash(mediaID)
		}
		callID := fmt.Sprintf("%s:%s:asr:%d", turnID, mediaID, index)
		digest := shortHash(a.asrSelection.Provider + "\x00" + a.asrSelection.Model + "\x00" + mediaID)
		reservationRequests = append(reservationRequests, budgetReservationRequest{CallID: callID, Digest: digest})
	}
	if ok, reason := a.reserveBudgetBatchRequests(ctx, "asr", reservationRequests); !ok {
		a.audit("budget_denied", "kind", "asr", "reason", reason, "scope", contextOverflowKey(a.scope(ctx)), "result", logging.ResultRejected)
		return ctx
	}

	type attempt struct {
		text string
		err  error
	}
	results := make([]attempt, len(slots))
	workers := a.asrParallelismLimit()
	if workers > len(slots) {
		workers = len(slots)
	}
	work := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range work {
				text, err := a.transcribeAudioSegment(ctx, slots[index].segment)
				results[index] = attempt{text: text, err: err}
			}
		}()
	}
	for index := range slots {
		work <- index
	}
	close(work)
	wg.Wait()

	out := append([]platform.MessageSegment(nil), msg.Segments...)
	for index, slot := range slots {
		if results[index].err != nil {
			a.audit("asr_failed", "media_id", slot.segment.MediaID, "error", results[index].err.Error(), "scope", contextOverflowKey(a.scope(ctx)), "result", logging.ResultFailed)
			continue
		}
		text := asrTranscriptionText(index+1, results[index].text)
		if text == "" {
			continue
		}
		out[slot.index] = platform.MessageSegment{Type: platform.SegmentText, Text: text}
	}
	msg.Segments = out
	return platform.WithMessageContext(ctx, msg)
}

func (a *Agent) transcribeAudioSegment(ctx context.Context, segment platform.MessageSegment) (string, error) {
	mediaID := strings.TrimSpace(segment.MediaID)
	data, meta, err := a.media.ReadLimited(ctx, mediaID, a.asrAudioByteLimit())
	if err != nil {
		return "", err
	}
	mimeType := strings.TrimSpace(segment.MIMEType)
	if meta != nil && strings.TrimSpace(meta.MIMEType) != "" {
		mimeType = meta.MIMEType
	}
	result, err := a.asr.Transcribe(ctx, AudioTranscriptionRequest{
		MediaID:  mediaID,
		Data:     data,
		Name:     segment.Name,
		MIMEType: mimeType,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

func (a *Agent) asrParallelismLimit() int {
	if a.asrParallelism > 0 {
		return a.asrParallelism
	}
	return DefaultASRParallelism
}

func (a *Agent) asrSegmentLimit() int {
	if a.asrMaxSegments > 0 {
		return a.asrMaxSegments
	}
	return DefaultASRMaxSegments
}

func (a *Agent) asrAudioByteLimit() int64 {
	if a.asrMaxAudioBytes > 0 {
		return a.asrMaxAudioBytes
	}
	return DefaultASRMaxAudioBytes
}

// isTranscribableAudioSegment recognizes voice/file segments across the
// supported adapters. QQ OneBot labels records with Text="语音"; Telegram and
// QQ Official attachments usually carry audio/* MIME types.
func isTranscribableAudioSegment(segment platform.MessageSegment) bool {
	if segment.Type != platform.SegmentFile {
		return false
	}
	mimeType := strings.ToLower(strings.TrimSpace(segment.MIMEType))
	if strings.HasPrefix(mimeType, "audio/") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(segment.Text)) {
	case "语音", "音频", "voice", "audio", "record":
		return true
	}
	// Only voice-codec extensions are accepted as a fallback. A generic
	// uploaded .mp3/.wav file is not transcribed unless its adapter marks it
	// with an audio MIME type or a voice/record label, so a shared music file
	// cannot create an unexpected ASR cost.
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(segment.Name))) {
	case ".amr", ".silk", ".spx":
		return true
	default:
		return false
	}
}

// asrTranscriptionText labels a generated transcript so the model can tell it
// apart from typed text and knows the recording may have been misheard. The
// label is intentionally command-inert (it starts with '['), so a misheard
// voice message cannot accidentally trigger a slash command.
func asrTranscriptionText(index int, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return fmt.Sprintf("[语音 %d 自动转写（可能有误）：%s]", index, text)
}
