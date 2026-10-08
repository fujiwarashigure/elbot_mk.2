package contextmgr

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/llm"
)

type CompactRequest struct {
	Provider          string
	Model             string
	Messages          []CompactMessage
	UserInputs        []string
	UserInputMaxRunes int
}

type CompactMessage struct {
	Role      string
	Content   string
	ToolCalls []CompactToolCall
}

type CompactToolCall struct {
	Name      string
	Arguments string
}

type CompactResult struct {
	Summary          string
	AssembledSummary string
	Usage            *llm.Usage
}

// Compactor 是一次上下文压缩的执行后端。当前唯一实现是客户端的 Compressor（把历史发给
// 压缩模型生成摘要）；一旦某个协议真的能用服务端原生压缩，就新增一个实现并在分派点按
// llm.ProtocolCapabilities 选择，而不是把协议判断写进压缩流程。
type Compactor interface {
	// Name 说明这条压缩路径，用于日志与审计（"这次是谁压的"）。
	Name() string
	Compact(ctx context.Context, req CompactRequest) (*CompactResult, error)
	SummarizeText(ctx context.Context, req SummarizeRequest) (*SummarizeResult, error)
}

// CompactionChoice 记录一次压缩实际走了哪个后端，以及是否发生了"协议更强、实现未跟上"的
// 回退。回退必须显式记录：静默回退会让"服务端压缩没生效"变成没人发现的行为差异。
type CompactionChoice struct {
	Backend        string
	FallbackReason string
}

type Compressor struct {
	ClientFor ClientProvider
}

// Name identifies the client-side summary backend.
func (c Compressor) Name() string { return "client_summary" }

func (c Compressor) Compact(ctx context.Context, req CompactRequest) (*CompactResult, error) {
	if c.ClientFor == nil {
		return nil, fmt.Errorf("compressor is not configured")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("没有可压缩的历史消息")
	}
	if req.Provider == "" || req.Model == "" {
		return nil, fmt.Errorf("压缩模型未配置")
	}

	messages, userInputs := capCompactRequest(req)
	prompt := compactPrompt(messages, userInputs)
	ch, err := c.ClientFor(req.Provider).ChatStream(ctx, llm.ChatRequest{
		Model: req.Model,
		Messages: []llm.LLMMessage{
			{Role: llm.RoleSystem, Segments: llm.TextSegments(compactSystemPrompt)},
			{Role: llm.RoleUser, Segments: llm.TextSegments(prompt)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("调用压缩模型: %w", err)
	}

	var sb strings.Builder
	var usage *llm.Usage
	for chunk := range ch {
		if chunk.Error != nil {
			return nil, fmt.Errorf("读取压缩结果: %w", chunk.Error)
		}
		sb.WriteString(chunk.DeltaContent)
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	summaryText := strings.TrimSpace(sb.String())
	if summaryText == "" {
		return nil, fmt.Errorf("压缩模型返回空摘要")
	}

	return &CompactResult{Summary: summaryText, AssembledSummary: assembleSummary(summaryText, userInputs), Usage: usage}, nil
}

func capCompactRequest(req CompactRequest) ([]CompactMessage, []string) {
	maxRunes := req.UserInputMaxRunes
	if maxRunes <= 0 {
		maxRunes = DefaultUserOriginalMaxRunes
	}
	messages := make([]CompactMessage, len(req.Messages))
	for i, message := range req.Messages {
		messages[i] = message
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			messages[i].Content = TruncateTextWithMarker(message.Content, maxRunes, "\n...[用户原话过长，压缩时已截断]")
		}
	}
	userInputs := make([]string, 0, len(req.UserInputs))
	for _, input := range req.UserInputs {
		userInputs = append(userInputs, TruncateTextWithMarker(input, maxRunes, "\n...[用户原话过长，压缩时已截断]"))
	}
	return messages, userInputs
}
