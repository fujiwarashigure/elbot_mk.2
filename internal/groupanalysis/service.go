// Package groupanalysis implements group chat statistics using ElBot's local
// chat-history tables. It is a clean-room implementation: it does not import or
// copy code, prompts, templates, or assets from third-party group-analysis
// plugins.
package groupanalysis

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"elbot/internal/safecontext"
	"elbot/internal/storage"
)

const (
	defaultRangeLimit  = 5000
	maxRangeLimit      = 200000
	maxTopSenders      = 10
	maxSenderNameRunes = 32
)

// Request selects one platform conversation and time window for analysis.
type Request struct {
	Platform string
	ScopeID  string
	Since    time.Time
	Until    time.Time
	Limit    int
}

// SenderStat summarizes one active participant.
type SenderStat struct {
	SenderID   string
	SenderName string
	Messages   int
	Chars      int
}

// Report is the platform-neutral result of one group analysis run.
type Report struct {
	Platform         string
	ScopeID          string
	Since            time.Time
	Until            time.Time
	TimeZone         string
	InboundMessages  int
	OutboundMessages int
	ActiveSenders    int
	TotalChars       int
	HourlyCounts     [24]int
	TopSenders       []SenderStat
	// Truncated is true when either direction reached the configured read
	// limit and more messages may exist in the requested window.
	Truncated bool
	// InboundTruncated and OutboundTruncated pinpoint which direction was cut
	// off, because outbound rows are only counted and inbound rows aggregated.
	InboundTruncated  bool
	OutboundTruncated bool
	// ScannedMessages is the number of inbound rows actually read.
	ScannedMessages int
}

// Service reads local history and computes statistics. It intentionally has no
// direct dependency on LLM providers or report renderers so it can be tested
// and extended incrementally.
type Service struct {
	history    storage.ChatHistoryRangeRepository
	outbound   storage.OutboundMessageRepository
	Summarizer Summarizer
}

func NewService(history storage.ChatHistoryRangeRepository, outbound storage.OutboundMessageRepository) *Service {
	return &Service{history: history, outbound: outbound}
}

// Analyze computes a report from inbound and outbound messages in the window.
func (s *Service) Analyze(ctx context.Context, req Request) (Report, error) {
	if s == nil || s.history == nil {
		return Report{}, fmt.Errorf("group analysis history repository is not configured")
	}
	platform := strings.TrimSpace(req.Platform)
	scopeID := strings.TrimSpace(req.ScopeID)
	if platform == "" || scopeID == "" {
		return Report{}, fmt.Errorf("group analysis platform and scope are required")
	}
	since := req.Since
	until := req.Until
	if since.IsZero() {
		since = time.Now().AddDate(0, 0, -1)
	}
	if until.IsZero() {
		until = time.Now()
	}
	if !since.Before(until) {
		return Report{}, fmt.Errorf("group analysis since must be before until")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultRangeLimit
	}
	if limit > maxRangeLimit {
		limit = maxRangeLimit
	}
	loc := since.Location()
	if loc == nil {
		loc = time.Local
	}

	report := Report{
		Platform: platform,
		ScopeID:  scopeID,
		Since:    since,
		Until:    until,
		TimeZone: loc.String(),
	}
	senders := map[string]*SenderStat{}

	// Inbound rows are aggregated page by page and never retained as a whole,
	// so a 200k-message window does not have to fit in memory.
	scanned, inboundTruncated, err := s.scanInbound(ctx, platform, scopeID, since, until, limit, func(page []storage.ChatMessage) {
		for _, row := range page {
			// A message counts even when it has no text (for example a pure
			// image or file message with segments). Chars remain text-only.
			report.InboundMessages++
			text := strings.TrimSpace(row.Text)
			report.TotalChars += len([]rune(text))
			if !row.CreatedAt.IsZero() {
				report.HourlyCounts[row.CreatedAt.In(loc).Hour()]++
			}
			senderID := strings.TrimSpace(row.SenderID)
			if senderID == "" {
				continue
			}
			stat := senders[senderID]
			if stat == nil {
				stat = &SenderStat{SenderID: senderID, SenderName: strings.TrimSpace(row.SenderName)}
				senders[senderID] = stat
			}
			if stat.SenderName == "" && strings.TrimSpace(row.SenderName) != "" {
				stat.SenderName = strings.TrimSpace(row.SenderName)
			}
			stat.Messages++
			stat.Chars += len([]rune(text))
		}
	})
	if err != nil {
		return Report{}, fmt.Errorf("list group analysis history: %w", err)
	}
	report.ScannedMessages = scanned
	report.InboundTruncated = inboundTruncated

	if s.outbound != nil {
		outboundCount, outboundTruncated, err := s.countOutbound(ctx, platform, scopeID, since, until, limit)
		if err != nil {
			return Report{}, fmt.Errorf("count group analysis outbound messages: %w", err)
		}
		report.OutboundMessages = outboundCount
		report.OutboundTruncated = outboundTruncated
	}

	report.Truncated = report.InboundTruncated || report.OutboundTruncated
	report.ActiveSenders = len(senders)
	report.TopSenders = topSenders(senders, maxTopSenders)
	return report, nil
}

// scanInbound reads up to totalLimit inbound rows in 5000-row pages and hands
// each page to visit as it arrives. Nothing beyond the current page is
// retained, so a high-volume window does not have to fit in memory.
func (s *Service) scanInbound(ctx context.Context, platform, scopeID string, since, until time.Time, totalLimit int, visit func([]storage.ChatMessage)) (int, bool, error) {
	scanned := 0
	var afterSeq int64
	for scanned < totalLimit {
		fetch := totalLimit - scanned
		if fetch > defaultRangeLimit {
			fetch = defaultRangeLimit
		}
		page, err := s.history.ListRange(ctx, storage.ChatHistoryRangeRequest{
			Platform:        platform,
			PlatformScopeID: scopeID,
			Since:           &since,
			Until:           &until,
			AfterSeq:        afterSeq,
			Limit:           fetch,
		})
		if err != nil {
			return scanned, false, err
		}
		if len(page) > 0 {
			visit(page)
			scanned += len(page)
			afterSeq = page[len(page)-1].Seq
		}
		if len(page) < fetch {
			return scanned, false, nil
		}
		if scanned >= totalLimit {
			probe, err := s.history.ListRange(ctx, storage.ChatHistoryRangeRequest{
				Platform:        platform,
				PlatformScopeID: scopeID,
				Since:           &since,
				Until:           &until,
				AfterSeq:        afterSeq,
				Limit:           1,
			})
			if err != nil {
				return scanned, false, err
			}
			return scanned, len(probe) > 0, nil
		}
	}
	return scanned, false, nil
}

// countOutbound returns the outbound message count for the window. Repositories
// that implement storage.OutboundMessageCounter answer with one COUNT query;
// the fallback pages through rows without retaining them.
func (s *Service) countOutbound(ctx context.Context, platform, scopeID string, since, until time.Time, totalLimit int) (int, bool, error) {
	if counter, ok := s.outbound.(storage.OutboundMessageCounter); ok {
		count, err := counter.CountRange(ctx, storage.OutboundMessageRangeRequest{
			Platform:        platform,
			PlatformScopeID: scopeID,
			Since:           &since,
			Until:           &until,
		})
		if err != nil {
			return 0, false, err
		}
		return count, false, nil
	}
	count := 0
	var afterSeq int64
	for count < totalLimit {
		fetch := totalLimit - count
		if fetch > defaultRangeLimit {
			fetch = defaultRangeLimit
		}
		page, err := s.outbound.ListRange(ctx, storage.OutboundMessageRangeRequest{
			Platform:        platform,
			PlatformScopeID: scopeID,
			Since:           &since,
			Until:           &until,
			AfterSeq:        afterSeq,
			Limit:           fetch,
		})
		if err != nil {
			return count, false, err
		}
		count += len(page)
		if len(page) < fetch {
			return count, false, nil
		}
		afterSeq = page[len(page)-1].Seq
		if count >= totalLimit {
			probe, err := s.outbound.ListRange(ctx, storage.OutboundMessageRangeRequest{
				Platform:        platform,
				PlatformScopeID: scopeID,
				Since:           &since,
				Until:           &until,
				AfterSeq:        afterSeq,
				Limit:           1,
			})
			if err != nil {
				return count, false, err
			}
			return count, len(probe) > 0, nil
		}
	}
	return count, false, nil
}

// FormatText renders a compact, deterministic report suitable for platform
// text output and for further LLM summarisation.
func (r Report) FormatText() string {
	loc := r.Since.Location()
	if loc == nil {
		loc = time.Local
	}
	timeZone := strings.TrimSpace(r.TimeZone)
	if timeZone == "" {
		timeZone = loc.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "群聊分析报告\n")
	fmt.Fprintf(&b, "平台：%s\n", reportInline(r.Platform, maxSenderNameRunes))
	fmt.Fprintf(&b, "会话：%s\n", reportInline(r.ScopeID, maxSenderNameRunes))
	fmt.Fprintf(&b, "统计区间：%s ~ %s（%s）\n", formatTime(r.Since, loc), formatTime(r.Until, loc), timeZone)
	fmt.Fprintf(&b, "消息数：入站 %d，出站 %d\n", r.InboundMessages, r.OutboundMessages)
	fmt.Fprintf(&b, "实际扫描：入站 %d 条\n", r.ScannedMessages)
	fmt.Fprintf(&b, "活跃成员：%d\n", r.ActiveSenders)
	fmt.Fprintf(&b, "总字数：%d\n", r.TotalChars)
	fmt.Fprintf(&b, "说明：消息数按聊天记录统计，字数只统计文本；短时间窗按本地时区 %s 计算。\n", timeZone)
	switch {
	case r.InboundTruncated && r.OutboundTruncated:
		b.WriteString("注意：入站和出站消息都达到了读取上限，统计可能不完整；请缩小时间范围或提高 max_messages。\n")
	case r.InboundTruncated:
		b.WriteString("注意：入站消息达到读取上限，入站统计可能不完整；请缩小时间范围或提高 max_messages。\n")
	case r.OutboundTruncated:
		b.WriteString("注意：出站消息达到读取上限，出站统计可能不完整；请缩小时间范围或提高 max_messages。\n")
	case r.Truncated:
		b.WriteString("注意：已达到读取上限，统计可能不完整；请缩小时间范围或提高 max_messages。\n")
	}
	if len(r.TopSenders) > 0 {
		b.WriteString("活跃成员 Top：\n")
		for i, stat := range r.TopSenders {
			name := reportInline(stat.SenderName, maxSenderNameRunes)
			if name == "" {
				name = reportInline(stat.SenderID, maxSenderNameRunes)
			}
			fmt.Fprintf(&b, "%d. %s：%d 条 / %d 字\n", i+1, name, stat.Messages, stat.Chars)
		}
	}
	peakHour, peakCount := peakHour(r.HourlyCounts)
	if peakCount > 0 {
		fmt.Fprintf(&b, "最活跃时段：%02d:00，%d 条\n", peakHour, peakCount)
	}
	return strings.TrimSpace(b.String())
}

func topSenders(senders map[string]*SenderStat, limit int) []SenderStat {
	out := make([]SenderStat, 0, len(senders))
	for _, stat := range senders {
		out = append(out, *stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Messages != out[j].Messages {
			return out[i].Messages > out[j].Messages
		}
		if out[i].Chars != out[j].Chars {
			return out[i].Chars > out[j].Chars
		}
		return out[i].SenderID < out[j].SenderID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func peakHour(counts [24]int) (int, int) {
	hour, count := 0, 0
	for i, value := range counts {
		if value > count {
			hour, count = i, value
		}
	}
	return hour, count
}

// reportInline folds one untrusted report field into a single bounded line so a
// nickname cannot inject newlines, control characters or prompt boundaries into
// the deterministic report or the summariser prompt.
func reportInline(value string, limit int) string {
	return safecontext.TruncateRunes(safecontext.Inline(value), limit)
}

func formatTime(value time.Time, loc *time.Location) string {
	if value.IsZero() {
		return "-"
	}
	if loc == nil {
		loc = time.Local
	}
	return value.In(loc).Format("2006-01-02 15:04")
}
