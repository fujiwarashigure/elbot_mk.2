package agent

import (
	"strings"
	"testing"
	"time"
)

func TestRiskConfirmationPromptUsesConfiguredPrefix(t *testing.T) {
	prompt := riskConfirmationPromptText("/*", 10*time.Minute)
	for _, want := range []string{"/*detail", "/*confirm", "/*confirmtool", "/*confirmall", "/*reject", "/*stop", "/*detail 可重新计时"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q missing %q", prompt, want)
		}
	}
	if strings.Contains(prompt, " /confirm") || strings.Contains(prompt, "，/detail") {
		t.Fatalf("prompt still contains legacy prefix: %q", prompt)
	}

	waiting := riskConfirmationWaitingText("/*")
	if !strings.Contains(waiting, "/*confirm") {
		t.Fatalf("waiting text %q missing /*confirm", waiting)
	}
}
