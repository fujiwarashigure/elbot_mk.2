package commands

import (
	"fmt"
	"strings"

	"elbot/internal/command"
)

func wantsCommandHelp(args string) bool {
	arg := strings.TrimSpace(args)
	return arg == "--help" || arg == "-h"
}

func formatCommandHelp(prefix string, info command.Info) *command.Result {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("command: %s\n", info.Name))
	sb.WriteString(fmt.Sprintf("usage: %s\n", commandUsage(prefix, info)))
	if len(info.Aliases) > 0 {
		sb.WriteString(fmt.Sprintf("aliases: %s\n", strings.Join(info.Aliases, ", ")))
	}
	if info.Description != "" {
		sb.WriteString(fmt.Sprintf("description: %s\n", info.Description))
	}
	if strings.TrimSpace(info.Help) != "" {
		sb.WriteString("\n")
		sb.WriteString(rewriteHelpPrefix(prefix, strings.TrimSpace(info.Help)))
		sb.WriteString("\n")
	}
	return &command.Result{Content: trimTrailingNewlines(sb.String())}
}

func trimTrailingNewlines(text string) string {
	return strings.TrimRight(text, "\n")
}

// rewriteHelpPrefix 把帮助文本中行首的 "/" 命令示例替换成配置的前缀。
func rewriteHelpPrefix(prefix, text string) string {
	if prefix == "" || prefix == "/" || text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "/") {
			indent := line[:len(line)-len(trimmed)]
			lines[i] = indent + prefix + strings.TrimPrefix(trimmed, "/")
		}
	}
	return strings.Join(lines, "\n")
}

func commandUsage(prefix string, info command.Info) string {
	usage := info.Usage
	if usage == "" {
		usage = prefix + info.Name
	}
	if prefix != "/" && strings.HasPrefix(usage, "/") {
		usage = prefix + strings.TrimPrefix(usage, "/")
	}
	return usage
}
