package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"elbot/internal/groupanalysis"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/tool"
	"elbot/internal/tool/runtimeinfo"
)

const groupAnalysisToolName = "group_analysis"

type GroupAnalysisTool struct {
	service     *groupanalysis.Service
	maxMessages int
	info        runtimeinfo.Info
}

type groupAnalysisArgs struct {
	Days  int `json:"days"`
	Limit int `json:"limit"`
}

func NewGroupAnalysisTool(service *groupanalysis.Service, maxMessages int, infos ...runtimeinfo.Info) GroupAnalysisTool {
	if maxMessages <= 0 {
		maxMessages = 5000
	}
	return GroupAnalysisTool{service: service, maxMessages: maxMessages, info: runtimeinfo.First(infos...)}
}

func (GroupAnalysisTool) Name() string { return groupAnalysisToolName }

func (t GroupAnalysisTool) Info() tool.Info { return groupAnalysisBuilder().BuildInfo() }

func (t GroupAnalysisTool) Schema() llm.ToolSchema { return groupAnalysisBuilder().BuildSchema() }

func (t GroupAnalysisTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if t.service == nil {
		return &tool.Result{Content: "群分析服务未配置。"}, nil
	}
	msgCtx, ok := platform.MessageContextFrom(ctx)
	if !ok || strings.TrimSpace(msgCtx.Platform) == "" || strings.TrimSpace(msgCtx.ScopeID) == "" {
		return &tool.Result{Content: "当前上下文没有平台聊天信息，无法确定要分析的群。"}, nil
	}
	var args groupAnalysisArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse group_analysis arguments: %w", err)
		}
	}
	days := args.Days
	if days <= 0 {
		days = 1
	}
	if days > 30 {
		days = 30
	}
	now := t.info.CurrentTime()
	if now.IsZero() {
		now = time.Now()
	}
	limit := args.Limit
	if limit <= 0 || limit > t.maxMessages {
		limit = t.maxMessages
	}
	report, err := t.service.Analyze(ctx, groupanalysis.Request{
		Platform: msgCtx.Platform,
		ScopeID:  msgCtx.ScopeID,
		Since:    now.AddDate(0, 0, -days),
		Until:    now,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	content := report.FormatText()
	if summary, summaryErr := t.service.Summarize(ctx, report); summaryErr == nil && strings.TrimSpace(summary) != "" {
		content = "摘要：" + strings.TrimSpace(summary) + "\n\n" + content
	}
	return &tool.Result{Content: content}, nil
}

func groupAnalysisBuilder() *tool.Builder {
	return tool.NewBuilder(groupAnalysisToolName).
		Description("统计当前群聊在指定天数内的消息量、活跃成员和活跃时段，输出平台无关的文本报告。").
		Risk(tool.RiskLow).
		Tags("chat", "group").
		Integer("days", "统计最近多少天，默认 1，最大 30。").
		Integer("limit", "最多读取的历史消息条数，默认 5000。")
}
