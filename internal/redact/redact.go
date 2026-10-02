// Package redact provides best-effort secret redaction shared by user-visible
// errors, hooks and logs. It is intentionally dependency-free so any package
// can use it without creating an import cycle.
package redact

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
)

type secretRule struct {
	pattern     *regexp.Regexp
	replacement string
}

// secretRules 按顺序匹配错误信息里常见的凭据形态。顺序有意义：先处理
// 结构化前缀（URL、sk-、bot token），再处理 key=value 形式。
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

// Secrets removes common credential shapes from value. It is a best-effort
// defense-in-depth measure; callers must still avoid putting secrets into
// errors and log attributes in the first place.
func Secrets(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	for _, rule := range secretRules {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value
}

// Error returns a redacted error string.
func Error(err error) string {
	if err == nil {
		return ""
	}
	return Secrets(err.Error())
}

// Summarize redacts and truncates value for user-visible notices. The limit is
// measured in runes so Chinese text is not split mid-codepoint.
func Summarize(value string, limit int) string {
	if limit <= 0 {
		limit = 400
	}
	value = Secrets(strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "...（已截断）"
}

// NewErrorID returns a random UUID-like identifier suitable for correlating a
// user-visible error with logs/audit entries.
func NewErrorID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "error-unknown"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
