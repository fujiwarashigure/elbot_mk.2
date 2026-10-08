package agent

import (
	"context"
	"sync"
	"time"

	"elbot/internal/config"
	"elbot/internal/contextmgr"
	"elbot/internal/llm"
	"elbot/internal/request"
	"elbot/internal/session"
	"elbot/internal/storage"
	"elbot/internal/turn"
)

type contextRuntimeState struct {
	store          storage.Store
	sessions       *session.Service
	requests       *request.Manager
	turns          *turn.Manager
	loader         contextmgr.Loader
	windowResolver *contextmgr.WindowResolver
	compactor      contextmgr.Compactor
	// clientFor 保留一份客户端查找入口：协议能力查询（分派压缩后端、记录会话协议来源）
	// 需要按 provider 拿到实际适配器。
	clientFor contextmgr.ClientProvider

	mu              sync.Mutex
	compressTimeout time.Duration
	config          config.ContextConfig
	modelMetadata   config.ModelMetadataConfig
	compactModel    config.ModelSelection
	lastUsage       map[string]*llm.Usage
}

func newContextRuntimeState(store storage.Store, sessions *session.Service, requests *request.Manager, turns *turn.Manager) contextRuntimeState {
	return contextRuntimeState{
		store:     store,
		sessions:  sessions,
		requests:  requests,
		turns:     turns,
		loader:    contextmgr.Loader{Store: store},
		config:    config.Default().Context,
		lastUsage: map[string]*llm.Usage{},
	}
}

func (r *contextRuntimeState) configure(ctxCfg config.ContextConfig, metadata config.ModelMetadataConfig, providers map[string]config.ProviderConfig, compactModel config.ModelSelection, clientFor contextmgr.ClientProvider) {
	ctxCfg = ctxCfg.Normalized()
	r.mu.Lock()
	r.config = ctxCfg
	r.modelMetadata = metadata
	r.compactModel = compactModel
	r.loader = contextmgr.Loader{Store: r.store}
	r.windowResolver = contextmgr.NewWindowResolver(metadata, providers, clientFor)
	r.compactor = contextmgr.Compressor{ClientFor: clientFor}
	r.clientFor = clientFor
	r.mu.Unlock()
}

// compactorFor 按协议能力选择这次压缩使用的后端，并说明是否发生了回退。
// 目前只有客户端后端：即使适配器声称支持服务端原生压缩，也必须回退到客户端压缩并让调用方
// 记一条审计——"协议支持但实现没跟上"不能变成压缩静默失效，也不能静默假装走了服务端路径。
func (r *contextRuntimeState) compactorFor(providerName, model string) (contextmgr.Compactor, contextmgr.CompactionChoice) {
	r.mu.Lock()
	compactor := r.compactor
	clientFor := r.clientFor
	r.mu.Unlock()
	if compactor == nil {
		return nil, contextmgr.CompactionChoice{}
	}
	choice := contextmgr.CompactionChoice{Backend: compactor.Name()}
	if clientFor == nil {
		return compactor, choice
	}
	if llm.ProtocolCapabilitiesOf(clientFor(providerName), model).NativeCompaction {
		choice.FallbackReason = "protocol_advertises_native_compaction_without_backend"
	}
	return compactor, choice
}

func (r *contextRuntimeState) load(ctx context.Context, sessionID string) (*contextmgr.LoadedContext, error) {
	r.mu.Lock()
	loader := r.loader
	r.mu.Unlock()
	return loader.Load(ctx, sessionID)
}

func (r *contextRuntimeState) loadRawMessages(ctx context.Context, sessionID string) ([]storage.Message, error) {
	r.mu.Lock()
	loader := r.loader
	r.mu.Unlock()
	return loader.LoadRawMessages(ctx, sessionID)
}

func (r *contextRuntimeState) compactSelection(fallback config.ModelSelection) config.ModelSelection {
	r.mu.Lock()
	selected := r.compactModel
	r.mu.Unlock()
	if selected.Provider != "" && selected.Model != "" {
		return selected
	}
	return fallback
}

func (r *contextRuntimeState) configuredCompactModel() config.ModelSelection {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.compactModel
}

func (r *contextRuntimeState) setCompactModel(selection config.ModelSelection) {
	r.mu.Lock()
	r.compactModel = selection
	r.mu.Unlock()
}

func (a *Agent) SetContextOptions(ctxCfg config.ContextConfig, metadata config.ModelMetadataConfig, providers map[string]config.ProviderConfig, compactModel config.ModelSelection) {
	a.contextRuntime.configure(ctxCfg, metadata, providers, compactModel, a.clientForProvider)
}

func (a *Agent) compactSelectionForSession(session *storage.Session) config.ModelSelection {
	mode := storage.SessionModeWork
	if session != nil && session.Mode != "" {
		mode = session.Mode
	}
	return a.contextRuntime.compactSelection(a.modelForMode(mode))
}
