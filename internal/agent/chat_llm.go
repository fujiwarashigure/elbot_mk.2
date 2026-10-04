package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/delivery"
	"elbot/internal/hook"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/redact"
	"elbot/internal/storage"
)

type llmCallResult struct {
	Text      string
	RawText   string
	Usage     *llm.Usage
	ToolCalls []llm.ToolCallRequest
	Outputs   []delivery.Output
	Messages  []llm.LLMMessage
	Stream    delivery.MessageStream
}

func (a *Agent) callLLM(ctx context.Context, sessionID string, selection config.ModelSelection, messages []llm.LLMMessage, tools []llm.ToolSchema, pending *pendingUserMessage, requestOptions llmRequestOptions, stream delivery.MessageStream, out turnOutput) (llmCallResult, error) {
	startedAt := time.Now()
	baseMessages := llm.CloneMessages(messages)
	hookMessage := hook.MessagePayload{}
	if pending != nil {
		hookMessage = hook.MessagePayload{
			ID:           pending.message.ID,
			Role:         string(llm.RoleUser),
			PlatformText: pending.platformText,
			Segments:     append([]llm.MessageSegment(nil), baseMessages[pending.messageIndex].Segments...),
		}
	}
	event, err := a.runHook(ctx, hook.Event{
		Point:   hook.PointLLMRequestPrepared,
		Session: hook.SessionContext{ID: sessionID},
		Message: hookMessage,
		LLM: hook.LLMPayload{
			Provider: selection.Provider,
			Model:    selection.Model,
			Messages: llm.CloneMessages(baseMessages),
			Tools:    tools,
		},
	})
	if err != nil {
		if pending != nil {
			if persistErr := a.persistTurnMessage(ctx, &pending.message, "append_pending_user_message"); persistErr != nil {
				err = errors.Join(err, persistErr)
			}
		}
		return llmCallResult{}, fmt.Errorf("llm request hook: %w", err)
	}
	selection.Provider = event.LLM.Provider
	selection.Model = event.LLM.Model
	if err := a.authorizeExecutionModelSelection(ctx, selection); err != nil {
		if pending != nil {
			if persistErr := a.persistTurnMessage(ctx, &pending.message, "append_pending_user_message"); persistErr != nil {
				err = errors.Join(err, persistErr)
			}
		}
		return llmCallResult{}, fmt.Errorf("llm request hook: %w", err)
	}
	tools = event.LLM.Tools
	requestOptions = mergeLLMRequestOptions(requestOptions, llmRequestOptionsFromPayload(event.LLM))
	if pending != nil {
		segments := a.materializeMedia(ctx, event.Message.Segments)
		baseMessages[pending.messageIndex].Segments = segments
		pending.message.Content = llm.SegmentsContentText(segments)
		pending.message.Segments = storedMessageSegments(segments)
		if err := a.persistTurnMessage(ctx, &pending.message, "append_pending_user_message"); err != nil {
			return llmCallResult{}, err
		}
	}
	requestMessages := baseMessages
	if a.media != nil {
		seen := map[string]bool{}
		for _, message := range baseMessages {
			for _, segment := range message.Segments {
				if segment.MediaID == "" || seen[segment.MediaID] {
					continue
				}
				seen[segment.MediaID] = true
				release, err := a.media.Hold(ctx, segment.MediaID)
				if err != nil {
					return llmCallResult{}, err
				}
				defer release()
			}
		}
		var cleanup func()
		requestMessages, cleanup, err = a.media.ResolveForLLM(ctx, baseMessages)
		if err != nil {
			return llmCallResult{}, err
		}
		defer cleanup()
	}
	for _, part := range requestOptions.SystemAppend {
		requestMessages = llm.AppendSystemSegmentText(requestMessages, part)
	}
	if err := a.guardPromptBeforeCall(ctx, sessionID, selection, requestMessages, tools, out); err != nil {
		return llmCallResult{Messages: baseMessages, Stream: stream}, err
	}
	req := llm.ChatRequest{
		Model:     selection.Model,
		SessionID: sessionID,
		Messages:  requestMessages,
		Tools:     tools,
	}
	if requestOptions.Temperature != nil {
		req.Temperature = *requestOptions.Temperature
	}
	if requestOptions.MaxTokens != nil {
		req.MaxTokens = *requestOptions.MaxTokens
	}
	if len(requestOptions.ExtraBody) > 0 {
		req.ExtraBody = requestOptions.ExtraBody
	}
	// Tracked before the first byte is read: the transparent vision fallback is
	// only safe while nothing has been streamed to the user or fed to the tool
	// runner, so these accumulators must be visible at both fallback sites.
	var assistant strings.Builder
	var usage *llm.Usage
	var toolCalls []llm.ToolCallRequest
	showReasoning := a.shouldShowCLIReasoning(ctx)
	reasoningOpen := false

	if err := a.checkChatBudget(ctx, selection); err != nil {
		return llmCallResult{Messages: baseMessages, Stream: stream}, err
	}
	usageCallID := storage.NewID()
	releaseProvider, err := a.acquireProviderSlot(ctx, selection.Provider)
	if err != nil {
		a.audit("provider_concurrency_rejected", "provider", selection.Provider, "error", err.Error())
		return llmCallResult{Messages: baseMessages, Stream: stream}, err
	}
	defer releaseProvider()
	ch, err := a.clientForProvider(selection.Provider).ChatStream(ctx, req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return llmCallResult{Messages: baseMessages, Stream: stream}, nil
		}
		if shouldFallbackVision(requestMessages, err) && visionFallbackTransparent(assistant.Len(), len(toolCalls), reasoningOpen) && !visionFallbackAttempted(ctx) {
			a.notifyVisionFallbackOnce(ctx, sessionID, out)
			releaseProvider()
			return a.callLLM(withVisionFallbackAttempt(ctx), sessionID, selection, a.visionFallbackMessages(ctx, baseMessages), tools, nil, requestOptions, stream, out)
		}
		a.audit("llm_error", "session_id", sessionID, "provider", selection.Provider, "model", selection.Model, "elapsed_ms", elapsedMillis(startedAt), "error", redact.Error(err))
		a.notifyHookError(ctx, hook.Event{Point: hook.PointLLMResponseReceived, Session: hook.SessionContext{ID: sessionID}, LLM: hook.LLMPayload{Provider: selection.Provider, Model: selection.Model, ElapsedMS: elapsedMillis(startedAt)}}, err)
		return llmCallResult{}, fmt.Errorf("chat: %w", err)
	}
	for chunk := range ch {
		if !a.turnOutputAllowed(ctx) {
			content := assistant.String()
			return llmCallResult{Text: content, RawText: content, Usage: usage, ToolCalls: toolCalls, Messages: baseMessages, Stream: stream}, nil
		}
		if chunk.Error != nil {
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				content := assistant.String()
				return llmCallResult{Text: content, RawText: content, Usage: usage, ToolCalls: toolCalls, Messages: baseMessages, Stream: stream}, nil
			}
			if shouldFallbackVision(requestMessages, chunk.Error) {
				if visionFallbackTransparent(assistant.Len(), len(toolCalls), reasoningOpen) && !visionFallbackAttempted(ctx) {
					a.notifyVisionFallbackOnce(ctx, sessionID, out)
					releaseProvider()
					return a.callLLM(withVisionFallbackAttempt(ctx), sessionID, selection, a.visionFallbackMessages(ctx, baseMessages), tools, nil, requestOptions, stream, out)
				}
				// The image rejection arrived after user-visible output, reasoning
				// or tool-call deltas. Replaying the request would stream a second
				// answer (and re-emit tool calls), so surface the failure instead
				// of retrying transparently.
				out.SendNotice(ctx, slog.LevelWarn, visionFallbackBlockedNotice)
				return llmCallResult{}, markUserNotified(fmt.Errorf("chat stream: %w", chunk.Error))
			}
			details := newUserErrorDetails("LLM 响应中断", chunk.Error)
			a.audit("llm_error", "session_id", sessionID, "provider", selection.Provider, "model", selection.Model, "elapsed_ms", elapsedMillis(startedAt), "error_id", details.ID, "error", details.Safe)
			a.notifyHookError(ctx, hook.Event{Point: hook.PointLLMResponseReceived, Session: hook.SessionContext{ID: sessionID}, LLM: hook.LLMPayload{Provider: selection.Provider, Model: selection.Model, SourceText: assistant.String(), Text: assistant.String(), ToolCalls: toolCalls, Usage: usage, ElapsedMS: elapsedMillis(startedAt)}}, errors.New(details.Safe))
			out.SendNotice(ctx, slog.LevelError, details.Text)

			return llmCallResult{}, markUserNotified(fmt.Errorf("chat stream: %w", chunk.Error))
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if chunk.DeltaReasoningContent != "" && showReasoning {
			if !reasoningOpen {
				out.SendReasoning(ctx, "[thinking] ")
				reasoningOpen = true
			}
			out.SendReasoning(ctx, chunk.DeltaReasoningContent)
		}
		for _, delta := range chunk.ToolCallDeltas {
			toolCalls = append(toolCalls, llm.ToolCallRequest{ID: delta.ID, Name: delta.Name, Arguments: delta.Args})
		}
		delta := chunk.DeltaContent
		assistant.WriteString(delta)
		if stream != nil && delta != "" {
			if !a.turnOutputAllowed(ctx) {
				content := assistant.String()
				return llmCallResult{Text: content, RawText: content, Usage: usage, ToolCalls: toolCalls, Messages: baseMessages, Stream: stream}, nil
			}
			if err := stream.Append(ctx, delta); err != nil {
				return llmCallResult{}, fmt.Errorf("stream append: %w", err)
			}
		}
	}
	if !a.turnOutputAllowed(ctx) {
		content := assistant.String()
		return llmCallResult{Text: content, RawText: content, Usage: usage, ToolCalls: toolCalls, Messages: baseMessages, Stream: stream}, nil
	}
	if reasoningOpen {
		out.SendReasoning(ctx, "[/thinking]\n\n")
	}
	elapsedMs := elapsedMillis(startedAt)
	content := assistant.String()
	event, err = a.runHook(ctx, hook.Event{
		Point:   hook.PointLLMResponseReceived,
		Session: hook.SessionContext{ID: sessionID},
		LLM: hook.LLMPayload{
			Provider:   selection.Provider,
			Model:      selection.Model,
			Usage:      usage,
			SourceText: content,
			Text:       content,
			ToolCalls:  toolCalls,
			ElapsedMS:  elapsedMs,
		},
	})
	if err != nil {
		return llmCallResult{}, fmt.Errorf("llm response hook: %w", err)
	}
	usage = event.LLM.Usage
	toolCalls = event.LLM.ToolCalls
	finalText := event.LLM.Text
	a.logLLMOutput(ctx, sessionID, selection, finalText, event.LLM.SourceText, len(toolCalls), elapsedMs)

	a.auditUsage(sessionID, selection, usage, elapsedMs)
	a.recordChatUsage(ctx, selection.Model, usage, usageCallID)
	return llmCallResult{Text: finalText, RawText: content, Usage: usage, ToolCalls: toolCalls, Outputs: event.Outputs, Messages: baseMessages, Stream: stream}, nil
}

func (a *Agent) logLLMOutput(ctx context.Context, sessionID string, selection config.ModelSelection, text, rawText string, toolCallCount int, elapsedMs int64) {
	if a.logger == nil {
		return
	}
	if !a.historyEnabled(ctx) {
		a.logger.Info("llm output", "event", "assistant_message", "session_id", sessionID, "provider", selection.Provider, "model", selection.Model, "elapsed_ms", elapsedMs, "tool_call_count", toolCallCount, "history", "off")
		return
	}
	a.logger.Info("llm output",
		"event", "assistant_message",
		"session_id", sessionID,
		"provider", selection.Provider,
		"model", selection.Model,
		"elapsed_ms", elapsedMs,
		"text", previewLogText(text),
		"raw_text", previewLogText(rawText),
		"tool_call_count", toolCallCount,
	)
}

// shouldFallbackVision reports whether an image-bearing request failed because
// the selected model cannot accept images, so the caller may retry with the
// images replaced by text references.
//
// The decision is made purely on APIError.Category, which the adapter derives
// from the response status plus the machine-readable code/type/param fields
// (including the one documented prose dialect that only reports image
// rejections in a message). The human-readable Message is never classified
// here: it is provider-controlled text and is user-visible, so a model named
// "image-chat-v2" or a temperature error mentioning "image-lab" can never make
// an image request silently lose its images.
func shouldFallbackVision(messages []llm.LLMMessage, err error) bool {
	if err == nil || !llm.MessagesHaveImageSegment(messages) {
		return false
	}
	apiErr, ok := llm.AsAPIError(err)
	if !ok || apiErr.Category != llm.ErrorCategoryVisionUnsupported {
		return false
	}
	// Defensive gate: only the statuses a capability rejection can arrive with.
	// A rate limit or a server error must never strip the images and retry.
	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound:
		return true
	default:
		return false
	}
}

// visionFallbackBlockedNotice is shown when the model rejects images only after
// it has already produced user-visible output, where a transparent retry would
// duplicate the answer.
const visionFallbackBlockedNotice = "模型在输出过程中报告了图片不受支持；为避免重复输出，本轮未自动改用图片描述，请重试或切换支持视觉的模型。"

// visionFallbackTransparent reports whether a failed request can be retried with
// image descriptions without replaying anything the user already saw. Streamed
// answer text, reasoning and tool-call deltas all count as side effects: the
// retry would emit them a second time, so once any is present the caller must
// surface the failure instead of falling back transparently.
func visionFallbackTransparent(assistantBytes, toolCallCount int, reasoningOpen bool) bool {
	return assistantBytes == 0 && toolCallCount == 0 && !reasoningOpen
}

type visionFallbackAttemptKey struct{}

// withVisionFallbackAttempt marks the request as already retried behind an image
// description. Together with the gateway check in shouldFallbackVision this
// guarantees at most one fallback per turn even if an image segment somehow
// survives the rewrite.
func withVisionFallbackAttempt(ctx context.Context) context.Context {
	return context.WithValue(ctx, visionFallbackAttemptKey{}, true)
}

func visionFallbackAttempted(ctx context.Context) bool {
	attempted, _ := ctx.Value(visionFallbackAttemptKey{}).(bool)
	return attempted
}

func fallbackVisionMessages(messages []llm.LLMMessage) []llm.LLMMessage {
	out := append([]llm.LLMMessage(nil), messages...)
	for i := range out {
		if len(out[i].Segments) == 0 {
			continue
		}
		out[i].Segments = llm.TextSegments(llm.SegmentsContentText(out[i].Segments))
	}
	return out
}

func (a *Agent) notifyVisionFallbackOnce(ctx context.Context, sessionID string, out turnOutput) {
	if !a.shouldShowCLIReasoning(ctx) {
		return
	}
	a.visionFallbackMu.Lock()
	if a.visionFallbackNotified[sessionID] {
		a.visionFallbackMu.Unlock()
		return
	}
	a.visionFallbackNotified[sessionID] = true
	a.visionFallbackMu.Unlock()
	_, _ = out.SendAssistant(ctx, "当前模型似乎不支持视觉，图片已按文本描述处理。")
}

func (a *Agent) userMessageSegments(ctx context.Context, text string) []llm.MessageSegment {
	if msg, ok := platform.MessageContextFrom(ctx); ok && len(msg.Segments) > 0 {
		return platformSegmentsToLLM(msg.Segments, text)
	}
	return llm.TextSegments(text)
}

func platformSegmentsToLLM(segments []platform.MessageSegment, fallbackText string) []llm.MessageSegment {
	out := make([]llm.MessageSegment, 0, len(segments))
	for _, segment := range segments {
		switch segment.Type {
		case platform.SegmentText:
			if segment.Text != "" {
				out = append(out, llm.MessageSegment{Type: llm.SegmentText, Text: segment.Text})
			}
		case platform.SegmentImage:
			if segment.URL != "" || segment.MediaID != "" {
				out = append(out, llm.MessageSegment{Type: llm.SegmentImage, MediaID: segment.MediaID, URL: segment.URL, MIMEType: segment.MIMEType, Name: segment.Name})
			} else {
				out = append(out, llm.MessageSegment{Type: llm.SegmentText, Text: fileSegmentText(segment.Name, "图片")})
			}
		case platform.SegmentFile:
			// TODO: 后续支持语音、视频和普通文件的真实模型输入；当前统一回滚为文本描述。
			out = append(out, llm.MessageSegment{Type: llm.SegmentFile, MediaID: segment.MediaID, URL: segment.URL, Text: fileSegmentText(segment.Name, segment.Text), MIMEType: segment.MIMEType, Name: segment.Name})
		}
	}
	if len(out) == 0 {
		return llm.TextSegments(fallbackText)
	}
	return out
}

func fileSegmentText(name, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		fallback = "文件"
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "[" + fallback + "]"
	}
	return fmt.Sprintf("[%s: %s]", fallback, name)
}

func (a *Agent) isCLIContext(ctx context.Context) bool {
	if msg, ok := platform.MessageContextFrom(ctx); ok {
		return msg.Platform == "cli"
	}
	return a.platform != nil && a.platform.Name() == "cli"
}

func (a *Agent) shouldShowCLIReasoning(ctx context.Context) bool {
	return a.isCLIContext(ctx)
}

type cliReasoningSender interface {
	SendReasoning(context.Context, string) error
}

func (a *Agent) sendCLIReasoning(ctx context.Context, text string) {
	if !a.shouldShowCLIReasoning(ctx) || text == "" {
		return
	}
	if msg, ok := platform.MessageContextFrom(ctx); ok {
		if sender, ok := msg.Sender.(cliReasoningSender); ok {
			_ = sender.SendReasoning(ctx, text)
			return
		}
	}
	if sender, ok := a.platform.(cliReasoningSender); ok {
		_ = sender.SendReasoning(ctx, text)
	}
}

func (a *Agent) auditUsage(sessionID string, selection config.ModelSelection, usage *llm.Usage, elapsedMs int64) {
	attrs := []any{"session_id", sessionID, "provider", selection.Provider, "model", selection.Model, "elapsed_ms", elapsedMs}
	if usage != nil {
		attrs = append(attrs,
			"prompt_tokens", usage.PromptTokens,
			"completion_tokens", usage.CompletionTokens,
			"total_tokens", usage.TotalTokens,
			"cache_hit_tokens", usage.CacheHitTokens,
		)
	}
	a.audit("llm_usage", attrs...)
}

func elapsedMillis(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}
