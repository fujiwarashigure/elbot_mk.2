package commands

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/command"
	"elbot/internal/security"
)

type fakeContextPolicy struct {
	status      string
	setTarget   string
	setValue    string
	resetTarget string
}

func (f *fakeContextPolicy) ContextPolicyStatus(context.Context) string { return f.status }

func (f *fakeContextPolicy) SetContextPolicy(_ context.Context, target, value string) (string, error) {
	f.setTarget = target
	f.setValue = value
	return "set " + target + "=" + value, nil
}

func (f *fakeContextPolicy) ResetContextPolicy(_ context.Context, target string) (string, error) {
	f.resetTarget = target
	return "reset " + target, nil
}

func TestContextPolicyCommand(t *testing.T) {
	service := &fakeContextPolicy{status: "current policy"}
	handler := NewContextPolicy(Deps{ContextPolicy: service})
	if handler.Info().MinRole != security.RoleSuperadmin || !handler.Info().AllowGroupAdmin {
		t.Fatalf("unexpected command info: %#v", handler.Info())
	}

	result, err := handler.Handle(context.Background(), command.Request{Args: ""})
	if err != nil || result.Content != "current policy" {
		t.Fatalf("show result = %#v, %v", result, err)
	}

	result, err = handler.Handle(context.Background(), command.Request{Args: "--work summarize"})
	if err != nil || !strings.Contains(result.Content, "work=summarize") {
		t.Fatalf("set result = %#v, %v", result, err)
	}
	if service.setTarget != "work" || service.setValue != "summarize" {
		t.Fatalf("set call = %q %q", service.setTarget, service.setValue)
	}

	result, err = handler.Handle(context.Background(), command.Request{Args: "reset --chat"})
	if err != nil || !strings.Contains(result.Content, "reset chat") {
		t.Fatalf("reset result = %#v, %v", result, err)
	}
	if service.resetTarget != "chat" {
		t.Fatalf("reset target = %q", service.resetTarget)
	}
}
