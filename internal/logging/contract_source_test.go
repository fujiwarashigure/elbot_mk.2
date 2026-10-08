package logging

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// auditResultLiteral 匹配审计/日志调用里"值本身就在源码里写死"的 result：
// 要么是 `logging.ResultXxx` 常量，要么是字符串字面量。把 result 交给变量或函数
// （例如 `"result", preloadSkipResult(reason)`）的地方无法静态判断，由调用方自己的
// 单元测试负责；这里的规则故意不匹配它们，避免误报。
var auditResultLiteral = regexp.MustCompile(`"result",\s*(logging\.Result\w+|"[^"]*")`)

// auditCallSite 确认这一行确实是日志/审计调用。没有这层判断时，查询代码里的纯字符串
// 列表（例如 reader.go 的候选字段名、commands/log.go 的展示标签）会被误判成写入 result。
var auditCallSite = regexp.MustCompile(`\.(audit|auditError|auditWarn|auditDebug|Log|Info|Warn|Error)\(`)

// TestSourceResultLiteralsAreValid 在源码层面守住操作结果契约：来源可以写
// logging.ResultXxx 常量，也可以写字符串字面量，但两种写法都必须是登记过的取值。
// 没有这层检查时，一个拼错的 "succeded" 会安静地写进日志，查询侧再也筛不到它。
func TestSourceResultLiteralsAreValid(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)
		for _, match := range auditResultLiteral.FindAllStringSubmatchIndex(content, -1) {
			lineStart := strings.LastIndexByte(content[:match[0]], '\n') + 1
			lineEnd := strings.IndexByte(content[match[1]:], '\n')
			if lineEnd < 0 {
				lineEnd = len(content)
			} else {
				lineEnd += match[1]
			}
			line := content[lineStart:lineEnd]
			if !auditCallSite.MatchString(line) {
				continue
			}
			value := strings.Trim(content[match[2]:match[3]], `"`)
			switch value {
			case "logging.ResultSucceeded", "ResultSucceeded":
				value = ResultSucceeded
			case "logging.ResultFailed", "ResultFailed":
				value = ResultFailed
			case "logging.ResultCanceled", "ResultCanceled":
				value = ResultCanceled
			case "logging.ResultRejected", "ResultRejected":
				value = ResultRejected
			case "logging.ResultSkipped", "ResultSkipped":
				value = ResultSkipped
			}
			checked++
			if !ValidLogResult(value) {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s: result 取值 %q 不在契约登记范围内：%s", relative, value, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk source tree: %v", err)
	}
	if checked == 0 {
		t.Fatal("没有找到任何 result 字面量，检查规则可能已经失效")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
