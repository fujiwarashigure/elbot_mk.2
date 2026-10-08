package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"elbot/internal/tool"
	"elbot/internal/utils/fileops"
)

func rollbackArgs(t *testing.T, value map[string]any) tool.CallRequest {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return tool.CallRequest{Arguments: data}
}

func TestEditFileBackupAndRollbackFileRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	before := []byte("hello\n")
	if err := os.WriteFile(path, before, 0o640); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store := fileops.NewRollbackStore()
	ctx := tool.WithSessionID(context.Background(), "s1")

	edit := NewEditFileTool()
	edit.Backups = store
	if _, err := edit.Call(ctx, rollbackArgs(t, map[string]any{
		"path":              path,
		"expected_revision": fileops.ContentRevision(before),
		"edits":             []map[string]any{{"operation": "overwrite", "new_text": "world\n"}},
	})); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if content, _ := os.ReadFile(path); string(content) != "world\n" {
		t.Fatalf("edit did not apply: %q", string(content))
	}
	records := store.List("s1")
	if len(records) != 1 || records[0].Path != path || records[0].RevisionBefore != fileops.ContentRevision(before) {
		t.Fatalf("records = %#v", records)
	}

	rollback := NewRollbackFileTool(store)
	result, err := rollback.Call(ctx, rollbackArgs(t, map[string]any{"id": records[0].ID}))
	if err != nil || !strings.Contains(result.Content, "已回滚") {
		t.Fatalf("rollback = %#v err=%v", result, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(before) {
		t.Fatalf("restored content = %q, want %q", string(content), string(before))
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != beforeInfo.Mode().Perm() {
		t.Fatalf("mode not preserved: %v (was %v) err=%v", info.Mode(), beforeInfo.Mode(), err)
	}
	if _, err := rollback.Call(ctx, rollbackArgs(t, map[string]any{"id": records[0].ID})); err != nil {
		t.Fatalf("rollback again: %v", err)
	}
	if len(store.List("s1")) != 0 {
		t.Fatal("the record must be consumed")
	}
}

func TestRollbackFileRefusesExternalChangesAndRemovesCreatedFiles(t *testing.T) {
	dir := t.TempDir()
	store := fileops.NewRollbackStore()
	ctx := tool.WithSessionID(context.Background(), "s1")

	// An external writer after the recorded edit always wins.
	path := filepath.Join(dir, "changed.txt")
	before := []byte("before\n")
	if err := os.WriteFile(path, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, available := store.Record("s1", path, before, 0o644, true, fileops.ContentRevision(before), fileops.ContentRevision([]byte("edited\n"))); !available {
		t.Fatal("record must be available")
	}
	if err := os.WriteFile(path, []byte("shell wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rollback := NewRollbackFileTool(store)
	result, err := rollback.Call(ctx, rollbackArgs(t, map[string]any{"path": path}))
	if err != nil || !strings.Contains(result.Content, "未回滚") {
		t.Fatalf("conflict result = %#v err=%v", result, err)
	}
	if content, _ := os.ReadFile(path); string(content) != "shell wrote this\n" {
		t.Fatalf("external change was overwritten: %q", string(content))
	}

	// A file created by the edit is removed again.
	created := filepath.Join(dir, "created.txt")
	if err := os.WriteFile(created, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := store.Record("s1", created, nil, 0, false, "", fileops.ContentRevision([]byte("new\n")))
	result, err = rollback.Call(ctx, rollbackArgs(t, map[string]any{"id": info.ID}))
	if err != nil || !strings.Contains(result.Content, "删除") {
		t.Fatalf("created rollback = %#v err=%v", result, err)
	}
	if _, err := os.Stat(created); err == nil {
		t.Fatal("the created file must be removed")
	}
}

func TestRollbackFileListsAndExpandsFromFileTools(t *testing.T) {
	store := fileops.NewRollbackStore()
	ctx := tool.WithSessionID(context.Background(), "s1")
	path := filepath.Join(t.TempDir(), "a.txt")
	store.Record("s1", path, []byte("x\n"), 0o644, true, "r1", "r2")

	rollback := NewRollbackFileTool(store)
	result, err := rollback.Call(ctx, tool.CallRequest{})
	if err != nil || !strings.Contains(result.Content, path) {
		t.Fatalf("list = %#v err=%v", result, err)
	}
	if info := (RollbackFileTool{}).Info(); !info.SuperadminOnly || info.Risk != tool.RiskHigh {
		t.Fatalf("rollback_file info = %#v", info)
	}
	for _, dependencies := range [][]string{readFileBuilder().BuildInfo().DependsOn, editFileBuilder().BuildInfo().DependsOn} {
		if !slices.Contains(dependencies, RollbackFileName) {
			t.Fatalf("file tool dependencies %v must include %s", dependencies, RollbackFileName)
		}
	}
}

func TestRollbackFileUnknownRecordAndSession(t *testing.T) {
	rollback := NewRollbackFileTool(fileops.NewRollbackStore())
	result, err := rollback.Call(context.Background(), rollbackArgs(t, map[string]any{"id": 7}))
	if err != nil || !strings.Contains(result.Content, "无法确定当前会话") {
		t.Fatalf("sessionless call = %#v err=%v", result, err)
	}
	ctx := tool.WithSessionID(context.Background(), "s1")
	result, err = rollback.Call(ctx, rollbackArgs(t, map[string]any{"id": 7}))
	if err != nil || !strings.Contains(result.Content, "没有编号 7") {
		t.Fatalf("unknown record = %#v err=%v", result, err)
	}
}
