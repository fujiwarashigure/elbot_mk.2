package groupanalysis

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/llm"
)

// Summarizer turns one deterministic Report into a short natural-language
// summary. Implementations may call an LLM, but the interface is deliberately
// small so tests and deployments can replace it.
type Summarizer interface {
	Summarize(ctx context.Context, report Report) (string, error)
}

// LLMSummarizer is a clean-room, provider-agnostic summarizer.
type LLMSummarizer struct {
	Client llm.LLM
	Model  string
}

func (s LLMSummarizer) Summarize(ctx context.Context, report Report) (string, error) {
	if s.Client == nil {
		return "", fmt.Errorf("group analysis summarizer client is not configured")
	}
	prompt := strings.TrimSpace(fmt.Sprintf(`请用中文简洁总结下面的群聊统计报告：只依据报告中的数字说明整体活跃度、最活跃成员和活跃时段；如果报告没有提供上一周期基线，不要推断或编造“上升/下降”等变化，也不要编造报告中没有的数据。控制在 200 字以内。

%s`, report.FormatText()))
	stream, err := s.Client.ChatStream(ctx, llm.ChatRequest{
		Model: s.Model,
		Messages: []llm.LLMMessage{{
			Role:     llm.RoleUser,
			Segments: llm.TextSegments(prompt),
		}},
	})
	if err != nil {
		return "", fmt.Errorf("summarize group analysis: %w", err)
	}
	var b strings.Builder
	for chunk := range stream {
		if chunk.Error != nil {
			return "", fmt.Errorf("summarize group analysis stream: %w", chunk.Error)
		}
		b.WriteString(chunk.DeltaContent)
	}
	return strings.TrimSpace(b.String()), nil
}

// Summarize uses the optional summarizer attached to the service.
func (s *Service) Summarize(ctx context.Context, report Report) (string, error) {
	if s == nil || s.Summarizer == nil {
		return "", nil
	}
	return s.Summarizer.Summarize(ctx, report)
}
