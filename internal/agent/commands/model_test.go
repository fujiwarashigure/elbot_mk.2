package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"elbot/internal/command"
)

type fakeModelService struct {
	models    []ModelOption
	profiles  []ModelProfile
	snapshots []ModelSnapshot
	// snapshotErr / applyErr / deleteErr 让测试覆盖失败路径；applyResult 是应用成功时
	// 返回的视图，便于断言命令回复的内容。
	snapshotErr error
	applyErr    error
	deleteErr   error
	applyResult ModelSnapshot
	savedName   string
	appliedName string
	deletedName string
}

func (s fakeModelService) ModelProfiles() []ModelProfile { return s.profiles }

func (s fakeModelService) ModelSnapshots() []ModelSnapshot { return s.snapshots }

func (s *fakeModelService) SaveModelSnapshot(name string) error {
	if s.snapshotErr != nil {
		return s.snapshotErr
	}
	s.savedName = name
	return nil
}

func (s *fakeModelService) ApplyModelSnapshot(name string) (ModelSnapshot, error) {
	if s.applyErr != nil {
		return ModelSnapshot{}, s.applyErr
	}
	s.appliedName = name
	return s.applyResult, nil
}

func (s *fakeModelService) DeleteModelSnapshot(name string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletedName = name
	return nil
}

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
	completer := NewModel(Deps{Models: &fakeModelService{}}).(command.Completer)
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
	completer := NewModel(Deps{Models: &fakeModelService{}}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --pro", Prefix: "/", Name: "model", Args: "--pro", Cursor: len("/model --pro")})
	if len(got) != 1 || got[0].Text != "--profiles" {
		t.Fatalf("profile option completion = %#v", got)
	}
}

func TestModelCommandDoesNotCompleteModelNamesAfterProfiles(t *testing.T) {
	models := &fakeModelService{models: []ModelOption{{Provider: "openai", Model: "gpt-4o"}}}
	completer := NewModel(Deps{Models: models}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --profiles", Prefix: "/", Name: "model", Args: "--profiles", Cursor: len("/model --profiles")})
	if len(got) != 1 || got[0].Text != "--profiles" {
		t.Fatalf("Complete after --profiles = %#v", got)
	}
}

func TestModelCommandListsProfiles(t *testing.T) {
	models := &fakeModelService{profiles: []ModelProfile{
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
	result, err := NewModel(Deps{Models: &fakeModelService{}}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--profiles"})
	if err != nil {
		t.Fatalf("Handle --profiles: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "no named model profiles") {
		t.Fatalf("result = %#v", result)
	}
}

func TestModelCommandSavesSnapshot(t *testing.T) {
	models := &fakeModelService{}
	result, err := NewModel(Deps{Models: models}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--save cheap"})
	if err != nil {
		t.Fatalf("Handle --save: %v", err)
	}
	if models.savedName != "cheap" {
		t.Fatalf("saved name = %q", models.savedName)
	}
	if result == nil || !strings.Contains(result.Content, "saved model snapshot: cheap") {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Content, "switched") {
		t.Fatalf("--save must not switch a model: %q", result.Content)
	}
}

func TestModelCommandSaveRequiresName(t *testing.T) {
	models := &fakeModelService{snapshotErr: errors.New("快照名不能为空")}
	if _, err := NewModel(Deps{Models: models}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--save"}); err == nil {
		t.Fatal("--save without a name must fail")
	}
}

func TestModelCommandAppliesSnapshot(t *testing.T) {
	models := &fakeModelService{applyResult: ModelSnapshot{Name: "cheap", Slots: []ModelSnapshotSlot{
		{Label: "chat", Provider: "openai", Model: "gpt-4o-mini"},
		{Label: "work", Provider: "deepseek", Model: "deepseek-chat"},
	}}}
	result, err := NewModel(Deps{Models: models}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--apply cheap"})
	if err != nil {
		t.Fatalf("Handle --apply: %v", err)
	}
	if models.appliedName != "cheap" {
		t.Fatalf("applied name = %q", models.appliedName)
	}
	if result == nil || !strings.Contains(result.Content, "chat -> openai/gpt-4o-mini") || !strings.Contains(result.Content, "work -> deepseek/deepseek-chat") {
		t.Fatalf("result = %#v", result)
	}
}

func TestModelCommandApplyReportsFailure(t *testing.T) {
	models := &fakeModelService{applyErr: errors.New(`没有名为 "nope" 的模型快照`)}
	if _, err := NewModel(Deps{Models: models}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--apply nope"}); err == nil {
		t.Fatal("--apply of a missing snapshot must fail")
	}
}

func TestModelCommandDeletesSnapshot(t *testing.T) {
	models := &fakeModelService{}
	result, err := NewModel(Deps{Models: models}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--delete cheap"})
	if err != nil {
		t.Fatalf("Handle --delete: %v", err)
	}
	if models.deletedName != "cheap" {
		t.Fatalf("deleted name = %q", models.deletedName)
	}
	if result == nil || !strings.Contains(result.Content, "deleted model snapshot: cheap") {
		t.Fatalf("result = %#v", result)
	}
}

func TestModelCommandListsSnapshots(t *testing.T) {
	models := &fakeModelService{snapshots: []ModelSnapshot{
		{Name: "cheap", Slots: []ModelSnapshotSlot{{Label: "chat", Provider: "openai", Model: "gpt-4o-mini"}}},
		{Name: "strong", Slots: []ModelSnapshotSlot{{Label: "work", Provider: "anthropic", Model: "claude-sonnet"}}},
	}}
	result, err := NewModel(Deps{Models: models}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--snapshots"})
	if err != nil {
		t.Fatalf("Handle --snapshots: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "cheap") || !strings.Contains(result.Content, "strong") {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.Content, "chat -> openai/gpt-4o-mini") {
		t.Fatalf("snapshot slots missing: %q", result.Content)
	}
	if strings.Contains(result.Content, "switched") {
		t.Fatalf("--snapshots must not switch a model: %q", result.Content)
	}
}

func TestModelCommandListsSnapshotsWhenEmpty(t *testing.T) {
	result, err := NewModel(Deps{Models: &fakeModelService{}}).Handle(context.Background(), command.Request{Prefix: "/", Name: "model", Args: "--snapshots"})
	if err != nil {
		t.Fatalf("Handle --snapshots: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "no saved model snapshots") {
		t.Fatalf("result = %#v", result)
	}
}

func TestModelCommandCompletesSnapshotNamesAfterApply(t *testing.T) {
	models := &fakeModelService{snapshots: []ModelSnapshot{
		{Name: "cheap", Slots: []ModelSnapshotSlot{{Label: "chat", Provider: "openai", Model: "gpt-4o-mini"}}},
		{Name: "strong", Slots: []ModelSnapshotSlot{{Label: "work", Provider: "anthropic", Model: "claude-sonnet"}}},
	}}
	completer := NewModel(Deps{Models: models}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --apply ch", Prefix: "/", Name: "model", Args: "--apply ch", Cursor: len("/model --apply ch")})
	if len(got) != 1 || got[0].Text != "cheap" || got[0].Kind != "model_snapshot" {
		t.Fatalf("snapshot completion = %#v", got)
	}
	if got[0].ReplaceStart != len("/model --apply ") {
		t.Fatalf("replace range = %d..%d", got[0].ReplaceStart, got[0].ReplaceEnd)
	}
	if !strings.Contains(got[0].Description, "chat=openai/gpt-4o-mini") {
		t.Fatalf("description = %q", got[0].Description)
	}
}

// --apply 后面补的是快照名，不是模型名：有模型可选时也不能混进来。
func TestModelCommandDoesNotCompleteModelNamesAfterApply(t *testing.T) {
	models := &fakeModelService{
		models:    []ModelOption{{Provider: "openai", Model: "gpt-4o"}},
		snapshots: []ModelSnapshot{{Name: "cheap", Slots: []ModelSnapshotSlot{{Label: "chat", Provider: "openai", Model: "gpt-4o"}}}},
	}
	completer := NewModel(Deps{Models: models}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --apply ", Prefix: "/", Name: "model", Args: "--apply ", Cursor: len("/model --apply ")})
	if len(got) != 1 || got[0].Text != "cheap" {
		t.Fatalf("completion after --apply = %#v", got)
	}
}

func TestModelCommandSnapshotOptionsAreCompleted(t *testing.T) {
	completer := NewModel(Deps{Models: &fakeModelService{}}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --sna", Prefix: "/", Name: "model", Args: "--sna", Cursor: len("/model --sna")})
	if len(got) != 1 || got[0].Text != "--snapshots" {
		t.Fatalf("snapshot option completion = %#v", got)
	}
}

func TestModelCommandCompletesElwispOptions(t *testing.T) {
	completer := NewModel(Deps{Models: &fakeModelService{}}).(command.Completer)
	got := completer.Complete(context.Background(), command.CompletionRequest{Raw: "/model --elw", Prefix: "/", Name: "model", Args: "--elw", Cursor: len("/model --elw")})
	if len(got) != 3 {
		t.Fatalf("Complete elwisp options = %#v", got)
	}
	if got[0].Text != "--elwisp1" || got[1].Text != "--elwisp2" || got[2].Text != "--elwisp3" {
		t.Fatalf("elwisp options = %#v", got)
	}
}

func TestModelCommandCompletesModelNames(t *testing.T) {
	models := &fakeModelService{models: []ModelOption{{Provider: "openai", Model: "gpt-4o"}, {Provider: "anthropic", Model: "claude-sonnet"}}}
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
	models := &fakeModelService{models: []ModelOption{{Provider: "openai", Model: "gpt-4o"}, {Provider: "anthropic", Model: "claude-sonnet"}}}
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
	models := &fakeModelService{models: []ModelOption{{Provider: "deepseek", Model: "deepseek-v3"}, {Provider: "openai", Model: "gpt-4o"}}}
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
	models := &fakeModelService{models: []ModelOption{{Provider: "openai", Model: "gpt-4o"}}}
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
