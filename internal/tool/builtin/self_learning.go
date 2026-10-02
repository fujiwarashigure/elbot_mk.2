package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"elbot/internal/llm"
	"elbot/internal/platform"
	"elbot/internal/security"
	"elbot/internal/selflearning"
	"elbot/internal/tool"
	"elbot/internal/tool/runtimeinfo"
)

const selfLearningReviewToolName = "self_learning_review"

type SelfLearningReviewTool struct {
	service *selflearning.Service
	info    runtimeinfo.Info
}

type selfLearningReviewArgs struct {
	Action   string `json:"action"`
	Status   string `json:"status"`
	ID       string `json:"id"`
	Meaning  string `json:"meaning"`
	MinCount int    `json:"min_count"`
	Limit    int    `json:"limit"`
}

func NewSelfLearningReviewTool(service *selflearning.Service, infos ...runtimeinfo.Info) SelfLearningReviewTool {
	return SelfLearningReviewTool{service: service, info: runtimeinfo.First(infos...)}
}

func (SelfLearningReviewTool) Name() string { return selfLearningReviewToolName }

func (t SelfLearningReviewTool) Info() tool.Info { return selfLearningReviewBuilder().BuildInfo() }

func (t SelfLearningReviewTool) Schema() llm.ToolSchema {
	return selfLearningReviewBuilder().BuildSchema()
}

func (t SelfLearningReviewTool) Call(ctx context.Context, req tool.CallRequest) (*tool.Result, error) {
	if t.service == nil || !t.service.Ready() {
		return &tool.Result{Content: "self learning 服务未配置。"}, nil
	}
	msgCtx, ok := platform.MessageContextFrom(ctx)
	if !ok || strings.TrimSpace(msgCtx.Platform) == "" || strings.TrimSpace(msgCtx.ScopeID) == "" {
		return &tool.Result{Content: "当前上下文没有平台聊天信息，无法确定学习范围。"}, nil
	}
	var args selfLearningReviewArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("parse self_learning_review arguments: %w", err)
		}
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	switch action {
	case "", "status":
		pending, approved, err := t.service.Stats(ctx, msgCtx.Platform, msgCtx.ScopeID)
		if err != nil {
			return nil, err
		}
		return &tool.Result{Content: fmt.Sprintf("self learning：待审 %d 条，已批准 %d 条。", pending, approved)}, nil
	case "mine":
		stats, err := t.service.Mine(ctx, msgCtx.Platform, msgCtx.ScopeID, args.MinCount, args.Limit)
		if err != nil {
			return nil, err
		}
		return &tool.Result{Content: fmt.Sprintf("候选挖掘完成：新增 %d 条，更新 %d 条，跳过 %d 条。", stats.Created, stats.Updated, stats.Skipped)}, nil
	case "list":
		status := strings.TrimSpace(args.Status)
		if status == "" {
			status = selflearning.StatusPending
		}
		candidates, err := t.service.Review(ctx, msgCtx.Platform, msgCtx.ScopeID, status, args.Limit)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			return &tool.Result{Content: "没有符合条件的候选。"}, nil
		}
		lines := []string{fmt.Sprintf("候选 %d 条：", len(candidates))}
		for i, candidate := range candidates {
			lines = append(lines, fmt.Sprintf("%d. [%s] id=%s count=%d pattern=%s", i+1, candidate.Status, candidate.ID, candidate.Count, candidate.Pattern))
		}
		return &tool.Result{Content: strings.Join(lines, "\n")}, nil
	case "approve", "reject":
		if strings.TrimSpace(args.ID) == "" {
			return &tool.Result{Content: "approve/reject 需要 id。"}, nil
		}
		status := selflearning.StatusApproved
		if action == "reject" {
			status = selflearning.StatusRejected
		}
		if err := t.service.Decide(ctx, msgCtx.Platform, msgCtx.ScopeID, args.ID, status, args.Meaning, toolReviewer(ctx)); err != nil {
			return nil, err
		}
		return &tool.Result{Content: fmt.Sprintf("候选 %s 已标记为 %s。", args.ID, status)}, nil
	case "undo":
		if strings.TrimSpace(args.ID) == "" {
			return &tool.Result{Content: "undo 需要 id。"}, nil
		}
		if err := t.service.Undo(ctx, msgCtx.Platform, msgCtx.ScopeID, args.ID, toolReviewer(ctx)); err != nil {
			return nil, err
		}
		return &tool.Result{Content: fmt.Sprintf("候选 %s 已撤回为待审。", args.ID)}, nil
	default:
		return &tool.Result{Content: "action 只支持 status、mine、list、approve、reject、undo。"}, nil
	}
}

func toolReviewer(ctx context.Context) string {
	actor, ok := security.ActorFromContext(ctx)
	if !ok {
		return "tool"
	}
	for _, value := range []string{actor.ID, actor.PlatformUserID, actor.Nickname, actor.DisplayName} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "tool"
}

func selfLearningReviewBuilder() *tool.Builder {
	return tool.NewBuilder(selfLearningReviewToolName).
		Description("管理 clean-room 表达/黑话候选：查看状态、挖掘候选、列出、批准、拒绝或撤回。批准后的内容才会注入上下文。").
		Risk(tool.RiskHigh).
		SuperadminOnly().
		Tags("learning").
		String("action", "status、mine、list、approve、reject、undo；默认 status。").
		String("status", "list 时过滤 pending/approved/rejected。").
		String("id", "approve/reject/undo 的候选 ID。").
		String("meaning", "approve 黑话时可选的含义。").
		Integer("min_count", "mine 时候选最低出现次数。").
		Integer("limit", "返回/生成数量上限。")
}
