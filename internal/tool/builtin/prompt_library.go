package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"elbot/internal/imagegen"
	"elbot/internal/llm"
	"elbot/internal/tool"
)

const PromptLibrarySearchName = "prompt_library_search"

// PromptLibrarySearchTool exposes the embedded GPT Image prompt library so the
// model can look up categories, anchors, keyword banks, negatives and ratios.
type PromptLibrarySearchTool struct{}

type promptLibrarySearchArgs struct {
	Query string `json:"query"`
	Scope string `json:"scope"`
	Limit int    `json:"limit"`
}

func (PromptLibrarySearchTool) Name() string { return PromptLibrarySearchName }

func (PromptLibrarySearchTool) Info() tool.Info {
	return tool.NewBuilder(PromptLibrarySearchName).
		Description("检索内置的 GPT Image 提示词库（GPT_Image_Prompts_大全）：场景条目、关键词词库、负面词、用途比例、进阶技巧。生图前可以用它查该配什么镜头/光线/画风。").
		Risk(tool.RiskLow).
		Tags("image").
		BuildInfo()
}

func (PromptLibrarySearchTool) Schema() llm.ToolSchema {
	return tool.NewBuilder(PromptLibrarySearchName).
		Description("检索内置生图提示词库。").
		String("query", "检索词，例如 cyberpunk、头像、水彩、构图。", tool.Required()).
		String("scope", "可选：entry、keyword、negative、preset、tip；不填检索全部。").
		Integer("limit", "返回条数，默认 5，最大 20。").
		BuildSchema()
}

func (PromptLibrarySearchTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	_ = ctx
	var args promptLibrarySearchArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse prompt_library_search arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Query) == "" {
		return &tool.Result{Content: "query 不能为空。"}, nil
	}
	library, err := imagegen.PromptLib()
	if err != nil {
		return &tool.Result{Content: "提示词库不可用：" + err.Error()}, nil
	}
	scopes := []string{}
	if scope := strings.TrimSpace(args.Scope); scope != "" {
		scopes = append(scopes, scope)
	}
	matches := library.Search(args.Query, scopes, args.Limit)
	if len(matches) == 0 {
		return &tool.Result{Content: "提示词库没有匹配结果。"}, nil
	}
	lines := []string{fmt.Sprintf("命中 %d 条：", len(matches))}
	for _, match := range matches {
		lines = append(lines, "- ["+match.Scope+"] "+match.Title)
		if len(match.Anchors) > 0 {
			lines = append(lines, "  anchors: "+truncatePromptLibLine(strings.Join(match.Anchors, ", "), 320))
		}
		if len(match.Terms) > 0 {
			lines = append(lines, "  terms: "+truncatePromptLibLine(strings.Join(match.Terms, ", "), 240))
		}
		if detail := strings.TrimSpace(match.Detail); detail != "" {
			lines = append(lines, "  "+truncatePromptLibLine(detail, 320))
		}
	}
	lines = append(lines, "把这些词条写进 image_generate 的 prompt，或直接让 image_generate 自动套用。")
	return &tool.Result{Content: strings.Join(lines, "\n")}, nil
}

func truncatePromptLibLine(text string, max int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if max <= 0 || len([]rune(text)) <= max {
		return text
	}
	return string([]rune(text)[:max]) + "..."
}
