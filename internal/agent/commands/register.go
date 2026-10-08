package commands

import (
	"context"
	"time"

	"elbot/internal/angelmemory"
	"elbot/internal/character"
	"elbot/internal/command"
	"elbot/internal/config"
	"elbot/internal/hook"
	hookruntime "elbot/internal/hook/runtime"
	"elbot/internal/logging"
	"elbot/internal/memory/resident"
	"elbot/internal/request"
	runtimestatus "elbot/internal/runtime"
	"elbot/internal/selflearning"
	"elbot/internal/session"
	"elbot/internal/storage"
	"elbot/internal/tool"
	"elbot/internal/turn"
	"elbot/internal/utils/fileops"
)

type Registrar interface {
	Register(command.Handler) error
}

type Module interface {
	RegisterCommands(Registrar, Deps) error
}

type HandlerFactory func(Deps) command.Handler

type CommandGroup struct {
	Factories []HandlerFactory
}

func NewCommandGroup(factories ...HandlerFactory) CommandGroup {
	return CommandGroup{Factories: factories}
}

func (g CommandGroup) RegisterCommands(registrar Registrar, deps Deps) error {
	return RegisterFactories(registrar, deps, g.Factories...)
}

type ModelOption struct {
	Index       int
	Provider    string
	Model       string
	Current     bool
	ChatCurrent bool
	WorkCurrent bool
	ModeMarks   []string
	Compact     bool
	Naming      bool
}

type ModelProviderError struct {
	Provider string
	Err      error
}

type ModelListOptions struct {
	Fresh bool
}

type ModelListResult struct {
	Options []ModelOption
	Errors  []ModelProviderError
}

// ModelProfile 是一个命名模型选择（services.toml 的 model_profiles / model_aliases）。
// Available 表示该 profile 指向的 provider 在当前进程里真的有客户端；不可用的 profile
// 在 `@model:<name>` 与群策略里都会被拒绝，所以列表要把两种情况分开显示。
type ModelProfile struct {
	Name      string
	Provider  string
	Model     string
	Available bool
}

type ModelService interface {
	CurrentModel() string
	CurrentProvider() string
	CurrentModeModel() ModelOption
	CurrentModelForMode(mode string) ModelOption
	CurrentCompactModel() ModelOption
	CurrentNamingModel() ModelOption
	SelectModel(ctx context.Context, arg string) (ModelOption, error)
	SelectModelForMode(mode, arg string) (ModelOption, error)
	SelectCompactModel(arg string) (ModelOption, error)
	SelectNamingModel(arg string) (ModelOption, error)
	Models(query string) []ModelOption
	ModelList(query string, opts ModelListOptions) ModelListResult
	// ModelProfiles 列出命名模型选择，让操作者能看到有哪些名字可用（而不必去翻
	// services.toml 的注释）。
	ModelProfiles() []ModelProfile
}

type ContextStatusService interface {
	ContextStatus(ctx context.Context, session *storage.Session) string
}

type CompactService interface {
	CompactCurrent(ctx context.Context, triggerReason string) (string, error)
}

type ContextPolicyService interface {
	ContextPolicyStatus(ctx context.Context) string
	SetContextPolicy(ctx context.Context, target, value string) (string, error)
	ResetContextPolicy(ctx context.Context, target string) (string, error)
}

type GroupKnowledgeService interface {
	GroupKnowledgeList(ctx context.Context) ([]config.GroupKnowledgeEntry, error)
	GroupKnowledgeAdd(ctx context.Context, question, answer, match string, aliases, keywords []string) (config.GroupKnowledgeEntry, error)
	GroupKnowledgeRemove(ctx context.Context, id string) (bool, error)
	GroupKnowledgeClear(ctx context.Context) (int, error)
	GroupKnowledgeTest(ctx context.Context, text string) (config.GroupKnowledgeEntry, bool, error)
}

// MemberPanelService builds the ordinary member's self-service task/quota view.
type MemberPanelService interface {
	MemberPanel(ctx context.Context, view string) (string, error)
}

// GroupServicesService provides deterministic reminders, polls and sign-ups.
type GroupServicesService interface {
	ReminderCreate(ctx context.Context, whenText, text string) (string, error)
	ReminderList(ctx context.Context) (string, error)
	ReminderRemove(ctx context.Context, id string) (string, error)
	PollCreate(ctx context.Context, question string, options []string) (string, error)
	PollList(ctx context.Context) (string, error)
	PollShow(ctx context.Context, id string) (string, error)
	PollVote(ctx context.Context, id, option string) (string, error)
	PollClose(ctx context.Context, id string) (string, error)
	SignupCreate(ctx context.Context, title string, capacity int) (string, error)
	SignupList(ctx context.Context) (string, error)
	SignupShow(ctx context.Context, id string) (string, error)
	SignupJoin(ctx context.Context, id string) (string, error)
	SignupLeave(ctx context.Context, id string) (string, error)
	SignupClose(ctx context.Context, id string) (string, error)
}

type GroupPolicyService interface {
	GroupPolicyStatus(ctx context.Context) string
	SetGroupPolicy(ctx context.Context, field, value string) (string, error)
	ResetGroupPolicy(ctx context.Context, field string) (string, error)
	// AuthorizeLearningAction gates one granular learning action for the
	// current group. It is evaluated on every command, so revoking a grant or
	// losing the group admin role takes effect immediately.
	AuthorizeLearningAction(ctx context.Context, action string) bool
	// GroupLearningEnabled reports whether the current group allows the
	// self-learning feature. Non-group scopes keep the global behavior.
	GroupLearningEnabled(ctx context.Context) bool
}

type ToolService interface {
	List() []tool.Info
	Unregister(name string) error
	Remove(ctx context.Context, name string) error
	Reload(ctx context.Context) error
}

type HookService interface {
	HookList() []hook.Info
	HookReload() (hook.ReloadReport, error)
	StatefulHooks() []hookruntime.Info
	StartStatefulHook(id string) error
	StopHook(ctx context.Context, id string) (bool, error)
	RestartStatefulHook(ctx context.Context, id string) error
}

type LogService interface {
	QueryLogs(ctx context.Context, query logging.LogQuery) ([]logging.LogEntry, error)
}

// RuntimeStateReloadReport describes one state.toml hot reload.
type RuntimeStateReloadReport struct {
	Path string
	// Applied is true when the file was read and merged into runtime state.
	Applied bool
	// Changed lists the runtime sections whose effective value changed.
	Changed []string
	// ModTime is the state.toml mtime that was applied.
	ModTime time.Time
}

// RuntimeStateStatus describes the loaded state file and whether the on-disk
// file is newer than what the process has applied.
type RuntimeStateStatus struct {
	Path          string
	LoadedModTime time.Time
	FileModTime   time.Time
	Pending       bool
}

// RuntimeStateService exposes state.toml to operators so an external edit can be
// applied without restarting the process.
type RuntimeStateService interface {
	ReloadRuntimeState(ctx context.Context) (RuntimeStateReloadReport, error)
	RuntimeStateStatus() RuntimeStateStatus
}

type Deps struct {
	Router         *command.Router
	Sessions       *session.Service
	Requests       *request.Manager
	Turns          *turn.Manager
	Store          storage.Store
	Scope          func(context.Context) session.Scope
	Models         ModelService
	Compact        CompactService
	ContextStatus  ContextStatusService
	ContextPolicy  ContextPolicyService
	GroupPolicy    GroupPolicyService
	GroupKnowledge GroupKnowledgeService
	MemberPanel    MemberPanelService
	GroupServices  GroupServicesService
	Tools          ToolService
	Hooks          HookService
	SessionState   *SessionCommandState
	Characters     *character.Store
	AngelMemory    *angelmemory.Service
	ResidentMemory *resident.Store
	SelfLearning   *selflearning.Service
	Audit          func(event string, attrs ...any)
	Logs           LogService
	RuntimeState   RuntimeStateService
	RuntimeStatus  func(sessionID string) runtimestatus.Snapshot
	// CancelSessionInbox drops ordinary chat messages that are still waiting
	// in the agent-side merge/serialization queue for one Session.
	CancelSessionInbox func(sessionID string) int
	// FileBackups keeps the pre-edit content of files ElBot edited, so
	// /rollback can undo the latest edit of each file in the current Session.
	FileBackups *fileops.RollbackStore
	// Doctor runs the read-only configuration check behind /doctor.
	Doctor DoctorService
}

func RegisterFactories(registrar Registrar, deps Deps, factories ...HandlerFactory) error {
	for _, factory := range factories {
		if err := registrar.Register(factory(deps)); err != nil {
			return err
		}
	}
	return nil
}

func RegisterModules(registrar Registrar, deps Deps, modules ...Module) error {
	for _, module := range modules {
		if err := module.RegisterCommands(registrar, deps); err != nil {
			return err
		}
	}
	return nil
}

func DefaultModules() []Module {
	return []Module{
		HelpModule{},
		ModelModule{},
		SessionModule{},
		CompactModule{},
		ContextPolicyModule{},
		GroupPolicyModule{},
		KnowledgeModule{},
		MemberPanelModule{},
		GroupServicesModule{},
		RequestModule{},
		FileModule{},
		DoctorModule{},
		LogModule{},
		ToolModule{},
		CharacterModule{},
		HookModule{},
		StateModule{},
		MemoryLearningModule{},
	}
}

func RegisterDefaultModules(registrar Registrar, deps Deps, extra ...Module) error {
	modules := append(DefaultModules(), extra...)
	return RegisterModules(registrar, deps, modules...)
}
