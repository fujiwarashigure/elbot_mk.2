package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/platform"
)

func hardBudgetTestMessages() []llm.LLMMessage {
	return []llm.LLMMessage{{Role: llm.RoleUser, Segments: llm.TextSegments("hello")}}
}

func TestHardChatBudgetPreReserveRejectsConcurrentOvershoot(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{
		GlobalChatTokensDaily:            1000,
		ChatHardLimit:                    true,
		ChatHardLimitReserveOutputTokens: 600,
	}
	ctx := context.Background()
	selection := config.ModelSelection{Provider: "default", Model: "test-model"}
	messages := hardBudgetTestMessages()

	first, err := a.beginChatBudget(ctx, selection, messages, nil, 0, "call-1")
	if err != nil {
		t.Fatalf("first reservation: %v", err)
	}
	if first == nil {
		t.Fatal("hard mode should return a reservation")
	}
	if _, err := a.beginChatBudget(ctx, selection, messages, nil, 0, "call-2"); err == nil {
		t.Fatal("second concurrent reservation should be rejected")
	}

	// Settle the first call with real usage. The difference must be released
	// so a later call can fit again.
	a.settleChatBudget(ctx, first, &llm.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}, false)
	a.budgetMu.Lock()
	used := a.budgetTokens[first.key]
	uncertain := a.budgetUncertain[first.key]
	a.budgetMu.Unlock()
	if used != 30 {
		t.Fatalf("settled tokens = %d, want 30", used)
	}
	if uncertain != 0 {
		t.Fatalf("settled entry should not be uncertain: %d", uncertain)
	}
	third, err := a.beginChatBudget(ctx, selection, messages, nil, 0, "call-3")
	if err != nil || third == nil {
		t.Fatalf("third reservation after settlement: %v", err)
	}
}

func TestHardChatBudgetKeepsReservedAmountWhenUsageMissing(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{
		GlobalChatTokensDaily:            10000,
		ChatHardLimit:                    true,
		ChatHardLimitReserveOutputTokens: 700,
	}
	ctx := context.Background()
	selection := config.ModelSelection{Provider: "default", Model: "test-model"}
	reservation, err := a.beginChatBudget(ctx, selection, hardBudgetTestMessages(), nil, 0, "call-uncertain")
	if err != nil || reservation == nil {
		t.Fatalf("reservation: %v", err)
	}
	a.settleChatBudget(ctx, reservation, nil, false)

	a.budgetMu.Lock()
	charged := a.budgetTokens[reservation.key]
	uncertain := a.budgetUncertain[reservation.key]
	a.budgetMu.Unlock()
	if charged != reservation.reservedTokens {
		t.Fatalf("missing usage charged %d, want reserved %d", charged, reservation.reservedTokens)
	}
	if uncertain == 0 {
		t.Fatal("missing usage should be marked uncertain")
	}
}

func TestHardChatBudgetRejectsCostLimitWithoutPrice(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{
		GlobalChatCostDaily: 1,
		ChatHardLimit:       true,
	}
	a.pricing = config.DailyReportConfig{}
	_, err := a.beginChatBudget(context.Background(), config.ModelSelection{Provider: "default", Model: "test-model"}, hardBudgetTestMessages(), nil, 0, "call-no-price")
	if err == nil {
		t.Fatal("cost hard limit without price should be rejected")
	}
	if !strings.Contains(err.Error(), "价格") {
		t.Fatalf("error = %v", err)
	}
}

func TestHardChatBudgetWorksForGroupPolicyQuota(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{
		ChatHardLimit:                    true,
		ChatHardLimitReserveOutputTokens: 600,
	}
	scope := a.scope(platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qqonebot",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
	}))
	a.setGroupPolicyForScope(scope, config.GroupPolicyConfig{ChatTokensQuota: 1000})
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qqonebot",
		ScopeID:          "group:9",
		ConversationKind: platform.ConversationGroup,
	})
	selection := config.ModelSelection{Provider: "default", Model: "test-model"}
	if _, err := a.beginChatBudget(ctx, selection, hardBudgetTestMessages(), nil, 0, "group-1"); err != nil {
		t.Fatalf("group reservation: %v", err)
	}
	if _, err := a.beginChatBudget(ctx, selection, hardBudgetTestMessages(), nil, 0, "group-2"); err == nil {
		t.Fatal("second group reservation should be rejected")
	}
}

func TestSettleChatBudgetWritesToOriginalDayKey(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{
		GlobalChatTokensDaily:            10000,
		ChatHardLimit:                    true,
		ChatHardLimitReserveOutputTokens: 600,
	}
	ctx := context.Background()
	yesterday := time.Now().AddDate(0, 0, -1)
	key := budgetKey(yesterday, "chat", "unknown", "", "old-call")
	reservation := &chatBudgetReservation{key: key, model: "test-model", reservedTokens: 700}
	a.budgetMu.Lock()
	a.budgetTokens = map[string]int64{key: 700}
	a.budgetUncertain = map[string]int64{key: yesterday.Unix()}
	a.budgetMu.Unlock()

	a.settleChatBudget(ctx, reservation, &llm.Usage{TotalTokens: 5}, false)

	a.budgetMu.Lock()
	got := a.budgetTokens[key]
	_, stillUncertain := a.budgetUncertain[key]
	a.budgetMu.Unlock()
	if got != 5 {
		t.Fatalf("old key tokens = %d, want 5", got)
	}
	if stillUncertain {
		t.Fatal("settled old reservation should clear uncertain")
	}
}
func TestHardChatBudgetEndToEndMarksMissingUsageUncertain(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{replies: []string{"answer"}}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{
		GlobalChatTokensDaily:            100000,
		ChatHardLimit:                    true,
		ChatHardLimitReserveOutputTokens: 600,
	}
	if err := a.HandleMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	a.budgetMu.Lock()
	total := int64(0)
	for _, value := range a.budgetTokens {
		total += value
	}
	uncertain := len(a.budgetUncertain)
	a.budgetMu.Unlock()
	if total <= 0 {
		t.Fatal("hard budget should charge a conservative estimate when usage is missing")
	}
	if uncertain == 0 {
		t.Fatal("missing usage should be marked uncertain")
	}
}
func TestHardChatBudgetEndToEndSettlesActualUsage(t *testing.T) {
	fake := &fakeLLM{chunks: [][]llm.StreamChunk{{
		{DeltaContent: "answer", Usage: &llm.Usage{TotalTokens: 42, PromptTokens: 30, CompletionTokens: 12}},
	}}}
	a := New(&fakePlatform{}, fake, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{
		GlobalChatTokensDaily:            100000,
		ChatHardLimit:                    true,
		ChatHardLimitReserveOutputTokens: 600,
	}
	if err := a.HandleMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	a.budgetMu.Lock()
	total := int64(0)
	for _, value := range a.budgetTokens {
		total += value
	}
	uncertain := len(a.budgetUncertain)
	a.budgetMu.Unlock()
	if total != 42 {
		t.Fatalf("settled tokens = %d, want 42", total)
	}
	if uncertain != 0 {
		t.Fatalf("settled usage should not be uncertain: %d", uncertain)
	}
}
func TestSettleChatBudgetReleaseRemovesReservation(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.budgetLimits = config.BudgetLimitsConfig{
		GlobalChatTokensDaily:            10000,
		ChatHardLimit:                    true,
		ChatHardLimitReserveOutputTokens: 600,
	}
	ctx := context.Background()
	reservation, err := a.beginChatBudget(ctx, config.ModelSelection{Provider: "default", Model: "test-model"}, hardBudgetTestMessages(), nil, 0, "call-release")
	if err != nil || reservation == nil {
		t.Fatalf("reservation: %v", err)
	}
	a.settleChatBudget(ctx, reservation, nil, true)
	a.budgetMu.Lock()
	_, tokenEntry := a.budgetTokens[reservation.key]
	_, costEntry := a.budgetCosts[reservation.key]
	_, uncertain := a.budgetUncertain[reservation.key]
	a.budgetMu.Unlock()
	if tokenEntry || costEntry || uncertain {
		t.Fatalf("released reservation leaked: tokens=%v costs=%v uncertain=%v", tokenEntry, costEntry, uncertain)
	}
	if next, err := a.beginChatBudget(ctx, config.ModelSelection{Provider: "default", Model: "test-model"}, hardBudgetTestMessages(), nil, 0, "call-after-release"); err != nil || next == nil {
		t.Fatalf("reservation after release: %v", err)
	}
}
