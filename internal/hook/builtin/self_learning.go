package builtin

import (
	"context"
	"strings"

	"elbot/internal/hook"
	"elbot/internal/llm"
	"elbot/internal/selflearning"
)

func registerSelfLearning(registrar hook.Registrar, service *selflearning.Service) error {
	if registrar == nil || service == nil || !service.Ready() {
		return nil
	}
	if err := registrar.Register(hook.Registration{
		Point:       hook.PointPlatformMessageReceived,
		Priority:    950,
		PluginID:    "builtin",
		Name:        "builtin.self_learning_observe",
		Description: "观察群消息，写入 clean-room 学习语料。",
		Match:       hook.Always(),
		Wakeup:      hook.WakeupAny,
		Handler: hook.HandlerFunc(func(ctx context.Context, event hook.Event) (hook.Event, error) {
			text := strings.TrimSpace(event.Message.PlatformText)
			if text == "" {
				text = strings.TrimSpace(llm.SegmentsTextOnly(event.Message.Segments))
			}
			if text == "" || strings.TrimSpace(event.Platform.Name) == "" || strings.TrimSpace(event.Platform.ScopeID) == "" {
				return event, nil
			}
			if err := service.Observe(ctx, event.Platform.Name, event.Platform.ScopeID, event.Actor.UserID, text); err != nil {
				return event, err
			}
			return event, nil
		}),
	}); err != nil {
		return err
	}
	return registrar.Register(hook.Registration{
		Point:       hook.PointLLMTurnPrepared,
		Priority:    510,
		PluginID:    "builtin",
		Name:        "builtin.self_learning_context",
		Description: "把审核通过的表达/黑话上下文追加为临时 system 上下文。",
		Match:       hook.Always(),
		Handler: hook.HandlerFunc(func(ctx context.Context, event hook.Event) (hook.Event, error) {
			platformName := strings.TrimSpace(event.Platform.Name)
			scopeID := strings.TrimSpace(event.Platform.ScopeID)
			if platformName == "" || scopeID == "" {
				return event, nil
			}
			query := strings.TrimSpace(event.Message.IntentText)
			if query == "" {
				query = strings.TrimSpace(llm.SegmentsTextOnly(event.Message.Segments))
			}
			contextText, err := service.Context(ctx, platformName, scopeID, query, 8)
			if err != nil {
				return event, err
			}
			if strings.TrimSpace(contextText) != "" {
				event.LLM.SystemAppend = append(event.LLM.SystemAppend, contextText)
			}
			return event, nil
		}),
	})
}
