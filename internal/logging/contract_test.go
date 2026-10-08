package logging

import "testing"

func TestLogModulesAreRegisteredAndUnique(t *testing.T) {
	modules := LogModules()
	if len(modules) == 0 {
		t.Fatal("LogModules is empty")
	}
	seen := map[string]bool{}
	for _, module := range modules {
		if module == "" {
			t.Fatal("registered module must not be empty")
		}
		if seen[module] {
			t.Fatalf("module %q registered twice", module)
		}
		seen[module] = true
		if !ValidLogModule(module) {
			t.Fatalf("LogModules returned %q but ValidLogModule rejects it", module)
		}
	}
	if len(seen) != len(logModules) {
		t.Fatalf("LogModules=%d entries, registry has %d", len(seen), len(logModules))
	}
}

func TestValidLogModuleRejectsUnregisteredAndEmpty(t *testing.T) {
	for _, module := range []string{"", "  ", "Agent", "hookk", "internal/agent", "hook.tool"} {
		if ValidLogModule(module) {
			t.Fatalf("ValidLogModule(%q) = true, want false", module)
		}
	}
	for _, module := range []string{" hook ", "agent"} {
		if !ValidLogModule(module) {
			t.Fatalf("ValidLogModule(%q) = false, want true", module)
		}
	}
}

func TestValidLogResultAcceptsOnlyContractValues(t *testing.T) {
	results := []string{ResultSucceeded, ResultFailed, ResultCanceled, ResultRejected, ResultSkipped}
	for _, result := range results {
		if !ValidLogResult(result) {
			t.Fatalf("ValidLogResult(%q) = false", result)
		}
	}
	// 零值表示"未声明"：不能默认成成功，也不能用自由措辞。
	for _, result := range []string{"", "success", "ok", "error", "denied", "retrying", "unknown"} {
		if ValidLogResult(result) {
			t.Fatalf("ValidLogResult(%q) = true, want false", result)
		}
	}
}
