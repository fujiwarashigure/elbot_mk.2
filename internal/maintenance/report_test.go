package maintenance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elbot/internal/config"
	"elbot/internal/logging"
	"elbot/internal/sysinfo"
)

func writeAuditLog(t *testing.T, dir string, lines []string) {
	t.Helper()
	name := "audit-" + time.Now().Format("2006-01-02") + ".log"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write audit log: %v", err)
	}
}

func TestDailyReportAggregatesUsageAndPricing(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	ts := `"` + now.Format("2006-01-02 15:04:05") + `"`
	lines := []string{
		`time=` + ts + ` level=INFO msg="audit event" event=llm_usage provider=deepseek model=deepseek-chat prompt_tokens=1000 completion_tokens=500 total_tokens=1500 cache_hit_tokens=200`,
		`time=` + ts + ` level=INFO msg="audit event" event=llm_usage provider=deepseek model=deepseek-chat prompt_tokens=2000 completion_tokens=1000 total_tokens=3000 cache_hit_tokens=0`,
		`time=` + ts + ` level=INFO msg="audit event" event=llm_usage provider=openai model=gpt-4o prompt_tokens=100 completion_tokens=50 total_tokens=150`,
		`time=` + ts + ` level=INFO msg="audit event" event=tool_call tool=image_generate success=true`,
		`time=` + ts + ` level=INFO msg="audit event" event=tool_call tool=image_generate success=false`,
	}
	writeAuditLog(t, dir, lines)

	flatPricing := false
	service := &Service{report: config.DailyReportConfig{
		Provider:           "deepseek",
		Currency:           "CNY",
		WindowHours:        12,
		ImagePricePerImage: 0.05,
		PeakPricing:        &flatPricing,
		Prices: map[string]config.ModelPriceConfig{
			"deepseek-chat": {InputPerMillion: 2, OutputPerMillion: 8, CacheInputPerMillion: 0.5},
		},
	}}
	reader := logging.Reader{Dir: dir}
	since := now.Add(-time.Hour)
	until := now.Add(time.Hour)

	usage, err := service.queryUsage(context.Background(), reader, since, until)
	if err != nil {
		t.Fatalf("queryUsage: %v", err)
	}
	if len(usage) != 1 || usage[0].Model != "deepseek-chat" {
		t.Fatalf("usage = %#v", usage)
	}
	if usage[0].PromptTokens != 3000 || usage[0].CompletionTokens != 1500 || usage[0].TotalTokens != 4500 || usage[0].CacheHitTokens != 200 {
		t.Fatalf("usage tokens = %#v", usage[0])
	}
	wantCost := 2800.0/1_000_000*2 + 200.0/1_000_000*0.5 + 1500.0/1_000_000*8
	if diff := usage[0].Cost - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cost = %v want %v", usage[0].Cost, wantCost)
	}

	images, err := service.queryImageStats(context.Background(), reader, since, until)
	if err != nil {
		t.Fatalf("queryImageStats: %v", err)
	}
	if images.Success != 1 || images.Failed != 1 {
		t.Fatalf("images = %#v", images)
	}

	report := formatDailyReport(service.report, images, usage, sysinfo.Snapshot{
		DirBytes:       2048,
		DiskTotalBytes: 100 * 1024 * 1024,
		DiskFreeBytes:  40 * 1024 * 1024,
		HeapAllocBytes: 1024 * 1024,
		HeapSysBytes:   4 * 1024 * 1024,
		RSSBytes:       8 * 1024 * 1024,
		Goroutines:     12,
	}, now.Add(-12*time.Hour), now)
	t.Log(report)
	for _, want := range []string{"成功：1 次", "失败：1 次", "deepseek-chat", "3,000", "¥0.0177", "生图：1 张 × ¥0.0500 = ¥0.0500", "¥0.0677", "2.0 KB", "60.0%", "进程 RSS"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "gpt-4o") {
		t.Fatalf("provider filter should exclude openai usage:\n%s", report)
	}
}

func TestDailyReportPeakAndOffpeakPricing(t *testing.T) {
	peak := time.Date(2026, 10, 6, 10, 0, 0, 0, shanghaiLocation)     // Tuesday 10:00 -> peak
	offpeak := time.Date(2026, 10, 6, 20, 0, 0, 0, shanghaiLocation)  // Tuesday 20:00 -> off-peak
	lunch := time.Date(2026, 10, 6, 13, 0, 0, 0, shanghaiLocation)    // Tuesday 13:00 -> off-peak
	weekend := time.Date(2026, 10, 10, 10, 0, 0, 0, shanghaiLocation) // Saturday -> off-peak
	for _, tt := range []struct {
		at   time.Time
		want bool
	}{
		{peak, false},
		{offpeak, true},
		{lunch, true},
		{weekend, true},
		{time.Time{}, false},
	} {
		if got := isOffPeak(tt.at, nil); got != tt.want {
			t.Fatalf("isOffPeak(%s) = %v, want %v", tt.at, got, tt.want)
		}
	}
	if !isOffPeak(peak, []string{"2026-10-06"}) {
		t.Fatal("configured holiday should be off-peak")
	}

	price := config.ModelPriceConfig{
		InputPerMillion: 9, CacheInputPerMillion: 0.30, OutputPerMillion: 27,
		OffpeakInputPerMillion: 4.5, OffpeakCacheInputPerMillion: 0.15, OffpeakOutputPerMillion: 13.5,
	}
	if got := computeEntryCost(1_000_000, 0, 0, price); got != 9 {
		t.Fatalf("peak cost = %v, want 9", got)
	}
	if got := computeEntryCost(1_000_000, 0, 0, offpeakPrice(price)); got != 4.5 {
		t.Fatalf("offpeak cost = %v, want 4.5", got)
	}
	// Cache hit and miss are priced separately.
	mixed := computeEntryCost(1_000_000, 0, 400_000, price)
	wantMixed := 600_000.0/1_000_000*9 + 400_000.0/1_000_000*0.30
	if mixed != wantMixed {
		t.Fatalf("mixed cost = %v, want %v", mixed, wantMixed)
	}
}
