package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/command"
	"elbot/internal/security"
)

func NewContextPolicy(deps Deps) command.Handler {
	return contextPolicyCommand{deps: deps}
}

type contextPolicyCommand struct {
	deps Deps
}

func (c contextPolicyCommand) Info() command.Info {
	return command.Info{
		Name:            "overflow",
		Aliases:         []string{"ctxpolicy", "longmsg"},
		Usage:           "/overflow [show|reset] [--chat|--work|--all] [reject|truncate|summarize]",
		Description:     "Show or change long-message overflow policy for this chat.",
		MinRole:         security.RoleSuperadmin,
		AllowGroupAdmin: true,
		Help: strings.TrimSpace(`Usage:
  /overflow
  /overflow show
  /overflow [--chat|--work|--all] <reject|truncate|summarize>
  /overflow reset [--chat|--work|--all]

Values:
  reject       Reject the oversized message, do not call the model.
  truncate     Keep a fitting prefix and continue.
  summarize    Summarize the oversized message with the compact model.

Examples:
  /overflow
  /overflow --work summarize
  /overflow --chat truncate
  /overflow reset --work`),
	}
}

func (c contextPolicyCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	if c.deps.ContextPolicy == nil {
		return nil, fmt.Errorf("context policy service is not configured")
	}
	args := strings.Fields(req.Args)
	if len(args) == 0 {
		return &command.Result{Content: c.deps.ContextPolicy.ContextPolicyStatus(ctx)}, nil
	}
	target := "all"
	value := ""
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "--chat", "chat":
			target = "chat"
		case "--work", "work":
			target = "work"
		case "--all", "all":
			target = "all"
		case "show", "status":
			value = "show"
		default:
			value = strings.ToLower(strings.TrimSpace(arg))
		}
	}
	switch value {
	case "", "show", "status":
		return &command.Result{Content: c.deps.ContextPolicy.ContextPolicyStatus(ctx)}, nil
	case "reset":
		content, err := c.deps.ContextPolicy.ResetContextPolicy(ctx, target)
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: content}, nil
	default:
		content, err := c.deps.ContextPolicy.SetContextPolicy(ctx, target, value)
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: content}, nil
	}
}

type ContextPolicyModule struct{}

func (ContextPolicyModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return RegisterFactories(registrar, deps, NewContextPolicy)
}
