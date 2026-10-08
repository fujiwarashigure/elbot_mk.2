package skill

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/security"
)

func noticeTestRecord() Record {
	return Record{Name: "reader", Kind: KindAgent, Detail: "# reader\n\n文档正文。"}
}

func TestAgentSkillNoticeIsSuperadminOnly(t *testing.T) {
	userCtx := security.WithActor(context.Background(), security.Actor{ID: "cli:u1", Platform: "cli", Role: security.RoleUser})
	block, err := NewDescriptor(noticeTestRecord()).LoadDetail(userCtx)
	if err != nil {
		t.Fatalf("LoadDetail: %v", err)
	}
	if strings.Contains(block.Content, "agent_skill_creator") {
		t.Fatalf("creator notice leaked to a regular user:\n%s", block.Content)
	}
	if strings.Contains(block.Content, "ElBot AgentSkill 使用提示") {
		t.Fatalf("hint header leaked to a regular user:\n%s", block.Content)
	}
	if !strings.Contains(block.Content, "文档正文") {
		t.Fatalf("document content missing:\n%s", block.Content)
	}

	adminCtx := security.WithActor(context.Background(), security.Actor{ID: "cli:root", Platform: "cli", Role: security.RoleSuperadmin})
	block, err = NewDescriptor(noticeTestRecord()).LoadDetail(adminCtx)
	if err != nil {
		t.Fatalf("LoadDetail: %v", err)
	}
	if !strings.Contains(block.Content, "agent_skill_creator") {
		t.Fatalf("creator notice missing for a superadmin:\n%s", block.Content)
	}
}

func TestAgentSkillManifestWarningStaysVisibleToEveryone(t *testing.T) {
	record := noticeTestRecord()
	record.ManifestFound = true
	record.ManifestError = "invalid TOML"

	userCtx := security.WithActor(context.Background(), security.Actor{ID: "cli:u1", Platform: "cli", Role: security.RoleUser})
	block, err := NewDescriptor(record).LoadDetail(userCtx)
	if err != nil {
		t.Fatalf("LoadDetail: %v", err)
	}
	if !strings.Contains(block.Content, AgentSkillConfigFile+" 无效") {
		t.Fatalf("invalid manifest warning must stay visible to every role:\n%s", block.Content)
	}
	if strings.Contains(block.Content, "agent_skill_creator") {
		t.Fatalf("creator notice leaked to a regular user:\n%s", block.Content)
	}
}
