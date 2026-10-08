package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/health"
	"elbot/internal/llm"
	"elbot/internal/llm/openai"
	"elbot/internal/platform/cli"
	"elbot/internal/redact"
	"elbot/internal/storage"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const defaultDoctorTimeout = 60 * time.Second

// errHealthzDisabled marks the secure default where /healthz is not registered
// because no ops token is configured.
var errHealthzDisabled = errors.New("/healthz is disabled because no ops token is configured")

type DoctorOptions struct {
	ConfigPath      string
	E2E             bool
	JSON            bool
	SkipModel       bool
	RequirePlatform bool
	Timeout         time.Duration
}

type DoctorCheck struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Error    string `json:"error,omitempty"`
}

type DoctorReport struct {
	ConfigOK   bool          `json:"config_ok"`
	PlatformOK bool          `json:"platform_ok"`
	E2EOK      bool          `json:"e2e_ok"`
	Checks     []DoctorCheck `json:"checks"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
}

// RunDoctor performs a deterministic deployment acceptance pass. Config and
// local dependency checks always run. The end-to-end round-trip is opt-in
// because it sends a real message through the configured CLI remote server.
func RunDoctor(ctx context.Context, opts DoctorOptions) (DoctorReport, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultDoctorTimeout
	}
	report := DoctorReport{ConfigOK: true, PlatformOK: true, StartedAt: time.Now()}
	add := func(category, name, status, detail string, err error) {
		check := DoctorCheck{Category: category, Name: name, Status: status, Detail: detail}
		if err != nil {
			check.Error = redact.Error(err)
		}
		report.Checks = append(report.Checks, check)
	}

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		add("config", "load", "failed", "", err)
		report.ConfigOK = false
		report.FinishedAt = time.Now()
		return report, nil
	}
	configDir := filepath.Dir(cfg.ConfigPath)
	summary, err := CheckConfig(cfg.ConfigPath)
	if err != nil {
		add("config", "check", "failed", "", err)
		report.ConfigOK = false
	} else {
		add("config", "check", "passed", firstLine(summary), nil)
	}
	if err := checkWritableDirs(cfg); err != nil {
		add("config", "storage_writable", "failed", "", err)
		report.ConfigOK = false
	} else {
		add("config", "storage_writable", "passed", "sessions/chat-history directories are writable", nil)
	}

	workModel := cfg.ModeModels[storage.SessionModeWork]
	provider := cfg.Providers[workModel.Provider]
	apiKey, keyErr := resolveDoctorAPIKey(provider, configDir)
	if keyErr != nil {
		add("config", "model_credentials", "failed", workModel.Provider+"/"+workModel.Model, keyErr)
		report.ConfigOK = false
	} else {
		add("config", "model_credentials", "passed", workModel.Provider+"/"+workModel.Model, nil)
	}

	healthURL, healthErr := doctorHealthURL(cfg)
	if healthErr != nil {
		add("port", "health_listen", "failed", "", healthErr)
		report.ConfigOK = false
	} else if healthURL == "" {
		add("port", "health_listen", "skipped", "ELBOT_HEALTH_ADDR is not configured", nil)
	} else if err := probeApplicationHealth(ctx, healthURL); err != nil {
		add("port", "health_listen", "failed", healthURL, err)
		report.ConfigOK = false
	} else {
		add("port", "health_listen", "passed", healthURL, nil)
	}

	enabled := enabledPlatformNames(cfg)
	if len(enabled) == 0 {
		add("platform", "configured", "passed", "no platform is enabled", nil)
	} else {
		add("platform", "configured", "passed", strings.Join(enabled, ", "), nil)
	}
	if healthURL != "" {
		opsToken := resolveDoctorOpsToken(cfg)
		if snapshot, err := fetchHealthSnapshot(ctx, healthURL, opsToken); err != nil {
			if opsToken == "" && errors.Is(err, errHealthzDisabled) && !opts.RequirePlatform {
				add("platform", "connection", "skipped", err.Error(), nil)
			} else {
				add("platform", "connection", "failed", healthURL, err)
				report.PlatformOK = false
			}
		} else {
			status, detail, err := doctorPlatformConnection(snapshot, enabled, opts.RequirePlatform)
			add("platform", "connection", status, detail, err)
			if status == "failed" {
				report.PlatformOK = false
			}
		}
	} else if opts.RequirePlatform && len(enabled) > 0 {
		add("platform", "connection", "failed", "health endpoint is not configured", fmt.Errorf("cannot verify platform connectivity without %s", healthAddrEnv))
		report.PlatformOK = false
	} else {
		add("platform", "connection", "skipped", "health endpoint is not configured", nil)
	}

	if opts.SkipModel {
		add("model", "chat_call", "skipped", "model call skipped by option", nil)
	} else if keyErr != nil {
		add("model", "chat_call", "failed", workModel.Provider+"/"+workModel.Model, keyErr)
		report.ConfigOK = false
	} else if err := doctorModelCall(ctx, cfg, workModel.Provider, provider, apiKey, workModel.Model); err != nil {
		add("model", "chat_call", "failed", workModel.Provider+"/"+workModel.Model, err)
		report.ConfigOK = false
	} else {
		add("model", "chat_call", "passed", workModel.Provider+"/"+workModel.Model, nil)
	}

	if opts.E2E {
		if err := doctorCLIRoundTrip(ctx, cfg, opts.Timeout); err != nil {
			add("e2e", "cli_roundtrip", "failed", "", err)
			report.E2EOK = false
		} else {
			add("e2e", "cli_roundtrip", "passed", "CLI remote message round-trip completed", nil)
			report.E2EOK = true
		}
	} else {
		add("e2e", "cli_roundtrip", "skipped", "use --e2e to send a real message through the CLI remote server", nil)
		report.E2EOK = false
	}

	report.FinishedAt = time.Now()
	return report, nil
}

func checkWritableDirs(cfg *config.Config) error {
	dirs := []string{
		filepath.Dir(cfg.Storage.SessionsSQLitePath),
		filepath.Dir(cfg.Storage.ChatHistorySQLitePath),
	}
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			return fmt.Errorf("storage directory is empty")
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		file, err := os.CreateTemp(dir, ".elbot-doctor-*")
		if err != nil {
			return fmt.Errorf("write %s: %w", dir, err)
		}
		name := file.Name()
		if _, err := file.WriteString("ok\n"); err != nil {
			_ = file.Close()
			_ = os.Remove(name)
			return fmt.Errorf("write %s: %w", name, err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(name)
			return fmt.Errorf("close %s: %w", name, err)
		}
		if err := os.Remove(name); err != nil {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}
	return nil
}

func resolveDoctorAPIKey(provider config.ProviderConfig, configDir string) (string, error) {
	envName := strings.TrimSpace(provider.APIKeyEnv)
	if envName != "" {
		value, ok, err := config.ConfigEnv(envName, configDir)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", envName, err)
		}
		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	if value := strings.TrimSpace(provider.APIKey); value != "" {
		return value, nil
	}
	if envName != "" {
		return "", fmt.Errorf("environment variable %s is not set", envName)
	}
	return "", fmt.Errorf("provider api_key/api_key_env is not configured")
}

func doctorHealthURL(cfg *config.Config) (string, error) {
	configDir := filepath.Dir(cfg.ConfigPath)
	addr, ok, err := config.ConfigEnv(healthAddrEnv, configDir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", healthAddrEnv, err)
	}
	addr = strings.TrimSpace(addr)
	if !ok || addr == "" {
		return "", nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("parse %s=%q: %w", healthAddrEnv, addr, err)
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func probeApplicationHealth(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/live", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/live returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func resolveDoctorOpsToken(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	value, _, err := config.ConfigEnv(healthOpsTokenEnv, filepath.Dir(cfg.ConfigPath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func fetchHealthSnapshot(ctx context.Context, baseURL, opsToken string) (health.Snapshot, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/healthz", nil)
	if err != nil {
		return health.Snapshot{}, err
	}
	if token := strings.TrimSpace(opsToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return health.Snapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return health.Snapshot{}, fmt.Errorf("%w; set %s to re-enable it", errHealthzDisabled, healthOpsTokenEnv)
		}
		return health.Snapshot{}, fmt.Errorf("/healthz returned HTTP %d", resp.StatusCode)
	}
	var snapshot health.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return health.Snapshot{}, fmt.Errorf("decode /healthz: %w", err)
	}
	return snapshot, nil
}

func enabledPlatformNames(cfg *config.Config) []string {
	names := make([]string, 0)
	for name, raw := range cfg.Platform {
		enabled := true
		if value, ok := raw["enabled"]; ok {
			if boolValue, ok := value.(bool); ok {
				enabled = boolValue
			}
		}
		if enabled {
			names = append(names, name)
		}
	}
	return names
}

func doctorPlatformConnection(snapshot health.Snapshot, enabled []string, require bool) (string, string, error) {
	detail := "health=" + snapshot.Status
	parts := make([]string, 0, len(snapshot.Platforms))
	connected := map[string]bool{}
	for _, platform := range snapshot.Platforms {
		value := platform.Name + "=disconnected"
		if platform.Connected {
			value = platform.Name + "=connected"
		}
		parts = append(parts, value)
		if platform.Connected {
			connected[platform.Name] = true
		}
	}
	if len(parts) > 0 {
		detail = strings.Join(parts, ", ")
	}
	if len(enabled) == 0 {
		if require {
			return "failed", detail, fmt.Errorf("--require-platform was set but no platform is enabled")
		}
		return "passed", detail + " (no platform is enabled)", nil
	}
	missing := make([]string, 0)
	disconnected := make([]string, 0)
	for _, name := range enabled {
		switch {
		case !connected[name]:
			if snapshotHasPlatform(snapshot, name) {
				disconnected = append(disconnected, name)
			} else {
				missing = append(missing, name)
			}
		}
	}
	if len(disconnected) > 0 {
		return "failed", detail, fmt.Errorf("platform disconnected: %s", strings.Join(disconnected, ", "))
	}
	if require && len(missing) > 0 {
		return "failed", detail, fmt.Errorf("platform status missing for: %s", strings.Join(missing, ", "))
	}
	return "passed", detail, nil
}

func snapshotHasPlatform(snapshot health.Snapshot, name string) bool {
	for _, platform := range snapshot.Platforms {
		if platform.Name == name {
			return true
		}
	}
	return false
}

func doctorModelCall(ctx context.Context, cfg *config.Config, providerName string, provider config.ProviderConfig, apiKey, model string) error {
	if strings.TrimSpace(provider.BaseURL) == "" {
		return fmt.Errorf("provider base_url is empty")
	}
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("work model is empty")
	}
	// The caller resolves the key (api_key first, then api_key_env); the adapter
	// only reads provider.APIKey.
	provider.APIKey = apiKey
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := newProviderLLM(providerName, provider, openai.RequestOptions{
		FirstChunkTimeout: 15 * time.Second,
		StreamIdleTimeout: 15 * time.Second,
		MaxRetries:        0,
	})
	if err != nil {
		return err
	}
	stream, err := client.ChatStream(callCtx, llm.ChatRequest{
		Model: model,
		Messages: []llm.LLMMessage{{
			Role: llm.RoleUser,
			Segments: []llm.MessageSegment{{
				Type: llm.SegmentText,
				Text: "Reply with exactly one word: pong",
			}},
		}},
	})
	if err != nil {
		return err
	}
	for chunk := range stream {
		if chunk.Error != nil {
			return chunk.Error
		}
		if strings.TrimSpace(chunk.DeltaContent) != "" {
			return nil
		}
	}
	return fmt.Errorf("model stream completed without a text response")
}

type doctorRemoteMessage struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Token    string `json:"token,omitempty"`
	Text     string `json:"text,omitempty"`
	Level    string `json:"level,omitempty"`
}

func doctorCLIRoundTrip(ctx context.Context, cfg *config.Config, timeout time.Duration) error {
	raw, ok := cfg.Platform["cli"]
	if !ok || raw == nil {
		return fmt.Errorf("cli platform is not configured")
	}
	cliCfg, err := cli.NewConfigFromPlatformConfig(raw)
	if err != nil {
		return err
	}
	if !cliCfg.Enabled {
		return fmt.Errorf("cli platform is disabled")
	}
	_, clientCfg, err := cliCfg.Client("")
	if err != nil {
		return err
	}
	if strings.TrimSpace(clientCfg.URL) == "" {
		return fmt.Errorf("cli client URL is empty")
	}
	configDir := filepath.Dir(cfg.ConfigPath)
	token := ""
	for _, envName := range clientCfg.TokenEnv {
		value, ok, err := config.ConfigEnv(envName, configDir)
		if err != nil {
			return err
		}
		if ok && strings.TrimSpace(value) != "" {
			token = strings.TrimSpace(value)
			break
		}
	}
	if token == "" {
		return fmt.Errorf("cli client token is not configured")
	}

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, clientCfg.URL, nil)
	if err != nil {
		return fmt.Errorf("connect cli server: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "doctor done")
	if err := wsjson.Write(dialCtx, conn, doctorRemoteMessage{Type: "hello", ClientID: clientCfg.ID, Token: token}); err != nil {
		return err
	}
	var hello doctorRemoteMessage
	if err := wsjson.Read(dialCtx, conn, &hello); err != nil {
		return err
	}
	if hello.Type == "error" {
		return fmt.Errorf("cli server rejected connection: %s", hello.Text)
	}
	if hello.Type != "hello_ok" {
		return fmt.Errorf("unexpected cli server hello type %q", hello.Type)
	}
	probe := "doctor-probe-" + strings.ReplaceAll(redact.NewErrorID(), "-", "")
	if err := wsjson.Write(dialCtx, conn, doctorRemoteMessage{Type: "input", Text: "请只回复这个标记，不要添加其他内容：" + probe}); err != nil {
		return err
	}
	expected := strings.ToLower(probe)
	streamMatched := false
	var streamed strings.Builder
	for {
		var msg doctorRemoteMessage
		if err := wsjson.Read(dialCtx, conn, &msg); err != nil {
			return err
		}
		switch msg.Type {
		case "chat":
			text := strings.TrimSpace(msg.Text)
			if text == "" {
				return fmt.Errorf("cli server returned an empty chat message")
			}
			if strings.Contains(strings.ToLower(text), expected) {
				return nil
			}
			return fmt.Errorf("cli server returned an unexpected reply %q, want probe %q", text, probe)
		case "stream_append":
			streamed.WriteString(msg.Text)
			if strings.Contains(strings.ToLower(streamed.String()), expected) {
				streamMatched = true
			}
		case "stream_replace":
			streamed.Reset()
			streamed.WriteString(msg.Text)
			if strings.Contains(strings.ToLower(streamed.String()), expected) {
				streamMatched = true
			}
		case "stream_finish":
			if streamMatched {
				return nil
			}
			return fmt.Errorf("cli stream finished without the expected probe reply %q", probe)
		case "error":
			return fmt.Errorf("cli round-trip error: %s", msg.Text)
		}
	}
}

func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return strings.TrimSpace(text[:index])
	}
	return strings.TrimSpace(text)
}
