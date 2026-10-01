package groupanalysis

import (
	"context"
	"strings"
	"testing"
	"time"

	"elbot/internal/storage"
)

type fakeRangeRepo struct {
	rows []storage.ChatMessage
	req  storage.ChatHistoryRangeRequest
}

func (f *fakeRangeRepo) ListRange(_ context.Context, req storage.ChatHistoryRangeRequest) ([]storage.ChatMessage, error) {
	f.req = req
	return f.rows, nil
}

type fakeOutboundRepo struct {
	rows []storage.OutboundMessage
}

func (f *fakeOutboundRepo) Append(context.Context, *storage.OutboundMessage) error { return nil }

func (f *fakeOutboundRepo) ListRange(context.Context, storage.OutboundMessageRangeRequest) ([]storage.OutboundMessage, error) {
	return f.rows, nil
}

func (f *fakeOutboundRepo) DeleteBefore(context.Context, time.Time) (int, error) { return 0, nil }

func TestAnalyzeCountsMessagesAndTopSenders(t *testing.T) {
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	history := &fakeRangeRepo{rows: []storage.ChatMessage{
		{SenderID: "u1", SenderName: "甲", Text: "你好", CreatedAt: since.Add(time.Hour)},
		{SenderID: "u1", SenderName: "甲", Text: "再见", CreatedAt: since.Add(2 * time.Hour)},
		{SenderID: "u2", SenderName: "乙", Text: "早", CreatedAt: since.Add(2 * time.Hour)},
	}}
	outbound := &fakeOutboundRepo{rows: []storage.OutboundMessage{{Text: "收到"}, {Text: "好的"}}}
	service := NewService(history, outbound)

	report, err := service.Analyze(context.Background(), Request{
		Platform: "qqonebot",
		ScopeID:  "group:1",
		Since:    since,
		Until:    since.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.InboundMessages != 3 {
		t.Fatalf("InboundMessages = %d, want 3", report.InboundMessages)
	}
	if report.OutboundMessages != 2 {
		t.Fatalf("OutboundMessages = %d, want 2", report.OutboundMessages)
	}
	if report.ActiveSenders != 2 {
		t.Fatalf("ActiveSenders = %d, want 2", report.ActiveSenders)
	}
	if len(report.TopSenders) != 2 || report.TopSenders[0].SenderID != "u1" || report.TopSenders[0].Messages != 2 {
		t.Fatalf("TopSenders = %#v", report.TopSenders)
	}
	text := report.FormatText()
	if !strings.Contains(text, "入站 3，出站 2") || !strings.Contains(text, "最活跃时段") {
		t.Fatalf("FormatText = %q", text)
	}
}

func TestAnalyzeRejectsMissingScope(t *testing.T) {
	service := NewService(&fakeRangeRepo{}, nil)
	if _, err := service.Analyze(context.Background(), Request{Platform: "qqonebot"}); err == nil {
		t.Fatal("Analyze accepted missing scope")
	}
}
