package health

import (
	"regexp"
	"strings"
)

type secretRule struct {
	pattern     *regexp.Regexp
	replacement string
}

// secretRules 按顺序匹配错误信息里常见的凭据形态。顺序有意义：先处理
// 结构化前缀（URL、sk-、bot token），再处理 key=value 形式。
// 替换串里的 ${1} 只保留非敏感前缀，绝不能引用被匹配到的秘密本身。
var secretRules = []secretRule{
	// Telegram: https://api.telegram.org/bot<id>:<token>/method
	{regexp.MustCompile(`(?i)(https?://[^/\s]+/bot)\d+:[A-Za-z0-9_-]+`), "${1}[REDACTED]"},
	// URL 中的 userinfo，例如 https://user:pass@example.com/hook
	{regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`), "${1}[REDACTED]@"},
	// 常见带前缀的 API Key / Token
	{regexp.MustCompile(`(?:sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})`), "[REDACTED]"},
	// Authorization: Bearer <token>
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]{6,}`), "${1}[REDACTED]"},
	// key=value / "key": "value" 形式的凭据
	{regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?key|secret[_-]?key|client[_-]?secret|bot[_-]?token|refresh[_-]?token|access[_-]?token|auth[_-]?token|token|secret|password|passwd)["']?\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`), "${1}[REDACTED]"},
}

// RedactSecrets 去掉文本中常见的凭据，避免它们通过 /healthz、/metrics、
// /diagnostics 或日志泄露。它是尽力而为的第二道防线：只能降低泄露面，
// 不能替代把运维接口限制在回环 / 可信内网，也不替代最小权限的密钥管理。
func RedactSecrets(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	for _, rule := range secretRules {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value
}
