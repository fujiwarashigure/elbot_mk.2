package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"elbot/internal/llm"
	"elbot/internal/tool"
	"elbot/internal/utils/fileops"
)

// RollbackFileName is the built-in tool that undoes ElBot's own latest edit of
// one file. It is superadmin-only and expands together with the file tools.
const RollbackFileName = "rollback_file"

type RollbackFileTool struct {
	Backups   *fileops.RollbackStore
	FileGuard *FileGuard
}

type rollbackFileArgs struct {
	ID   uint64 `json:"id"`
	Path string `json:"path"`
}

func NewRollbackFileTool(backups *fileops.RollbackStore, fileGuard ...*FileGuard) RollbackFileTool {
	return RollbackFileTool{Backups: backups, FileGuard: firstFileGuard(fileGuard)}
}

func (RollbackFileTool) Name() string { return RollbackFileName }

func rollbackFileBuilder() *tool.Builder {
	return tool.NewBuilder(RollbackFileName).
		Description("撤销 ElBot 最近一次 edit_file 对某个文件的修改，恢复到编辑前的内容。只在用户明确要求回滚时调用；用 id（来自当前会话的回滚记录）或 path 指定文件；不传参数时只列出可回滚记录。不覆盖 Shell 或外部程序在编辑之后做的修改。").
		SuperadminOnly().
		Risk(tool.RiskHigh).
		DependsOn("read_file", "edit_file").
		Tags("files", "agent").
		Integer("id", "回滚记录编号；与 path 二选一。").
		String("path", "要回滚的文件路径，基于当前 workspace 解析；也可传绝对路径。")
}

func (RollbackFileTool) Info() tool.Info { return rollbackFileBuilder().BuildInfo() }

func (RollbackFileTool) Schema() llm.ToolSchema { return rollbackFileBuilder().BuildSchema() }

func (t RollbackFileTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	var args rollbackFileArgs
	if len(req.Arguments) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(req.Arguments)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&args); err != nil {
			return nil, fmt.Errorf("parse rollback_file arguments: %w", err)
		}
	}
	if t.Backups == nil {
		return &tool.Result{Content: "文件回滚未启用。"}, nil
	}
	sessionID := tool.SessionIDFromContext(ctx)
	if sessionID == "" {
		return &tool.Result{Content: "无法确定当前会话，未回滚。"}, nil
	}
	records := t.Backups.List(sessionID)
	id := args.ID
	if id == 0 && strings.TrimSpace(args.Path) != "" {
		resolved, err := tool.ResolveWorkspacePath(ctx, args.Path, tool.PathResolveOptions{})
		if err != nil {
			return nil, err
		}
		for _, info := range records {
			if info.Path == resolved.Path {
				id = info.ID
				break
			}
		}
		if id == 0 {
			return &tool.Result{Content: fmt.Sprintf("没有该文件的可回滚记录：%s", resolved.Path)}, nil
		}
	}
	if id == 0 {
		return &tool.Result{Content: formatRollbackRecords(records)}, nil
	}
	info, ok := t.Backups.Get(sessionID, id)
	if !ok {
		return &tool.Result{Content: fmt.Sprintf("没有编号 %d 的回滚记录，可能已被使用、被更新或已淘汰。", id)}, nil
	}
	if t.FileGuard != nil {
		if err := t.FileGuard.CheckWrite(info.Path); err != nil {
			return nil, err
		}
	}
	restored, err := t.Backups.Restore(ctx, sessionID, id)
	switch {
	case errors.Is(err, fileops.ErrRollbackConflict):
		return &tool.Result{Content: fmt.Sprintf("文件在编辑之后被 Shell 或外部程序修改，未回滚：%s", info.Path)}, nil
	case errors.Is(err, fileops.ErrRollbackNotFound):
		return &tool.Result{Content: fmt.Sprintf("没有编号 %d 的回滚记录，可能已被使用、被更新或已淘汰。", id)}, nil
	case err != nil:
		return nil, err
	}
	if restored.Created {
		return &tool.Result{Content: fmt.Sprintf("已回滚 %s：删除这次编辑创建的文件（记录 %d）。", restored.Path, restored.ID)}, nil
	}
	return &tool.Result{Content: fmt.Sprintf("已回滚 %s：恢复编辑前内容（记录 %d，编辑前 revision %s）。", restored.Path, restored.ID, restored.RevisionBefore)}, nil
}

// formatRollbackRecords renders the list used by both the tool and /rollback.
func formatRollbackRecords(records []fileops.RollbackInfo) string {
	if len(records) == 0 {
		return "当前会话没有可回滚的文件编辑。"
	}
	var sb strings.Builder
	sb.WriteString("可回滚的文件编辑：")
	for _, record := range records {
		kind := "编辑"
		if record.Created {
			kind = "新建"
		}
		fmt.Fprintf(&sb, "\n  %d. %s（%s，%s，%d 字节）", record.ID, record.Path, kind, record.EditedAt.Format("2006-01-02 15:04:05"), record.Bytes)
	}
	return sb.String()
}
