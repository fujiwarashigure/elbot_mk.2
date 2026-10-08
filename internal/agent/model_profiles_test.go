package agent

import (
	"testing"

	agentcommands "elbot/internal/agent/commands"
	"elbot/internal/config"
	"elbot/internal/llm"
)

func newModelProfilesTestAgent(t *testing.T) *Agent {
	t.Helper()
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.modelRuntime.clients = map[string]llm.LLM{"prov": &fakeLLM{}}
	return a
}

func profileNames(profiles []agentcommands.ModelProfile) []string {
	out := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, profile.Name)
	}
	return out
}

func TestModelProfilesListsProfilesAndAliases(t *testing.T) {
	a := newModelProfilesTestAgent(t)
	a.modelProfiles = map[string]config.ModelSelection{
		"fast": {Provider: "prov", Model: "fast-model"},
		"dead": {Provider: "missing", Model: "ghost"},
		"":     {Provider: "prov", Model: "ignored"},
		"bad":  {Provider: "prov"},
	}
	a.modelAliases = map[string]string{
		// 生产装配会给每个 profile 注册一条指向自身的别名（registerTurnAlias），
		// 这里照抄这个形状，避免测试走的是与线上不同的分支。
		"fast":     "fast",
		"cheap":    "fast",
		"dangling": "nonexistent",
	}
	got := a.ModelProfiles()
	names := profileNames(got)
	want := []string{"cheap", "dead", "fast"}
	if len(names) != len(want) {
		t.Fatalf("profiles = %#v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("profiles = %#v, want %#v", names, want)
		}
	}
	byName := map[string]bool{}
	for _, profile := range got {
		byName[profile.Name] = profile.Available
	}
	if !byName["fast"] || !byName["cheap"] {
		t.Fatalf("providers with a client must be available: %#v", got)
	}
	if byName["dead"] {
		t.Fatalf("provider without a client must be unavailable: %#v", got)
	}
}

// 别名先于同名 profile 命中（resolveGroupModelSelection 的顺序），所以列表只保留一行，
// 留的是用户实际输入的那个名字（别名键已小写化），否则操作者会看到两个一样的名字却不知道
// 哪个生效。
func TestModelProfilesAliasShadowsSameNamedProfile(t *testing.T) {
	a := newModelProfilesTestAgent(t)
	a.modelProfiles = map[string]config.ModelSelection{
		"cheap": {Provider: "prov", Model: "cheap-model"},
	}
	a.modelAliases = map[string]string{"CHEAP": "cheap"}
	got := a.ModelProfiles()
	if len(got) != 1 || got[0].Name != "cheap" {
		t.Fatalf("profiles = %#v", got)
	}
}

// 列表里的 Available 必须和真实解析结果一致：标成 available 的名字一定解析得出来，
// 否则这个列表就是误导。
func TestModelProfilesAvailabilityMatchesResolution(t *testing.T) {
	a := newModelProfilesTestAgent(t)
	a.modelProfiles = map[string]config.ModelSelection{
		"fast": {Provider: "prov", Model: "fast-model"},
		"dead": {Provider: "missing", Model: "ghost"},
	}
	a.modelAliases = map[string]string{"cheap": "fast"}
	got := a.ModelProfiles()
	if len(got) != 3 {
		t.Fatalf("profiles = %#v", got)
	}
	for _, profile := range got {
		selection, ok := a.resolveGroupModelSelection(profile.Name)
		if ok != profile.Available {
			t.Fatalf("profile %s: listed available=%v but resolved ok=%v", profile.Name, profile.Available, ok)
		}
		if ok && (selection.Provider != profile.Provider || selection.Model != profile.Model) {
			t.Fatalf("profile %s: listed %s/%s but resolved %s/%s", profile.Name, profile.Provider, profile.Model, selection.Provider, selection.Model)
		}
	}
}

func TestModelProfilesWithoutConfiguration(t *testing.T) {
	a := newModelProfilesTestAgent(t)
	if got := a.ModelProfiles(); len(got) != 0 {
		t.Fatalf("profiles = %#v", got)
	}
	var nilAgent *Agent
	if got := nilAgent.ModelProfiles(); got != nil {
		t.Fatalf("nil agent profiles = %#v", got)
	}
}
