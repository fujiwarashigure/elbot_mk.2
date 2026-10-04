package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/angelmemory"
	"elbot/internal/command"
	"elbot/internal/config"
	"elbot/internal/security"
)

type MemoryLearningModule struct{}

func (MemoryLearningModule) RegisterCommands(registrar Registrar, deps Deps) error {
	if err := registrar.Register(memoryCommand{deps: deps}); err != nil {
		return err
	}
	if err := registrar.Register(forgetCommand{deps: deps}); err != nil {
		return err
	}
	return registrar.Register(learningCommand{deps: deps})
}

type memoryCommand struct{ deps Deps }

func (c memoryCommand) Info() command.Info {
	return command.Info{
		Name:        "memory",
		Aliases:     []string{"angel_memory"},
		Usage:       "/memory status | list [n] | recall [关键词] | show <id> | delete <id> | source <消息id> | backfill [--confirm]",
		Description: "查看、检索或删除当前平台/会话的 clean-room 长期记忆。",
		MinRole:     security.RoleSuperadmin,
		Help: strings.TrimSpace(`Usage:
  /memory status                     # 查看当前范围条数
  /memory list [n]                   # 列出记忆（默认 20，上限 100）
  /memory recall [关键词]             # 按关键词检索
  /memory show <id>                  # 查看一条记忆的来源与内容
  /memory delete <id>                # 删除一条记忆
  /memory source <平台消息id>         # 删除由该消息派生的记忆
  /memory backfill                   # 预检旧记忆来源回填（全库统计）
  /memory backfill --confirm         # 为旧记忆回填可确定的 source_kind

说明:
  id 支持唯一前缀；删除只作用于当前平台/会话 scope。
  旧记忆没有记录来源成员 / 消息 ID / Session ID，无法回填，也不会被按来源删除命中。`),
	}
}

func (c memoryCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	if c.deps.AngelMemory == nil || !c.deps.AngelMemory.Ready() {
		return &command.Result{Content: "angel memory 未启用或未配置。"}, nil
	}
	scope := c.deps.Scope(ctx)
	args := strings.Fields(req.Args)
	action := "status"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch action {
	case "status":
		count, err := c.deps.AngelMemory.Count(ctx, scope.Platform, scope.PlatformScopeID)
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: fmt.Sprintf("当前范围长期记忆：%d 条。", count)}, nil
	case "list", "ls":
		limit := 20
		if len(args) > 1 {
			limit = memoryParseLimit(args[1], 20, 100)
		}
		memories, err := c.deps.AngelMemory.List(ctx, scope.Platform, scope.PlatformScopeID, angelmemory.SourceFilter{}, limit)
		if err != nil {
			return nil, err
		}
		if len(memories) == 0 {
			return &command.Result{Content: "当前范围没有长期记忆。"}, nil
		}
		lines := []string{fmt.Sprintf("长期记忆 %d 条（最多显示 %d 条）：", len(memories), limit)}
		for i, memory := range memories {
			lines = append(lines, memoryEntryLine(i+1, memory))
		}
		return &command.Result{Content: strings.Join(lines, "\n")}, nil
	case "recall":
		query := strings.TrimSpace(strings.TrimPrefix(req.Args, args[0]))
		memories, err := c.deps.AngelMemory.Recall(ctx, scope.Platform, scope.PlatformScopeID, query, 10)
		if err != nil {
			return nil, err
		}
		if len(memories) == 0 {
			return &command.Result{Content: "没有找到符合条件的长期记忆。"}, nil
		}
		lines := []string{fmt.Sprintf("记忆 %d 条：", len(memories))}
		for i, memory := range memories {
			lines = append(lines, memoryEntryLine(i+1, memory))
		}
		return &command.Result{Content: strings.Join(lines, "\n")}, nil
	case "show", "detail":
		if len(args) < 2 {
			return &command.Result{Content: "用法：/memory show <id>"}, nil
		}
		memory, err := resolveScopedMemory(ctx, c.deps.AngelMemory, scope.Platform, scope.PlatformScopeID, args[1], angelmemory.SourceFilter{})
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: memoryEntryDetail(*memory)}, nil
	case "delete", "forget", "rm":
		if len(args) < 2 {
			return &command.Result{Content: "用法：/memory delete <id>"}, nil
		}
		memory, err := resolveScopedMemory(ctx, c.deps.AngelMemory, scope.Platform, scope.PlatformScopeID, args[1], angelmemory.SourceFilter{})
		if err != nil {
			return nil, err
		}
		if err := c.deps.AngelMemory.Delete(ctx, scope.Platform, scope.PlatformScopeID, memory.ID); err != nil {
			return nil, err
		}
		commandAudit(c.deps, "angel_memory_delete", "platform", scope.Platform, "scope", scope.PlatformScopeID, "memory_id", memory.ID, "source_message_id", memory.SourceMessageID)
		return &command.Result{Content: fmt.Sprintf("已删除记忆 %s。", shortMemoryID(memory.ID))}, nil
	case "source":
		if len(args) < 2 {
			return &command.Result{Content: "用法：/memory source <平台消息id>"}, nil
		}
		count, err := c.deps.AngelMemory.DeleteBySource(ctx, scope.Platform, scope.PlatformScopeID, angelmemory.SourceFilter{MessageID: args[1]})
		if err != nil {
			return nil, err
		}
		commandAudit(c.deps, "angel_memory_delete_source", "platform", scope.Platform, "scope", scope.PlatformScopeID, "source_message_id", args[1], "count", count)
		return &command.Result{Content: fmt.Sprintf("已删除 %d 条由消息 %s 派生的记忆。", count, args[1])}, nil
	case "backfill":
		stats, err := c.deps.AngelMemory.LegacySourceBackfillStats(ctx)
		if err != nil {
			return nil, err
		}
		if !hasFlag(args[1:], "--confirm") {
			return &command.Result{Content: formatLegacyBackfillPreview(stats)}, nil
		}
		count, err := c.deps.AngelMemory.BackfillLegacySourceKind(ctx)
		if err != nil {
			return nil, err
		}
		commandAudit(c.deps, "angel_memory_backfill_legacy_source", "updated", count, "total", stats.Total, "linked", stats.Linked)
		return &command.Result{Content: fmt.Sprintf("已为 %d 条旧记忆回填 source_kind（全库共 %d 条，其中 %d 条已有结构化来源）。\n旧记忆的来源成员、消息 ID 和 Session ID 无法回填，仍按“不匹配、不误删”处理。", count, stats.Total, stats.Linked)}, nil
	default:
		return &command.Result{Content: "用法：/memory status|list|recall|show|delete|source|backfill"}, nil
	}
}

func formatLegacyBackfillPreview(stats angelmemory.LegacySourceBackfill) string {
	unlinked := stats.Total - stats.Linked
	if unlinked < 0 {
		unlinked = 0
	}
	lines := []string{
		"旧记忆来源回填预检（全库统计，尚未修改任何数据）：",
		fmt.Sprintf("记忆总数：%d 条", stats.Total),
		fmt.Sprintf("已有结构化来源：%d 条", stats.Linked),
		fmt.Sprintf("可确定性回填 source_kind=tool：%d 条（旧版只写自由文本 source=\"tool\"，写入方唯一，可直接确定）", stats.Backfillable),
		fmt.Sprintf("无法回填的来源成员 / 消息 ID / Session ID：涉及 %d 条旧记忆（旧数据没有记录这些字段）", unlinked),
		"这部分按“不匹配、不误删”处理：不会按来源消息或 Session 删除旧记忆，也不会被 /forget source、撤回清理和 /delete Session 命中。",
	}
	if stats.Backfillable == 0 {
		lines = append(lines, "当前没有需要回填的条目。")
	} else {
		lines = append(lines, "执行回填：/memory backfill --confirm")
	}
	return strings.Join(lines, "\n")
}

type learningCommand struct{ deps Deps }

func (c learningCommand) Info() command.Info {
	return command.Info{
		Name:                 "learning",
		Aliases:              []string{"selflearning"},
		Usage:                "/learning status | mine | review [status] | approve <id> [meaning] | reject <id> | undo <id> | history <id>",
		Description:          "管理 clean-room 表达/黑话候选；只有 approved 的内容会注入上下文。",
		MinRole:              security.RoleSuperadmin,
		AllowGroupAdmin:      true,
		GroupAdminNeedsGrant: true,
		Help: strings.TrimSpace(`Usage:
  /learning status
  /learning mine
  /learning review [pending|approved|rejected]
  /learning approve <id> [含义]
  /learning reject <id>
  /learning undo <id>
  /learning history <id>`),
	}
}

func (c learningCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	if c.deps.SelfLearning == nil || !c.deps.SelfLearning.Ready() {
		return &command.Result{Content: "self learning 未启用或未配置。"}, nil
	}
	if c.deps.GroupPolicy != nil && !c.deps.GroupPolicy.GroupLearningEnabled(ctx) {
		return &command.Result{Content: "本群已关闭学习观察；请让超级管理员使用 /grouppolicy learning on 开启。"}, nil
	}
	scope := c.deps.Scope(ctx)
	args := strings.Fields(req.Args)
	action := "status"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}
	if actor, _ := security.ActorFromContext(ctx); actor.Role != security.RoleSuperadmin {
		requiredAction := learningActionPermission(action)
		if requiredAction == "" || c.deps.GroupPolicy == nil || !c.deps.GroupPolicy.AuthorizeLearningAction(ctx, requiredAction) {
			return &command.Result{Content: "当前群管理员未获授权执行该学习操作；请让机器人超级管理员使用 /grouppolicy learning-moderation-actions 授予具体权限。"}, nil
		}
	}
	switch action {
	case "status":
		pending, approved, err := c.deps.SelfLearning.Stats(ctx, scope.Platform, scope.PlatformScopeID)
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: fmt.Sprintf("self learning：待审 %d 条，已批准 %d 条。", pending, approved)}, nil
	case "mine":
		stats, err := c.deps.SelfLearning.Mine(ctx, scope.Platform, scope.PlatformScopeID, 3, 100)
		if err != nil {
			return nil, err
		}
		summary := fmt.Sprintf("候选挖掘完成：新增 %d 条，更新 %d 条，跳过 %d 条；扫描 %d 条 / %d 字。", stats.Created, stats.Updated, stats.Skipped, stats.Scanned, stats.TotalChars)
		if stats.Truncated {
			summary += " 已达到挖掘预算，结果可能不完整。"
		}
		if stats.TimedOut {
			summary += " 挖掘超时，结果可能不完整。"
		}
		return &command.Result{Content: summary}, nil
	case "review":
		status := "pending"
		if len(args) > 1 {
			status = strings.TrimSpace(args[1])
		}
		candidates, err := c.deps.SelfLearning.Review(ctx, scope.Platform, scope.PlatformScopeID, status, 100)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			return &command.Result{Content: "没有符合条件的候选。"}, nil
		}
		lines := []string{fmt.Sprintf("候选 %d 条：", len(candidates))}
		for i, candidate := range candidates {
			lines = append(lines, fmt.Sprintf("%d. [%s] id=%s count=%d pattern=%s", i+1, candidate.Status, candidate.ID, candidate.Count, candidate.Pattern))
		}
		return &command.Result{Content: strings.Join(lines, "\n")}, nil
	case "approve", "reject":
		if len(args) < 2 {
			return &command.Result{Content: "approve/reject 需要候选 ID。"}, nil
		}
		status := "approved"
		if action == "reject" {
			status = "rejected"
		}
		meaning := ""
		if len(args) > 2 {
			meaning = strings.Join(args[2:], " ")
		}
		if err := c.deps.SelfLearning.Decide(ctx, scope.Platform, scope.PlatformScopeID, args[1], status, meaning, reviewerName(ctx)); err != nil {
			return nil, err
		}
		return &command.Result{Content: fmt.Sprintf("候选 %s 已标记为 %s。", args[1], status)}, nil
	case "undo":
		if len(args) < 2 {
			return &command.Result{Content: "undo 需要候选 ID。"}, nil
		}
		if err := c.deps.SelfLearning.Undo(ctx, scope.Platform, scope.PlatformScopeID, args[1], reviewerName(ctx)); err != nil {
			return nil, err
		}
		return &command.Result{Content: fmt.Sprintf("候选 %s 已撤回为待审。", args[1])}, nil
	case "history":
		if len(args) < 2 {
			return &command.Result{Content: "history 需要候选 ID。"}, nil
		}
		records, err := c.deps.SelfLearning.History(ctx, scope.Platform, scope.PlatformScopeID, args[1], 20)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			return &command.Result{Content: "没有审核记录。"}, nil
		}
		lines := []string{fmt.Sprintf("审核历史 %d 条：", len(records))}
		for i, record := range records {
			lines = append(lines, fmt.Sprintf("%d. %s → %s by %s", i+1, record.FromStatus, record.ToStatus, record.Reviewer))
		}
		return &command.Result{Content: strings.Join(lines, "\n")}, nil
	default:
		return &command.Result{Content: "用法：/learning status|mine|review|approve|reject|undo|history"}, nil
	}
}

func learningActionPermission(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "status", "review", "history":
		return config.LearningModerationView
	case "approve", "reject", "undo":
		return config.LearningModerationDecide
	case "mine":
		return config.LearningModerationMine
	case "delete":
		return config.LearningModerationDelete
	case "export":
		return config.LearningModerationExport
	default:
		return ""
	}
}

func reviewerName(ctx context.Context) string {
	actor, ok := security.ActorFromContext(ctx)
	if !ok {
		return ""
	}
	for _, value := range []string{actor.ID, actor.PlatformUserID, actor.Nickname, actor.DisplayName} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
