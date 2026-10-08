package logging

import (
	"context"
	"testing"
)

// TestContractFieldsRoundTripThroughProductionPipeline 端到端验证 P1#1 的验收条件：
// 来源标识（module）与操作结果（result）经**生产链路**写入后，Reader 的字段筛选仍然可用。
// 之前只有"源码里的 result 字面量合法"这一层静态校验，以及 Reader 对固定文本夹具的解析
// 测试；两者都不覆盖"契约字段真的能落盘并被查出来"。
func TestContractFieldsRoundTripThroughProductionPipeline(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager("info", dir+"/elbot_sessions.db", DefaultRetentionDays)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	audit := manager.Audit()

	// 模拟 Agent / app 侧审计入口的写法：event + module 在前，正文与结果在后。
	audit.Info("audit event",
		"event", "permission_denied", "module", ModuleAgent,
		"tool", "shell", "reason", "tool_risk_above_allowed_level", "result", ResultRejected)
	audit.Info("audit event",
		"event", "tool_call", "module", ModuleHook,
		"tool", "web_search", "result", ResultSucceeded)
	audit.Info("audit event",
		"event", "session_naming_failed", "module", ModuleApp,
		"result", ResultFailed)
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}

	reader := Reader{Dir: manager.LogDir()}
	ctx := context.Background()

	// 1. 按 module 筛选（/log --hook、/audit --hook 依赖这个字段）。
	hookOnly, err := reader.Query(ctx, LogQuery{Prefix: "audit", Fields: map[string]string{"module": ModuleHook}})
	if err != nil {
		t.Fatalf("Query by module: %v", err)
	}
	if len(hookOnly) != 1 || hookOnly[0].Fields["event"] != "tool_call" {
		t.Fatalf("module=%s 筛选结果 = %#v", ModuleHook, hookOnly)
	}

	// 2. module + event 组合筛选（--hook 与 --event 同时给出的场景）。
	combined, err := reader.Query(ctx, LogQuery{Prefix: "audit", Fields: map[string]string{"module": ModuleAgent, "event": "permission_denied"}})
	if err != nil {
		t.Fatalf("Query by module+event: %v", err)
	}
	if len(combined) != 1 || combined[0].Fields["result"] != ResultRejected {
		t.Fatalf("module+event 筛选结果 = %#v", combined)
	}

	// 3. 按 result 筛选：新契约的核心价值，此刻必须真的可查。
	rejected, err := reader.Query(ctx, LogQuery{Prefix: "audit", Fields: map[string]string{"result": ResultRejected}})
	if err != nil {
		t.Fatalf("Query by result: %v", err)
	}
	if len(rejected) != 1 || rejected[0].Fields["reason"] != "tool_risk_above_allowed_level" {
		t.Fatalf("result=%s 筛选结果 = %#v", ResultRejected, rejected)
	}

	// 4. 不同 result 取值互不串台。
	failed, err := reader.Query(ctx, LogQuery{Prefix: "audit", Fields: map[string]string{"result": ResultFailed}})
	if err != nil {
		t.Fatalf("Query by result=failed: %v", err)
	}
	if len(failed) != 1 || failed[0].Fields["event"] != "session_naming_failed" {
		t.Fatalf("result=%s 筛选结果 = %#v", ResultFailed, failed)
	}

	// 5. 按 module 统计：三类来源都能被分辨出来，没有记录缺来源。
	all, err := reader.Query(ctx, LogQuery{Prefix: "audit", Limit: 10, Days: 1, MinLevel: "info"})
	if err != nil {
		t.Fatalf("Query all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("记录数 = %d, want 3: %#v", len(all), all)
	}
	for _, entry := range all {
		if !ValidLogModule(entry.Fields["module"]) {
			t.Fatalf("记录缺少合法来源标识: %#v", entry.Fields)
		}
		if !ValidLogResult(entry.Fields["result"]) {
			t.Fatalf("记录缺少合法操作结果: %#v", entry.Fields)
		}
	}
}
