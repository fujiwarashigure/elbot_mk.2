package app

import (
	"fmt"
	"sort"
	"strings"

	"elbot/internal/config"
)

// CheckConfig loads app.toml and returns a human-readable summary plus
// warnings. It exits with an error when the file cannot be loaded.
func CheckConfig(configPath string) (string, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("config OK\n")
	b.WriteString("path: " + cfg.ConfigPath + "\n")
	b.WriteString("commands.prefixes: " + strings.Join(cfg.Commands.Prefixes, " ") + "\n")
	b.WriteString(fmt.Sprintf("character_library: enabled=%v root=%s\n", cfg.CharacterLibrary.IsEnabled(), cfg.CharacterLibrary.Root))
	if cfg.ImageGeneration.Enabled {
		b.WriteString(fmt.Sprintf("image_generation: model=%s profiles=%d default_profile=%q\n",
			cfg.ImageGeneration.Model, len(cfg.ImageGeneration.Profiles), cfg.ImageGeneration.DefaultProfileName()))
	} else {
		b.WriteString("image_generation: disabled\n")
	}
	b.WriteString(fmt.Sprintf("model_profiles: %d\n", len(cfg.ModelProfiles)))
	b.WriteString(fmt.Sprintf("tool_profiles: %d\n", len(cfg.ToolProfiles)))
	directives := cfg.TurnDirectives.Normalized()
	b.WriteString(fmt.Sprintf("turn_directives: prefixes=%s model=%s image=%s tool=%s\n",
		strings.Join(directives.Prefixes, " "),
		strings.Join(directives.ModelKeywords, "/"),
		strings.Join(directives.ImageKeywords, "/"),
		strings.Join(directives.ToolKeywords, "/")))
	report := cfg.Maintenance.DailyReport
	b.WriteString(fmt.Sprintf("daily_report: enabled=%v schedule=%q window_hours=%d provider=%q\n",
		report.Enabled, report.Schedule, report.WindowHours, report.Provider))

	warnings := checkConfigWarnings(cfg)
	if len(warnings) == 0 {
		b.WriteString("warnings: none\n")
	} else {
		b.WriteString("warnings:\n")
		for _, warning := range warnings {
			b.WriteString("- " + warning + "\n")
		}
	}
	return b.String(), nil
}

func checkConfigWarnings(cfg *config.Config) []string {
	warnings := []string{}
	names := make([]string, 0, len(cfg.ModelProfiles))
	for name := range cfg.ModelProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		profile := cfg.ModelProfiles[name]
		if _, ok := cfg.Providers[profile.Provider]; !ok {
			warnings = append(warnings, fmt.Sprintf("model_profiles.%s 的 provider %q 不在 providers 配置里", name, profile.Provider))
		}
	}
	if !cfg.ImageGeneration.Enabled && len(cfg.ImageGeneration.Profiles) > 0 {
		warnings = append(warnings, "配置了 image_generation.profiles 但 enabled=false，生图工具不会注册")
	}
	if cfg.ImageGeneration.Enabled &&
		strings.TrimSpace(cfg.ImageGeneration.BaseURL) == "" &&
		strings.TrimSpace(cfg.ImageGeneration.Endpoint) == "" &&
		len(cfg.ImageGeneration.Profiles) == 0 {
		warnings = append(warnings, "image_generation 缺少 base_url/endpoint")
	}
	if report := cfg.Maintenance.DailyReport; report.Enabled {
		if len(cfg.Security.Superadmins) == 0 {
			warnings = append(warnings, "daily_report.enabled=true 但没有配置 security.superadmins，报告无法发送")
		}
		if len(report.Prices) == 0 {
			warnings = append(warnings, "daily_report 没有配置 prices，报告只统计 token 不统计金额")
		}
	}
	return warnings
}
