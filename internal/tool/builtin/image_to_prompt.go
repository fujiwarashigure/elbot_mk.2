package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"elbot/internal/llm"
	"elbot/internal/media"
	"elbot/internal/tool"
	"elbot/internal/vision"
)

// ImageToPromptName is the built-in tool that reverse-engineers a drawing
// prompt from a reference image.
const ImageToPromptName = "image_to_prompt"

// ImagePromptService is the shared image-description engine behind the tool.
// internal/vision.Service implements it; the tool deliberately owns no cache,
// no coalescing and no transport details of its own, so the built-in tool and
// the automatic chat fallback share one engine, one cache and one set of
// timeout/credential rules.
type ImagePromptService interface {
	Describe(ctx context.Context, req vision.Request) (vision.Result, error)
}

type imageToPromptArgs struct {
	Image    string `json:"image"`
	Target   string `json:"target"`
	Language string `json:"language"`
}

// ImageToPromptTool reads one stored image and delegates to a vision model.
type ImageToPromptTool struct {
	media   *media.Manager
	service ImagePromptService
}

func NewImageToPromptTool(center *media.Manager, service ImagePromptService) ImageToPromptTool {
	return ImageToPromptTool{media: center, service: service}
}

func (ImageToPromptTool) Name() string { return ImageToPromptName }

func imageToPromptBuilder() *tool.Builder {
	return tool.NewBuilder(ImageToPromptName).
		Description("把参考图片反推成可直接用于绘图的提示词，覆盖主体、外观、服装、姿势、构图、背景、光线、色彩与画风。只在用户要求反推图片提示词、分析参考图或生成绘图 prompt 时调用；不猜测人物身份，也不补充图片里不存在的细节。").
		Risk(tool.RiskLow).
		Tags("image", "prompt")
}

func (ImageToPromptTool) Info() tool.Info { return imageToPromptBuilder().BuildInfo() }

func (ImageToPromptTool) Schema() llm.ToolSchema {
	return tool.NewBuilder(ImageToPromptName).
		Description("把参考图片反推成绘图提示词。").
		String("image", "参考图片的媒体 ID，格式 media:<sha256>；取当前消息里 [图片 N；媒体 ID：media:...] 的 ID。", tool.Required()).
		String("target", "提示词面向的绘图模型：general（默认，通用）、sdxl、flux。", tool.Enum("general", "sdxl", "flux")).
		String("language", "提示词语言：zh（默认，中文）、en（英文）。", tool.Enum("zh", "en")).
		BuildSchema()
}

func (t ImageToPromptTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	var args imageToPromptArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse image_to_prompt arguments: %w", err)
		}
	}
	imageID := strings.TrimSpace(args.Image)
	if imageID == "" {
		return &tool.Result{Content: "image 不能为空，请传入 media:<sha256> 媒体 ID。"}, nil
	}
	if !media.ValidID(imageID) {
		return &tool.Result{Content: "image 必须是 media:<sha256> 格式的媒体 ID。"}, nil
	}
	if t.service == nil {
		return &tool.Result{Content: "图片反推提示词未配置视觉模型，请在 services.toml 的 [image_to_prompt] 设置 provider 和 model。"}, nil
	}
	if t.media == nil {
		return &tool.Result{Content: "Media Center 未配置，无法读取图片。"}, nil
	}
	target := vision.NormalizeImagePromptTarget(args.Target)
	language := vision.NormalizeImagePromptLanguage(args.Language)

	data, meta, err := t.media.ReadLimited(ctx, imageID, media.VisionImageInputLimit)
	if err != nil {
		if errors.Is(err, media.ErrMediaTooLarge) {
			return &tool.Result{Content: fmt.Sprintf("参考图片过大，超过 %d MiB 处理上限", media.VisionImageInputLimit>>20)}, nil
		}
		return &tool.Result{Content: "读取图片失败：媒体不存在或已过期"}, nil
	}
	mimeType := ""
	if meta != nil {
		mimeType = meta.MIMEType
	}
	// The service owns the whole pipeline from here on: preprocessing, prompt
	// rendering, the upstream call, the cache key and the coalescing layer. The
	// prompt template only depends on the normalized target/language, so two
	// requests that differ in either are automatically distinct cache entries.
	result, err := t.service.Describe(ctx, vision.Request{
		MediaID:  imageID,
		Data:     data,
		MIMEType: mimeType,
		Prompt:   vision.ImagePrompt(target, language),
	})
	if err != nil {
		return imageToPromptErrorResult(fmt.Errorf("反推提示词失败：%w", err))
	}
	return &tool.Result{Content: result.Text}, nil
}

// imageToPromptErrorResult maps internal errors to the tool result convention.
// Cancellation and deadline errors are surfaced as real call failures so the
// caller can tell them apart from a usable model response. Transient failures
// are never cached by the service, so the next call retries them.
func imageToPromptErrorResult(err error) (*tool.Result, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return &tool.Result{Content: err.Error()}, nil
}
