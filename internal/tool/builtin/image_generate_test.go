package builtin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"elbot/internal/character"
	"elbot/internal/imagegen"
	"elbot/internal/llm"
	"elbot/internal/media"
	"elbot/internal/platform"
	"elbot/internal/storage"
	"elbot/internal/storage/sqlite"
	"elbot/internal/tool"
)

func TestImageGenerateComposesPresets(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	prompts := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		prompt, _ := body["prompt"].(string)
		prompts = append(prompts, prompt)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
	defer server.Close()

	client := imagegen.New(imagegen.Config{
		Enabled:        true,
		BaseURL:        server.URL + "/v1",
		APIKey:         "k",
		Model:          "gpt-image-2.5",
		PresetPrompt:   "GLOBAL STYLE",
		NegativePrompt: "blurry",
		Optimize:       "off",
		SuperadminOnly: true,
	}, nil)
	characters := character.NewStore(t.TempDir())
	if _, err := characters.Write(context.Background(), character.WriteRequest{
		ID:         "catgirl",
		Name:       "猫娘",
		Visibility: "public",
		Image:      &character.ImageSettings{PresetPrompt: "CATGIRL LOOK", NegativePrompt: "extra fingers"},
	}, character.Viewer{Platform: "cli", ActorID: "cli:local", Superadmin: true}); err != nil {
		t.Fatalf("seed character: %v", err)
	}
	generate := imageToolForTest(client, characters, nil, nil)
	if info := generate.Info(); !info.SuperadminOnly {
		t.Fatalf("superadmin_only should be enforced: %#v", info)
	}

	ctx := context.Background()
	if _, err := generate.Call(ctx, tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"raining street","character_id":"catgirl"}`)}); err != nil {
		t.Fatalf("explicit character: %v", err)
	}
	activeCtx := character.WithActive(ctx, "catgirl")
	if _, err := generate.Call(activeCtx, tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"night sky"}`)}); err != nil {
		t.Fatalf("active character: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("prompts = %#v", prompts)
	}
	for _, prompt := range prompts {
		for _, want := range []string{"GLOBAL STYLE", "CATGIRL LOOK", "avoid: blurry, extra fingers"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("prompt %q missing %q", prompt, want)
			}
		}
	}
	if !strings.Contains(prompts[0], "raining street") || !strings.Contains(prompts[1], "night sky") {
		t.Fatalf("scene text missing: %#v", prompts)
	}
}

func TestImageGenerateWithoutCharacterUsesGlobalPresetOnly(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47}
	var prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt, _ = body["prompt"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
	defer server.Close()
	client := imagegen.New(imagegen.Config{Enabled: true, BaseURL: server.URL + "/v1", APIKey: "k", PresetPrompt: "GLOBAL", Optimize: "off"}, nil)
	generate := imageToolForTest(client, character.NewStore(t.TempDir()), nil, nil)
	if _, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"scene"}`)}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if prompt != "GLOBAL\n\nscene" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestImageGenerateAppliesPromptOptimizer(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47}
	var prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt, _ = body["prompt"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
	defer server.Close()
	client := imagegen.New(imagegen.Config{Enabled: true, BaseURL: server.URL + "/v1", APIKey: "k", Optimize: "rules"}, nil)
	generate := imageToolForTest(client, character.NewStore(t.TempDir()), nil, nil)
	result, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"给猫娘画一个头像"}`)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	for _, want := range []string{"给猫娘画一个头像", "1:1", "Square avatar"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("optimized prompt %q missing %q", prompt, want)
		}
	}
	_ = result
}

func TestImageGenerateTagMode(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47}
	var prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt, _ = body["prompt"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
	defer server.Close()
	client := imagegen.New(imagegen.Config{
		Enabled:          true,
		BaseURL:          server.URL + "/v1",
		APIKey:           "k",
		Optimize:         "rules",
		OptimizeTermMode: "tag",
	}, nil)
	generate := imageToolForTest(client, character.NewStore(t.TempDir()), nil, nil)
	if _, err := generate.Call(context.Background(), tool.CallRequest{
		Arguments: json.RawMessage(`{"prompt":"a cyberpunk street at night","term_mode":"tag"}`),
	}); err != nil {
		t.Fatalf("call: %v", err)
	}
	for _, want := range []string{"a cyberpunk street at night", "neon", ", "} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q missing %q", prompt, want)
		}
	}
}

func imageToolForTest(client *imagegen.Client, characters *character.Store, history storage.ChatHistoryRepository, rewriter ImagePromptRewriter) ImageGenerateTool {
	profiles := map[string]ImageProfile{}
	if client != nil {
		profiles[""] = ImageProfile{Name: "", Client: client, Config: client.Config()}
	}
	return NewImageGenerateTool(profiles, "", characters, nil, history, rewriter, nil)
}

type fakeHistoryRepo struct {
	rows    []storage.ChatMessage
	lastReq storage.ChatHistorySearchRequest
}

func (f *fakeHistoryRepo) Append(context.Context, *storage.ChatMessage) error { return nil }

func (f *fakeHistoryRepo) GetByPlatformMessage(context.Context, string, string, string) (*storage.ChatMessage, error) {
	return nil, storage.ErrNotFound
}

func (f *fakeHistoryRepo) Search(_ context.Context, req storage.ChatHistorySearchRequest) ([]storage.ChatMessage, error) {
	f.lastReq = req
	return f.rows, nil
}

func (f *fakeHistoryRepo) Around(context.Context, storage.ChatHistoryAroundRequest) ([]storage.ChatMessage, error) {
	return nil, nil
}

func (f *fakeHistoryRepo) DeleteBefore(context.Context, time.Time) (int, error) { return 0, nil }

type fakeRewriter struct {
	called bool
	req    ImagePromptRewriteRequest
}

func (f *fakeRewriter) RewriteImagePrompt(_ context.Context, req ImagePromptRewriteRequest) (string, error) {
	f.called = true
	f.req = req
	return "REWRITTEN " + req.Scene, nil
}

func imageTestServer(t *testing.T, capture func(prompt string)) *httptest.Server {
	t.Helper()
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if capture != nil {
			prompt, _ := body["prompt"].(string)
			capture(prompt)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
}

func TestPromptLibrarySearchTool(t *testing.T) {
	result, err := (PromptLibrarySearchTool{}).Call(context.Background(), tool.CallRequest{
		Arguments: json.RawMessage(`{"query":"cyberpunk","limit":3}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	for _, want := range []string{"赛博朋克", "terms: cyberpunk"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("result %q missing %q", result.Content, want)
		}
	}
}

func TestImageGenerateAutoCharacterByName(t *testing.T) {
	var prompt string
	server := imageTestServer(t, func(value string) { prompt = value })
	defer server.Close()

	characters := character.NewStore(t.TempDir())
	if _, err := characters.Write(context.Background(), character.WriteRequest{
		ID:         "catgirl",
		Name:       "猫娘",
		Visibility: "public",
		Image:      &character.ImageSettings{PresetPrompt: "CATGIRL LOOK"},
	}, character.Viewer{Platform: "cli", ActorID: "cli:local", Superadmin: true}); err != nil {
		t.Fatalf("seed character: %v", err)
	}
	client := imagegen.New(imagegen.Config{
		Enabled:       true,
		BaseURL:       server.URL + "/v1",
		APIKey:        "k",
		Optimize:      "off",
		AutoCharacter: true,
	}, nil)
	generate := imageToolForTest(client, characters, nil, nil)
	if _, err := generate.Call(context.Background(), tool.CallRequest{
		Arguments: json.RawMessage(`{"prompt":"画一张猫娘在雨里"}`),
	}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(prompt, "CATGIRL LOOK") {
		t.Fatalf("auto character preset missing: %q", prompt)
	}
}

func TestImageGeneratePullsGroupContext(t *testing.T) {
	var prompt string
	server := imageTestServer(t, func(value string) { prompt = value })
	defer server.Close()

	history := &fakeHistoryRepo{rows: []storage.ChatMessage{
		{SenderName: "张三", Text: "想要一张雨夜霓虹街道的图", CreatedAt: time.Now()},
	}}
	client := imagegen.New(imagegen.Config{
		Enabled:     true,
		BaseURL:     server.URL + "/v1",
		APIKey:      "k",
		Optimize:    "off",
		AutoContext: true,
	}, nil)
	generate := imageToolForTest(client, character.NewStore(t.TempDir()), history, nil)
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{Platform: "qqonebot", ScopeID: "group:9"})
	if _, err := generate.Call(ctx, tool.CallRequest{
		Arguments: json.RawMessage(`{"prompt":"画一下刚才群里说的那个场景"}`),
	}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(prompt, "雨夜霓虹街道") {
		t.Fatalf("group context missing from prompt: %q", prompt)
	}
	if history.lastReq.Platform != "qqonebot" || history.lastReq.PlatformScopeID != "group:9" {
		t.Fatalf("history request = %#v", history.lastReq)
	}
}

func TestImageGenerateUsesLLMRewrite(t *testing.T) {
	var prompt string
	server := imageTestServer(t, func(value string) { prompt = value })
	defer server.Close()

	rewriter := &fakeRewriter{}
	client := imagegen.New(imagegen.Config{
		Enabled:              true,
		BaseURL:              server.URL + "/v1",
		APIKey:               "k",
		Optimize:             "off",
		OptimizeRewrite:      "always",
		OptimizeRewriteModel: "naming",
	}, nil)
	generate := imageToolForTest(client, character.NewStore(t.TempDir()), nil, rewriter)
	result, err := generate.Call(context.Background(), tool.CallRequest{
		Arguments: json.RawMessage(`{"prompt":"猫在窗台"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !rewriter.called {
		t.Fatal("rewriter was not called")
	}
	if !strings.Contains(prompt, "REWRITTEN") {
		t.Fatalf("rewritten prompt missing: %q", prompt)
	}
	_ = result
}

func TestImageGenerateProfileSelection(t *testing.T) {
	baseHits, fastHits := 0, 0
	baseServer := imageTestServer(t, func(string) { baseHits++ })
	defer baseServer.Close()
	fastServer := imageTestServer(t, func(string) { fastHits++ })
	defer fastServer.Close()

	baseClient := imagegen.New(imagegen.Config{Enabled: true, BaseURL: baseServer.URL + "/v1", APIKey: "k", Optimize: "off"}, nil)
	fastClient := imagegen.New(imagegen.Config{Enabled: true, BaseURL: fastServer.URL + "/v1", APIKey: "k", Model: "fast-model", Optimize: "off"}, nil)
	profiles := map[string]ImageProfile{
		"":     {Name: "", Client: baseClient, Config: baseClient.Config()},
		"fast": {Name: "fast", Client: fastClient, Config: fastClient.Config()},
	}
	generate := NewImageGenerateTool(profiles, "", character.NewStore(t.TempDir()), nil, nil, nil, nil)

	// @image:fast for this turn only
	ctx := imagegen.WithProfile(context.Background(), "fast")
	if _, err := generate.Call(ctx, tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"x"}`)}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if fastHits != 1 || baseHits != 0 {
		t.Fatalf("with profile: base=%d fast=%d", baseHits, fastHits)
	}
	// next turn without the directive falls back to the default
	if _, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"x"}`)}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if baseHits != 1 || fastHits != 1 {
		t.Fatalf("without profile: base=%d fast=%d", baseHits, fastHits)
	}
	// explicit per-call profile
	if _, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"x","profile":"fast"}`)}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if fastHits != 2 {
		t.Fatalf("per-call profile: fast=%d", fastHits)
	}
	// unknown profile is rejected
	result, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"x","profile":"nope"}`)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(result.Content, "没有配置生图 profile") {
		t.Fatalf("result = %q", result.Content)
	}
}

func imageToolWithMediaForTest(t *testing.T, client *imagegen.Client, characters *character.Store) (ImageGenerateTool, *media.Manager) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root := t.TempDir()
	center := media.NewManager(store, root, &media.LocalBackend{Root: root})
	profiles := map[string]ImageProfile{}
	if client != nil {
		profiles[""] = ImageProfile{Name: "", Client: client, Config: client.Config()}
	}
	return NewImageGenerateTool(profiles, "", characters, center, nil, nil, nil), center
}

func seedImageCharacter(t *testing.T, store *character.Store, id, name, preset string, references ...string) {
	t.Helper()
	if _, err := store.Write(context.Background(), character.WriteRequest{
		ID:         id,
		Name:       name,
		Visibility: "public",
		Image:      &character.ImageSettings{PresetPrompt: preset, References: references},
	}, character.Viewer{Platform: "cli", ActorID: "cli:local", Superadmin: true}); err != nil {
		t.Fatalf("seed character %s: %v", id, err)
	}
}

func countImageSegments(result *tool.Result) int {
	if result == nil {
		return 0
	}
	count := 0
	for _, segment := range result.Segments {
		if segment.Type == llm.SegmentImage {
			count++
		}
	}
	return count
}

func TestImageGenerateMultipleCharactersInOneImage(t *testing.T) {
	var mu sync.Mutex
	var prompt string
	server := imageTestServer(t, func(value string) {
		mu.Lock()
		prompt = value
		mu.Unlock()
	})
	defer server.Close()

	characters := character.NewStore(t.TempDir())
	seedImageCharacter(t, characters, "catgirl", "猫娘", "CATGIRL LOOK")
	seedImageCharacter(t, characters, "foxgirl", "狐娘", "FOXGIRL LOOK")
	client := imagegen.New(imagegen.Config{Enabled: true, BaseURL: server.URL + "/v1", APIKey: "k", Optimize: "off"}, nil)
	generate, _ := imageToolWithMediaForTest(t, client, characters)

	result, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"两人在雨夜同框","character_ids":["catgirl","foxgirl"]}`)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	mu.Lock()
	got := prompt
	mu.Unlock()
	for _, want := range []string{"CATGIRL LOOK", "FOXGIRL LOOK", "猫娘", "狐娘", "画面必须同时出现以下全部角色"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt %q missing %q", got, want)
		}
	}
	if countImageSegments(result) != 1 || len(result.Outputs) != 0 {
		t.Fatalf("result segments=%d outputs=%d content=%q", countImageSegments(result), len(result.Outputs), result.Content)
	}
}

func TestImageGenerateAllActiveCharactersInOneImage(t *testing.T) {
	var prompt string
	server := imageTestServer(t, func(value string) { prompt = value })
	defer server.Close()

	characters := character.NewStore(t.TempDir())
	seedImageCharacter(t, characters, "catgirl", "猫娘", "CATGIRL LOOK")
	seedImageCharacter(t, characters, "foxgirl", "狐娘", "FOXGIRL LOOK")
	client := imagegen.New(imagegen.Config{Enabled: true, BaseURL: server.URL + "/v1", APIKey: "k", Optimize: "off"}, nil)
	generate, _ := imageToolWithMediaForTest(t, client, characters)

	ctx := character.WithActive(context.Background(), "catgirl", "foxgirl")
	result, err := generate.Call(ctx, tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"两人在雨夜同框"}`)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	for _, want := range []string{"CATGIRL LOOK", "FOXGIRL LOOK", "画面必须同时出现以下全部角色"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q missing %q", prompt, want)
		}
	}
	if countImageSegments(result) != 1 {
		t.Fatalf("result image segments = %d, want 1", countImageSegments(result))
	}
}

func TestImageGenerateAutoMatchesMultipleCharacters(t *testing.T) {
	var prompt string
	server := imageTestServer(t, func(value string) { prompt = value })
	defer server.Close()

	characters := character.NewStore(t.TempDir())
	seedImageCharacter(t, characters, "catgirl", "猫娘", "CATGIRL LOOK")
	seedImageCharacter(t, characters, "foxgirl", "狐娘", "FOXGIRL LOOK")
	client := imagegen.New(imagegen.Config{Enabled: true, BaseURL: server.URL + "/v1", APIKey: "k", Optimize: "off", AutoCharacter: true}, nil)
	generate, _ := imageToolWithMediaForTest(t, client, characters)

	result, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"画猫娘和狐娘一起喝咖啡"}`)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	for _, want := range []string{"CATGIRL LOOK", "FOXGIRL LOOK", "画面必须同时出现以下全部角色"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q missing %q", prompt, want)
		}
	}
	if countImageSegments(result) != 1 {
		t.Fatalf("result image segments = %d, want 1", countImageSegments(result))
	}
}

func TestImageGenerateCountMultipliesImagesWithoutSplittingCharacters(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
	defer server.Close()

	characters := character.NewStore(t.TempDir())
	seedImageCharacter(t, characters, "catgirl", "猫娘", "CATGIRL LOOK")
	seedImageCharacter(t, characters, "foxgirl", "狐娘", "FOXGIRL LOOK")
	client := imagegen.New(imagegen.Config{Enabled: true, BaseURL: server.URL + "/v1", APIKey: "k", Optimize: "off"}, nil)
	generate, _ := imageToolWithMediaForTest(t, client, characters)

	result, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(`{"prompt":"两人同框","character_ids":["catgirl","foxgirl"],"count":3,"send":true}`)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("upstream hits = %d, want 3", got)
	}
	if got := countImageSegments(result); got != 3 {
		t.Fatalf("image segments = %d, want 3", got)
	}
	if len(result.Outputs) != 3 {
		t.Fatalf("outputs = %d, want 3", len(result.Outputs))
	}
	if !strings.Contains(result.Content, "已生成 3/3 张图片") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestImageGenerateMultipleReferencesUseArrayPayload(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	var mu sync.Mutex
	var referenceValue any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		mu.Lock()
		referenceValue = body["image"]
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
	defer server.Close()

	client := imagegen.New(imagegen.Config{Enabled: true, BaseURL: server.URL + "/v1", APIKey: "k", Optimize: "off", SupportsReference: true}, nil)
	generate, center := imageToolWithMediaForTest(t, client, character.NewStore(t.TempDir()))
	first, err := center.ImportBytes(context.Background(), []byte("ref-one"), media.Input{Name: "one.png", MIMEType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := center.ImportBytes(context.Background(), []byte("ref-two"), media.Input{Name: "two.png", MIMEType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}

	args := fmt.Sprintf(`{"prompt":"x","reference_images":[%q,%q]}`, first.ID, second.ID)
	result, err := generate.Call(context.Background(), tool.CallRequest{Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if countImageSegments(result) != 1 {
		t.Fatalf("image segments = %d, want 1", countImageSegments(result))
	}
	mu.Lock()
	got := referenceValue
	mu.Unlock()
	values, ok := got.([]any)
	if !ok || len(values) != 2 {
		t.Fatalf("reference payload = %#v, want 2-element array", got)
	}
	for _, value := range values {
		if text, _ := value.(string); !strings.HasPrefix(text, "data:image/png;base64,") {
			t.Fatalf("reference value = %q", text)
		}
	}
}
