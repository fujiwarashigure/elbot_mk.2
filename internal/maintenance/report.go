package maintenance

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/logging"
	"elbot/internal/sysinfo"
)

type imageReportStats struct {
	Success int
	Failed  int
}

type modelUsageReport struct {
	Model            string
	Calls            int
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CacheHitTokens   int64
	Cost             float64
	Priced           bool
}

// RunDailyReport builds and delivers the scheduled resource report.
func (s *Service) RunDailyReport(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.Report == nil {
		s.warn("daily report skipped: no delivery target configured")
		return nil
	}
	window := time.Duration(s.report.WindowHours) * time.Hour
	if window <= 0 {
		window = 12 * time.Hour
	}
	until := time.Now()
	since := until.Add(-window)
	text, err := s.buildDailyReport(ctx, since, until)
	if err != nil {
		return err
	}
	return s.Report(ctx, text)
}

func (s *Service) buildDailyReport(ctx context.Context, since, until time.Time) (string, error) {
	if s.logs == nil {
		return "", fmt.Errorf("log manager is not configured")
	}
	reader := logging.Reader{Dir: s.logs.LogDir()}
	images, err := s.queryImageStats(ctx, reader, since, until)
	if err != nil {
		return "", err
	}
	usage, err := s.queryUsage(ctx, reader, since, until)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(s.report.DataRoot)
	if root == "" {
		root = filepath.Dir(s.logs.LogDir())
	}
	snapshot := sysinfo.Collect(root)
	return formatDailyReport(s.report, images, usage, snapshot, since, until), nil
}

func (s *Service) queryImageStats(ctx context.Context, reader logging.Reader, since, until time.Time) (imageReportStats, error) {
	entries, err := reader.Query(ctx, logging.LogQuery{
		Prefix:   "audit",
		Limit:    200000,
		Days:     logQueryDays(since, until),
		MinLevel: "debug",
		Since:    &since,
		Until:    &until,
		Fields:   map[string]string{"event": "tool_call", "tool": "image_generate"},
	})
	if err != nil {
		return imageReportStats{}, err
	}
	stats := imageReportStats{}
	for _, entry := range entries {
		if strings.EqualFold(strings.TrimSpace(entry.Fields["success"]), "true") {
			stats.Success++
		} else {
			stats.Failed++
		}
	}
	return stats, nil
}

func (s *Service) queryUsage(ctx context.Context, reader logging.Reader, since, until time.Time) ([]modelUsageReport, error) {
	entries, err := reader.Query(ctx, logging.LogQuery{
		Prefix:   "audit",
		Limit:    400000,
		Days:     logQueryDays(since, until),
		MinLevel: "debug",
		Since:    &since,
		Until:    &until,
		Fields:   map[string]string{"event": "llm_usage"},
	})
	if err != nil {
		return nil, err
	}
	provider := strings.ToLower(strings.TrimSpace(s.report.Provider))
	buckets := map[string]*modelUsageReport{}
	order := []string{}
	for _, entry := range entries {
		if provider != "" && strings.ToLower(strings.TrimSpace(entry.Fields["provider"])) != provider {
			continue
		}
		model := strings.TrimSpace(entry.Fields["model"])
		if model == "" {
			model = "(unknown)"
		}
		bucket := buckets[model]
		if bucket == nil {
			bucket = &modelUsageReport{Model: model}
			buckets[model] = bucket
			order = append(order, model)
		}
		promptTokens := parseLogInt(entry.Fields["prompt_tokens"])
		completionTokens := parseLogInt(entry.Fields["completion_tokens"])
		cacheHitTokens := parseLogInt(entry.Fields["cache_hit_tokens"])
		bucket.Calls++
		bucket.PromptTokens += promptTokens
		bucket.CompletionTokens += completionTokens
		bucket.TotalTokens += parseLogInt(entry.Fields["total_tokens"])
		bucket.CacheHitTokens += cacheHitTokens
		if price, ok := s.priceFor(model, entry.Time); ok {
			bucket.Priced = true
			bucket.Cost += computeEntryCost(promptTokens, completionTokens, cacheHitTokens, price)
		}
	}
	out := make([]modelUsageReport, 0, len(order))
	for _, model := range order {
		out = append(out, *buckets[model])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

// priceFor returns the price tier for one usage entry at time at.
func (s *Service) priceFor(model string, at time.Time) (config.ModelPriceConfig, bool) {
	price, ok := s.report.Prices[model]
	if !ok {
		return config.ModelPriceConfig{}, false
	}
	if !s.report.IsPeakPricing() || !isOffPeak(at, s.report.Holidays) {
		return price, true
	}
	return offpeakPrice(price), true
}

// offpeakPrice falls back to the standard price for unset offpeak fields.
func offpeakPrice(price config.ModelPriceConfig) config.ModelPriceConfig {
	out := price
	if price.OffpeakInputPerMillion > 0 {
		out.InputPerMillion = price.OffpeakInputPerMillion
	}
	if price.OffpeakCacheInputPerMillion > 0 {
		out.CacheInputPerMillion = price.OffpeakCacheInputPerMillion
	}
	if price.OffpeakOutputPerMillion > 0 {
		out.OutputPerMillion = price.OffpeakOutputPerMillion
	}
	return out
}

var shanghaiLocation = func() *time.Location {
	if location, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return location
	}
	return time.FixedZone("CST", 8*3600)
}()

// isOffPeak implements the DeepSeek rule: Saturday/Sunday and every weekday
// outside 09:00-12:00 / 14:00-18:00 Beijing time is off-peak. Holidays listed
// in the config are also treated as off-peak.
func isOffPeak(at time.Time, holidays []string) bool {
	if at.IsZero() {
		// Unknown timestamp: assume peak to avoid underestimating the bill.
		return false
	}
	local := at.In(shanghaiLocation)
	day := local.Format("2006-01-02")
	for _, holiday := range holidays {
		if strings.TrimSpace(holiday) == day {
			return true
		}
	}
	switch local.Weekday() {
	case time.Saturday, time.Sunday:
		return true
	}
	minutes := local.Hour()*60 + local.Minute()
	peak := (minutes >= 9*60 && minutes < 12*60) || (minutes >= 14*60 && minutes < 18*60)
	return !peak
}

func computeEntryCost(promptTokens, completionTokens, cacheHitTokens int64, price config.ModelPriceConfig) float64 {
	cacheHit := cacheHitTokens
	if cacheHit > promptTokens {
		cacheHit = promptTokens
	}
	if cacheHit < 0 {
		cacheHit = 0
	}
	cacheMiss := promptTokens - cacheHit
	if cacheMiss < 0 {
		cacheMiss = 0
	}
	cachePrice := price.CacheInputPerMillion
	if cachePrice <= 0 {
		cachePrice = price.InputPerMillion
	}
	return float64(cacheMiss)/1_000_000*price.InputPerMillion +
		float64(cacheHit)/1_000_000*cachePrice +
		float64(completionTokens)/1_000_000*price.OutputPerMillion
}

func formatDailyReport(cfg config.DailyReportConfig, images imageReportStats, usage []modelUsageReport, snapshot sysinfo.Snapshot, since, until time.Time) string {
	var b strings.Builder
	b.WriteString("ElBot 定时报告\n")
	b.WriteString("周期：" + since.Format("2006-01-02 15:04") + " ~ " + until.Format("2006-01-02 15:04") + "\n")
	b.WriteString(fmt.Sprintf("窗口：最近 %d 小时\n", cfg.WindowHours))

	b.WriteString("\n【生图】\n")
	b.WriteString(fmt.Sprintf("- 成功：%d 次\n", images.Success))
	b.WriteString(fmt.Sprintf("- 失败：%d 次\n", images.Failed))
	b.WriteString(fmt.Sprintf("- 合计：%d 次\n", images.Success+images.Failed))

	b.WriteString("\n【Token 消耗】\n")
	provider := strings.TrimSpace(cfg.Provider)
	if provider == "" {
		provider = "全部 provider"
	}
	if len(usage) == 0 {
		b.WriteString("- 无 llm_usage 记录（" + provider + "）\n")
	} else {
		var promptTotal, completionTotal, totalTotal, cacheTotal int64
		var callsTotal int
		for _, item := range usage {
			promptTotal += item.PromptTokens
			completionTotal += item.CompletionTokens
			totalTotal += item.TotalTokens
			cacheTotal += item.CacheHitTokens
			callsTotal += item.Calls
			b.WriteString(fmt.Sprintf("- %s：调用 %d 次｜输入 %s｜输出 %s｜缓存命中 %s｜合计 %s\n",
				item.Model, item.Calls, humanInt(item.PromptTokens), humanInt(item.CompletionTokens),
				humanInt(item.CacheHitTokens), humanInt(item.TotalTokens)))
		}
		b.WriteString(fmt.Sprintf("- 合计：调用 %d 次｜输入 %s｜输出 %s｜缓存命中 %s｜合计 %s\n",
			callsTotal, humanInt(promptTotal), humanInt(completionTotal), humanInt(cacheTotal), humanInt(totalTotal)))
	}

	b.WriteString("\n【费用】\n")
	symbol := currencySymbol(cfg.Currency)
	unpriced := []string{}
	var llmCost float64
	for _, item := range usage {
		if !item.Priced {
			unpriced = append(unpriced, item.Model)
			continue
		}
		llmCost += item.Cost
		b.WriteString(fmt.Sprintf("- %s：%s\n", item.Model, formatMoney(item.Cost, symbol)))
	}
	imageCost := float64(images.Success) * cfg.ImagePricePerImage
	if cfg.ImagePricePerImage > 0 {
		b.WriteString(fmt.Sprintf("- 生图：%d 张 × %s = %s\n",
			images.Success, formatMoney(cfg.ImagePricePerImage, symbol), formatMoney(imageCost, symbol)))
	}
	if llmCost == 0 && imageCost == 0 && len(usage) == 0 && cfg.ImagePricePerImage <= 0 {
		b.WriteString("- 无数据\n")
	} else {
		b.WriteString(fmt.Sprintf("- 合计：%s\n", formatMoney(llmCost+imageCost, symbol)))
	}
	if cfg.IsPeakPricing() {
		b.WriteString("- 计价：高峰/空闲双档（北京时间周一至周五 9-12、14-18 为高峰；周末及配置的节假日为空闲）\n")
	}
	if len(unpriced) > 0 {
		b.WriteString("- 未配置单价：" + strings.Join(unpriced, ", ") + "（只统计 token，不计金额）\n")
	}
	b.WriteString("\n【磁盘】\n")
	b.WriteString("- 数据目录：" + sysinfo.FormatBytes(snapshot.DirBytes) + "\n")
	if snapshot.DiskTotalBytes > 0 {
		used := snapshot.DiskTotalBytes - snapshot.DiskFreeBytes
		percent := float64(used) / float64(snapshot.DiskTotalBytes) * 100
		b.WriteString(fmt.Sprintf("- 文件系统：已用 %s / %s（剩余 %s，%.1f%%）\n",
			sysinfo.FormatUintBytes(used), sysinfo.FormatUintBytes(snapshot.DiskTotalBytes),
			sysinfo.FormatUintBytes(snapshot.DiskFreeBytes), percent))
	} else if snapshot.DiskErr != "" {
		b.WriteString("- 文件系统：暂不支持（" + snapshot.DiskErr + "）\n")
	}

	b.WriteString("\n【内存】\n")
	if snapshot.RSSBytes > 0 {
		b.WriteString("- 进程 RSS：" + sysinfo.FormatUintBytes(snapshot.RSSBytes) + "\n")
	}
	b.WriteString("- Go 堆：" + sysinfo.FormatUintBytes(snapshot.HeapAllocBytes) + " / 系统 " + sysinfo.FormatUintBytes(snapshot.HeapSysBytes) + "\n")
	b.WriteString(fmt.Sprintf("- goroutine：%d\n", snapshot.Goroutines))
	return strings.TrimRight(b.String(), "\n")
}

func logQueryDays(since, until time.Time) int {
	days := int(until.Sub(since).Hours()/24) + 2
	if days < 1 {
		days = 1
	}
	if days > 7 {
		days = 7
	}
	return days
}

func parseLogInt(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func currencySymbol(currency string) string {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "", "CNY", "RMB", "¥", "￥":
		return "¥"
	case "USD", "$":
		return "$"
	default:
		return strings.TrimSpace(currency) + " "
	}
}

func formatMoney(value float64, symbol string) string {
	return symbol + strconv.FormatFloat(value, 'f', 4, 64)
}

func humanInt(value int64) string {
	text := strconv.FormatInt(value, 10)
	negative := strings.HasPrefix(text, "-")
	if negative {
		text = text[1:]
	}
	var out []byte
	for i, digit := range []byte(text) {
		if i > 0 && (len(text)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, digit)
	}
	if negative {
		return "-" + string(out)
	}
	return string(out)
}
