package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"elbot/internal/character"
	"elbot/internal/llm"
	"elbot/internal/security"
	"elbot/internal/tool"
)

func characterToolsByName(t *testing.T) map[string]tool.Tool {
	t.Helper()
	store := character.NewStore(t.TempDir())
	out := map[string]tool.Tool{}
	for _, candidate := range NewCharacterTools(store, nil) {
		out[candidate.Name()] = candidate
	}
	return out
}

func TestCharacterToolsManageListSearchRead(t *testing.T) {
	owner := security.WithActor(context.Background(), security.Actor{ID: "qqonebot:1001", Platform: "qqonebot", PlatformUserID: "1001", Role: security.RoleUser})
	other := security.WithActor(context.Background(), security.Actor{ID: "qqonebot:1002", Platform: "qqonebot", PlatformUserID: "1002", Role: security.RoleUser})
	tools := characterToolsByName(t)

	manage := tools[CharacterManageName]
	result, err := manage.Call(owner, tool.CallRequest{
		Name:      CharacterManageName,
		Arguments: json.RawMessage(`{"operation":"create","id":"catgirl","name":"猫娘","tags":["anime"],"docs":{"profile":"你是一只猫娘，说话带喵。"}}`),
	})
	if err != nil {
		t.Fatalf("manage: %v", err)
	}
	if !strings.Contains(result.Content, "角色已保存") {
		t.Fatalf("manage = %q", result.Content)
	}

	// Another user must not modify it.
	result, err = manage.Call(other, tool.CallRequest{
		Name:      CharacterManageName,
		Arguments: json.RawMessage(`{"operation":"update","id":"catgirl","name":"hacked"}`),
	})
	if err != nil {
		t.Fatalf("manage other: %v", err)
	}
	if !strings.Contains(result.Content, "不属于你") {
		t.Fatalf("other manage = %q", result.Content)
	}

	// List hides the private character from other users.
	list := tools[CharacterListName]
	result, err = list.Call(owner, tool.CallRequest{Name: CharacterListName, Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(result.Content, "catgirl") || !strings.Contains(result.Content, "猫娘") {
		t.Fatalf("owner list = %q", result.Content)
	}
	result, err = list.Call(other, tool.CallRequest{Name: CharacterListName, Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if strings.Contains(result.Content, "catgirl") {
		t.Fatalf("private character leaked to other: %q", result.Content)
	}

	// Search by body text.
	search := tools[CharacterSearchName]
	result, err = search.Call(owner, tool.CallRequest{Name: CharacterSearchName, Arguments: json.RawMessage(`{"query":"猫娘"}`)})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(result.Content, "catgirl") {
		t.Fatalf("search = %q", result.Content)
	}

	// Read returns the profile text.
	read := tools[CharacterReadName]
	result, err = read.Call(owner, tool.CallRequest{Name: CharacterReadName, Arguments: json.RawMessage(`{"id":"catgirl","section":"profile"}`)})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := llm.SegmentsContentText(result.Segments)
	if !strings.Contains(content, "你是一只猫娘") {
		t.Fatalf("read = %q", content)
	}

	// Delete is owner-only.
	deleteTool := tools[CharacterDeleteName]
	result, err = deleteTool.Call(other, tool.CallRequest{Name: CharacterDeleteName, Arguments: json.RawMessage(`{"id":"catgirl"}`)})
	if err != nil {
		t.Fatalf("delete other: %v", err)
	}
	if !strings.Contains(result.Content, "不属于你") {
		t.Fatalf("delete other = %q", result.Content)
	}
	result, err = deleteTool.Call(owner, tool.CallRequest{Name: CharacterDeleteName, Arguments: json.RawMessage(`{"id":"catgirl"}`)})
	if err != nil {
		t.Fatalf("delete owner: %v", err)
	}
	if !strings.Contains(result.Content, "已删除角色") {
		t.Fatalf("delete owner = %q", result.Content)
	}
}
