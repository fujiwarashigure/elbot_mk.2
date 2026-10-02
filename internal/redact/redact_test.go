package redact

import (
	"strings"
	"testing"
)

func TestSecretsRedactsCredentialShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "telegram bot token in url",
			in:   `Post "https://api.telegram.org/bot123456789:AAHsecretTokenValue/getUpdates": unexpected EOF`,
			want: `Post "https://api.telegram.org/bot[REDACTED]/getUpdates": unexpected EOF`,
		},
		{
			name: "bearer token",
			in:   "request failed: Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payloadx header",
			want: "request failed: Authorization: Bearer [REDACTED] header",
		},
		{
			name: "json credential",
			in:   `{"refresh_token":"json-secret-value","password": "pw-123456"}`,
			want: `{"refresh_token":[REDACTED],"password": [REDACTED]}`,
		},
		{
			name: "plain text untouched",
			in:   "platform telegram disconnected after 3 attempts",
			want: "platform telegram disconnected after 3 attempts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Secrets(tc.in); got != tc.want {
				t.Fatalf("Secrets(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSummarizeRedactsAndTruncates(t *testing.T) {
	value := strings.Repeat("中文", 300) + " api_key=sk-abcdefghijklmnopqrstuvwxyz"
	got := Summarize(value, 20)
	if strings.Contains(got, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("Summarize leaked secret: %q", got)
	}
	if !strings.Contains(got, "已截断") {
		t.Fatalf("Summarize did not mark truncation: %q", got)
	}
}

func TestNewErrorIDNonEmptyAndUnique(t *testing.T) {
	first := NewErrorID()
	second := NewErrorID()
	if first == "" || second == "" || first == second {
		t.Fatalf("NewErrorID() = %q, %q; want non-empty unique values", first, second)
	}
}
