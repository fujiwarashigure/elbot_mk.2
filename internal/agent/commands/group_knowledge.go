package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/command"
	"elbot/internal/config"
	"elbot/internal/security"
)

type KnowledgeModule struct{}

func (KnowledgeModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return registrar.Register(NewKnowledge(deps))
}

func NewKnowledge(deps Deps) command.Handler {
	return knowledgeCommand{deps: deps}
}

type knowledgeCommand struct {
	deps Deps
}

func (c knowledgeCommand) Info() command.Info {
	return command.Info{
		Name:            "faq",
		Aliases:         []string{"knowledge", "kb", "groupkb"},
		Usage:           "/faq [list|add|add-contains|add-keyword|remove|clear|test|on|off]",
		Description:     "管理当前群的确定性知识库；命中后本地回答，不调用模型。",
		MinRole:         security.RoleSuperadmin,
		AllowGroupAdmin: true,
		Help: strings.TrimSpace(`Usage:
  /faq                          # 列出当前群知识库
  /faq list [关键词]            # 搜索
  /faq add <问题> => <答案>      # 精确匹配；左侧可用 | 分隔别名
  /faq add-contains <文本> => <答案>
  /faq add-keyword <词1,词2> => <答案>
  /faq remove <id>
  /faq clear --confirm
  /faq test <文本>
  /faq on|off                   # 开关当前群的知识库回答

说明:
  知识库只按本地确定性规则匹配，命中后直接发送答案，不进入 Session 或 LLM。
  exact 需要整句一致（忽略大小写、全半角和常用标点）；contains 只要求包含；
  keywords 要求所有关键词都出现。答案和触发词由服务端保存，不进入模型提示词。`),
	}
}

func (c knowledgeCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	if c.deps.GroupKnowledge == nil {
		return nil, fmt.Errorf("group knowledge service is not configured")
	}
	args := strings.TrimSpace(req.Args)
	if args == "" {
		return c.list(ctx, "")
	}
	name, rest := splitCommandArgs(args)
	switch strings.ToLower(name) {
	case "list", "show", "search", "ls":
		return c.list(ctx, rest)
	case "add":
		return c.add(ctx, rest, "exact")
	case "add-contains", "contains":
		return c.add(ctx, rest, "contains")
	case "add-keyword", "add-keywords", "keyword", "keywords":
		return c.add(ctx, rest, "keywords")
	case "remove", "delete", "rm":
		return c.remove(ctx, rest)
	case "clear":
		return c.clear(ctx, rest)
	case "test", "match":
		return c.test(ctx, rest)
	case "on", "enable":
		return c.setEnabled(ctx, true)
	case "off", "disable":
		return c.setEnabled(ctx, false)
	case "help", "?":
		return &command.Result{Content: c.Info().Help}, nil
	default:
		return nil, fmt.Errorf("未知的 /faq 子命令 %q；发送 /faq help 查看用法", name)
	}
}

func splitCommandArgs(args string) (string, string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", ""
	}
	if index := strings.IndexAny(args, " 	\n"); index >= 0 {
		return args[:index], strings.TrimSpace(args[index+1:])
	}
	return args, ""
}

func (c knowledgeCommand) list(ctx context.Context, query string) (*command.Result, error) {
	entries, err := c.deps.GroupKnowledge.GroupKnowledgeList(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("群知识库（%d 条）\n", len(entries)))
	matched := 0
	for _, entry := range entries {
		if query != "" && !knowledgeEntryContains(entry, query) {
			continue
		}
		matched++
		mode := entry.Match
		if mode == "" {
			mode = "exact"
		}
		sb.WriteString(fmt.Sprintf("- %s [%s] %s\n", entry.ID, mode, previewKnowledgeText(entry.Question, 60)))
		if len(entry.Aliases) > 0 {
			sb.WriteString(fmt.Sprintf("  别名：%s\n", strings.Join(entry.Aliases, "、")))
		}
		if len(entry.Keywords) > 0 {
			sb.WriteString(fmt.Sprintf("  关键词：%s\n", strings.Join(entry.Keywords, "、")))
		}
		sb.WriteString(fmt.Sprintf("  答案：%s\n", previewKnowledgeText(entry.Answer, 80)))
	}
	if matched == 0 {
		if query == "" {
			return &command.Result{Content: "当前群还没有知识库条目。使用 /faq add 问题 => 答案 添加。"}, nil
		}
		return &command.Result{Content: fmt.Sprintf("没有匹配 %q 的知识条目。", strings.TrimSpace(query))}, nil
	}
	sb.WriteString("修改：/faq add、/faq remove <id>、/faq clear --confirm")
	return &command.Result{Content: strings.TrimRight(sb.String(), "\n")}, nil
}

func (c knowledgeCommand) add(ctx context.Context, rest, mode string) (*command.Result, error) {
	trigger, answer, err := parseKnowledgeAddArgs(rest)
	if err != nil {
		return nil, err
	}
	var question string
	var aliases, keywords []string
	switch mode {
	case "keywords":
		keywords = splitKnowledgeList(trigger)
		if len(keywords) == 0 {
			return nil, fmt.Errorf("关键词不能为空")
		}
		question = strings.Join(keywords, "、")
	default:
		parts := splitKnowledgeAliases(trigger)
		if len(parts) == 0 {
			return nil, fmt.Errorf("问题不能为空")
		}
		question = parts[0]
		aliases = parts[1:]
	}
	entry, err := c.deps.GroupKnowledge.GroupKnowledgeAdd(ctx, question, answer, mode, aliases, keywords)
	if err != nil {
		return nil, err
	}
	return &command.Result{Content: fmt.Sprintf("已添加知识 %s [%s]：%s", entry.ID, entry.Match, previewKnowledgeText(entry.Question, 60))}, nil
}

func (c knowledgeCommand) remove(ctx context.Context, id string) (*command.Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("请指定要删除的知识 id")
	}
	removed, err := c.deps.GroupKnowledge.GroupKnowledgeRemove(ctx, id)
	if err != nil {
		return nil, err
	}
	if !removed {
		return &command.Result{Content: fmt.Sprintf("没有找到知识 %q", id)}, nil
	}
	return &command.Result{Content: fmt.Sprintf("已删除知识 %s", id)}, nil
}

func (c knowledgeCommand) clear(ctx context.Context, rest string) (*command.Result, error) {
	if !strings.EqualFold(strings.TrimSpace(rest), "--confirm") {
		return &command.Result{Content: "请使用 /faq clear --confirm 确认清空当前群知识库。"}, nil
	}
	count, err := c.deps.GroupKnowledge.GroupKnowledgeClear(ctx)
	if err != nil {
		return nil, err
	}
	return &command.Result{Content: fmt.Sprintf("已清空 %d 条知识。", count)}, nil
}

func (c knowledgeCommand) test(ctx context.Context, text string) (*command.Result, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("请输入要测试的文本")
	}
	entry, ok, err := c.deps.GroupKnowledge.GroupKnowledgeTest(ctx, text)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &command.Result{Content: "未匹配到任何知识条目。"}, nil
	}
	return &command.Result{Content: fmt.Sprintf("匹配 %s [%s]：%s\n答案：%s", entry.ID, entry.Match, previewKnowledgeText(entry.Question, 60), previewKnowledgeText(entry.Answer, 120))}, nil
}

func (c knowledgeCommand) setEnabled(ctx context.Context, enabled bool) (*command.Result, error) {
	if c.deps.GroupPolicy == nil {
		return nil, fmt.Errorf("group policy service is not configured")
	}
	value := "off"
	if enabled {
		value = "on"
	}
	content, err := c.deps.GroupPolicy.SetGroupPolicy(ctx, "knowledge", value)
	if err != nil {
		return nil, err
	}
	return &command.Result{Content: content}, nil
}

func parseKnowledgeAddArgs(rest string) (string, string, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", fmt.Errorf("用法：/faq add <问题> => <答案>")
	}
	parts := strings.SplitN(rest, "=>", 2)
	if len(parts) != 2 {
		parts = strings.SplitN(rest, "＝>", 2)
	}
	if len(parts) != 2 {
		return "", "", fmt.Errorf("请使用 => 分隔问题和答案")
	}
	trigger := strings.TrimSpace(parts[0])
	answer := strings.TrimSpace(parts[1])
	if trigger == "" || answer == "" {
		return "", "", fmt.Errorf("问题和答案都不能为空")
	}
	return trigger, answer, nil
}

func splitKnowledgeAliases(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '|' || r == '｜'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key := strings.ToLower(field)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, field)
	}
	return out
}

func splitKnowledgeList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；':
			return true
		default:
			return false
		}
	})
	return splitKnowledgeAliases(strings.Join(fields, "|"))
}

func knowledgeEntryContains(entry config.GroupKnowledgeEntry, query string) bool {
	values := []string{entry.ID, entry.Question, entry.Answer}
	values = append(values, entry.Aliases...)
	values = append(values, entry.Keywords...)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func previewKnowledgeText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return value
}
