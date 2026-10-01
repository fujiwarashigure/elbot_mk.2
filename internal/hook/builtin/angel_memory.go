package builtin

import (
	"context"
	"strings"

	"elbot/internal/angelmemory"
	"elbot/internal/hook"
	"elbot/internal/llm"
)

func registerAngelMemory(registrar hook.Registrar, service *angelmemory.Service) error {
	if registrar == nil || service == nil || !service.Ready() {
		return nil
	}
	return registrar.Register(hook.Registration{
		Point:       hook.PointLLMTurnPrepared,
		Priority:    500,
		PluginID:    "builtin",
		Name:        "builtin.angel_memory",
		Description: "按当前平台/会话召回长期记忆，追加为临时 system 上下文。",
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
			contextText, err := service.Context(ctx, platformName, scopeID, query, 5)
			if err != nil {
				return event, err
			}
			if strings.TrimSpace(contextText) == "" {
				return event, nil
			}
			event.LLM.SystemAppend = append(event.LLM.SystemAppend, contextText)
			return event, nil
		}),
	})
}
