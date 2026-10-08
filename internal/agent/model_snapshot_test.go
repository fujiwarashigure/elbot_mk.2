package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcommands "elbot/internal/agent/commands"
	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/storage"
)

// newModelSnapshotTestAgent 造一个带 state.toml 的 Agent：两个 provider 都有 client，
// mode_models 里 work/chat 各一个模型。快照要落盘，所以必须有真实 statePath。
func newModelSnapshotTestAgent(t *testing.T) *Agent {
	t.Helper()
	a := New(&fakePlatform{}, &fakeLLM{}, "work-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	a.modelRuntime.providers = map[string]config.ProviderConfig{"prov": {}, "other": {}}
	a.modelRuntime.clients = map[string]llm.LLM{"prov": &fakeLLM{}, "other": &fakeLLM{}}
	a.modelRuntime.modeModels = map[string]config.ModelSelection{
		storage.SessionModeWork: {Provider: "prov", Model: "work-model"},
		storage.SessionModeChat: {Provider: "prov", Model: "chat-model"},
	}
	a.setNamingModel(config.ModelSelection{Provider: "prov", Model: "naming-model"})
	a.contextRuntime.setCompactModel(config.ModelSelection{Provider: "prov", Model: "compact-model"})
	return a
}

func snapshotSlotLabels(snapshot agentcommands.ModelSnapshot) []string {
	labels := make([]string, 0, len(snapshot.Slots))
	for _, slot := range snapshot.Slots {
		labels = append(labels, slot.Label)
	}
	return labels
}

func TestSaveModelSnapshotPersistsEverySlot(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	if err := a.SaveModelSnapshot("cheap"); err != nil {
		t.Fatalf("SaveModelSnapshot: %v", err)
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	saved, ok := state.ModelSnapshots["cheap"]
	if !ok {
		t.Fatalf("state file has no snapshot: %#v", state.ModelSnapshots)
	}
	if saved.ModeModels["chat"].Model != "chat-model" || saved.ModeModels["work"].Model != "work-model" {
		t.Fatalf("mode models = %#v", saved.ModeModels)
	}
	if saved.CompactModel.Model != "compact-model" || saved.NamingModel.Model != "naming-model" {
		t.Fatalf("compact/naming = %#v / %#v", saved.CompactModel, saved.NamingModel)
	}
	list := a.ModelSnapshots()
	if len(list) != 1 || list[0].Name != "cheap" {
		t.Fatalf("ModelSnapshots = %#v", list)
	}
	labels := snapshotSlotLabels(list[0])
	want := []string{storage.SessionModeChat, storage.SessionModeWork, "compact", "naming"}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("slot labels = %#v, want %#v", labels, want)
		}
	}
}

func TestSaveModelSnapshotRejectsStaticProfileName(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	a.modelProfiles = map[string]config.ModelSelection{"fast": {Provider: "prov", Model: "fast-model"}}
	a.modelAliases = map[string]string{"quick": "fast"}
	for _, name := range []string{"fast", "quick", "QUICK"} {
		if err := a.SaveModelSnapshot(name); err == nil {
			t.Fatalf("SaveModelSnapshot(%q) must fail because the name is a static profile", name)
		}
	}
	if len(a.ModelSnapshots()) != 0 {
		t.Fatalf("rejected name must not be stored: %#v", a.ModelSnapshots())
	}
}

func TestSaveModelSnapshotRejectsUnusableNames(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	cases := map[string]string{
		"empty":      "",
		"blank":      "   ",
		"space":      "a b",
		"slash":      "a/b",
		"dot":        "a.b",
		"too long":   strings.Repeat("x", 33),
		"non-letter": "模型!",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := a.SaveModelSnapshot(value); err == nil {
				t.Fatalf("SaveModelSnapshot(%q) must fail", value)
			}
		})
	}
	// 允许的字符集：Unicode 字母/数字/下划线/连字符，最长 32。
	for _, value := range []string{"cheap", "gpt4-mini", "省钱", "a_b"} {
		if _, err := normalizeModelSnapshotName(value); err != nil {
			t.Fatalf("normalizeModelSnapshotName(%q) = %v", value, err)
		}
	}
}

func TestApplyModelSnapshotRestoresEverySlot(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	if err := a.SaveModelSnapshot("cheap"); err != nil {
		t.Fatalf("SaveModelSnapshot: %v", err)
	}
	// 切到别的模型，再应用快照，必须整体回到保存时的状态。
	for _, mode := range []string{storage.SessionModeWork, storage.SessionModeChat} {
		if err := a.applyModeModelSelection(mode, "other", "changed-"+mode); err != nil {
			t.Fatalf("applyModeModelSelection: %v", err)
		}
	}
	a.setNamingModel(config.ModelSelection{Provider: "other", Model: "changed-naming"})
	a.contextRuntime.setCompactModel(config.ModelSelection{Provider: "other", Model: "changed-compact"})

	applied, err := a.ApplyModelSnapshot("cheap")
	if err != nil {
		t.Fatalf("ApplyModelSnapshot: %v", err)
	}
	if applied.Name != "cheap" || len(applied.Slots) != 4 {
		t.Fatalf("applied view = %#v", applied)
	}
	current := a.modeModelsSnapshot()
	if current[storage.SessionModeWork].Model != "work-model" || current[storage.SessionModeChat].Model != "chat-model" {
		t.Fatalf("mode models after apply = %#v", current)
	}
	if a.configuredNamingModel().Model != "naming-model" {
		t.Fatalf("naming after apply = %#v", a.configuredNamingModel())
	}
	if a.contextRuntime.configuredCompactModel().Model != "compact-model" {
		t.Fatalf("compact after apply = %#v", a.contextRuntime.configuredCompactModel())
	}
	// work 必须最后应用：applyModeModelSelection 会把最后一次调用的 provider 记为进程主
	// provider，顺序不确定时这里就会随机失败。
	if a.modelRuntime.providerName != "prov" || a.modelRuntime.model != "work-model" {
		t.Fatalf("primary selection = %s/%s", a.modelRuntime.providerName, a.modelRuntime.model)
	}
	// 应用后的选择也要落盘，否则重启就回到旧模型。
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.ModeModels[storage.SessionModeWork].Model != "work-model" {
		t.Fatalf("persisted mode models = %#v", state.ModeModels)
	}
}

func TestApplyModelSnapshotRejectsUnknownProviderWithoutPartialApply(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	a.modelSnapshots = map[string]config.StateModelSnapshot{
		"broken": {ModeModels: map[string]config.ModelSelection{
			storage.SessionModeWork: {Provider: "prov", Model: "work-model"},
			storage.SessionModeChat: {Provider: "missing", Model: "ghost"},
		}},
	}
	if _, err := a.ApplyModelSnapshot("broken"); err == nil {
		t.Fatal("ApplyModelSnapshot with an unknown provider must fail")
	}
	current := a.modeModelsSnapshot()
	if current[storage.SessionModeWork].Model != "work-model" || current[storage.SessionModeChat].Model != "chat-model" {
		t.Fatalf("failed apply must not change anything: %#v", current)
	}
}

func TestApplyModelSnapshotExplainsMissingName(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	if _, err := a.ApplyModelSnapshot("nope"); err == nil || !strings.Contains(err.Error(), "--snapshots") {
		t.Fatalf("missing snapshot error = %v", err)
	}
	a.modelProfiles = map[string]config.ModelSelection{"fast": {Provider: "prov", Model: "fast-model"}}
	if _, err := a.ApplyModelSnapshot("fast"); err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("static profile error = %v", err)
	}
}

func TestApplyModelSnapshotMergesExternalEditFirst(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	if err := a.SaveModelSnapshot("first"); err != nil {
		t.Fatalf("SaveModelSnapshot: %v", err)
	}
	// 模拟"别的进程/手工编辑"在两次命令之间改写了 state.toml：文件里只有 second。
	// 应用 first 时必须先合并这次外部编辑，否则 application 会在内存里凭空造出 first。
	external := config.StateConfig{
		Session:         config.StateSessionConfig{DefaultMode: storage.SessionModeWork},
		ModeModels:      a.modeModelsSnapshot(),
		ModelSnapshots:  map[string]config.StateModelSnapshot{"second": {ModeModels: map[string]config.ModelSelection{storage.SessionModeWork: {Provider: "prov", Model: "work-model"}}}},
		CompactModel:    a.contextRuntime.configuredCompactModel(),
		NamingModel:     a.configuredNamingModel(),
		ContextOverflow: nil,
	}
	if err := config.SaveState(a.statePath, external); err != nil {
		t.Fatalf("SaveState external: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a.statePath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, err := a.ApplyModelSnapshot("first"); err == nil {
		t.Fatal("first was replaced externally, applying it must fail")
	}
	if _, err := a.ApplyModelSnapshot("second"); err != nil {
		t.Fatalf("externally added snapshot must be merged in: %v", err)
	}
}

// SaveModelSnapshot 必须先合并外部编辑再改内存：反过来会把刚保存的快照丢掉，
// 命令却报成功。
func TestSaveModelSnapshotKeepsSnapshotWithConcurrentExternalEdit(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	if err := a.SaveModelSnapshot("first"); err != nil {
		t.Fatalf("SaveModelSnapshot: %v", err)
	}
	external := config.StateConfig{
		Session:         config.StateSessionConfig{DefaultMode: storage.SessionModeWork},
		ModeModels:      a.modeModelsSnapshot(),
		ModelSnapshots:  map[string]config.StateModelSnapshot{"second": {ModeModels: map[string]config.ModelSelection{storage.SessionModeWork: {Provider: "prov", Model: "work-model"}}}},
		CompactModel:    a.contextRuntime.configuredCompactModel(),
		NamingModel:     a.configuredNamingModel(),
		ContextOverflow: nil,
	}
	if err := config.SaveState(a.statePath, external); err != nil {
		t.Fatalf("SaveState external: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a.statePath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := a.SaveModelSnapshot("third"); err != nil {
		t.Fatalf("SaveModelSnapshot third: %v", err)
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if _, ok := state.ModelSnapshots["third"]; !ok {
		t.Fatalf("just-saved snapshot was lost by the external-edit merge: %#v", state.ModelSnapshots)
	}
	if _, ok := state.ModelSnapshots["second"]; !ok {
		t.Fatalf("external snapshot was overwritten: %#v", state.ModelSnapshots)
	}
}

func TestDeleteModelSnapshotRemovesItFromDisk(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	if err := a.SaveModelSnapshot("cheap"); err != nil {
		t.Fatalf("SaveModelSnapshot: %v", err)
	}
	if err := a.DeleteModelSnapshot("cheap"); err != nil {
		t.Fatalf("DeleteModelSnapshot: %v", err)
	}
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if _, ok := state.ModelSnapshots["cheap"]; ok {
		t.Fatalf("snapshot still on disk: %#v", state.ModelSnapshots)
	}
	if err := a.DeleteModelSnapshot("cheap"); err == nil {
		t.Fatal("deleting a missing snapshot must fail so the command can report it")
	}
}

func TestModelSnapshotsSurviveRuntimeStateReload(t *testing.T) {
	a := newModelSnapshotTestAgent(t)
	if err := a.SaveModelSnapshot("cheap"); err != nil {
		t.Fatalf("SaveModelSnapshot: %v", err)
	}
	// 换一个 Agent 读同一个 state.toml：快照必须能恢复出来（applyRuntimeState 的分片）。
	b := newModelSnapshotTestAgent(t)
	b.statePath = a.statePath
	b.modelRuntime.providers = a.modelRuntime.providers
	b.modelRuntime.clients = a.modelRuntime.clients
	if _, err := b.refreshRuntimeState(true); err != nil {
		t.Fatalf("refreshRuntimeState: %v", err)
	}
	list := b.ModelSnapshots()
	if len(list) != 1 || list[0].Name != "cheap" {
		t.Fatalf("ModelSnapshots after reload = %#v", list)
	}
	if _, err := b.ApplyModelSnapshot("cheap"); err != nil {
		t.Fatalf("ApplyModelSnapshot after reload: %v", err)
	}
}
