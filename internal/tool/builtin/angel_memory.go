package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"elbot/internal/angelmemory"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/tool"
	"elbot/internal/tool/runtimeinfo"
)

const (
	angelRememberToolName = "angel_remember"
	angelRecallToolName   = "angel_recall"
)

type AngelRememberTool struct {
	service *angelmemory.Service
}

type AngelRecallTool struct {
	service *angelmemory.Service
}

type angelRememberArgs struct {
	Content string `json:"content"`
	Tags    string `json:"tags"`
}

type angelRecallArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func NewAngelMemoryTools(service *angelmemory.Service, _ ...runtimeinfo.Info) []tool.Tool {
	return []tool.Tool{
		AngelRememberTool{service: service},
		AngelRecallTool{service: service},
	}
}

func (AngelRememberTool) Name() string { return angelRememberToolName }
func (AngelRecallTool) Name() string   { return angelRecallToolName }

func (t AngelRememberTool) Info() tool.Info { return angelRememberBuilder().BuildInfo() }
func (t AngelRememberTool) Schema() llm.ToolSchema {
	return angelRememberBuilder().BuildSchema()
}
func (t AngelRecallTool) Info() tool.Info { return angelRecallBuilder().BuildInfo() }
func (t AngelRecallTool) Schema() llm.ToolSchema {
	return angelRecallBuilder().BuildSchema()
}

func (t AngelRememberTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if t.service == nil || !t.service.Ready() {
		return &tool.Result{Content: "angel memory 服务未配置。"}, nil
	}
	msgCtx, ok := platform.MessageContextFrom(ctx)
	if !ok || strings.TrimSpace(msgCtx.Platform) == "" || strings.TrimSpace(msgCtx.ScopeID) == "" {
		return &tool.Result{Content: "当前上下文没有平台聊天信息，无法确定记忆范围。"}, nil
	}
	var args angelRememberArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse angel_remember arguments: %w", err)
		}
	}
	content := strings.TrimSpace(args.Content)
	if content == "" {
		return &tool.Result{Content: "content 不能为空。"}, nil
	}
	memory, err := t.service.Remember(ctx, msgCtx.Platform, msgCtx.ScopeID, content, args.Tags, "tool")
	if err != nil {
		return nil, err
	}
	return &tool.Result{Content: fmt.Sprintf("已记住（强度 %d）：%s", memory.Strength, memory.Content)}, nil
}

func (t AngelRecallTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if t.service == nil || !t.service.Ready() {
		return &tool.Result{Content: "angel memory 服务未配置。"}, nil
	}
	msgCtx, ok := platform.MessageContextFrom(ctx)
	if !ok || strings.TrimSpace(msgCtx.Platform) == "" || strings.TrimSpace(msgCtx.ScopeID) == "" {
		return &tool.Result{Content: "当前上下文没有平台聊天信息，无法确定记忆范围。"}, nil
	}
	var args angelRecallArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse angel_recall arguments: %w", err)
		}
	}
	memories, err := t.service.Recall(ctx, msgCtx.Platform, msgCtx.ScopeID, args.Query, args.Limit)
	if err != nil {
		return nil, err
	}
	if len(memories) == 0 {
		return &tool.Result{Content: "没有找到符合条件的长期记忆。"}, nil
	}
	lines := []string{fmt.Sprintf("找到 %d 条相关记忆：", len(memories))}
	for i, memory := range memories {
		lines = append(lines, fmt.Sprintf("%d. [%d] %s", i+1, memory.Strength, memory.Content))
	}
	return &tool.Result{Content: strings.Join(lines, "\n")}, nil
}

func angelRememberBuilder() *tool.Builder {
	return tool.NewBuilder(angelRememberToolName).
		Description("记录一条当前平台/会话范围内的长期记忆。用户明确表达重要偏好、事实或约定时使用。").
		Risk(tool.RiskMedium).
		Tags("memory").
		String("content", "要记住的内容，建议第三人称、一条一件事。", tool.Required()).
		String("tags", "可选标签，用逗号或空格分隔。")
}

func angelRecallBuilder() *tool.Builder {
	return tool.NewBuilder(angelRecallToolName).
		Description("按关键词检索当前平台/会话范围内的长期记忆；不传关键词时返回强度最高的记忆。").
		Risk(tool.RiskLow).
		Tags("memory").
		String("query", "检索关键词，留空返回强度最高的记忆。").
		Integer("limit", "返回条数，默认 5，最大 50。")
}
