package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"elbot/internal/command"
	"elbot/internal/security"
)

type GroupServicesModule struct{}

func (GroupServicesModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return RegisterFactories(registrar, deps,
		NewRemind,
		NewPoll,
		NewVote,
		NewSignup,
		NewJoin,
	)
}

func NewRemind(deps Deps) command.Handler {
	return command.NewFunc(command.Info{
		Name:        "remind",
		Aliases:     []string{"reminder", "timer", "reminders"},
		Usage:       "/remind <时间> <内容>",
		Description: "创建群提醒；支持相对时间、时刻和日期时间。",
		MinRole:     security.RoleUser,
		Help: strings.TrimSpace(`Usage:
  /remind <时间> <内容>   # 例如：/remind 10m 开会
  /remind list
  /remind remove <id>

时间格式:
  10m / 1h30m / 2d / 2d3h
  15:04 / 15:04:05
  YYYY-MM-DD HH:MM / MM-DD HH:MM`),
	}, func(ctx context.Context, req command.Request) (*command.Result, error) {
		if deps.GroupServices == nil {
			return nil, fmt.Errorf("group services are not configured")
		}
		args := strings.TrimSpace(req.Args)
		if args == "" || strings.EqualFold(args, "list") {
			content, err := deps.GroupServices.ReminderList(ctx)
			return resultOrError(content, err)
		}
		name, rest := splitCommandArgs(args)
		switch strings.ToLower(name) {
		case "remove", "delete", "rm":
			content, err := deps.GroupServices.ReminderRemove(ctx, rest)
			return resultOrError(content, err)
		case "help", "?":
			return &command.Result{Content: remindUsage()}, nil
		default:
			when, text := name, rest
			if text == "" {
				return nil, fmt.Errorf("用法：/remind <时间> <内容>")
			}
			content, err := deps.GroupServices.ReminderCreate(ctx, when, text)
			return resultOrError(content, err)
		}
	})
}

func NewPoll(deps Deps) command.Handler {
	return command.NewFunc(command.Info{
		Name:        "poll",
		Aliases:     []string{"polls"},
		Usage:       "/poll <问题> | <选项1> | <选项2>",
		Description: "创建群投票；用 /vote <id> <序号> 投票。",
		MinRole:     security.RoleUser,
		Help: strings.TrimSpace(`Usage:
  /poll <问题> | <选项1> | <选项2>
  /poll list
  /poll show <id>
  /poll close <id>

示例:
  /poll 周末团建去哪 | 爬山 | 桌游 | 聚餐
  /vote p1 2`),
	}, func(ctx context.Context, req command.Request) (*command.Result, error) {
		if deps.GroupServices == nil {
			return nil, fmt.Errorf("group services are not configured")
		}
		args := strings.TrimSpace(req.Args)
		if args == "" || strings.EqualFold(args, "list") {
			content, err := deps.GroupServices.PollList(ctx)
			return resultOrError(content, err)
		}
		name, rest := splitCommandArgs(args)
		switch strings.ToLower(name) {
		case "show", "results", "result":
			if strings.TrimSpace(rest) == "" {
				return nil, fmt.Errorf("请指定投票 id")
			}
			content, err := deps.GroupServices.PollShow(ctx, rest)
			return resultOrError(content, err)
		case "close", "end":
			if strings.TrimSpace(rest) == "" {
				return nil, fmt.Errorf("请指定投票 id")
			}
			content, err := deps.GroupServices.PollClose(ctx, rest)
			return resultOrError(content, err)
		case "help", "?":
			return &command.Result{Content: pollUsage()}, nil
		default:
			question, options := parsePollArgs(args)
			if question == "" || len(options) < 2 {
				return nil, fmt.Errorf("用法：/poll <问题> | <选项1> | <选项2>")
			}
			content, err := deps.GroupServices.PollCreate(ctx, question, options)
			return resultOrError(content, err)
		}
	})
}

func NewVote(deps Deps) command.Handler {
	return command.NewFunc(command.Info{
		Name:        "vote",
		Aliases:     []string{"ballot"},
		Usage:       "/vote <投票id> <选项序号>",
		Description: "为当前群的投票投票；可改票。",
		MinRole:     security.RoleUser,
	}, func(ctx context.Context, req command.Request) (*command.Result, error) {
		if deps.GroupServices == nil {
			return nil, fmt.Errorf("group services are not configured")
		}
		id, option := splitCommandArgs(strings.TrimSpace(req.Args))
		if id == "" || option == "" {
			return nil, fmt.Errorf("用法：/vote <投票id> <选项序号>")
		}
		content, err := deps.GroupServices.PollVote(ctx, id, option)
		return resultOrError(content, err)
	})
}

func NewSignup(deps Deps) command.Handler {
	return command.NewFunc(command.Info{
		Name:        "signup",
		Aliases:     []string{"sign", "signups"},
		Usage:       "/signup <标题> [人数]",
		Description: "创建群报名；用 /join <id> 加入。",
		MinRole:     security.RoleUser,
		Help: strings.TrimSpace(`Usage:
  /signup <标题> [人数]     # 人数省略或 0 表示不限
  /signup list
  /signup show <id>
  /signup join <id>
  /signup leave <id>
  /signup close <id>`),
	}, func(ctx context.Context, req command.Request) (*command.Result, error) {
		if deps.GroupServices == nil {
			return nil, fmt.Errorf("group services are not configured")
		}
		args := strings.TrimSpace(req.Args)
		if args == "" || strings.EqualFold(args, "list") {
			content, err := deps.GroupServices.SignupList(ctx)
			return resultOrError(content, err)
		}
		name, rest := splitCommandArgs(args)
		switch strings.ToLower(name) {
		case "show":
			if strings.TrimSpace(rest) == "" {
				return nil, fmt.Errorf("请指定报名 id")
			}
			content, err := deps.GroupServices.SignupShow(ctx, rest)
			return resultOrError(content, err)
		case "join":
			if strings.TrimSpace(rest) == "" {
				return nil, fmt.Errorf("请指定报名 id")
			}
			content, err := deps.GroupServices.SignupJoin(ctx, rest)
			return resultOrError(content, err)
		case "leave", "quit":
			if strings.TrimSpace(rest) == "" {
				return nil, fmt.Errorf("请指定报名 id")
			}
			content, err := deps.GroupServices.SignupLeave(ctx, rest)
			return resultOrError(content, err)
		case "close", "end":
			if strings.TrimSpace(rest) == "" {
				return nil, fmt.Errorf("请指定报名 id")
			}
			content, err := deps.GroupServices.SignupClose(ctx, rest)
			return resultOrError(content, err)
		case "help", "?":
			return &command.Result{Content: signupUsage()}, nil
		default:
			title, capacity := parseSignupArgs(args)
			if title == "" {
				return nil, fmt.Errorf("用法：/signup <标题> [人数]")
			}
			content, err := deps.GroupServices.SignupCreate(ctx, title, capacity)
			return resultOrError(content, err)
		}
	})
}

func NewJoin(deps Deps) command.Handler {
	return command.NewFunc(command.Info{
		Name:        "join",
		Usage:       "/join <报名id>",
		Description: "加入当前群的报名。",
		MinRole:     security.RoleUser,
	}, func(ctx context.Context, req command.Request) (*command.Result, error) {
		if deps.GroupServices == nil {
			return nil, fmt.Errorf("group services are not configured")
		}
		id := strings.TrimSpace(req.Args)
		if id == "" {
			return nil, fmt.Errorf("用法：/join <报名id>")
		}
		content, err := deps.GroupServices.SignupJoin(ctx, id)
		return resultOrError(content, err)
	})
}

func resultOrError(content string, err error) (*command.Result, error) {
	if err != nil {
		return nil, err
	}
	return &command.Result{Content: content}, nil
}

func remindUsage() string {
	return "用法：/remind <时间> <内容>；/remind list；/remind remove <id>"
}

func pollUsage() string {
	return "用法：/poll <问题> | <选项1> | <选项2>；/poll list；/poll show <id>；/poll close <id>"
}

func signupUsage() string {
	return "用法：/signup <标题> [人数]；/signup list；/signup show <id>；/signup join <id>；/signup leave <id>；/signup close <id>"
}

func parsePollArgs(args string) (string, []string) {
	parts := strings.FieldsFunc(args, func(r rune) bool { return r == '|' || r == '\uff5c' })
	if len(parts) < 2 {
		return "", nil
	}
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) < 2 {
		return "", nil
	}
	return clean[0], clean[1:]
}

func parseSignupArgs(args string) (string, int) {
	fields := strings.Fields(args)
	if len(fields) >= 2 {
		if capacity, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			return strings.Join(fields[:len(fields)-1], " "), capacity
		}
	}
	return strings.Join(fields, " "), 0
}
