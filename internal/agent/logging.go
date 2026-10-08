package agent

import (
	"context"
	"log/slog"

	"elbot/internal/logging"
	"elbot/internal/redact"
)

type LogManager interface {
	Runtime() *slog.Logger
	Audit() *slog.Logger
	LogDir() string
}

func (a *Agent) SetLogger(logger *slog.Logger) {
	a.logger = logger
	if a.titleGen != nil {
		a.titleGen.setLogger(logger)
	}
}

func (a *Agent) SetLogManager(logs LogManager) {
	if logs == nil {
		a.logger = nil
		a.auditLogger = nil
		return
	}
	a.logger = logs.Runtime()
	a.auditLogger = logs.Audit()
	a.logReader = logging.Reader{Dir: logs.LogDir()}
	if a.titleGen != nil {
		a.titleGen.setLogger(a.logger)
	}
}

func (a *Agent) QueryLogs(ctx context.Context, query logging.LogQuery) ([]logging.LogEntry, error) {
	return a.logReader.Query(ctx, query)
}

func (a *Agent) audit(event string, attrs ...any) {
	a.auditLog(slog.LevelInfo, event, attrs...)
}

func (a *Agent) auditDebug(event string, attrs ...any) {
	a.auditLog(slog.LevelDebug, event, attrs...)
}

func (a *Agent) auditWarn(event string, attrs ...any) {
	a.auditLog(slog.LevelWarn, event, attrs...)
}

func (a *Agent) auditError(event string, attrs ...any) {
	// ERROR 级审计记录按约定就是"这次操作失败了"：补 result=failed，除非调用方自己
	// 明确给了 result（例如将来某个 ERROR 记录其实是被拒绝）。
	if !auditAttrsHaveKey(attrs, "result") {
		attrs = append([]any{"result", logging.ResultFailed}, attrs...)
	}
	a.auditLog(slog.LevelError, event, attrs...)
}

// auditAttrsHaveKey 报告附加属性里是否已经有该键；审计属性按 key/value 成对给出。
func auditAttrsHaveKey(attrs []any, key string) bool {
	for i := 0; i+1 < len(attrs); i += 2 {
		if name, ok := attrs[i].(string); ok && name == key {
			return true
		}
	}
	return false
}

func (a *Agent) auditLog(level slog.Level, event string, attrs ...any) {
	if a.auditLogger == nil {
		return
	}
	// module 属于来源标识契约：/log --hook、/audit --hook 按 module=hook 过滤，写入端
	// 必须给出自己的来源，不能再靠读取方猜测。event 与 module 放在最前，调用方仍可用
	// 同名属性覆盖（当前没有这样的调用方）。
	head := make([]any, 0, len(attrs)+4)
	head = append(head, "event", event, "module", logging.ModuleAgent)
	head = append(head, attrs...)
	a.auditLogger.Log(context.Background(), level, "audit event", normalizeAuditAttrs(head)...)
}

// normalizeAuditAttrs 把审计属性里的 error 统一转成脱敏后的字符串。审计记录只接受
// 文本与标量：error 对象交给 slog.Any 时最终输出取决于动态类型，可能变成无法按字段
// 查询的结构；来源已经写成 err.Error() 的地方保持原样。
func normalizeAuditAttrs(attrs []any) []any {
	for i := 0; i+1 < len(attrs); i += 2 {
		if err, ok := attrs[i+1].(error); ok {
			attrs[i+1] = redact.Error(err)
		}
	}
	return attrs
}
