package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentcommands "elbot/internal/agent/commands"
	"elbot/internal/angelmemory"
	"elbot/internal/character"
	"elbot/internal/command"
	"elbot/internal/completion"
	"elbot/internal/config"
	"elbot/internal/delivery"
	"elbot/internal/hook"
	"elbot/internal/llm"
	"elbot/internal/logging"
	"elbot/internal/media"
	"elbot/internal/memory/resident"
	"elbot/internal/ops/concurrency"
	"elbot/internal/ops/ratelimit"
	"elbot/internal/platform"
	"elbot/internal/request"
	runtimestatus "elbot/internal/runtime"
	"elbot/internal/security"
	"elbot/internal/selflearning"
	"elbot/internal/session"
	"elbot/internal/storage"
	"elbot/internal/tool"
	"elbot/internal/turn"
	"elbot/internal/utils/fileops"
)

// Agent is the minimal agent core that handles messages and commands.
type Agent struct {
	platform                  platform.PlatformAdapter
	platformSenders           map[string]delivery.MessageSender
	modelRuntime              modelRuntimeState
	statePath                 string
	stateModTime              time.Time
	stateMu                   sync.Mutex
	stateWatchMu              sync.Mutex
	stateWatchStarted         bool
	contextOverflowMu         sync.RWMutex
	contextOverflow           map[string]config.ContextOverflowConfig
	groupPolicyMu             sync.RWMutex
	groupPolicy               map[string]config.GroupPolicyConfig
	groupKnowledgeMu          sync.RWMutex
	groupKnowledge            map[string][]config.GroupKnowledgeEntry
	groupKnowledgeCfg         config.GroupKnowledgeConfig
	groupKnowledgeWriteMu     sync.Mutex
	groupServicesMu           sync.RWMutex
	groupReminders            map[string][]config.GroupReminderConfig
	groupPolls                map[string][]config.GroupPollConfig
	groupSignups              map[string][]config.GroupSignupConfig
	groupServicesCfg          config.GroupServicesConfig
	groupServicesWriteMu      sync.Mutex
	groupServicesStarted      bool
	groupRuntimeMu            sync.RWMutex
	groupRuntime              map[string]config.GroupRuntimeConfig
	budgetMu                  sync.Mutex
	budgetReservations        map[string]int64
	budgetDigests             map[string]string
	budgetTokens              map[string]int64
	budgetCosts               map[string]int64
	budgetRetries             map[string]int64
	budgetExecutions          map[string]string
	budgetUncertain           map[string]int64
	budgetWriteFailed         bool
	budgetLimits              config.BudgetLimitsConfig
	pricing                   config.DailyReportConfig
	messageWorkMu             sync.Mutex
	messageWork               map[string]messageWorkRef
	turnMu                    sync.Mutex
	turnStates                map[string]bool
	recallMu                  sync.Mutex
	recentRecalls             map[string]time.Time
	store                     storage.Store
	media                     *media.Manager
	vision                    VisionDescriber
	visionSelection           config.ModelSelection
	groupAnalysisModel        config.ModelSelection
	visionParallelism         int
	visionMaxImages           int
	visionBudget              time.Duration
	asr                       AudioTranscriber
	asrSelection              config.ModelSelection
	asrParallelism            int
	asrMaxSegments            int
	asrMaxAudioBytes          int64
	sessions                  *session.Service
	requests                  *request.Manager
	turns                     *turn.Manager
	inboxMu                   sync.Mutex
	inbox                     *sessionInbox
	commands                  *command.Router
	commandExecutor           *commandExecutor
	completion                *completion.Service
	titleGen                  *titleGenerator
	soul                      SoulProvider
	residentMemory            *resident.Store
	angelMemory               *angelmemory.Service
	angelMemoryForgetOnRecall bool
	selfLearning              *selflearning.Service
	characters                *character.Store
	fileBackups               *fileops.RollbackStore
	modelProfiles             map[string]config.ModelSelection
	modelAliases              map[string]string
	toolProfiles              map[string][]string
	toolAliases               map[string]string
	imageProfiles             map[string]bool
	imageAliases              map[string]string
	turnDirectives            config.TurnDirectivesConfig
	promptBuilder             PromptBuilder
	toolRuntime               toolRuntimeState
	securityPolicy            *security.Policy
	contextRuntime            contextRuntimeState
	hooks                     hookRunner
	hookRuntime               HookRouter
	outputs                   delivery.Manager
	namingModelMu             sync.RWMutex
	namingModel               config.ModelSelection
	statusMu                  sync.Mutex
	runtimeStatus             map[string]runtimestatus.Snapshot
	sessionCommands           *agentcommands.SessionCommandState
	idleExpiration            session.IdleExpirationConfig
	mediaRetentionDays        int
	sandboxRoot               string
	logger                    *slog.Logger
	auditLogger               *slog.Logger
	logReader                 logging.Reader
	autoConfirmMu             sync.Mutex
	autoConfirmSession        map[string]bool
	autoConfirmTools          map[string]map[string]bool
	visionFallbackMu          sync.Mutex

	visionFallbackNotified  map[string]bool
	responseTimeout         time.Duration
	toolTimeout             time.Duration
	hookTimeout             time.Duration
	compressTimeout         time.Duration
	rateLimitUser           *ratelimit.Limiter
	rateLimitGroup          *ratelimit.Limiter
	rateLimitAllowed        atomic.Int64
	rateLimitRejected       atomic.Int64
	rateLimitUserRejected   atomic.Int64
	rateLimitGroupRejected  atomic.Int64
	rateLimitUserPerMinute  int
	rateLimitUserBurst      int
	rateLimitGroupPerMinute int
	rateLimitGroupBurst     int
	rateLimitMu             sync.Mutex
	rateLimitLastReason     string
	providerLimitMu         sync.Mutex
	providerLimiters        map[string]*concurrency.Limiter
	providerLimitCfg        concurrency.Config
	rateLimitLastRejectedAt time.Time
	userConfirmationTimeout time.Duration
	discoveredTools         map[string]map[string]llm.ToolSchema
	actorID                 string
	scopeID                 string
}

// New creates a new Agent.
func New(p platform.PlatformAdapter, client llm.LLM, model string, provider config.ProviderConfig, store storage.Store) *Agent {
	modeModels := map[string]config.ModelSelection{
		storage.SessionModeWork: {Provider: "default", Model: model},
		storage.SessionModeChat: {Provider: "default", Model: model},
	}
	return NewWithPrefixes(p, client, modeModels, provider, store, []string{"/"})
}

func NewWithPrefixes(p platform.PlatformAdapter, client llm.LLM, modeModels map[string]config.ModelSelection, provider config.ProviderConfig, store storage.Store, prefixes []string) *Agent {
	defaults := config.Default()
	agent, err := NewWithOptions(Options{
		Platform:              p,
		Clients:               map[string]llm.LLM{"default": client},
		ModeModels:            modeModels,
		Providers:             map[string]config.ProviderConfig{"default": provider},
		Store:                 store,
		CommandPrefixes:       prefixes,
		SessionConfig:         session.Config{NamingConfig: session.NamingConfig{TriggerStep: 1}, DefaultMode: storage.SessionModeWork},
		LLMRequestConfig:      defaults.LLMRequest,
		Ops:                   defaults.Ops,
		SecurityPolicy:        security.DefaultPolicy(),
		ContextConfig:         defaults.Context,
		SessionListPageSize:   defaults.View.SessionListPageSize,
		CleanupRetentionDays:  30,
		MediaRetentionDays:    defaults.Maintenance.SandboxCleanup.RetentionDays,
		SessionIdleExpiration: defaults.Session.IdleExpiration,
		SandboxRoot:           defaults.Sandbox.Root,
		ToolsConfig:           defaults.Tools,
		AngelMemoryConfig:     defaults.AngelMemory,
	})
	if err != nil {
		panic(err)
	}
	return agent
}

func responseTimeout(cfg config.LLMRequestConfig) time.Duration {
	if cfg.ResponseTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(cfg.ResponseTimeoutSeconds) * time.Second
}

func durationFromSeconds(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func queueWaitKinds(values []string, defaultEnabled bool) map[request.Kind]bool {
	out := map[request.Kind]bool{}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case string(request.KindTool):
			out[request.KindTool] = true
		case string(request.KindHook):
			out[request.KindHook] = true
		case string(request.KindCompress):
			out[request.KindCompress] = true
		case string(request.KindTurn):
			out[request.KindTurn] = true
		case string(request.KindLLM):
			out[request.KindLLM] = true
		case string(request.KindSubAgent):
			out[request.KindSubAgent] = true
		}
	}
	if len(out) == 0 && defaultEnabled {
		out[request.KindTool] = true
		out[request.KindHook] = true
		out[request.KindCompress] = true
	}
	return out
}

func newRateLimiter(messagesPerMinute, burst, idleTTLSeconds int) *ratelimit.Limiter {
	if messagesPerMinute <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = 1
	}
	idleTTL := durationFromSeconds(idleTTLSeconds)
	if idleTTL <= 0 {
		idleTTL = 10 * time.Minute
	}
	return ratelimit.New(ratelimit.Limit{
		RatePerSecond: float64(messagesPerMinute) / 60,
		Burst:         burst,
		IdleTTL:       idleTTL,
	})
}

func NewWithOptions(opts Options) (*Agent, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	p := opts.Platform
	modeModels := opts.ModeModels
	providers := opts.Providers
	statePath := opts.StatePath
	store := opts.Store
	prefixes := opts.CommandPrefixes
	sessionCfg := opts.SessionConfig
	namingSelection := opts.NamingSelection
	namingNotifier := opts.NamingNotifier
	soulPath := opts.SoulPath
	llmRequestConfig := opts.LLMRequestConfig
	hookService := opts.HookService
	workModel := modeModels[storage.SessionModeWork]
	provider := providers[workModel.Provider]
	clients := make(map[string]llm.LLM, len(opts.Clients))
	for name, configured := range opts.Clients {
		clients[name] = configured
	}
	client := clients[workModel.Provider]
	titleGen := &titleGenerator{primary: client, primaryModel: workModel.Model, naming: clients[namingSelection.Provider], namingModel: namingSelection.Model}
	promptSoul := SoulProvider(staticSoulProvider{Prompt: "You are a helpful assistant."})
	if soulPath != "" {
		promptSoul = &FileSoulProvider{Path: soulPath}
	}
	stateModTime := initialStateModTime(statePath)
	requests := request.NewManagerWithLimitsAndQueue(0, request.Limits{
		request.KindTurn:     opts.Ops.MaxConcurrentTurns,
		request.KindTool:     opts.Ops.MaxConcurrentTools,
		request.KindHook:     opts.Ops.MaxConcurrentHooks,
		request.KindCompress: opts.Ops.MaxConcurrentTurns,
	}, request.QueueConfig{
		MaxQueue:           opts.Ops.QueueMaxSize,
		MaxQueuePerFairKey: opts.Ops.QueueMaxPerUser,
		MaxQueuePerScope:   opts.Ops.QueueMaxPerScope,
		WaitTimeout:        durationFromSeconds(opts.Ops.QueueWaitTimeoutSeconds),
		WaitKinds:          queueWaitKinds(opts.Ops.QueueWaitKinds, opts.Ops.QueueMaxSize > 0),
	})
	turns := turn.NewManager()
	sessions := session.NewServiceWithConfig(store, sessionCfg, titleGen, namingNotifier)
	sessionCommands := agentcommands.NewSessionCommandState(opts.SessionListPageSize, opts.CleanupRetentionDays)
	policy := opts.SecurityPolicy
	hookManager := hookRunner(opts.HookManager)
	if hookManager == nil {
		hookManager = hook.NoopManager{}
	}
	outputs := opts.OutputManager
	if outputs.Sender == nil && outputs.Logger == nil {
		outputs = delivery.NewManager(nil, nil)
	}
	a := &Agent{
		platform:                  p,
		platformSenders:           map[string]delivery.MessageSender{},
		modelRuntime:              newModelRuntimeState(client, workModel.Model, workModel.Provider, provider, providers, modeModels, clients),
		statePath:                 statePath,
		stateModTime:              stateModTime,
		budgetLimits:              opts.BudgetLimits.Normalized(),
		pricing:                   opts.Pricing,
		contextOverflow:           cloneContextOverflow(opts.ContextOverflow),
		groupPolicy:               cloneGroupPolicy(opts.GroupPolicy),
		groupKnowledge:            map[string][]config.GroupKnowledgeEntry{},
		groupKnowledgeCfg:         opts.GroupKnowledge.Normalized(),
		groupReminders:            map[string][]config.GroupReminderConfig{},
		groupPolls:                map[string][]config.GroupPollConfig{},
		groupSignups:              map[string][]config.GroupSignupConfig{},
		groupServicesCfg:          opts.GroupServices.Normalized(),
		store:                     store,
		media:                     opts.Media,
		vision:                    opts.VisionDescriber,
		visionSelection:           opts.VisionSelection,
		visionParallelism:         opts.VisionParallelism,
		visionMaxImages:           opts.VisionMaxImages,
		visionBudget:              opts.VisionBudget,
		asr:                       opts.AudioTranscriber,
		asrSelection:              opts.ASRSelection,
		asrParallelism:            opts.ASRParallelism,
		asrMaxSegments:            opts.ASRMaxSegments,
		asrMaxAudioBytes:          opts.ASRMaxAudioBytes,
		mediaRetentionDays:        opts.MediaRetentionDays,
		sessions:                  sessions,
		requests:                  requests,
		turns:                     turns,
		inbox:                     newSessionInbox(),
		commands:                  command.NewRouter(prefixes),
		soul:                      promptSoul,
		residentMemory:            opts.ResidentMemoryStore,
		angelMemory:               opts.AngelMemory,
		angelMemoryForgetOnRecall: opts.AngelMemoryConfig.ForgetOnRecallEnabled(),
		selfLearning:              opts.SelfLearning,
		characters:                opts.CharacterStore,
		fileBackups:               opts.FileBackups,
		modelProfiles:             opts.ModelProfiles,
		modelAliases:              opts.ModelAliases,
		toolProfiles:              opts.ToolProfiles,
		toolAliases:               opts.ToolAliases,
		imageProfiles:             opts.ImageProfiles,
		imageAliases:              opts.ImageAliases,
		turnDirectives:            opts.TurnDirectives,
		securityPolicy:            policy,
		contextRuntime:            newContextRuntimeState(store, sessions, requests, turns),
		hooks:                     hookManager,
		hookRuntime:               opts.HookRuntime,
		outputs:                   outputs,
		namingModel:               namingSelection,
		runtimeStatus:             map[string]runtimestatus.Snapshot{},
		autoConfirmSession:        map[string]bool{},
		autoConfirmTools:          map[string]map[string]bool{},
		visionFallbackNotified:    map[string]bool{},
		providerLimiters:          map[string]*concurrency.Limiter{},
		providerLimitCfg: concurrency.Config{
			Max:         opts.Ops.ProviderMaxConcurrent,
			QueueSize:   opts.Ops.ProviderQueueMaxSize,
			WaitTimeout: durationFromSeconds(opts.Ops.ProviderWaitTimeoutSecs),
		},
		responseTimeout:         responseTimeout(llmRequestConfig),
		toolTimeout:             durationFromSeconds(opts.Ops.ToolTimeoutSeconds),
		hookTimeout:             durationFromSeconds(opts.Ops.HookTimeoutSeconds),
		compressTimeout:         durationFromSeconds(opts.Ops.CompressTimeoutSeconds),
		userConfirmationTimeout: defaultUserConfirmationTimeout,

		discoveredTools: map[string]map[string]llm.ToolSchema{},

		sessionCommands: sessionCommands,
		idleExpiration:  sessionIdleExpirationConfig(opts.SessionIdleExpiration),
		sandboxRoot:     filepath.Clean(strings.TrimSpace(opts.SandboxRoot)),
		actorID:         "cli:local",
		scopeID:         "local",
	}
	if opts.SelfLearning != nil {
		opts.SelfLearning.SetScopeEnabledPolicy(a.learningEnabledForScope)
	}
	a.contextRuntime.compressTimeout = durationFromSeconds(opts.Ops.CompressTimeoutSeconds)
	a.rateLimitUserPerMinute = opts.Ops.UserMessagesPerMinute
	a.rateLimitUserBurst = opts.Ops.UserBurst
	a.rateLimitGroupPerMinute = opts.Ops.GroupMessagesPerMinute
	a.rateLimitGroupBurst = opts.Ops.GroupBurst
	if opts.Ops.UserMessagesPerMinute > 0 {
		a.rateLimitUser = newRateLimiter(opts.Ops.UserMessagesPerMinute, opts.Ops.UserBurst, opts.Ops.RateLimitIdleTTLSeconds)
	}
	if opts.Ops.GroupMessagesPerMinute > 0 {
		a.rateLimitGroup = newRateLimiter(opts.Ops.GroupMessagesPerMinute, opts.Ops.GroupBurst, opts.Ops.RateLimitIdleTTLSeconds)
	}
	if opts.Logs != nil {
		a.SetLogManager(opts.Logs)
	}
	if defaultManager, ok := opts.HookManager.(*hook.DefaultManager); ok {
		defaultManager.SetWakeupFunc(a.hookWakeup)
		defaultManager.SetObserver(a.observeHookRun)
	}
	if opts.ToolRegistry != nil || opts.Skills != nil {
		a.SetToolRuntime(opts.ToolRegistry, opts.Skills)
	} else if opts.ToolProvider != nil {
		a.SetToolProvider(opts.ToolProvider)
	}
	a.SetToolConfig(opts.ToolsConfig)
	a.SetToolTagConfig(opts.ToolTagsPath, opts.ToolTags)
	a.rebuildSystemPrompt()
	for name, configured := range clients {
		a.attachLLMRetryNotifier(configured, name)
	}
	if p != nil {
		a.platformSenders[p.Name()] = p
	}
	a.SetContextOptions(opts.ContextConfig, opts.ModelMetadata, providers, opts.CompactModel)
	if err := agentcommands.RegisterDefaultModules(a.commands, agentcommands.Deps{
		Router:             a.commands,
		Sessions:           a.sessions,
		Requests:           a.requests,
		Turns:              a.turns,
		Store:              a.store,
		Scope:              a.scope,
		Models:             a,
		Compact:            a,
		ContextStatus:      a,
		ContextPolicy:      a,
		GroupPolicy:        a,
		GroupKnowledge:     a,
		MemberPanel:        a,
		GroupServices:      a,
		Tools:              a,
		Hooks:              hookService,
		SessionState:       sessionCommands,
		Characters:         a.characters,
		AngelMemory:        a.angelMemory,
		ResidentMemory:     a.residentMemory,
		SelfLearning:       a.selfLearning,
		Audit:              a.audit,
		Logs:               a,
		RuntimeState:       a,
		RuntimeStatus:      a.runtimeStatusForSession,
		CancelSessionInbox: a.cancelInboxSession,
		FileBackups:        a.fileBackups,
	}); err != nil {
		return nil, err
	}
	a.commandExecutor = &commandExecutor{
		router:        a.commands,
		sessions:      a.sessions,
		turns:         a.turns,
		scope:         a.scope,
		compactActive: a.compactActive,
		sendChat:      a.sendChat,
		sendNotice: func(ctx context.Context, text string) error {
			return a.sendNotice(ctx, delivery.Notice{Outputs: []delivery.Output{delivery.Text(text)}})
		},
		audit:           a.audit,
		handleAppend:    a.handleAppendConfirmationInput,
		handleRisk:      a.handleRiskConfirmationInput,
		continueInput:   a.continueCommandInput,
		groupAdminGrant: a.groupAdminCommandGrant,
	}
	a.completion = completion.NewService(
		completion.RiskConfirmationSource{Router: a.commands, Sessions: a.sessions, Turns: a.turns, Scope: a.scope, CommandNames: riskConfirmationCommandNames()},
		completion.ForkMessageSource{Router: a.commands, Sessions: a.sessions, Store: a.store, Scope: a.scope},
		completion.CharacterDirectiveSource{Characters: func(ctx context.Context) []completion.CharacterOption {
			if a.characters == nil || !a.characters.Enabled() {
				return nil
			}
			items, err := a.characters.List(ctx, a.characterViewer(ctx))
			if err != nil {
				return nil
			}
			out := make([]completion.CharacterOption, 0, len(items))
			for _, item := range items {
				out = append(out, completion.CharacterOption{ID: item.ID, Name: item.Name, Description: item.Description})
			}
			return out
		}},
		completion.ToolDirectiveSource{
			Registry:       func() *tool.Registry { return a.toolRuntime.registry },
			Actor:          a.actor,
			Policy:         func() *security.Policy { return a.securityPolicy },
			Tags:           a.completionToolTags,
			ToolNamesByTag: a.completionToolNamesByTag,
		},
		completion.RouterSource{Router: a.commands, Actor: a.actor},
	)

	a.loadRuntimeStateAtStartup()
	return a, nil
}

type staticSoulProvider struct {
	Prompt string
}

func (p staticSoulProvider) SystemPrompt(context.Context, string) (string, error) {
	return p.Prompt, nil
}

func cloneModeModels(models map[string]config.ModelSelection) map[string]config.ModelSelection {
	out := map[string]config.ModelSelection{}
	for mode, model := range models {
		out[mode] = model
	}
	return out
}

func (a *Agent) providerLimiter(providerName string) *concurrency.Limiter {
	if a == nil || a.providerLimitCfg.Max <= 0 {
		return nil
	}
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		providerName = "default"
	}
	a.providerLimitMu.Lock()
	defer a.providerLimitMu.Unlock()
	if a.providerLimiters == nil {
		a.providerLimiters = map[string]*concurrency.Limiter{}
	}
	limiter := a.providerLimiters[providerName]
	if limiter == nil {
		limiter = concurrency.New(a.providerLimitCfg)
		a.providerLimiters[providerName] = limiter
	}
	return limiter
}

func (a *Agent) acquireProviderSlot(ctx context.Context, providerName string) (func(), error) {
	limiter := a.providerLimiter(providerName)
	if limiter == nil {
		return func() {}, nil
	}
	release, err := limiter.Acquire(ctx)
	if err != nil {
		return func() {}, fmt.Errorf("provider %s concurrency limit: %w", providerName, err)
	}
	return release, nil
}
