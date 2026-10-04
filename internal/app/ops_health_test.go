package app

import (
	"testing"

	"elbot/internal/config"
	"elbot/internal/health"
	"elbot/internal/vision"
)

func TestCollectOpsMetricsIncludesVisionStats(t *testing.T) {
	state := health.NewState(health.Options{Version: "test"})
	visionState := newVisionMetricsState()
	counters := visionState.countersValue()
	counters.CacheLookup("hit")
	counters.JobStarted()
	counters.Error("http_429")
	visionState.set("image_to_prompt", vision.New(vision.Options{
		Client:  &recordingVisionLLM{},
		Model:   "vision-1",
		Metrics: counters,
	}))

	raw := collectOpsMetrics(config.Default(), state, nil, nil, visionState.snapshot)
	metrics, ok := raw.(opsMetrics)
	if !ok {
		t.Fatalf("collectOpsMetrics returned %T", raw)
	}
	if metrics.Vision == nil {
		t.Fatal("vision metrics must be reported once a role is configured")
	}
	if metrics.Vision.Jobs != 1 || metrics.Vision.Cache["hit"] != 1 || metrics.Vision.Errors["http_429"] != 1 {
		t.Fatalf("vision counters = %#v", metrics.Vision)
	}
	if metrics.Vision.ImageToPrompt == nil || metrics.Vision.Fallback != nil {
		t.Fatalf("vision roles = %#v", metrics.Vision)
	}
	if metrics.Vision.ImageToPrompt.MaxConcurrentJobs != vision.DefaultMaxConcurrentJobs {
		t.Fatalf("runner caps = %#v", metrics.Vision.ImageToPrompt)
	}
}

func TestCollectOpsMetricsOmitsUnconfiguredVision(t *testing.T) {
	state := health.NewState(health.Options{Version: "test"})
	raw := collectOpsMetrics(config.Default(), state, nil, nil, newVisionMetricsState().snapshot)
	metrics, ok := raw.(opsMetrics)
	if !ok {
		t.Fatalf("collectOpsMetrics returned %T", raw)
	}
	if metrics.Vision != nil {
		t.Fatalf("vision metrics must stay hidden when no role is configured: %#v", metrics.Vision)
	}
}
