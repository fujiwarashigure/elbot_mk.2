package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"elbot/internal/request"
	"elbot/internal/storage"
	"elbot/internal/turn"
)

// MemberPanel builds the self-service task and quota view for the current
// actor. It only reads local runtime state and never invokes the model.
func (a *Agent) MemberPanel(ctx context.Context, view string) (string, error) {
	if a == nil {
		return "", fmt.Errorf("agent is nil")
	}
	view = strings.ToLower(strings.TrimSpace(view))
	if view == "" {
		view = "all"
	}
	showTasks, showQuota := true, true
	switch view {
	case "all", "panel", "status", "me":
	case "tasks", "task", "requests", "queue":
		showQuota = false
	case "quota", "budget", "limits", "usage":
		showTasks = false
	default:
		return "", fmt.Errorf("未知的面板视图 %q，可选：tasks、quota、all", view)
	}

	scope := a.scope(ctx)
	actor := a.actor(ctx)
	displayName := firstNonEmpty(actor.GroupCard, actor.Nickname, actor.DisplayName)
	if displayName == "" {
		displayName = "未知成员"
	}
	var sb strings.Builder
	sb.WriteString("我的面板\n")
	sb.WriteString(fmt.Sprintf("平台：%s\n", scope.Platform))
	sb.WriteString(fmt.Sprintf("作用域：%s\n", scope.PlatformScopeID))
	sb.WriteString(fmt.Sprintf("身份：%s（id:%s）\n", displayName, actor.ID))

	session, _ := a.currentMemberSession(ctx)
	if showTasks {
		a.writeMemberSessionSection(&sb, ctx, session)
		a.writeMemberTaskSection(&sb, ctx)
	}
	if showQuota {
		a.writeMemberQuotaSection(&sb, ctx)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func (a *Agent) currentMemberSession(ctx context.Context) (*storage.Session, error) {
	if a == nil || a.sessions == nil {
		return nil, nil
	}
	session, err := a.sessions.Current(ctx, a.scope(ctx))
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (a *Agent) writeMemberSessionSection(sb *strings.Builder, ctx context.Context, session *storage.Session) {
	sb.WriteString("\n当前会话：\n")
	if session == nil {
		sb.WriteString("  无（下一条消息会创建或恢复当前会话）\n")
		return
	}
	title := strings.TrimSpace(session.Title)
	if title == "" {
		title = "(未命名)"
	}
	sb.WriteString(fmt.Sprintf("  标题：%s\n", title))
	sb.WriteString(fmt.Sprintf("  模式：%s\n", session.Mode))
	if a.turns == nil {
		return
	}
	snapshot := a.turns.Snapshot(session.ID)
	phase := snapshot.Phase
	if phase == "" {
		phase = turn.PhaseIdle
	}
	sb.WriteString(fmt.Sprintf("  阶段：%s\n", phase))
	sb.WriteString(fmt.Sprintf("  待处理输入：%d\n", snapshot.PendingCount))
	if tools := turn.ToolsString(snapshot.Tools); tools != "" {
		sb.WriteString(fmt.Sprintf("  本轮工具：%s\n", tools))
	}
}

func (a *Agent) writeMemberTaskSection(sb *strings.Builder, ctx context.Context) {
	sb.WriteString("\n我的任务：\n")
	fairKey := a.requestFairKey(ctx)
	queued := a.inboxPendingForFairKey(fairKey)
	own := []request.Request{}
	if a.requests != nil {
		for _, req := range a.requests.List() {
			if strings.TrimSpace(req.FairKey) == fairKey {
				own = append(own, req)
			}
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i].StartedAt.Before(own[j].StartedAt) })
	if len(own) == 0 && queued == 0 {
		sb.WriteString("  当前没有进行中的任务。\n")
		return
	}
	for _, req := range own {
		sb.WriteString("  - ")
		sb.WriteString(formatMemberRequest(req))
		sb.WriteString("\n")
	}
	if queued > 0 {
		sb.WriteString(fmt.Sprintf("  排队消息：%d 条\n", queued))
	}
}

func formatMemberRequest(req request.Request) string {
	kind := strings.TrimSpace(string(req.Kind))
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = kind
	}
	elapsed := time.Since(req.StartedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return fmt.Sprintf("[%s] %s，已运行 %s（request %s）", kind, label, memberPanelDuration(elapsed), req.ID)
}

func memberPanelDuration(value time.Duration) string {
	if value < time.Second {
		return "0s"
	}
	return value.Round(time.Second).String()
}

func (a *Agent) writeMemberQuotaSection(sb *strings.Builder, ctx context.Context) {
	scope := a.scope(ctx)
	scopeKey := contextOverflowKey(scope)
	actor := a.actor(ctx)
	actorID := firstNonEmpty(actor.ID, actor.PlatformUserID)
	groupLabel := "本群"
	if !a.isGroupScope(ctx) {
		groupLabel = "当前作用域"
	}

	imageSpecs := a.callLimitSpecs(ctx, "image")
	visionSpecs := a.callLimitSpecs(ctx, "vision")
	asrSpecs := a.callLimitSpecs(ctx, "asr")
	chatSpecs := a.chatBudgetLimitSpecs(ctx)

	now := time.Now()
	a.budgetMu.Lock()
	a.budgetReservations = pruneBudgetReservations(a.budgetReservations, now)
	a.budgetTokens = pruneBudgetUsage(a.budgetTokens, now)
	a.budgetCosts = pruneBudgetUsage(a.budgetCosts, now)
	writable := !a.budgetWriteFailed

	imageLines := memberCallQuotaLines(a.budgetReservations, "image", scopeKey, actorID, imageSpecs)
	visionLines := memberCallQuotaLines(a.budgetReservations, "vision", scopeKey, actorID, visionSpecs)
	asrLines := memberCallQuotaLines(a.budgetReservations, "asr", scopeKey, actorID, asrSpecs)
	chatLines := memberChatQuotaLines(a.budgetTokens, a.budgetCosts, scopeKey, actorID, chatSpecs)
	if len(imageLines) == 0 {
		groupUsed := countBudgetReservationMatchesLocked(a.budgetReservations, "image", scopeKey, actorID, true, false)
		actorUsed := countBudgetReservationMatchesLocked(a.budgetReservations, "image", scopeKey, actorID, true, true)
		imageLines = []string{fmt.Sprintf("    %s今日已用 %d（未设置上限）", groupLabel, groupUsed), fmt.Sprintf("    我今日已用 %d", actorUsed)}
	}
	if len(visionLines) == 0 {
		groupUsed := countBudgetReservationMatchesLocked(a.budgetReservations, "vision", scopeKey, actorID, true, false)
		actorUsed := countBudgetReservationMatchesLocked(a.budgetReservations, "vision", scopeKey, actorID, true, true)
		visionLines = []string{fmt.Sprintf("    %s今日已用 %d（未设置上限）", groupLabel, groupUsed), fmt.Sprintf("    我今日已用 %d", actorUsed)}
	}
	if len(asrLines) == 0 {
		groupUsed := countBudgetReservationMatchesLocked(a.budgetReservations, "asr", scopeKey, actorID, true, false)
		actorUsed := countBudgetReservationMatchesLocked(a.budgetReservations, "asr", scopeKey, actorID, true, true)
		asrLines = []string{fmt.Sprintf("    %s今日已用 %d（未设置上限）", groupLabel, groupUsed), fmt.Sprintf("    我今日已用 %d", actorUsed)}
	}
	if len(chatLines) == 0 {
		groupTokens := countBudgetUsageMatchesLocked(a.budgetTokens, "chat", scopeKey, actorID, true, false)
		groupCosts := countBudgetUsageMatchesLocked(a.budgetCosts, "chat", scopeKey, actorID, true, false)
		actorTokens := countBudgetUsageMatchesLocked(a.budgetTokens, "chat", scopeKey, actorID, true, true)
		actorCosts := countBudgetUsageMatchesLocked(a.budgetCosts, "chat", scopeKey, actorID, true, true)
		chatLines = []string{
			fmt.Sprintf("    %s今日：%d token，%s（未设置上限）", groupLabel, groupTokens, formatMemberCostMicros(groupCosts)),
			fmt.Sprintf("    我今日：%d token，%s", actorTokens, formatMemberCostMicros(actorCosts)),
		}
	}
	a.budgetMu.Unlock()

	sb.WriteString("\n我的额度（今日）：\n")
	sb.WriteString("  生图：\n")
	for _, line := range imageLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("  视觉：\n")
	for _, line := range visionLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("  语音转写：\n")
	for _, line := range asrLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("  聊天：\n")
	for _, line := range chatLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	if a.budgetLimits.Normalized().ChatHardLimit {
		sb.WriteString("    硬预算：开启（调用前预占）\n")
	}
	if !writable {
		sb.WriteString("  注意：额度账本当前不可写，受限调用会被拒绝。\n")
	}
}

func memberCallQuotaLines(reservations map[string]int64, kind, scopeKey, actorID string, specs []budgetLimitSpec) []string {
	out := make([]string, 0, len(specs))
	for _, spec := range specs {
		// Ordinary members see group-scoped limits and their own user-scoped
		// limits. Global aggregate usage across other groups stays admin-only.
		if !spec.scopeMatch && !spec.actorMatch {
			continue
		}
		used := countBudgetReservationMatchesLocked(reservations, kind, scopeKey, actorID, spec.scopeMatch, spec.actorMatch)
		out = append(out, fmt.Sprintf("    - %s：%s", spec.label, budgetText(int(spec.limit), used)))
	}
	return out
}

func memberChatQuotaLines(tokens, costs map[string]int64, scopeKey, actorID string, specs []chatBudgetLimitSpec) []string {
	out := make([]string, 0, len(specs))
	for _, spec := range specs {
		if !spec.scopeMatch && !spec.actorMatch {
			continue
		}
		var line string
		switch spec.metric {
		case "cost":
			used := countBudgetUsageMatchesLocked(costs, "chat", scopeKey, actorID, spec.scopeMatch, spec.actorMatch)
			line = fmt.Sprintf("    - %s：%s", spec.label, costBudgetText(float64(spec.limit)/1_000_000, used))
		default:
			used := countBudgetUsageMatchesLocked(tokens, "chat", scopeKey, actorID, spec.scopeMatch, spec.actorMatch)
			line = fmt.Sprintf("    - %s：%s", spec.label, int64BudgetText(spec.limit, used))
		}
		out = append(out, line)
	}
	return out
}

func formatMemberCostMicros(micros int64) string {
	return fmt.Sprintf("%.6f", float64(micros)/1_000_000)
}
