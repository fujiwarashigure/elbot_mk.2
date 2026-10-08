package logging

import (
	"log/slog"
	"testing"
)

func TestAuditFloorLevelKeepsDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  slog.Level
	}{
		{level: "debug", want: slog.LevelDebug},
		{level: "info", want: slog.LevelInfo},
		{level: "warn", want: slog.LevelInfo},
		{level: "error", want: slog.LevelInfo},
		{level: "", want: slog.LevelInfo},
	} {
		if got := parseLevel(auditFloorLevel(tc.level)); got != tc.want {
			t.Fatalf("auditFloorLevel(%q) parsed to %v, want %v", tc.level, got, tc.want)
		}
	}
}
