package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/contextmgr"
	"elbot/internal/llm"
	"elbot/internal/logging"
	"elbot/internal/storage"
)

// budgetKey builds the stable daily ledger key. Scope+actor are stored even
// when a particular limit is global, so one ledger can enforce group,
// per-user and global budgets without rewriting old state.
func budgetKey(now time.Time, kind, scopeKey, actorID, callID string) string {
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeKey == "" {
		scopeKey = "unknown"
	}
	actorID = strings.TrimSpace(actorID)
	callID = strings.TrimSpace(callID)
	return strings.Join([]string{now.Format("2006-01-02"), strings.TrimSpace(kind), scopeKey, actorID, callID}, "|")
}

func budgetPrefix(now time.Time, kind, scopeKey string) string {
	return strings.Join([]string{now.Format("2006-01-02"), strings.TrimSpace(kind), strings.TrimSpace(scopeKey), ""}, "|")
}

func budgetKeyParts(key string) (date, kind, scopeKey, actorID string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(key), "|", 5)
	if len(parts) != 5 {
		return "", "", "", "", false
	}
	return parts[0], parts[1], parts[2], parts[3], true
}

func cloneBudgetReservations(input map[string]int64) map[string]int64 {
	if len(input) == 0 {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		if key == "" || value <= 0 {
			continue
		}
		out[key] = value
	}
	return out
}

func cloneBudgetValues(input map[string]int64) map[string]int64 {
	return cloneBudgetReservations(input)
}

func cloneBudgetDigests(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func (a *Agent) setBudgetSnapshot(snapshot map[string]int64) {
	a.budgetMu.Lock()
	a.budgetReservations = cloneBudgetReservations(snapshot)
	a.budgetMu.Unlock()
}

func (a *Agent) setBudgetDigestSnapshot(snapshot map[string]string) {
	a.budgetMu.Lock()
	a.budgetDigests = cloneBudgetDigests(snapshot)
	a.budgetMu.Unlock()
}

func (a *Agent) setBudgetUsageSnapshot(tokens, costs, retries map[string]int64) {
	a.budgetMu.Lock()
	a.budgetTokens = cloneBudgetValues(tokens)
	a.budgetCosts = cloneBudgetValues(costs)
	a.budgetRetries = cloneBudgetValues(retries)
	a.budgetMu.Unlock()
}

func (a *Agent) setBudgetUncertainSnapshot(snapshot map[string]int64) {
	a.budgetMu.Lock()
	a.budgetUncertain = cloneBudgetValues(snapshot)
	a.budgetMu.Unlock()
}

func (a *Agent) budgetStateSnapshot() (map[string]int64, map[string]string) {
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	return cloneBudgetReservations(a.budgetReservations), cloneBudgetDigests(a.budgetDigests)
}

func (a *Agent) budgetUsageSnapshot() (map[string]int64, map[string]int64, map[string]int64) {
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	return cloneBudgetValues(a.budgetTokens), cloneBudgetValues(a.budgetCosts), cloneBudgetValues(a.budgetRetries)
}

func (a *Agent) budgetUncertainSnapshot() map[string]int64 {
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	return cloneBudgetValues(a.budgetUncertain)
}

func (a *Agent) budgetSnapshot() map[string]int64 {
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	return cloneBudgetReservations(a.budgetReservations)
}

func (a *Agent) budgetDigestSnapshot() map[string]string {
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	return cloneBudgetDigests(a.budgetDigests)
}

func (a *Agent) markBudgetWriteFailed(failed bool) {
	a.budgetMu.Lock()
	a.budgetWriteFailed = failed
	a.budgetMu.Unlock()
}

func (a *Agent) budgetWritable() bool {
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	return !a.budgetWriteFailed
}

// budgetReservationRequest binds a call ID to a digest of its arguments. The
// digest turns a replayed call ID into an idempotent replay only when the
// request is actually the same; reusing an ID for a different tool call is
// rejected instead of silently consuming another unit.
type budgetReservationRequest struct {
	CallID string
	Digest string
}

// reserveBudgetBatch atomically reserves one daily unit per call ID. If the
// batch would exceed any active group/user/global limit, nothing is reserved.
// Replayed call IDs are idempotent and never double-count.
func (a *Agent) reserveBudgetBatch(ctx context.Context, kind string, callIDs []string) (bool, string) {
	requests := make([]budgetReservationRequest, 0, len(callIDs))
	for _, callID := range callIDs {
		requests = append(requests, budgetReservationRequest{CallID: callID})
	}
	return a.reserveBudgetBatchRequests(ctx, kind, requests)
}

func (a *Agent) reserveBudget(ctx context.Context, kind, callID string) (bool, string) {
	return a.reserveBudgetBatch(ctx, kind, []string{callID})
}

func (a *Agent) reserveBudgetRequest(ctx context.Context, kind string, request budgetReservationRequest) (bool, string) {
	return a.reserveBudgetBatchRequests(ctx, kind, []budgetReservationRequest{request})
}

type budgetLimitSpec struct {
	label      string
	limit      int64
	scopeMatch bool
	actorMatch bool
}

func (a *Agent) callLimitSpecs(ctx context.Context, kind string) []budgetLimitSpec {
	if a == nil {
		return nil
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	limits := a.budgetLimits.Normalized()
	var specs []budgetLimitSpec
	if a.isGroupScope(ctx) {
		policy := a.groupPolicyForScope(a.scope(ctx))
		switch kind {
		case "image":
			specs = append(specs,
				budgetLimitSpec{label: "本群生图日", limit: int64(policy.ImageQuota), scopeMatch: true},
				budgetLimitSpec{label: "本群单用户生图日", limit: int64(policy.UserImageQuota), scopeMatch: true, actorMatch: true},
			)
		case "vision":
			specs = append(specs,
				budgetLimitSpec{label: "本群视觉日", limit: int64(policy.VisionQuota), scopeMatch: true},
				budgetLimitSpec{label: "本群单用户视觉日", limit: int64(policy.UserVisionQuota), scopeMatch: true, actorMatch: true},
			)
		case "asr":
			specs = append(specs,
				budgetLimitSpec{label: "本群语音转写日", limit: int64(policy.ASRQuota), scopeMatch: true},
				budgetLimitSpec{label: "本群单用户语音转写日", limit: int64(policy.UserASRQuota), scopeMatch: true, actorMatch: true},
			)
		}
	}
	switch kind {
	case "image":
		specs = append(specs,
			budgetLimitSpec{label: "全局生图日", limit: int64(limits.GlobalImageDaily)},
			budgetLimitSpec{label: "全局单用户生图日", limit: int64(limits.UserImageDaily), actorMatch: true},
		)
	case "vision":
		specs = append(specs,
			budgetLimitSpec{label: "全局视觉日", limit: int64(limits.GlobalVisionDaily)},
			budgetLimitSpec{label: "全局单用户视觉日", limit: int64(limits.UserVisionDaily), actorMatch: true},
		)
	case "asr":
		specs = append(specs,
			budgetLimitSpec{label: "全局语音转写日", limit: int64(limits.GlobalASRDaily)},
			budgetLimitSpec{label: "全局单用户语音转写日", limit: int64(limits.UserASRDaily), actorMatch: true},
		)
	}
	active := specs[:0]
	for _, spec := range specs {
		if spec.limit > 0 {
			active = append(active, spec)
		}
	}
	return active
}

func (a *Agent) reserveBudgetBatchRequests(ctx context.Context, kind string, requests []budgetReservationRequest) (bool, string) {
	if a == nil {
		return true, ""
	}
	specs := a.callLimitSpecs(ctx, kind)
	if len(specs) == 0 {
		return true, ""
	}
	scopeKey := contextOverflowKey(a.scope(ctx))
	actor := a.actor(ctx)
	actorID := firstNonEmpty(actor.ID, actor.PlatformUserID)
	now := time.Now()

	type pendingReservation struct {
		key    string
		digest string
	}
	pending := make([]pendingReservation, 0, len(requests))
	seen := map[string]bool{}
	for _, request := range requests {
		callID := strings.TrimSpace(request.CallID)
		if callID == "" {
			continue
		}
		key := budgetKey(now, kind, scopeKey, actorID, callID)
		if seen[key] {
			continue
		}
		seen[key] = true
		pending = append(pending, pendingReservation{key: key, digest: strings.TrimSpace(request.Digest)})
	}
	if len(pending) == 0 {
		return true, ""
	}

	a.budgetMu.Lock()
	a.budgetReservations = pruneBudgetReservations(a.budgetReservations, now)
	a.budgetDigests = pruneBudgetDigests(a.budgetDigests, now)
	unique := 0
	for _, item := range pending {
		if _, ok := a.budgetReservations[item.key]; ok {
			if item.digest != "" {
				if existing := strings.TrimSpace(a.budgetDigests[item.key]); existing != "" && existing != item.digest {
					a.budgetMu.Unlock()
					return false, "调用 ID 被复用到了不同参数；请检查工具调用方是否错误重放"
				}
			}
			continue
		}
		if item.digest != "" {
			if existing := strings.TrimSpace(a.budgetDigests[item.key]); existing != "" && existing != item.digest {
				a.budgetMu.Unlock()
				return false, "调用 ID 被复用到了不同参数；请检查工具调用方是否错误重放"
			}
		}
		unique++
	}
	for _, spec := range specs {
		used := countBudgetReservationMatchesLocked(a.budgetReservations, kind, scopeKey, actorID, spec.scopeMatch, spec.actorMatch)
		if used+int64(unique) > spec.limit {
			a.budgetMu.Unlock()
			return false, fmt.Sprintf("%s额度 %d 已用完（已用 %d）", spec.label, spec.limit, used)
		}
	}
	added := make([]pendingReservation, 0, unique)
	for _, item := range pending {
		if _, ok := a.budgetReservations[item.key]; ok {
			continue
		}
		a.budgetReservations[item.key] = now.Unix()
		if item.digest != "" {
			if a.budgetDigests == nil {
				a.budgetDigests = map[string]string{}
			}
			a.budgetDigests[item.key] = item.digest
		}
		added = append(added, item)
	}
	a.budgetMu.Unlock()

	if a.statePath != "" {
		if err := a.saveRuntimeState(); err != nil {
			a.budgetMu.Lock()
			for _, item := range added {
				delete(a.budgetReservations, item.key)
				delete(a.budgetDigests, item.key)
			}
			a.budgetMu.Unlock()
			a.markBudgetWriteFailed(true)
			return false, fmt.Sprintf("额度账本写入失败：%v", err)
		}
		a.markBudgetWriteFailed(false)
	}
	return true, ""
}

type chatBudgetLimitSpec struct {
	label      string
	limit      int64
	metric     string
	scopeMatch bool
	actorMatch bool
}

func (a *Agent) chatBudgetLimitSpecs(ctx context.Context) []chatBudgetLimitSpec {
	if a == nil {
		return nil
	}
	limits := a.budgetLimits.Normalized()
	var specs []chatBudgetLimitSpec
	if a.isGroupScope(ctx) {
		policy := a.groupPolicyForScope(a.scope(ctx))
		specs = append(specs,
			chatBudgetLimitSpec{label: "本群日聊天 token", limit: policy.ChatTokensQuota, metric: "tokens", scopeMatch: true},
			chatBudgetLimitSpec{label: "本群日聊天费用", limit: int64(policy.ChatCostQuota * 1_000_000), metric: "cost", scopeMatch: true},
		)
	}
	specs = append(specs,
		chatBudgetLimitSpec{label: "全局日聊天 token", limit: limits.GlobalChatTokensDaily, metric: "tokens"},
		chatBudgetLimitSpec{label: "全局单用户日聊天 token", limit: limits.UserChatTokensDaily, metric: "tokens", actorMatch: true},
		chatBudgetLimitSpec{label: "全局日聊天费用", limit: int64(limits.GlobalChatCostDaily * 1_000_000), metric: "cost"},
		chatBudgetLimitSpec{label: "全局单用户日聊天费用", limit: int64(limits.UserChatCostDaily * 1_000_000), metric: "cost", actorMatch: true},
	)
	active := specs[:0]
	for _, spec := range specs {
		if spec.limit > 0 {
			active = append(active, spec)
		}
	}
	return active
}

// checkChatBudget is called before each paid LLM call. When any chat
// token/cost limit is active it also proves the ledger can be persisted, so a
// broken state file denies restricted calls instead of silently granting
// unlimited usage. Concurrent turns can still overshoot the limit by one
// in-flight request; the next call is then refused.
func (a *Agent) checkChatBudget(ctx context.Context, selection config.ModelSelection) error {
	if a == nil {
		return nil
	}
	specs := a.chatBudgetLimitSpecs(ctx)
	if len(specs) == 0 {
		return nil
	}
	if !a.budgetWritable() {
		return fmt.Errorf("额度账本当前不可写，已拒绝新的受限调用")
	}
	if a.statePath != "" {
		if err := a.saveRuntimeState(); err != nil {
			a.markBudgetWriteFailed(true)
			return fmt.Errorf("额度账本写入失败：%w", err)
		}
		a.markBudgetWriteFailed(false)
	}
	scopeKey := contextOverflowKey(a.scope(ctx))
	actor := a.actor(ctx)
	actorID := firstNonEmpty(actor.ID, actor.PlatformUserID)
	now := time.Now()
	a.budgetMu.Lock()
	a.budgetTokens = pruneBudgetUsage(a.budgetTokens, now)
	a.budgetCosts = pruneBudgetUsage(a.budgetCosts, now)
	for _, spec := range specs {
		values := a.budgetTokens
		if spec.metric == "cost" {
			values = a.budgetCosts
		}
		used := countBudgetUsageMatchesLocked(values, "chat", scopeKey, actorID, spec.scopeMatch, spec.actorMatch)
		if used >= spec.limit {
			a.budgetMu.Unlock()
			unit := "token"
			if spec.metric == "cost" {
				unit = "微单位"
			}
			return fmt.Errorf("%s额度 %d %s 已用完（已用 %d）", spec.label, spec.limit, unit, used)
		}
	}
	a.budgetMu.Unlock()
	return nil
}

type chatBudgetReservation struct {
	key            string
	callID         string
	model          string
	reservedTokens int64
	reservedCost   int64
}

func llmUsageTotalTokens(usage *llm.Usage) int64 {
	if usage == nil {
		return 0
	}
	tokens := int64(usage.TotalTokens)
	if tokens <= 0 {
		tokens = int64(usage.PromptTokens + usage.CompletionTokens)
	}
	return tokens
}

func llmUsageCostMicros(price config.ModelPriceConfig, usage *llm.Usage) int64 {
	if usage == nil {
		return 0
	}
	cost := price.ComputeCost(int64(usage.PromptTokens), int64(usage.CompletionTokens), int64(usage.CacheHitTokens))
	return int64(cost*1_000_000 + 0.5)
}

// beginChatBudget either performs the legacy soft check or, when
// `[budget_limits].chat_hard_limit` is enabled and at least one chat
// token/cost limit is active, atomically pre-reserves the estimated input plus
// output budget before the provider call. The returned reservation must be
// settled on every return path.
func (a *Agent) beginChatBudget(ctx context.Context, selection config.ModelSelection, messages []llm.LLMMessage, tools []llm.ToolSchema, maxOutput int, callID string) (*chatBudgetReservation, error) {
	if a == nil {
		return nil, nil
	}
	specs := a.chatBudgetLimitSpecs(ctx)
	if len(specs) == 0 {
		return nil, nil
	}
	limits := a.budgetLimits.Normalized()
	if !limits.ChatHardLimit {
		return nil, a.checkChatBudget(ctx, selection)
	}
	if !a.budgetWritable() {
		return nil, fmt.Errorf("额度账本当前不可写，已拒绝新的受限调用")
	}
	estimatedInput := int64(contextmgr.EstimateMessagesTokens(messages) + contextmgr.EstimateToolsTokens(tools))
	if estimatedInput < 1 {
		estimatedInput = 1
	}
	outputReserve := int64(maxOutput)
	if limits.ChatHardLimitReserveOutputTokens > 0 {
		outputReserve = int64(limits.ChatHardLimitReserveOutputTokens)
	}
	if outputReserve <= 0 {
		outputReserve = int64(a.contextRuntime.promptBudget(ctx, selection).ReserveOutputTokens)
	}
	if outputReserve <= 0 {
		outputReserve = 512
	}
	reservedTokens := estimatedInput + outputReserve

	wantCost := false
	for _, spec := range specs {
		if spec.metric == "cost" {
			wantCost = true
			break
		}
	}
	var reservedCost int64
	if wantCost {
		price, ok := a.pricing.PriceFor(strings.TrimSpace(selection.Model), time.Now())
		if !ok {
			return nil, fmt.Errorf("已启用聊天费用硬限制，但模型 %q 没有配置价格；请先配置 [maintenance.daily_report].prices", strings.TrimSpace(selection.Model))
		}
		reservedCost = int64(price.ComputeCost(estimatedInput, outputReserve, 0)*1_000_000 + 0.5)
	}

	callID = strings.TrimSpace(callID)
	if callID == "" {
		callID = storage.NewID()
	}
	now := time.Now()
	scopeKey := contextOverflowKey(a.scope(ctx))
	actor := a.actor(ctx)
	actorID := firstNonEmpty(actor.ID, actor.PlatformUserID)
	key := budgetKey(now, "chat", scopeKey, actorID, callID)

	a.budgetMu.Lock()
	a.budgetTokens = pruneBudgetUsage(a.budgetTokens, now)
	a.budgetCosts = pruneBudgetUsage(a.budgetCosts, now)
	if _, exists := a.budgetTokens[key]; exists {
		a.budgetMu.Unlock()
		return nil, fmt.Errorf("聊天预算调用 ID 已被预占；请检查调用方是否重复使用 call ID")
	}
	if _, exists := a.budgetCosts[key]; exists {
		a.budgetMu.Unlock()
		return nil, fmt.Errorf("聊天预算调用 ID 已被预占；请检查调用方是否重复使用 call ID")
	}
	for _, spec := range specs {
		values := a.budgetTokens
		reserve := reservedTokens
		if spec.metric == "cost" {
			values = a.budgetCosts
			reserve = reservedCost
		}
		used := countBudgetUsageMatchesLocked(values, "chat", scopeKey, actorID, spec.scopeMatch, spec.actorMatch)
		if used+reserve > spec.limit {
			a.budgetMu.Unlock()
			unit := "token"
			if spec.metric == "cost" {
				unit = "微单位"
			}
			return nil, fmt.Errorf("%s额度 %d %s 不足：已用 %d，本次预占 %d", spec.label, spec.limit, unit, used, reserve)
		}
	}
	if a.budgetTokens == nil {
		a.budgetTokens = map[string]int64{}
	}
	if a.budgetCosts == nil {
		a.budgetCosts = map[string]int64{}
	}
	a.budgetTokens[key] = reservedTokens
	if reservedCost > 0 {
		a.budgetCosts[key] = reservedCost
	}
	if a.budgetUncertain == nil {
		a.budgetUncertain = map[string]int64{}
	}
	a.budgetUncertain[key] = now.Unix()
	a.budgetMu.Unlock()

	if a.statePath != "" {
		if err := a.saveRuntimeState(); err != nil {
			a.budgetMu.Lock()
			delete(a.budgetTokens, key)
			delete(a.budgetCosts, key)
			delete(a.budgetUncertain, key)
			a.budgetMu.Unlock()
			a.markBudgetWriteFailed(true)
			return nil, fmt.Errorf("聊天预算预占写入失败：%w", err)
		}
		a.markBudgetWriteFailed(false)
	}
	a.audit("chat_budget_reserved", "model", selection.Model, "call_id", callID, "reserved_tokens", reservedTokens, "reserved_cost_micros", reservedCost, "result", logging.ResultSucceeded)
	return &chatBudgetReservation{key: key, callID: callID, model: strings.TrimSpace(selection.Model), reservedTokens: reservedTokens, reservedCost: reservedCost}, nil
}

// settleChatBudget settles one hard-mode reservation. release=true is only for
// a definite pre-call failure where the provider never accepted a billable
// request. Missing usage keeps the reserved amount charged and marks the entry
// uncertain, so an upstream that never returns usage cannot spend forever.
func (a *Agent) settleChatBudget(ctx context.Context, reservation *chatBudgetReservation, usage *llm.Usage, release bool) {
	if a == nil || reservation == nil {
		return
	}
	_ = ctx
	now := time.Now()
	var actualTokens, actualCost int64
	uncertain := false
	switch {
	case release:
		// Remove the reservation; nothing was executed.
	case usage != nil:
		actualTokens = llmUsageTotalTokens(usage)
		if price, ok := a.pricing.PriceFor(reservation.model, now); ok {
			actualCost = llmUsageCostMicros(price, usage)
		} else if reservation.reservedCost > 0 {
			actualCost = reservation.reservedCost
			uncertain = true
		}
	default:
		actualTokens = reservation.reservedTokens
		actualCost = reservation.reservedCost
		uncertain = true
	}

	a.budgetMu.Lock()
	if actualTokens > 0 {
		if a.budgetTokens == nil {
			a.budgetTokens = map[string]int64{}
		}
		a.budgetTokens[reservation.key] = actualTokens
	} else {
		delete(a.budgetTokens, reservation.key)
	}
	if actualCost > 0 {
		if a.budgetCosts == nil {
			a.budgetCosts = map[string]int64{}
		}
		a.budgetCosts[reservation.key] = actualCost
	} else {
		delete(a.budgetCosts, reservation.key)
	}
	if uncertain {
		if a.budgetUncertain == nil {
			a.budgetUncertain = map[string]int64{}
		}
		a.budgetUncertain[reservation.key] = now.Unix()
	} else {
		delete(a.budgetUncertain, reservation.key)
	}
	a.budgetMu.Unlock()

	if release {
		a.audit("chat_budget_released", "model", reservation.model, "call_id", reservation.callID, "result", logging.ResultSucceeded)
	} else if uncertain {
		a.audit("chat_budget_uncertain", "model", reservation.model, "call_id", reservation.callID, "charged_tokens", actualTokens, "charged_cost_micros", actualCost)
	} else {
		a.audit("chat_budget_settled", "model", reservation.model, "call_id", reservation.callID, "tokens", actualTokens, "cost_micros", actualCost, "result", logging.ResultSucceeded)
	}
	if a.statePath == "" {
		return
	}
	if err := a.saveRuntimeState(); err != nil {
		a.markBudgetWriteFailed(true)
		a.audit("budget_write_error", "kind", "chat_settle", "call_id", reservation.callID, "error", err.Error(), "result", logging.ResultFailed)
		return
	}
	a.markBudgetWriteFailed(false)
}

// recordChatUsage writes post-call token/cost usage into the persistent
// ledger. Cost is calculated from the same price table as the daily report.
func (a *Agent) recordChatUsage(ctx context.Context, model string, usage *llm.Usage, callID string) {
	if a == nil || usage == nil {
		return
	}
	tokens := int64(usage.TotalTokens)
	if tokens <= 0 {
		tokens = int64(usage.PromptTokens + usage.CompletionTokens)
	}
	now := time.Now()
	costMicros := int64(0)
	if price, ok := a.pricing.PriceFor(model, now); ok {
		cost := price.ComputeCost(int64(usage.PromptTokens), int64(usage.CompletionTokens), int64(usage.CacheHitTokens))
		costMicros = int64(cost*1_000_000 + 0.5)
	}
	scopeKey := contextOverflowKey(a.scope(ctx))
	actor := a.actor(ctx)
	actorID := firstNonEmpty(actor.ID, actor.PlatformUserID)
	if strings.TrimSpace(callID) == "" {
		callID = storage.NewID()
	}
	key := budgetKey(now, "chat", scopeKey, actorID, callID)

	a.budgetMu.Lock()
	a.budgetTokens = pruneBudgetUsage(a.budgetTokens, now)
	a.budgetCosts = pruneBudgetUsage(a.budgetCosts, now)
	if tokens > 0 {
		if a.budgetTokens == nil {
			a.budgetTokens = map[string]int64{}
		}
		a.budgetTokens[key] += tokens
	}
	if costMicros > 0 {
		if a.budgetCosts == nil {
			a.budgetCosts = map[string]int64{}
		}
		a.budgetCosts[key] += costMicros
	}
	a.budgetMu.Unlock()

	if a.statePath == "" {
		return
	}
	if err := a.saveRuntimeState(); err != nil {
		a.markBudgetWriteFailed(true)
		a.audit("budget_write_error", "kind", "chat_usage", "model", model, "error", err.Error(), "result", logging.ResultFailed)
		return
	}
	a.markBudgetWriteFailed(false)
}

// recordProviderRetry writes one provider retry event to a separate ledger so
// retries do not silently inflate call-count budgets but are still auditable.
func (a *Agent) recordProviderRetry(ctx context.Context, provider string) {
	if a == nil {
		return
	}
	now := time.Now()
	scopeKey := contextOverflowKey(a.scope(ctx))
	actor := a.actor(ctx)
	actorID := firstNonEmpty(actor.ID, actor.PlatformUserID)
	key := budgetKey(now, "retry", scopeKey, actorID, strings.TrimSpace(provider)+":"+storage.NewID())
	a.budgetMu.Lock()
	if a.budgetRetries == nil {
		a.budgetRetries = map[string]int64{}
	}
	a.budgetRetries[key]++
	a.budgetMu.Unlock()
	if a.statePath != "" {
		if err := a.saveRuntimeState(); err != nil {
			a.markBudgetWriteFailed(true)
			a.audit("budget_write_error", "kind", "provider_retry", "provider", provider, "error", err.Error(), "result", logging.ResultFailed)
		}
	}
}

func (a *Agent) setBudgetExecutionSnapshot(snapshot map[string]string) {
	a.budgetMu.Lock()
	a.budgetExecutions = cloneBudgetDigests(snapshot)
	a.budgetMu.Unlock()
}

func (a *Agent) budgetExecutionSnapshot() map[string]string {
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	return cloneBudgetDigests(a.budgetExecutions)
}

// reserveToolExecution records that one tool call ID with one argument digest
// is about to execute. A replay with the same digest is suppressed before the
// external operation starts; a different digest for the same call ID is
// rejected as a caller error. The reservation is intentionally conservative:
// once an external tool may have run, it is not silently released.
func (a *Agent) reserveToolExecution(ctx context.Context, callID, digest string) (bool, string) {
	if a == nil {
		return true, ""
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return true, ""
	}
	now := time.Now()
	scopeKey := contextOverflowKey(a.scope(ctx))
	actor := a.actor(ctx)
	actorID := firstNonEmpty(actor.ID, actor.PlatformUserID)
	key := budgetKey(now, "exec", scopeKey, actorID, callID)
	digest = strings.TrimSpace(digest)
	if digest == "" {
		digest = shortHash(callID)
	}
	a.budgetMu.Lock()
	a.budgetExecutions = pruneBudgetDigests(a.budgetExecutions, now)
	if existing := strings.TrimSpace(a.budgetExecutions[key]); existing != "" {
		a.budgetMu.Unlock()
		if existing == digest {
			return false, "该工具调用已经执行过，已抑制重复执行"
		}
		return false, "工具调用 ID 被复用到了不同参数；已拒绝重复执行"
	}
	if a.budgetExecutions == nil {
		a.budgetExecutions = map[string]string{}
	}
	a.budgetExecutions[key] = digest
	a.budgetMu.Unlock()

	if a.statePath != "" {
		if err := a.saveRuntimeState(); err != nil {
			a.budgetMu.Lock()
			delete(a.budgetExecutions, key)
			a.budgetMu.Unlock()
			a.markBudgetWriteFailed(true)
			return false, fmt.Sprintf("执行幂等账本写入失败：%v", err)
		}
		a.markBudgetWriteFailed(false)
	}
	return true, ""
}

func (a *Agent) releaseToolExecution(ctx context.Context, callID string) {
	if a == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	now := time.Now()
	scopeKey := contextOverflowKey(a.scope(ctx))
	actor := a.actor(ctx)
	actorID := firstNonEmpty(actor.ID, actor.PlatformUserID)
	key := budgetKey(now, "exec", scopeKey, actorID, callID)
	a.budgetMu.Lock()
	delete(a.budgetExecutions, key)
	a.budgetMu.Unlock()
	if a.statePath != "" {
		if err := a.saveRuntimeState(); err != nil {
			a.markBudgetWriteFailed(true)
			a.audit("budget_write_error", "kind", "release_execution", "call_id", callID, "error", err.Error(), "result", logging.ResultFailed)
		}
	}
}

// chatBudgetUsageForScope returns today's group-level chat token and cost
// usage. Non-group scopes report zero.
func (a *Agent) chatBudgetUsageForScope(ctx context.Context) (int64, int64) {
	if a == nil || !a.isGroupScope(ctx) {
		return 0, 0
	}
	scopeKey := contextOverflowKey(a.scope(ctx))
	now := time.Now()
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	a.budgetTokens = pruneBudgetUsage(a.budgetTokens, now)
	a.budgetCosts = pruneBudgetUsage(a.budgetCosts, now)
	tokens := countBudgetUsageMatchesLocked(a.budgetTokens, "chat", scopeKey, "", true, false)
	costs := countBudgetUsageMatchesLocked(a.budgetCosts, "chat", scopeKey, "", true, false)
	return tokens, costs
}

// budgetUsageForScope returns today's image and vision usage for the current
// group. Non-group scopes report zero.
func (a *Agent) budgetUsageForScope(ctx context.Context) (int64, int64) {
	if a == nil || !a.isGroupScope(ctx) {
		return 0, 0
	}
	scopeKey := contextOverflowKey(a.scope(ctx))
	now := time.Now()
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	imageUsed := countBudgetPrefixLocked(a.budgetReservations, budgetPrefix(now, "image", scopeKey))
	visionUsed := countBudgetPrefixLocked(a.budgetReservations, budgetPrefix(now, "vision", scopeKey))
	return imageUsed, visionUsed
}

// asrBudgetUsageForScope returns today's voice-transcription usage for the
// current group. Non-group scopes report zero.
func (a *Agent) asrBudgetUsageForScope(ctx context.Context) int64 {
	if a == nil || !a.isGroupScope(ctx) {
		return 0
	}
	scopeKey := contextOverflowKey(a.scope(ctx))
	now := time.Now()
	a.budgetMu.Lock()
	defer a.budgetMu.Unlock()
	return countBudgetPrefixLocked(a.budgetReservations, budgetPrefix(now, "asr", scopeKey))
}

func countBudgetPrefixLocked(reservations map[string]int64, prefix string) int64 {
	var count int64
	for key := range reservations {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}

func countBudgetReservationMatchesLocked(values map[string]int64, kind, scopeKey, actorID string, scopeMatch, actorMatch bool) int64 {
	if len(values) == 0 {
		return 0
	}
	today := time.Now().Format("2006-01-02")
	kind = strings.TrimSpace(kind)
	scopeKey = strings.TrimSpace(scopeKey)
	actorID = strings.TrimSpace(actorID)
	var total int64
	for key, value := range values {
		date, keyKind, keyScope, keyActor, ok := budgetKeyParts(key)
		if !ok || date != today || keyKind != kind || value <= 0 {
			continue
		}
		if scopeMatch && keyScope != scopeKey {
			continue
		}
		if actorMatch && keyActor != actorID {
			continue
		}
		total++
	}
	return total
}

func countBudgetUsageMatchesLocked(values map[string]int64, kind, scopeKey, actorID string, scopeMatch, actorMatch bool) int64 {
	if len(values) == 0 {
		return 0
	}
	today := time.Now().Format("2006-01-02")
	kind = strings.TrimSpace(kind)
	scopeKey = strings.TrimSpace(scopeKey)
	actorID = strings.TrimSpace(actorID)
	var total int64
	for key, value := range values {
		date, keyKind, keyScope, keyActor, ok := budgetKeyParts(key)
		if !ok || date != today || keyKind != kind || value <= 0 {
			continue
		}
		if scopeMatch && keyScope != scopeKey {
			continue
		}
		if actorMatch && keyActor != actorID {
			continue
		}
		total += value
	}
	return total
}

func pruneBudgetUsage(values map[string]int64, now time.Time) map[string]int64 {
	return pruneBudgetReservations(values, now)
}

func pruneBudgetDigests(digests map[string]string, now time.Time) map[string]string {
	if len(digests) == 0 {
		return map[string]string{}
	}
	cutoff := now.AddDate(0, 0, -1).Format("2006-01-02")
	out := make(map[string]string, len(digests))
	for key, value := range digests {
		date, _, ok := strings.Cut(key, "|")
		if !ok || date < cutoff || strings.TrimSpace(value) == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func pruneBudgetReservations(reservations map[string]int64, now time.Time) map[string]int64 {
	if len(reservations) == 0 {
		return map[string]int64{}
	}
	cutoff := now.AddDate(0, 0, -1).Format("2006-01-02")
	out := make(map[string]int64, len(reservations))
	for key, value := range reservations {
		date, _, ok := strings.Cut(key, "|")
		if !ok || date < cutoff || value <= 0 {
			continue
		}
		out[key] = value
	}
	return out
}
