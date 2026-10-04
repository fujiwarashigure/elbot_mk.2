package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/command"
	"elbot/internal/config"
	"elbot/internal/security"
)

type MemoryLearningModule struct{}

func (MemoryLearningModule) RegisterCommands(registrar Registrar, deps Deps) error {
	if err := registrar.Register(memoryCommand{deps: deps}); err != nil {
		return err
	}
	return registrar.Register(learningCommand{deps: deps})
}

type memoryCommand struct{ deps Deps }

func (c memoryCommand) Info() command.Info {
	return command.Info{
		Name:        "memory",
		Aliases:     []string{"angel_memory"},
		Usage:       "/memory status | recall <关键词>",
		Description: "查看或检索当前平台/会话的 clean-room 长期记忆。",
		MinRole:     security.RoleSuperadmin,
		Help: strings.TrimSpace(`Usage:
  /memory status
  /memory recall [关键词]`),
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
			lines = append(lines, fmt.Sprintf("%d. [%d] %s", i+1, memory.Strength, memory.Content))
		}
		return &command.Result{Content: strings.Join(lines, "\n")}, nil
	default:
		return &command.Result{Content: "用法：/memory status 或 /memory recall <关键词>"}, nil
	}
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
