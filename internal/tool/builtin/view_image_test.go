package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elbot/internal/llm"
	"elbot/internal/security"
	"elbot/internal/tool"
)

func TestViewImageSchemaAndArguments(t *testing.T) {
	properties := (ViewImageTool{}).Schema().Function.Parameters["properties"].(map[string]any)
	if properties["message_id"].(map[string]any)["minItems"] != 1 {
		t.Fatalf("message_id must require at least one id: %#v", properties["message_id"])
	}
	if properties["media_index"] == nil {
		t.Fatal("media_index must be documented in the schema")
	}

	for _, raw := range []string{
		`{}`,
		`{"source":"media:x","message_id":["1"]}`,
		`{"source":"local.png","media_index":[[1]]}`,
		`{"source":"local.png","message_id":["1"]}`,
		`{"message_id":["1"],"media_index":[]}`,
		`{"message_id":["1"],"media_index":[[]]}`,
		`{"message_id":["1"],"media_index":[[0]]}`,
	} {
		if _, err := parseViewImageArgs(tool.CallRequest{Arguments: json.RawMessage(raw)}); err == nil {
			t.Fatalf("accepted invalid arguments %s", raw)
		}
	}

	args, err := parseViewImageArgs(tool.CallRequest{Arguments: json.RawMessage(`{"message_id":["#7"],"media_index":[[2]]}`)})
	if err != nil || len(args.MessageIDs) != 1 || args.MessageIDs[0] != "7" || args.MediaIndex[0][0] != 2 {
		t.Fatalf("parse = %#v err=%v", args, err)
	}
}

func TestViewImageFollowsVisionCapability(t *testing.T) {
	info := (ViewImageTool{}).Info()
	if !info.VisionRequired {
		t.Fatal("view_image must declare VisionRequired")
	}
	if !tool.InfoAvailableInContext(context.Background(), info) {
		t.Fatal("view_image must stay available when the caller never declares capabilities")
	}
	withoutVision := tool.WithCapabilities(context.Background(), tool.Capabilities{Vision: false})
	if tool.InfoAvailableInContext(withoutVision, info) {
		t.Fatal("view_image must be hidden for a model without image input")
	}

	registry := tool.NewRegistry()
	if err := registry.Register(NewViewImageTool(nil, nil)); err != nil {
		t.Fatal(err)
	}
	// Callers filter by availability before asking the registry, so the witness
	// here mirrors internal/tool/discover.go.
	blocked := func(candidate tool.Tool) bool {
		return tool.InfoAvailableInContext(withoutVision, candidate.Info())
	}
	if _, errs := registry.DiscoverDetails(withoutVision, []string{ViewImageName}, blocked); len(errs) == 0 {
		t.Fatal("a model without vision must not discover view_image")
	}
	details, errs := registry.DiscoverDetails(context.Background(), []string{ViewImageName}, nil)
	if len(errs) != 0 || len(details) != 1 || details[0].Schema == nil {
		t.Fatalf("details = %#v errs = %#v", details, errs)
	}
}

func TestViewImageRejectsModelWithoutVision(t *testing.T) {
	ctx, tools, _ := newHistoryMediaToolTest(t)
	view := NewViewImageTool(tools.history, tools.center)
	noVision := tool.WithCapabilities(ctx, tool.Capabilities{Vision: false})
	_, err := view.Call(noVision, tool.CallRequest{Arguments: json.RawMessage(`{"message_id":["1"]}`)})
	if err == nil || !strings.Contains(err.Error(), "不支持图片输入") {
		t.Fatalf("Call error = %v, want a vision refusal", err)
	}
}

func TestViewImageLocalPathNeedsSuperadmin(t *testing.T) {
	view := NewViewImageTool(nil, nil)
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("not-a-real-png"), 0o644); err != nil {
		t.Fatal(err)
	}
	userCtx := security.WithActor(context.Background(), security.Actor{ID: "cli:u1", Platform: "cli", Role: security.RoleUser})
	if _, err := view.sourcePath(userCtx, path); err == nil || !strings.Contains(err.Error(), "超级管理员") {
		t.Fatalf("sourcePath error = %v, want a superadmin requirement", err)
	}
	mediaID := "media:" + strings.Repeat("a", 64)
	if _, err := view.sourcePath(userCtx, mediaID); err != nil {
		t.Fatalf("a media ID must not require superadmin: %v", err)
	}
	if _, err := view.sourcePath(userCtx, "https://example.com/a.png"); err != nil {
		t.Fatalf("an HTTP source must not require superadmin: %v", err)
	}
}

func TestViewImageReturnsImageSegmentsForHistoryMessage(t *testing.T) {
	ctx, tools, _ := newHistoryMediaToolTest(t)
	view := NewViewImageTool(tools.history, tools.center)

	result, err := view.Call(ctx, tool.CallRequest{Arguments: json.RawMessage(`{"message_id":["1"]}`)})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	image, label := false, false
	for _, segment := range result.Segments {
		if segment.Type == llm.SegmentImage && segment.MediaID != "" {
			image = true
		}
		if segment.Type == llm.SegmentText && strings.Contains(segment.Text, "[#1]") {
			label = true
		}
	}
	if !image || !label {
		t.Fatalf("segments = %#v, want a label and an image", result.Segments)
	}

	// An explicit index that is not an image reports the usable image numbers.
	outOfRange, err := view.Call(ctx, tool.CallRequest{Arguments: json.RawMessage(`{"message_id":["1"],"media_index":[[100]]}`)})
	if err != nil || !strings.Contains(llm.SegmentsTextOnly(outOfRange.Segments), "不是图片") {
		t.Fatalf("out of range = %#v err=%v", outOfRange.Segments, err)
	}
}
