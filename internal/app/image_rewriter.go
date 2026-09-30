package app

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/storage"
	"elbot/internal/tool/builtin"
)

// imagePromptRewriter performs the optional LLM rewrite pass for image_generate.
type imagePromptRewriter struct {
	client llm.LLM
	model  string
}

// buildImagePromptRewriter picks the rewrite model slot and returns nil when it
// is not configured, so image generation falls back to the rules optimizer.
func buildImagePromptRewriter(cfg *config.Config, models ModelClients) builtin.ImagePromptRewriter {
	if cfg == nil {
		return nil
	}
	selection := imageRewriteSelection(cfg)
	client := models.ByProvider[selection.Provider]
	if client == nil || strings.TrimSpace(selection.Model) == "" {
		return nil
	}
	return imagePromptRewriter{client: client, model: selection.Model}
}

func imageRewriteSelection(cfg *config.Config) config.ModelSelection {
	work := cfg.ModeModels[storage.SessionModeWork]
	fallback := func(selection config.ModelSelection) config.ModelSelection {
		if strings.TrimSpace(selection.Provider) == "" || strings.TrimSpace(selection.Model) == "" {
			return work
		}
		return selection
	}
	switch strings.ToLower(strings.TrimSpace(cfg.ImageGeneration.OptimizeRewriteModel)) {
	case "naming":
		return fallback(cfg.NamingModel)
	case "compact":
		return fallback(cfg.CompactModel)
	case "chat":
		return fallback(cfg.ModeModels[storage.SessionModeChat])
	case "work":
		return work
	default:
		return work
	}
}

const imageRewriteSystemPrompt = `你是生图提示词工程师。把用户给的画面描述改写成一段更适合 GPT Image 2.5 的完整提示词。
要求：
- 只输出提示词本身，不要解释、不要 markdown 代码块、不要引号；
- 严格保留用户原意、主体和角色身份，不新增无关元素；
- 优先自然语言描述，可补充镜头、光线、构图、材质、画质；
- 如果给了角色设定，保持外貌、服装、风格一致；
- 如果给了参考对话，从中提取关键场景信息，不要照抄无关闲聊；
- 语言以英文为主，专有名词可以保留中文；
- 长度不超过给定上限。`

func (r imagePromptRewriter) RewriteImagePrompt(ctx context.Context, req builtin.ImagePromptRewriteRequest) (string, error) {
	if r.client == nil || strings.TrimSpace(r.model) == "" {
		return "", fmt.Errorf("rewrite model is not configured")
	}
	maxRunes := req.MaxRunes
	if maxRunes <= 0 {
		maxRunes = 4000
	}
	var prompt strings.Builder
	prompt.WriteString("[场景描述]\n" + strings.TrimSpace(req.Scene))
	if strings.TrimSpace(req.CharacterName) != "" || strings.TrimSpace(req.CharacterPrompt) != "" {
		prompt.WriteString("\n\n[角色]\n" + strings.TrimSpace(req.CharacterName))
		if value := strings.TrimSpace(req.CharacterPrompt); value != "" {
			prompt.WriteString("\n" + value)
		}
	}
	if value := strings.TrimSpace(req.ContextText); value != "" {
		prompt.WriteString("\n\n" + value)
	}
	if value := strings.TrimSpace(req.LibraryHints); value != "" {
		prompt.WriteString("\n\n[TAG 库参考]\n" + value)
	}
	prompt.WriteString(fmt.Sprintf("\n\n[输出]\n直接输出改写后的提示词，不超过 %d 个字符。", maxRunes))

	stream, err := r.client.ChatStream(ctx, llm.ChatRequest{
		Model: r.model,
		Messages: []llm.LLMMessage{
			{Role: llm.RoleSystem, Segments: llm.TextSegments(imageRewriteSystemPrompt)},
			{Role: llm.RoleUser, Segments: llm.TextSegments(prompt.String())},
		},
		Temperature: 0.4,
		MaxTokens:   700,
	})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for chunk := range stream {
		if chunk.Error != nil {
			return "", chunk.Error
		}
		out.WriteString(chunk.DeltaContent)
	}
	value := strings.TrimSpace(out.String())
	if value == "" {
		return "", fmt.Errorf("rewrite returned an empty result")
	}
	return value, nil
}
