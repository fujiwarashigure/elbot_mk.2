package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/command"
	"elbot/internal/security"
)

func NewGroupPolicy(deps Deps) command.Handler {
	return groupPolicyCommand{deps: deps}
}

type groupPolicyCommand struct {
	deps Deps
}

func (c groupPolicyCommand) Info() command.Info {
	return command.Info{
		Name:            "grouppolicy",
		Aliases:         []string{"gpolicy", "grouppolicies"},
		Usage:           "/grouppolicy [show|reset] [field] [value]",
		Description:     "查看或修改当前群的唤醒、响应、工具与额度策略。",
		MinRole:         security.RoleSuperadmin,
		AllowGroupAdmin: true,
		Help: strings.TrimSpace(`Usage:
  /grouppolicy
  /grouppolicy show
  /grouppolicy wake <词1,词2>
  /grouppolicy response <mention|all|keyword|reply|off>
  /grouppolicy default-mode <work|chat|inherit>
  /grouppolicy default-model <别名|clear>
  /grouppolicy allowed-models <别名或provider/model|*>  # 仅超级管理员
  /grouppolicy tool-allow <工具名,工具名|none|clear>
  /grouppolicy image-quota <次数>
  /grouppolicy vision-quota <次数>
  /grouppolicy user-image-quota <次数>
  /grouppolicy user-vision-quota <次数>
  /grouppolicy chat-tokens-quota <token 数>
  /grouppolicy chat-cost-quota <金额>
  /grouppolicy quiet <HH:MM-HH:MM|clear>
  /grouppolicy analysis <on|off>
  /grouppolicy learning <on|off>
  /grouppolicy history <on|off>
  /grouppolicy learning-moderation <on|off>   # 仅超级管理员
  /grouppolicy learning-moderation-actions <view,decide,mine>  # 仅超级管理员
  /grouppolicy reset [field]

Scope:
  只能操作当前群。群管理员可修改普通群策略；allowed-models、
  learning-moderation 和 learning-moderation-actions 只能由超级管理员配置。
  tool-allow none 表示当前群禁止全部工具；clear 表示继承全局工具策略。`),
	}
}

func (c groupPolicyCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	if c.deps.GroupPolicy == nil {
		return nil, fmt.Errorf("group policy service is not configured")
	}
	field, value, hasValue := strings.Cut(strings.TrimSpace(req.Args), " ")
	field = strings.ToLower(strings.TrimSpace(field))
	value = strings.TrimSpace(value)
	switch field {
	case "", "show", "status":
		return &command.Result{Content: c.deps.GroupPolicy.GroupPolicyStatus(ctx)}, nil
	case "reset":
		content, err := c.deps.GroupPolicy.ResetGroupPolicy(ctx, value)
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: content}, nil
	default:
		if !hasValue {
			return &command.Result{Content: "用法：/grouppolicy <field> <value>；发送 /grouppolicy 查看当前策略。"}, nil
		}
		content, err := c.deps.GroupPolicy.SetGroupPolicy(ctx, field, value)
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: content}, nil
	}
}

type GroupPolicyModule struct{}

func (GroupPolicyModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return RegisterFactories(registrar, deps, NewGroupPolicy)
}
