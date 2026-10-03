package groupanalysis

import (
	"context"
	"fmt"
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

type pagedRangeRepo struct {
	rows []storage.ChatMessage
}

func (f *pagedRangeRepo) ListRange(_ context.Context, req storage.ChatHistoryRangeRequest) ([]storage.ChatMessage, error) {
	out := make([]storage.ChatMessage, 0)
	for _, row := range f.rows {
		if row.Seq <= req.AfterSeq {
			continue
		}
		if req.Limit > 0 && len(out) >= req.Limit {
			break
		}
		out = append(out, row)
	}
	return out, nil
}

type fakeOutboundRepo struct {
	rows []storage.OutboundMessage
}

func (f *fakeOutboundRepo) Append(context.Context, *storage.OutboundMessage) error { return nil }

func (f *fakeOutboundRepo) ListRange(context.Context, storage.OutboundMessageRangeRequest) ([]storage.OutboundMessage, error) {
	return f.rows, nil
}

func (f *fakeOutboundRepo) DeleteBefore(context.Context, time.Time) (int, error) { return 0, nil }

func TestAnalyzeCountsMediaMessagesAndTopSenders(t *testing.T) {
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	history := &fakeRangeRepo{rows: []storage.ChatMessage{
		{SenderID: "u1", SenderName: "甲", Text: "你好", CreatedAt: since.Add(time.Hour)},
		{SenderID: "u1", SenderName: "甲", Text: "再见", CreatedAt: since.Add(2 * time.Hour)},
		{SenderID: "u2", SenderName: "乙", Text: "早", CreatedAt: since.Add(2 * time.Hour)},
		{SenderID: "u3", SenderName: "丙", Text: "", Segments: `[{"type":"image"}]`, CreatedAt: since.Add(3 * time.Hour)},
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
	if report.InboundMessages != 4 {
		t.Fatalf("InboundMessages = %d, want 4 (media message must count)", report.InboundMessages)
	}
	if report.OutboundMessages != 2 {
		t.Fatalf("OutboundMessages = %d, want 2", report.OutboundMessages)
	}
	if report.ActiveSenders != 3 {
		t.Fatalf("ActiveSenders = %d, want 3", report.ActiveSenders)
	}
	if report.TotalChars != 5 {
		t.Fatalf("TotalChars = %d, want 5 (text only)", report.TotalChars)
	}
	if len(report.TopSenders) != 3 || report.TopSenders[0].SenderID != "u1" || report.TopSenders[0].Messages != 2 {
		t.Fatalf("TopSenders = %#v", report.TopSenders)
	}
	foundMediaSender := false
	for _, stat := range report.TopSenders {
		if stat.SenderID == "u3" {
			foundMediaSender = true
			if stat.Messages != 1 || stat.Chars != 0 {
				t.Fatalf("media sender stat = %#v", stat)
			}
		}
	}
	if !foundMediaSender {
		t.Fatalf("media-only sender missing from TopSenders: %#v", report.TopSenders)
	}
	text := report.FormatText()
	if !strings.Contains(text, "入站 4，出站 2") || !strings.Contains(text, "实际扫描：入站 4 条") || !strings.Contains(text, "说明：消息数按聊天记录统计") {
		t.Fatalf("FormatText = %q", text)
	}
}

func TestAnalyzePaginatesAndMarksTruncation(t *testing.T) {
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	history := &pagedRangeRepo{rows: []storage.ChatMessage{
		{Seq: 1, SenderID: "u1", Text: "一", CreatedAt: since.Add(time.Hour)},
		{Seq: 2, SenderID: "u1", Text: "二", CreatedAt: since.Add(2 * time.Hour)},
		{Seq: 3, SenderID: "u2", Text: "三", CreatedAt: since.Add(3 * time.Hour)},
	}}
	service := NewService(history, nil)
	report, err := service.Analyze(context.Background(), Request{
		Platform: "qqonebot",
		ScopeID:  "group:1",
		Since:    since,
		Until:    since.Add(24 * time.Hour),
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.InboundMessages != 2 || report.ScannedMessages != 2 {
		t.Fatalf("report = %#v, want exactly 2 scanned messages", report)
	}
	if !report.Truncated || !report.InboundTruncated {
		t.Fatalf("report = %#v, want inbound truncation", report)
	}
	if report.OutboundTruncated {
		t.Fatalf("report = %#v, outbound was not truncated", report)
	}
	if !strings.Contains(report.FormatText(), "读取上限") {
		t.Fatalf("FormatText missing truncation note: %q", report.FormatText())
	}
}

type countingOutboundRepo struct {
	fakeOutboundRepo
	countRequests int
	listRequests  int
}

func (f *countingOutboundRepo) CountRange(context.Context, storage.OutboundMessageRangeRequest) (int, error) {
	f.countRequests++
	return 12345, nil
}

func (f *countingOutboundRepo) ListRange(context.Context, storage.OutboundMessageRangeRequest) ([]storage.OutboundMessage, error) {
	f.listRequests++
	return f.rows, nil
}

func TestAnalyzeUsesOutboundCountQuery(t *testing.T) {
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	outbound := &countingOutboundRepo{}
	service := NewService(&fakeRangeRepo{}, outbound)
	report, err := service.Analyze(context.Background(), Request{
		Platform: "qqonebot",
		ScopeID:  "group:1",
		Since:    since,
		Until:    since.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.OutboundMessages != 12345 {
		t.Fatalf("OutboundMessages = %d, want 12345", report.OutboundMessages)
	}
	if report.OutboundTruncated {
		t.Fatalf("OutboundTruncated = true, want false for a COUNT query")
	}
	if outbound.countRequests != 1 || outbound.listRequests != 0 {
		t.Fatalf("count=%d list=%d, want count-only path", outbound.countRequests, outbound.listRequests)
	}
}

func BenchmarkAnalyzeLargeGroup(b *testing.B) {
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	rows := make([]storage.ChatMessage, 0, 5000)
	for i := 0; i < 5000; i++ {
		rows = append(rows, storage.ChatMessage{
			Seq:       int64(i + 1),
			SenderID:  fmt.Sprintf("u%d", i%50),
			Text:      strings.Repeat("消息内容", 4),
			CreatedAt: since.Add(time.Duration(i) * time.Second),
		})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		service := NewService(&pagedRangeRepo{rows: rows}, nil)
		if _, err := service.Analyze(context.Background(), Request{
			Platform: "qqonebot",
			ScopeID:  "group:1",
			Since:    since,
			Until:    since.Add(24 * time.Hour),
			Limit:    5000,
		}); err != nil {
			b.Fatalf("Analyze: %v", err)
		}
	}
}

func TestAnalyzeRejectsMissingScope(t *testing.T) {
	service := NewService(&fakeRangeRepo{}, nil)
	if _, err := service.Analyze(context.Background(), Request{Platform: "qqonebot"}); err == nil {
		t.Fatal("Analyze accepted missing scope")
	}
}
