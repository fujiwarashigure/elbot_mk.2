package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"elbot/internal/character"
	"elbot/internal/delivery"
	"elbot/internal/imagegen"
	"elbot/internal/llm"
	"elbot/internal/media"
	"elbot/internal/ops/concurrency"
	"elbot/internal/storage"
	"elbot/internal/tool"
)

const ImageGenerateName = "image_generate"

// ImageGenerateTool calls an OpenAI-compatible image endpoint and composes the
// final prompt from the global preset, the active character preset and the
// caller supplied scene description.
// ImageProfile is one named image generation endpoint.
type ImageProfile struct {
	Name   string
	Client *imagegen.Client
	Config imagegen.Config
}

type ImageGenerateTool struct {
	profiles       map[string]ImageProfile
	defaultProfile string
	client         *imagegen.Client
	characters     *character.Store
	media          *media.Manager
	history        storage.ChatHistoryRepository
	rewriter       ImagePromptRewriter
	cfg            imagegen.Config
	limiter        *concurrency.Limiter
}

func NewImageGenerateTool(profiles map[string]ImageProfile, defaultProfile string, characters *character.Store, center *media.Manager, history storage.ChatHistoryRepository, rewriter ImagePromptRewriter, limiter *concurrency.Limiter) ImageGenerateTool {
	instance := ImageGenerateTool{
		profiles:       profiles,
		defaultProfile: strings.TrimSpace(defaultProfile),
		characters:     characters,
		media:          center,
		history:        history,
		rewriter:       rewriter,
	}
	if profile, ok := profiles[instance.defaultProfile]; ok {
		instance.client = profile.Client
		instance.cfg = profile.Config
	}
	instance.limiter = limiter
	return instance
}

type imageGenerateArgs struct {
	Prompt          string `json:"prompt"`
	CharacterID     string `json:"character_id"`
	Size            string `json:"size"`
	Quality         string `json:"quality"`
	ReferenceImage  string `json:"reference_image"`
	SaveToCharacter *bool  `json:"save_to_character"`
	Send            *bool  `json:"send"`
	Optimize        *bool  `json:"optimize"`
	TermMode        string `json:"term_mode"`
	Profile         string `json:"profile"`
	Mode            string `json:"mode"`
	CharacterQuery  string `json:"character_query"`
	ContextQuery    string `json:"context_query"`
	ContextLimit    int    `json:"context_limit"`
	Rewrite         *bool  `json:"rewrite"`
}

func (ImageGenerateTool) Name() string { return ImageGenerateName }

func (t ImageGenerateTool) Info() tool.Info {
	builder := tool.NewBuilder(ImageGenerateName).
		Description("调用生图服务（OpenAI 兼容 images/generations，默认 GPT Image 2.5）生成图片。最终提示词 = 全局预设 + 角色图片预设 + 提示词优化后的场景描述；消息里先用 @char:<id> 启用角色时，会自动带上该角色的图片预设和参考图。mode=auto（默认）会从 prompt 自动识别角色名/别名、按需拉当前群聊上下文、并在 prompt 太短或含糊时用低成本模型做语义改写；内置提示词库会自动补充用途比例、画风锚点和负面词。").
		Risk(tool.RiskMedium).
		Tags("image", "character").
		DependsOn(CharacterReadName, "send_file", PromptLibrarySearchName, "search_chat_history")
	if t.cfg.SuperadminOnly {
		builder = builder.SuperadminOnly()
	}
	return builder.BuildInfo()
}

func (t ImageGenerateTool) Schema() llm.ToolSchema {
	builder := tool.NewBuilder(ImageGenerateName).
		Description("生成图片。").
		String("prompt", "画面/场景描述。不要重复角色外貌，角色预设会自动拼接。", tool.Required()).
		String("character_id", "可选角色 id；不填时使用本轮 @char:<id> 启用的角色。").
		String("size", "可选尺寸，例如 1024x1024、1536x1024、1024x1536；不填用默认。").
		String("quality", "可选画质：low、medium、high；不填用默认。").
		String("reference_image", "可选参考图：media:<sha256> 或角色图片名（如 avatar.png）。需服务端支持。").
		Boolean("save_to_character", "可选，是否把结果写回角色 images/；默认跟随配置。").
		Boolean("send", "可选，生成后是否直接发送到当前聊天；默认 false。").
		Boolean("optimize", "可选，是否用内置提示词库优化 prompt；默认跟随配置。").
		String("term_mode", "可选，phrase 或 tag；tag 会追加单个单词的 tag 串。默认跟随配置。").
		String("profile", "可选，生图配置 profile 名（[image_generation.profiles.<name>]）；不填时用本轮 @image: 声明的或默认 profile。").
		String("mode", "可选，auto（默认，自动选角/拉群聊上下文/按需改写）或 manual（只用显式参数）。").
		String("character_id", "可选角色 id；也支持 auto（按 character_query 或 prompt 自动选角）。").
		String("character_query", "可选，自动选角时的检索词；不填时用 prompt。").
		String("context_query", "可选，拉取当前群聊上下文的关键词；auto 表示从 prompt 自动提取，off 关闭。").
		Integer("context_limit", "可选，拉取群聊消息条数，默认 6。").
		Boolean("rewrite", "可选，是否用低成本模型做语义改写；默认跟随配置。").
		BuildSchema()
	return builder
}

func (t ImageGenerateTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	var args imageGenerateArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse image_generate arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return &tool.Result{Content: "prompt 不能为空。"}, nil
	}
	if len(t.profiles) > 0 {
		profileName := strings.TrimSpace(args.Profile)
		if profileName == "" {
			profileName = imagegen.ProfileFrom(ctx)
		}
		if profileName == "" {
			profileName = t.defaultProfile
		}
		profile, ok := t.profiles[profileName]
		if !ok {
			return &tool.Result{Content: "没有配置生图 profile：" + profileName}, nil
		}
		// The default profile is already gated by Info().SuperadminOnly at the
		// executor; only extra profiles need the in-call check.
		if profileName != t.defaultProfile && profile.Config.SuperadminOnly && !characterViewer(ctx).Superadmin {
			return &tool.Result{Content: "生图 profile " + profileName + " 仅超级管理员可用。"}, nil
		}
		t.client = profile.Client
		t.cfg = profile.Config
	}
	if t.client == nil || !t.client.Enabled() {
		return &tool.Result{Content: "生图服务未启用。请在 app.toml 配置 [image_generation] 并设置 API Key。"}, nil
	}
	viewer := characterViewer(ctx)
	mode := strings.ToLower(strings.TrimSpace(args.Mode))
	if mode == "" {
		mode = "auto"
	}
	auto := mode == "auto"
	item, err := t.resolveImageCharacter(ctx, args, args.Prompt, viewer, auto)
	if err != nil {
		return &tool.Result{Content: err.Error()}, nil
	}

	contextText := ""
	contextCount := 0
	if query, want := t.imageContextQuery(args, args.Prompt, auto); want {
		if rows := t.searchImageContext(ctx, query, args.ContextLimit); len(rows) > 0 {
			contextText = formatImageContext(rows)
			contextCount = len(rows)
		}
	}

	scene := strings.TrimSpace(args.Prompt)
	rewriteModel := ""
	if t.shouldRewriteImagePrompt(args, scene, contextText) && t.rewriter != nil {
		hints := ""
		if library, err := imagegen.PromptLib(); err == nil {
			hints = library.LibraryHints(scene, 3)
		}
		if value, err := t.rewriter.RewriteImagePrompt(ctx, ImagePromptRewriteRequest{
			Scene:           scene,
			CharacterName:   characterName(item),
			CharacterPrompt: characterImagePrompt(item),
			ContextText:     contextText,
			LibraryHints:    hints,
			MaxRunes:        t.cfg.MaxPromptRunes,
		}); err == nil && strings.TrimSpace(value) != "" {
			scene = strings.TrimSpace(value)
			rewriteModel = t.cfg.OptimizeRewriteModel
		}
	}
	if contextText != "" && rewriteModel == "" {
		scene = strings.TrimSpace(scene + "\n\n" + contextText)
	}

	optimizerNegative := ""
	optimizerMatch := ""
	optimize := strings.EqualFold(strings.TrimSpace(t.cfg.Optimize), "rules")
	if args.Optimize != nil {
		optimize = *args.Optimize
	}
	if optimize {
		if library, err := imagegen.PromptLib(); err == nil {
			termMode := t.cfg.OptimizeTermMode
			if strings.TrimSpace(args.TermMode) != "" {
				termMode = args.TermMode
			}
			optimized := library.Optimize(scene, imagegen.OptimizeOptions{
				MaxAnchors:    t.cfg.OptimizeMaxAnchors,
				MaxNegatives:  t.cfg.OptimizeMaxNegatives,
				MaxAddedRunes: t.cfg.OptimizeMaxAddedRunes,
				MaxTags:       t.cfg.OptimizeMaxTags,
				TermMode:      termMode,
			})
			scene = optimized.Prompt
			optimizerNegative = optimized.Negative
			optimizerMatch = optimized.Matched
		}
	}

	size := firstNonEmptyString(args.Size, imageSetting(item, func(s character.ImageSettings) string { return s.Size }), t.cfg.Size)
	quality := firstNonEmptyString(args.Quality, imageSetting(item, func(s character.ImageSettings) string { return s.Quality }), t.cfg.Quality)

	request := imagegen.Request{
		Prompt:  scene,
		Size:    size,
		Quality: quality,
	}
	referenceUsed := false
	if strings.TrimSpace(args.ReferenceImage) != "" || (item != nil && len(item.Image.References) > 0) {
		data, mimeType, err := t.referenceBytes(ctx, args.ReferenceImage, item, viewer)
		if err != nil {
			return &tool.Result{Content: err.Error()}, nil
		}
		request.ReferenceData = data
		request.ReferenceMIME = mimeType
		referenceUsed = len(data) > 0
	}
	finalPrompt, err := t.composePrompt(item, request.Prompt, optimizerNegative, referenceUsed)
	if err != nil {
		return &tool.Result{Content: err.Error()}, nil
	}
	request.Prompt = finalPrompt

	if t.limiter != nil {
		release, err := t.limiter.Acquire(ctx)
		if err != nil {
			return &tool.Result{Content: imageConcurrencyMessage(err)}, nil
		}
		defer release()
	}
	result, err := t.client.Generate(ctx, request)
	if err != nil {
		return &tool.Result{Content: "生图失败：" + err.Error()}, nil
	}

	base := "image"
	if item != nil {
		base = item.ID
	}
	name := safeFileName(fmt.Sprintf("%s-%s%s", base, time.Now().Format("20060102-150405"), extForMIME(result.MIMEType)))
	stored, err := t.storeImage(ctx, result.Data, result.MIMEType, name)
	if err != nil {
		return &tool.Result{Content: "生图成功但保存失败：" + err.Error()}, nil
	}

	saveToCharacter := t.cfg.SaveToCharacter
	if args.SaveToCharacter != nil {
		saveToCharacter = *args.SaveToCharacter
	}
	if saveToCharacter && item != nil && t.characters != nil && t.characters.Enabled() {
		if _, err := t.characters.AddImage(ctx, item.ID, name, result.MIMEType, stored.ID, result.Data, viewer); err != nil {
			// Generation succeeded; report the writeback failure without failing the call.
			return t.imageResult(result, stored, name, fmt.Sprintf("（写回角色失败：%v）", err), args.Send)
		}
	}

	characterText := ""
	if item != nil {
		characterText = "角色：" + item.Name + "(" + item.ID + ")\n"
		if contextCount > 0 {
			characterText += fmt.Sprintf("参考对话：%d 条\n", contextCount)
		}
		if rewriteModel != "" {
			characterText += "语义改写：" + rewriteModel + "\n"
		}
	}
	summary := fmt.Sprintf("已生成图片。\n%smodel: %s\nsize: %s\nprompt: %s",
		characterText, result.Model, firstNonEmptyString(size, t.cfg.Size), previewText(finalPrompt))
	if strings.TrimSpace(result.RevisedPrompt) != "" {
		if optimizerMatch != "" {
			summary += "\noptimizer: " + optimizerMatch
		}
		summary += "\nrevised_prompt: " + previewText(result.RevisedPrompt)
	}
	summary += "\nmedia: " + stored.ID
	return t.imageResult(result, stored, name, summary, args.Send)
}

func imageConcurrencyMessage(err error) string {
	switch {
	case errors.Is(err, concurrency.ErrFull):
		return "生图服务繁忙，请稍后重试。"
	case errors.Is(err, concurrency.ErrTimeout):
		return "生图服务排队超时，请稍后重试。"
	case errors.Is(err, context.Canceled):
		return "生图已取消。"
	case errors.Is(err, context.DeadlineExceeded):
		return "生图排队超时，请稍后重试。"
	default:
		return "生图服务暂时不可用，请稍后重试。"
	}
}

func (t ImageGenerateTool) imageResult(result *imagegen.Result, stored *storage.Media, name, summary string, send *bool) (*tool.Result, error) {
	out := &tool.Result{
		Content: summary,
		Segments: []llm.MessageSegment{
			{Type: llm.SegmentText, Text: summary},
			{Type: llm.SegmentImage, MediaID: stored.ID, Name: name, MIMEType: result.MIMEType},
		},
	}
	shouldSend := t.cfg.SendByDefault
	if send != nil {
		shouldSend = *send
	}
	if shouldSend {
		out.Outputs = []delivery.Output{{
			Kind:   delivery.KindImage,
			Name:   name,
			Source: delivery.Source{MediaID: stored.ID, MIMEType: result.MIMEType},
		}}
	}
	return out, nil
}

func (t ImageGenerateTool) resolveCharacter(ctx context.Context, explicit string, viewer character.Viewer) (*character.Character, error) {
	if t.characters == nil || !t.characters.Enabled() {
		return nil, nil
	}
	id := strings.TrimSpace(explicit)
	if id == "" {
		active := character.ActiveIDs(ctx)
		if len(active) > 0 {
			id = active[0]
		}
	}
	if id == "" {
		return nil, nil
	}
	item, err := t.characters.GetVisible(ctx, id, viewer)
	if err != nil {
		if err == character.ErrNotFound {
			return nil, fmt.Errorf("没有找到角色 %q，可先用 character_list 查询。", id)
		}
		if err == character.ErrForbidden {
			return nil, fmt.Errorf("角色 %q 不可访问。", id)
		}
		return nil, err
	}
	return item, nil
}

func (t ImageGenerateTool) composePrompt(item *character.Character, scene, optimizerNegative string, referenceUsed bool) (string, error) {
	parts := []string{}
	if preset := strings.TrimSpace(t.cfg.PresetPrompt); preset != "" {
		parts = append(parts, preset)
	}
	if item != nil {
		if preset := strings.TrimSpace(item.ImagePrompt()); preset != "" {
			parts = append(parts, preset)
		}
	}
	if referenceUsed {
		parts = append(parts, "Keep face, hairstyle, outfit, and overall style consistent with the reference image")
	}
	parts = append(parts, strings.TrimSpace(scene))
	negative := []string{}
	if value := strings.TrimSpace(optimizerNegative); value != "" {
		negative = append(negative, value)
	}
	if value := strings.TrimSpace(t.cfg.NegativePrompt); value != "" {
		negative = append(negative, value)
	}
	if item != nil {
		if value := strings.TrimSpace(item.Image.NegativePrompt); value != "" {
			negative = append(negative, value)
		}
	}
	if len(negative) > 0 {
		parts = append(parts, "avoid: "+strings.Join(negative, ", "))
	}
	prompt := strings.TrimSpace(strings.Join(parts, "\n\n"))
	limit := t.cfg.MaxPromptRunes
	if limit <= 0 {
		limit = 4000
	}
	if len([]rune(prompt)) > limit {
		prompt = string([]rune(prompt)[:limit])
	}
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt 为空")
	}
	return prompt, nil
}

func (t ImageGenerateTool) referenceBytes(ctx context.Context, explicit string, item *character.Character, viewer character.Viewer) ([]byte, string, error) {
	source := strings.TrimSpace(explicit)
	if source == "" && item != nil && len(item.Image.References) > 0 {
		source = strings.TrimSpace(item.Image.References[0])
	}
	if source == "" {
		return nil, "", nil
	}
	if !t.cfg.SupportsReference {
		return nil, "", fmt.Errorf("当前生图服务未开启参考图支持（image_generation.supports_reference=false）")
	}
	if strings.HasPrefix(source, media.IDPrefix) {
		if !media.ValidID(source) {
			return nil, "", fmt.Errorf("参考图 media id 不合法")
		}
		if t.media == nil {
			return nil, "", fmt.Errorf("Media Center 未配置")
		}
		data, meta, err := t.media.Read(ctx, source)
		if err != nil {
			return nil, "", fmt.Errorf("读取参考图失败：%w", err)
		}
		return data, meta.MIMEType, nil
	}
	if item == nil {
		return nil, "", fmt.Errorf("使用参考图需要指定 character_id 或先用 @char:<id> 启用角色")
	}
	if t.characters == nil || !t.characters.Enabled() {
		return nil, "", fmt.Errorf("角色素材库未配置")
	}
	data, mimeType, err := t.characters.ReadImage(ctx, item.ID, source, viewer)
	if err != nil {
		return nil, "", fmt.Errorf("读取角色图片 %q 失败：%w", source, err)
	}
	return data, mimeType, nil
}

func (t ImageGenerateTool) storeImage(ctx context.Context, data []byte, mimeType, name string) (*storage.Media, error) {
	if t.media == nil {
		return nil, fmt.Errorf("Media Center 未配置")
	}
	return t.media.ImportReader(ctx, bytes.NewReader(data), int64(len(data)), media.Input{Name: name, MIMEType: mimeType})
}

func imageSetting(item *character.Character, pick func(character.ImageSettings) string) string {
	if item == nil {
		return ""
	}
	return pick(item.Image)
}

func extForMIME(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func previewText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) <= 200 {
		return text
	}
	return string([]rune(text)[:200]) + "..."
}
