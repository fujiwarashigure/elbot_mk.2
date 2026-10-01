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

	"elbot/internal/storage"
)

const (
	defaultRangeLimit = 5000
	maxTopSenders     = 10
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
	InboundMessages  int
	OutboundMessages int
	ActiveSenders    int
	TotalChars       int
	HourlyCounts     [24]int
	TopSenders       []SenderStat
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
	if limit <= 0 || limit > defaultRangeLimit {
		limit = defaultRangeLimit
	}

	report := Report{
		Platform: platform,
		ScopeID:  scopeID,
		Since:    since,
		Until:    until,
	}
	senders := map[string]*SenderStat{}
	rows, err := s.history.ListRange(ctx, storage.ChatHistoryRangeRequest{
		Platform:        platform,
		PlatformScopeID: scopeID,
		Since:           &since,
		Until:           &until,
		Limit:           limit,
	})
	if err != nil {
		return Report{}, fmt.Errorf("list group analysis history: %w", err)
	}
	for _, row := range rows {
		text := strings.TrimSpace(row.Text)
		if text == "" {
			continue
		}
		report.InboundMessages++
		report.TotalChars += len([]rune(text))
		if !row.CreatedAt.IsZero() {
			report.HourlyCounts[row.CreatedAt.Local().Hour()]++
		}
		if strings.TrimSpace(row.SenderID) == "" {
			continue
		}
		stat := senders[row.SenderID]
		if stat == nil {
			stat = &SenderStat{SenderID: row.SenderID, SenderName: strings.TrimSpace(row.SenderName)}
			senders[row.SenderID] = stat
		}
		if stat.SenderName == "" && strings.TrimSpace(row.SenderName) != "" {
			stat.SenderName = strings.TrimSpace(row.SenderName)
		}
		stat.Messages++
		stat.Chars += len([]rune(text))
	}
	if s.outbound != nil {
		outboundRows, err := s.outbound.ListRange(ctx, storage.OutboundMessageRangeRequest{
			Platform:        platform,
			PlatformScopeID: scopeID,
			Since:           &since,
			Until:           &until,
			Limit:           limit,
		})
		if err != nil {
			return Report{}, fmt.Errorf("list group analysis outbound messages: %w", err)
		}
		for _, row := range outboundRows {
			if strings.TrimSpace(row.Text) != "" {
				report.OutboundMessages++
			}
		}
	}

	report.ActiveSenders = len(senders)
	report.TopSenders = topSenders(senders, maxTopSenders)
	return report, nil
}

// FormatText renders a compact, deterministic report suitable for platform
// text output and for further LLM summarisation.
func (r Report) FormatText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "群聊分析报告\n")
	fmt.Fprintf(&b, "平台：%s\n", r.Platform)
	fmt.Fprintf(&b, "会话：%s\n", r.ScopeID)
	fmt.Fprintf(&b, "统计区间：%s ~ %s\n", formatTime(r.Since), formatTime(r.Until))
	fmt.Fprintf(&b, "消息数：入站 %d，出站 %d\n", r.InboundMessages, r.OutboundMessages)
	fmt.Fprintf(&b, "活跃成员：%d\n", r.ActiveSenders)
	fmt.Fprintf(&b, "总字数：%d\n", r.TotalChars)
	if len(r.TopSenders) > 0 {
		b.WriteString("活跃成员 Top：\n")
		for i, stat := range r.TopSenders {
			name := stat.SenderName
			if name == "" {
				name = stat.SenderID
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

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04")
}
