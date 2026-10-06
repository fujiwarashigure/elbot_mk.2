package agent

import "strings"

// commandPrefix 返回当前配置的主命令前缀，用于拼接提示文案和识别特殊命令。
// 未配置或 Router 缺失时回退到默认前缀。
func (a *Agent) commandPrefix() string {
	if a != nil && a.commands != nil {
		if prefix := strings.TrimSpace(a.commands.PrimaryPrefix()); prefix != "" {
			return prefix
		}
	}
	return "/"
}

// commandPrefixes 返回当前配置的全部命令前缀，用于识别带 @机器人后缀的命令。
// 未配置或 Router 缺失时回退到 "/"，与历史行为保持一致。
func (a *Agent) commandPrefixes() []string {
	if a != nil && a.commands != nil {
		if prefixes := a.commands.Prefixes(); len(prefixes) > 0 {
			return prefixes
		}
	}
	return []string{"/"}
}

// isCancelCommand 判断输入是否是 Hook 会话取消命令（<prefix>cancel）。
func (a *Agent) isCancelCommand(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	return text == a.commandPrefix()+"cancel"
}
