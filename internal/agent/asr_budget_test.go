package agent

import (
	"context"
	"strings"
	"testing"

	"elbot/internal/config"
	"elbot/internal/platform"
)

func TestASRQuotaReservation(t *testing.T) {
	agent := &Agent{
		platform:           &fakePlatform{},
		budgetReservations: map[string]int64{},
		budgetDigests:      map[string]string{},
	}
	ctx := platform.WithMessageContext(context.Background(), platform.MessageContext{
		Platform:         "qq-onebot",
		ScopeID:          "group:10001",
		ConversationKind: platform.ConversationGroup,
		Sender:           nil,
	})
	agent.setGroupPolicyForScope(agent.scope(ctx), config.GroupPolicyConfig{ASRQuota: 1, UserASRQuota: 1})
	specs := agent.callLimitSpecs(ctx, "asr")
	if len(specs) != 2 {
		t.Fatalf("asr specs = %#v", specs)
	}
	for _, spec := range specs {
		if !strings.Contains(spec.label, "语音转写") {
			t.Fatalf("spec label = %q", spec.label)
		}
	}
	if ok, reason := agent.reserveBudgetBatchRequests(ctx, "asr", []budgetReservationRequest{{CallID: "one"}}); !ok {
		t.Fatalf("first reservation denied: %s", reason)
	}
	if ok, reason := agent.reserveBudgetBatchRequests(ctx, "asr", []budgetReservationRequest{{CallID: "two"}}); ok {
		t.Fatal("second reservation should be denied by the group quota")
	} else if !strings.Contains(reason, "语音转写") {
		t.Fatalf("denial reason = %q", reason)
	}
}
