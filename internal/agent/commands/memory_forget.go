package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/command"
	"elbot/internal/memory/resident"
	"elbot/internal/security"
	"elbot/internal/session"
)

type forgetCommand struct{ deps Deps }

func (c forgetCommand) Info() command.Info {
	return command.Info{
		Name:        "forget",
		Aliases:     []string{"forget_memory", "delete_memory"},
		Usage:       "/forget list [n] | <id> | source <消息id> | resident normal|core|all [--confirm]",
		Description: "删除当前范围中自己可管理的长期记忆，或清空自己的常驻记忆。",
		MinRole:     security.RoleUser,
		Help: strings.TrimSpace(`Usage:
  /forget list [n]                 # 列出自己可删除的长期记忆（默认 20）
  /forget <id>                     # 删除一条长期记忆；id 支持唯一前缀
  /forget source <平台消息id>       # 删除由该消息派生的、自己可删除的记忆
  /forget resident normal          # 清空自己的普通常驻记忆
  /forget resident core --confirm  # 清空自己的核心常驻记忆
  /forget resident all --confirm   # 清空自己的全部常驻记忆

说明:
  群聊中普通成员只能删除来源成员为自己的长期记忆；
  群主、群管理员和超级管理员可以删除当前群 scope 的任意长期记忆。
  private/单聊 scope 中当前用户可删除整个 scope 的记忆。`),
	}
}

func (c forgetCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	args := strings.TrimSpace(req.Args)
	if args == "" || args == "help" || args == "?" {
		return &command.Result{Content: c.Info().Help}, nil
	}
	scope := c.deps.Scope(ctx)
	fields := strings.Fields(args)
	action := strings.ToLower(fields[0])
	switch action {
	case "list", "ls":
		return c.handleList(ctx, scope, fields)
	case "source":
		return c.handleSource(ctx, scope, fields)
	case "resident":
		return c.handleResident(ctx, scope, fields)
	default:
		return c.deleteOne(ctx, scope, fields[0])
	}
}

func (c forgetCommand) handleList(ctx context.Context, scope session.Scope, fields []string) (*command.Result, error) {
	if c.deps.AngelMemory == nil || !c.deps.AngelMemory.Ready() {
		return &command.Result{Content: "angel memory 未启用或未配置。"}, nil
	}
	limit := 20
	if len(fields) > 1 {
		limit = memoryParseLimit(fields[1], 20, 100)
	}
	filter := memoryVisibleFilter(ctx, scope)
	memories, err := c.deps.AngelMemory.List(ctx, scope.Platform, scope.PlatformScopeID, filter, limit)
	if err != nil {
		return nil, err
	}
	if len(memories) == 0 {
		return &command.Result{Content: "当前没有你可删除的长期记忆。"}, nil
	}
	lines := []string{fmt.Sprintf("可管理的长期记忆：%d 条", len(memories))}
	for i, memory := range memories {
		lines = append(lines, memoryEntryLine(i+1, memory))
	}
	lines = append(lines, "删除请用：/forget <id>")
	return &command.Result{Content: strings.Join(lines, "\n")}, nil
}

func (c forgetCommand) handleSource(ctx context.Context, scope session.Scope, fields []string) (*command.Result, error) {
	if c.deps.AngelMemory == nil || !c.deps.AngelMemory.Ready() {
		return &command.Result{Content: "angel memory 未启用或未配置。"}, nil
	}
	if len(fields) < 2 {
		return &command.Result{Content: "用法：/forget source <平台消息id>"}, nil
	}
	messageID := strings.TrimSpace(fields[1])
	if messageID == "" {
		return &command.Result{Content: "用法：/forget source <平台消息id>"}, nil
	}
	filter := memoryVisibleFilter(ctx, scope)
	filter.MessageID = messageID
	count, err := c.deps.AngelMemory.DeleteBySource(ctx, scope.Platform, scope.PlatformScopeID, filter)
	if err != nil {
		return nil, err
	}
	commandAudit(c.deps, "angel_memory_forget_source", "platform", scope.Platform, "scope", scope.PlatformScopeID, "source_message_id", messageID, "count", count)
	return &command.Result{Content: fmt.Sprintf("已删除 %d 条由消息 %s 派生的记忆。", count, messageID)}, nil
}

func (c forgetCommand) deleteOne(ctx context.Context, scope session.Scope, prefix string) (*command.Result, error) {
	if c.deps.AngelMemory == nil || !c.deps.AngelMemory.Ready() {
		return &command.Result{Content: "angel memory 未启用或未配置。"}, nil
	}
	filter := memoryVisibleFilter(ctx, scope)
	memory, err := resolveScopedMemory(ctx, c.deps.AngelMemory, scope.Platform, scope.PlatformScopeID, prefix, filter)
	if err != nil {
		return nil, err
	}
	if !memoryCanDelete(ctx, scope, memory) {
		return &command.Result{Content: "你没有权限删除这条记忆。"}, nil
	}
	if err := c.deps.AngelMemory.Delete(ctx, scope.Platform, scope.PlatformScopeID, memory.ID); err != nil {
		return nil, err
	}
	commandAudit(c.deps, "angel_memory_forget", "platform", scope.Platform, "scope", scope.PlatformScopeID, "memory_id", memory.ID, "source_actor_id", memory.SourceActorID, "source_message_id", memory.SourceMessageID)
	return &command.Result{Content: fmt.Sprintf("已删除记忆 %s。", shortMemoryID(memory.ID))}, nil
}

func (c forgetCommand) handleResident(ctx context.Context, scope session.Scope, fields []string) (*command.Result, error) {
	if c.deps.ResidentMemory == nil {
		return &command.Result{Content: "常驻记忆未启用或未配置。"}, nil
	}
	action := "normal"
	if len(fields) > 1 {
		action = strings.ToLower(strings.TrimSpace(fields[1]))
	}
	confirmed := false
	for _, field := range fields[2:] {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "--confirm", "-y", "--yes", "confirm":
			confirmed = true
		}
	}
	actor, ok := security.ActorFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("无法确定当前用户身份")
	}
	actorScope := resident.ActorScope(actor)
	if strings.TrimSpace(actorScope.Platform) == "" {
		actorScope.Platform = scope.Platform
	}
	if strings.TrimSpace(actorScope.ActorID) == "" {
		actorScope.ActorID = scope.ActorID
	}
	if strings.TrimSpace(actorScope.Platform) == "" || strings.TrimSpace(actorScope.ActorID) == "" {
		return &command.Result{Content: "当前上下文没有足够的用户信息，无法清空常驻记忆。"}, nil
	}
	requiresConfirm := action == "core" || action == "all"
	if requiresConfirm && !confirmed {
		return &command.Result{Content: fmt.Sprintf("清空 %s 常驻记忆需要确认：/forget resident %s --confirm", action, action)}, nil
	}
	switch action {
	case "normal":
		if err := c.deps.ResidentMemory.DeleteNormal(ctx, actorScope); err != nil {
			return nil, err
		}
	case "core":
		if err := c.deps.ResidentMemory.WriteCore(ctx, actorScope, ""); err != nil {
			return nil, err
		}
	case "all", "clear":
		if err := c.deps.ResidentMemory.DeleteNormal(ctx, actorScope); err != nil {
			return nil, err
		}
		if err := c.deps.ResidentMemory.WriteCore(ctx, actorScope, ""); err != nil {
			return nil, err
		}
	default:
		return &command.Result{Content: "用法：/forget resident normal|core|all [--confirm]"}, nil
	}
	commandAudit(c.deps, "resident_memory_forget", "platform", actorScope.Platform, "actor_id", actorScope.ActorID, "section", action)
	if action == "normal" {
		return &command.Result{Content: "已清空你的普通常驻记忆。"}, nil
	}
	return &command.Result{Content: fmt.Sprintf("已清空你的 %s 常驻记忆。", action)}, nil
}
