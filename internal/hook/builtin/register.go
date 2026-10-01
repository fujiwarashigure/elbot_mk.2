package builtin

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"elbot/internal/angelmemory"
	"elbot/internal/delivery"
	"elbot/internal/hook"
	"elbot/internal/hook/rules"
	hookruntime "elbot/internal/hook/runtime"
	"elbot/internal/llm"
	"elbot/internal/selflearning"
	"elbot/internal/storage"
	"elbot/internal/tool"
)

// Options contains shared dependencies for hook plugins shipped with ElBot.
type Options struct {
	ConfigDir        string
	Tools            *tool.Registry
	Logger           *slog.Logger
	Audit            func(event string, attrs ...any)
	Notify           func(context.Context, string)
	Send             func(context.Context, delivery.Target, []delivery.Output) (delivery.Receipt, error)
	PlatformCallers  rules.PlatformCallerResolver
	Runtime          *hookruntime.Manager
	ProcessEnv       hook.ProcessEnvironment
	OutboundMessages storage.OutboundMessageRepository
	AngelMemory      *angelmemory.Service
	SelfLearning     *selflearning.Service
}

func RegisterAll(registrar hook.Registrar, opts Options) ([]hookruntime.Config, error) {
	if registrar == nil {
		return nil, nil
	}
	rulesModule, err := rules.NewModule(rules.Options{
		ConfigDir:       opts.ConfigDir,
		Tools:           opts.Tools,
		Logger:          opts.Logger,
		Audit:           opts.Audit,
		Notify:          opts.Notify,
		Send:            opts.Send,
		PlatformCallers: opts.PlatformCallers,
		Runtime:         opts.Runtime,
		ProcessEnv:      opts.ProcessEnv,
	})
	if err == nil {
		if err := registerModule(registrar, opts, "rules", rulesModule); err != nil {
			return nil, err
		}
	} else {
		reportPluginError(opts, "rules", err)
		return nil, err
	}
	if opts.OutboundMessages != nil {
		if err := registrar.Register(hook.Registration{
			Point:       hook.PointPlatformMessageSent,
			Priority:    900,
			PluginID:    "builtin",
			Name:        "builtin.outbound_history",
			Description: "记录实际发送的 assistant 出站消息，供学习和群分析复用。",
			Match:       hook.Always(),
			Handler:     hook.HandlerFunc(recordOutboundMessage(opts.OutboundMessages)),
		}); err != nil {
			return nil, err
		}
	}
	if err := registerAngelMemory(registrar, opts.AngelMemory); err != nil {
		return nil, err
	}
	if err := registerSelfLearning(registrar, opts.SelfLearning); err != nil {
		return nil, err
	}
	return append([]hookruntime.Config(nil), rulesModule.Runtimes...), nil
}

func recordOutboundMessage(repo storage.OutboundMessageRepository) func(context.Context, hook.Event) (hook.Event, error) {
	return func(ctx context.Context, event hook.Event) (hook.Event, error) {
		if repo == nil || event.Point != hook.PointPlatformMessageSent {
			return event, nil
		}
		text := strings.TrimSpace(llm.SegmentsTextOnly(event.Message.Segments))
		if text == "" || strings.HasPrefix(text, "[tool] ") {
			return event, nil
		}
		platform := strings.TrimSpace(event.Platform.Name)
		scopeID := strings.TrimSpace(event.Platform.ScopeID)
		if platform == "" || scopeID == "" {
			return event, nil
		}
		if err := repo.Append(ctx, &storage.OutboundMessage{
			Platform:          platform,
			PlatformScopeID:   scopeID,
			PlatformMessageID: strings.TrimSpace(event.Platform.PlatformMessageID),
			Text:              text,
			CreatedAt:         storage.Now(),
		}); err != nil {
			return event, fmt.Errorf("record outbound message: %w", err)
		}
		return event, nil
	}
}

func registerModule(registrar hook.Registrar, opts Options, name string, module hook.Module) error {
	if err := module.RegisterHooks(registrar); err != nil {
		reportPluginError(opts, name, err)
		return err
	}
	return nil
}

func reportPluginError(opts Options, name string, err error) {
	if err == nil {
		return
	}
	if opts.Logger != nil {
		opts.Logger.Error("hook plugin disabled", "plugin", name, "error", err)
	}
	if opts.Notify != nil {
		opts.Notify(context.Background(), fmt.Sprintf("Hook 插件 %s 已禁用：%v", name, err))
	}
}
