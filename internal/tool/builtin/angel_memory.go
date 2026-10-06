package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"elbot/internal/angelmemory"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/ratelimit"
	"elbot/internal/security"
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
	// exposeForget adds angel_forget as a dependency of angel_recall. Hidden
	// tools only reach the model as a dependency of a visible root, so this is
	// what makes the opt-in forget tool reachable once it is registered.
	exposeForget bool
}

type angelRememberArgs struct {
	Content string `json:"content"`
	Tags    string `json:"tags"`
}

type angelRecallArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func NewAngelMemoryTools(service *angelmemory.Service, opts AngelMemoryToolOptions, _ ...runtimeinfo.Info) []tool.Tool {
	tools := []tool.Tool{
		AngelRememberTool{service: service},
		AngelRecallTool{service: service, exposeForget: opts.AllowForget},
	}
	if !opts.AllowForget {
		return tools
	}
	return append(tools, AngelForgetTool{service: service, deletions: ratelimit.New(angelForgetMaxPerMinute, time.Minute)})
}

func (AngelRememberTool) Name() string { return angelRememberToolName }
func (AngelRecallTool) Name() string   { return angelRecallToolName }

func (t AngelRememberTool) Info() tool.Info { return angelRememberBuilder().BuildInfo() }
func (t AngelRememberTool) Schema() llm.ToolSchema {
	return angelRememberBuilder().BuildSchema()
}
func (t AngelRecallTool) Info() tool.Info { return t.builder().BuildInfo() }
func (t AngelRecallTool) Schema() llm.ToolSchema {
	return t.builder().BuildSchema()
}

func (t AngelRecallTool) builder() *tool.Builder {
	builder := angelRecallBuilder()
	if t.exposeForget {
		builder = builder.DependsOn(angelForgetToolName)
	}
	return builder
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
	actorID := ""
	if actor, ok := security.ActorFromContext(ctx); ok {
		actorID = strings.TrimSpace(actor.ID)
	}
	if actorID == "" {
		actorID = strings.TrimSpace(msgCtx.ActorID)
	}
	memory, err := t.service.RememberWithSource(ctx, msgCtx.Platform, msgCtx.ScopeID, content, args.Tags, angelmemory.Source{
		Kind:      "tool",
		ActorID:   actorID,
		MessageID: strings.TrimSpace(msgCtx.PlatformMessageID),
		SessionID: strings.TrimSpace(msgCtx.SessionID),
		Label:     "tool",
	})
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
		lines = append(lines, fmt.Sprintf("%d. [%d] id=%s %s%s", i+1, memory.Strength, memory.ID, memory.Content, memoryTraceSuffix(memory)))
	}
	return &tool.Result{Content: strings.Join(lines, "\n")}, nil
}

func memoryTraceSuffix(memory angelmemory.Memory) string {
	parts := make([]string, 0, 4)
	if kind := strings.TrimSpace(memory.SourceKind); kind != "" {
		parts = append(parts, "source="+kind)
	}
	if actorID := strings.TrimSpace(memory.SourceActorID); actorID != "" {
		parts = append(parts, "actor="+actorID)
	}
	if messageID := strings.TrimSpace(memory.SourceMessageID); messageID != "" {
		parts = append(parts, "message="+messageID)
	}
	if sessionID := strings.TrimSpace(memory.SourceSessionID); sessionID != "" {
		parts = append(parts, "session="+sessionID)
	}
	if len(parts) == 0 {
		if label := strings.TrimSpace(memory.Source); label != "" {
			parts = append(parts, "source="+label)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " ") + ")"
}

func angelRememberBuilder() *tool.Builder {
	return tool.NewBuilder(angelRememberToolName).
		Description("记录一条当前平台/会话范围内的长期记忆。用户明确表达重要偏好、事实或约定时使用。").
		Risk(tool.RiskLow).
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
