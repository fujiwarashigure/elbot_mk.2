package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elbot/internal/agent"
	"elbot/internal/command"
	"elbot/internal/config"
	elcron "elbot/internal/cron"
	"elbot/internal/delivery"
	"elbot/internal/elvena"
	"elbot/internal/groupanalysis"
	"elbot/internal/hook"
	hookbuiltin "elbot/internal/hook/builtin"
	hookcontrol "elbot/internal/hook/control"
	hookruntime "elbot/internal/hook/runtime"
	"elbot/internal/media"
	"elbot/internal/memory/resident"
	"elbot/internal/ops/diskguard"
	"elbot/internal/processenv"
	"elbot/internal/security"
	"elbot/internal/session"
	"elbot/internal/storage"
	"elbot/internal/tool/builtin"
	"elbot/internal/tool/runtimeinfo"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

type defaultRuntimeFactory struct{}

func (defaultRuntimeFactory) Build(ctx context.Context, req RuntimeRequest) (*RuntimeComponents, error) {
	foundation := req.Foundation
	cfg := foundation.Config
	logger := foundation.Logger
	dotEnv, err := config.LoadDotEnv(filepath.Dir(cfg.ConfigPath))
	if err != nil {
		return nil, fmt.Errorf("load process environment: %w", err)
	}
	baseProcessEnv := processenv.New(os.Environ())
	credentialEnv := baseProcessEnv.Fill(dotEnv)
	// Shell / Skill / Go Skill child processes are powerful, so they only get
	// non-credential variables from .env. Parent-side tools (web search, image
	// generation, media download) still use credentialEnv to resolve keys.
	shellProcessEnv := credentialEnv.WithoutSensitiveKeys()
	hookProcessEnv := hook.ProcessEnvironment(baseProcessEnv)
	fileDeliveryCredentials, err := resolveFileDeliveryCredentials(cfg.FileDelivery, filepath.Dir(cfg.ConfigPath))
	if err != nil {
		logger.Warn("S3 media backend is unavailable; remote operations will fail until configuration is fixed", "error", err)
		fileDeliveryCredentials = nil
	}
	var agt *agent.Agent
	sendNotice := func(ctx context.Context, target delivery.Target, outputs []delivery.Output) (delivery.Receipt, error) {
		if agt == nil {
			return delivery.Receipt{}, fmt.Errorf("agent is not ready")
		}
		return agt.SendNotice(ctx, delivery.Notice{Target: target, Outputs: outputs})
	}

	cronService, err := buildCronService(ctx, foundation, sendNotice)
	if err != nil {
		return nil, err
	}

	mediaCenter, err := media.NewConfigured(ctx, foundation.Store, filepath.Join(filepath.Dir(cfg.Sandbox.Root), "media"), cfg.FileDelivery, fileDeliveryCredentials)
	if err != nil {
		return nil, err
	}
	if err := foundation.Store.Media().RecoverInterrupted(ctx); err != nil {
		return nil, err
	}
	mediaCenter.History = foundation.ChatHistory
	if err := mediaCenter.ReconcileHistory(ctx); err != nil {
		return nil, err
	}
	mediaCenter.MaxImportBytes = cfg.PlatformFiles.MaxReceiveFileBytes
	mediaCenter.DownloadTimeout = time.Duration(cfg.PlatformFiles.DownloadTimeoutSecs) * time.Second
	mediaCenter.Media = cfg.Media
	mediaCenter.Logger = logger
	mediaCenter.Guard = diskguard.New(filepath.Dir(cfg.Storage.SessionsSQLitePath), diskguard.Config{
		WarnRatio:     cfg.Storage.DiskWarnRatio,
		CriticalRatio: cfg.Storage.DiskCriticalRatio,
		MinFreeBytes:  uint64(maxInt64(cfg.Storage.DiskMinFreeBytes, 0)),
	})
	if foundation.Maintenance != nil {
		foundation.Maintenance.Media = mediaCenter
	}
	imageRewriter := buildImagePromptRewriter(cfg, req.Models)
	visionMetrics := newVisionMetricsState()
	imageToPrompt, err := buildImagePromptService(ctx, cfg, req.Models, visionMetrics.countersValue())
	if err != nil {
		return nil, err
	}
	visionMetrics.set("image_to_prompt", imageToPrompt)
	var groupAnalysisSummarizer groupanalysis.Summarizer
	if selection := cfg.DefaultModelSelection(); selection.Provider != "" && selection.Model != "" {
		if client := req.Models.ByProvider[selection.Provider]; client != nil {
			groupAnalysisSummarizer = groupanalysis.LLMSummarizer{Client: client, Model: selection.Model}
		}
	}
	toolRuntime, err := builtin.NewRuntime(builtin.RuntimeOptions{
		ConfigDir: filepath.Dir(cfg.ConfigPath),
		RuntimeInfo: runtimeinfo.Info{
			ConfigPath:   cfg.ConfigPath,
			SandboxRoot:  cfg.Sandbox.Root,
			FileDelivery: cfg.FileDelivery,
		},
		CronService:             cronService,
		ChatHistory:             foundation.ChatHistory,
		OutboundMessages:        foundation.OutboundMessages,
		GroupAnalysis:           cfg.GroupAnalysis,
		GroupAnalysisSummarizer: groupAnalysisSummarizer,
		AngelMemory:             cfg.AngelMemory,
		SelfLearning:            cfg.SelfLearning,
		Store:                   foundation.Store,
		Media:                   mediaCenter,
		ResidentMemoryMaxUnits:  resident.Limits{Core: cfg.ResidentMemory.CoreMaxUnits, Normal: cfg.ResidentMemory.NormalMaxUnits},
		ResidentMemoryPolicy: resident.NormalWritePolicy{
			MinInterval:              time.Duration(cfg.ResidentMemory.NormalWriteMinIntervalSecondsValue()) * time.Second,
			Window:                   time.Duration(cfg.ResidentMemory.NormalWriteWindowSecondsValue()) * time.Second,
			MaxWrites:                cfg.ResidentMemory.NormalWriteMaxPerWindowValue(),
			MaxLines:                 cfg.ResidentMemory.NormalMaxLinesValue(),
			MaxUnitsPerEntry:         cfg.ResidentMemory.NormalMaxUnitsPerEntryValue(),
			BlockInstructionPatterns: cfg.ResidentMemory.IsNormalBlockInstructionPatterns(),
		},
		CharacterEnabled:   cfg.CharacterLibrary.IsEnabled(),
		CharacterRoot:      cfg.CharacterLibrary.Root,
		ImageGeneration:    cfg.ImageGeneration,
		PromptRewriter:     imageRewriter,
		ImagePromptService: imageToPrompt,
		ProcessEnv:         credentialEnv,
		ChildProcessEnv:    shellProcessEnv,
	})
	if err != nil {
		return nil, err
	}
	req.Profiler.Mark("builtin tools register")
	if cfg.GroupAnalysis.IsReportEnabled() && toolRuntime.GroupAnalysis != nil {
		if err := foundation.CronManager.RegisterHandler("group_analysis.report", func(ctx context.Context, job storage.CronJob) error {
			days := cfg.GroupAnalysis.ReportDays
			if days <= 0 {
				days = 1
			}
			now := time.Now()
			report, err := toolRuntime.GroupAnalysis.Analyze(ctx, groupanalysis.Request{
				Platform: cfg.GroupAnalysis.ReportPlatform,
				ScopeID:  cfg.GroupAnalysis.ReportScopeID,
				Since:    now.AddDate(0, 0, -days),
				Until:    now,
			})
			if err != nil {
				return err
			}
			text := report.FormatText()
			if summary, summaryErr := toolRuntime.GroupAnalysis.Summarize(ctx, report); summaryErr == nil && strings.TrimSpace(summary) != "" {
				text = "摘要：" + strings.TrimSpace(summary) + "\n\n" + text
			}
			target := delivery.Target{Platform: cfg.GroupAnalysis.ReportPlatform, ScopeID: cfg.GroupAnalysis.ReportScopeID}
			if target.Empty() {
				target.Superadmins = true
			}
			_, err = sendNotice(ctx, target, []delivery.Output{delivery.Text(text)})
			return err
		}); err != nil {
			return nil, err
		}
		if _, err := foundation.CronManager.UpsertJob(ctx, elcron.UpsertJobRequest{
			Name:     "system.group_analysis.report",
			Handler:  "group_analysis.report",
			Schedule: cfg.GroupAnalysis.ReportSchedule,
			Enabled:  true,
		}); err != nil {
			return nil, err
		}
	}
	if foundation.Maintenance != nil {
		if toolRuntime.AngelMemory != nil {
			foundation.Maintenance.RegisterRetentionCleanup("angel_memory", func(ctx context.Context, cutoff time.Time) error {
				_, err := toolRuntime.AngelMemory.DeleteBefore(ctx, cutoff)
				return err
			})
		}
		if toolRuntime.SelfLearning != nil {
			foundation.Maintenance.RegisterRetentionCleanup("self_learning", func(ctx context.Context, cutoff time.Time) error {
				_, _, err := toolRuntime.SelfLearning.DeleteBefore(ctx, cutoff)
				return err
			})
		}
	}
	toolRuntime.SkillManager.StartDelayedReload(ctx, time.Second)
	req.Profiler.Mark("skill reload scheduled")

	hooks := hook.NewManager()
	hooks.SetLogger(logger)
	securityPolicy := security.NewPolicy(cfg.Security.UserMaxToolRisk, cfg.Security.SuperadminConfirmRisk, cfg.Security.Superadmins)
	elvenaBus := elvena.NewBus()

	startupHookNotices := []string{}
	notifyHookIssue := func(ctx context.Context, text string) {
		if agt == nil {
			startupHookNotices = append(startupHookNotices, text)
			return
		}
		_, _ = agt.SendNotice(ctx, delivery.Notice{Outputs: []delivery.Output{delivery.Text(text)}, Level: slog.LevelWarn})
	}

	hookRuntime := hookruntime.NewManager(hookruntime.Options{
		Media:      mediaCenter,
		Registry:   toolRuntime.Registry,
		Logger:     logger,
		Audit:      auditFunc(foundation.Logs),
		Send:       sendNotice,
		SharedDir:  filepath.Join(config.PluginConfigDir(cfg.ConfigPath), "_shared"),
		ProcessEnv: hookProcessEnv,
	})

	hookService := buildHookService(foundation, req.Platforms, toolRuntime, cronService, hooks, hookRuntime, hookProcessEnv, notifyHookIssue, sendNotice)
	req.Profiler.Mark("hook register")

	agt, err = buildAgent(ctx, foundation, req.Models, req.Platforms, toolRuntime, securityPolicy, hooks, hookRuntime, hookService, visionMetrics)
	if err != nil {
		if closeErr := hookRuntime.Close(context.Background()); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup hook runtime after agent build: %w", closeErr))
		}
		return nil, err
	}
	cronService.SetRunner(agt)
	if foundation.Maintenance != nil {
		foundation.Maintenance.Report = func(ctx context.Context, text string) error {
			if agt == nil {
				return fmt.Errorf("agent is not ready")
			}
			_, err := sendNotice(ctx, delivery.Target{
				Platform:    cfg.Maintenance.DailyReport.Platform,
				Superadmins: true,
			}, []delivery.Output{delivery.Text(text)})
			return err
		}
	}
	for _, notice := range startupHookNotices {
		notifyHookIssue(context.Background(), notice)
	}
	req.Profiler.Mark("agent init")

	return &RuntimeComponents{
		Media:        mediaCenter,
		Agent:        agt,
		Handler:      agt,
		CronService:  cronService,
		ElvenaBus:    elvenaBus,
		ImageLimiter: toolRuntime.ImageLimiter,
		VisionStats:  visionMetrics.snapshot,
		Lifecycle:    hookRuntimeLifecycle{runtime: hookRuntime},
	}, nil
}

func maxInt64(value, fallback int64) int64 {
	if value > fallback {
		return value
	}
	return fallback
}

func resolveFileDeliveryCredentials(cfg config.FileDeliveryConfig, configDir string) (aws.CredentialsProvider, error) {
	backend := strings.TrimSpace(cfg.Backend)
	if backend == "" {
		backend = config.Default().FileDelivery.Backend
	}
	if backend != "s3" && backend != "hybrid" {
		return nil, nil
	}
	resolve := func(name, label string) (string, error) {
		name = strings.TrimSpace(name)
		if name == "" {
			return "", fmt.Errorf("%s environment variable name is empty", label)
		}
		value, ok, err := config.ConfigEnv(name, configDir)
		if err != nil {
			return "", fmt.Errorf("resolve %s environment variable %q: %w", label, name, err)
		}
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			return "", fmt.Errorf("%s environment variable %q is not configured", label, name)
		}
		return value, nil
	}
	accessKey, err := resolve(cfg.S3AccessKeyEnv, "s3 access key")
	if err != nil {
		return nil, err
	}
	secretKey, err := resolve(cfg.S3SecretKeyEnv, "s3 secret key")
	if err != nil {
		return nil, err
	}
	return credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""), nil
}

func buildCronService(ctx context.Context, foundation *FoundationComponents, send func(context.Context, delivery.Target, []delivery.Output) (delivery.Receipt, error)) (*elcron.Service, error) {
	cfg := foundation.Config
	service := elcron.NewService(elcron.Options{
		Manager:          foundation.CronManager,
		Store:            foundation.Store,
		Logger:           foundation.Logger,
		EnabledPlatforms: enabledCronPlatforms(cfg),
		SandboxRoot:      cfg.Sandbox.Root,
		CommandPrefix:    command.PrimaryPrefix(cfg.Commands.Prefixes),
		Audit:            auditFunc(foundation.Logs),
		SendTarget:       send,
	})
	if err := service.MigrateLegacyDeliveryState(ctx); err != nil {
		return nil, err
	}
	if err := foundation.CronManager.RegisterHandler(elcron.UserHandlerName, service.Handler); err != nil {
		return nil, err
	}
	return service, nil
}

func buildHookService(
	foundation *FoundationComponents,
	platforms PlatformComponents,
	toolRuntime *builtin.Runtime,
	cronService *elcron.Service,
	hooks *hook.DefaultManager,
	hookRuntime *hookruntime.Manager,
	hookProcessEnv hook.ProcessEnvironment,
	notifyHookIssue func(context.Context, string),
	sendNotice func(context.Context, delivery.Target, []delivery.Output) (delivery.Receipt, error),
) *hookcontrol.Service {
	cfg := foundation.Config
	hookOpts := hookbuiltin.Options{
		ConfigDir:        config.PluginConfigDir(cfg.ConfigPath),
		Tools:            toolRuntime.Registry,
		Logger:           foundation.Logger,
		Audit:            auditFunc(foundation.Logs),
		Notify:           notifyHookIssue,
		Send:             sendNotice,
		PlatformCallers:  hookPlatformCallerResolver{runtimes: platforms.Runtimes},
		Runtime:          hookRuntime,
		ProcessEnv:       hookProcessEnv,
		OutboundMessages: foundation.OutboundMessages,
		AngelMemory:      toolRuntime.AngelMemory,
		SelfLearning:     toolRuntime.SelfLearning,
	}

	loadHooks := func(registrar hook.Registrar) (hook.ReloadReport, []hookruntime.Config, error) {
		var notices []string
		loadOpts := hookOpts
		loadOpts.Notify = func(_ context.Context, text string) {
			text = strings.TrimSpace(text)
			if text != "" {
				notices = append(notices, text)
			}
		}
		configs, err := hookbuiltin.RegisterAll(registrar, loadOpts)
		if err == nil {
			err = registerCronPlatformHook(registrar, cronService)
		}
		return hook.ReloadReport{Notices: notices}, configs, err
	}
	hookService := hookcontrol.New(hooks, hookRuntime, loadHooks)
	hookRuntime.SetPluginReloadPreparer(hookService.PreparePluginReload)
	report, err := hookService.HookReload()
	for _, notice := range report.Notices {
		notifyHookIssue(context.Background(), notice)
	}
	if err != nil {
		foundation.Logger.Error("hook registration failed", "error", err)
		notifyHookIssue(context.Background(), fmt.Sprintf("Hook 注册失败：%v", err))
	}

	return hookService
}

func buildAgent(
	ctx context.Context,
	foundation *FoundationComponents,
	models ModelClients,
	platforms PlatformComponents,
	toolRuntime *builtin.Runtime,
	securityPolicy *security.Policy,
	hooks *hook.DefaultManager,
	hookRuntime *hookruntime.Manager,
	hookService *hookcontrol.Service,
	visionMetrics *visionMetricsState,
) (*agent.Agent, error) {
	cfg := foundation.Config
	modelProfiles := map[string]config.ModelSelection{}
	modelAliases := map[string]string{}
	for name, profile := range cfg.ModelProfiles {
		if strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
			continue
		}
		if models.ByProvider[profile.Provider] == nil {
			continue
		}
		modelProfiles[name] = config.ModelSelection{Provider: profile.Provider, Model: profile.Model}
		registerTurnAlias(modelAliases, name, name)
		for _, alias := range profile.Aliases {
			registerTurnAlias(modelAliases, alias, name)
		}
	}
	toolProfiles := map[string][]string{}
	toolAliases := map[string]string{}
	for name, profile := range cfg.ToolProfiles {
		if len(profile.Tools) == 0 {
			continue
		}
		toolProfiles[name] = append([]string(nil), profile.Tools...)
		registerTurnAlias(toolAliases, name, name)
		for _, alias := range profile.Aliases {
			registerTurnAlias(toolAliases, alias, name)
		}
	}
	imageProfiles := map[string]bool{}
	imageAliases := map[string]string{}
	if toolRuntime != nil {
		for name := range toolRuntime.ImageProfiles {
			if strings.TrimSpace(name) == "" {
				continue
			}
			imageProfiles[name] = true
			registerTurnAlias(imageAliases, name, name)
			if profile, ok := cfg.ImageGeneration.Profiles[name]; ok {
				for _, alias := range profile.Aliases {
					registerTurnAlias(imageAliases, alias, name)
				}
			}
		}
	}
	visionDescriber, err := buildVisionDescriber(ctx, cfg, models, visionMetrics.countersValue())
	if err != nil {
		return nil, err
	}
	if describer, ok := visionDescriber.(visionChatDescriber); ok {
		visionMetrics.set("fallback", describer.service)
	}
	agt, err := agent.NewWithOptions(agent.Options{
		Platform:              platforms.Primary,
		Clients:               models.ByProvider,
		ModeModels:            cfg.ModeModels,
		Providers:             cfg.Providers,
		StatePath:             cfg.StateConfigPath,
		ContextOverflow:       cfg.ContextOverflow,
		Store:                 foundation.Store,
		Media:                 toolRuntime.FileManager.Media,
		VisionDescriber:       visionDescriber,
		CommandPrefixes:       cfg.Commands.Prefixes,
		SessionConfig:         session.Config{NamingConfig: session.NamingConfig{TriggerStep: cfg.Session.Naming.TriggerStep}, DefaultMode: cfg.Session.DefaultMode},
		NamingSelection:       cfg.NamingModel,
		NamingNotifier:        namingLogger{logger: foundation.Logger},
		SoulPath:              cfg.Soul.Path,
		ResidentMemoryStore:   toolRuntime.ResidentMemoryStore,
		AngelMemory:           toolRuntime.AngelMemory,
		SelfLearning:          toolRuntime.SelfLearning,
		CharacterStore:        toolRuntime.CharacterStore,
		LLMRequestConfig:      cfg.LLMRequest,
		Ops:                   cfg.Ops,
		HookService:           hookService,
		HookManager:           hooks,
		HookRuntime:           hookRuntime,
		OutputManager:         delivery.NewManager(nil, foundation.Logger),
		Logs:                  foundation.Logs,
		ToolRegistry:          toolRuntime.Registry,
		Skills:                toolRuntime.SkillManager,
		SecurityPolicy:        securityPolicy,
		ContextConfig:         cfg.Context,
		ModelMetadata:         cfg.ModelMetadata,
		CompactModel:          cfg.CompactModel,
		SessionListPageSize:   cfg.View.SessionListPageSize,
		CleanupRetentionDays:  cfg.Maintenance.SessionCleanup.RetentionDays,
		MediaRetentionDays:    cfg.Maintenance.SandboxCleanup.RetentionDays,
		SessionIdleExpiration: cfg.Session.IdleExpiration,
		SandboxRoot:           cfg.Sandbox.Root,
		ToolsConfig:           cfg.Tools,
		ToolTagsPath:          cfg.ToolTagsConfigPath,
		ToolTags:              cfg.ToolTags,
		ModelProfiles:         modelProfiles,
		ModelAliases:          modelAliases,
		ToolProfiles:          toolProfiles,
		ToolAliases:           toolAliases,
		ImageProfiles:         imageProfiles,
		ImageAliases:          imageAliases,
		TurnDirectives:        cfg.TurnDirectives,
	})
	if err != nil {
		return nil, err
	}
	return agt, nil
}

func auditFunc(logs LogManager) func(string, ...any) {
	return func(event string, attrs ...any) {
		logs.Audit().Log(context.Background(), slog.LevelInfo, "audit event", append([]any{"event", event}, attrs...)...)
	}
}

type hookRuntimeLifecycle struct {
	runtime *hookruntime.Manager
}

func (l hookRuntimeLifecycle) Close(ctx context.Context) error {
	return l.runtime.Close(ctx)
}

func registerTurnAlias(aliases map[string]string, alias, name string) {
	key := strings.ToLower(strings.TrimSpace(alias))
	if key == "" {
		return
	}
	aliases[key] = name
}
