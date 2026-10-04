package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/command"
	"elbot/internal/security"
)

type MemberPanelModule struct{}

func (MemberPanelModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return registrar.Register(NewMemberPanel(deps))
}

func NewMemberPanel(deps Deps) command.Handler {
	return memberPanelCommand{deps: deps}
}

type memberPanelCommand struct {
	deps Deps
}

func (c memberPanelCommand) Info() command.Info {
	return command.Info{
		Name:        "me",
		Aliases:     []string{"mytasks", "my", "mypanel"},
		Usage:       "/me [tasks|quota|all]",
		Description: "查看我的进行中任务和今日额度。",
		MinRole:     security.RoleUser,
		Help: strings.TrimSpace(`Usage:
  /me              # 查看任务和额度
  /me tasks        # 只看当前会话与进行中任务
  /me quota        # 只看今日额度

说明:
  只显示当前平台/作用域内属于当前用户的请求与额度；
  /me 不会调用模型，也不会改变会话状态。`),
	}
}

func (c memberPanelCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	if c.deps.MemberPanel == nil {
		return nil, fmt.Errorf("member panel service is not configured")
	}
	view := strings.ToLower(strings.TrimSpace(req.Args))
	if view == "help" || view == "?" {
		return &command.Result{Content: c.Info().Help}, nil
	}
	text, err := c.deps.MemberPanel.MemberPanel(ctx, view)
	if err != nil {
		return nil, err
	}
	return &command.Result{Content: text}, nil
}
