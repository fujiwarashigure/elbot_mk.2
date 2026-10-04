package toolrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"elbot/internal/delivery"
	"elbot/internal/llm"
	"elbot/internal/security"
	"elbot/internal/storage"
	"elbot/internal/tool"
)

type RunnerDeps interface {
	PrepareToolCall(ctx context.Context, session *storage.Session, call llm.ToolCallRequest) (llm.ToolCallRequest, error)
	ShouldSendPreview(ctx context.Context, session *storage.Session, call llm.ToolCallRequest, assistantText string) bool
	ConfirmToolCall(ctx context.Context, sessionID string, call llm.ToolCallRequest, assessment tool.RiskAssessment, detail string) (ConfirmResult, error)
	ConfirmBackgroundTool(ctx context.Context, sessionID string, call llm.ToolCallRequest, resolved ResolvedTool, assessment tool.RiskAssessment) (ConfirmResult, bool)
	StartToolRequest(ctx context.Context, sessionID, toolName string) (context.Context, time.Time, func(), error)
	PrepareToolContext(ctx context.Context, session *storage.Session, call llm.ToolCallRequest) context.Context
	CompleteToolCall(ctx context.Context, session *storage.Session, call llm.ToolCallRequest, risk string, segments []llm.MessageSegment, callErr error) ([]llm.MessageSegment, error)
	SendPreview(ctx context.Context, text string)
	SendOutputs(ctx context.Context, outputs []delivery.Output) error
	RecordToolCall(ctx context.Context, sessionID string, call llm.ToolCallRequest, risk string, startedAt time.Time, result string, callErr error)
	AuditToolDenied(ctx context.Context, sessionID string, call llm.ToolCallRequest, risk tool.RiskLevel, reason string)
	RememberDiscoveryResult(ctx context.Context, session *storage.Session, result *tool.Result)
	AddToolUse(sessionID, toolName string)
	ToolResultMessage(sessionID string, message llm.LLMMessage) storage.Message
	ToolCallMessage(sessionID, content, rawText string, calls []llm.ToolCallRequest) storage.Message
	PersistedToolMessage(message llm.LLMMessage) llm.LLMMessage
}

type ConfirmResult struct {
	Allowed bool
	Extra   string
	Message llm.LLMMessage
	Stopped bool
}

type RunRequest struct {
	Session          *storage.Session
	Calls            []llm.ToolCallRequest
	AssistantText    string
	AssistantRawText string
	CachedTools      []CachedTool
	Actor            security.Actor
	// AllowedTools, when non-nil, is a server-side allowlist for tool names.
	// It is enforced before confirmation/execution so a model cannot invoke a
	// tool that only exists in its prompt or in cached discovery.
	AllowedTools map[string]bool
	// AuthorizeTool is the authoritative server-side tool check. It is called
	// after alias/canonical resolution and again immediately before execution,
	// after any queue wait, so a policy revoked while the request was queued
	// still denies the call. A nil callback keeps the legacy behavior (allow
	// after AllowedTools).
	AuthorizeTool func(ctx context.Context, call llm.ToolCallRequest, resolved ResolvedTool) (bool, string)
	// BeforeExecute runs after confirmation and after the request queue slot is
	// acquired but before the tool executes. A non-nil error denies the call
	// without executing it; this is where the daily quota ledger reserves.
	BeforeExecute func(ctx context.Context, call llm.ToolCallRequest, resolved ResolvedTool) error
}

func (r RunRequest) authorize(ctx context.Context, call llm.ToolCallRequest, resolved ResolvedTool) (bool, string) {
	if r.AuthorizeTool == nil {
		return true, ""
	}
	return r.AuthorizeTool(ctx, call, resolved)
}

type RunResult struct {
	Messages          []llm.LLMMessage
	PreparedCalls     []llm.ToolCallRequest
	ConfirmationExtra string
	Transcript        []storage.Message
	Stopped           bool
}

func (m *Manager) Run(ctx context.Context, deps RunnerDeps, req RunRequest) RunResult {
	if req.Session == nil || deps == nil {
		return RunResult{}
	}
	sessionID := req.Session.ID
	messages := make([]llm.LLMMessage, 0, len(req.Calls))
	preparedCalls := make([]llm.ToolCallRequest, 0, len(req.Calls))
	transcript := []storage.Message{}
	var confirmationExtra string
	batchPreviewSent := sendBatchToolPreview(ctx, deps, req)
	for _, original := range req.Calls {
		startedAt := storage.Now()
		call, err := deps.PrepareToolCall(ctx, req.Session, original)
		if err != nil {
			preparedCalls = append(preparedCalls, original)
			message := toolMessage(original.Name, original.ID, fmt.Sprintf("tool call %s failed: hook: %v", original.Name, err))
			messages = append(messages, message)
			transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
			continue
		}
		preparedCalls = append(preparedCalls, call)
		if !toolAllowed(req.AllowedTools, call.Name) {
			deps.AuditToolDenied(ctx, sessionID, call, tool.RiskHigh, "tool_not_in_group_allowlist")
			message := toolMessage(call.Name, call.ID, fmt.Sprintf("tool call %s denied: tool is not in this group's allowlist", call.Name))
			content := llm.SegmentsContentText(message.Segments)
			deps.RecordToolCall(ctx, sessionID, call, "denied", startedAt, content, fmt.Errorf("tool not in group allowlist"))
			messages = append(messages, message)
			transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
			continue
		}
		resolved := m.Resolve(ctx, call.Name, req.CachedTools)
		if allowed, reason := req.authorize(ctx, call, resolved); !allowed {
			deps.AuditToolDenied(ctx, sessionID, call, tool.RiskHigh, reason)
			message := toolMessage(call.Name, call.ID, fmt.Sprintf("tool call %s denied: %s", call.Name, reason))
			content := llm.SegmentsContentText(message.Segments)
			deps.RecordToolCall(ctx, sessionID, call, "denied", startedAt, content, fmt.Errorf("%s", reason))
			messages = append(messages, message)
			transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
			continue
		}
		toolCtx := deps.PrepareToolContext(ctx, req.Session, call)
		assessment, riskText := m.assessForRun(toolCtx, resolved, call)
		deps.AddToolUse(sessionID, call.Name)
		if err := m.PreflightConfirmation(toolCtx, resolved, call); err != nil {
			message := toolMessage(call.Name, call.ID, fmt.Sprintf("tool call %s failed: %v", call.Name, err))
			content := llm.SegmentsContentText(message.Segments)
			deps.RecordToolCall(ctx, sessionID, call, riskText, startedAt, content, err)
			messages = append(messages, message)
			transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
			continue
		}
		if !batchPreviewSent && deps.ShouldSendPreview(ctx, req.Session, call, req.AssistantText) {
			deps.SendPreview(ctx, fmt.Sprintf("正在调用 %s：%s", call.Name, previewArguments(call.Arguments)))
		}
		confirm, err := m.confirm(ctx, deps, req.Actor, sessionID, call, resolved, assessment)
		if !confirm.Allowed {
			if confirm.Stopped {
				return RunResult{Messages: messages, PreparedCalls: preparedCalls, ConfirmationExtra: confirmationExtra, Transcript: transcript, Stopped: true}
			}
			message := confirm.Message
			if len(message.Segments) == 0 {
				message = toolMessage(call.Name, call.ID, fmt.Sprintf("tool call %s failed: %v", call.Name, err))
			}
			messageText := llm.SegmentsContentText(message.Segments)
			deps.RecordToolCall(ctx, sessionID, call, riskText, startedAt, messageText, err)
			messages = append(messages, message)
			transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
			confirmationExtra = joinAssistantText(confirmationExtra, confirm.Extra)
			continue
		}
		confirmationExtra = joinAssistantText(confirmationExtra, confirm.Extra)
		runToolCtx, _, done, err := deps.StartToolRequest(ctx, sessionID, call.Name)
		if err != nil {
			content := fmt.Sprintf("tool call %s failed: %v", call.Name, err)
			message := toolMessage(call.Name, call.ID, content)
			deps.RecordToolCall(ctx, sessionID, call, riskText, startedAt, content, err)
			messages = append(messages, message)
			transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
			continue
		}
		runToolCtx = deps.PrepareToolContext(runToolCtx, req.Session, call)
		if allowed, reason := req.authorize(runToolCtx, call, resolved); !allowed {
			done()
			deps.AuditToolDenied(ctx, sessionID, call, tool.RiskHigh, reason)
			message := toolMessage(call.Name, call.ID, fmt.Sprintf("tool call %s denied: %s", call.Name, reason))
			content := llm.SegmentsContentText(message.Segments)
			deps.RecordToolCall(ctx, sessionID, call, "denied", startedAt, content, fmt.Errorf("%s", reason))
			messages = append(messages, message)
			transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
			continue
		}
		if req.BeforeExecute != nil {
			if err := req.BeforeExecute(runToolCtx, call, resolved); err != nil {
				done()
				reason := err.Error()
				deps.AuditToolDenied(ctx, sessionID, call, tool.RiskHigh, reason)
				message := toolMessage(call.Name, call.ID, fmt.Sprintf("tool call %s denied: %s", call.Name, reason))
				content := llm.SegmentsContentText(message.Segments)
				deps.RecordToolCall(ctx, sessionID, call, riskText, startedAt, content, err)
				messages = append(messages, message)
				transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
				continue
			}
		}
		if m.Media != nil {
			if err := m.Media.RetainSessionToolArguments(runToolCtx, sessionID, call.Arguments); err != nil {
				done()
				message := toolMessage(call.Name, call.ID, fmt.Sprintf("tool call %s failed: retain media arguments: %v", call.Name, err))
				content := llm.SegmentsContentText(message.Segments)
				deps.RecordToolCall(ctx, sessionID, call, riskText, startedAt, content, err)
				messages = append(messages, message)
				transcript = append(transcript, deps.ToolResultMessage(sessionID, message))
				continue
			}
		}
		result := m.Execute(runToolCtx, call, resolved, req.Actor)
		toolErr := runToolCtx.Err()
		done()
		if err := toolErr; err != nil {
			if ctx.Err() != nil {
				return RunResult{Messages: messages, PreparedCalls: preparedCalls, ConfirmationExtra: confirmationExtra, Transcript: transcript, Stopped: true}
			}
			result.Err = err
			result.Message = toolMessage(call.Name, call.ID, fmt.Sprintf("tool call %s canceled by user", call.Name))
		}
		if result.Result != nil && len(result.Result.Outputs) > 0 {
			if err := deps.SendOutputs(ctx, result.Result.Outputs); err != nil {
				result.Err = err
				result.Message.Segments = llm.TextSegments(fmt.Sprintf("tool call %s failed: output: %v", call.Name, err))
			}
		}
		completedSegments, hookErr := deps.CompleteToolCall(ctx, req.Session, call, riskText, result.Message.Segments, result.Err)
		if hookErr != nil {
			result.Err = hookErr
			result.Message.Segments = llm.TextSegments(fmt.Sprintf("tool call %s failed: hook: %v", call.Name, hookErr))
		} else {
			result.Message.Segments = completedSegments
		}
		resultText := llm.SegmentsContentText(result.Message.Segments)
		deps.RecordToolCall(ctx, sessionID, call, riskText, startedAt, resultText, result.Err)
		messages = append(messages, result.Message)
		if call.Name == "discover_tool" && result.Result != nil {
			deps.RememberDiscoveryResult(ctx, req.Session, result.Result)
		}
		transcript = append(transcript, deps.ToolResultMessage(sessionID, deps.PersistedToolMessage(result.Message)))
		if result.Err != nil {
			deps.SendPreview(ctx, fmt.Sprintf("%s 调用失败：%v", call.Name, result.Err))
		}
	}
	transcript = append([]storage.Message{deps.ToolCallMessage(sessionID, req.AssistantText, req.AssistantRawText, preparedCalls)}, transcript...)
	return RunResult{Messages: messages, PreparedCalls: preparedCalls, ConfirmationExtra: confirmationExtra, Transcript: transcript}
}

func sendBatchToolPreview(ctx context.Context, deps RunnerDeps, req RunRequest) bool {
	if len(req.Calls) <= 1 || !deps.ShouldSendPreview(ctx, req.Session, llm.ToolCallRequest{}, req.AssistantText) {
		return false
	}
	lines := make([]string, 0, len(req.Calls))
	for _, call := range req.Calls {
		lines = append(lines, fmt.Sprintf("正在调用 %s：%s", call.Name, previewArguments(call.Arguments)))
	}
	deps.SendPreview(ctx, strings.Join(lines, "\n"))
	return true
}

func (m *Manager) confirm(ctx context.Context, deps RunnerDeps, actor security.Actor, sessionID string, call llm.ToolCallRequest, resolved ResolvedTool, assessment tool.RiskAssessment) (ConfirmResult, error) {
	message := llm.LLMMessage{Role: llm.RoleTool, Name: call.Name, ToolCallID: call.ID}
	if !resolved.Available {
		message.Segments = llm.TextSegments(fmt.Sprintf("tool call %s failed: %s", call.Name, resolved.Reason))
		return ConfirmResult{Message: message}, fmt.Errorf("%s", resolved.Reason)
	}
	if resolved.Source == SourceKindELwisp {
		return ConfirmResult{Allowed: true}, nil
	}
	policy := policyForManager(ctx, m.Policy)
	if resolved.Native == nil {
		message.Segments = llm.TextSegments(fmt.Sprintf("tool call %s failed: native tool unavailable", call.Name))
		return ConfirmResult{Message: message}, fmt.Errorf("native tool unavailable")
	}
	info := resolved.Native.Info()
	if info.SuperadminOnly && actor.Role != security.RoleSuperadmin {
		deps.AuditToolDenied(ctx, sessionID, call, assessment.Level, "tool_requires_superadmin")
		message.Segments = llm.TextSegments(fmt.Sprintf("tool call %s denied: requires superadmin role; ask an administrator to add you to security.superadmins", call.Name))
		return ConfirmResult{Message: message}, fmt.Errorf("tool requires superadmin")
	}
	if !policy.CanUseTool(actor, assessment.Level, info.OwnerScoped) {
		deps.AuditToolDenied(ctx, sessionID, call, assessment.Level, "tool_risk_above_allowed_level")
		message.Segments = llm.TextSegments(fmt.Sprintf("tool call %s denied: risk %s is above your allowed tool level; ask an administrator to raise security.user_max_tool_risk or add you to security.superadmins", call.Name, assessment.Level))
		return ConfirmResult{Message: message}, fmt.Errorf("tool risk above allowed level")
	}
	if confirm, handled := deps.ConfirmBackgroundTool(ctx, sessionID, call, resolved, assessment); handled {
		return confirm, nil
	}
	if !policy.NeedsToolConfirmation(actor, assessment.Level) {
		return ConfirmResult{Allowed: true}, nil
	}
	return deps.ConfirmToolCall(ctx, sessionID, call, assessment, m.RiskDetail(ctx, resolved, call))
}

func (m *Manager) assessForRun(ctx context.Context, resolved ResolvedTool, call llm.ToolCallRequest) (tool.RiskAssessment, string) {
	assessment, err := m.AssessRisk(ctx, resolved, call.Arguments)
	if err != nil || assessment.Level == "" {
		return tool.RiskAssessment{Level: tool.RiskLow}, "unknown"
	}
	return assessment, string(assessment.Level)
}

func toolMessage(name, id, content string) llm.LLMMessage {
	return llm.LLMMessage{Role: llm.RoleTool, Name: name, ToolCallID: id, Segments: llm.TextSegments(content)}
}

func toolAllowed(allowlist map[string]bool, name string) bool {
	if allowlist == nil {
		return true
	}
	name = strings.ToLower(strings.TrimSpace(name))
	return allowlist[name]
}

func previewArguments(args string) string {
	args = compactArguments(args)
	const maxPreviewRunes = 160
	if len([]rune(args)) <= maxPreviewRunes {
		return args
	}
	runes := []rune(args)
	return string(runes[:maxPreviewRunes]) + "..."
}

func compactArguments(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "{}"
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(args)); err == nil {
		return compact.String()
	}
	return args
}

func joinAssistantText(first, second string) string {
	first = strings.TrimSpace(first)
	second = strings.TrimSpace(second)
	switch {
	case first == "":
		return second
	case second == "":
		return first
	default:
		return first + "\n\n" + second
	}
}
