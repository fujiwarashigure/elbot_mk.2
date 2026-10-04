package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultWriteElbotHookSkillMarkdown(t *testing.T) {
	if !strings.Contains(defaultAgentSkillCreatorSkillTOML, `risk = "low"`) || !strings.Contains(defaultAgentSkillCreatorSkillTOML, `superadmin_only = true`) {
		t.Fatalf("default agent_skill_creator toml = %q", defaultAgentSkillCreatorSkillTOML)
	}
	for _, want := range []string{
		"name: write_elbot_hook",
		"description: 编写或修改 ElBot 规则 Hook 配置。",
		"hook路径：",
		"plugins/hooks.toml",
		"https://raw.githubusercontent.com/fujiwarashigure/elbot_mk.2/main/docs/hooks.md",
	} {
		if !strings.Contains(defaultWriteElbotHookSkillMD, want) {
			t.Fatalf("default write_elbot_hook skill missing %q", want)
		}
	}
	if !strings.Contains(defaultWriteElbotHookSkillTOML, `risk = "low"`) || !strings.Contains(defaultWriteElbotHookSkillTOML, `superadmin_only = true`) {
		t.Fatalf("default write_elbot_hook toml = %q", defaultWriteElbotHookSkillTOML)
	}
}

func TestResolvePathUsesExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.toml")
	resolved, err := ResolvePath(path)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if resolved != filepath.Clean(path) {
		t.Fatalf("resolved path = %q, want %q", resolved, filepath.Clean(path))
	}
}

func TestResolvePathUsesEnvConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env-app.toml")
	t.Setenv(EnvConfigFile, path)
	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if resolved != filepath.Clean(path) {
		t.Fatalf("resolved path = %q, want %q", resolved, filepath.Clean(path))
	}
}

func TestResolvePathGeneratesPlatformDefaultsWhenNoConfigExists(t *testing.T) {
	configHome := t.TempDir()
	setUserConfigDirEnv(t, configHome)
	t.Setenv(EnvConfigFile, "")

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	want, ok := platformDefaultConfigPath()
	if !ok {
		t.Fatal("platform default config path unavailable")
	}
	if resolved != filepath.Clean(want) {
		t.Fatalf("resolved path = %q, want %q", resolved, filepath.Clean(want))
	}
	for _, rel := range []string{"app.toml", "services.toml", "state.toml", "SOUL.md", "memories.toml", "elnis.toml", filepath.Join("skills", "agent", "agent_skill_creator", "SKILL.md"), filepath.Join("skills", "agent", "agent_skill_creator", "ELBOT_SKILL.toml"), filepath.Join("skills", "agent", "write_elbot_hook", "SKILL.md"), filepath.Join("skills", "agent", "write_elbot_hook", "ELBOT_SKILL.toml"), filepath.Join("plugins", ".env"), ".env.example"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(want), rel)); err != nil {
			t.Fatalf("expected generated file %s: %v", rel, err)
		}
	}
	envExamplePath := filepath.Join(filepath.Dir(want), ".env.example")
	envExampleData, err := os.ReadFile(envExamplePath)
	if err != nil {
		t.Fatalf("read generated .env.example: %v", err)
	}
	if !strings.Contains(string(envExampleData), "JINA_API_KEY=") {
		t.Fatalf("generated .env.example is missing JINA_API_KEY: %q", string(envExampleData))
	}
	servicesData, err := os.ReadFile(filepath.Join(filepath.Dir(want), "services.toml"))
	if err != nil {
		t.Fatalf("read generated services.toml: %v", err)
	}
	if !strings.Contains(string(servicesData), "# [image_to_prompt]") {
		t.Fatalf("generated services.toml is missing the image_to_prompt example: %q", string(servicesData))
	}
	hookEnvData, err := os.ReadFile(filepath.Join(filepath.Dir(want), "plugins", ".env"))
	if err != nil {
		t.Fatalf("read generated plugins/.env: %v", err)
	}
	if !strings.Contains(string(hookEnvData), "# PATH=/absolute/path/to/bin") {
		t.Fatalf("generated plugins/.env is missing PATH guidance: %q", string(hookEnvData))
	}
	elnisData, err := os.ReadFile(filepath.Join(filepath.Dir(want), "elnis.toml"))
	if err != nil {
		t.Fatalf("read generated elnis.toml: %v", err)
	}
	for _, setting := range []string{
		"read_header_timeout_seconds = 5",
		"read_timeout_seconds = 30",
		"write_timeout_seconds = 300",
		"idle_timeout_seconds = 60",
	} {
		if !strings.Contains(string(elnisData), setting) {
			t.Fatalf("generated elnis.toml is missing %q", setting)
		}
	}
	creatorTomlPath := filepath.Join(filepath.Dir(want), "skills", "agent", "agent_skill_creator", "ELBOT_SKILL.toml")
	creatorTomlData, err := os.ReadFile(creatorTomlPath)
	if err != nil {
		t.Fatalf("read generated agent_skill_creator toml: %v", err)
	}
	if !strings.Contains(string(creatorTomlData), `risk = "low"`) || !strings.Contains(string(creatorTomlData), `superadmin_only = true`) {
		t.Fatalf("generated agent_skill_creator toml = %q", string(creatorTomlData))
	}
	skillPath := filepath.Join(filepath.Dir(want), "skills", "agent", "write_elbot_hook", "SKILL.md")
	skillData, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read generated write_elbot_hook skill: %v", err)
	}
	if !strings.Contains(string(skillData), "hook路径：") || !strings.Contains(string(skillData), "plugins/hooks.toml") {
		t.Fatalf("generated write_elbot_hook skill = %q", string(skillData))
	}
	tomlPath := filepath.Join(filepath.Dir(want), "skills", "agent", "write_elbot_hook", "ELBOT_SKILL.toml")
	tomlData, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("read generated write_elbot_hook toml: %v", err)
	}
	if !strings.Contains(string(tomlData), `risk = "low"`) || !strings.Contains(string(tomlData), `superadmin_only = true`) {
		t.Fatalf("generated write_elbot_hook toml = %q", string(tomlData))
	}
	for _, rel := range []string{filepath.Join("skills", "agent"), filepath.Join("skills", "go"), "plugins", "long_memory"} {
		info, err := os.Stat(filepath.Join(filepath.Dir(want), rel))
		if err != nil {
			t.Fatalf("expected generated dir %s: %v", rel, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", rel)
		}
	}
	cfg, err := Load(resolved)
	if err != nil {
		t.Fatalf("Load generated config: %v", err)
	}
	if cfg.ConfigPath != filepath.Clean(want) {
		t.Fatalf("generated ConfigPath = %q, want %q", cfg.ConfigPath, filepath.Clean(want))
	}
	if cfg.Elnis.Enabled {
		t.Fatal("generated Elnis config should be disabled")
	}
	if cfg.Elnis.HTTP.ReadHeaderTimeoutSeconds != 5 || cfg.Elnis.HTTP.ReadTimeoutSeconds != 30 || cfg.Elnis.HTTP.WriteTimeoutSeconds != 300 || cfg.Elnis.HTTP.IdleTimeoutSeconds != 60 {
		t.Fatalf("generated Elnis HTTP timeouts = %#v", cfg.Elnis.HTTP)
	}
}

func TestEnsurePlatformDefaultsDoesNotOverwriteExistingFiles(t *testing.T) {
	configHome := t.TempDir()
	setUserConfigDirEnv(t, configHome)
	configPath, ok := platformDefaultConfigPath()
	if !ok {
		t.Fatal("platform default config path unavailable")
	}
	custom := "# custom app\n"
	writeFile(t, configPath, custom)
	customHookEnv := "TOKEN=custom\n"
	hookEnvPath := filepath.Join(filepath.Dir(configPath), "plugins", ".env")
	writeFile(t, hookEnvPath, customHookEnv)

	generated, err := EnsurePlatformDefaults()
	if err != nil {
		t.Fatalf("EnsurePlatformDefaults: %v", err)
	}
	if generated != configPath {
		t.Fatalf("generated path = %q, want %q", generated, configPath)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != custom {
		t.Fatalf("existing app.toml was overwritten: %q", string(data))
	}
	hookEnvData, err := os.ReadFile(hookEnvPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(hookEnvData) != customHookEnv {
		t.Fatalf("existing plugins/.env was overwritten: %q", string(hookEnvData))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), "elnis.toml")); err != nil {
		t.Fatalf("expected missing assets to be created: %v", err)
	}
}

func TestLoadSplitConfig(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	appPath := filepath.Join(configDir, "app.toml")
	providersPath := filepath.Join(configDir, "providers.toml")
	statePath := filepath.Join(configDir, "state.toml")
	toolTagsPath := filepath.Join(configDir, "tool_tags.toml")
	writeFile(t, appPath, `
[config_files]
providers = "providers.toml"
state = "state.toml"
tool_tags = "tool_tags.toml"

[soul]
path = "SOUL.md"

[storage]
sessions_sqlite_path = "../data/elbot_sessions.db"
chat_history_sqlite_path = "../data/elbot_chat_history.db"

[runtime]
log_level = "debug"
log_retention_days = 14

[context]
compact_enabled = true
compact_trigger_ratio = 0.75

[view]
session_list_page_size = 7

[commands]
prefixes = ["/", "-"]

[tools]
max_rounds_per_turn = 3

[maintenance.log_cleanup]
enabled = true
schedule = "0 4 * * *"

[maintenance.session_cleanup]
enabled = true
schedule = "15 3 * * *"
retention_days = 14

[maintenance.sandbox_cleanup]
enabled = true
schedule = "0 5 * * *"
retention_days = 9

[sandbox]
root = "../data/sandbox"

[file_delivery]
max_direct_base64_bytes = 123456
backend = "base64"
s3_endpoint = "https://r2.example"
s3_region = "auto"
s3_bucket = "elbot-files"
s3_access_key_env = "ELBOT_TEST_S3_ACCESS"
s3_secret_key_env = "ELBOT_TEST_S3_SECRET"
s3_public_base_url = "https://files.example"

[platform_files]
max_receive_file_bytes = 456789
download_timeout_secs = 12

[platform.qqonebot]
enabled = true
ws_url = "ws://example"
trigger_keywords = ["芙莉丝"]

[session]

[session.idle_expiration]
group_user_ttl_minutes = 0
group_superadmin_ttl_minutes = 12
private_user_ttl_minutes = 13
private_superadmin_ttl_minutes = 14

[session.naming]
trigger_step = 3
`)
	writeFile(t, providersPath, `
[providers.deepseek]
base_url = "https://api.deepseek.com"
api_key = "${DUMMY_API_KEY}"
models = ["deepseek-v4-flash"]
extra_payload = { provider_field = "provider" }

[providers.deepseek.model_configs."deepseek-v4-flash"]
context_window = 64000
extra_payload = { thinking = { type = "disabled" }, provider_field = "model" }

[model_metadata]
default_context_window = 12345
`)
	writeFile(t, statePath, `
[session]
default_mode = "chat"

[mode_models.work]
provider = "deepseek"
model = "deepseek-state"

[mode_models.chat]
provider = "deepseek"
model = "deepseek-chat-state"

[naming_model]
provider = "deepseek"
model = "deepseek-title"

[compact_model]
provider = "deepseek"
model = "deepseek-compact"
`)
	writeFile(t, toolTagsPath, `
[tags.web]
tools = ["web_search", "web_extract"]
prompt = "Use web tools."

[tags.agent]
tools = ["read_file", "shell"]
prompt = "Use agent tools."
`)

	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ConfigPath != filepath.Clean(appPath) {
		t.Fatalf("ConfigPath = %q, want %q", cfg.ConfigPath, filepath.Clean(appPath))
	}
	if cfg.ProvidersConfigPath != filepath.Clean(providersPath) {
		t.Fatalf("ProvidersConfigPath = %q, want %q", cfg.ProvidersConfigPath, filepath.Clean(providersPath))
	}
	if cfg.StateConfigPath != filepath.Clean(statePath) {
		t.Fatalf("StateConfigPath = %q, want %q", cfg.StateConfigPath, filepath.Clean(statePath))
	}
	if cfg.ToolTagsConfigPath != filepath.Clean(toolTagsPath) {
		t.Fatalf("ToolTagsConfigPath = %q, want %q", cfg.ToolTagsConfigPath, filepath.Clean(toolTagsPath))
	}
	wantToolTags := ToolTagsConfig{Tags: map[string]ToolTagConfig{
		"web":   {Tools: []string{"web_search", "web_extract"}, Prompt: "Use web tools."},
		"agent": {Tools: []string{"read_file", "shell"}, Prompt: "Use agent tools."},
	}}
	if !reflect.DeepEqual(cfg.ToolTags, wantToolTags) {
		t.Fatalf("ToolTags = %#v, want %#v", cfg.ToolTags, wantToolTags)
	}
	wantDB := filepath.Clean(filepath.Join(configDir, "../data/elbot_sessions.db"))
	if cfg.Storage.SessionsSQLitePath != wantDB {
		t.Fatalf("SessionsSQLitePath = %q, want %q", cfg.Storage.SessionsSQLitePath, wantDB)
	}
	wantChatHistoryDB := filepath.Clean(filepath.Join(configDir, "../data/elbot_chat_history.db"))
	if cfg.Storage.ChatHistorySQLitePath != wantChatHistoryDB {
		t.Fatalf("ChatHistorySQLitePath = %q, want %q", cfg.Storage.ChatHistorySQLitePath, wantChatHistoryDB)
	}
	if cfg.Runtime.LogLevel != "debug" || cfg.Runtime.LogRetentionDays != 14 {
		t.Fatalf("runtime = %#v", cfg.Runtime)
	}
	if !cfg.Context.CompactEnabled {
		t.Fatal("CompactEnabled = false")
	}
	if cfg.Context.CompactTriggerRatio != 0.75 {
		t.Fatalf("CompactTriggerRatio = %v", cfg.Context.CompactTriggerRatio)
	}
	if cfg.ModelMetadata.DefaultContextWindow != 12345 {
		t.Fatalf("DefaultContextWindow = %d", cfg.ModelMetadata.DefaultContextWindow)
	}
	if !reflect.DeepEqual(cfg.Commands.Prefixes, []string{"/", "-"}) {
		t.Fatalf("Command prefixes = %#v", cfg.Commands.Prefixes)
	}
	if cfg.View.SessionListPageSize != 7 {
		t.Fatalf("session list page size = %d", cfg.View.SessionListPageSize)
	}
	if cfg.Tools.MaxRoundsPerTurn != 3 {
		t.Fatalf("max tool rounds per turn = %d", cfg.Tools.MaxRoundsPerTurn)
	}
	wantMaintenance := MaintenanceConfig{
		LogCleanup:         CronTaskConfig{Enabled: true, Schedule: "0 4 * * *"},
		SessionCleanup:     MaintenanceCleanupConfig{Enabled: true, Schedule: "15 3 * * *", RetentionDays: 14},
		SandboxCleanup:     MaintenanceCleanupConfig{Enabled: true, Schedule: "0 5 * * *", RetentionDays: 9},
		ChatHistoryCleanup: ChatHistoryCleanupConfig{Schedule: "35 4 * * *", RetentionDays: 180},
		DailyReport:        DailyReportConfig{Schedule: "0 9,21 * * *", WindowHours: 12, Provider: "deepseek", Currency: "CNY"},
	}
	if !reflect.DeepEqual(cfg.Maintenance, wantMaintenance) {
		t.Fatalf("maintenance = %#v, want %#v", cfg.Maintenance, wantMaintenance)
	}
	if cfg.Sandbox.Root != filepath.Clean(filepath.Join(configDir, "../data/sandbox")) {
		t.Fatalf("sandbox root = %q", cfg.Sandbox.Root)
	}
	wantFileDelivery := FileDeliveryConfig{MaxDirectBase64Bytes: 123456, Backend: "base64", S3Endpoint: "https://r2.example", S3Region: "auto", S3Bucket: "elbot-files", S3AccessKeyEnv: "ELBOT_TEST_S3_ACCESS", S3SecretKeyEnv: "ELBOT_TEST_S3_SECRET", S3PublicBaseURL: "https://files.example"}
	if !reflect.DeepEqual(cfg.FileDelivery, wantFileDelivery) {
		t.Fatalf("file_delivery = %#v, want %#v", cfg.FileDelivery, wantFileDelivery)
	}
	wantPlatformFiles := PlatformFilesConfig{MaxReceiveFileBytes: 456789, DownloadTimeoutSecs: 12}
	if !reflect.DeepEqual(cfg.PlatformFiles, wantPlatformFiles) {
		t.Fatalf("platform_files = %#v, want %#v", cfg.PlatformFiles, wantPlatformFiles)
	}
	wantNaming := SessionNamingConfig{TriggerStep: 3}
	if !reflect.DeepEqual(cfg.Session.Naming, wantNaming) {
		t.Fatalf("naming = %#v, want %#v", cfg.Session.Naming, wantNaming)
	}
	qqConfig := cfg.Platform["qqonebot"]
	if qqConfig["enabled"] != true || qqConfig["ws_url"] != "ws://example" {
		t.Fatalf("qq onebot platform config = %#v", qqConfig)
	}
	keywords, ok := qqConfig["trigger_keywords"].([]any)
	if !ok || len(keywords) != 1 || keywords[0] != "芙莉丝" {
		t.Fatalf("trigger keywords = %#v", qqConfig["trigger_keywords"])
	}
	if _, ok := cfg.Platform["qq_onebot"]; ok {
		t.Fatal("config should not keep legacy qq_onebot platform name")
	}
	wantIdleExpiration := SessionIdleExpirationConfig{GroupUserTTLMinutes: 0, GroupSuperadminTTLMinutes: 12, PrivateUserTTLMinutes: 13, PrivateSuperadminTTLMinutes: 14}
	if !reflect.DeepEqual(cfg.Session.IdleExpiration, wantIdleExpiration) {
		t.Fatalf("idle expiration = %#v, want %#v", cfg.Session.IdleExpiration, wantIdleExpiration)
	}
	if cfg.NamingModel.Provider != "deepseek" || cfg.NamingModel.Model != "deepseek-title" {
		t.Fatalf("naming model = %q/%q", cfg.NamingModel.Provider, cfg.NamingModel.Model)
	}
	if cfg.CompactModel.Provider != "deepseek" || cfg.CompactModel.Model != "deepseek-compact" {
		t.Fatalf("compact model = %q/%q", cfg.CompactModel.Provider, cfg.CompactModel.Model)
	}
	if cfg.Session.DefaultMode != "chat" {
		t.Fatalf("default mode = %q", cfg.Session.DefaultMode)
	}
	if cfg.ModeModels["work"].Provider != "deepseek" || cfg.ModeModels["work"].Model != "deepseek-state" {
		t.Fatalf("work model = %q/%q", cfg.ModeModels["work"].Provider, cfg.ModeModels["work"].Model)
	}
	if cfg.ModeModels["chat"].Provider != "deepseek" || cfg.ModeModels["chat"].Model != "deepseek-chat-state" {
		t.Fatalf("chat model = %q/%q", cfg.ModeModels["chat"].Provider, cfg.ModeModels["chat"].Model)
	}
	if cfg.Soul.Path != filepath.Clean(filepath.Join(configDir, "SOUL.md")) {
		t.Fatalf("Soul.Path = %q", cfg.Soul.Path)
	}
	provider := cfg.Providers["deepseek"]
	if provider.APIKey != "${DUMMY_API_KEY}" {
		t.Fatalf("provider api key = %q", provider.APIKey)
	}
	if provider.ExtraPayload["provider_field"] != "provider" {
		t.Fatalf("provider extra payload = %#v", provider.ExtraPayload)
	}
	modelCfg := provider.ModelConfigs["deepseek-v4-flash"]
	if modelCfg.ContextWindow != 64000 {
		t.Fatalf("model context window = %d", modelCfg.ContextWindow)
	}
	thinking, ok := modelCfg.ExtraPayload["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" || modelCfg.ExtraPayload["provider_field"] != "model" {
		t.Fatalf("model config extra payload = %#v", modelCfg.ExtraPayload)
	}
}

func TestLoadServicesConfig(t *testing.T) {
	configDir := t.TempDir()
	appPath := filepath.Join(configDir, "app.toml")
	servicesPath := filepath.Join(configDir, "services.toml")
	statePath := filepath.Join(configDir, "state.toml")

	writeFile(t, appPath, `
[config_files]
services = "services.toml"
state = "state.toml"

# Legacy location: services.toml must override this whole section.
[image_generation]
enabled = false
base_url = "https://legacy.example/v1"
model = "legacy-image"
`)
	writeFile(t, servicesPath, `
[providers.central]
base_url = "https://central.example/v1"
api_key_env = "CENTRAL_API_KEY"
models = ["central-fast", "central-pro"]

[model_metadata]
default_context_window = 64000

[model_profiles.fast]
provider = "central"
model = "central-fast"
aliases = ["快"]

[image_generation]
enabled = true
base_url = "https://images.example/v1"
api_key_env = "IMAGE_API_KEY"
model = "central-image"

[image_generation.profiles.hq]
model = "central-image-hq"
quality = "high"

[image_to_prompt]
provider = "central"
model = "central-vision"
max_tokens = 256
`)
	writeFile(t, statePath, `
[mode_models.work]
provider = "central"
model = "central-pro"

[mode_models.chat]
provider = "central"
model = "central-fast"
`)

	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ServicesConfigPath != filepath.Clean(servicesPath) {
		t.Fatalf("ServicesConfigPath = %q, want %q", cfg.ServicesConfigPath, filepath.Clean(servicesPath))
	}
	if cfg.ProvidersConfigPath != "" {
		t.Fatalf("ProvidersConfigPath = %q, want empty when services is used", cfg.ProvidersConfigPath)
	}
	provider, ok := cfg.Providers["central"]
	if !ok {
		t.Fatalf("central provider missing: %#v", cfg.Providers)
	}
	if provider.BaseURL != "https://central.example/v1" || len(provider.Models) != 2 {
		t.Fatalf("central provider = %#v", provider)
	}
	if cfg.ModelMetadata.DefaultContextWindow != 64000 {
		t.Fatalf("DefaultContextWindow = %d", cfg.ModelMetadata.DefaultContextWindow)
	}
	if profile := cfg.ModelProfiles["fast"]; profile.Model != "central-fast" {
		t.Fatalf("model profile = %#v", profile)
	}
	if !cfg.ImageGeneration.Enabled || cfg.ImageGeneration.BaseURL != "https://images.example/v1" || cfg.ImageGeneration.Model != "central-image" {
		t.Fatalf("image_generation = %#v", cfg.ImageGeneration)
	}
	if profile := cfg.ImageGeneration.Profiles["hq"]; profile.Model != "central-image-hq" || profile.Quality != "high" {
		t.Fatalf("image profile = %#v", profile)
	}
	if !cfg.ImageToPrompt.IsEnabled() || cfg.ImageToPrompt.Provider != "central" || cfg.ImageToPrompt.Model != "central-vision" {
		t.Fatalf("image_to_prompt = %#v", cfg.ImageToPrompt)
	}
	if cfg.ImageToPrompt.MaxTokens != 256 || cfg.ImageToPrompt.MaxEdge != 1536 || cfg.ImageToPrompt.TemperatureValue() != 0.2 {
		t.Fatalf("image_to_prompt defaults = %#v", cfg.ImageToPrompt)
	}
}

func TestImageToPromptConfigDefaultsAndEnabled(t *testing.T) {
	cfg := Default()
	if cfg.ImageToPrompt.MaxTokens != 400 || cfg.ImageToPrompt.TemperatureValue() != 0.2 || cfg.ImageToPrompt.MaxEdge != 1536 || cfg.ImageToPrompt.MaxImageBytes != 12*1024*1024 {
		t.Fatalf("defaults = %#v", cfg.ImageToPrompt)
	}
	if cfg.ImageToPrompt.IsEnabled() {
		t.Fatal("image_to_prompt should stay disabled without provider/model")
	}
	cfg.ImageToPrompt.Provider = "openai"
	cfg.ImageToPrompt.Model = "gpt-4o-mini"
	if !cfg.ImageToPrompt.IsEnabled() {
		t.Fatal("provider+model should enable image_to_prompt")
	}
	disabled := false
	cfg.ImageToPrompt.Enabled = &disabled
	if cfg.ImageToPrompt.IsEnabled() {
		t.Fatal("enabled=false should disable image_to_prompt")
	}
}

func TestImageToPromptExplicitValuesSurviveDefaults(t *testing.T) {
	zeroTemperature := 0.0
	cfg := Config{ImageToPrompt: ImageToPromptConfig{Temperature: &zeroTemperature}}
	cfg.applyAppDefaults()
	if cfg.ImageToPrompt.Temperature == nil || *cfg.ImageToPrompt.Temperature != 0 {
		t.Fatalf("explicit zero temperature was overwritten: %#v", cfg.ImageToPrompt.Temperature)
	}
	if got := cfg.ImageToPrompt.TemperatureValue(); got != 0 {
		t.Fatalf("TemperatureValue = %v, want 0", got)
	}
}

func TestLoadServicesWithoutImageKeepsLegacyImage(t *testing.T) {
	configDir := t.TempDir()
	appPath := filepath.Join(configDir, "app.toml")
	servicesPath := filepath.Join(configDir, "services.toml")
	statePath := filepath.Join(configDir, "state.toml")

	writeFile(t, appPath, `
[config_files]
services = "services.toml"
state = "state.toml"

[image_generation]
enabled = true
base_url = "https://legacy.example/v1"
model = "legacy-image"
`)
	writeFile(t, servicesPath, `
[providers.central]
base_url = "https://central.example/v1"
api_key_env = "CENTRAL_API_KEY"
`)
	writeFile(t, statePath, `
[mode_models.work]
provider = "central"
model = "central-fast"

[mode_models.chat]
provider = "central"
model = "central-fast"
`)

	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.ImageGeneration.Enabled || cfg.ImageGeneration.BaseURL != "https://legacy.example/v1" || cfg.ImageGeneration.Model != "legacy-image" {
		t.Fatalf("legacy image_generation lost: %#v", cfg.ImageGeneration)
	}
}

func TestLoadRejectsStateSharingServicesFile(t *testing.T) {
	configDir := t.TempDir()
	appPath := filepath.Join(configDir, "app.toml")
	servicesPath := filepath.Join(configDir, "services.toml")

	writeFile(t, appPath, `
[config_files]
services = "services.toml"
state = "services.toml"
`)
	writeFile(t, servicesPath, `
[providers.central]
base_url = "https://central.example/v1"
api_key_env = "CENTRAL_API_KEY"
`)

	_, err := Load(appPath)
	if err == nil {
		t.Fatal("expected error when state and services share a file")
	}
	if !strings.Contains(err.Error(), "dedicated writable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	appPath := filepath.Join(configDir, "app.toml")
	providersPath := filepath.Join(configDir, "providers.toml")
	writeFile(t, appPath, ``)
	writeFile(t, providersPath, ``)
	writeFile(t, filepath.Join(configDir, "state.toml"), `
[mode_models.work]
provider = "deepseek"
model = "deepseek-v4-flash"

[mode_models.chat]
provider = "deepseek"
model = "deepseek-chat"
`)

	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ProvidersConfigPath != filepath.Clean(providersPath) {
		t.Fatalf("ProvidersConfigPath = %q", cfg.ProvidersConfigPath)
	}
	if cfg.StateConfigPath != filepath.Clean(filepath.Join(configDir, "state.toml")) {
		t.Fatalf("StateConfigPath = %q", cfg.StateConfigPath)
	}
	if cfg.Runtime.LogLevel != "info" || cfg.Runtime.LogRetentionDays != 30 {
		t.Fatalf("runtime defaults = %#v", cfg.Runtime)
	}
	if cfg.Context.CompactTriggerRatio != 0.8 {
		t.Fatalf("CompactTriggerRatio = %v", cfg.Context.CompactTriggerRatio)
	}
	if cfg.ModelMetadata.DefaultContextWindow != 256000 {
		t.Fatalf("DefaultContextWindow = %d", cfg.ModelMetadata.DefaultContextWindow)
	}
	if !cfg.CharacterLibrary.IsEnabled() || cfg.CharacterLibrary.Root != filepath.Join(configDir, "characters") {
		t.Fatalf("character library defaults = %#v", cfg.CharacterLibrary)
	}
	if !reflect.DeepEqual(cfg.Commands.Prefixes, []string{"/*"}) {
		t.Fatalf("Command prefixes = %#v", cfg.Commands.Prefixes)
	}
	if cfg.Tools.MaxRoundsPerTurn != 2 {
		t.Fatalf("default max tool rounds per turn = %d", cfg.Tools.MaxRoundsPerTurn)
	}
	if cfg.View.SessionListPageSize != 10 {
		t.Fatalf("default session list page size = %d", cfg.View.SessionListPageSize)
	}
	if cfg.Maintenance.LogCleanup.Schedule != "0 3 * * *" || cfg.Maintenance.SessionCleanup.Schedule != "15 3 * * *" || cfg.Maintenance.SessionCleanup.RetentionDays != 30 || cfg.Maintenance.SandboxCleanup.Schedule != "0 4 * * *" || cfg.Maintenance.SandboxCleanup.RetentionDays != 7 {
		t.Fatalf("maintenance defaults = %#v", cfg.Maintenance)
	}
	if cfg.Sandbox.Root != filepath.Clean(filepath.Join(platformDefaultDataDir(), "sandbox")) {
		t.Fatalf("sandbox root default = %q", cfg.Sandbox.Root)
	}
	wantFileDelivery := FileDeliveryConfig{MaxDirectBase64Bytes: 8 * 1024 * 1024, Backend: "base64", S3Region: "auto"}
	if !reflect.DeepEqual(cfg.FileDelivery, wantFileDelivery) {
		t.Fatalf("file_delivery defaults = %#v, want %#v", cfg.FileDelivery, wantFileDelivery)
	}
	wantPlatformFiles := PlatformFilesConfig{MaxReceiveFileBytes: 100 * 1024 * 1024, DownloadTimeoutSecs: 60}
	if !reflect.DeepEqual(cfg.PlatformFiles, wantPlatformFiles) {
		t.Fatalf("platform_files defaults = %#v, want %#v", cfg.PlatformFiles, wantPlatformFiles)
	}
	if cfg.Session.Naming.TriggerStep != 1 {
		t.Fatalf("naming trigger step = %d", cfg.Session.Naming.TriggerStep)
	}
	if cfg.Session.DefaultMode != "work" {
		t.Fatalf("default mode = %q", cfg.Session.DefaultMode)
	}
	wantIdleExpiration := SessionIdleExpirationConfig{GroupUserTTLMinutes: 10, GroupSuperadminTTLMinutes: 10, PrivateUserTTLMinutes: 10, PrivateSuperadminTTLMinutes: 0}
	if !reflect.DeepEqual(cfg.Session.IdleExpiration, wantIdleExpiration) {
		t.Fatalf("default idle expiration = %#v, want %#v", cfg.Session.IdleExpiration, wantIdleExpiration)
	}
	wantDB := filepath.Clean(filepath.Join(platformDefaultDataDir(), "elbot_sessions.db"))
	if cfg.Storage.SessionsSQLitePath != wantDB {
		t.Fatalf("SessionsSQLitePath = %q, want %q", cfg.Storage.SessionsSQLitePath, wantDB)
	}
	wantChatHistoryDB := filepath.Clean(filepath.Join(platformDefaultDataDir(), "elbot_chat_history.db"))
	if cfg.Storage.ChatHistorySQLitePath != wantChatHistoryDB {
		t.Fatalf("ChatHistorySQLitePath = %q, want %q", cfg.Storage.ChatHistorySQLitePath, wantChatHistoryDB)
	}
	if cfg.Soul.Path != filepath.Clean(filepath.Join(configDir, "SOUL.md")) {
		t.Fatalf("Soul.Path = %q", cfg.Soul.Path)
	}
	if cfg.Providers == nil {
		t.Fatal("Providers map is nil")
	}
	if got := cfg.ResidentMemory.NormalWriteMinIntervalSecondsValue(); got != 5 {
		t.Fatalf("normal write min interval default = %d, want 5", got)
	}
	if got := cfg.ResidentMemory.NormalWriteWindowSecondsValue(); got != 60 {
		t.Fatalf("normal write window default = %d, want 60", got)
	}
	if got := cfg.ResidentMemory.NormalWriteMaxPerWindowValue(); got != 6 {
		t.Fatalf("normal write max per window default = %d, want 6", got)
	}
	if got := cfg.ResidentMemory.NormalMaxLinesValue(); got != 20 {
		t.Fatalf("normal max lines default = %d, want 20", got)
	}
	if got := cfg.ResidentMemory.NormalMaxUnitsPerEntryValue(); got != 80 {
		t.Fatalf("normal max units per entry default = %d, want 80", got)
	}
	if !cfg.ResidentMemory.IsNormalBlockInstructionPatterns() {
		t.Fatal("normal instruction pattern blocking should default to true")
	}
}

func TestLoadResidentMemoryWritePolicy(t *testing.T) {
	configDir := t.TempDir()
	appPath := filepath.Join(configDir, "app.toml")
	writeFile(t, appPath, `
[config_files]
state = "state.toml"

[resident_memory]
normal_write_min_interval_seconds = 0
normal_write_window_seconds = 120
normal_write_max_per_window = 2
normal_max_lines = 0
normal_max_units_per_entry = 12
normal_block_instruction_patterns = false
`)
	writeFile(t, filepath.Join(configDir, "providers.toml"), `
[providers.central]
base_url = "https://central.example/v1"
api_key_env = "CENTRAL_API_KEY"
`)
	writeFile(t, filepath.Join(configDir, "state.toml"), `
[mode_models.work]
provider = "central"
model = "central-fast"

[mode_models.chat]
provider = "central"
model = "central-fast"
`)

	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ResidentMemory.NormalWriteMinIntervalSecondsValue(); got != 0 {
		t.Fatalf("min interval = %d, want 0", got)
	}
	if got := cfg.ResidentMemory.NormalWriteWindowSecondsValue(); got != 120 {
		t.Fatalf("window = %d, want 120", got)
	}
	if got := cfg.ResidentMemory.NormalWriteMaxPerWindowValue(); got != 2 {
		t.Fatalf("max per window = %d, want 2", got)
	}
	if got := cfg.ResidentMemory.NormalMaxLinesValue(); got != 0 {
		t.Fatalf("max lines = %d, want 0", got)
	}
	if got := cfg.ResidentMemory.NormalMaxUnitsPerEntryValue(); got != 12 {
		t.Fatalf("max units per entry = %d, want 12", got)
	}
	if cfg.ResidentMemory.IsNormalBlockInstructionPatterns() {
		t.Fatal("instruction pattern blocking should be disabled")
	}
}

func TestSaveState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "state.toml")
	state := StateConfig{
		Session: StateSessionConfig{DefaultMode: "chat"},
		ModeModels: map[string]ModelSelection{
			"work": {Provider: "zhipu", Model: "glm-4-flash"},
			"chat": {Provider: "zhipu", Model: "glm-4-air"},
		},
		NamingModel:  ModelSelection{Provider: "deepseek", Model: "deepseek-title"},
		CompactModel: ModelSelection{Provider: "zhipu", Model: "glm-compact"},
	}
	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded := &StateConfig{}
	if err := loadTOML(path, loaded); err != nil {
		t.Fatalf("load saved state: %v", err)
	}
	if !reflect.DeepEqual(*loaded, state) {
		t.Fatalf("state = %#v, want %#v", *loaded, state)
	}
}

func TestSaveStateRemovesBackupAndTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")
	if err := SaveState(path, StateConfig{Session: StateSessionConfig{DefaultMode: "work"}}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".bak") || strings.Contains(name, ".tmp-") {
			t.Fatalf("stale state artifact left behind: %s", name)
		}
	}
}

func TestLoadStateRecoversBackupAfterInterruptedSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")
	backup := path + ".bak"
	if err := SaveState(backup, StateConfig{Session: StateSessionConfig{DefaultMode: "chat"}}); err != nil {
		t.Fatalf("SaveState backup: %v", err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState fallback: %v", err)
	}
	if loaded.Session.DefaultMode != "chat" {
		t.Fatalf("recovered mode = %q, want chat", loaded.Session.DefaultMode)
	}
}

func TestLoadMissingAppConfig(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	t.Fatalf("expected os.ErrNotExist, got %v", err)
}

func TestLoadMissingProvidersConfig(t *testing.T) {
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.toml")
	writeFile(t, appPath, `
[config_files]
providers = "missing.toml"
`)

	_, err := Load(appPath)
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	t.Fatalf("expected os.ErrNotExist, got %v", err)
}

func TestLoadProviderAPIKeyEnv(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	appPath := filepath.Join(configDir, "app.toml")
	writeFile(t, appPath, ``)
	writeFile(t, filepath.Join(configDir, "providers.toml"), `
[providers.deepseek]
base_url = "https://api.deepseek.com"
api_key_env = "ELBOT_TEST_DEEPSEEK_API_KEY"
`)
	writeFile(t, filepath.Join(configDir, "state.toml"), `
[mode_models.work]
provider = "deepseek"
model = "deepseek-chat"

[mode_models.chat]
provider = "deepseek"
model = "deepseek-chat"
`)
	writeFile(t, filepath.Join(configDir, ".env"), `ELBOT_TEST_DEEPSEEK_API_KEY=from-dotenv
`)

	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Providers["deepseek"].APIKey != "from-dotenv" {
		t.Fatalf("api key = %q", cfg.Providers["deepseek"].APIKey)
	}
}

func TestLoadProviderAPIKeyEnvMissingDoesNotFail(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	appPath := filepath.Join(configDir, "app.toml")
	writeFile(t, appPath, ``)
	writeFile(t, filepath.Join(configDir, "providers.toml"), `
[providers.deepseek]
base_url = "https://api.deepseek.com"
api_key_env = "ELBOT_TEST_MISSING_API_KEY"
`)
	writeFile(t, filepath.Join(configDir, "state.toml"), `
[mode_models.work]
provider = "deepseek"
model = "deepseek-chat"

[mode_models.chat]
provider = "deepseek"
model = "deepseek-chat"
`)

	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Providers["deepseek"].APIKey != "" {
		t.Fatalf("api key = %q", cfg.Providers["deepseek"].APIKey)
	}
}

func TestLoadProviderAPIKeyEnvPrefersOS(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	appPath := filepath.Join(configDir, "app.toml")
	writeFile(t, appPath, ``)
	writeFile(t, filepath.Join(configDir, "providers.toml"), `
[providers.deepseek]
base_url = "https://api.deepseek.com"
api_key_env = "ELBOT_TEST_DEEPSEEK_API_KEY"
`)
	writeFile(t, filepath.Join(configDir, "state.toml"), `
[mode_models.work]
provider = "deepseek"
model = "deepseek-chat"

[mode_models.chat]
provider = "deepseek"
model = "deepseek-chat"
`)
	writeFile(t, filepath.Join(configDir, ".env"), `ELBOT_TEST_DEEPSEEK_API_KEY=from-dotenv
`)
	t.Setenv("ELBOT_TEST_DEEPSEEK_API_KEY", "from-os")

	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Providers["deepseek"].APIKey != "from-os" {
		t.Fatalf("api key = %q", cfg.Providers["deepseek"].APIKey)
	}
}

func TestLoadDotEnvReadsAllValuesAndKeepsFirstDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), `
# comment
FIRST='one'
SECOND = "two=2"
FIRST=later
EMPTY=
invalid
`)

	values, err := LoadDotEnv(dir)
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	want := map[string]string{"FIRST": "one", "SECOND": "two=2", "EMPTY": ""}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("values = %#v, want %#v", values, want)
	}
}

func TestLoadDotEnvMissingFileReturnsEmpty(t *testing.T) {
	values, err := LoadDotEnv(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("values = %#v, want empty", values)
	}
}

func TestLoadDotEnvReturnsReadError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0o755); err != nil {
		t.Fatalf("mkdir .env: %v", err)
	}
	if _, err := LoadDotEnv(dir); err == nil {
		t.Fatal("LoadDotEnv succeeded for a directory")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setUserConfigDirEnv(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", dir)
		t.Setenv("APPDATA", dir)
		return
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
}
