package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const EnvConfigFile = "ELBOT_CONFIG_FILE"
const AppDirName = "ElBot"
const XDGAppDirName = "elbot"
const PluginConfigDirName = "plugins"

type Config struct {
	ConfigFiles         ConfigFilesConfig                `toml:"config_files"`
	ModeModels          map[string]ModelSelection        `toml:"mode_models"`
	NamingModel         ModelSelection                   `toml:"naming_model"`
	CompactModel        ModelSelection                   `toml:"-"`
	Providers           map[string]ProviderConfig        `toml:"providers"`
	ModelMetadata       ModelMetadataConfig              `toml:"model_metadata"`
	Storage             StorageConfig                    `toml:"storage"`
	Runtime             RuntimeConfig                    `toml:"runtime"`
	Ops                 OpsConfig                        `toml:"ops"`
	BudgetLimits        BudgetLimitsConfig               `toml:"budget_limits"`
	Context             ContextConfig                    `toml:"context"`
	Commands            CommandsConfig                   `toml:"commands"`
	Tools               ToolsConfig                      `toml:"tools"`
	ResidentMemory      ResidentMemoryConfig             `toml:"resident_memory"`
	View                ViewConfig                       `toml:"view"`
	Security            SecurityConfig                   `toml:"security"`
	Session             SessionConfig                    `toml:"session"`
	LLMRequest          LLMRequestConfig                 `toml:"llm_request"`
	Maintenance         MaintenanceConfig                `toml:"maintenance"`
	Sandbox             SandboxConfig                    `toml:"sandbox"`
	Media               MediaConfig                      `toml:"media"`
	FileDelivery        FileDeliveryConfig               `toml:"file_delivery"`
	PlatformFiles       PlatformFilesConfig              `toml:"platform_files"`
	Platform            PlatformConfig                   `toml:"platform"`
	Elnis               ElnisConfig                      `toml:"elnis"`
	Soul                SoulConfig                       `toml:"soul"`
	CharacterLibrary    CharacterLibraryConfig           `toml:"character_library"`
	ImageGeneration     ImageGenerationConfig            `toml:"image_generation"`
	ImageToPrompt       ImageToPromptConfig              `toml:"image_to_prompt"`
	Vision              VisionConfig                     `toml:"vision"`
	ASR                 ASRConfig                        `toml:"asr"`
	GroupAnalysis       GroupAnalysisConfig              `toml:"group_analysis"`
	GroupKnowledge      GroupKnowledgeConfig             `toml:"group_knowledge"`
	GroupServices       GroupServicesConfig              `toml:"group_services"`
	AngelMemory         AngelMemoryConfig                `toml:"angel_memory"`
	SelfLearning        SelfLearningConfig               `toml:"self_learning"`
	ModelProfiles       map[string]ModelProfileConfig    `toml:"model_profiles"`
	ToolProfiles        map[string]ToolProfileConfig     `toml:"tool_profiles"`
	TurnDirectives      TurnDirectivesConfig             `toml:"turn_directives"`
	ToolTags            ToolTagsConfig                   `toml:"-"`
	ContextOverflow     map[string]ContextOverflowConfig `toml:"-"`
	GroupPolicy         map[string]GroupPolicyConfig     `toml:"-"`
	ConfigPath          string                           `toml:"-"`
	ProvidersConfigPath string                           `toml:"-"`
	ServicesConfigPath  string                           `toml:"-"`
	StateConfigPath     string                           `toml:"-"`
	ElnisConfigPath     string                           `toml:"-"`
	ToolTagsConfigPath  string                           `toml:"-"`
}

type ConfigFilesConfig struct {
	// Services points to the shared, read-only service config (LLM providers,
	// image generation and model aliases). It is optional for backward
	// compatibility; when empty, Providers is loaded instead.
	Services  string `toml:"services"`
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
	// Vision declares whether models from this provider accept image content.
	// It is a pointer so "unset" (nil, unknown) is distinct from an explicit
	// false. Per-model values in [providers.<name>.model_configs.<model>]
	// override this default.
	Vision *bool `toml:"vision"`
	// Audio declares whether models from this provider can be used for audio
	// transcription through an OpenAI-compatible /audio/transcriptions
	// endpoint. It is a pointer so "unset" (unknown) is distinct from an
	// explicit false. Per-model audio values override this default.
	Audio *bool `toml:"audio"`
	// FallbackMode controls when the fallback provider takes over:
	// "circuit" (default) waits until the provider breaker opens; "on_error"
	// switches on the first pre-stream error. "off" disables fallback.
	FallbackMode string `toml:"fallback_mode"`
	// FallbackOnError is a compatibility shorthand for fallback_mode="on_error".
	FallbackOnError bool `toml:"fallback_on_error"`
	// FallbackTimeoutSeconds bounds one provider attempt (including fallback)
	// before it is aborted with a total timeout error. 0 disables the extra bound.
	FallbackTimeoutSeconds int `toml:"fallback_timeout_seconds"`
	// APIMode selects the upstream protocol: "chat" (default, POST {base_url}/chat/completions)
	// or "response" (POST {base_url}/responses). A per-model value in
	// [providers.<name>.model_configs.<model>] overrides this default.
	APIMode string `toml:"api_mode"`
}

// UsesFallbackOnError reports whether this provider should switch to its
// fallback provider on the first pre-stream failure rather than waiting for
// the circuit breaker to open.
func (p ProviderConfig) UsesFallbackOnError() bool {
	if p.FallbackOnError {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(p.FallbackMode)) {
	case "on_error", "first_error", "immediate", "error":
		return true
	default:
		return false
	}
}

type ModelConfig struct {
	ContextWindow int            `toml:"context_window"`
	ExtraPayload  map[string]any `toml:"extra_payload"`
	// Vision overrides the provider-level capability for this single model.
	Vision *bool `toml:"vision"`
	// Audio overrides the provider-level transcription capability for this
	// single model.
	Audio *bool `toml:"audio"`
	// APIMode overrides [providers.<name>].api_mode for this single model.
	APIMode string `toml:"api_mode"`
}

type ModelConfigs map[string]ModelConfig

// VisionSupport is a tri-state image-input capability declaration.
type VisionSupport int

const (
	// VisionUnknown means the config does not declare support either way.
	VisionUnknown VisionSupport = iota
	// VisionSupported means the config declares image input is accepted.
	VisionSupported
	// VisionUnsupported means the config declares image input is rejected.
	VisionUnsupported
)

// VisionSupportFor resolves the declared vision capability for a
// provider/model pair. Model-level config wins over the provider default and
// both default to unknown, so existing configs keep working unchanged.
func (c *Config) VisionSupportFor(provider, model string) VisionSupport {
	if c == nil {
		return VisionUnknown
	}
	providerCfg, ok := c.Providers[provider]
	if !ok {
		return VisionUnknown
	}
	return providerCfg.VisionSupport(model)
}

// VisionSupport resolves the capability for one model, honoring the
// model-level override before the provider default.
func (p ProviderConfig) VisionSupport(model string) VisionSupport {
	if modelConfig, ok := p.ModelConfigs[model]; ok && modelConfig.Vision != nil {
		return visionSupportFromBool(*modelConfig.Vision)
	}
	if p.Vision != nil {
		return visionSupportFromBool(*p.Vision)
	}
	return VisionUnknown
}

func visionSupportFromBool(supported bool) VisionSupport {
	if supported {
		return VisionSupported
	}
	return VisionUnsupported
}

// AudioSupport is a tri-state transcription capability declaration.
type AudioSupport int

const (
	// AudioUnknown means the config does not declare transcription support.
	AudioUnknown AudioSupport = iota
	// AudioSupported means the config declares transcription support.
	AudioSupported
	// AudioUnsupported means the config declares transcription is rejected.
	AudioUnsupported
)

// AudioSupportFor resolves the declared transcription capability for a
// provider/model pair. Model-level config wins over the provider default and
// both default to unknown, so existing configs keep working unchanged.
func (c *Config) AudioSupportFor(provider, model string) AudioSupport {
	if c == nil {
		return AudioUnknown
	}
	providerCfg, ok := c.Providers[provider]
	if !ok {
		return AudioUnknown
	}
	return providerCfg.AudioSupport(model)
}

// AudioSupport resolves the capability for one model.
func (p ProviderConfig) AudioSupport(model string) AudioSupport {
	if modelConfig, ok := p.ModelConfigs[model]; ok && modelConfig.Audio != nil {
		return audioSupportFromBool(*modelConfig.Audio)
	}
	if p.Audio != nil {
		return audioSupportFromBool(*p.Audio)
	}
	return AudioUnknown
}

func audioSupportFromBool(supported bool) AudioSupport {
	if supported {
		return AudioSupported
	}
	return AudioUnsupported
}

// APIProtocol selects the upstream request/response protocol of an
// OpenAI-compatible provider.
type APIProtocol int

const (
	// APIProtocolChat is the OpenAI Chat Completions protocol
	// (POST {base_url}/chat/completions). It is the default.
	APIProtocolChat APIProtocol = iota
	// APIProtocolResponses is the OpenAI Responses protocol
	// (POST {base_url}/responses).
	APIProtocolResponses
)

// ParseAPIProtocol parses a raw [providers.*].api_mode value. Canonical values
// are "chat" and "response"; the chat_completions / responses_api spellings are
// tolerated for convenience. An empty value means the default protocol. ok is
// false for an unrecognized value so config loading can fail loudly instead of
// silently falling back to another endpoint.
func ParseAPIProtocol(raw string) (APIProtocol, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "chat", "chat_completions", "chat-completions":
		return APIProtocolChat, true
	case "response", "responses", "responses_api", "responses-api":
		return APIProtocolResponses, true
	default:
		return APIProtocolChat, false
	}
}

// APIProtocolFor resolves the protocol for one model: the model-level override
// in [providers.<name>.model_configs.<model>] wins over the provider default,
// and both default to chat.
func (p ProviderConfig) APIProtocolFor(model string) APIProtocol {
	if modelConfig, ok := p.ModelConfigs[model]; ok && strings.TrimSpace(modelConfig.APIMode) != "" {
		if protocol, ok := ParseAPIProtocol(modelConfig.APIMode); ok {
			return protocol
		}
	}
	if protocol, ok := ParseAPIProtocol(p.APIMode); ok {
		return protocol
	}
	return APIProtocolChat
}

// UsesResponsesAPI reports whether this provider needs a Responses client,
// either because of its own [providers.<name>].api_mode value or because one of
// its model_configs overrides the protocol.
func (p ProviderConfig) UsesResponsesAPI() bool {
	if protocol, ok := ParseAPIProtocol(p.APIMode); ok && protocol == APIProtocolResponses {
		return true
	}
	for _, modelConfig := range p.ModelConfigs {
		if strings.TrimSpace(modelConfig.APIMode) == "" {
			continue
		}
		if protocol, ok := ParseAPIProtocol(modelConfig.APIMode); ok && protocol == APIProtocolResponses {
			return true
		}
	}
	return false
}

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
	QueueMaxPerUser         int      `toml:"queue_max_per_user"`
	QueueMaxPerScope        int      `toml:"queue_max_per_scope"`
	QueueWaitTimeoutSeconds int      `toml:"queue_wait_timeout_seconds"`
	QueueWaitKinds          []string `toml:"queue_wait_kinds"`
	ProviderMaxConcurrent   int      `toml:"provider_max_concurrent"`
	ProviderQueueMaxSize    int      `toml:"provider_queue_max_size"`
	ProviderWaitTimeoutSecs int      `toml:"provider_wait_timeout_seconds"`

	// Provider 熔断（对应 default config 里 [ops] 的 circuit_breaker_* 键）：
	// 连续失败达到阈值后打开，冷却后放少量半开探测；0 表示关闭。
	CircuitBreakerFailureThreshold    int `toml:"circuit_breaker_failure_threshold"`
	CircuitBreakerOpenCooldownSeconds int `toml:"circuit_breaker_open_cooldown_seconds"`
	CircuitBreakerHalfOpenMax         int `toml:"circuit_breaker_half_open_max"`
}

// BudgetLimitsConfig controls daily platform/group/user budgets. Zero means
// unlimited. Image and vision limits count successful local reservations;
// chat token/cost limits are checked before each paid LLM call and recorded
// after the provider returns usage. Cost is in the currency configured by
// [maintenance.daily_report].currency and stored in micro-units.
type BudgetLimitsConfig struct {
	GlobalImageDaily      int     `toml:"global_image_daily"`
	UserImageDaily        int     `toml:"user_image_daily"`
	GlobalVisionDaily     int     `toml:"global_vision_daily"`
	UserVisionDaily       int     `toml:"user_vision_daily"`
	GlobalASRDaily        int     `toml:"global_asr_daily"`
	UserASRDaily          int     `toml:"user_asr_daily"`
	GlobalChatTokensDaily int64   `toml:"global_chat_tokens_daily"`
	UserChatTokensDaily   int64   `toml:"user_chat_tokens_daily"`
	GlobalChatCostDaily   float64 `toml:"global_chat_cost_daily"`
	UserChatCostDaily     float64 `toml:"user_chat_cost_daily"`
	// ChatHardLimit turns every active chat token/cost limit into a hard
	// pre-reserved budget. Without it, concurrent in-flight calls may overshoot
	// the limit by one request; with it, the estimate is reserved atomically
	// before the provider call and settled afterwards.
	ChatHardLimit bool `toml:"chat_hard_limit"`
	// ChatHardLimitReserveOutputTokens overrides the output-token part of the
	// pre-reservation. 0 uses the per-model prompt budget.
	ChatHardLimitReserveOutputTokens int `toml:"chat_hard_limit_reserve_output_tokens"`
}

// Normalized clamps negative limits to zero (unlimited).
func (c BudgetLimitsConfig) Normalized() BudgetLimitsConfig {
	out := c
	if out.GlobalImageDaily < 0 {
		out.GlobalImageDaily = 0
	}
	if out.UserImageDaily < 0 {
		out.UserImageDaily = 0
	}
	if out.GlobalVisionDaily < 0 {
		out.GlobalVisionDaily = 0
	}
	if out.UserVisionDaily < 0 {
		out.UserVisionDaily = 0
	}
	if out.GlobalASRDaily < 0 {
		out.GlobalASRDaily = 0
	}
	if out.UserASRDaily < 0 {
		out.UserASRDaily = 0
	}
	if out.GlobalChatTokensDaily < 0 {
		out.GlobalChatTokensDaily = 0
	}
	if out.UserChatTokensDaily < 0 {
		out.UserChatTokensDaily = 0
	}
	if out.GlobalChatCostDaily < 0 {
		out.GlobalChatCostDaily = 0
	}
	if out.UserChatCostDaily < 0 {
		out.UserChatCostDaily = 0
	}
	if out.ChatHardLimitReserveOutputTokens < 0 {
		out.ChatHardLimitReserveOutputTokens = 0
	}
	return out
}

type ContextConfig struct {
	CompactEnabled        bool    `toml:"compact_enabled"`
	CompactTriggerRatio   float64 `toml:"compact_trigger_ratio"`
	MaxPromptRatio        float64 `toml:"max_prompt_ratio"`
	ReserveOutputTokens   int     `toml:"reserve_output_tokens"`
	SingleMessageMaxRatio float64 `toml:"single_message_max_ratio"`
	UserOriginalMaxRunes  int     `toml:"user_original_max_runes"`
	OverflowMode          string  `toml:"overflow_mode"`
}

// Normalized returns a context config with safe defaults filled in.
func (c ContextConfig) Normalized() ContextConfig {
	if c.CompactTriggerRatio <= 0 || c.CompactTriggerRatio >= 1 {
		c.CompactTriggerRatio = 0.8
	}
	if c.MaxPromptRatio <= 0 || c.MaxPromptRatio >= 1 {
		c.MaxPromptRatio = 0.8
	}
	if c.SingleMessageMaxRatio <= 0 || c.SingleMessageMaxRatio >= 1 {
		c.SingleMessageMaxRatio = 0.5
	}
	if c.ReserveOutputTokens < 0 {
		c.ReserveOutputTokens = 0
	}
	if c.UserOriginalMaxRunes <= 0 {
		c.UserOriginalMaxRunes = 4000
	}
	switch strings.ToLower(strings.TrimSpace(c.OverflowMode)) {
	case "reject", "truncate", "summarize":
		c.OverflowMode = strings.ToLower(strings.TrimSpace(c.OverflowMode))
	default:
		c.OverflowMode = "reject"
	}
	return c
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

	// P1/P2 guards for normal memory writes. Explicit 0 disables the
	// corresponding rate/line/per-entry limit; nil uses the built-in default.
	NormalWriteMinIntervalSeconds  *int  `toml:"normal_write_min_interval_seconds"`
	NormalWriteWindowSeconds       *int  `toml:"normal_write_window_seconds"`
	NormalWriteMaxPerWindow        *int  `toml:"normal_write_max_per_window"`
	NormalMaxLines                 *int  `toml:"normal_max_lines"`
	NormalMaxUnitsPerEntry         *int  `toml:"normal_max_units_per_entry"`
	NormalBlockInstructionPatterns *bool `toml:"normal_block_instruction_patterns"`
}

// IsNormalBlockInstructionPatterns reports whether normal memory content is
// checked against the built-in instruction-like patterns (default true).
func (c ResidentMemoryConfig) IsNormalBlockInstructionPatterns() bool {
	return c.NormalBlockInstructionPatterns == nil || *c.NormalBlockInstructionPatterns
}

// Effective values used by the app runtime. Explicit 0 disables the guard.
func (c ResidentMemoryConfig) NormalWriteMinIntervalSecondsValue() int {
	return intValueOrDefault(c.NormalWriteMinIntervalSeconds, 5)
}

func (c ResidentMemoryConfig) NormalWriteWindowSecondsValue() int {
	return intValueOrDefault(c.NormalWriteWindowSeconds, 60)
}

func (c ResidentMemoryConfig) NormalWriteMaxPerWindowValue() int {
	return intValueOrDefault(c.NormalWriteMaxPerWindow, 6)
}

func (c ResidentMemoryConfig) NormalMaxLinesValue() int {
	return intValueOrDefault(c.NormalMaxLines, 20)
}

func (c ResidentMemoryConfig) NormalMaxUnitsPerEntryValue() int {
	return intValueOrDefault(c.NormalMaxUnitsPerEntry, 80)
}

func intValueOrDefault(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
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

// GroupAnalysisConfig controls the clean-room group analysis tool.
type GroupAnalysisConfig struct {
	Enabled        *bool  `toml:"enabled"`
	MaxMessages    int    `toml:"max_messages"`
	ReportEnabled  *bool  `toml:"report_enabled"`
	ReportSchedule string `toml:"report_schedule"`
	ReportPlatform string `toml:"report_platform"`
	ReportScopeID  string `toml:"report_scope_id"`
	ReportDays     int    `toml:"report_days"`
}

func (c GroupAnalysisConfig) IsReportEnabled() bool {
	return c.ReportEnabled != nil && *c.ReportEnabled
}

// IsEnabled reports whether group analysis is enabled (default true).
func (c GroupAnalysisConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// AngelMemoryConfig controls the clean-room long-memory tool and injection.
type AngelMemoryConfig struct {
	Enabled            *bool `toml:"enabled"`
	RetentionDays      int   `toml:"retention_days"`
	MaxContentRunes    int   `toml:"max_content_runes"`
	MaxContextRunes    int   `toml:"max_context_runes"`
	MaxPerScope        int   `toml:"max_per_scope"`
	MaxWritesPerMinute int   `toml:"max_writes_per_minute"`
	// ForgetOnRecall removes long-memory entries whose source message is
	// recalled. Defaults to true.
	ForgetOnRecall *bool `toml:"forget_on_recall"`
	// AllowToolForget registers the high-risk angel_forget tool, which lets the
	// model delete one long-memory entry that belongs to the current speaker.
	// Defaults to false: model-initiated deletion is opt-in.
	AllowToolForget *bool `toml:"allow_tool_forget"`
}

func (c AngelMemoryConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func (c AngelMemoryConfig) ForgetOnRecallEnabled() bool {
	return c.ForgetOnRecall == nil || *c.ForgetOnRecall
}

func (c AngelMemoryConfig) AllowToolForgetEnabled() bool {
	return c.AllowToolForget != nil && *c.AllowToolForget
}

// SelfLearningConfig controls the clean-room expression/jargon learning layer.
type SelfLearningConfig struct {
	Enabled                       *bool `toml:"enabled"`
	RetentionDays                 int   `toml:"retention_days"`
	MinCount                      int   `toml:"min_count"`
	MinUsers                      int   `toml:"min_users"`
	MaxMeaningRunes               int   `toml:"max_meaning_runes"`
	MaxContextRunes               int   `toml:"max_context_runes"`
	MaxObservationRunes           int   `toml:"max_observation_runes"`
	MaxObservationsPerScope       int   `toml:"max_observations_per_scope"`
	MaxObservationWritesPerMinute int   `toml:"max_observation_writes_per_minute"`
	MaxMineChars                  int   `toml:"max_mine_chars"`
	MineTimeoutSeconds            int   `toml:"mine_timeout_seconds"`
}

func (c SelfLearningConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// GroupKnowledgeConfig controls the deterministic per-group FAQ/knowledge
// base. It is intentionally separate from the LLM and never injects prompts.
type GroupKnowledgeConfig struct {
	Enabled            *bool `toml:"enabled"`
	MaxEntriesPerScope int   `toml:"max_entries_per_scope"`
	MaxQuestionRunes   int   `toml:"max_question_runes"`
	MaxAnswerRunes     int   `toml:"max_answer_runes"`
	MaxAliases         int   `toml:"max_aliases"`
	MaxKeywords        int   `toml:"max_keywords"`
	MaxMatchRunes      int   `toml:"max_match_runes"`
}

const (
	defaultGroupKnowledgeMaxEntries  = 200
	defaultGroupKnowledgeMaxQuestion = 200
	defaultGroupKnowledgeMaxAnswer   = 2000
	defaultGroupKnowledgeMaxAliases  = 8
	defaultGroupKnowledgeMaxKeywords = 16
	defaultGroupKnowledgeMaxMatch    = 2000
)

func (c GroupKnowledgeConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// Normalized fills zero limits with conservative defaults. An explicit 0 is
// treated as "use the default"; use enabled=false to disable the feature.
func (c GroupKnowledgeConfig) Normalized() GroupKnowledgeConfig {
	if c.MaxEntriesPerScope <= 0 {
		c.MaxEntriesPerScope = defaultGroupKnowledgeMaxEntries
	}
	if c.MaxQuestionRunes <= 0 {
		c.MaxQuestionRunes = defaultGroupKnowledgeMaxQuestion
	}
	if c.MaxAnswerRunes <= 0 {
		c.MaxAnswerRunes = defaultGroupKnowledgeMaxAnswer
	}
	if c.MaxAliases <= 0 {
		c.MaxAliases = defaultGroupKnowledgeMaxAliases
	}
	if c.MaxKeywords <= 0 {
		c.MaxKeywords = defaultGroupKnowledgeMaxKeywords
	}
	if c.MaxMatchRunes <= 0 {
		c.MaxMatchRunes = defaultGroupKnowledgeMaxMatch
	}
	return c
}

// GroupKnowledgeEntry is one deterministic FAQ entry. Match is exact
// (default), contains or keywords. Aliases participate in exact/contains
// matching; Keywords are required for keywords mode.
type GroupKnowledgeEntry struct {
	ID        string   `toml:"id"`
	Question  string   `toml:"question"`
	Answer    string   `toml:"answer"`
	Aliases   []string `toml:"aliases,omitempty"`
	Keywords  []string `toml:"keywords,omitempty"`
	Match     string   `toml:"match,omitempty"`
	Enabled   *bool    `toml:"enabled,omitempty"`
	UpdatedAt string   `toml:"updated_at,omitempty"`
}

func (e GroupKnowledgeEntry) IsEnabled() bool {
	return e.Enabled == nil || *e.Enabled
}

// GroupServicesConfig controls the deterministic group reminders, polls and
// sign-ups. All state is local and never enters the model prompt.
type GroupServicesConfig struct {
	Enabled              *bool `toml:"enabled"`
	MaxRemindersPerScope int   `toml:"max_reminders_per_scope"`
	MaxReminderDays      int   `toml:"max_reminder_days"`
	MaxPollsPerScope     int   `toml:"max_polls_per_scope"`
	MaxPollOptions       int   `toml:"max_poll_options"`
	MaxSignupsPerScope   int   `toml:"max_signups_per_scope"`
	MaxSignupCapacity    int   `toml:"max_signup_capacity"`
	MaxTextRunes         int   `toml:"max_text_runes"`
}

const (
	defaultGroupRemindersPerScope = 50
	defaultGroupReminderDays      = 365
	defaultGroupPollsPerScope     = 20
	defaultGroupPollOptions       = 10
	defaultGroupSignupsPerScope   = 20
	defaultGroupSignupCapacity    = 500
	defaultGroupServiceTextRunes  = 500
)

func (c GroupServicesConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// Normalized fills zero limits with conservative defaults.
func (c GroupServicesConfig) Normalized() GroupServicesConfig {
	if c.MaxRemindersPerScope <= 0 {
		c.MaxRemindersPerScope = defaultGroupRemindersPerScope
	}
	if c.MaxReminderDays <= 0 {
		c.MaxReminderDays = defaultGroupReminderDays
	}
	if c.MaxPollsPerScope <= 0 {
		c.MaxPollsPerScope = defaultGroupPollsPerScope
	}
	if c.MaxPollOptions <= 0 {
		c.MaxPollOptions = defaultGroupPollOptions
	}
	if c.MaxSignupsPerScope <= 0 {
		c.MaxSignupsPerScope = defaultGroupSignupsPerScope
	}
	if c.MaxSignupCapacity <= 0 {
		c.MaxSignupCapacity = defaultGroupSignupCapacity
	}
	if c.MaxTextRunes <= 0 {
		c.MaxTextRunes = defaultGroupServiceTextRunes
	}
	return c
}

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

// ServicesConfig is the shared, read-only service config loaded from
// [config_files].services. It intentionally excludes runtime-written state;
// StateConfig remains in its own file because ElBot rewrites it at runtime.
type ServicesConfig struct {
	Providers       map[string]ProviderConfig     `toml:"providers"`
	ModelMetadata   *ModelMetadataConfig          `toml:"model_metadata"`
	ModelProfiles   map[string]ModelProfileConfig `toml:"model_profiles"`
	ImageGeneration *ImageGenerationConfig        `toml:"image_generation"`
	ImageToPrompt   *ImageToPromptConfig          `toml:"image_to_prompt"`
	Vision          *VisionConfig                 `toml:"vision"`
	ASR             *ASRConfig                    `toml:"asr"`
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

// ImageGenerationConfig controls the optional image generation tool.
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

// ImageToPromptConfig controls the built-in image_to_prompt tool. It reuses one
// existing [providers.*] entry as its vision model, so no extra API key is
// needed; set provider + model (optionally enabled = false) to turn it on.
type ImageToPromptConfig struct {
	Enabled        *bool    `toml:"enabled"`
	Provider       string   `toml:"provider"`
	Model          string   `toml:"model"`
	MaxTokens      int      `toml:"max_tokens"`
	Temperature    *float64 `toml:"temperature"`
	MaxEdge        int      `toml:"max_edge"`
	MaxImageBytes  int64    `toml:"max_image_bytes"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
}

// TemperatureValue returns the configured sampling temperature (default 0.2).
// The field is a pointer so an explicit 0 is preserved instead of being treated
// as "unset"; the OpenAI adapter omits a non-positive temperature, which means
// "use the provider default".
func (c ImageToPromptConfig) TemperatureValue() float64 {
	if c.Temperature == nil {
		return 0.2
	}
	return *c.Temperature
}

// IsEnabled reports whether the tool is usable. It defaults to true once a
// provider and model are both configured, so enabling the tool stays a one-liner.
func (c ImageToPromptConfig) IsEnabled() bool {
	if c.Enabled != nil {
		return *c.Enabled
	}
	return strings.TrimSpace(c.Provider) != "" && strings.TrimSpace(c.Model) != ""
}

// VisionConfig controls the automatic vision fallback: when a text-only chat
// model rejects image content, ElBot describes the images with this vision model
// and retries with the descriptions in place of the images.
//
// The fallback is opt-in (enabled defaults to false) because it adds an extra
// model call and cost. When provider/model are left empty they are inherited
// from [image_to_prompt] at startup, so an existing install can enable it with a
// single "enabled = true".
type VisionConfig struct {
	Enabled        *bool    `toml:"enabled"`
	Provider       string   `toml:"provider"`
	Model          string   `toml:"model"`
	MaxTokens      int      `toml:"max_tokens"`
	Temperature    *float64 `toml:"temperature"`
	MaxEdge        int      `toml:"max_edge"`
	MaxImageBytes  int64    `toml:"max_image_bytes"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
	// Language is the description language for the fallback ("zh" or "en").
	Language                string `toml:"language"`
	CacheTTLSeconds         int    `toml:"cache_ttl_seconds"`
	NegativeCacheTTLSeconds int    `toml:"negative_cache_ttl_seconds"`
}

// IsEnabled reports whether the automatic vision fallback is switched on. It is
// explicit rather than inferred from provider/model so enabling it is never a
// surprise cost.
func (c VisionConfig) IsEnabled() bool {
	return c.Enabled != nil && *c.Enabled
}

// TemperatureValue returns the configured sampling temperature (default 0.2).
func (c VisionConfig) TemperatureValue() float64 {
	if c.Temperature == nil {
		return 0.2
	}
	return *c.Temperature
}

// LanguageValue normalizes the description language to "zh" or "en".
func (c VisionConfig) LanguageValue() string {
	if strings.EqualFold(strings.TrimSpace(c.Language), "en") {
		return "en"
	}
	return "zh"
}

// ASRConfig controls the optional voice-message transcription pipeline. It
// reuses one existing [providers.*] entry, so no extra API key is needed. The
// feature is opt-in (enabled defaults to false) because every voice message
// that wakes the bot adds one paid transcription call.
type ASRConfig struct {
	Enabled  *bool  `toml:"enabled"`
	Provider string `toml:"provider"`
	Model    string `toml:"model"`
	// Language is an optional ISO-639-1 hint ("zh", "en", ...). Empty means
	// the provider auto-detects.
	Language string `toml:"language"`
	// Prompt is an optional provider hint for names or domain terms.
	Prompt         string `toml:"prompt"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	MaxAudioBytes  int64  `toml:"max_audio_bytes"`
	MaxConcurrent  int    `toml:"max_concurrent"`
	QueueSize      int    `toml:"queue_size"`
	// MaxSegments bounds how many recordings one inbound message transcribes.
	MaxSegments int `toml:"max_segments"`

	CacheTTLSeconds         int `toml:"cache_ttl_seconds"`
	CacheMaxEntries         int `toml:"cache_max_entries"`
	NegativeCacheTTLSeconds int `toml:"negative_cache_ttl_seconds"`
	NegativeCacheMaxEntries int `toml:"negative_cache_max_entries"`

	MaxRetries               int `toml:"max_retries"`
	RetryInitialDelaySeconds int `toml:"retry_initial_delay_seconds"`
}

// IsEnabled reports whether ASR is explicitly switched on.
func (c ASRConfig) IsEnabled() bool {
	return c.Enabled != nil && *c.Enabled
}

// Normalized fills the documented defaults and trims user-provided strings.
func (c ASRConfig) Normalized() ASRConfig {
	out := c
	out.Provider = strings.TrimSpace(out.Provider)
	out.Model = strings.TrimSpace(out.Model)
	out.Language = strings.TrimSpace(out.Language)
	out.Prompt = strings.TrimSpace(out.Prompt)
	if out.TimeoutSeconds <= 0 {
		out.TimeoutSeconds = 120
	}
	if out.MaxAudioBytes <= 0 {
		out.MaxAudioBytes = 20 * 1024 * 1024
	}
	if out.MaxConcurrent <= 0 {
		out.MaxConcurrent = 2
	}
	if out.QueueSize <= 0 {
		out.QueueSize = 8
	}
	if out.MaxSegments <= 0 {
		out.MaxSegments = 4
	}
	if out.CacheTTLSeconds <= 0 {
		out.CacheTTLSeconds = 1800
	}
	if out.CacheMaxEntries <= 0 {
		out.CacheMaxEntries = 128
	}
	if out.NegativeCacheTTLSeconds <= 0 {
		out.NegativeCacheTTLSeconds = 30
	}
	if out.NegativeCacheMaxEntries <= 0 {
		out.NegativeCacheMaxEntries = 128
	}
	if out.MaxRetries <= 0 {
		out.MaxRetries = 2
	}
	if out.RetryInitialDelaySeconds <= 0 {
		out.RetryInitialDelaySeconds = 1
	}
	return out
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
	PrivacyCleanup     MaintenanceCleanupConfig `toml:"privacy_cleanup"`
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

var shanghaiLocation = func() *time.Location {
	if location, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return location
	}
	return time.FixedZone("CST", 8*3600)
}()

// PriceFor returns the effective price tier for model at at, applying the
// configured peak/off-peak rule. It is shared by the scheduled report and the
// live chat budget ledger so both bill against the same table.
func (c DailyReportConfig) PriceFor(model string, at time.Time) (ModelPriceConfig, bool) {
	price, ok := c.Prices[model]
	if !ok {
		return ModelPriceConfig{}, false
	}
	if !c.IsPeakPricing() || !isOffPeak(at, c.Holidays) {
		return price, true
	}
	return offpeakPrice(price), true
}

// ComputeCost returns the cost in the configured currency for one usage row.
func (p ModelPriceConfig) ComputeCost(promptTokens, completionTokens, cacheHitTokens int64) float64 {
	cacheHit := cacheHitTokens
	if cacheHit > promptTokens {
		cacheHit = promptTokens
	}
	if cacheHit < 0 {
		cacheHit = 0
	}
	cacheMiss := promptTokens - cacheHit
	if cacheMiss < 0 {
		cacheMiss = 0
	}
	cachePrice := p.CacheInputPerMillion
	if cachePrice <= 0 {
		cachePrice = p.InputPerMillion
	}
	return float64(cacheMiss)/1_000_000*p.InputPerMillion +
		float64(cacheHit)/1_000_000*cachePrice +
		float64(completionTokens)/1_000_000*p.OutputPerMillion
}

func offpeakPrice(price ModelPriceConfig) ModelPriceConfig {
	out := price
	if price.OffpeakInputPerMillion > 0 {
		out.InputPerMillion = price.OffpeakInputPerMillion
	}
	if price.OffpeakCacheInputPerMillion > 0 {
		out.CacheInputPerMillion = price.OffpeakCacheInputPerMillion
	}
	if price.OffpeakOutputPerMillion > 0 {
		out.OutputPerMillion = price.OffpeakOutputPerMillion
	}
	return out
}

func isOffPeak(at time.Time, holidays []string) bool {
	if at.IsZero() {
		return false
	}
	local := at.In(shanghaiLocation)
	day := local.Format("2006-01-02")
	for _, holiday := range holidays {
		if strings.TrimSpace(holiday) == day {
			return true
		}
	}
	switch local.Weekday() {
	case time.Saturday, time.Sunday:
		return true
	}
	minutes := local.Hour()*60 + local.Minute()
	peak := (minutes >= 9*60 && minutes < 12*60) || (minutes >= 14*60 && minutes < 18*60)
	return !peak
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

	cfg := newLoadConfig()
	if err := loadTOML(configPath, cfg); err != nil {
		return nil, err
	}
	cfg.applyAppDefaults()

	providersPath := ""
	if cfg.ConfigFiles.Providers != "" {
		providersPath = resolveRelative(configPath, cfg.ConfigFiles.Providers)
		providersCfg := &Config{}
		if err := loadTOML(providersPath, providersCfg); err != nil {
			return nil, err
		}
		cfg.mergeProviders(providersCfg)
	}

	servicesPath := ""
	if cfg.ConfigFiles.Services != "" {
		servicesPath = resolveRelative(configPath, cfg.ConfigFiles.Services)
		servicesCfg := &ServicesConfig{}
		if err := loadTOML(servicesPath, servicesCfg); err != nil {
			return nil, err
		}
		cfg.mergeServices(servicesCfg)
	}

	cfg.applyProviderDefaults()
	// The first defaults pass ran before the optional services file was
	// merged; run it again so a services-provided [image_generation] gets the
	// standard defaults too. All defaults are idempotent.
	cfg.applyAppDefaults()
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

	// state.toml is rewritten by ElBot at runtime. Sharing it with any
	// read-only config file would make SaveState overwrite that file with just
	// the state sections.
	for _, shared := range []struct {
		name string
		path string
	}{
		{"app", configPath},
		{"providers", providersPath},
		{"services", servicesPath},
		{"elnis", elnisPath},
		{"tool_tags", toolTagsPath},
	} {
		if shared.path != "" && statePath == shared.path {
			return nil, fmt.Errorf("config_files.state must point to a dedicated writable file, but %q is also used by %s config", statePath, shared.name)
		}
	}

	if err := cfg.validateProviderProtocols(); err != nil {
		return nil, err
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
	cfg.ServicesConfigPath = servicesPath
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
	Session         StateSessionConfig               `toml:"session"`
	ModeModels      map[string]ModelSelection        `toml:"mode_models"`
	NamingModel     ModelSelection                   `toml:"naming_model"`
	CompactModel    ModelSelection                   `toml:"compact_model"`
	ContextOverflow map[string]ContextOverflowConfig `toml:"context_overflow,omitempty"`
	GroupPolicy     map[string]GroupPolicyConfig     `toml:"group_policy,omitempty"`
	GroupRuntime    map[string]GroupRuntimeConfig    `toml:"group_runtime,omitempty"`
	GroupKnowledge  map[string][]GroupKnowledgeEntry `toml:"group_knowledge,omitempty"`
	GroupServices   StateGroupServicesConfig         `toml:"group_services,omitempty"`
	Budget          StateBudgetConfig                `toml:"budget,omitempty"`
}

// GroupRuntimeConfig is the persisted, event-derived runtime state of one
// group scope. It does not grant any permission; it only pauses paid calls and
// output while the bot itself is muted, removed or otherwise unavailable.
type GroupRuntimeConfig struct {
	State     string `toml:"state,omitempty"`
	Reason    string `toml:"reason,omitempty"`
	UpdatedAt string `toml:"updated_at,omitempty"`
}

// StateGroupServicesConfig stores deterministic group reminders, polls and
// sign-ups. Each map is keyed by platform:scope exactly like group policy.
type StateGroupServicesConfig struct {
	Reminders map[string][]GroupReminderConfig `toml:"reminders,omitempty"`
	Polls     map[string][]GroupPollConfig     `toml:"polls,omitempty"`
	Signups   map[string][]GroupSignupConfig   `toml:"signups,omitempty"`
}

type GroupReminderConfig struct {
	ID            string `toml:"id"`
	Text          string `toml:"text"`
	DueAt         string `toml:"due_at"`
	CreatedBy     string `toml:"created_by,omitempty"`
	CreatedByName string `toml:"created_by_name,omitempty"`
	CreatedAt     string `toml:"created_at,omitempty"`
	Status        string `toml:"status,omitempty"`
}

type GroupPollConfig struct {
	ID            string            `toml:"id"`
	Question      string            `toml:"question"`
	Options       []string          `toml:"options,omitempty"`
	Votes         map[string]int    `toml:"votes,omitempty"`
	Voters        map[string]string `toml:"voters,omitempty"`
	CreatedBy     string            `toml:"created_by,omitempty"`
	CreatedByName string            `toml:"created_by_name,omitempty"`
	CreatedAt     string            `toml:"created_at,omitempty"`
	Status        string            `toml:"status,omitempty"`
}

type GroupSignupConfig struct {
	ID            string            `toml:"id"`
	Title         string            `toml:"title"`
	Capacity      int               `toml:"capacity,omitempty"`
	Participants  map[string]string `toml:"participants,omitempty"`
	CreatedBy     string            `toml:"created_by,omitempty"`
	CreatedByName string            `toml:"created_by_name,omitempty"`
	CreatedAt     string            `toml:"created_at,omitempty"`
	Status        string            `toml:"status,omitempty"`
}

type ContextOverflowConfig struct {
	Chat string `toml:"chat,omitempty"`
	Work string `toml:"work,omitempty"`
}

// StateBudgetConfig stores the daily metering ledger. Reservations are keyed by
// date/kind/scope/actor/call-id, so replayed tool call IDs do not double count.
// Values are Unix timestamps and are pruned to the current and previous day.
type StateBudgetConfig struct {
	Reservations map[string]int64  `toml:"reservations,omitempty"`
	Digests      map[string]string `toml:"digests,omitempty"`
	Tokens       map[string]int64  `toml:"tokens,omitempty"`
	Costs        map[string]int64  `toml:"costs,omitempty"`
	Retries      map[string]int64  `toml:"retries,omitempty"`
	Executions   map[string]string `toml:"executions,omitempty"`
	// Uncertain marks hard-budget pre-reservations that could not be settled
	// with real provider usage. The reserved amount stays charged.
	Uncertain map[string]int64 `toml:"uncertain,omitempty"`
}

// ThreadModeGroup makes all members of a group scope share one Session and
// serializes their turns. The zero value keeps the historical per-user mode.
const ThreadModeGroup = "group"

const maxGroupMergeWindowMS = 10000

// GroupPolicyConfig is the per-group interaction and authorization policy. It
// is intentionally a local/server-side policy: prompts and roles never decide
// whether a group admin may change a setting or run a management command.
type GroupPolicyConfig struct {
	// WakeKeywords are additive to the platform-level trigger_keywords.
	WakeKeywords []string `toml:"wake_keywords,omitempty"`
	// ResponseMode controls when an ordinary group message wakes the LLM:
	// mention (default), all, keyword, reply or off.
	ResponseMode string `toml:"response_mode,omitempty"`
	// DefaultMode is the session mode used for newly created sessions in this
	// group (work or chat). Empty inherits the global default.
	DefaultMode string `toml:"default_mode,omitempty"`
	// DefaultModel is an optional model alias for this group. When
	// AllowedModels is non-empty the resolved selection must appear there.
	DefaultModel string `toml:"default_model,omitempty"`
	// AllowedModels is a superadmin-managed catalog for this group. Empty means
	// "only configured model aliases/profiles"; a non-empty list additionally
	// allows exact provider/model entries listed in it. The literal "*" keeps
	// the profile catalog open but still rejects unconfigured providers.
	AllowedModels []string `toml:"allowed_models,omitempty"`
	// ToolAllowlist restricts tool names for this group. ToolAllowlistSet
	// distinguishes "inherit global tools" (false) from "deny all group tools"
	// (true with an empty list).
	ToolAllowlist    []string `toml:"tool_allowlist,omitempty"`
	ToolAllowlistSet bool     `toml:"tool_allowlist_set,omitempty"`
	// ImageQuota and VisionQuota are daily per-group call budgets. 0 means no
	// local limit. UserImageQuota/UserVisionQuota additionally cap one actor
	// inside this group.
	ImageQuota      int `toml:"image_quota,omitempty"`
	VisionQuota     int `toml:"vision_quota,omitempty"`
	ASRQuota        int `toml:"asr_quota,omitempty"`
	UserImageQuota  int `toml:"user_image_quota,omitempty"`
	UserVisionQuota int `toml:"user_vision_quota,omitempty"`
	UserASRQuota    int `toml:"user_asr_quota,omitempty"`
	// ChatTokensQuota and ChatCostQuota cap one group's daily paid chat
	// usage. Cost is in [maintenance.daily_report].currency and stored in
	// micro-units. 0 means no local group limit.
	ChatTokensQuota int64   `toml:"chat_tokens_quota,omitempty"`
	ChatCostQuota   float64 `toml:"chat_cost_quota,omitempty"`
	// ThreadMode selects how group members share conversation state:
	// per_user (default) keeps one Session per actor; group uses one shared
	// Session per group scope and serializes turns across members.
	ThreadMode string `toml:"thread_mode,omitempty"`
	// MergeWindowMS is the quiet window used to merge consecutive messages
	// from the same actor before starting a turn. 0 disables pre-turn merging.
	// It is clamped to [0, 10000].
	MergeWindowMS int `toml:"merge_window_ms,omitempty"`
	// QuietHours is an optional local-time window such as "23:00-07:30".
	QuietHours string `toml:"quiet_hours,omitempty"`
	// Feature switches. nil means enabled (preserves existing behavior).
	GroupAnalysis *bool `toml:"group_analysis,omitempty"`
	Learning      *bool `toml:"learning,omitempty"`
	History       *bool `toml:"history,omitempty"`
	Knowledge     *bool `toml:"knowledge,omitempty"`
	Services      *bool `toml:"services,omitempty"`
	// ASR switches voice-message transcription for this group. nil means
	// enabled (subject to the global [asr] switch and per-group quotas).
	ASR *bool `toml:"asr,omitempty"`
	// LearningModeration grants the current group owner/admin permission to
	// review this group's learning candidates. Only a superadmin may set it.
	LearningModeration bool `toml:"learning_moderation,omitempty"`
	// LearningModerationActions narrows what the grant allows. Empty means the
	// safe default "view,decide". Only a superadmin may set it.
	LearningModerationActions []string `toml:"learning_moderation_actions,omitempty"`
}

// Normalize fills defaults and removes unsafe/empty values.
const (
	LearningModerationView   = "view"
	LearningModerationDecide = "decide"
	LearningModerationMine   = "mine"
	LearningModerationDelete = "delete"
	LearningModerationExport = "export"
	LearningModerationPolicy = "policy"
)

var allowedLearningModerationActions = map[string]bool{
	LearningModerationView:   true,
	LearningModerationDecide: true,
	LearningModerationMine:   true,
	LearningModerationDelete: true,
	LearningModerationExport: true,
	LearningModerationPolicy: true,
}

// Normalize fills defaults and removes unsafe/empty values.
func (c GroupPolicyConfig) Normalize() GroupPolicyConfig {
	out := c
	out.WakeKeywords = normalizeStringList(c.WakeKeywords)
	out.AllowedModels = normalizeStringList(c.AllowedModels)
	out.ToolAllowlist = normalizeStringList(c.ToolAllowlist)
	if c.ToolAllowlist != nil || len(out.ToolAllowlist) > 0 {
		out.ToolAllowlistSet = true
	}
	out.LearningModerationActions = normalizeLearningModerationActions(c.LearningModerationActions)
	if len(out.LearningModerationActions) == 0 && out.LearningModeration {
		out.LearningModerationActions = []string{LearningModerationView, LearningModerationDecide}
	}
	switch strings.ToLower(strings.TrimSpace(c.ResponseMode)) {
	case "all":
		out.ResponseMode = "all"
	case "keyword", "keywords":
		out.ResponseMode = "keyword"
	case "reply", "replies":
		out.ResponseMode = "reply"
	case "off", "none", "disabled":
		out.ResponseMode = "off"
	default:
		out.ResponseMode = "mention"
	}
	switch strings.ToLower(strings.TrimSpace(c.DefaultMode)) {
	case "chat":
		out.DefaultMode = "chat"
	case "work":
		out.DefaultMode = "work"
	default:
		out.DefaultMode = ""
	}
	switch strings.ToLower(strings.TrimSpace(c.ThreadMode)) {
	case "group", "shared", "multi", "multi_user", "multi-user":
		out.ThreadMode = ThreadModeGroup
	case "per_user", "per-user", "single", "isolated":
		out.ThreadMode = ""
	default:
		out.ThreadMode = ""
	}
	out.MergeWindowMS = c.MergeWindowMS
	if out.MergeWindowMS < 0 {
		out.MergeWindowMS = 0
	}
	if out.MergeWindowMS > maxGroupMergeWindowMS {
		out.MergeWindowMS = maxGroupMergeWindowMS
	}
	out.DefaultModel = strings.TrimSpace(c.DefaultModel)
	out.QuietHours = strings.TrimSpace(c.QuietHours)
	if out.ImageQuota < 0 {
		out.ImageQuota = 0
	}
	if out.VisionQuota < 0 {
		out.VisionQuota = 0
	}
	if out.ASRQuota < 0 {
		out.ASRQuota = 0
	}
	if out.UserImageQuota < 0 {
		out.UserImageQuota = 0
	}
	if out.UserVisionQuota < 0 {
		out.UserVisionQuota = 0
	}
	if out.UserASRQuota < 0 {
		out.UserASRQuota = 0
	}
	if out.ChatTokensQuota < 0 {
		out.ChatTokensQuota = 0
	}
	if out.ChatCostQuota < 0 {
		out.ChatCostQuota = 0
	}
	return out
}

func (c GroupPolicyConfig) ResponseModeValue() string {
	return c.Normalize().ResponseMode
}

// ThreadModeValue returns the effective thread mode, treating the zero value
// as the backwards-compatible per-user mode.
func (c GroupPolicyConfig) ThreadModeValue() string {
	if c.Normalize().ThreadMode == ThreadModeGroup {
		return ThreadModeGroup
	}
	return "per_user"
}

func (c GroupPolicyConfig) WakeKeywordsValue() []string {
	return c.Normalize().WakeKeywords
}

func (c GroupPolicyConfig) IsGroupAnalysisEnabled() bool {
	return c.GroupAnalysis == nil || *c.GroupAnalysis
}

func (c GroupPolicyConfig) IsLearningEnabled() bool {
	return c.Learning == nil || *c.Learning
}

func (c GroupPolicyConfig) IsHistoryEnabled() bool {
	return c.History == nil || *c.History
}

func (c GroupPolicyConfig) IsKnowledgeEnabled() bool {
	return c.Knowledge == nil || *c.Knowledge
}

func (c GroupPolicyConfig) IsServicesEnabled() bool {
	return c.Services == nil || *c.Services
}

func (c GroupPolicyConfig) IsASREnabled() bool {
	return c.ASR == nil || *c.ASR
}

func (c GroupPolicyConfig) IsZero() bool {
	n := c.Normalize()
	return len(n.WakeKeywords) == 0 && n.ResponseMode == "mention" && n.DefaultMode == "" && n.DefaultModel == "" && len(n.AllowedModels) == 0 && len(n.ToolAllowlist) == 0 && !n.ToolAllowlistSet && n.ImageQuota == 0 && n.VisionQuota == 0 && n.ASRQuota == 0 && n.UserImageQuota == 0 && n.UserVisionQuota == 0 && n.UserASRQuota == 0 && n.ChatTokensQuota == 0 && n.ChatCostQuota == 0 && n.ThreadMode == "" && n.MergeWindowMS == 0 && n.QuietHours == "" && c.GroupAnalysis == nil && c.Learning == nil && c.History == nil && c.Knowledge == nil && c.Services == nil && c.ASR == nil && !c.LearningModeration && len(n.LearningModerationActions) == 0
}

// ToolAllowlistRestricted reports whether a group has an explicit tool policy.
// A restricted policy with an empty list denies all group tools.
func (c GroupPolicyConfig) ToolAllowlistRestricted() bool {
	n := c.Normalize()
	return n.ToolAllowlistSet || len(n.ToolAllowlist) > 0
}

// LearningModerationActionsValue returns the effective actions for a granted
// group admin. It defaults to view+decide for old state files.
func (c GroupPolicyConfig) LearningModerationActionsValue() []string {
	n := c.Normalize()
	if len(n.LearningModerationActions) == 0 && n.LearningModeration {
		return []string{LearningModerationView, LearningModerationDecide}
	}
	return append([]string(nil), n.LearningModerationActions...)
}

func normalizeLearningModerationActions(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !allowedLearningModerationActions[value] || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func normalizeStringList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

type StateSessionConfig struct {
	DefaultMode string `toml:"default_mode"`
}

func LoadState(path string) (*StateConfig, error) {
	state := &StateConfig{}
	if err := loadTOML(path, state); err != nil {
		// SaveState replaces an existing state file through a backup swap. If
		// the process crashed after moving the old file aside but before the
		// new one was renamed into place, recover the last complete version.
		if errors.Is(err, os.ErrNotExist) {
			backup := path + ".bak"
			if backupErr := loadTOML(backup, state); backupErr == nil {
				return state, nil
			}
		}
		return nil, err
	}
	return state, nil
}

func SaveState(path string, state StateConfig) error {
	data, err := toml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state config dir %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary state config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary state config %q: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary state config %q: %w", tmpName, err)
	}
	// Flush file contents before the name switch. Without this a crash can
	// leave a zero-length or partially written state file even though rename
	// itself succeeded.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary state config %q: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary state config %q: %w", tmpName, err)
	}

	// Use a backup swap instead of falling back to os.WriteFile on platforms
	// where os.Rename cannot replace an existing file (notably Windows). The
	// old version stays intact until the new file is fully durable, so a
	// failed write never silently rolls the ledger back to zero.
	backup := path + ".bak"
	hadOriginal := false
	if _, statErr := os.Stat(path); statErr == nil {
		hadOriginal = true
		_ = os.Remove(backup)
		if err := os.Rename(path, backup); err != nil {
			return fmt.Errorf("move state config to backup %q: %w", backup, err)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat state config %q: %w", path, statErr)
	}
	if err := os.Rename(tmpName, path); err != nil {
		if hadOriginal {
			_ = os.Rename(backup, path)
		}
		return fmt.Errorf("replace state config %q: %w", path, err)
	}
	if hadOriginal {
		_ = os.Remove(backup)
	}
	return nil
}

func Default() *Config {
	cfg := defaultAppConfig()
	cfg.applyProviderDefaults()
	return cfg
}

func defaultAppConfig() *Config {
	cfg := newLoadConfig()
	cfg.applyAppDefaults()
	return cfg
}

// newLoadConfig returns a config with only the internal defaults that must
// exist before parsing app.toml (currently idle expiration). ConfigFiles
// defaults are applied after parsing so app.toml can opt into services.toml
// without inheriting the legacy providers.toml default.
func newLoadConfig() *Config {
	cfg := &Config{}
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

// DefaultModelSelection returns the model used by the configured default
// session mode. It falls back to chat, work, then any configured mode so
// auxiliary features can reuse the same default without inventing a new model
// slot.
func (c *Config) DefaultModelSelection() ModelSelection {
	if c == nil {
		return ModelSelection{}
	}
	mode := strings.TrimSpace(c.Session.DefaultMode)
	if mode == "" {
		mode = "work"
	}
	if selection := c.ModeModels[mode]; selection.Provider != "" && selection.Model != "" {
		return selection
	}
	for _, fallback := range []string{"chat", "work"} {
		if selection := c.ModeModels[fallback]; selection.Provider != "" && selection.Model != "" {
			return selection
		}
	}
	for _, selection := range c.ModeModels {
		if selection.Provider != "" && selection.Model != "" {
			return selection
		}
	}
	return ModelSelection{}
}

func (c *Config) applyAppDefaults() {
	// New installs use services.toml as the single read-only service config.
	// Keep the old providers.toml default only for configs that do not opt in
	// to the services file, so existing deployments keep loading unchanged.
	if c.ConfigFiles.Providers == "" && c.ConfigFiles.Services == "" {
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
	c.GroupKnowledge = c.GroupKnowledge.Normalized()
	c.GroupServices = c.GroupServices.Normalized()
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
	if c.ImageToPrompt.MaxTokens <= 0 {
		c.ImageToPrompt.MaxTokens = 400
	}
	if c.ImageToPrompt.Temperature == nil {
		defaultTemperature := 0.2
		c.ImageToPrompt.Temperature = &defaultTemperature
	}
	if c.ImageToPrompt.MaxEdge <= 0 {
		c.ImageToPrompt.MaxEdge = 1536
	}
	if c.ImageToPrompt.MaxImageBytes <= 0 {
		c.ImageToPrompt.MaxImageBytes = 12 * 1024 * 1024
	}
	if c.ImageToPrompt.TimeoutSeconds <= 0 {
		c.ImageToPrompt.TimeoutSeconds = 90
	}
	if c.Vision.MaxTokens <= 0 {
		c.Vision.MaxTokens = 400
	}
	if c.Vision.Temperature == nil {
		defaultVisionTemperature := 0.2
		c.Vision.Temperature = &defaultVisionTemperature
	}
	if c.Vision.MaxEdge <= 0 {
		c.Vision.MaxEdge = 1536
	}
	if c.Vision.MaxImageBytes <= 0 {
		c.Vision.MaxImageBytes = 12 * 1024 * 1024
	}
	if c.Vision.TimeoutSeconds <= 0 {
		c.Vision.TimeoutSeconds = 90
	}
	if c.Vision.CacheTTLSeconds <= 0 {
		c.Vision.CacheTTLSeconds = 1800
	}
	if c.Vision.NegativeCacheTTLSeconds <= 0 {
		c.Vision.NegativeCacheTTLSeconds = 30
	}
	// [vision] inherits the backend already configured for the image_to_prompt
	// tool so enabling the chat fallback stays a one-liner. An explicit provider
	// or model in [vision] always wins.
	if strings.TrimSpace(c.Vision.Provider) == "" && strings.TrimSpace(c.Vision.Model) == "" {
		c.Vision.Provider = c.ImageToPrompt.Provider
		c.Vision.Model = c.ImageToPrompt.Model
	}
	c.ASR = c.ASR.Normalized()
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
	if c.Ops.QueueMaxPerUser < 0 {
		c.Ops.QueueMaxPerUser = 0
	}
	if c.Ops.QueueMaxPerScope < 0 {
		c.Ops.QueueMaxPerScope = 0
	}
	if c.Ops.QueueWaitTimeoutSeconds < 0 {
		c.Ops.QueueWaitTimeoutSeconds = 0
	}
	if c.Ops.ProviderMaxConcurrent < 0 {
		c.Ops.ProviderMaxConcurrent = 0
	}
	if c.Ops.ProviderQueueMaxSize < 0 {
		c.Ops.ProviderQueueMaxSize = 0
	}
	if c.Ops.ProviderWaitTimeoutSecs < 0 {
		c.Ops.ProviderWaitTimeoutSecs = 0
	}
	c.BudgetLimits = c.BudgetLimits.Normalized()
	if c.Ops.CircuitBreakerFailureThreshold < 0 {
		c.Ops.CircuitBreakerFailureThreshold = 0
	}
	if c.Ops.CircuitBreakerOpenCooldownSeconds < 0 {
		c.Ops.CircuitBreakerOpenCooldownSeconds = 0
	}
	if c.Ops.CircuitBreakerHalfOpenMax <= 0 {
		c.Ops.CircuitBreakerHalfOpenMax = 1
	}
	for name, provider := range c.Providers {
		if provider.FallbackTimeoutSeconds < 0 {
			provider.FallbackTimeoutSeconds = 0
		}
		c.Providers[name] = provider
	}
	c.Context = c.Context.Normalized()
	if len(c.GroupPolicy) > 0 {
		for key, value := range c.GroupPolicy {
			key = strings.TrimSpace(key)
			if key == "" {
				delete(c.GroupPolicy, key)
				continue
			}
			value = value.Normalize()
			if value.IsZero() {
				delete(c.GroupPolicy, key)
				continue
			}
			c.GroupPolicy[key] = value
		}
	}
	if len(c.Commands.Prefixes) == 0 {
		c.Commands.Prefixes = []string{"/"}
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
	if c.Maintenance.PrivacyCleanup.Enabled && c.Maintenance.PrivacyCleanup.Schedule == "" {
		c.Maintenance.PrivacyCleanup.Schedule = "45 4 * * *"
	}
	if c.GroupAnalysis.ReportSchedule == "" {
		c.GroupAnalysis.ReportSchedule = "0 9 * * *"
	}
	if c.GroupAnalysis.ReportDays <= 0 {
		c.GroupAnalysis.ReportDays = 1
	}
	if c.AngelMemory.RetentionDays == 0 {
		c.AngelMemory.RetentionDays = 365
	}
	if c.SelfLearning.RetentionDays == 0 {
		c.SelfLearning.RetentionDays = 365
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
	if len(state.ContextOverflow) > 0 {
		if c.ContextOverflow == nil {
			c.ContextOverflow = map[string]ContextOverflowConfig{}
		}
		for key, value := range state.ContextOverflow {
			c.ContextOverflow[key] = value
		}
	}
	if len(state.GroupPolicy) > 0 {
		if c.GroupPolicy == nil {
			c.GroupPolicy = map[string]GroupPolicyConfig{}
		}
		for key, value := range state.GroupPolicy {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			value = value.Normalize()
			if value.IsZero() {
				delete(c.GroupPolicy, key)
				continue
			}
			c.GroupPolicy[key] = value
		}
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

// validateProviderProtocols rejects unrecognized api_mode values instead of
// letting them silently fall back to the chat protocol.
func (c *Config) validateProviderProtocols() error {
	for name, provider := range c.Providers {
		if _, ok := ParseAPIProtocol(provider.APIMode); !ok {
			return fmt.Errorf("[providers.%s].api_mode must be chat or response, got %q", name, provider.APIMode)
		}
		for model, modelConfig := range provider.ModelConfigs {
			if _, ok := ParseAPIProtocol(modelConfig.APIMode); !ok {
				return fmt.Errorf("[providers.%s.model_configs.%q].api_mode must be chat or response, got %q", name, model, modelConfig.APIMode)
			}
		}
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

// mergeServices overlays the shared services config on top of the legacy
// providers/app config. Sections that are absent from services.toml keep the
// value loaded from the legacy files, which makes migration gradual.
func (c *Config) mergeServices(services *ServicesConfig) {
	if len(services.Providers) > 0 {
		c.Providers = services.Providers
	}
	if services.ModelMetadata != nil {
		c.ModelMetadata = *services.ModelMetadata
	}
	if len(services.ModelProfiles) > 0 {
		c.ModelProfiles = services.ModelProfiles
	}
	if services.ImageGeneration != nil {
		c.ImageGeneration = *services.ImageGeneration
	}
	if services.ImageToPrompt != nil {
		c.ImageToPrompt = *services.ImageToPrompt
	}
	if services.Vision != nil {
		c.Vision = *services.Vision
	}
	if services.ASR != nil {
		c.ASR = *services.ASR
	}
}

func resolveRelative(baseFile, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(baseFile), path))
}
