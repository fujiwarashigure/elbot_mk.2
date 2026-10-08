package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"elbot/internal/delivery"
	"elbot/internal/llm"
	"elbot/internal/media"
	"elbot/internal/platform"
	"elbot/internal/security"
	"elbot/internal/storage"
	"elbot/internal/tool"
)

// ViewImageName is the built-in tool that feeds an image back into the model.
const ViewImageName = "view_image"

// ViewImageTool hands one image to the model as a real image segment instead of
// only describing where it lives. It is hidden from models that cannot accept
// image input (tool.Info.VisionRequired).
type ViewImageTool struct {
	history storage.ChatHistoryRepository
	center  *media.Manager
}

type viewImageArgs struct {
	Source     string   `json:"source"`
	MessageIDs []string `json:"message_id"`
	MediaIndex [][]int  `json:"media_index"`
}

func NewViewImageTool(history storage.ChatHistoryRepository, center *media.Manager) ViewImageTool {
	return ViewImageTool{history: history, center: center}
}

func (ViewImageTool) Name() string { return ViewImageName }

func viewImageBuilder() *tool.Builder {
	return tool.NewBuilder(ViewImageName).
		Description("查看图片，把图片本身交给模型，而不是只给地址。source 与 message_id 二选一：source 传媒体 ID、HTTP(S) URL 或本地路径；message_id 传当前聊天的消息 ID 数组（可带 #），按 media_index 选择该消息里的第几张图片。已经看过的图片不要重复查看。").
		VisionRequired().
		Risk(tool.RiskLow).
		Tags("chat", "media").
		String("source", "媒体 ID（media:<sha256>）、HTTP(S) URL 或本地路径；本地路径仅超级管理员可用。").
		StringArray("message_id", "当前聊天的平台消息 ID 数组，可带 #。")
}

func (ViewImageTool) Info() tool.Info { return viewImageBuilder().BuildInfo() }

func (ViewImageTool) Schema() llm.ToolSchema {
	schema := viewImageBuilder().BuildSchema()
	properties := schema.Function.Parameters["properties"].(map[string]any)
	properties["message_id"].(map[string]any)["minItems"] = 1
	properties["media_index"] = map[string]any{
		"type":        "array",
		"description": "与 message_id 逐项对应的图片序号数组，从 1 开始，只能选图片；省略时每条消息取首张图片。",
		"items":       map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "integer", "minimum": 1}},
	}
	return schema
}

func parseViewImageArgs(req tool.CallRequest) (viewImageArgs, error) {
	var args viewImageArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return args, fmt.Errorf("parse view_image arguments: %w", err)
		}
	}
	args.Source = strings.TrimSpace(args.Source)
	if (args.Source != "") == (len(args.MessageIDs) > 0) {
		return args, fmt.Errorf("source 与 message_id 必须二选一")
	}
	if args.Source != "" {
		if args.MediaIndex != nil || args.MessageIDs != nil {
			return args, fmt.Errorf("source 不能与消息参数同时使用")
		}
		return args, nil
	}
	if args.MediaIndex != nil && len(args.MediaIndex) != len(args.MessageIDs) {
		return args, fmt.Errorf("media_index 必须与 message_id 逐项对应")
	}
	for i, id := range args.MessageIDs {
		args.MessageIDs[i] = parseChatHistoryMessageID(id)
		if args.MessageIDs[i] == "" {
			return args, fmt.Errorf("message_id 不能为空")
		}
		if args.MediaIndex != nil {
			if len(args.MediaIndex[i]) == 0 {
				return args, fmt.Errorf("media_index 每项不能为空")
			}
			for _, index := range args.MediaIndex[i] {
				if index < 1 {
					return args, fmt.Errorf("媒体序号从 1 开始")
				}
			}
		}
	}
	return args, nil
}

// sourcePath resolves a local source before it is touched. Call repeats this
// check so invoking the handler cannot bypass the argument-specific permission.
func (t ViewImageTool) sourcePath(ctx context.Context, source string) (tool.ResolvedPath, error) {
	if source == "" || delivery.IsHTTPMediaSource(source) {
		return tool.ResolvedPath{}, nil
	}
	if strings.HasPrefix(source, media.IDPrefix) {
		if !media.ValidID(source) {
			return tool.ResolvedPath{}, fmt.Errorf("invalid media ID")
		}
		return tool.ResolvedPath{}, nil
	}
	actor, ok := security.ActorFromContext(ctx)
	if !ok || actor.Role != security.RoleSuperadmin {
		return tool.ResolvedPath{}, fmt.Errorf("查看本地图片需要超级管理员权限")
	}
	path, err := localSourcePath(source)
	if err != nil {
		return tool.ResolvedPath{}, err
	}
	return tool.ResolveWorkspacePath(ctx, path, tool.PathResolveOptions{})
}

func (t ViewImageTool) AssessRisk(ctx context.Context, req tool.CallRequest) (tool.RiskAssessment, error) {
	args, err := parseViewImageArgs(req)
	if err != nil {
		return tool.RiskAssessment{}, err
	}
	path, err := t.sourcePath(ctx, args.Source)
	if err != nil {
		return tool.RiskAssessment{}, err
	}
	if path.Path != "" && isSensitiveReadFile(path.Path) {
		return tool.RiskAssessment{Level: tool.RiskHigh, Reasons: []string{"读取可能包含凭据的敏感文件，需要用户确认"}}, nil
	}
	return tool.RiskAssessment{Level: tool.RiskLow}, nil
}

func (t ViewImageTool) PreflightConfirmation(ctx context.Context, req tool.CallRequest) error {
	_, err := t.AssessRisk(ctx, req)
	return err
}

func (t ViewImageTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	args, err := parseViewImageArgs(req)
	if err != nil {
		return nil, err
	}
	if !tool.VisionAvailable(ctx) {
		return nil, fmt.Errorf("当前模型不支持图片输入，无法使用 %s", ViewImageName)
	}
	if t.center == nil {
		return nil, fmt.Errorf("media center is not configured")
	}
	if args.Source == "" {
		return t.viewMessages(ctx, args)
	}
	path, err := t.sourcePath(ctx, args.Source)
	if err != nil {
		return nil, err
	}
	var item *storage.Media
	switch {
	case strings.HasPrefix(args.Source, media.IDPrefix):
		item, err = t.center.Metadata(ctx, args.Source)
	case delivery.IsHTTPMediaSource(args.Source):
		item, err = t.center.ImportURL(ctx, args.Source, media.Input{})
	default:
		item, err = t.center.ImportFile(ctx, path.Path, media.Input{})
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("图片获取失败：来源不可用或超限")
	}
	if err := t.validateImage(ctx, item); err != nil {
		return nil, err
	}
	return &tool.Result{Segments: []llm.MessageSegment{viewImageSegment(item)}, Warnings: path.Warnings}, nil
}

// validateImage confirms the stored object really is a decodable image, so the
// model is not handed a file it cannot read.
func (t ViewImageTool) validateImage(ctx context.Context, item *storage.Media) error {
	if item == nil {
		return fmt.Errorf("图片获取失败：来源不可用或超限")
	}
	if !isImageMIME(item.MIMEType) {
		return fmt.Errorf("媒体不是有效或支持的图片")
	}
	data, meta, err := t.center.ReadLimited(ctx, item.ID, media.VisionImageInputLimit)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("图片读取失败：媒体不存在、过大或已过期")
	}
	mimeType := item.MIMEType
	if meta != nil && meta.MIMEType != "" {
		mimeType = meta.MIMEType
	}
	if _, _, err := media.PrepareVisionImageContext(ctx, data, mimeType, 0, 0); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("媒体不是有效或支持的图片")
	}
	return nil
}

func (t ViewImageTool) viewMessages(ctx context.Context, args viewImageArgs) (*tool.Result, error) {
	chat, err := currentChatHistoryContext(ctx)
	if err != nil {
		return &tool.Result{Content: err.Error()}, nil
	}
	if t.history == nil {
		return nil, fmt.Errorf("chat history storage is not configured")
	}
	msg, _ := platform.MessageContextFrom(ctx)
	attempts := 0
	segments := []llm.MessageSegment{}
	for i, id := range args.MessageIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := t.history.GetByPlatformMessage(ctx, chat.Platform, chat.ScopeID, id)
		if errors.Is(err, storage.ErrNotFound) {
			segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: fmt.Sprintf("[#%s] 当前聊天没有该消息", id)})
			continue
		}
		if err != nil {
			return nil, err
		}
		all := media.HistorySegments(*row)
		ids, err := t.center.HistoryIDs(ctx, *row)
		if err != nil {
			return nil, err
		}
		var indexes []int
		if args.MediaIndex != nil {
			indexes = args.MediaIndex[i]
		}
		if indexes == nil {
			if first := firstImageIndex(all); first > 0 {
				indexes = []int{first}
			}
		}
		if len(indexes) == 0 {
			segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: fmt.Sprintf("[#%s] 没有图片", id)})
			continue
		}
		for _, index := range indexes {
			if index > len(all) || all[index-1].Type != platform.SegmentImage {
				available := imageIndexes(all)
				if len(available) == 0 {
					segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: fmt.Sprintf("[#%s] 没有图片", id)})
				} else {
					segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: fmt.Sprintf("[#%s] 序号 %d 不是图片；可选图片序号：%v", id, index, available)})
				}
				continue
			}
			label := fmt.Sprintf("[#%s] %d.", id, index)
			if ids[index] == "" && attempts >= getMediaDownloadLimit {
				segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: label + fmt.Sprintf(" 达到本次 %d 个下载尝试上限", getMediaDownloadLimit)})
				continue
			}
			if ids[index] == "" {
				attempts++
			}
			item, err := t.center.GetHistoryMedia(ctx, *row, index, msg.MediaResolver)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				// Transport errors may contain credential URLs or local paths;
				// never echo them to the LLM.
				segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: label + " 图片获取失败"})
				continue
			}
			if err := t.validateImage(ctx, item); err != nil {
				segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: label + " 媒体不是有效或支持的图片"})
				continue
			}
			segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: label}, viewImageSegment(item))
		}
	}
	return &tool.Result{Segments: segments}, ctx.Err()
}

func firstImageIndex(segments []platform.MessageSegment) int {
	for i, segment := range segments {
		if segment.Type == platform.SegmentImage {
			return i + 1
		}
	}
	return 0
}

func imageIndexes(segments []platform.MessageSegment) []int {
	var out []int
	for i, segment := range segments {
		if segment.Type == platform.SegmentImage {
			out = append(out, i+1)
		}
	}
	return out
}

func viewImageSegment(item *storage.Media) llm.MessageSegment {
	return llm.MessageSegment{Type: llm.SegmentImage, MediaID: item.ID, Name: item.Name, MIMEType: item.MIMEType}
}
