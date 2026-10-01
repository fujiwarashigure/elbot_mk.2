package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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

const (
	maxImageCharacters    = 4
	maxImageReferences    = 4
	maxImageBatchCount    = 4
	maxImageBatchWorkers  = 4
	defaultImageBatchSize = 1
)

// ImageGenerateTool calls an OpenAI-compatible image endpoint and composes the
// final prompt from the global preset, all indexed character presets and the
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
	Prompt          string   `json:"prompt"`
	CharacterID     string   `json:"character_id"`
	CharacterIDs    []string `json:"character_ids"`
	Size            string   `json:"size"`
	Quality         string   `json:"quality"`
	ReferenceImage  string   `json:"reference_image"`
	ReferenceImages []string `json:"reference_images"`
	Count           int      `json:"count"`
	SaveToCharacter *bool    `json:"save_to_character"`
	Send            *bool    `json:"send"`
	Optimize        *bool    `json:"optimize"`
	TermMode        string   `json:"term_mode"`
	Profile         string   `json:"profile"`
	Mode            string   `json:"mode"`
	CharacterQuery  string   `json:"character_query"`
	ContextQuery    string   `json:"context_query"`
	ContextLimit    int      `json:"context_limit"`
	Rewrite         *bool    `json:"rewrite"`
}

func (ImageGenerateTool) Name() string { return ImageGenerateName }

func (t ImageGenerateTool) Info() tool.Info {
	builder := tool.NewBuilder(ImageGenerateName).
		Description("调用生图服务（OpenAI 兼容 images/generations，默认 GPT Image 2.5）生成图片。最终提示词 = 全局预设 + 所有已索引角色的图片预设 + 提示词优化后的场景描述；消息里用 @char:<id> 启用多个角色、或调用时传 character_ids，会自动把多个角色放进同一张图。mode=auto（默认）会从 prompt 自动识别多个角色名/别名、按需拉当前群聊上下文、并在 prompt 太短或含糊时用低成本模型做语义改写；内置提示词库会自动补充用途比例、画风锚点和负面词。除非显式传 count>1，否则一次调用只生成一张包含全部索引角色的图。").
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
		Description("生成图片。默认一张图包含所有已索引角色。").
		String("prompt", "画面/场景描述。不要重复角色外貌，角色预设会自动拼接。", tool.Required()).
		String("character_id", "可选单个角色 id；也支持 auto（按 character_query 或 prompt 自动选角）。").
		StringArray("character_ids", "可选多个角色 id；和本轮 @char:<id> 一起使用。除非传 count>1，否则所有角色会画进同一张图。").
		String("size", "可选尺寸，例如 1024x1024、1536x1024、1024x1536；不填用默认。").
		String("quality", "可选画质：low、medium、high；不填用默认。").
		String("reference_image", "可选单张参考图：media:<sha256> 或角色图片名（如 avatar.png）。多角色时可用 <character_id>:<图片名> 指定归属。需服务端支持。").
		StringArray("reference_images", "可选多张参考图，每张为 media:<sha256> 或 <character_id>:<图片名>；需服务端支持多图参考。").
		Integer("count", "可选，强制生成的图片张数，默认 1，单次最多 4。每一张都会包含全部已索引角色。").
		Boolean("save_to_character", "可选，是否把结果写回角色 images/；默认跟随配置。").
		Boolean("send", "可选，生成后是否直接发送到当前聊天；默认 false。").
		Boolean("optimize", "可选，是否用内置提示词库优化 prompt；默认跟随配置。").
		String("term_mode", "可选，phrase 或 tag；tag 会追加单个单词的 tag 串。默认跟随配置。").
		String("profile", "可选，生图配置 profile 名（[image_generation.profiles.<name>]）；不填时用本轮 @image: 声明的或默认 profile。").
		String("mode", "可选，auto（默认，自动选角/拉群聊上下文/按需改写）或 manual（只用显式参数）。").
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
	items, err := t.resolveImageCharacters(ctx, args, args.Prompt, viewer, auto)
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
			CharacterName:   imageCharacterNames(items),
			CharacterPrompt: imageCharacterPrompts(items),
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

	first := firstImageCharacter(items)
	size := firstNonEmptyString(args.Size, imageSetting(first, func(s character.ImageSettings) string { return s.Size }), t.cfg.Size)
	quality := firstNonEmptyString(args.Quality, imageSetting(first, func(s character.ImageSettings) string { return s.Quality }), t.cfg.Quality)

	references, err := t.collectReferences(ctx, args, items, viewer)
	if err != nil {
		return &tool.Result{Content: err.Error()}, nil
	}
	finalPrompt, err := t.composePrompt(items, scene, optimizerNegative, len(references) > 0)
	if err != nil {
		return &tool.Result{Content: err.Error()}, nil
	}
	request := imagegen.Request{
		Prompt:     finalPrompt,
		Size:       size,
		Quality:    quality,
		References: references,
	}

	count := args.Count
	if count <= 0 {
		count = defaultImageBatchSize
	}
	requestedCount := count
	if count > maxImageBatchCount {
		count = maxImageBatchCount
	}
	results, generationErrors := t.generateImages(ctx, request, count)

	saveToCharacter := t.cfg.SaveToCharacter
	if args.SaveToCharacter != nil {
		saveToCharacter = *args.SaveToCharacter
	}
	base := "image"
	if len(items) > 0 {
		base = items[0].ID
	}
	timestamp := time.Now().Format("20060102-150405")
	var (
		storedResults   []*imagegen.Result
		storedMedia     []*storage.Media
		storedNames     []string
		failures        []string
		writebackErrors []string
	)
	for i, result := range results {
		if result == nil {
			if i < len(generationErrors) && generationErrors[i] != nil {
				failures = append(failures, fmt.Sprintf("第 %d 张：%s", i+1, imageGenerateErrorMessage(generationErrors[i])))
			}
			continue
		}
		suffix := ""
		if count > 1 {
			suffix = fmt.Sprintf("-%02d", i+1)
		}
		name := safeFileName(fmt.Sprintf("%s-%s%s%s", base, timestamp, suffix, extForMIME(result.MIMEType)))
		stored, err := t.storeImage(ctx, result.Data, result.MIMEType, name)
		if err != nil {
			failures = append(failures, fmt.Sprintf("第 %d 张保存失败：%v", i+1, err))
			continue
		}
		storedResults = append(storedResults, result)
		storedMedia = append(storedMedia, stored)
		storedNames = append(storedNames, name)
		if saveToCharacter && len(items) > 0 && t.characters != nil && t.characters.Enabled() {
			if _, err := t.characters.AddImage(ctx, items[0].ID, name, result.MIMEType, stored.ID, result.Data, viewer); err != nil {
				writebackErrors = append(writebackErrors, fmt.Sprintf("%s：%v", name, err))
			}
		}
	}
	if len(storedMedia) == 0 {
		message := "生图失败。"
		if len(failures) > 0 {
			message += "\n" + strings.Join(failures, "\n")
		}
		return &tool.Result{Content: message}, nil
	}

	var summary strings.Builder
	if requestedCount > 1 {
		fmt.Fprintf(&summary, "已生成 %d/%d 张图片。\n", len(storedMedia), requestedCount)
	} else {
		summary.WriteString("已生成图片。\n")
	}
	if requestedCount > count {
		fmt.Fprintf(&summary, "单次最多生成 %d 张，本次已按上限执行。\n", maxImageBatchCount)
	}
	if len(items) > 0 {
		summary.WriteString("角色：" + imageCharacterSummary(items) + "\n")
	}
	if contextCount > 0 {
		fmt.Fprintf(&summary, "参考对话：%d 条\n", contextCount)
	}
	if rewriteModel != "" {
		summary.WriteString("语义改写：" + rewriteModel + "\n")
	}
	if len(references) > 0 {
		fmt.Fprintf(&summary, "参考图：%d 张\n", len(references))
	}
	firstResult := storedResults[0]
	fmt.Fprintf(&summary, "model: %s\nsize: %s\nprompt: %s", firstResult.Model, firstNonEmptyString(size, t.cfg.Size), previewText(finalPrompt))
	if revised := strings.TrimSpace(firstResult.RevisedPrompt); revised != "" {
		if optimizerMatch != "" {
			summary.WriteString("\noptimizer: " + optimizerMatch)
		}
		summary.WriteString("\nrevised_prompt: " + previewText(revised))
	}
	if len(failures) > 0 {
		summary.WriteString("\n失败：\n- " + strings.Join(failures, "\n- "))
	}
	if len(writebackErrors) > 0 {
		summary.WriteString("\n写回角色失败：\n- " + strings.Join(writebackErrors, "\n- "))
	}
	mediaIDs := make([]string, 0, len(storedMedia))
	for _, stored := range storedMedia {
		mediaIDs = append(mediaIDs, stored.ID)
	}
	summary.WriteString("\nmedia: " + strings.Join(mediaIDs, ", "))
	return t.imageResultBatch(storedResults, storedMedia, storedNames, summary.String(), args.Send)
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

func (t ImageGenerateTool) imageResultBatch(results []*imagegen.Result, stored []*storage.Media, names []string, summary string, send *bool) (*tool.Result, error) {
	out := &tool.Result{Content: summary}
	out.Segments = append(out.Segments, llm.MessageSegment{Type: llm.SegmentText, Text: summary})
	shouldSend := t.cfg.SendByDefault
	if send != nil {
		shouldSend = *send
	}
	for i := range stored {
		if i >= len(results) || i >= len(names) || stored[i] == nil || results[i] == nil {
			continue
		}
		out.Segments = append(out.Segments, llm.MessageSegment{
			Type:     llm.SegmentImage,
			MediaID:  stored[i].ID,
			Name:     names[i],
			MIMEType: results[i].MIMEType,
		})
		if shouldSend {
			out.Outputs = append(out.Outputs, delivery.Output{
				Kind:   delivery.KindImage,
				Name:   names[i],
				Source: delivery.Source{MediaID: stored[i].ID, MIMEType: results[i].MIMEType},
			})
		}
	}
	return out, nil
}

func (t ImageGenerateTool) generateImages(ctx context.Context, request imagegen.Request, count int) ([]*imagegen.Result, []error) {
	if count <= 1 {
		result, err := t.generateOne(ctx, request)
		return []*imagegen.Result{result}, []error{err}
	}
	results := make([]*imagegen.Result, count)
	errs := make([]error, count)
	workers := min(count, maxImageBatchWorkers)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i], errs[i] = t.generateOne(ctx, request)
			}
		}()
	}
dispatch:
	for i := 0; i < count; i++ {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	for i := 0; i < count; i++ {
		if errs[i] == nil && results[i] == nil {
			errs[i] = ctx.Err()
			if errs[i] == nil {
				errs[i] = context.Canceled
			}
		}
	}
	return results, errs
}

func (t ImageGenerateTool) generateOne(ctx context.Context, request imagegen.Request) (*imagegen.Result, error) {
	if t.limiter != nil {
		release, err := t.limiter.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	return t.client.Generate(ctx, request)
}

func imageGenerateErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, concurrency.ErrFull),
		errors.Is(err, concurrency.ErrTimeout),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return imageConcurrencyMessage(err)
	default:
		return err.Error()
	}
}

func (t ImageGenerateTool) composePrompt(items []*character.Character, scene, optimizerNegative string, referenceUsed bool) (string, error) {
	parts := []string{}
	if preset := strings.TrimSpace(t.cfg.PresetPrompt); preset != "" {
		parts = append(parts, preset)
	}
	if len(items) > 1 {
		for _, item := range items {
			if item == nil {
				continue
			}
			preset := strings.TrimSpace(item.ImagePrompt())
			if preset == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("角色 %s（%s）：\n%s", item.Name, item.ID, preset))
		}
	} else if len(items) == 1 && items[0] != nil {
		if preset := strings.TrimSpace(items[0].ImagePrompt()); preset != "" {
			parts = append(parts, preset)
		}
	}
	if referenceUsed {
		if len(items) > 1 {
			parts = append(parts, "参考图中的每个角色都要与对应角色设定一致；保持各自的脸、发型、服装和配色独立，不要串角色。")
		} else {
			parts = append(parts, "Keep face, hairstyle, outfit, and overall style consistent with the reference image")
		}
	}
	parts = append(parts, strings.TrimSpace(scene))
	if len(items) > 1 {
		parts = append(parts, multiCharacterConstraint(items))
	}
	negative := []string{}
	if value := strings.TrimSpace(optimizerNegative); value != "" {
		negative = append(negative, value)
	}
	if value := strings.TrimSpace(t.cfg.NegativePrompt); value != "" {
		negative = append(negative, value)
	}
	for _, item := range items {
		if item != nil && strings.TrimSpace(item.Image.NegativePrompt) != "" {
			negative = append(negative, strings.TrimSpace(item.Image.NegativePrompt))
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

func multiCharacterConstraint(items []*character.Character) string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		names = append(names, fmt.Sprintf("%s(%s)", item.Name, item.ID))
	}
	return "画面必须同时出现以下全部角色：" + strings.Join(names, "、") + "。保持每个角色的脸、发型、服装、配色和特征独立，不要遗漏、合并、替换或交换角色。"
}

type imageReferenceSource struct {
	source string
	item   *character.Character
}

func (t ImageGenerateTool) collectReferences(ctx context.Context, args imageGenerateArgs, items []*character.Character, viewer character.Viewer) ([]imagegen.Reference, error) {
	sources, err := t.referenceSources(args, items)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, nil
	}
	if !t.cfg.SupportsReference {
		return nil, fmt.Errorf("当前生图服务未开启参考图支持（image_generation.supports_reference=false）")
	}
	if len(sources) > maxImageReferences {
		sources = sources[:maxImageReferences]
	}
	out := make([]imagegen.Reference, 0, len(sources))
	for _, source := range sources {
		data, mimeType, err := t.referenceBytesFromSource(ctx, source.source, source.item, items, viewer)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		out = append(out, imagegen.Reference{Data: data, MIMEType: mimeType})
	}
	return out, nil
}

func (t ImageGenerateTool) referenceSources(args imageGenerateArgs, items []*character.Character) ([]imageReferenceSource, error) {
	var sources []imageReferenceSource
	seen := map[string]bool{}
	first := firstImageCharacter(items)
	add := func(source string, item *character.Character) {
		source = strings.TrimSpace(source)
		if source == "" || seen[source] {
			return
		}
		seen[source] = true
		sources = append(sources, imageReferenceSource{source: source, item: item})
	}
	add(args.ReferenceImage, first)
	for _, value := range args.ReferenceImages {
		add(value, first)
	}
	if len(sources) > 0 {
		return sources, nil
	}
	for _, item := range items {
		if len(sources) >= maxImageReferences {
			break
		}
		if item != nil && len(item.Image.References) > 0 {
			add(item.Image.References[0], item)
		}
	}
	return sources, nil
}

func (t ImageGenerateTool) referenceBytesFromSource(ctx context.Context, source string, item *character.Character, items []*character.Character, viewer character.Viewer) ([]byte, string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, "", nil
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
	target := item
	imageName := source
	// 支持 character_id:图片名，多角色时用于明确参考图归属。
	if id, name, ok := strings.Cut(source, ":"); ok && t.characters != nil && t.characters.Enabled() {
		if candidate, err := t.characters.GetVisible(ctx, id, viewer); err == nil {
			target = candidate
			imageName = name
		}
	}
	if target == nil && len(items) > 0 {
		target = items[0]
	}
	if target == nil {
		return nil, "", fmt.Errorf("使用角色参考图需要指定 character_id 或先用 @char:<id> 启用角色")
	}
	if t.characters == nil || !t.characters.Enabled() {
		return nil, "", fmt.Errorf("角色素材库未配置")
	}
	data, mimeType, err := t.characters.ReadImage(ctx, target.ID, imageName, viewer)
	if err != nil {
		return nil, "", fmt.Errorf("读取角色图片 %q 失败：%w", imageName, err)
	}
	return data, mimeType, nil
}

func firstImageCharacter(items []*character.Character) *character.Character {
	for _, item := range items {
		if item != nil {
			return item
		}
	}
	return nil
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
