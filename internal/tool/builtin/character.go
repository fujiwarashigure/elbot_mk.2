package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"elbot/internal/character"
	"elbot/internal/delivery"
	"elbot/internal/llm"
	"elbot/internal/media"
	"elbot/internal/security"
	"elbot/internal/tool"
)

const (
	CharacterListName   = "character_list"
	CharacterReadName   = "character_read"
	CharacterSearchName = "character_search"
	CharacterManageName = "character_manage"
	CharacterDeleteName = "character_delete"

	characterMaxImageBytes  = 10 << 20
	characterPromptMaxRunes = 12000
)

// CharacterTools wires the character library into the tool registry.
type CharacterTools struct {
	Store *character.Store
	Media *media.Manager
}

func NewCharacterTools(store *character.Store, center *media.Manager) []tool.Tool {
	tools := CharacterTools{Store: store, Media: center}
	return []tool.Tool{
		CharacterListTool{tools},
		CharacterReadTool{tools},
		CharacterSearchTool{tools},
		CharacterManageTool{tools},
		CharacterDeleteTool{tools},
	}
}

func characterViewer(ctx context.Context) character.Viewer {
	actor := actorFromContext(ctx)
	return character.Viewer{Platform: actor.Platform, ActorID: actor.ID, Superadmin: actor.Role == security.RoleSuperadmin}
}

func characterNotFoundText(id string) string {
	return fmt.Sprintf("没有找到角色 %q，可先用 character_list 或 character_search 查询。", id)
}

// ---------------------------------------------------------------- list

type CharacterListTool struct{ CharacterTools }

type characterListArgs struct {
	Tag   string `json:"tag"`
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (CharacterListTool) Name() string { return CharacterListName }

func (CharacterListTool) Info() tool.Info {
	return tool.NewBuilder(CharacterListName).
		Description("列出角色素材库中当前可见的角色（公开角色 + 自己的私有角色）。返回 id、名称、别名、tags、可见性、拥有者、图片数和简介。用 @char:<id> 可在消息里临时启用某个角色。").
		Risk(tool.RiskLow).
		Tags("character").
		DependsOn(CharacterReadName, CharacterSearchName).
		BuildInfo()
}

func (CharacterListTool) Schema() llm.ToolSchema {
	return tool.NewBuilder(CharacterListName).
		Description("列出角色素材库中当前可见的角色。").
		String("tag", "可选，按 tag 过滤。").
		String("query", "可选，按 id/名称/别名/简介做简单过滤。").
		Integer("limit", "最多返回数量，默认 50，最大 200。").
		BuildSchema()
}

func (t CharacterListTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if !t.Store.Enabled() {
		return &tool.Result{Content: "角色素材库未配置。"}, nil
	}
	var args characterListArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse character_list arguments: %w", err)
		}
	}
	items, err := t.Store.List(ctx, characterViewer(ctx))
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(args.Query))
	tag := strings.ToLower(strings.TrimSpace(args.Tag))
	filtered := make([]*character.Character, 0, len(items))
	for _, item := range items {
		if tag != "" && !characterHasTag(item, tag) {
			continue
		}
		if query != "" && !strings.Contains(characterSearchText(item), query) {
			continue
		}
		filtered = append(filtered, item)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	lines := []string{fmt.Sprintf("角色：共 %d 个可见。", len(filtered))}
	for i, item := range filtered {
		if i >= limit {
			lines = append(lines, fmt.Sprintf("...[已截断，还有 %d 个]", len(filtered)-limit))
			break
		}
		lines = append(lines, "- "+formatCharacterSummary(item))
	}
	lines = append(lines, "提示：发送 @char:<id> 可临时启用该角色。")
	return &tool.Result{Content: strings.Join(lines, "\n")}, nil
}

func formatCharacterSummary(item *character.Character) string {
	parts := []string{item.ID, item.Name}
	if len(item.Aliases) > 0 {
		parts = append(parts, "aliases="+strings.Join(item.Aliases, ","))
	}
	if len(item.Tags) > 0 {
		parts = append(parts, "tags="+strings.Join(item.Tags, ","))
	}
	parts = append(parts, "visibility="+string(item.Visibility))
	if owner := formatCharacterOwner(item); owner != "" {
		parts = append(parts, "owner="+owner)
	}
	if len(item.Images) > 0 {
		parts = append(parts, fmt.Sprintf("images=%d", len(item.Images)))
	}
	line := strings.Join(parts, " | ")
	if item.Description != "" {
		line += " | " + item.Description
	}
	return line
}

func formatCharacterOwner(item *character.Character) string {
	if item.OwnerID == "" {
		return "system"
	}
	if item.OwnerPlatform != "" && !strings.Contains(item.OwnerID, ":") {
		return item.OwnerPlatform + ":" + item.OwnerID
	}
	return item.OwnerID
}

func characterHasTag(item *character.Character, tag string) bool {
	for _, candidate := range item.Tags {
		if strings.EqualFold(strings.TrimSpace(candidate), tag) {
			return true
		}
	}
	return false
}

func characterSearchText(item *character.Character) string {
	var b strings.Builder
	b.WriteString(item.ID + "\n" + item.Name + "\n" + item.Description + "\n")
	b.WriteString(strings.Join(item.Aliases, " ") + "\n" + strings.Join(item.Tags, " ") + "\n")
	kinds := make([]string, 0, len(item.Docs))
	for kind := range item.Docs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		b.WriteString(item.Docs[kind] + "\n")
	}
	return strings.ToLower(b.String())
}

// ---------------------------------------------------------------- read

type CharacterReadTool struct{ CharacterTools }

type characterReadArgs struct {
	ID            string `json:"id"`
	Section       string `json:"section"`
	IncludeImages bool   `json:"include_images"`
}

func (CharacterReadTool) Name() string { return CharacterReadName }

func (CharacterReadTool) Info() tool.Info {
	return tool.NewBuilder(CharacterReadName).
		Description("读取指定角色的设定文本（profile/profile、world、greeting、examples、notes/<name>），可选返回角色图片。没有明确要求时优先只读 profile。").
		Risk(tool.RiskLow).
		Tags("character").
		BuildInfo()
}

func (CharacterReadTool) Schema() llm.ToolSchema {
	return tool.NewBuilder(CharacterReadName).
		Description("读取指定角色的设定文本和图片。").
		String("id", "角色 id。", tool.Required()).
		String("section", "读取范围：all（默认）、profile、world、greeting、examples、notes 或 notes/<name>。").
		Boolean("include_images", "为 true 时把角色图片作为图片段返回，默认 false。").
		BuildSchema()
}

func (t CharacterReadTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if !t.Store.Enabled() {
		return &tool.Result{Content: "角色素材库未配置。"}, nil
	}
	var args characterReadArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse character_read arguments: %w", err)
		}
	}
	item, err := t.Store.GetVisible(ctx, args.ID, characterViewer(ctx))
	if err != nil {
		if err == character.ErrNotFound {
			return &tool.Result{Content: characterNotFoundText(args.ID)}, nil
		}
		if err == character.ErrForbidden {
			return &tool.Result{Content: fmt.Sprintf("角色 %q 不可访问。", args.ID)}, nil
		}
		return nil, err
	}
	sections, err := characterSections(item, args.Section)
	if err != nil {
		return nil, err
	}
	segments := []llm.MessageSegment{{Type: llm.SegmentText, Text: renderCharacterSections(item, sections)}}
	if args.IncludeImages {
		if len(item.Images) == 0 {
			segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: "（该角色没有图片）"})
		}
		for _, image := range item.Images {
			if strings.TrimSpace(image.MediaID) == "" {
				segments = append(segments, llm.MessageSegment{Type: llm.SegmentText, Text: fmt.Sprintf("（图片 %s 未关联 Media Center，仅保存在 %s）", image.Name, image.Path)})
				continue
			}
			segments = append(segments, llm.MessageSegment{Type: llm.SegmentImage, MediaID: image.MediaID, Name: image.Name, MIMEType: image.MIMEType})
		}
	}
	return &tool.Result{Segments: segments}, nil
}

func characterSections(item *character.Character, section string) ([]string, error) {
	section = strings.TrimSpace(section)
	if section == "" || strings.EqualFold(section, "all") {
		kinds := make([]string, 0, len(item.Docs))
		for kind := range item.Docs {
			kinds = append(kinds, kind)
		}
		sort.Slice(kinds, func(i, j int) bool { return characterDocRank(kinds[i]) < characterDocRank(kinds[j]) })
		return kinds, nil
	}
	if !strings.EqualFold(section, "notes") && !strings.HasPrefix(strings.ToLower(section), "notes/") {
		if _, ok := item.Docs[section]; !ok {
			return nil, fmt.Errorf("角色 %q 没有 %q 文档；可用：all、profile、world、greeting、examples、notes/<name>", item.ID, section)
		}
		return []string{section}, nil
	}
	if strings.EqualFold(section, "notes") {
		kinds := make([]string, 0, len(item.Docs))
		for kind := range item.Docs {
			if strings.HasPrefix(kind, "notes/") {
				kinds = append(kinds, kind)
			}
		}
		sort.Strings(kinds)
		if len(kinds) == 0 {
			return nil, fmt.Errorf("角色 %q 没有 notes", item.ID)
		}
		return kinds, nil
	}
	key := "notes/" + strings.TrimPrefix(section, "notes/")
	if _, ok := item.Docs[key]; !ok {
		return nil, fmt.Errorf("角色 %q 没有 %q", item.ID, key)
	}
	return []string{key}, nil
}

func characterDocRank(kind string) int {
	switch kind {
	case "profile":
		return 0
	case "world":
		return 1
	case "greeting":
		return 2
	case "examples":
		return 3
	}
	return 10
}

func renderCharacterSections(item *character.Character, sections []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("角色 %s（%s）", item.Name, item.ID))
	if len(item.Aliases) > 0 {
		b.WriteString(" 别名：" + strings.Join(item.Aliases, "、"))
	}
	if item.Description != "" {
		b.WriteString(" 简介：" + item.Description)
	}
	if len(item.Tags) > 0 {
		b.WriteString(" tags：" + strings.Join(item.Tags, "、"))
	}
	b.WriteString("\n")
	for _, kind := range sections {
		b.WriteString("\n## " + kind + "\n" + strings.TrimSpace(item.Docs[kind]) + "\n")
	}
	return strings.TrimSpace(b.String())
}

// ---------------------------------------------------------------- search

type CharacterSearchTool struct{ CharacterTools }

type characterSearchArgs struct {
	Query     string `json:"query"`
	MatchMode string `json:"match_mode"`
	Tag       string `json:"tag"`
	Limit     int    `json:"limit"`
}

func (CharacterSearchTool) Name() string { return CharacterSearchName }

func (CharacterSearchTool) Info() tool.Info {
	return tool.NewBuilder(CharacterSearchName).
		Description("在角色素材库中按名称、别名、tags 和设定正文做全文检索，返回命中的角色和片段。").
		Risk(tool.RiskLow).
		Tags("character").
		BuildInfo()
}

func (CharacterSearchTool) Schema() llm.ToolSchema {
	return tool.NewBuilder(CharacterSearchName).
		Description("检索角色素材库。").
		String("query", "检索词，多个词用空格或逗号分隔。", tool.Required()).
		String("match_mode", "or（默认）或 and。").
		String("tag", "可选，按 tag 过滤。").
		Integer("limit", "返回数量，默认 5，最大 20。").
		BuildSchema()
}

func (t CharacterSearchTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if !t.Store.Enabled() {
		return &tool.Result{Content: "角色素材库未配置。"}, nil
	}
	var args characterSearchArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse character_search arguments: %w", err)
		}
	}
	results, err := t.Store.Search(ctx, args.Query, args.MatchMode, args.Tag, args.Limit, characterViewer(ctx))
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return &tool.Result{Content: "没有找到匹配的角色。"}, nil
	}
	lines := []string{fmt.Sprintf("命中 %d 个角色：", len(results))}
	for _, result := range results {
		line := fmt.Sprintf("- %s | %s", result.ID, result.Name)
		if len(result.Tags) > 0 {
			line += " | tags=" + strings.Join(result.Tags, ",")
		}
		line += fmt.Sprintf(" | score=%d", result.Score)
		if result.Snippet != "" {
			line += " | " + result.Snippet
		}
		lines = append(lines, line)
	}
	lines = append(lines, "用 character_read 读取完整设定，或发送 @char:<id> 临时启用。")
	return &tool.Result{Content: strings.Join(lines, "\n")}, nil
}

// ---------------------------------------------------------------- manage

type CharacterManageTool struct{ CharacterTools }

type characterImageArgs struct {
	PresetPrompt   string   `json:"preset_prompt"`
	NegativePrompt string   `json:"negative_prompt"`
	Size           string   `json:"size"`
	Quality        string   `json:"quality"`
	References     []string `json:"references"`
}

type characterManageArgs struct {
	Operation       string              `json:"operation"`
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Aliases         []string            `json:"aliases"`
	Tags            []string            `json:"tags"`
	Description     *string             `json:"description"`
	Visibility      string              `json:"visibility"`
	Version         *string             `json:"version"`
	Source          *string             `json:"source"`
	Docs            map[string]string   `json:"docs"`
	RemoveDocs      []string            `json:"remove_docs"`
	ImageSource     string              `json:"image_source"`
	ImageName       string              `json:"image_name"`
	ImageVersion    string              `json:"image_version"`
	ImageSourceName string              `json:"image_source_label"`
	Image           *characterImageArgs `json:"image"`
}

func (CharacterManageTool) Name() string { return CharacterManageName }

func (CharacterManageTool) Info() tool.Info {
	return tool.NewBuilder(CharacterManageName).
		Description("创建或修改自己的角色（公开角色只有超级管理员能改）。operation 为 create、update、add_image 或 remove_image；docs 支持 profile、world、greeting、examples、notes/<name>。普通用户创建的角色默认为私有。").
		Risk(tool.RiskLow).
		OwnerScoped().
		Tags("character").
		BuildInfo()
}

func (CharacterManageTool) Schema() llm.ToolSchema {
	return tool.NewBuilder(CharacterManageName).
		Description("创建或修改角色。").
		String("operation", "create、update、add_image 或 remove_image。", tool.Required(), tool.Enum("create", "update", "add_image", "remove_image")).
		String("id", "角色 id：小写字母/数字/-/_/. ，2-64 位。", tool.Required()).
		String("name", "显示名称。").
		StringArray("aliases", "别名列表。").
		StringArray("tags", "分类 tag。").
		String("description", "一句话简介。").
		String("visibility", "public 或 private；普通用户只能创建/保留 private。").
		Object("docs", "要写入的文档，key 为 profile、world、greeting、examples、image_prompt 或 notes/<name>，value 为正文。传空字符串删除该文档。").
		StringArray("remove_docs", "要删除的文档 key。").
		Object("image", "可选，生图预设：preset_prompt、negative_prompt、size、quality、references（角色图片名列表）。").
		String("image_source", "add_image 用：工作区相对路径、http(s) URL 或 media:<sha256>。").
		String("image_name", "add_image/remove_image 用的图片文件名，例如 avatar.png。").
		BuildSchema()
}

func (t CharacterManageTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if !t.Store.Enabled() {
		return &tool.Result{Content: "角色素材库未配置。"}, nil
	}
	var args characterManageArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse character_manage arguments: %w", err)
		}
	}
	viewer := characterViewer(ctx)
	switch strings.ToLower(strings.TrimSpace(args.Operation)) {
	case "add_image":
		image, err := t.addImage(ctx, args, viewer)
		if err != nil {
			return &tool.Result{Content: err.Error()}, nil
		}
		return &tool.Result{Content: fmt.Sprintf("已写入角色 %s 的图片 %s（%d bytes，media_id=%s）。", args.ID, image.Name, image.Size, firstNonEmptyString(image.MediaID, "none"))}, nil
	case "remove_image":
		if strings.TrimSpace(args.ImageName) == "" {
			return &tool.Result{Content: "remove_image 需要 image_name。"}, nil
		}
		if err := t.Store.RemoveImage(ctx, args.ID, args.ImageName, viewer); err != nil {
			return &tool.Result{Content: characterWriteError(args.ID, err)}, nil
		}
		return &tool.Result{Content: fmt.Sprintf("已删除角色 %s 的图片 %s。", args.ID, args.ImageName)}, nil
	case "create", "update":
		writeReq := character.WriteRequest{
			ID:          args.ID,
			Name:        args.Name,
			Aliases:     args.Aliases,
			Description: args.Description,
			Tags:        args.Tags,
			Visibility:  args.Visibility,
			Version:     args.Version,
			Source:      args.Source,
			RemoveDocs:  args.RemoveDocs,
			Docs:        map[string]*string{},
		}
		if args.Image != nil {
			writeReq.Image = &character.ImageSettings{
				PresetPrompt:   args.Image.PresetPrompt,
				NegativePrompt: args.Image.NegativePrompt,
				Size:           args.Image.Size,
				Quality:        args.Image.Quality,
				References:     args.Image.References,
			}
		}
		for kind, content := range args.Docs {
			value := content
			writeReq.Docs[kind] = &value
		}
		item, err := t.Store.Write(ctx, writeReq, viewer)
		if err != nil {
			return &tool.Result{Content: characterWriteError(args.ID, err)}, nil
		}
		return &tool.Result{Content: fmt.Sprintf("角色已保存：%s（可见性 %s，文档 %d 个，图片 %d 张）。可用 @char:%s 临时启用。", item.Name, item.Visibility, len(item.Docs), len(item.Images), item.ID)}, nil
	default:
		return &tool.Result{Content: "operation 必须是 create、update、add_image 或 remove_image。"}, nil
	}
}

func characterWriteError(id string, err error) string {
	switch err {
	case character.ErrNotFound:
		return characterNotFoundText(id)
	case character.ErrForbidden:
		return fmt.Sprintf("角色 %q 不属于你，只有拥有者或超级管理员可以修改。", id)
	case character.ErrInvalidID:
		return "角色 id 不合法：只允许小写字母、数字、-、_、.，且以字母或数字开头。"
	}
	return err.Error()
}

func (t CharacterManageTool) addImage(ctx context.Context, args characterManageArgs, viewer character.Viewer) (*character.Image, error) {
	if t.Media == nil {
		return nil, fmt.Errorf("Media Center 未配置，无法保存图片")
	}
	source := strings.TrimSpace(args.ImageSource)
	if source == "" {
		return nil, fmt.Errorf("add_image 需要 image_source")
	}
	name := strings.TrimSpace(args.ImageName)
	mimeType := ""
	var data []byte
	var mediaID string
	switch {
	case strings.HasPrefix(source, media.IDPrefix):
		if !media.ValidID(source) {
			return nil, fmt.Errorf("media id 不合法")
		}
		item, err := t.Media.Metadata(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("读取媒体失败：%w", err)
		}
		mediaID = source
		mimeType = item.MIMEType
		if name == "" {
			name = item.Name
		}
		content, _, err := t.Media.Read(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("读取媒体内容失败：%w", err)
		}
		data = content
	case delivery.IsHTTPMediaSource(source):
		imported, err := t.Media.ImportURL(ctx, source, media.Input{Name: name})
		if err != nil {
			return nil, fmt.Errorf("导入图片失败：%w", err)
		}
		mediaID = imported.ID
		mimeType = imported.MIMEType
		if name == "" {
			name = imported.Name
		}
		content, _, err := t.Media.Read(ctx, imported.ID)
		if err != nil {
			return nil, fmt.Errorf("读取导入内容失败：%w", err)
		}
		data = content
	default:
		resolved, err := tool.ResolveWorkspacePath(ctx, source, tool.PathResolveOptions{})
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(resolved.Path)
		if err != nil {
			return nil, fmt.Errorf("读取图片失败：%w", err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("image_source 不能是目录")
		}
		if info.Size() > characterMaxImageBytes {
			return nil, fmt.Errorf("图片超过 %d bytes 上限", characterMaxImageBytes)
		}
		content, err := os.ReadFile(resolved.Path)
		if err != nil {
			return nil, fmt.Errorf("读取图片失败：%w", err)
		}
		data = content
		if name == "" {
			name = filepath.Base(resolved.Path)
		}
		imported, err := t.Media.ImportReader(ctx, bytes.NewReader(data), int64(len(data)), media.Input{Name: name})
		if err != nil {
			return nil, fmt.Errorf("导入图片失败：%w", err)
		}
		mediaID = imported.ID
		mimeType = imported.MIMEType
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("图片内容为空")
	}
	if len(data) > characterMaxImageBytes {
		return nil, fmt.Errorf("图片超过 %d bytes 上限", characterMaxImageBytes)
	}
	if name == "" {
		name = "image"
	}
	return t.Store.AddImageWithMeta(ctx, args.ID, name, mimeType, mediaID, args.ImageVersion, args.ImageSourceName, data, viewer)
}

// ---------------------------------------------------------------- delete

type CharacterDeleteTool struct{ CharacterTools }

type characterDeleteArgs struct {
	ID string `json:"id"`
}

func (CharacterDeleteTool) Name() string { return CharacterDeleteName }

func (CharacterDeleteTool) Info() tool.Info {
	return tool.NewBuilder(CharacterDeleteName).
		Description("永久删除自己的角色（含文本和图片）。公开角色或不属于自己的角色需要超级管理员。").
		Risk(tool.RiskHigh).
		OwnerScoped().
		Tags("character").
		BuildInfo()
}

func (CharacterDeleteTool) Schema() llm.ToolSchema {
	return tool.NewBuilder(CharacterDeleteName).
		Description("永久删除角色。").
		String("id", "要删除的角色 id。", tool.Required()).
		BuildSchema()
}

func (t CharacterDeleteTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if !t.Store.Enabled() {
		return &tool.Result{Content: "角色素材库未配置。"}, nil
	}
	var args characterDeleteArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse character_delete arguments: %w", err)
		}
	}
	if err := t.Store.Delete(ctx, args.ID, characterViewer(ctx)); err != nil {
		return &tool.Result{Content: characterWriteError(args.ID, err)}, nil
	}
	return &tool.Result{Content: fmt.Sprintf("已删除角色 %s。", args.ID)}, nil
}
