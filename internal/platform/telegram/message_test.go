package telegram

import (
	"testing"

	"elbot/internal/platform"
)

func TestStripBotMentionFromCommand(t *testing.T) {
	text, mentioned := stripBotMention("/help@ElBot hi", "ElBot")
	if !mentioned {
		t.Fatal("mentioned = false")
	}
	if text != "/help hi" {
		t.Fatalf("text = %q", text)
	}
}

func TestStripBotMentionFromText(t *testing.T) {
	text, mentioned := stripBotMention("hello @ElBot", "ElBot")
	if !mentioned {
		t.Fatal("mentioned = false")
	}
	if text != "hello" {
		t.Fatalf("text = %q", text)
	}
}

func TestStripBotMentionFromTextCaseInsensitive(t *testing.T) {
	text, mentioned := stripBotMention("hello @elbot", "ElBot")
	if !mentioned {
		t.Fatal("mentioned = false")
	}
	if text != "hello" {
		t.Fatalf("text = %q", text)
	}
}

func TestNormalizeVoiceAndAudio(t *testing.T) {
	voiceMessage := normalizeMessage(message{Voice: &voice{FileID: "voice-id", MIMEType: "audio/ogg", FileSize: 10}})
	if voiceMessage.Text != "[语音]" || len(voiceMessage.Segments) != 1 {
		t.Fatalf("voice text=%q segments=%#v", voiceMessage.Text, voiceMessage.Segments)
	}
	voiceSegment := voiceMessage.Segments[0]
	if voiceSegment.Type != platform.SegmentFile || voiceSegment.Text != "语音" || voiceSegment.MIMEType != "audio/ogg" || voiceSegment.PlatformFileID != "voice-id" || voiceSegment.Name != "voice.ogg" {
		t.Fatalf("voice segment = %#v", voiceSegment)
	}

	audioMessage := normalizeMessage(message{Audio: &audio{FileID: "audio-id", FileName: "song.mp3", MIMEType: "audio/mpeg", FileSize: 20}})
	if audioMessage.Text != "[语音]" || len(audioMessage.Segments) != 1 {
		t.Fatalf("audio text=%q segments=%#v", audioMessage.Text, audioMessage.Segments)
	}
	audioSegment := audioMessage.Segments[0]
	if audioSegment.Type != platform.SegmentFile || audioSegment.Text != "语音" || audioSegment.Name != "song.mp3" || audioSegment.MIMEType != "audio/mpeg" || audioSegment.PlatformFileID != "audio-id" {
		t.Fatalf("audio segment = %#v", audioSegment)
	}
}

func TestNormalizeDocument(t *testing.T) {
	msg := message{Document: &document{FileID: "file-id", FileName: "a.txt", MIMEType: "text/plain"}}
	normalized := normalizeMessage(msg)
	if normalized.Text != "[文件]" {
		t.Fatalf("text = %q", normalized.Text)
	}
	if len(normalized.Segments) != 1 || normalized.Segments[0].Name != "a.txt" || normalized.Segments[0].MIMEType != "text/plain" || normalized.Segments[0].PlatformFileID != "file-id" || normalized.Segments[0].URL != "" {
		t.Fatalf("segments = %#v", normalized.Segments)
	}
}
