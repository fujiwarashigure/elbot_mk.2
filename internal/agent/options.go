package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agentcommands "elbot/internal/agent/commands"
	"elbot/internal/angelmemory"
	"elbot/internal/character"
	"elbot/internal/config"
	"elbot/internal/delivery"
	"elbot/internal/hook"
	"elbot/internal/llm"
	"elbot/internal/media"
	"elbot/internal/memory/resident"
	"elbot/internal/platform"
	"elbot/internal/security"
	"elbot/internal/selflearning"
	"elbot/internal/session"
	"elbot/internal/storage"
	"elbot/internal/tool"
	"elbot/internal/utils/fileops"
)

// Options groups the agent's construction-time dependencies and configuration.
type Options struct {
	Platform        platform.PlatformAdapter
	Clients         map[string]llm.LLM
	ModeModels      map[string]config.ModelSelection
	Providers       map[string]config.ProviderConfig
	StatePath       string
	ContextOverflow map[string]config.ContextOverflowConfig
	GroupPolicy     map[string]config.GroupPolicyConfig
	GroupKnowledge  config.GroupKnowledgeConfig
	GroupServices   config.GroupServicesConfig
	Store           storage.Store
	Media           *media.Manager
	VisionDescriber VisionDescriber
	VisionSelection config.ModelSelection
	// GroupAnalysisSelection is the model used by the group_analysis summary
	// route. It is authorized against group policy before the tool runs.
	GroupAnalysisSelection config.ModelSelection
	// VisionParallelism, VisionMaxImages and VisionBudget bound the automatic
	// multi-image fallback: how many images are described at once, how large a
	// batch may be, and how long one batch may take when the caller context has
	// no deadline. Zero uses the DefaultVisionFallback* values; a negative
	// VisionBudget disables the extra time cap.
	VisionParallelism int
	VisionMaxImages   int
	VisionBudget      time.Duration
	// AudioTranscriber is the optional voice-message transcription backend.
	// ASRSelection is its provider/model pair, authorized against the group
	// model catalog before any paid call. ASRParallelism and ASRMaxSegments
	// bound one inbound message; ASRMaxAudioBytes bounds one recording read.
	AudioTranscriber    AudioTranscriber
	ASRSelection        config.ModelSelection
	ASRParallelism      int
	ASRMaxSegments      int
	ASRMaxAudioBytes    int64
	CommandPrefixes     []string
	SessionConfig       session.Config
	NamingSelection     config.ModelSelection
	NamingNotifier      session.NamingNotifier
	SoulPath            string
	ResidentMemoryStore *resident.Store
	AngelMemory         *angelmemory.Service
	AngelMemoryConfig   config.AngelMemoryConfig
	SelfLearning        *selflearning.Service
	CharacterStore      *character.Store
	LLMRequestConfig    config.LLMRequestConfig
	Ops                 config.OpsConfig
	BudgetLimits        config.BudgetLimitsConfig
	Pricing             config.DailyReportConfig
	HookService         agentcommands.HookService
	HookManager         hook.Manager
	HookRuntime         HookRouter
	OutputManager       delivery.Manager
	Logs                LogManager
	ToolRegistry        *tool.Registry
	// FileBackups keeps the pre-edit content of files ElBot edited, so
	// /rollback can undo them within the current Session.
	FileBackups           *fileops.RollbackStore
	Skills                SkillLifecycle
	ToolProvider          ToolSchemaProvider
	SecurityPolicy        *security.Policy
	ContextConfig         config.ContextConfig
	ModelMetadata         config.ModelMetadataConfig
	CompactModel          config.ModelSelection
	SessionListPageSize   int
	CleanupRetentionDays  int
	MediaRetentionDays    int
	SessionIdleExpiration config.SessionIdleExpirationConfig
	SandboxRoot           string
	ToolsConfig           config.ToolsConfig
	ToolTagsPath          string
	ToolTags              config.ToolTagsConfig
	ModelProfiles         map[string]config.ModelSelection
	ModelAliases          map[string]string
	ToolProfiles          map[string][]string
	ToolAliases           map[string]string
	ImageProfiles         map[string]bool
	ImageAliases          map[string]string
	TurnDirectives        config.TurnDirectivesConfig
}

func validateOptions(opts Options) error {
	workModel := opts.ModeModels[storage.SessionModeWork]
	if workModel.Provider == "" || workModel.Model == "" {
		return fmt.Errorf("mode_models.work provider/model is required")
	}
	if opts.Store == nil {
		return fmt.Errorf("store is required")
	}
	if len(opts.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	for name := range opts.Providers {
		if opts.Clients[name] == nil {
			return fmt.Errorf("client not found for provider %q", name)
		}
	}
	for mode, selection := range opts.ModeModels {
		if err := validateModelSelection("mode_models."+mode, selection, opts.Providers); err != nil {
			return err
		}
	}
	if err := validateOptionalModelSelection("naming_model", opts.NamingSelection, opts.Providers); err != nil {
		return err
	}
	if err := validateOptionalModelSelection("compact_model", opts.CompactModel, opts.Providers); err != nil {
		return err
	}
	if err := validateOptionalModelSelection("vision_model", opts.VisionSelection, opts.Providers); err != nil {
		return err
	}
	if err := validateOptionalModelSelection("group_analysis_model", opts.GroupAnalysisSelection, opts.Providers); err != nil {
		return err
	}
	if opts.SessionConfig.DefaultMode == "" {
		return fmt.Errorf("session default mode is required")
	}
	if opts.SessionListPageSize <= 0 {
		return fmt.Errorf("session list page size must be positive")
	}
	if opts.CleanupRetentionDays <= 0 {
		return fmt.Errorf("cleanup retention days must be positive")
	}
	if strings.TrimSpace(opts.SandboxRoot) == "" {
		return fmt.Errorf("sandbox root is required")
	}
	if opts.ToolsConfig.MaxRoundsPerTurn <= 0 {
		return fmt.Errorf("tools max rounds per turn must be positive")
	}
	if opts.SecurityPolicy == nil {
		return fmt.Errorf("security policy is required")
	}
	return nil
}

func validateOptionalModelSelection(name string, selection config.ModelSelection, providers map[string]config.ProviderConfig) error {
	if selection.Provider == "" && selection.Model == "" {
		return nil
	}
	return validateModelSelection(name, selection, providers)
}

func validateModelSelection(name string, selection config.ModelSelection, providers map[string]config.ProviderConfig) error {
	if selection.Provider == "" || selection.Model == "" {
		return fmt.Errorf("%s provider/model must both be set", name)
	}
	if _, ok := providers[selection.Provider]; !ok {
		return fmt.Errorf("%s provider %q not found", name, selection.Provider)
	}
	return nil
}

func (a *Agent) SetSessionListPageSize(size int) {
	if size <= 0 {
		size = config.Default().View.SessionListPageSize
	}
	a.sessionCommands.SetListPageSize(size)
}

func (a *Agent) SetCleanupRetentionDays(days int) {
	if days <= 0 {
		days = 30
	}
	a.sessionCommands.SetRetentionDays(days)
}

func (a *Agent) SetSessionIdleExpiration(cfg config.SessionIdleExpirationConfig) {
	a.idleExpiration = sessionIdleExpirationConfig(cfg)
}

func sessionIdleExpirationConfig(cfg config.SessionIdleExpirationConfig) session.IdleExpirationConfig {
	return session.IdleExpirationConfig{
		GroupUserTTLMinutes:         cfg.GroupUserTTLMinutes,
		GroupSuperadminTTLMinutes:   cfg.GroupSuperadminTTLMinutes,
		PrivateUserTTLMinutes:       cfg.PrivateUserTTLMinutes,
		PrivateSuperadminTTLMinutes: cfg.PrivateSuperadminTTLMinutes,
	}
}

func (a *Agent) SetSandboxRoot(root string) {
	root = filepath.Clean(root)
	if root == "." || root == "" {
		root = config.Default().Sandbox.Root
	}
	a.sandboxRoot = root
}

func (a *Agent) SetSecurityPolicy(policy *security.Policy) {
	if policy == nil {
		policy = security.DefaultPolicy()
	}
	a.securityPolicy = policy
}
