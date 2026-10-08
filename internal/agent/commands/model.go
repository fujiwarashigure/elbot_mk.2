package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/command"
	"elbot/internal/storage"
)

func NewModel(deps Deps) command.Handler {
	return modelCommand{deps: deps}
}

type modelCommand struct {
	deps Deps
}

func (c modelCommand) Info() command.Info {
	return modelCommandInfo()
}

func modelCommandInfo() command.Info {
	return command.Info{
		Name:        "model",
		Usage:       "/model [--profiles|--snapshots|--save <名字>|--apply <名字>|--delete <名字>|--chat|--work|--elwisp1|--elwisp2|--elwisp3|--compact|--naming] <name or number>",
		Description: "Switch model for current or specified mode.",
		Help: strings.TrimSpace(`Options:
  --profiles           List named model selections (@model:<name>).
  --snapshots          List saved named model snapshots.
  --save <name>        Save the current model selections under <name>.
  --apply <name>       Switch every slot back to the saved snapshot.
  --delete <name>      Delete a saved snapshot.
  --chat <model>       Switch chat mode model.
  --work <model>       Switch work mode model.
  --elwisp1 <model>    Switch Elnis elwisp1 model slot.
  --elwisp2 <model>    Switch Elnis elwisp2 model slot.
  --elwisp3 <model>    Switch Elnis elwisp3 model slot.
  --compact <model>    Switch context compact model.
  --naming <model>     Switch session naming model.

Without a target option, /model switches the current session mode model.
Model can be a list number, model name, or provider/model.

Examples:
  /model 2
  /model --profiles
  /model --snapshots
  /model --save cheap
  /model --apply cheap
  /model --delete cheap
  /model --chat gpt-4o
  /model --work openai/gpt-4.1
  /model --elwisp2 openai/gpt-4.1
  /model --compact claude-3-5-haiku
  /model --naming gpt-4o-mini`),
	}
}

func (c modelCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	info := c.Info()
	deps := c.deps
	if wantsCommandHelp(req.Args) {
		return formatCommandHelp(req.Prefix, info), nil
	}
	args, target, err := parseModelArgs(req.Prefix, req.Args)
	if err != nil {
		return nil, err
	}
	if target == modelTargetProfiles {
		if deps.Models == nil {
			return nil, fmt.Errorf("model service unavailable")
		}
		return formatModelProfiles(deps.Models.ModelProfiles()), nil
	}
	switch target {
	case modelTargetSnapshots:
		if deps.Models == nil {
			return nil, fmt.Errorf("model service unavailable")
		}
		return formatModelSnapshots(deps.Models.ModelSnapshots()), nil
	case modelTargetSave:
		if deps.Models == nil {
			return nil, fmt.Errorf("model service unavailable")
		}
		if err := deps.Models.SaveModelSnapshot(args); err != nil {
			return nil, err
		}
		return &command.Result{Content: fmt.Sprintf("saved model snapshot: %s", args)}, nil
	case modelTargetApply:
		if deps.Models == nil {
			return nil, fmt.Errorf("model service unavailable")
		}
		applied, err := deps.Models.ApplyModelSnapshot(args)
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: "applied model snapshot:\n" + formatModelSnapshotSlots(applied)}, nil
	case modelTargetDelete:
		if deps.Models == nil {
			return nil, fmt.Errorf("model service unavailable")
		}
		if err := deps.Models.DeleteModelSnapshot(args); err != nil {
			return nil, err
		}
		return &command.Result{Content: fmt.Sprintf("deleted model snapshot: %s", args)}, nil
	}
	var selected ModelOption
	switch target {
	case modelTargetChat:
		selected, err = deps.Models.SelectModelForMode(storage.SessionModeChat, args)
	case modelTargetWork:
		selected, err = deps.Models.SelectModelForMode(storage.SessionModeWork, args)
	case modelTargetElwisp1, modelTargetElwisp2, modelTargetElwisp3:
		selected, err = deps.Models.SelectModelForMode(string(target), args)
	case modelTargetCompact:
		selected, err = deps.Models.SelectCompactModel(args)
	case modelTargetNaming:
		selected, err = deps.Models.SelectNamingModel(args)
	default:
		selected, err = deps.Models.SelectModel(ctx, args)
	}
	if err != nil {
		return nil, err
	}
	switch target {
	case modelTargetChat:
		return &command.Result{Content: fmt.Sprintf("switched chat model: %s/%s", selected.Provider, selected.Model)}, nil
	case modelTargetWork:
		return &command.Result{Content: fmt.Sprintf("switched work model: %s/%s", selected.Provider, selected.Model)}, nil
	case modelTargetElwisp1, modelTargetElwisp2, modelTargetElwisp3:
		return &command.Result{Content: fmt.Sprintf("switched %s model: %s/%s", target, selected.Provider, selected.Model)}, nil
	case modelTargetCompact:
		return &command.Result{Content: fmt.Sprintf("switched compact model: %s/%s", selected.Provider, selected.Model)}, nil
	case modelTargetNaming:
		return &command.Result{Content: fmt.Sprintf("switched naming model: %s/%s", selected.Provider, selected.Model)}, nil
	default:
		return &command.Result{Content: fmt.Sprintf("switched to model: %s/%s", selected.Provider, selected.Model)}, nil
	}
}

// formatModelProfiles 列出命名模型选择（services.toml 的 model_profiles / model_aliases）。
// 失效的 profile 仍然列出但标注 unavailable：它写对了名字却指向当前进程缺少客户端的
// provider，直接隐藏会让操作者以为是自己名字写错。
func formatModelProfiles(profiles []ModelProfile) *command.Result {
	if len(profiles) == 0 {
		return &command.Result{Content: "no named model profiles configured (services.toml: model_profiles / model_aliases)"}
	}
	var sb strings.Builder
	sb.WriteString("named model profiles (@model:<name>):\n")
	for _, profile := range profiles {
		status := "unavailable"
		if profile.Available {
			status = "available"
		}
		sb.WriteString(fmt.Sprintf("  %s -> %s/%s (%s)\n", profile.Name, profile.Provider, profile.Model, status))
	}
	return &command.Result{Content: trimTrailingNewlines(sb.String())}
}

// formatModelSnapshots 列出已保存的命名快照。空列表也要说明怎么创建，
// 否则用户看到一个空回复不知道下一步做什么。
func formatModelSnapshots(snapshots []ModelSnapshot) *command.Result {
	if len(snapshots) == 0 {
		return &command.Result{Content: "no saved model snapshots (use `/model --save <name>` to save the current selections)"}
	}
	var sb strings.Builder
	sb.WriteString("saved model snapshots:\n")
	for _, snapshot := range snapshots {
		sb.WriteString(fmt.Sprintf("  %s\n", snapshot.Name))
		for _, line := range strings.Split(formatModelSnapshotSlots(snapshot), "\n") {
			sb.WriteString("    " + line + "\n")
		}
	}
	return &command.Result{Content: trimTrailingNewlines(sb.String())}
}

func formatModelSnapshotSlots(snapshot ModelSnapshot) string {
	if len(snapshot.Slots) == 0 {
		return "(empty snapshot)"
	}
	lines := make([]string, 0, len(snapshot.Slots))
	for _, slot := range snapshot.Slots {
		lines = append(lines, fmt.Sprintf("%s -> %s/%s", slot.Label, slot.Provider, slot.Model))
	}
	return strings.Join(lines, "\n")
}

func (c modelCommand) Complete(ctx context.Context, req command.CompletionRequest) []command.Completion {
	_ = ctx
	cursor := req.Cursor
	if cursor <= 0 || cursor > len(req.Raw) {
		cursor = len(req.Raw)
	}
	tokenStart := cursor
	for tokenStart > 0 && req.Raw[tokenStart-1] != ' ' && req.Raw[tokenStart-1] != '\t' {
		tokenStart--
	}
	query := req.Raw[tokenStart:cursor]
	if strings.HasPrefix(query, "-") {
		return completeModelOptions(query, tokenStart, cursor)
	}
	// --apply / --delete 后面跟的是快照名，不是模型名：这里补全已保存的快照，
	// 免得用户必须先去 --snapshots 里抄名字。
	if previous := previousCompletionToken(req.Args, query); previous == "--apply" || previous == "--delete" {
		return c.completeModelSnapshotNames(query, tokenStart, cursor)
	}
	if optionOnlyModelArgs(req.Args) || c.deps.Models == nil {
		return nil
	}
	result := c.deps.Models.ModelList(query, ModelListOptions{})
	items := result.Options
	if len(items) == 0 {
		items = c.fuzzyModelOptions(query)
	}
	out := make([]command.Completion, 0, len(items))
	seen := map[string]bool{}
	for _, model := range items {
		text := model.Provider + "/" + model.Model
		if seen[text] {
			continue
		}
		seen[text] = true
		out = append(out, command.Completion{Text: text, Label: model.Model, Description: model.Provider, Kind: "model", ReplaceStart: tokenStart, ReplaceEnd: cursor})
	}
	return out
}

func (c modelCommand) fuzzyModelOptions(query string) []ModelOption {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || c.deps.Models == nil {
		return nil
	}
	items := c.deps.Models.ModelList("", ModelListOptions{}).Options
	out := make([]ModelOption, 0, len(items))
	for _, model := range items {
		providerModel := strings.ToLower(model.Provider + "/" + model.Model)
		if fuzzySubsequenceMatch(providerModel, query) || fuzzySubsequenceMatch(strings.ToLower(model.Model), query) || fuzzySubsequenceMatch(strings.ToLower(model.Provider), query) {
			out = append(out, model)
		}
	}
	return out
}

// previousCompletionToken 返回当前正在补全的 token 之前的那个 token（用来判断
// "--apply" / "--delete" 这类需要参数的命令，参数是不是快照名）。
func previousCompletionToken(args, query string) string {
	trimmed := strings.TrimSuffix(args, query)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func (c modelCommand) completeModelSnapshotNames(query string, start, end int) []command.Completion {
	if c.deps.Models == nil {
		return nil
	}
	snapshots := c.deps.Models.ModelSnapshots()
	out := make([]command.Completion, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !strings.HasPrefix(snapshot.Name, query) {
			continue
		}
		parts := make([]string, 0, len(snapshot.Slots))
		for _, slot := range snapshot.Slots {
			parts = append(parts, slot.Label+"="+slot.Provider+"/"+slot.Model)
		}
		out = append(out, command.Completion{Text: snapshot.Name, Label: snapshot.Name, Description: strings.Join(parts, ", "), Kind: "model_snapshot", ReplaceStart: start, ReplaceEnd: end})
	}
	return out
}

func fuzzySubsequenceMatch(value, query string) bool {
	if query == "" {
		return true
	}
	value = strings.ToLower(value)
	query = strings.ToLower(query)
	j := 0
	for i := 0; i < len(value) && j < len(query); i++ {
		if value[i] == query[j] {
			j++
		}
	}
	return j == len(query)
}

func completeModelOptions(query string, start, end int) []command.Completion {
	options := []struct {
		Text        string
		Description string
	}{
		{"--profiles", "List named model selections"},
		{"--snapshots", "List saved model snapshots"},
		{"--save", "Save current model selections as a snapshot"},
		{"--apply", "Apply a saved model snapshot"},
		{"--delete", "Delete a saved model snapshot"},
		{"--chat", "Switch chat mode model"},
		{"--work", "Switch work mode model"},
		{"--elwisp1", "Switch Elnis elwisp1 model slot"},
		{"--elwisp2", "Switch Elnis elwisp2 model slot"},
		{"--elwisp3", "Switch Elnis elwisp3 model slot"},
		{"--compact", "Switch context compact model"},
		{"--naming", "Switch session naming model"},
		{"-c", "Switch context compact model"},
		{"-n", "Switch session naming model"},
	}
	out := []command.Completion{}
	for _, option := range options {
		if strings.HasPrefix(option.Text, query) {
			out = append(out, command.Completion{Text: option.Text, Label: option.Text, Description: option.Description, Kind: "model_option", ReplaceStart: start, ReplaceEnd: end})
		}
	}
	return out
}

func optionOnlyModelArgs(args string) bool {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return false
	}
	last := fields[len(fields)-1]
	return last == "--profiles" || last == "--snapshots" || last == "--save" || last == "--apply" || last == "--delete" || last == "--chat" || last == "--work" || last == "--elwisp1" || last == "--elwisp2" || last == "--elwisp3" || last == "--compact" || last == "--naming" || last == "-c" || last == "-n"
}

func NewCheckModel(deps Deps) command.Handler {
	return command.NewFunc(command.Info{
		Name:        "checkmodel",
		Usage:       "/checkmodel [--fresh|--refresh] [query]",
		Description: "List or search available models.",
		Aliases:     []string{"models"},
		Help: strings.TrimSpace(`Options:
  --fresh, --refresh    Refresh provider model lists before showing results.

Examples:
  /models
  /models claude
  /models --fresh
  /models --refresh`),
	}, func(ctx context.Context, req command.Request) (*command.Result, error) {
		args, fresh := parseModelListArgs(req.Args)
		result := deps.Models.ModelList(args, ModelListOptions{Fresh: fresh})

		models := result.Options
		if len(models) == 0 {
			var sb strings.Builder
			if strings.TrimSpace(args) != "" {
				sb.WriteString(fmt.Sprintf("no models matching %q", strings.TrimSpace(args)))
			} else {
				sb.WriteString("no models available")
			}
			appendModelProviderErrors(&sb, result.Errors)
			return &command.Result{Content: trimTrailingNewlines(sb.String())}, nil
		}

		var sb strings.Builder
		sb.WriteString("available models:\n")
		currentProvider := ""
		for _, m := range models {
			if m.Provider != currentProvider {
				if currentProvider != "" {
					sb.WriteString("\n")
				}
				currentProvider = m.Provider
				sb.WriteString(fmt.Sprintf("%s:\n", currentProvider))
			}
			marker := " "
			if m.Current || m.ChatCurrent || m.WorkCurrent || m.Compact || m.Naming {
				marker = "*"
			}
			suffix := modelSuffix(m)
			if !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("  %s [%d] %s%s", marker, m.Index, m.Model, suffix))
		}
		appendModelProviderErrors(&sb, result.Errors)
		return &command.Result{Content: trimTrailingNewlines(sb.String())}, nil
	})
}

func appendModelProviderErrors(sb *strings.Builder, errors []ModelProviderError) {
	hasError := false
	for _, providerErr := range errors {
		if providerErr.Err == nil {
			continue
		}
		if !hasError {
			sb.WriteString("\nmodel provider errors:")
			hasError = true
		}
		sb.WriteString(fmt.Sprintf("\n  - %s: %v", providerErr.Provider, providerErr.Err))
	}
}

func modelSuffix(m ModelOption) string {
	marks := append([]string{}, m.ModeMarks...)
	if len(marks) == 0 {
		if m.ChatCurrent {
			marks = append(marks, "chat")
		}
		if m.WorkCurrent {
			marks = append(marks, "work")
		}
	}
	if m.Compact {
		marks = append(marks, "compact")
	}
	if m.Naming {
		marks = append(marks, "naming")
	}
	if len(marks) == 0 {
		return ""
	}
	return " (" + strings.Join(marks, ", ") + ")"
}

func parseModelListArgs(args string) (string, bool) {
	fields := strings.Fields(args)
	out := []string{}
	fresh := false
	for _, field := range fields {
		if field == "--fresh" || field == "--refresh" {
			fresh = true
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " "), fresh
}

type modelTarget string

const (
	modelTargetCurrent   modelTarget = "current"
	modelTargetProfiles  modelTarget = "profiles"
	modelTargetSnapshots modelTarget = "snapshots"
	modelTargetSave      modelTarget = "save"
	modelTargetApply     modelTarget = "apply"
	modelTargetDelete    modelTarget = "delete"
	modelTargetChat      modelTarget = "chat"
	modelTargetWork      modelTarget = "work"
	modelTargetElwisp1   modelTarget = "elwisp1"
	modelTargetElwisp2   modelTarget = "elwisp2"
	modelTargetElwisp3   modelTarget = "elwisp3"
	modelTargetCompact   modelTarget = "compact"
	modelTargetNaming    modelTarget = "naming"
)

func parseModelArgs(prefix, args string) (string, modelTarget, error) {
	fields := strings.Fields(args)
	out := []string{}
	target := modelTargetCurrent
	for _, field := range fields {
		nextTarget := modelTarget("")
		switch field {
		case "--profiles":
			nextTarget = modelTargetProfiles
		case "--snapshots":
			nextTarget = modelTargetSnapshots
		case "--save":
			nextTarget = modelTargetSave
		case "--apply":
			nextTarget = modelTargetApply
		case "--delete":
			nextTarget = modelTargetDelete
		case "--chat":
			nextTarget = modelTargetChat
		case "--work":
			nextTarget = modelTargetWork
		case "--elwisp1":
			nextTarget = modelTargetElwisp1
		case "--elwisp2":
			nextTarget = modelTargetElwisp2
		case "--elwisp3":
			nextTarget = modelTargetElwisp3
		case "--compact", "-c":
			nextTarget = modelTargetCompact
		case "--naming", "-n":
			nextTarget = modelTargetNaming
		}
		if nextTarget != "" {
			if target != modelTargetCurrent {
				return "", "", fmt.Errorf("usage: %smodel [--profiles|--snapshots|--save <name>|--apply <name>|--delete <name>|--chat|--work|--elwisp1|--elwisp2|--elwisp3|--compact|--naming] <name or number>", prefix)
			}
			target = nextTarget
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " "), target, nil
}

type ModelModule struct{}

func (ModelModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return RegisterFactories(registrar, deps,
		NewModel,
		NewCheckModel,
	)
}
