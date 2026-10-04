package agent

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/config"
)

// authorizeExecutionModelSelection is the last-mile model authorization check.
// Group policy is resolved before queueing/streaming, but hooks, turn
// overrides, cron overrides and transparent fallbacks can still change the
// selection later. This method is called immediately before the real provider
// request so the catalog governs the model that actually runs, not just the
// /model menu.
func (a *Agent) authorizeExecutionModelSelection(ctx context.Context, selection config.ModelSelection) error {
	if a == nil {
		return nil
	}
	if a.groupRuntimeBlocked(ctx) {
		return fmt.Errorf("当前群已暂停模型调用：机器人被禁言、已离群或状态不可用")
	}
	if !a.isGroupScope(ctx) {
		return nil
	}
	policy := a.groupPolicyForScope(a.scope(ctx))
	catalog := policy.AllowedModels
	defaultModel := strings.TrimSpace(policy.DefaultModel)
	if len(catalog) == 0 && defaultModel == "" {
		// No explicit group model policy: inherit the global model exactly as
		// before. A group that never opted in is not broken by this check.
		return nil
	}
	provider := strings.TrimSpace(selection.Provider)
	model := strings.TrimSpace(selection.Model)
	if provider == "" || model == "" {
		return fmt.Errorf("模型未配置或不可用")
	}
	if defaultModel != "" {
		if resolved, ok := a.resolveGroupModelSelection(defaultModel); ok && resolved.Provider == provider && resolved.Model == model {
			return nil
		}
	}
	if len(catalog) > 0 && a.groupModelSelectionAllowed(policy, config.ModelSelection{Provider: provider, Model: model}, provider+"/"+model) {
		return nil
	}
	label := provider + "/" + model
	if len(catalog) == 0 {
		return fmt.Errorf("模型 %s 不在本群默认模型内；请让超级管理员配置 /grouppolicy default-model 或 allowed-models", label)
	}
	return fmt.Errorf("模型 %s 不在本群模型目录内；请让超级管理员先配置 /grouppolicy allowed-models", label)
}
