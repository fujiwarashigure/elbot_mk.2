package logging

import "strings"

// 本文件是 fork 版"来源标识 + 操作结果"最小契约的唯一出处。上游 Elflare/elbot 用九字段
// LogRecord（internal/events/logging.go）承载这件事；fork 没有 internal/events，日志仍走
// log/slog 文本文件 + internal/logging.Reader 的字段查询契约，因此这里只固化两件最小、
// 且不需要改变文件格式与查询契约的事：
//
//  1. 来源标识：每条运行日志和审计日志都要写明它来自哪个模块（module），并且只允许用
//     下面登记过的名字。查询侧一直按 module 过滤（/log --hook、/audit --hook 会设置
//     fields["module"]="hook"），此前却没有任何地方约束写入端，导致来源名只能靠约定。
//  2. 操作结果：拒绝、跳过、失败、成功、取消不再是只写在自由文本里的措辞，而是 as
//     `result` 字段的稳定取值，使 /audit --contains 之外的筛选可以按结果做，并且不再
//     需要从错误链或 reason 文案反推结果。
//
// 消息正文（slog 的 msg）、文本长度上限、JSON 正文与 JSONL 落盘属于完整契约的范围，
// 本轮不实施；迁移来源时按包分批接入，不改变已有事件名，避免破坏 /audit --event。

// 来源模块标识。作为 audit/log 记录的 module 字段写入，查询侧按同名值过滤。
const (
	// ModuleApp 是进程装配、启动、平台连接与关闭协调的来源。
	ModuleApp = "app"
	// ModuleAgent 是 Agent 对话、工具、命令、后台任务与群策略的来源。
	ModuleAgent = "agent"
	// ModuleSession 是 Session 生命周期与命名的来源。
	ModuleSession = "session"
	// ModuleHook 是 Hook 规则执行与 Hook 运行时桥接的来源；/log --hook 与
	// /audit --hook 正是按这个值过滤。
	ModuleHook = "hook"
	// ModulePlatform 是各平台适配器（CLI / Telegram / QQ OneBot / QQ 官方）的来源。
	ModulePlatform = "platform"
	// ModuleDelivery 是发送、通知与落盘关联的来源。
	ModuleDelivery = "delivery"
	// ModuleCron 是定时任务调度与执行的来源。
	ModuleCron = "cron"
	// ModuleElnis 是 Elnis / Elwisp / Elvena 事件处理的来源。
	ModuleElnis = "elnis"
	// ModuleModel 是模型客户端、Model Manager 与健康检查的来源。
	ModuleModel = "model"
	// ModuleStorage 是 SQLite 存储与迁移的来源。
	ModuleStorage = "storage"
	// ModuleMedia 是媒体入库、解析与清理的来源。
	ModuleMedia = "media"
	// ModuleMaintenance 是维护与清理任务的来源。
	ModuleMaintenance = "maintenance"
	// ModuleTool 是工具运行时与内置工具的来源。
	ModuleTool = "tool"
)

// logModules 登记全部合法来源标识。新增来源必须先在这里登记，避免出现拼写漂移的
// 第三套命名。
var logModules = map[string]struct{}{
	ModuleApp:         {},
	ModuleAgent:       {},
	ModuleSession:     {},
	ModuleHook:        {},
	ModulePlatform:    {},
	ModuleDelivery:    {},
	ModuleCron:        {},
	ModuleElnis:       {},
	ModuleModel:       {},
	ModuleStorage:     {},
	ModuleMedia:       {},
	ModuleMaintenance: {},
	ModuleTool:        {},
}

// LogModules 返回全部已登记的来源标识（顺序固定，便于文档与测试比对）。
func LogModules() []string {
	return []string{
		ModuleApp, ModuleAgent, ModuleSession, ModuleHook, ModulePlatform, ModuleDelivery,
		ModuleCron, ModuleElnis, ModuleModel, ModuleStorage, ModuleMedia, ModuleMaintenance, ModuleTool,
	}
}

// ValidLogModule 报告来源标识是否已登记。空字符串不是合法来源：来源必须写明自己是谁，
// 不能依赖读取方猜测。
func ValidLogModule(module string) bool {
	_, ok := logModules[strings.TrimSpace(module)]
	return ok
}

// 操作结果。写入 audit/log 记录的 result 字段。
//
// 这组取值只表达"业务在这个分支上确定下来的事实"，不代表日志等级：日志等级由掌握
// 上下文的来源决定，读取方不得根据 result 反推 level，也不得根据 level 反推 result。
// 例如运行中定时任务的正常取消是 canceled + INFO，而策略拒绝是 rejected + WARN。
const (
	// ResultSucceeded 表示操作按预期完成。
	ResultSucceeded = "succeeded"
	// ResultFailed 表示操作因故障未完成，不同于"被拒绝"与"被跳过"。
	ResultFailed = "failed"
	// ResultCanceled 表示操作被取消（用户撤回、/stop、关停），不是故障。
	ResultCanceled = "canceled"
	// ResultRejected 表示操作被策略、权限或额度拒绝；这是明确裁决，不是故障。
	ResultRejected = "rejected"
	// ResultSkipped 表示操作因前置条件不成立而未执行（未发现、未命中、开关关闭）。
	ResultSkipped = "skipped"
)

var logResults = map[string]struct{}{
	ResultSucceeded: {},
	ResultFailed:    {},
	ResultCanceled:  {},
	ResultRejected:  {},
	ResultSkipped:   {},
}

// ValidLogResult 报告操作结果取值是否合法。
func ValidLogResult(result string) bool {
	_, ok := logResults[strings.TrimSpace(result)]
	return ok
}
