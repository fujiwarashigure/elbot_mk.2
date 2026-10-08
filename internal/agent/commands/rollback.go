package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"elbot/internal/command"
	"elbot/internal/utils/fileops"
)

// FileModule registers the file rollback command.
type FileModule struct{}

func (FileModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return RegisterFactories(registrar, deps, NewRollback)
}

// NewRollback lists and undoes ElBot's own latest edit of a file. It is
// superadmin-only: Info.MinRole stays empty, which the router treats as
// superadmin.
func NewRollback(deps Deps) command.Handler {
	return command.NewFunc(command.Info{
		Name:        "rollback",
		Usage:       "/rollback [编号]",
		Description: "List the files ElBot edited in the current session and undo one of them. Without an argument it only lists records; /rollback <number> restores that file. Only superadmins can use it.",
	}, func(ctx context.Context, req command.Request) (*command.Result, error) {
		if deps.FileBackups == nil {
			return &command.Result{Content: "文件回滚未启用。"}, nil
		}
		if deps.Sessions == nil || deps.Scope == nil {
			return &command.Result{Content: "当前没有可用会话。"}, nil
		}
		current, err := deps.Sessions.Current(ctx, deps.Scope(ctx))
		if err != nil {
			return nil, err
		}
		arg := strings.TrimSpace(req.Args)
		if arg == "" {
			return &command.Result{Content: formatRollbackList(deps.FileBackups.List(current.ID))}, nil
		}
		id, err := strconv.ParseUint(arg, 10, 64)
		if err != nil || id == 0 {
			return &command.Result{Content: "用法：/rollback [编号]；编号来自不带参数的 /rollback 列表。"}, nil
		}
		info, ok := deps.FileBackups.Get(current.ID, id)
		if !ok {
			return &command.Result{Content: fmt.Sprintf("没有编号 %d 的回滚记录，可能已被使用、被更新或已淘汰。", id)}, nil
		}
		restored, err := deps.FileBackups.Restore(ctx, current.ID, id)
		switch {
		case errors.Is(err, fileops.ErrRollbackConflict):
			return &command.Result{Content: fmt.Sprintf("文件在编辑之后被 Shell 或外部程序修改，未回滚：%s", info.Path)}, nil
		case errors.Is(err, fileops.ErrRollbackNotFound):
			return &command.Result{Content: fmt.Sprintf("没有编号 %d 的回滚记录，可能已被使用、被更新或已淘汰。", id)}, nil
		case err != nil:
			return nil, err
		}
		if restored.Created {
			return &command.Result{Content: fmt.Sprintf("已回滚：删除 %s（这次编辑创建的文件）。", restored.Path)}, nil
		}
		return &command.Result{Content: fmt.Sprintf("已回滚 %s：恢复编辑前内容（编辑前 revision %s）。", restored.Path, restored.RevisionBefore)}, nil
	})
}

func formatRollbackList(records []fileops.RollbackInfo) string {
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
	sb.WriteString("\n使用 /rollback <编号> 回滚其中一个文件；切换 Session、重启或容量淘汰后记录失效。")
	return sb.String()
}
