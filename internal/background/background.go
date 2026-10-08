package background

import (
	"context"
	"elbot/internal/llm"

	"elbot/internal/security"
	"elbot/internal/toolrun"
)

type Kind string

const (
	KindCron  Kind = "cron"
	KindElnis Kind = "elnis"
)

type Runner interface {
	RunBackground(ctx context.Context, req RunRequest) (RunResult, error)
}

type RunRequest struct {
	Kind           Kind
	Name           string
	Title          string
	Platform       string
	Actor          security.Actor
	ScopeID        string
	SessionID      string
	ModelProvider  string
	Model          string
	SessionMode    string
	PromptSegments []llm.MessageSegment
	Prompt         string
	RetryPrompt    string
	ToolListNames  []string
	CachedTools    []toolrun.CachedTool
	SandboxSubdir  string
	Metadata       map[string]string
}

// OutcomeTakenOver 表示后台任务在运行中被前台接管而提前结束：它既不成功也不失败，
// 不产生汇报，也不重复投递。
const OutcomeTakenOver = "taken_over"

type RunResult struct {
	SessionID string
	MessageID string
	Text      string
	Parsed    JSONResult
	ParseErr  error
	// TakenOver 为 true 时表示该 Session 已被前台接管，本次后台运行在安全点自行
	// 停止，Text 为空且不应当作报告解析。
	TakenOver bool
	Outcome   string
}

type JSONResult struct {
	Completed      bool                 `json:"completed"`
	NeedReport     bool                 `json:"need_report"`
	ReportSegments []llm.MessageSegment `json:"report_segments,omitempty"`
	Report         string               `json:"report"`
}
