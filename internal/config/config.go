package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const EnvConfigFile = "ELBOT_CONFIG_FILE"
const AppDirName = "ElBot"
const XDGAppDirName = "elbot"
const PluginConfigDirName = "plugins"

type Config struct {
	ConfigFiles         ConfigFilesConfig             `toml:"config_files"`
	ModeModels          map[string]ModelSelection     `toml:"mode_models"`
	NamingModel         ModelSelection                `toml:"naming_model"`
	CompactModel        ModelSelection                `toml:"-"`
	Providers           map[string]ProviderConfig     `toml:"providers"`
	ModelMetadata       ModelMetadataConfig           `toml:"model_metadata"`
	Storage             StorageConfig                 `toml:"storage"`
	Runtime             RuntimeConfig                 `toml:"runtime"`
	Ops                 OpsConfig                     `toml:"ops"`
	Context             ContextConfig                 `toml:"context"`
	Commands            CommandsConfig                `toml:"commands"`
	Tools               ToolsConfig                   `toml:"tools"`
	ResidentMemory      ResidentMemoryConfig          `toml:"resident_memory"`
	View                ViewConfig                    `toml:"view"`
	Security            SecurityConfig                `toml:"security"`
	Session             SessionConfig                 `toml:"session"`
	LLMRequest          LLMRequestConfig              `toml:"llm_request"`
	Maintenance         MaintenanceConfig             `toml:"maintenance"`
	Sandbox             SandboxConfig                 `toml:"sandbox"`
	Media               MediaConfig                   `toml:"media"`
	FileDelivery        FileDeliveryConfig            `toml:"file_delivery"`
	PlatformFiles       PlatformFilesConfig           `toml:"platform_files"`
	Platform            PlatformConfig                `toml:"platform"`
	Elnis               ElnisConfig                   `toml:"elnis"`
	Soul                SoulConfig                    `toml:"soul"`
	CharacterLibrary    CharacterLibraryConfig        `toml:"character_library"`
	ImageGeneration     ImageGenerationConfig         `toml:"image_generation"`
	ModelProfiles       map[string]ModelProfileConfig `toml:"model_profiles"`
	ToolProfiles        map[string]ToolProfileConfig  `toml:"tool_profiles"`
	TurnDirectives      TurnDirectivesConfig          `toml:"turn_directives"`
	ToolTags            ToolTagsConfig                `toml:"-"`
	ConfigPath          string                        `toml:"-"`
	ProvidersConfigPath string                        `toml:"-"`
	StateConfigPath     string                        `toml:"-"`
	ElnisConfigPath     string                        `toml:"-"`
	ToolTagsConfigPath  string                        `toml:"-"`
}

type ConfigFilesConfig struct {
	Providers string `toml:"providers"`
	State     string `toml:"state"`
	Elnis     string `toml:"elnis"`
	ToolTags  string `toml:"tool_tags"`
}

type ModelSelection struct {
	Provider string `toml:"provider"`
	Model    string `toml:"model"`
}

type ProviderConfig struct {
	BaseURL          string                 `toml:"base_url"`
	APIKey           string                 `toml:"api_key"`
	APIKeyEnv        string                 `toml:"api_key_env"`
	Proxy            string                 `toml:"proxy"`
	Models           []string               `toml:"models"`
	ModelConfigs     map[string]ModelConfig `toml:"model_configs"`
	ExtraPayload     map[string]any         `toml:"extra_payload"`
	FallbackProvider string                 `toml:"fallback_provider"`
	FallbackModel    string                 `toml:"fallback_model"`
}

type ModelConfig struct {
	ContextWindow int            `toml:"context_window"`
	ExtraPayload  map[string]any `toml:"extra_payload"`
}

type ModelConfigs map[string]ModelConfig

type ModelMetadataConfig struct {
	DefaultContextWindow int `toml:"default_context_window"`
}

const DefaultContextWindow = 256000

type LLMRequestConfig struct {
	FirstChunkTimeoutSeconds int `toml:"first_chunk_timeout_seconds"`
	StreamIdleTimeoutSeconds int `toml:"stream_idle_timeout_seconds"`
	ResponseTimeoutSeconds   int `toml:"response_timeout_seconds"`
	MaxRetries               int `toml:"max_retries"`
	RetryInitialDelaySeconds int `toml:"retry_initial_delay_seconds"`
}

type StorageConfig struct {
	SessionsSQLitePath    string  `toml:"sessions_sqlite_path"`
	ChatHistorySQLitePath string  `toml:"chat_history_sqlite_path"`
	DiskWarnRatio         float64 `toml:"disk_warn_ratio"`
	DiskCriticalRatio     float64 `toml:"disk_critical_ratio"`
	DiskMinFreeBytes      int64   `toml:"disk_min_free_bytes"`
}

type SoulConfig struct {
	Path string `toml:"path"`
}

type ToolTagsConfig struct {
	Tags map[string]ToolTagConfig `toml:"tags"`
}

type ToolTagConfig struct {
	Tools  []string `toml:"tools"`
	Prompt string   `toml:"prompt"`
}

type RuntimeConfig struct {
	LogLevel         string `toml:"log_level"`
	LogRetentionDays int    `toml:"log_retention_days"`
}

// OpsConfig controls operational timeouts and concurrency limits.
type OpsConfig struct {
	ToolTimeoutSeconds      int      `toml:"tool_timeout_seconds"`
	HookTimeoutSeconds      int      `toml:"hook_timeout_seconds"`
	CompressTimeoutSeconds  int      `toml:"compress_timeout_seconds"`
	MaxConcurrentTurns      int      `toml:"max_concurrent_turns"`
	MaxConcurrentTools      int      `toml:"max_concurrent_tools"`
	MaxConcurrentHooks      int      `toml:"max_concurrent_hooks"`
	UserMessagesPerMinute   int      `toml:"user_messages_per_minute"`
	UserBurst               int      `toml:"user_burst"`
	GroupMessagesPerMinute  int      `toml:"group_messages_per_minute"`
	GroupBurst              int      `toml:"group_burst"`
	RateLimitIdleTTLSeconds int      `toml:"rate_limit_idle_ttl_seconds"`
	QueueMaxSize            int      `toml:"queue_max_size"`
	QueueWaitTimeoutSeconds int      `toml:"queue_wait_timeout_seconds"`
	QueueWaitKinds          []string `toml:"queue_wait_kinds"`

	// Provider 熔断（对应 default config 里 [ops] 的 circuit_breaker_* 键）：
	// 连续失败达到阈值后打开，冷却后放少量半开探测；0 表示关闭。
	CircuitBreakerFailureThreshold    int `toml:"circuit_breaker_failure_threshold"`
	CircuitBreakerOpenCooldownSeconds int `toml:"circuit_breaker_open_cooldown_seconds"`
	CircuitBreakerHalfOpenMax         int `toml:"circuit_breaker_half_open_max"`
}

type ContextConfig struct {
	CompactEnabled      bool    `toml:"compact_enabled"`
	CompactTriggerRatio float64 `toml:"compact_trigger_ratio"`
}

type CommandsConfig struct {
	Prefixes []string `toml:"prefixes"`
}

type ToolsConfig struct {
	MaxRoundsPerTurn int `toml:"max_rounds_per_turn"`
}

type ResidentMemoryConfig struct {
	CoreMaxUnits   int `toml:"core_max_units"`
	NormalMaxUnits int `toml:"normal_max_units"`
}

// CharacterLibraryConfig controls the folder-based character library.
type CharacterLibraryConfig struct {
	Enabled *bool  `toml:"enabled"`
	Root    string `toml:"root"`
}

// IsEnabled reports whether the character library is enabled (default true).
func (c CharacterLibraryConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// ImageGenerationConfig controls the optional image generation tool.
// ModelProfileConfig is one named model profile for the @model: directive.
type ModelProfileConfig struct {
	Provider string   `toml:"provider"`
	Model    string   `toml:"model"`
	Aliases  []string `toml:"aliases"`
}

// ToolProfileConfig is one named set of tools for the @use: directive.
type ToolProfileConfig struct {
	Tools   []string `toml:"tools"`
	Aliases []string `toml:"aliases"`
}

// TurnDirectivesConfig controls how @model: / @image: / @use: style declarations
// are typed in chat. Prefixes and keywords are additive; Chinese keywords are
// accepted by default so users can type e.g. #模型:强.
type TurnDirectivesConfig struct {
	Prefixes      []string `toml:"prefixes"`
	ModelKeywords []string `toml:"model_keywords"`
	ImageKeywords []string `toml:"image_keywords"`
	ToolKeywords  []string `toml:"tool_keywords"`
}

// Normalized fills the directive defaults.
func (c TurnDirectivesConfig) Normalized() TurnDirectivesConfig {
	out := c
	if len(out.Prefixes) == 0 {
		out.Prefixes = []string{"@", "#"}
	}
	if len(out.ModelKeywords) == 0 {
		out.ModelKeywords = []string{"model", "m", "模型", "用模型"}
	}
	if len(out.ImageKeywords) == 0 {
		out.ImageKeywords = []string{"image", "img", "生图", "出图"}
	}
	if len(out.ToolKeywords) == 0 {
		out.ToolKeywords = []string{"use", "工具", "用工具"}
	}
	return out
}

// ImageGenerationProfileConfig overrides the base [image_generation] settings.
// Empty strings / nil pointers inherit the base value.
type ImageGenerationProfileConfig struct {
	Enabled           *bool             `toml:"enabled"`
	BaseURL           string            `toml:"base_url"`
	Endpoint          string            `toml:"endpoint"`
	APIKey            string            `toml:"api_key"`
	APIKeyEnv         string            `toml:"api_key_env"`
	Model             string            `toml:"model"`
	Size              string            `toml:"size"`
	Quality           string            `toml:"quality"`
	OutputFormat      string            `toml:"output_format"`
	ResponseFormat    string            `toml:"response_format"`
	TimeoutSeconds    int               `toml:"timeout_seconds"`
	PresetPrompt      *string           `toml:"preset_prompt"`
	NegativePrompt    *string           `toml:"negative_prompt"`
	SuperadminOnly    *bool             `toml:"superadmin_only"`
	SaveToCharacter   *bool             `toml:"save_to_character"`
	SendByDefault     *bool             `toml:"send_by_default"`
	SupportsReference *bool             `toml:"supports_reference"`
	ReferenceField    string            `toml:"reference_field"`
	ExtraPayload      map[string]any    `toml:"extra_payload"`
	ExtraHeaders      map[string]string `toml:"extra_headers"`
	Proxy             string            `toml:"proxy"`
	Aliases           []string          `toml:"aliases"`
}

// ResolveProfile returns the effective config for a named profile. An empty
// name returns the base config. ok is false when the profile does not exist.
func (c ImageGenerationConfig) ResolveProfile(name string) (ImageGenerationConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return c, true
	}
	profile, ok := c.Profiles[name]
	if !ok {
		return ImageGenerationConfig{}, false
	}
	out := c
	out.Profiles = nil
	if profile.Enabled != nil {
		out.Enabled = *profile.Enabled
	}
	if profile.BaseURL != "" {
		out.BaseURL = profile.BaseURL
	}
	if profile.Endpoint != "" {
		out.Endpoint = profile.Endpoint
	}
	if profile.APIKey != "" {
		out.APIKey = profile.APIKey
	}
	if profile.APIKeyEnv != "" {
		out.APIKeyEnv = profile.APIKeyEnv
	}
	if profile.Model != "" {
		out.Model = profile.Model
	}
	if profile.Size != "" {
		out.Size = profile.Size
	}
	if profile.Quality != "" {
		out.Quality = profile.Quality
	}
	if profile.OutputFormat != "" {
		out.OutputFormat = profile.OutputFormat
	}
	if profile.ResponseFormat != "" {
		out.ResponseFormat = profile.ResponseFormat
	}
	if profile.TimeoutSeconds > 0 {
		out.TimeoutSeconds = profile.TimeoutSeconds
	}
	if profile.PresetPrompt != nil {
		out.PresetPrompt = *profile.PresetPrompt
	}
	if profile.NegativePrompt != nil {
		out.NegativePrompt = *profile.NegativePrompt
	}
	if profile.SuperadminOnly != nil {
		out.SuperadminOnly = profile.SuperadminOnly
	}
	if profile.SaveToCharacter != nil {
		out.SaveToCharacter = profile.SaveToCharacter
	}
	if profile.SendByDefault != nil {
		out.SendByDefault = *profile.SendByDefault
	}
	if profile.SupportsReference != nil {
		out.SupportsReference = *profile.SupportsReference
	}
	if profile.ReferenceField != "" {
		out.ReferenceField = profile.ReferenceField
	}
	if profile.ExtraPayload != nil {
		out.ExtraPayload = profile.ExtraPayload
	}
	if profile.ExtraHeaders != nil {
		out.ExtraHeaders = profile.ExtraHeaders
	}
	if profile.Proxy != "" {
		out.Proxy = profile.Proxy
	}
	return out, true
}

// DefaultProfileName returns the configured default image profile (empty = base).
func (c ImageGenerationConfig) DefaultProfileName() string {
	return strings.TrimSpace(c.DefaultProfile)
}

type ImageGenerationConfig struct {
	Enabled                 bool                                    `toml:"enabled"`
	DefaultProfile          string                                  `toml:"default_profile"`
	Profiles                map[string]ImageGenerationProfileConfig `toml:"profiles"`
	BaseURL                 string                                  `toml:"base_url"`
	Endpoint                string                                  `toml:"endpoint"`
	APIKey                  string                                  `toml:"api_key"`
	APIKeyEnv               string                                  `toml:"api_key_env"`
	Model                   string                                  `toml:"model"`
	Size                    string                                  `toml:"size"`
	Quality                 string                                  `toml:"quality"`
	OutputFormat            string                                  `toml:"output_format"`
	ResponseFormat          string                                  `toml:"response_format"`
	TimeoutSeconds          int                                     `toml:"timeout_seconds"`
	PresetPrompt            string                                  `toml:"preset_prompt"`
	NegativePrompt          string                                  `toml:"negative_prompt"`
	MaxPromptRunes          int                                     `toml:"max_prompt_runes"`
	Optimize                string                                  `toml:"optimize"`
	OptimizeTermMode        string                                  `toml:"optimize_term_mode"`
	OptimizeMaxAnchors      int                                     `toml:"optimize_max_anchors"`
	OptimizeMaxNegatives    int                                     `toml:"optimize_max_negatives"`
	OptimizeMaxAddedRunes   int                                     `toml:"optimize_max_added_runes"`
	OptimizeMaxTags         int                                     `toml:"optimize_max_tags"`
	OptimizeRewrite         string                                  `toml:"optimize_rewrite"`
	OptimizeRewriteModel    string                                  `toml:"optimize_rewrite_model"`
	OptimizeRewriteMinRunes int                                     `toml:"optimize_rewrite_min_runes"`
	AutoCharacter           *bool                                   `toml:"auto_character"`
	AutoContext             *bool                                   `toml:"auto_context"`
	ContextDefaultLimit     int                                     `toml:"context_default_limit"`
	SuperadminOnly          *bool                                   `toml:"superadmin_only"`
	SaveToCharacter         *bool                                   `toml:"save_to_character"`
	SendByDefault           bool                                    `toml:"send_by_default"`
	SupportsReference       bool                                    `toml:"supports_reference"`
	ReferenceField          string                                  `toml:"reference_field"`
	ExtraPayload            map[string]any                          `toml:"extra_payload"`
	ExtraHeaders            map[string]string                       `toml:"extra_headers"`
	Proxy                   string                                  `toml:"proxy"`
	MaxConcurrent           int                                     `toml:"max_concurrent"`
	QueueSize               int                                     `toml:"queue_size"`
	QueueTimeoutSeconds     int                                     `toml:"queue_timeout_seconds"`
}

// IsSuperadminOnly reports whether only superadmins may call image_generate (default true).
func (c ImageGenerationConfig) IsSuperadminOnly() bool {
	return c.SuperadminOnly == nil || *c.SuperadminOnly
}

// IsAutoCharacter reports whether image_generate may pick a character by name
// or alias found in the prompt (default true).
func (c ImageGenerationConfig) IsAutoCharacter() bool {
	return c.AutoCharacter == nil || *c.AutoCharacter
}

// IsAutoContext reports whether image_generate may pull group chat context
// automatically (default true).
func (c ImageGenerationConfig) IsAutoContext() bool {
	return c.AutoContext == nil || *c.AutoContext
}

// IsSaveToCharacter reports whether generated images are written back to the
// active character folder (default true).
func (c ImageGenerationConfig) IsSaveToCharacter() bool {
	return c.SaveToCharacter == nil || *c.SaveToCharacter
}

type ViewConfig struct {
	SessionListPageSize int `toml:"session_list_page_size"`
}

type SecurityConfig struct {
	UserMaxToolRisk       string              `toml:"user_max_tool_risk"`
	SuperadminConfirmRisk string              `toml:"superadmin_confirm_risk"`
	Superadmins           map[string][]string `toml:"superadmins"`
}

type SessionConfig struct {
	DefaultMode    string                      `toml:"default_mode"`
	IdleExpiration SessionIdleExpirationConfig `toml:"idle_expiration"`
	Naming         SessionNamingConfig         `toml:"naming"`
}

type SessionIdleExpirationConfig struct {
	GroupUserTTLMinutes         int `toml:"group_user_ttl_minutes"`
	GroupSuperadminTTLMinutes   int `toml:"group_superadmin_ttl_minutes"`
	PrivateUserTTLMinutes       int `toml:"private_user_ttl_minutes"`
	PrivateSuperadminTTLMinutes int `toml:"private_superadmin_ttl_minutes"`
}

type MaintenanceConfig struct {
	LogCleanup         CronTaskConfig           `toml:"log_cleanup"`
	SessionCleanup     MaintenanceCleanupConfig `toml:"session_cleanup"`
	SandboxCleanup     MaintenanceCleanupConfig `toml:"sandbox_cleanup"`
	ChatHistoryCleanup ChatHistoryCleanupConfig `toml:"chat_history_cleanup"`
	DailyReport        DailyReportConfig        `toml:"daily_report"`
}

// DailyReportConfig controls the scheduled resource/usage report.
type DailyReportConfig struct {
	Enabled            bool                        `toml:"enabled"`
	Schedule           string                      `toml:"schedule"`
	WindowHours        int                         `toml:"window_hours"`
	Provider           string                      `toml:"provider"`
	Platform           string                      `toml:"platform"`
	Currency           string                      `toml:"currency"`
	DataRoot           string                      `toml:"data_root"`
	ImagePricePerImage float64                     `toml:"image_price_per_image"`
	PeakPricing        *bool                       `toml:"peak_pricing"`
	Holidays           []string                    `toml:"holidays"`
	Prices             map[string]ModelPriceConfig `toml:"prices"`
}

// IsPeakPricing reports whether the peak/off-peak price tiers are applied
// (default true; DeepSeek charges half price outside peak hours).
func (c DailyReportConfig) IsPeakPricing() bool {
	return c.PeakPricing == nil || *c.PeakPricing
}

// ModelPriceConfig is the per-million-token price for one model.
// The base fields are the peak-hour (standard) prices; the offpeak fields are
// optional and fall back to the base fields when unset.
type ModelPriceConfig struct {
	InputPerMillion      float64 `toml:"input_per_million"`
	OutputPerMillion     float64 `toml:"output_per_million"`
	CacheInputPerMillion float64 `toml:"cache_input_per_million"`

	OffpeakInputPerMillion      float64 `toml:"offpeak_input_per_million"`
	OffpeakOutputPerMillion     float64 `toml:"offpeak_output_per_million"`
	OffpeakCacheInputPerMillion float64 `toml:"offpeak_cache_input_per_million"`
}

type SandboxConfig struct {
	Root string `toml:"root"`
}

type MaintenanceCleanupConfig struct {
	Enabled       bool   `toml:"enabled"`
	Schedule      string `toml:"schedule"`
	RetentionDays int    `toml:"retention_days"`
}

type MediaConfig struct {
	LLMImageCompressionThresholdBytes int64 `toml:"llm_image_compression_threshold_bytes"`
	LLMImageMaxLength                 int   `toml:"llm_image_max_length"`
}

type FileDeliveryConfig struct {
	MaxDirectBase64Bytes int64  `toml:"max_direct_base64_bytes"`
	Backend              string `toml:"backend"`
	S3Endpoint           string `toml:"s3_endpoint"`
	S3Region             string `toml:"s3_region"`
	S3Bucket             string `toml:"s3_bucket"`
	S3AccessKeyEnv       string `toml:"s3_access_key_env"`
	S3SecretKeyEnv       string `toml:"s3_secret_key_env"`
	S3PublicBaseURL      string `toml:"s3_public_base_url"`
}

type PlatformConfig map[string]map[string]any

type PlatformFilesConfig struct {
	MaxReceiveFileBytes int64 `toml:"max_receive_file_bytes"`
	DownloadTimeoutSecs int   `toml:"download_timeout_secs"`
}

type ElnisConfig struct {
	Enabled          bool                         `toml:"enabled"`
	AllowedTools     []string                     `toml:"allowed_tools"`
	HTTP             ElnisHTTPConfig              `toml:"http"`
	Tokens           map[string]ElnisTokenConfig  `toml:"tokens"`
	DeliveryDisabled ElnisDeliveryDisabledConfig  `toml:"delivery_disabled"`
	Segment          ElnisSegmentConfig           `toml:"segment"`
	Elwisps          map[string]ElnisElwispConfig `toml:"elwisps"`
}

type ElnisSegmentConfig struct {
	MaxFileBytes        int64 `toml:"max_file_bytes"`
	DownloadTimeoutSecs int   `toml:"download_timeout_secs"`
}

type ElnisHTTPConfig struct {
	Addr                     string `toml:"addr"`
	MaxBodyBytes             int64  `toml:"max_body_bytes"`
	QueueSize                int    `toml:"queue_size"`
	Workers                  int    `toml:"workers"`
	ReadHeaderTimeoutSeconds int    `toml:"read_header_timeout_seconds"`
	ReadTimeoutSeconds       int    `toml:"read_timeout_seconds"`
	WriteTimeoutSeconds      int    `toml:"write_timeout_seconds"`
	IdleTimeoutSeconds       int    `toml:"idle_timeout_seconds"`
}

type ElnisTokenConfig struct {
	TokenEnv []string `toml:"token_env"`
}

type ElnisDeliveryDisabledConfig struct {
	Targets []ElnisTargetConfig `toml:"targets"`
}

type ElnisTargetConfig struct {
	Platform string `toml:"platform"`
	Type     string `toml:"type"`
	ID       string `toml:"id"`
}

type ElnisElwispConfig struct {
	Enabled               *bool               `toml:"enabled"`
	AllowedTokens         []string            `toml:"allowed_tokens"`
	AllowedTools          []string            `toml:"allowed_tools"`
	DisabledExternalTools []string            `toml:"disabled_external_tools"`
	DisabledTargets       []ElnisTargetConfig `toml:"disabled_targets"`
}

type CronTaskConfig struct {
	Enabled  bool   `toml:"enabled"`
	Schedule string `toml:"schedule"`
}

type ChatHistoryCleanupConfig struct {
	Enabled       bool   `toml:"enabled"`
	Schedule      string `toml:"schedule"`
	RetentionDays int    `toml:"retention_days"`
}

type SessionNamingConfig struct {
	TriggerStep int `toml:"trigger_step"`
}

func ResolvePath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return filepath.Clean(path), nil
	}
	if envPath := strings.TrimSpace(os.Getenv(EnvConfigFile)); envPath != "" {
		return filepath.Clean(envPath), nil
	}
	if defaultPath, ok := platformDefaultConfigPath(); ok {
		if _, err := os.Stat(defaultPath); err == nil {
			return defaultPath, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat default config %q: %w", defaultPath, err)
		}
	}
	if generatedPath, err := EnsurePlatformDefaults(); err == nil {
		return generatedPath, nil
	} else if _, ok := platformDefaultConfigPath(); ok {
		return "", err
	}
	return "", fmt.Errorf("platform config dir is unavailable")
}

func platformDefaultConfigPath() (string, bool) {
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		name := XDGAppDirName
		if runtime.GOOS == "windows" {
			name = AppDirName
		}
		return filepath.Join(dir, name, "app.toml"), true
	}
	return "", false
}

func platformDefaultDataDir() string {
	name := XDGAppDirName
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
			return filepath.Join(dir, AppDirName, "data")
		}
		return filepath.Join(AppDirName, "data")
	}
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return filepath.Join(dir, name)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".local", "share", name)
	}
	return "data"
}

func ConfigEnv(key, configDir string) (string, bool, error) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value, true, nil
	}
	return lookupDotEnv(key, filepath.Join(configDir, ".env"))
}

// LoadDotEnv reads all variables from the config directory .env file.
// Earlier duplicate definitions win, matching ConfigEnv's existing behavior.
func LoadDotEnv(configDir string) (map[string]string, error) {
	return LoadEnvFile(filepath.Join(configDir, ".env"))
}

// LoadEnvFile reads variables from an exact dotenv file path.
func LoadEnvFile(path string) (map[string]string, error) {
	return loadDotEnvFile(path)
}

func lookupDotEnv(key, path string) (string, bool, error) {
	values, err := loadDotEnvFile(path)
	if err != nil {
		return "", false, err
	}
	value, ok := values[key]
	return value, ok, nil
}

func loadDotEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	if strings.TrimSpace(path) == "" {
		return values, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return values, nil
		}
		return nil, fmt.Errorf("open env file %q: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		if _, exists := values[name]; !exists {
			values[name] = strings.TrimSpace(unquoteEnvValue(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file %q: %w", path, err)
	}
	return values, nil
}

func unquoteEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		quote := value[0]
		if (quote == '\'' || quote == '"') && value[len(value)-1] == quote {
			return value[1 : len(value)-1]
		}
	}
	return value
}
func Load(path string) (*Config, error) {
	configPath, err := ResolvePath(path)
	if err != nil {
		return nil, err
	}

	cfg := defaultAppConfig()
	if err := loadTOML(configPath, cfg); err != nil {
		return nil, err
	}
	cfg.applyAppDefaults()

	providersPath := resolveRelative(configPath, cfg.ConfigFiles.Providers)
	providersCfg := &Config{}
	if err := loadTOML(providersPath, providersCfg); err != nil {
		return nil, err
	}
	cfg.mergeProviders(providersCfg)
	cfg.applyProviderDefaults()
	if err := cfg.resolveProviderAPIKeys(filepath.Dir(configPath)); err != nil {
		return nil, err
	}

	statePath := resolveRelative(configPath, cfg.ConfigFiles.State)
	stateCfg := &StateConfig{}
	if err := loadTOML(statePath, stateCfg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else {
		cfg.applyState(stateCfg)
	}

	elnisPath := resolveRelative(configPath, cfg.ConfigFiles.Elnis)
	elnisCfg := &ElnisConfig{}
	if err := loadTOML(elnisPath, elnisCfg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else {
		cfg.Elnis = *elnisCfg
	}
	cfg.applyElnisDefaults()

	toolTagsPath := resolveRelative(configPath, cfg.ConfigFiles.ToolTags)
	toolTagsCfg := &ToolTagsConfig{}
	if err := loadTOML(toolTagsPath, toolTagsCfg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else {
		cfg.ToolTags = *toolTagsCfg
	}

	if err := cfg.validateModeModels(); err != nil {
		return nil, err
	}
	cfg.Storage.SessionsSQLitePath = resolveRelative(configPath, cfg.Storage.SessionsSQLitePath)
	cfg.Storage.ChatHistorySQLitePath = resolveRelative(configPath, cfg.Storage.ChatHistorySQLitePath)
	cfg.Soul.Path = resolveRelative(configPath, cfg.Soul.Path)
	cfg.CharacterLibrary.Root = resolveRelative(configPath, cfg.CharacterLibrary.Root)
	cfg.Sandbox.Root = resolveRelative(configPath, cfg.Sandbox.Root)
	cfg.ConfigPath = configPath
	cfg.ProvidersConfigPath = providersPath
	cfg.StateConfigPath = statePath
	cfg.ElnisConfigPath = elnisPath
	cfg.ToolTagsConfigPath = toolTagsPath
	return cfg, nil
}

func PluginConfigDir(configPath string) string {
	// 插件专属配置不进入 Config 模型。
	// app 层只提供 plugins 目录，具体字段由插件自行解析，避免核心配置结构随插件膨胀。
	if configPath == "" {
		if defaultPath, ok := platformDefaultConfigPath(); ok {
			configPath = defaultPath
		}
	}
	return filepath.Join(filepath.Dir(filepath.Clean(configPath)), PluginConfigDirName)
}

type StateConfig struct {
	Session      StateSessionConfig        `toml:"session"`
	ModeModels   map[string]ModelSelection `toml:"mode_models"`
	NamingModel  ModelSelection            `toml:"naming_model"`
	CompactModel ModelSelection            `toml:"compact_model"`
}

type StateSessionConfig struct {
	DefaultMode string `toml:"default_mode"`
}

func LoadState(path string) (*StateConfig, error) {
	state := &StateConfig{}
	if err := loadTOML(path, state); err != nil {
		return nil, err
	}
	return state, nil
}

func SaveState(path string, state StateConfig) error {
	data, err := toml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state config dir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write state config %q: %w", path, err)
	}
	return nil
}

func Default() *Config {
	cfg := defaultAppConfig()
	cfg.applyProviderDefaults()
	return cfg
}

func defaultAppConfig() *Config {
	cfg := &Config{}
	cfg.applyAppDefaults()
	cfg.Session.IdleExpiration = defaultSessionIdleExpirationConfig()
	return cfg
}

func defaultSessionIdleExpirationConfig() SessionIdleExpirationConfig {
	return SessionIdleExpirationConfig{
		GroupUserTTLMinutes:         10,
		GroupSuperadminTTLMinutes:   10,
		PrivateUserTTLMinutes:       10,
		PrivateSuperadminTTLMinutes: 0,
	}
}

func (c *Config) applySessionIdleExpirationDefaults() {
	if c.Session.IdleExpiration.GroupUserTTLMinutes < 0 {
		c.Session.IdleExpiration.GroupUserTTLMinutes = 0
	}
	if c.Session.IdleExpiration.GroupSuperadminTTLMinutes < 0 {
		c.Session.IdleExpiration.GroupSuperadminTTLMinutes = 0
	}
	if c.Session.IdleExpiration.PrivateUserTTLMinutes < 0 {
		c.Session.IdleExpiration.PrivateUserTTLMinutes = 0
	}
	if c.Session.IdleExpiration.PrivateSuperadminTTLMinutes < 0 {
		c.Session.IdleExpiration.PrivateSuperadminTTLMinutes = 0
	}
}

func loadTOML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	if err := toml.Unmarshal(data, out); err != nil {
		var decodeErr *toml.DecodeError
		if errors.As(err, &decodeErr) {
			row, col := decodeErr.Position()
			return fmt.Errorf("parse config %q at line %d, column %d: %w", path, row, col, err)
		}
		return fmt.Errorf("parse config %q: %w", path, err)
	}
	return nil
}

func (c *Config) applyAppDefaults() {
	if c.ConfigFiles.Providers == "" {
		c.ConfigFiles.Providers = "providers.toml"
	}
	if c.ConfigFiles.State == "" {
		c.ConfigFiles.State = "state.toml"
	}
	if c.ConfigFiles.Elnis == "" {
		c.ConfigFiles.Elnis = "elnis.toml"
	}
	if c.ConfigFiles.ToolTags == "" {
		c.ConfigFiles.ToolTags = "tool_tags.toml"
	}
	if c.Storage.SessionsSQLitePath == "" {
		c.Storage.SessionsSQLitePath = filepath.Join(platformDefaultDataDir(), "elbot_sessions.db")
	}
	if c.Storage.ChatHistorySQLitePath == "" {
		c.Storage.ChatHistorySQLitePath = filepath.Join(platformDefaultDataDir(), "elbot_chat_history.db")
	}
	if c.Storage.DiskWarnRatio <= 0 || c.Storage.DiskWarnRatio >= 1 {
		c.Storage.DiskWarnRatio = 0.85
	}
	if c.Storage.DiskCriticalRatio <= 0 || c.Storage.DiskCriticalRatio >= 1 {
		c.Storage.DiskCriticalRatio = 0.95
	}
	if c.Storage.DiskMinFreeBytes < 0 {
		c.Storage.DiskMinFreeBytes = 0
	}
	if c.Soul.Path == "" {
		c.Soul.Path = "SOUL.md"
	}
	if c.CharacterLibrary.Root == "" {
		c.CharacterLibrary.Root = "characters"
	}
	if c.ImageGeneration.APIKeyEnv == "" {
		c.ImageGeneration.APIKeyEnv = "IMAGE_API_KEY"
	}
	if c.ImageGeneration.Model == "" {
		c.ImageGeneration.Model = "gpt-image-2.5"
	}
	if c.ImageGeneration.Size == "" {
		c.ImageGeneration.Size = "1024x1024"
	}
	if c.ImageGeneration.Quality == "" {
		c.ImageGeneration.Quality = "high"
	}
	if c.ImageGeneration.OutputFormat == "" {
		c.ImageGeneration.OutputFormat = "png"
	}
	if c.ImageGeneration.TimeoutSeconds <= 0 {
		c.ImageGeneration.TimeoutSeconds = 180
	}
	if c.ImageGeneration.MaxConcurrent < 0 {
		c.ImageGeneration.MaxConcurrent = 0
	}
	if c.ImageGeneration.QueueSize < 0 {
		c.ImageGeneration.QueueSize = 0
	}
	if c.ImageGeneration.QueueTimeoutSeconds < 0 {
		c.ImageGeneration.QueueTimeoutSeconds = 0
	}
	if c.ImageGeneration.MaxPromptRunes <= 0 {
		c.ImageGeneration.MaxPromptRunes = 4000
	}
	if c.ImageGeneration.Optimize == "" {
		c.ImageGeneration.Optimize = "rules"
	}
	if c.ImageGeneration.OptimizeMaxAnchors <= 0 {
		c.ImageGeneration.OptimizeMaxAnchors = 4
	}
	if c.ImageGeneration.OptimizeMaxNegatives <= 0 {
		c.ImageGeneration.OptimizeMaxNegatives = 10
	}
	if c.ImageGeneration.OptimizeMaxAddedRunes <= 0 {
		c.ImageGeneration.OptimizeMaxAddedRunes = 400
	}
	if c.ImageGeneration.OptimizeTermMode == "" {
		c.ImageGeneration.OptimizeTermMode = "phrase"
	}
	if c.ImageGeneration.OptimizeMaxTags <= 0 {
		c.ImageGeneration.OptimizeMaxTags = 12
	}
	if c.ImageGeneration.OptimizeRewrite == "" {
		c.ImageGeneration.OptimizeRewrite = "auto"
	}
	if c.ImageGeneration.OptimizeRewriteModel == "" {
		c.ImageGeneration.OptimizeRewriteModel = "naming"
	}
	if c.ImageGeneration.OptimizeRewriteMinRunes <= 0 {
		c.ImageGeneration.OptimizeRewriteMinRunes = 40
	}
	if c.ImageGeneration.ContextDefaultLimit <= 0 {
		c.ImageGeneration.ContextDefaultLimit = 6
	}
	if c.ImageGeneration.ReferenceField == "" {
		c.ImageGeneration.ReferenceField = "image"
	}
	if c.Maintenance.DailyReport.Schedule == "" {
		c.Maintenance.DailyReport.Schedule = "0 9,21 * * *"
	}
	if c.Maintenance.DailyReport.WindowHours <= 0 {
		c.Maintenance.DailyReport.WindowHours = 12
	}
	if c.Maintenance.DailyReport.Provider == "" {
		c.Maintenance.DailyReport.Provider = "deepseek"
	}
	if c.Maintenance.DailyReport.Currency == "" {
		c.Maintenance.DailyReport.Currency = "CNY"
	}
	c.TurnDirectives = c.TurnDirectives.Normalized()
	if c.Runtime.LogLevel == "" {
		c.Runtime.LogLevel = "info"
	}
	if c.Runtime.LogRetentionDays <= 0 {
		c.Runtime.LogRetentionDays = 30
	}
	if c.Ops.ToolTimeoutSeconds < 0 {
		c.Ops.ToolTimeoutSeconds = 0
	}
	if c.Ops.HookTimeoutSeconds < 0 {
		c.Ops.HookTimeoutSeconds = 0
	}
	if c.Ops.CompressTimeoutSeconds < 0 {
		c.Ops.CompressTimeoutSeconds = 0
	}
	if c.Ops.MaxConcurrentTurns < 0 {
		c.Ops.MaxConcurrentTurns = 0
	}
	if c.Ops.MaxConcurrentTools < 0 {
		c.Ops.MaxConcurrentTools = 0
	}
	if c.Ops.MaxConcurrentHooks < 0 {
		c.Ops.MaxConcurrentHooks = 0
	}
	if c.Ops.QueueMaxSize < 0 {
		c.Ops.QueueMaxSize = 0
	}
	if c.Ops.QueueWaitTimeoutSeconds < 0 {
		c.Ops.QueueWaitTimeoutSeconds = 0
	}
	if c.Ops.CircuitBreakerFailureThreshold < 0 {
		c.Ops.CircuitBreakerFailureThreshold = 0
	}
	if c.Ops.CircuitBreakerOpenCooldownSeconds < 0 {
		c.Ops.CircuitBreakerOpenCooldownSeconds = 0
	}
	if c.Ops.CircuitBreakerHalfOpenMax <= 0 {
		c.Ops.CircuitBreakerHalfOpenMax = 1
	}
	if c.Context.CompactTriggerRatio == 0 {
		c.Context.CompactTriggerRatio = 0.8
	}
	if len(c.Commands.Prefixes) == 0 {
		c.Commands.Prefixes = []string{"/*"}
	}
	if c.Tools.MaxRoundsPerTurn <= 0 {
		c.Tools.MaxRoundsPerTurn = 2
	}
	if c.ResidentMemory.CoreMaxUnits <= 0 {
		c.ResidentMemory.CoreMaxUnits = 200
	}
	if c.ResidentMemory.NormalMaxUnits <= 0 {
		c.ResidentMemory.NormalMaxUnits = 300
	}
	if c.LLMRequest.FirstChunkTimeoutSeconds <= 0 {
		c.LLMRequest.FirstChunkTimeoutSeconds = 180
	}
	if c.LLMRequest.StreamIdleTimeoutSeconds <= 0 {
		c.LLMRequest.StreamIdleTimeoutSeconds = 60
	}
	if c.LLMRequest.ResponseTimeoutSeconds < 0 {
		c.LLMRequest.ResponseTimeoutSeconds = 0
	}
	if c.LLMRequest.MaxRetries <= 0 {
		c.LLMRequest.MaxRetries = 3
	}
	if c.LLMRequest.RetryInitialDelaySeconds <= 0 {
		c.LLMRequest.RetryInitialDelaySeconds = 2
	}
	if c.View.SessionListPageSize <= 0 {
		c.View.SessionListPageSize = 10
	}
	if c.Security.UserMaxToolRisk == "" {
		c.Security.UserMaxToolRisk = "low"
	}
	if c.Security.SuperadminConfirmRisk == "" {
		c.Security.SuperadminConfirmRisk = "high"
	}
	if c.Security.Superadmins == nil {
		c.Security.Superadmins = map[string][]string{"cli": {"local"}}
	}
	if c.Platform == nil {
		c.Platform = PlatformConfig{}
	}
	c.applyElnisDefaults()
	c.applySessionIdleExpirationDefaults()
	if c.Maintenance.LogCleanup.Schedule == "" {
		c.Maintenance.LogCleanup.Schedule = "0 3 * * *"
	}
	if c.Maintenance.SessionCleanup.Schedule == "" {
		c.Maintenance.SessionCleanup.Schedule = "15 3 * * *"
	}
	if c.Maintenance.SessionCleanup.RetentionDays == 0 {
		c.Maintenance.SessionCleanup.RetentionDays = 30
	}
	if c.Maintenance.SandboxCleanup.Schedule == "" {
		c.Maintenance.SandboxCleanup.Schedule = "0 4 * * *"
	}
	if c.Maintenance.SandboxCleanup.RetentionDays == 0 {
		c.Maintenance.SandboxCleanup.RetentionDays = 7
	}
	if c.Maintenance.ChatHistoryCleanup.Schedule == "" {
		c.Maintenance.ChatHistoryCleanup.Schedule = "35 4 * * *"
	}
	if c.Maintenance.ChatHistoryCleanup.RetentionDays == 0 {
		c.Maintenance.ChatHistoryCleanup.RetentionDays = 180
	}
	if c.Sandbox.Root == "" {
		c.Sandbox.Root = filepath.Join(platformDefaultDataDir(), "sandbox")
	}
	if c.Media.LLMImageCompressionThresholdBytes <= 0 {
		c.Media.LLMImageCompressionThresholdBytes = 4 * 1024 * 1024
	}
	if c.Media.LLMImageMaxLength <= 0 {
		c.Media.LLMImageMaxLength = 4096
	}
	if c.FileDelivery.MaxDirectBase64Bytes <= 0 {
		c.FileDelivery.MaxDirectBase64Bytes = 8 * 1024 * 1024
	}
	if c.FileDelivery.Backend == "" {
		c.FileDelivery.Backend = "base64"
	}
	if c.FileDelivery.S3Region == "" {
		c.FileDelivery.S3Region = "auto"
	}
	if c.PlatformFiles.MaxReceiveFileBytes <= 0 {
		c.PlatformFiles.MaxReceiveFileBytes = 100 * 1024 * 1024
	}
	if c.PlatformFiles.DownloadTimeoutSecs <= 0 {
		c.PlatformFiles.DownloadTimeoutSecs = 60
	}
	if c.Session.Naming.TriggerStep <= 0 {
		c.Session.Naming.TriggerStep = 1
	}
}

func (c *Config) applyElnisDefaults() {
	if c.Elnis.HTTP.Addr == "" {
		c.Elnis.HTTP.Addr = "127.0.0.1:32170"
	}
	if c.Elnis.HTTP.MaxBodyBytes <= 0 {
		c.Elnis.HTTP.MaxBodyBytes = 1024 * 1024
	}
	if c.Elnis.HTTP.QueueSize <= 0 {
		c.Elnis.HTTP.QueueSize = 128
	}
	if c.Elnis.HTTP.Workers <= 0 {
		c.Elnis.HTTP.Workers = 2
	}
	if c.Elnis.HTTP.ReadHeaderTimeoutSeconds <= 0 {
		c.Elnis.HTTP.ReadHeaderTimeoutSeconds = 5
	}
	if c.Elnis.HTTP.ReadTimeoutSeconds <= 0 {
		c.Elnis.HTTP.ReadTimeoutSeconds = 30
	}
	if c.Elnis.HTTP.WriteTimeoutSeconds <= 0 {
		c.Elnis.HTTP.WriteTimeoutSeconds = 300
	}
	if c.Elnis.HTTP.IdleTimeoutSeconds <= 0 {
		c.Elnis.HTTP.IdleTimeoutSeconds = 60
	}
	if c.Elnis.Tokens == nil {
		c.Elnis.Tokens = map[string]ElnisTokenConfig{}
	}
	if c.Elnis.Segment.MaxFileBytes <= 0 {
		c.Elnis.Segment.MaxFileBytes = 100 * 1024 * 1024
	}
	if c.Elnis.Segment.DownloadTimeoutSecs <= 0 {
		c.Elnis.Segment.DownloadTimeoutSecs = 60
	}
	if c.Elnis.Elwisps == nil {
		c.Elnis.Elwisps = map[string]ElnisElwispConfig{}
	}
}

func (c *Config) applyProviderDefaults() {
	if c.Providers == nil {
		c.Providers = map[string]ProviderConfig{}
	}
	if c.ModelMetadata.DefaultContextWindow <= 0 {
		c.ModelMetadata.DefaultContextWindow = DefaultContextWindow
	}
}

func (c *Config) applyState(state *StateConfig) {
	if len(state.ModeModels) > 0 {
		if c.ModeModels == nil {
			c.ModeModels = map[string]ModelSelection{}
		}
		for mode, model := range state.ModeModels {
			c.ModeModels[mode] = model
		}
	}
	if state.NamingModel.Provider != "" || state.NamingModel.Model != "" {
		c.NamingModel = state.NamingModel
	}
	if state.CompactModel.Provider != "" || state.CompactModel.Model != "" {
		// 压缩模型是运行态选择，只从 state.toml 读取；未配置时由调用方用当前模式模型兜底。
		c.CompactModel = state.CompactModel
	}
	if state.Session.DefaultMode != "" {
		c.Session.DefaultMode = state.Session.DefaultMode
	}
}

func (c *Config) resolveProviderAPIKeys(configDir string) error {
	for name, provider := range c.Providers {
		if strings.TrimSpace(provider.APIKey) != "" || strings.TrimSpace(provider.APIKeyEnv) == "" {
			continue
		}
		value, ok, err := ConfigEnv(provider.APIKeyEnv, configDir)
		if err != nil {
			return fmt.Errorf("resolve provider %q api key: %w", name, err)
		}
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		provider.APIKey = value
		c.Providers[name] = provider
	}
	return nil
}

func (c *Config) validateModeModels() error {
	if c.Session.DefaultMode == "" {
		c.Session.DefaultMode = "work"
	}
	if c.Session.DefaultMode != "work" && c.Session.DefaultMode != "chat" {
		return fmt.Errorf("session.default_mode must be work or chat, got %q", c.Session.DefaultMode)
	}
	if c.ModeModels == nil {
		c.ModeModels = map[string]ModelSelection{}
	}
	for _, mode := range []string{"work", "chat"} {
		selected := c.ModeModels[mode]
		if selected.Provider == "" || selected.Model == "" {
			return fmt.Errorf("mode_models.%s provider/model is required", mode)
		}
	}
	return nil
}

func (c *Config) mergeProviders(providerCfg *Config) {
	c.Providers = providerCfg.Providers
	c.ModelMetadata = providerCfg.ModelMetadata
}

func resolveRelative(baseFile, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(baseFile), path))
}
