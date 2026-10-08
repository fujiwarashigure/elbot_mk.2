package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnknownAppConfigKeysReportsTypos(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.toml")
	if err := os.WriteFile(path, []byte("[provders.openai]\nbase_url = \"http://example.invalid\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := UnknownAppConfigKeys(path)
	if err != nil {
		t.Fatalf("UnknownAppConfigKeys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("a misspelled section must be reported")
	}
	joined := strings.Join(keys, ",")
	if !strings.Contains(joined, "provders") {
		t.Fatalf("keys = %v", keys)
	}

	if err := os.WriteFile(path, []byte("[storage]\ndisk_warn_ratio = 0.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if keys, err := UnknownAppConfigKeys(path); err != nil || len(keys) != 0 {
		t.Fatalf("known keys reported as unknown: %v err=%v", keys, err)
	}
}

func TestBuiltinAssetDriftReportsMissingAndModifiedSkills(t *testing.T) {
	dir := t.TempDir()
	drift := BuiltinAssetDrift(dir)
	if len(drift) == 0 {
		t.Fatal("missing built-in skills must be reported")
	}
	for _, item := range drift {
		if !strings.Contains(item, "缺失内置 Skill 文件") {
			t.Fatalf("unexpected drift entry: %q", item)
		}
	}

	creator := filepath.Join(dir, "skills", "agent", "agent_skill_creator", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(creator), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(creator, []byte(defaultAgentSkillCreatorSkillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	creatorRel := filepath.Join("agent_skill_creator", "SKILL.md")
	after := BuiltinAssetDrift(dir)
	for _, item := range after {
		if strings.Contains(item, creatorRel) {
			t.Fatalf("an identical skill must not be reported: %q", item)
		}
	}
	if len(after) != len(drift)-1 {
		t.Fatalf("drift = %v, want one fewer entry", after)
	}

	if err := os.WriteFile(creator, []byte("# edited locally\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modified := BuiltinAssetDrift(dir)
	found := false
	for _, item := range modified {
		if strings.Contains(item, creatorRel) && strings.Contains(item, "不一致") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an edited built-in skill must be reported: %v", modified)
	}
}
