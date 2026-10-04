package builtin

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"elbot/internal/angelmemory"
	"elbot/internal/character"
	"elbot/internal/config"
	elcron "elbot/internal/cron"
	"elbot/internal/groupanalysis"
	"elbot/internal/imagegen"
	"elbot/internal/media"
	"elbot/internal/memory/resident"
	"elbot/internal/ops/concurrency"
	"elbot/internal/processenv"
	"elbot/internal/selflearning"
	"elbot/internal/storage"
	"elbot/internal/tool"
	"elbot/internal/tool/runtimeinfo"
	"elbot/internal/tool/skill"
)

type Runtime struct {
	Registry            *tool.Registry
	ResidentMemoryStore *resident.Store
	AngelMemoryStore    *angelmemory.Store
	AngelMemory         *angelmemory.Service
	SelfLearningStore   *selflearning.Store
	SelfLearning        *selflearning.Service
	GroupAnalysis       *groupanalysis.Service
	CharacterStore      *character.Store
	ImageProfiles       map[string]ImageProfile
	DefaultImageProfile string
	ImageLimiter        *concurrency.Limiter
	SkillManager        *skill.Manager
	FileManager         *FileManager
}

type RuntimeOptions struct {
	ConfigDir               string
	RuntimeInfo             runtimeinfo.Info
	CronService             *elcron.Service
	ChatHistory             storage.ChatHistoryRepository
	OutboundMessages        storage.OutboundMessageRepository
	GroupAnalysis           config.GroupAnalysisConfig
	GroupAnalysisSummarizer groupanalysis.Summarizer
	AngelMemory             config.AngelMemoryConfig
	SelfLearning            config.SelfLearningConfig
	Store                   storage.Store
	Media                   *media.Manager
	SandboxRoot             string
	FileDelivery            config.FileDeliveryConfig
	ResidentMemoryMaxUnits  resident.Limits
	ResidentMemoryPolicy    resident.NormalWritePolicy
	CharacterEnabled        bool
	CharacterRoot           string
	ImageGeneration         config.ImageGenerationConfig
	PromptRewriter          ImagePromptRewriter
	ImagePromptService      ImagePromptService
	ProcessEnv              processenv.Environment
	ChildProcessEnv         processenv.Environment
}

func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	if opts.ConfigDir == "" {
		return nil, fmt.Errorf("builtin runtime config dir is required")
	}
	info := opts.RuntimeInfo
	if info.ConfigDir == "" {
		info.ConfigDir = opts.ConfigDir
	}
	if info.SandboxRoot == "" {
		info.SandboxRoot = opts.SandboxRoot
	}
	if info.FileDelivery == (config.FileDeliveryConfig{}) {
		info.FileDelivery = opts.FileDelivery
	}
	info = info.Normalize()
	childProcessEnv := opts.ChildProcessEnv
	if !childProcessEnv.Configured() {
		childProcessEnv = opts.ProcessEnv
	}
	registry := tool.NewRegistry()
	residentStore := resident.NewStoreWithOptions(filepath.Join(opts.ConfigDir, "memories.toml"), opts.ResidentMemoryMaxUnits, opts.ResidentMemoryPolicy)
	var angelMemoryStore *angelmemory.Store
	if opts.AngelMemory.IsEnabled() {
		var err error
		angelMemoryStore, err = angelmemory.Open(context.Background(), filepath.Join(opts.ConfigDir, "angel_memory.db"))
		if err != nil {
			return nil, err
		}
	}
	var angelMemoryService *angelmemory.Service
	if angelMemoryStore != nil {
		angelMemoryService = angelmemory.NewService(angelMemoryStore, angelmemory.Options{
			MaxContentRunes:    opts.AngelMemory.MaxContentRunes,
			MaxContextRunes:    opts.AngelMemory.MaxContextRunes,
			MaxPerScope:        opts.AngelMemory.MaxPerScope,
			MaxWritesPerMinute: opts.AngelMemory.MaxWritesPerMinute,
		})
	}
	var selfLearningStore *selflearning.Store
	if opts.SelfLearning.IsEnabled() {
		var err error
		selfLearningStore, err = selflearning.Open(context.Background(), filepath.Join(opts.ConfigDir, "self_learning.db"))
		if err != nil {
			return nil, err
		}
	}
	var selfLearningService *selflearning.Service
	if selfLearningStore != nil {
		selfLearningService = selflearning.NewService(selfLearningStore, selflearning.Options{
			MinUsers:                      opts.SelfLearning.MinUsers,
			MaxMeaningRunes:               opts.SelfLearning.MaxMeaningRunes,
			MaxContextRunes:               opts.SelfLearning.MaxContextRunes,
			MaxObservationRunes:           opts.SelfLearning.MaxObservationRunes,
			MaxObservationsPerScope:       opts.SelfLearning.MaxObservationsPerScope,
			MaxObservationWritesPerMinute: opts.SelfLearning.MaxObservationWritesPerMinute,
			MaxMineChars:                  opts.SelfLearning.MaxMineChars,
			MineTimeout:                   time.Duration(opts.SelfLearning.MineTimeoutSeconds) * time.Second,
		})
	}
	var groupAnalysisService *groupanalysis.Service
	if opts.ChatHistory != nil && opts.GroupAnalysis.IsEnabled() {
		if historyRange, ok := opts.ChatHistory.(storage.ChatHistoryRangeRepository); ok {
			groupAnalysisService = groupanalysis.NewService(historyRange, opts.OutboundMessages)
			groupAnalysisService.Summarizer = opts.GroupAnalysisSummarizer
		}
	}
	var characterStore *character.Store
	if opts.CharacterEnabled {
		characterStore = character.NewStore(opts.CharacterRoot)
	}
	imageLimiter := concurrency.New(concurrency.Config{
		Max:         opts.ImageGeneration.MaxConcurrent,
		QueueSize:   opts.ImageGeneration.QueueSize,
		WaitTimeout: time.Duration(opts.ImageGeneration.QueueTimeoutSeconds) * time.Second,
	})
	imageProfiles := map[string]ImageProfile{}
	imageDefaultProfile := ""
	if opts.ImageGeneration.Enabled {
		addProfile := func(name string, cfg config.ImageGenerationConfig) {
			client := imagegen.New(imageConfigFrom(cfg), opts.ProcessEnv.Lookup)
			if client == nil {
				return
			}
			imageProfiles[name] = ImageProfile{Name: name, Client: client, Config: client.Config()}
		}
		if base, ok := opts.ImageGeneration.ResolveProfile(""); ok {
			addProfile("", base)
		}
		for name := range opts.ImageGeneration.Profiles {
			resolved, ok := opts.ImageGeneration.ResolveProfile(name)
			if !ok || !resolved.Enabled {
				continue
			}
			addProfile(name, resolved)
		}
		if name := opts.ImageGeneration.DefaultProfileName(); name != "" {
			if _, ok := imageProfiles[name]; ok {
				imageDefaultProfile = name
			}
		}
	}
	skillManager := skill.NewManager(filepath.Join(opts.ConfigDir, "skills"), registry, childProcessEnv)
	fileManager := NewFileManagerWithMedia(info.SandboxRoot, info.FileDelivery, opts.Store)
	if opts.Media != nil {
		fileManager.Media = opts.Media
	}
	runtime := &Runtime{Registry: registry, ResidentMemoryStore: residentStore, AngelMemoryStore: angelMemoryStore, AngelMemory: angelMemoryService, SelfLearningStore: selfLearningStore, SelfLearning: selfLearningService, GroupAnalysis: groupAnalysisService, CharacterStore: characterStore, ImageProfiles: imageProfiles, DefaultImageProfile: imageDefaultProfile, ImageLimiter: imageLimiter, SkillManager: skillManager, FileManager: fileManager}
	if err := RegisterAll(registry, RegisterOptions{
		RuntimeInfo:              info,
		ResidentMemoryStore:      residentStore,
		CharacterStore:           characterStore,
		ImageProfiles:            imageProfiles,
		DefaultImageProfile:      imageDefaultProfile,
		ImageLimiter:             imageLimiter,
		PromptRewriter:           opts.PromptRewriter,
		ImagePromptService:       opts.ImagePromptService,
		SkillManager:             skillManager,
		CronService:              opts.CronService,
		ChatHistory:              opts.ChatHistory,
		OutboundMessages:         opts.OutboundMessages,
		GroupAnalysisService:     groupAnalysisService,
		GroupAnalysisMaxMessages: opts.GroupAnalysis.MaxMessages,
		AngelMemory:              angelMemoryService,
		SelfLearning:             selfLearningService,
		LongMemoryDir:            filepath.Join(opts.ConfigDir, "long_memory"),
		FileManager:              fileManager,
		ProcessEnv:               opts.ProcessEnv,
		ChildProcessEnv:          childProcessEnv,
	}); err != nil {
		return nil, err
	}
	return runtime, nil
}

func imageConfigFrom(imageCfg config.ImageGenerationConfig) imagegen.Config {
	return imagegen.Config{
		Enabled:                 true,
		BaseURL:                 imageCfg.BaseURL,
		Endpoint:                imageCfg.Endpoint,
		APIKey:                  imageCfg.APIKey,
		APIKeyEnv:               imageCfg.APIKeyEnv,
		Model:                   imageCfg.Model,
		Size:                    imageCfg.Size,
		Quality:                 imageCfg.Quality,
		OutputFormat:            imageCfg.OutputFormat,
		ResponseFormat:          imageCfg.ResponseFormat,
		TimeoutSeconds:          imageCfg.TimeoutSeconds,
		PresetPrompt:            imageCfg.PresetPrompt,
		NegativePrompt:          imageCfg.NegativePrompt,
		MaxPromptRunes:          imageCfg.MaxPromptRunes,
		Optimize:                imageCfg.Optimize,
		OptimizeTermMode:        imageCfg.OptimizeTermMode,
		OptimizeMaxAnchors:      imageCfg.OptimizeMaxAnchors,
		OptimizeMaxNegatives:    imageCfg.OptimizeMaxNegatives,
		OptimizeMaxAddedRunes:   imageCfg.OptimizeMaxAddedRunes,
		OptimizeMaxTags:         imageCfg.OptimizeMaxTags,
		OptimizeRewrite:         imageCfg.OptimizeRewrite,
		OptimizeRewriteModel:    imageCfg.OptimizeRewriteModel,
		OptimizeRewriteMinRunes: imageCfg.OptimizeRewriteMinRunes,
		AutoCharacter:           imageCfg.IsAutoCharacter(),
		AutoContext:             imageCfg.IsAutoContext(),
		ContextDefaultLimit:     imageCfg.ContextDefaultLimit,
		SuperadminOnly:          imageCfg.IsSuperadminOnly(),
		SaveToCharacter:         imageCfg.IsSaveToCharacter(),
		SendByDefault:           imageCfg.SendByDefault,
		SupportsReference:       imageCfg.SupportsReference,
		ReferenceField:          imageCfg.ReferenceField,
		ExtraPayload:            imageCfg.ExtraPayload,
		ExtraHeaders:            imageCfg.ExtraHeaders,
		Proxy:                   imageCfg.Proxy,
	}
}
