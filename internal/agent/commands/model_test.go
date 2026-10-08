package commands

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/command"
)

type fakeModelService struct {
	models   []ModelOption
	profiles []ModelProfile
}

func (s fakeModelService) ModelProfiles() []ModelProfile { return s.profiles }

func (s fakeModelService) CurrentModel() string                   { return "" }
func (s fakeModelService) CurrentProvider() string                { return "" }
func (s fakeModelService) CurrentModeModel() ModelOption          { return ModelOption{} }
func (s fakeModelService) CurrentModelForMode(string) ModelOption { return ModelOption{} }
func (s fakeModelService) CurrentCompactModel() ModelOption       { return ModelOption{} }
func (s fakeModelService) CurrentNamingModel() ModelOption        { return ModelOption{} }
func (s fakeModelService) SelectModel(context.Context, string) (ModelOption, error) {
	return ModelOption{}, nil
}
func (s fakeModelService) SelectCompactModel(string) (ModelOption, error) { return ModelOption{}, nil }
func (s fakeModelService) SelectNamingModel(string) (ModelOption, error)  { return ModelOption{}, nil }
func (s fakeModelService) SelectModelForMode(string, string) (ModelOption, error) {
	return ModelOption{}, nil
}
func (s fakeModelService) Models(query string) []ModelOption {
	return s.ModelList(query, ModelListOptions{}).Options
}
func (s fakeModelService) ModelList(query string, opts ModelListOptions) ModelListResult {
	query = strings.ToLower(strings.TrimSpace(query))
	out := []ModelOption{}
	for _, model := range s.models {
		value := strings.ToLower(model.Provider + "/" + model.Model)
		if query == "" || strings.Contains(value, query) {
			out = append(out, model)
		}
	}
	return ModelListResult{Options: out}
}

func TestModelCommandCompletesOptions(t *testing.T) {
	completer := NewModel(Deps{Models: fakeModelService{}}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --", Prefix: "/", Name: "model", Args: "--", Cursor: len("/model --")})
	if len(got) < 7 {
		t.Fatalf("Complete options = %#v", got)
	}
	if got[0].Text != "--profiles" || got[0].Kind != "model_option" || got[0].ReplaceStart != len("/model ") {
		t.Fatalf("first option = %#v", got[0])
	}
	seen := map[string]bool{}
	for _, completion := range got {
		seen[completion.Text] = true
	}
	for _, want := range []string{"--profiles", "--chat", "--work", "--naming"} {
		if !seen[want] {
			t.Fatalf("option %q missing from %#v", want, got)
		}
	}
}

func TestModelCommandCompletesProfilesOption(t *testing.T) {
	completer := NewModel(Deps{Models: fakeModelService{}}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --pro", Prefix: "/", Name: "model", Args: "--pro", Cursor: len("/model --pro")})
	if len(got) != 1 || got[0].Text != "--profiles" {
		t.Fatalf("profile option completion = %#v", got)
	}
}

func TestModelCommandDoesNotCompleteModelNamesAfterProfiles(t *testing.T) {
	models := fakeModelService{models: []ModelOption{{Provider: "openai", Model: "gpt-4o"}}}
	completer := NewModel(Deps{Models: models}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --profiles", Prefix: "/", Name: "model", Args: "--profiles", Cursor: len("/model --profiles")})
	if len(got) != 1 || got[0].Text != "--profiles" {
		t.Fatalf("Complete after --profiles = %#v", got)
	}
}

func TestModelCommandListsProfiles(t *testing.T) {
	models := fakeModelService{profiles: []ModelProfile{
		{Name: "fast", Provider: "openai", Model: "gpt-4o", Available: true},
		{Name: "dead", Provider: "missing", Model: "ghost"},
	}}
	result, err := NewModel(Deps{Models: models}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--profiles"})
	if err != nil {
		t.Fatalf("Handle --profiles: %v", err)
	}
	if result == nil {
		t.Fatal("Handle --profiles returned nil result")
	}
	if !strings.Contains(result.Content, "fast -> openai/gpt-4o (available)") {
		t.Fatalf("available profile missing: %q", result.Content)
	}
	if !strings.Contains(result.Content, "dead -> missing/ghost (unavailable)") {
		t.Fatalf("unavailable profile missing: %q", result.Content)
	}
	if strings.Contains(result.Content, "switched") {
		t.Fatalf("--profiles must not switch a model: %q", result.Content)
	}
}

func TestModelCommandListsProfilesWhenEmpty(t *testing.T) {
	result, err := NewModel(Deps{Models: fakeModelService{}}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--profiles"})
	if err != nil {
		t.Fatalf("Handle --profiles: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "no named model profiles") {
		t.Fatalf("result = %#v", result)
	}
}

func TestModelCommandCompletesElwispOptions(t *testing.T) {
	completer := NewModel(Deps{Models: fakeModelService{}}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --elw", Prefix: "/", Name: "model", Args: "--elw", Cursor: len("/model --elw")})
	if len(got) != 3 {
		t.Fatalf("Complete elwisp options = %#v", got)
	}
	if got[0].Text != "--elwisp1" || got[1].Text != "--elwisp2" || got[2].Text != "--elwisp3" {
		t.Fatalf("elwisp options = %#v", got)
	}
}

func TestModelCommandCompletesModelNames(t *testing.T) {
	models := fakeModelService{models: []ModelOption{{Provider: "openai", Model: "gpt-4o"}, {Provider: "anthropic", Model: "claude-sonnet"}}}
	completer := NewModel(Deps{Models: models}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model gp", Prefix: "/", Name: "model", Args: "gp", Cursor: len("/model gp")})
	if len(got) != 1 {
		t.Fatalf("Complete models = %#v", got)
	}
	if got[0].Text != "openai/gpt-4o" || got[0].Label != "gpt-4o" || got[0].Description != "openai" || got[0].Kind != "model" {
		t.Fatalf("model completion = %#v", got[0])
	}
}

func TestModelCommandCompletesModelAfterTargetOption(t *testing.T) {
	models := fakeModelService{models: []ModelOption{{Provider: "openai", Model: "gpt-4o"}, {Provider: "anthropic", Model: "claude-sonnet"}}}
	completer := NewModel(Deps{Models: models}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --chat cla", Prefix: "/", Name: "model", Args: "--chat cla", Cursor: len("/model --chat cla")})
	if len(got) != 1 {
		t.Fatalf("Complete option models = %#v", got)
	}
	if got[0].Text != "anthropic/claude-sonnet" || got[0].ReplaceStart != len("/model --chat ") {
		t.Fatalf("option model completion = %#v", got[0])
	}
}

func TestModelCommandFuzzyCompletesAbbreviation(t *testing.T) {
	models := fakeModelService{models: []ModelOption{{Provider: "deepseek", Model: "deepseek-v3"}, {Provider: "openai", Model: "gpt-4o"}}}
	completer := NewModel(Deps{Models: models}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model dpsk", Prefix: "/", Name: "model", Args: "dpsk", Cursor: len("/model dpsk")})
	if len(got) != 1 {
		t.Fatalf("fuzzy completion = %#v", got)
	}
	if got[0].Text != "deepseek/deepseek-v3" {
		t.Fatalf("fuzzy completion text = %#v", got[0])
	}
}

func TestModelCommandDoesNotCompleteModelImmediatelyAfterOption(t *testing.T) {
	models := fakeModelService{models: []ModelOption{{Provider: "openai", Model: "gpt-4o"}}}
	completer := NewModel(Deps{Models: models}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --chat", Prefix: "/", Name: "model", Args: "--chat", Cursor: len("/model --chat")})
	if len(got) != 1 || got[0].Text != "--chat" {
		t.Fatalf("Complete option token = %#v", got)
	}
}

func TestModelCommandSwitchesElwispSlot(t *testing.T) {
	models := &recordingModelService{}
	result, err := NewModel(Deps{Models: models}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--elwisp2 openai/gpt-4.1"})
	if err != nil {
		t.Fatalf("Handle elwisp model: %v", err)
	}
	if models.mode != "elwisp2" || models.arg != "openai/gpt-4.1" {
		t.Fatalf("selected mode=%q arg=%q", models.mode, models.arg)
	}
	if result == nil || !strings.Contains(result.Content, "switched elwisp2 model") {
		t.Fatalf("result = %#v", result)
	}
}

func TestModelSuffixUsesModeMarks(t *testing.T) {
	got := modelSuffix(ModelOption{ModeMarks: []string{"work", "elwisp2"}, Compact: true})
	if got != " (work, elwisp2, compact)" {
		t.Fatalf("modelSuffix = %q", got)
	}
}

type recordingModelService struct {
	fakeModelService
	mode string
	arg  string
}

func (s *recordingModelService) SelectModelForMode(mode, arg string) (ModelOption, error) {
	s.mode = mode
	s.arg = arg
	return ModelOption{Provider: "openai", Model: "gpt-4.1"}, nil
}
