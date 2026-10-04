package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"elbot/internal/angelmemory"
	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/ratelimit"
	"elbot/internal/security"
	"elbot/internal/tool"
)

const (
	angelForgetToolName = "angel_forget"

	// angelForgetMaxPerMinute bounds model-initiated deletions per actor and
	// scope. A looping model must not be able to erase a scope one id at a time;
	// mass deletion stays an explicit operator action (/memory delete, /forget).
	angelForgetMaxPerMinute = 3
)

// AngelMemoryToolOptions carries the operator decisions that change which
// long-memory tools exist at all.
type AngelMemoryToolOptions struct {
	// AllowForget registers angel_forget so the model can delete one memory entry
	// that belongs to the current speaker. Default false.
	AllowForget bool
}

// AngelForgetTool deletes one long-memory entry whose source actor is the
// current speaker. It never touches another member's memory, another scope, or
// rows without a structured source, and it requires an explicit confirm flag on
// top of the regular high-risk tool confirmation.
type AngelForgetTool struct {
	service   *angelmemory.Service
	deletions *ratelimit.Window
}

type angelForgetArgs struct {
	ID      string `json:"id"`
	Confirm bool   `json:"confirm"`
}

type angelForgetTarget struct {
	memory  *angelmemory.Memory
	actorID string
}

func (AngelForgetTool) Name() string { return angelForgetToolName }

func (t AngelForgetTool) Info() tool.Info { return angelForgetBuilder().BuildInfo() }

func (t AngelForgetTool) Schema() llm.ToolSchema { return angelForgetBuilder().BuildSchema() }

func (t AngelForgetTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	refusal, target, err := t.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	if refusal != "" {
		return &tool.Result{Content: refusal}, nil
	}
	if !target.confirmed {
		return &tool.Result{Content: angelForgetPreview(*target.memory)}, nil
	}
	if t.deletions != nil && !t.deletions.Allow(angelForgetRateKey(ctx, target.actorID)) {
		return &tool.Result{Content: fmt.Sprintf("同一范围内 1 分钟最多通过工具删除 %d 条长期记忆，请稍后再试，或让用户使用 /forget。", angelForgetMaxPerMinute)}, nil
	}
	if err := t.service.Delete(ctx, target.memory.Platform, target.memory.ScopeID, target.memory.ID); err != nil {
		return nil, err
	}
	return &tool.Result{Content: fmt.Sprintf("已删除长期记忆 %s。\n内容：%s", shortAngelMemoryID(target.memory.ID), angelForgetInline(target.memory.Content))}, nil
}

// PreflightConfirmation resolves the target before the user is asked to confirm,
// so a confirmation prompt never offers a memory the tool would refuse anyway.
func (t AngelForgetTool) PreflightConfirmation(ctx context.Context, req tool.CallRequest) error {
	refusal, _, err := t.resolve(ctx, req)
	if err != nil {
		return err
	}
	if refusal != "" {
		return errors.New(refusal)
	}
	return nil
}

// RiskDetail shows the exact entry that will be deleted, so the confirmation
// prompt is about content the user can recognise.
func (t AngelForgetTool) RiskDetail(ctx context.Context, req tool.CallRequest) (string, error) {
	refusal, target, err := t.resolve(ctx, req)
	if err != nil {
		return "", err
	}
	if refusal != "" {
		return "", errors.New(refusal)
	}
	var b strings.Builder
	b.WriteString("操作：删除长期记忆（仅限来源为当前发言人的记忆）\n")
	b.WriteString(fmt.Sprintf("id：%s\n", target.memory.ID))
	b.WriteString(fmt.Sprintf("强度：%d\n", target.memory.Strength))
	b.WriteString(fmt.Sprintf("内容：%s\n", angelForgetInline(target.memory.Content)))
	if source := angelForgetSourceText(*target.memory); source != "" {
		b.WriteString("来源：" + source + "\n")
	}
	b.WriteString("删除后无法恢复。")
	return b.String(), nil
}

// angelForgetResolved carries the validated target plus the confirm flag.
type angelForgetResolved struct {
	angelForgetTarget
	confirmed bool
}

func (t AngelForgetTool) resolve(ctx context.Context, req tool.CallRequest) (string, angelForgetResolved, error) {
	var resolved angelForgetResolved
	if t.service == nil || !t.service.Ready() {
		return "angel memory 服务未配置。", resolved, nil
	}
	msgCtx, ok := platform.MessageContextFrom(ctx)
	if !ok || strings.TrimSpace(msgCtx.Platform) == "" || strings.TrimSpace(msgCtx.ScopeID) == "" {
		return "当前上下文没有平台聊天信息，无法确定记忆范围。", resolved, nil
	}
	var args angelForgetArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return "", resolved, fmt.Errorf("parse %s arguments: %w", angelForgetToolName, err)
		}
	}
	prefix := strings.TrimSpace(args.ID)
	if prefix == "" {
		return "id 不能为空。请先用 angel_recall 取得记忆 id，再删除。", resolved, nil
	}
	actorID := ""
	if actor, ok := security.ActorFromContext(ctx); ok {
		actorID = strings.TrimSpace(actor.ID)
	}
	if actorID == "" {
		actorID = strings.TrimSpace(msgCtx.ActorID)
	}
	if actorID == "" {
		return "当前上下文没有足够的用户信息，无法确认这条记忆是否属于当前用户，已拒绝删除。", resolved, nil
	}
	// The filter is the permission boundary: only memories whose recorded source
	// actor is the current speaker can even be resolved. Entries written before
	// structured sources existed (empty source_actor_id) never match.
	filter := angelmemory.SourceFilter{ActorID: actorID}
	id, err := t.service.ResolveID(ctx, msgCtx.Platform, msgCtx.ScopeID, prefix, filter)
	switch {
	case errors.Is(err, angelmemory.ErrNotFound):
		return fmt.Sprintf("当前范围没有找到 id 前缀为 %q 且来源为你自己的长期记忆。", prefix), resolved, nil
	case errors.Is(err, angelmemory.ErrAmbiguousID):
		return fmt.Sprintf("id 前缀 %q 不唯一，请提供更多字符。", prefix), resolved, nil
	case err != nil:
		return "", resolved, err
	}
	memory, err := t.service.Get(ctx, msgCtx.Platform, msgCtx.ScopeID, id)
	if err != nil {
		return "", resolved, err
	}
	if strings.TrimSpace(memory.SourceActorID) == "" || memory.SourceActorID != actorID {
		return "这条记忆的来源成员不是当前用户，已拒绝删除。", resolved, nil
	}
	resolved.memory = memory
	resolved.actorID = actorID
	resolved.confirmed = args.Confirm
	return "", resolved, nil
}

func angelForgetRateKey(ctx context.Context, actorID string) string {
	msgCtx, _ := platform.MessageContextFrom(ctx)
	return strings.Join([]string{msgCtx.Platform, msgCtx.ScopeID, actorID}, "\x00")
}

func angelForgetPreview(memory angelmemory.Memory) string {
	lines := []string{
		"待删除的长期记忆（尚未删除）：",
		fmt.Sprintf("id：%s", memory.ID),
		fmt.Sprintf("内容：%s", angelForgetInline(memory.Content)),
	}
	if source := angelForgetSourceText(memory); source != "" {
		lines = append(lines, "来源："+source)
	}
	lines = append(lines, "确认删除请再次调用 angel_forget，并传 confirm=true；删除后无法恢复。")
	return strings.Join(lines, "\n")
}

func angelForgetSourceText(memory angelmemory.Memory) string {
	parts := make([]string, 0, 4)
	if kind := strings.TrimSpace(memory.SourceKind); kind != "" {
		parts = append(parts, "kind="+kind)
	}
	if messageID := strings.TrimSpace(memory.SourceMessageID); messageID != "" {
		parts = append(parts, "message="+messageID)
	}
	if sessionID := strings.TrimSpace(memory.SourceSessionID); sessionID != "" {
		parts = append(parts, "session="+sessionID)
	}
	return strings.Join(parts, " ")
}

func angelForgetInline(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return content
}

func shortAngelMemoryID(id string) string {
	id = strings.TrimSpace(id)
	runes := []rune(id)
	if len(runes) <= 12 {
		return id
	}
	return string(runes[:12])
}

func angelForgetBuilder() *tool.Builder {
	return tool.NewBuilder(angelForgetToolName).
		Description("删除一条来源为当前发言人的长期记忆；只在当前用户明确要求删除某条记忆时使用。第一次调用返回待删除内容，带 confirm=true 再次调用才会真正删除。").
		Risk(tool.RiskHigh).
		Tags("memory").
		OwnerScoped().
		Hidden().
		String("id", "要删除的记忆 id 或唯一前缀，来自 angel_recall 的结果。", tool.Required()).
		Boolean("confirm", "首次调用省略或传 false，只返回待删除内容；确认删除时传 true。")
}
