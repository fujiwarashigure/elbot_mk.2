package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"elbot/internal/media"
	"elbot/internal/storage/sqlite"
	"elbot/internal/tool"
	"elbot/internal/vision"
)

// fakeImagePromptService stands in for internal/vision.Service. It records the
// exact Request the tool builds, which is how these tests pin the tool's
// contract with the shared engine: read the media, normalize the enums, render
// the right prompt template and never cache anything locally.
type fakeImagePromptService struct {
	mu       sync.Mutex
	calls    int
	last     vision.Request
	response string
	err      error
}

func (f *fakeImagePromptService) Describe(_ context.Context, req vision.Request) (vision.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = req
	if f.err != nil {
		return vision.Result{}, f.err
	}
	return vision.Result{Text: f.response, Provider: "test", Model: "vision-1"}, nil
}

func (f *fakeImagePromptService) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeImagePromptService) lastRequest() vision.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func (f *fakeImagePromptService) setResult(response string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.response = response
	f.err = err
}

func imageToPromptTestPNG(t *testing.T) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 12; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 20), G: uint8(y * 30), B: 120, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, source); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return output.Bytes()
}

func newImageToPromptTest(t *testing.T, service ImagePromptService) (context.Context, ImageToPromptTool, string) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	root := t.TempDir()
	center := media.NewManager(store, root, &media.LocalBackend{Root: root})
	item, err := center.ImportBytes(ctx, imageToPromptTestPNG(t), media.Input{Name: "ref.png", MIMEType: "image/png"})
	if err != nil {
		t.Fatalf("import media: %v", err)
	}
	return ctx, NewImageToPromptTool(center, service), item.ID
}

func callImageToPrompt(t *testing.T, ctx context.Context, value ImageToPromptTool, args map[string]any) *tool.Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := value.Call(ctx, tool.CallRequest{Name: ImageToPromptName, Arguments: raw})
	if err != nil {
		t.Fatalf("call image_to_prompt: %v", err)
	}
	if result == nil {
		t.Fatal("image_to_prompt returned nil result")
	}
	return result
}

func TestImageToPromptDelegatesNormalizedRequestToVisionService(t *testing.T) {
	service := &fakeImagePromptService{response: "a red cube on a wooden table"}
	ctx, value, mediaID := newImageToPromptTest(t, service)

	result := callImageToPrompt(t, ctx, value, map[string]any{"image": mediaID, "target": "SDXL", "language": "EN"})
	if result.Content != "a red cube on a wooden table" {
		t.Fatalf("content = %q", result.Content)
	}
	if service.callCount() != 1 {
		t.Fatalf("service calls = %d, want 1", service.callCount())
	}
	req := service.lastRequest()
	if req.MediaID != mediaID {
		t.Fatalf("media id = %q, want %q", req.MediaID, mediaID)
	}
	if len(req.Data) == 0 || req.MIMEType != "image/png" {
		t.Fatalf("service media = %d bytes, %q", len(req.Data), req.MIMEType)
	}
	if req.Prompt.Version != vision.ImagePromptTemplateVersion {
		t.Fatalf("prompt version = %q", req.Prompt.Version)
	}
	if !strings.Contains(req.Prompt.User, "SDXL") || !strings.Contains(req.Prompt.User, "English") {
		t.Fatalf("prompt user text = %q", req.Prompt.User)
	}

	// Omitted target/language default to general/zh.
	callImageToPrompt(t, ctx, value, map[string]any{"image": mediaID})
	if service.callCount() != 2 {
		t.Fatalf("service calls = %d, want 2", service.callCount())
	}
	req = service.lastRequest()
	if !strings.Contains(req.Prompt.User, "general") || !strings.Contains(req.Prompt.User, "中文") {
		t.Fatalf("default prompt user text = %q", req.Prompt.User)
	}
}

func TestImageToPromptRejectsBadInputWithoutCallingModel(t *testing.T) {
	service := &fakeImagePromptService{response: "x"}
	ctx, value, mediaID := newImageToPromptTest(t, service)

	for name, args := range map[string]map[string]any{
		"missing image": {},
		"bad id":        {"image": "not-a-media-id"},
	} {
		result := callImageToPrompt(t, ctx, value, args)
		if strings.TrimSpace(result.Content) == "" {
			t.Fatalf("%s: empty error content", name)
		}
	}
	if service.callCount() != 0 {
		t.Fatalf("service was called for invalid input: %d", service.callCount())
	}

	missing := media.IDPrefix + strings.Repeat("0", 64)
	if missing == mediaID {
		t.Fatal("test media id collision")
	}
	result := callImageToPrompt(t, ctx, value, map[string]any{"image": missing})
	if !strings.Contains(result.Content, "读取图片失败") {
		t.Fatalf("missing media content = %q", result.Content)
	}
	if service.callCount() != 0 {
		t.Fatalf("service was called for missing media: %d", service.callCount())
	}
}

func TestImageToPromptWithoutServiceReportsConfiguration(t *testing.T) {
	ctx, value, mediaID := newImageToPromptTest(t, nil)
	result := callImageToPrompt(t, ctx, value, map[string]any{"image": mediaID})
	if !strings.Contains(result.Content, "未配置视觉模型") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestImageToPromptInfoAndSchema(t *testing.T) {
	value := NewImageToPromptTool(nil, nil)
	if value.Name() != ImageToPromptName {
		t.Fatalf("name = %q", value.Name())
	}
	info := value.Info()
	if info.Risk != tool.RiskMedium || !containsString(info.Tags, "image") || !containsString(info.Tags, "prompt") {
		t.Fatalf("info = %#v", info)
	}
	schema := value.Schema()
	if schema.Function.Name != ImageToPromptName {
		t.Fatalf("schema name = %q", schema.Function.Name)
	}
	properties, ok := schema.Function.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema.Function.Parameters["properties"])
	}
	imageProperty, ok := properties["image"].(map[string]any)
	if !ok || imageProperty["type"] != "string" {
		t.Fatalf("image property = %#v", properties["image"])
	}
	target, ok := properties["target"].(map[string]any)
	if !ok {
		t.Fatalf("target property = %#v", properties["target"])
	}
	targetEnum, ok := target["enum"].([]string)
	if !ok || len(targetEnum) != 3 || targetEnum[0] != "general" || targetEnum[1] != "sdxl" || targetEnum[2] != "flux" {
		t.Fatalf("target enum = %#v", target["enum"])
	}
	language, ok := properties["language"].(map[string]any)
	if !ok {
		t.Fatalf("language property = %#v", properties["language"])
	}
	languageEnum, ok := language["enum"].([]string)
	if !ok || len(languageEnum) != 2 || languageEnum[0] != "zh" || languageEnum[1] != "en" {
		t.Fatalf("language enum = %#v", language["enum"])
	}
	required, ok := schema.Function.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "image" {
		t.Fatalf("required = %#v", schema.Function.Parameters["required"])
	}
}

func TestImageToPromptSurfacesServiceError(t *testing.T) {
	service := &fakeImagePromptService{err: errImageToPromptTest}
	ctx, value, mediaID := newImageToPromptTest(t, service)
	result := callImageToPrompt(t, ctx, value, map[string]any{"image": mediaID})
	if !strings.Contains(result.Content, "反推提示词失败") || !strings.Contains(result.Content, errImageToPromptTest.Error()) {
		t.Fatalf("content = %q", result.Content)
	}
	if service.callCount() != 1 {
		t.Fatalf("service calls = %d", service.callCount())
	}
}

// The tool must not keep a private success cache: caching and retries are the
// shared engine's job, so a failed call has to reach the service again.
func TestImageToPromptDoesNotCacheFailuresLocally(t *testing.T) {
	service := &fakeImagePromptService{err: errImageToPromptTest}
	ctx, value, mediaID := newImageToPromptTest(t, service)

	if result := callImageToPrompt(t, ctx, value, map[string]any{"image": mediaID}); !strings.Contains(result.Content, "反推提示词失败") {
		t.Fatalf("failure content = %q", result.Content)
	}
	service.setResult("recovered prompt", nil)
	result := callImageToPrompt(t, ctx, value, map[string]any{"image": mediaID})
	if result.Content != "recovered prompt" {
		t.Fatalf("content after retry = %q", result.Content)
	}
	if service.callCount() != 2 {
		t.Fatalf("service calls = %d, want 2", service.callCount())
	}
}

func TestImageToPromptCancellationIsACallFailure(t *testing.T) {
	service := &fakeImagePromptService{err: context.Canceled}
	ctx, value, mediaID := newImageToPromptTest(t, service)
	raw, err := json.Marshal(map[string]any{"image": mediaID})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, callErr := value.Call(ctx, tool.CallRequest{Name: ImageToPromptName, Arguments: raw})
	if !errors.Is(callErr, context.Canceled) {
		t.Fatalf("call error = %v, want context.Canceled", callErr)
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil", result)
	}
}

var errImageToPromptTest = errors.New("vision backend unavailable")

func TestRegisterAllIncludesImageToPromptOnlyWhenConfigured(t *testing.T) {
	registry := tool.NewRegistry()
	if err := RegisterAll(registry, RegisterOptions{}); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Get(ImageToPromptName); ok {
		t.Fatal("image_to_prompt must not register without a vision service")
	}

	configured := tool.NewRegistry()
	if err := RegisterAll(configured, RegisterOptions{ImagePromptService: &fakeImagePromptService{response: "x"}}); err != nil {
		t.Fatalf("RegisterAll configured: %v", err)
	}
	registered, ok := configured.Get(ImageToPromptName)
	if !ok {
		t.Fatal("image_to_prompt was not registered")
	}
	if registered.Info().Source != tool.SourceBuiltin {
		t.Fatalf("image_to_prompt source = %q", registered.Info().Source)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
