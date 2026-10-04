package agent

import (
	"context"
	"path/filepath"
	"testing"

	"elbot/internal/config"
)

func newRuntimeStateTestAgent(t *testing.T) *Agent {
	t.Helper()
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	return a
}

func containsSection(sections []string, want string) bool {
	for _, section := range sections {
		if section == want {
			return true
		}
	}
	return false
}

// applyExternalGroupPolicy simulates an operator editing state.toml on disk.
func applyExternalGroupPolicy(t *testing.T, a *Agent, mode string) {
	t.Helper()
	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.GroupPolicy == nil {
		state.GroupPolicy = map[string]config.GroupPolicyConfig{}
	}
	state.GroupPolicy["cli:group:9"] = config.GroupPolicyConfig{ResponseMode: mode}
	if err := config.SaveState(a.statePath, *state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

func TestRefreshRuntimeStateAppliesExternalEdit(t *testing.T) {
	a := newRuntimeStateTestAgent(t)
	ctx := knowledgeTestContext()
	enableKnowledgeTestPolicy(t, a, ctx)
	if err := a.saveRuntimeState(); err != nil {
		t.Fatalf("saveRuntimeState: %v", err)
	}

	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	state.GroupPolicy["cli:group:9"] = config.GroupPolicyConfig{ResponseMode: "all", WakeKeywords: []string{"外部编辑"}}
	if err := config.SaveState(a.statePath, *state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	reload, err := a.refreshRuntimeState(false)
	if err != nil {
		t.Fatalf("refreshRuntimeState: %v", err)
	}
	if !reload.Applied {
		t.Fatal("external state.toml edit was not applied")
	}
	if !containsSection(reload.Changed, "group_policy") {
		t.Fatalf("changed sections = %#v, want group_policy", reload.Changed)
	}
	if got := a.groupPolicyForScope(a.baseScope(ctx)); len(got.WakeKeywords) != 1 || got.WakeKeywords[0] != "外部编辑" {
		t.Fatalf("in-memory policy = %#v", got)
	}

	// A second poll with no new edit must not report anything again.
	reload, err = a.refreshRuntimeState(false)
	if err != nil {
		t.Fatalf("refreshRuntimeState: %v", err)
	}
	if reload.Applied {
		t.Fatalf("unchanged state.toml should not be re-applied: %#v", reload)
	}
}

func TestSaveRuntimeStateMergesExternalEditBeforeWriteBack(t *testing.T) {
	a := newRuntimeStateTestAgent(t)
	if err := a.saveRuntimeState(); err != nil {
		t.Fatalf("saveRuntimeState: %v", err)
	}
	applyExternalGroupPolicy(t, a, "off")

	// An internal write-back triggered by unrelated runtime state must not drop
	// the external edit it never knew about.
	a.setBudgetUsageSnapshot(map[string]int64{"scope": 1}, nil, nil)
	if err := a.saveRuntimeState(); err != nil {
		t.Fatalf("saveRuntimeState: %v", err)
	}

	after, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got := after.GroupPolicy["cli:group:9"].ResponseMode; got != "off" {
		t.Fatalf("external group policy was overwritten by write-back: %q", got)
	}
}

func TestRefreshRuntimeStateKeepsLedgerOwnedByRuntime(t *testing.T) {
	a := newRuntimeStateTestAgent(t)
	if err := a.saveRuntimeState(); err != nil {
		t.Fatalf("saveRuntimeState: %v", err)
	}

	state, err := config.LoadState(a.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	state.Budget.Tokens = map[string]int64{"external": 999}
	if err := config.SaveState(a.statePath, *state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if _, err := a.refreshRuntimeState(false); err != nil {
		t.Fatalf("refreshRuntimeState: %v", err)
	}
	tokens, _, _ := a.budgetUsageSnapshot()
	if _, ok := tokens["external"]; ok {
		t.Fatalf("hot reload must not replace the live budget ledger: %#v", tokens)
	}
}

func TestRuntimeStateStatusReportsPendingExternalEdit(t *testing.T) {
	a := newRuntimeStateTestAgent(t)
	if err := a.saveRuntimeState(); err != nil {
		t.Fatalf("saveRuntimeState: %v", err)
	}
	if status := a.RuntimeStateStatus(); status.Path != a.statePath || status.Pending {
		t.Fatalf("status = %#v, want in-sync file", status)
	}

	applyExternalGroupPolicy(t, a, "off")
	if status := a.RuntimeStateStatus(); !status.Pending {
		t.Fatalf("status = %#v, want pending external edit", status)
	}

	report, err := a.ReloadRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("ReloadRuntimeState: %v", err)
	}
	if !report.Applied || !containsSection(report.Changed, "group_policy") {
		t.Fatalf("report = %#v", report)
	}
	if status := a.RuntimeStateStatus(); status.Pending {
		t.Fatalf("status after reload = %#v, want in sync", status)
	}
}
