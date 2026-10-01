package health

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
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
			name: "openai style key",
			in:   "provider error: invalid key sk-abcdefghijklmnopqrstuvwxyz",
			want: "provider error: invalid key [REDACTED]",
		},
		{
			name: "url userinfo",
			in:   "webhook https://user:secret-pass@example.com/hook refused",
			want: "webhook https://[REDACTED]@example.com/hook refused",
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
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactSecrets(tc.in); got != tc.want {
				t.Fatalf("RedactSecrets(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStateSnapshotRedactsSecrets(t *testing.T) {
	state := NewState(Options{})
	state.MarkPlatformDisconnected("telegram", errors.New(`Post "https://api.telegram.org/bot123456789:AAHsecretTokenValue/getMe": dial tcp: i/o timeout`))
	state.RecordModelError("openai", errors.New("401 invalid api_key=sk-abcdefghijklmnopqrstuvwxyz"))
	state.SetLastRestartReason("restart: webhook https://user:secret-pass@example.com/hook failed")

	snapshot := state.Snapshot()
	var blob strings.Builder
	for _, platform := range snapshot.Platforms {
		blob.WriteString(platform.LastError)
	}
	for _, model := range snapshot.Models {
		blob.WriteString(model.LastError)
	}
	blob.WriteString(snapshot.LastRestartReason)
	text := blob.String()

	for _, secret := range []string{"AAHsecretTokenValue", "sk-abcdefghijklmnopqrstuvwxyz", "secret-pass"} {
		if strings.Contains(text, secret) {
			t.Fatalf("health snapshot leaks %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("health snapshot did not redact anything: %s", text)
	}
}
