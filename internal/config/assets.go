package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type defaultAsset struct {
	Path    string
	Content string
}

var defaultConfigAssets = []defaultAsset{
	{Path: "app.toml", Content: defaultAppTOML},
	{Path: "services.toml", Content: defaultServicesTOML},
	{Path: "state.toml", Content: defaultStateTOML},
	{Path: "SOUL.md", Content: defaultSoulMD},
	{Path: "memories.toml", Content: defaultMemoriesTOML},
	{Path: "elnis.toml", Content: defaultElnisTOML},
	{Path: "tool_tags.toml", Content: defaultToolTagsTOML},
	{Path: "plugins/hooks.toml", Content: defaultHooksTOML},
	{Path: "plugins/.env", Content: defaultHookEnv},
	{Path: filepath.Join("skills", "agent", "agent_skill_creator", "SKILL.md"), Content: defaultAgentSkillCreatorSkillMD},
	{Path: filepath.Join("skills", "agent", "agent_skill_creator", "ELBOT_SKILL.toml"), Content: defaultAgentSkillCreatorSkillTOML},
	{Path: filepath.Join("skills", "agent", "write_elbot_hook", "SKILL.md"), Content: defaultWriteElbotHookSkillMD},
	{Path: filepath.Join("skills", "agent", "write_elbot_hook", "ELBOT_SKILL.toml"), Content: defaultWriteElbotHookSkillTOML},
	{Path: ".env.example", Content: defaultEnvExample},
}

var defaultConfigDirs = []string{
	"skills",
	filepath.Join("skills", "agent"),
	filepath.Join("skills", "go"),
	"plugins",
	"long_memory",
}

func EnsurePlatformDefaults() (string, error) {
	configPath, ok := platformDefaultConfigPath()
	if !ok {
		return "", fmt.Errorf("platform config dir is unavailable")
	}
	configDir := filepath.Dir(configPath)
	for _, dir := range defaultConfigDirs {
		path := filepath.Join(configDir, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("create default config dir %q: %w", path, err)
		}
	}
	for _, asset := range defaultConfigAssets {
		path := filepath.Join(configDir, asset.Path)
		if err := writeFileIfMissing(path, asset.Content); err != nil {
			return "", err
		}
	}
	return configPath, nil
}

func writeFileIfMissing(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat default config asset %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create default config asset dir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write default config asset %q: %w", path, err)
	}
	return nil
}

const defaultAppTOML = `# Main application config. Relative paths are resolved from this file.

[config_files]
# 共享的只读服务配置：大模型 provider、image_generation、model_profiles。
# 多个服务可以挂载并读取同一个 services.toml；密钥仍只放在 .env。
services = "services.toml"
# providers 仅用于兼容旧部署；当 services 存在时不再读取 providers.toml。
# providers = "providers.toml"
state = "state.toml"
elnis = "elnis.toml"
tool_tags = "tool_tags.toml"

[storage]
# Leave empty to use the platform default data directory.
sessions_sqlite_path = ""
chat_history_sqlite_path = ""
# Disk protection: ratios are used-space ratios; critical rejects non-essential media writes.
disk_warn_ratio = 0.85
disk_critical_ratio = 0.95
disk_min_free_bytes = 0

[runtime]
log_level = "info"
log_retention_days = 30

[maintenance.log_cleanup]
enabled = true
schedule = "0 3 * * *"

[maintenance.session_cleanup]
enabled = false
schedule = "15 3 * * *"
retention_days = 30

[maintenance.sandbox_cleanup]
enabled = true
schedule = "0 4 * * *"
retention_days = 7

[maintenance.chat_history_cleanup]
enabled = true
schedule = "35 4 * * *"
retention_days = 180

[maintenance.privacy_cleanup]
# clean-room angel_memory / self_learning 数据保留清理；具体保留天数在各自 section 配置。
enabled = true
schedule = "45 4 * * *"

# 每天两次的资源/用量报告（生图量、Token、费用、磁盘、内存）。
[maintenance.daily_report]
enabled = false
schedule = "0 9,21 * * *"    # 每天 9:00 和 21:00
window_hours = 12            # 每次统计最近 12 小时
provider = "deepseek"        # 统计哪个 provider 的 llm_usage；留空统计全部
# platform = ""              # 指定发送平台（按 superadmins 发送）；留空用主平台
currency = "CNY"
image_price_per_image = 0.05 # 生图服务每张价格（只统计成功出图；失败不计费）
peak_pricing = true          # DeepSeek 高峰/空闲双档
# holidays = ["2026-10-01", "2026-10-02", "2026-10-03"]  # 法定节假日按空闲价
# data_root = ""             # 默认取 SQLite 所在数据目录

# DeepSeek 官方单价（单位：元 / 百万 tokens，2026 定价页）。
# 基准字段是高峰价；offpeak_* 是空闲价，未填则沿用高峰价。
[maintenance.daily_report.prices."deepseek-v4-pro"]
input_per_million = 9.0
cache_input_per_million = 0.30
output_per_million = 27.0
offpeak_input_per_million = 4.5
offpeak_cache_input_per_million = 0.15
offpeak_output_per_million = 13.5

[maintenance.daily_report.prices."deepseek-flash"]
input_per_million = 2.0
cache_input_per_million = 0.04
output_per_million = 8.0
offpeak_input_per_million = 1.0
offpeak_cache_input_per_million = 0.02
offpeak_output_per_million = 4.0

# 旧模型名仍可调用，按 Flash 价格计费。
[maintenance.daily_report.prices."deepseek-v4-flash"]
input_per_million = 2.0
cache_input_per_million = 0.04
output_per_million = 8.0
offpeak_input_per_million = 1.0
offpeak_cache_input_per_million = 0.02
offpeak_output_per_million = 4.0

# ── 命名 profile：群聊消息里声明，仅本轮有效，仅超级管理员 ──
#
# 触发写法（可自定义）：
#   @model:强 / #模型:强 / #生图:高清 / #工具:管理
#   @m:pro / @image:hq / @use:admin
[turn_directives]
prefixes = ["@", "#"]                 # 触发符号，可再加 "！" 等
model_keywords = ["model", "m", "模型", "用模型"]
image_keywords = ["image", "img", "生图", "出图"]
tool_keywords = ["use", "工具", "用工具"]

# 模型 profile：默认模型仍来自 state.toml 的 mode_models.*。
# aliases 是中文/短标签，声明时用它代替 profile 名。
# [model_profiles.pro]
# provider = "deepseek"
# model = "deepseek-v4-pro"
# aliases = ["强", "强模型", "pro"]
#
# [model_profiles.cheap]
# provider = "deepseek"
# model = "deepseek-flash"
# aliases = ["快", "便宜", "flash"]

# 工具 profile：声明后为本轮额外注入这些工具（不写入 Session）。
# [tool_profiles.admin]
# tools = ["shell", "read_file", "edit_file"]
# aliases = ["管理", "运维"]

# 生图 profile：没写的字段沿用上面的 [image_generation] 基础配置。
# 用 [image_generation] default_profile = "fast" 可把某个 profile 设为默认。
# [image_generation.profiles.fast]
# base_url = "https://another-relay.example.com/v1"
# api_key_env = "IMAGE_API_KEY_FAST"
# model = "gpt-image-2.5"
# quality = "medium"
# superadmin_only = true
# aliases = ["快", "高清"]

[sandbox]
root = ""

[file_delivery]
# Controls media delivery in model requests: base64 uses inline data, s3 uses presigned download URLs, and hybrid uses S3 when the total media size in a request exceeds max_direct_base64_bytes. After switching to S3, existing local media will be uploaded when remote delivery is required, and previously recorded object keys will be reused.
# base64 will increase the file size by about 33%.
max_direct_base64_bytes = 8388608
backend = "base64"
s3_endpoint = ""
s3_region = "auto"
s3_bucket = ""
s3_access_key_env = "ELBOT_S3_ACCESS_KEY_ID"
s3_secret_key_env = "ELBOT_S3_SECRET_ACCESS_KEY"
s3_public_base_url = ""

[media]
# Images exceeding either limit are stored as compressed JPEGs on import; originals are not kept.
# Compression starts when the original image exceeds this byte threshold.
llm_image_compression_threshold_bytes = 4194304
# The maximum image width or height after compression.
llm_image_max_length = 4096

[platform_files]
max_receive_file_bytes = 104857600
download_timeout_secs = 60

[llm_request]
first_chunk_timeout_seconds = 180
stream_idle_timeout_seconds = 60
response_timeout_seconds = 0
max_retries = 3
retry_initial_delay_seconds = 2

[context]
compact_enabled = true
compact_trigger_ratio = 0.8
# 发送前的 prompt 上限比例，超过会触发长消息保护。
max_prompt_ratio = 0.8
# 预留的输出 token；0 表示根据模型窗口自动计算。
reserve_output_tokens = 0
# 单条用户消息占模型窗口的比例上限，超过会触发保护。
single_message_max_ratio = 0.5
# 压缩上下文时，每条历史用户原话保留的最大字符数，避免长消息撑爆摘要。
user_original_max_runes = 4000
# 长消息保护默认策略：reject / truncate / summarize。
# 群管理员可用 /*overflow 覆盖当前群的 chat/work 模式。
overflow_mode = "reject"

[soul]
path = "SOUL.md"

[character_library]
enabled = true
root = "characters"

[group_analysis]
# 群分析工具默认启用；它只读取本地 chat_history/outbound_messages，不复制第三方模板或素材。
enabled = true
# 单次统计最多读取多少条本地历史消息。
max_messages = 5000
# 可选：每天定时把统计/摘要发送到指定平台会话。默认关闭。
report_enabled = false
report_schedule = "0 9 * * *"
# report_platform = "qqonebot"      # 必填且 report_enabled=true 才会注册
# report_scope_id = "group:123456"
report_days = 1

[angel_memory]
# clean-room 长期记忆；默认启用。
enabled = true
# 0 表示不按时间清理。
retention_days = 365
# 单条记忆最大字符数（rune）；超过会拒绝写入。
# max_content_runes = 1000
# 单个会话最多保留多少条记忆。
# max_per_scope = 1000
# 每个会话每分钟最多写入次数。
# max_writes_per_minute = 30
# 每轮最多注入多少字符的上下文。
# max_context_runes = 1200

[self_learning]
# clean-room 表达/黑话学习；默认启用，但只有 review 通过的内容会注入。
enabled = true
retention_days = 365
# 候选至少出现多少次才进入 review。
min_count = 3
# 候选至少被多少个不同用户说过才进入 review；设为 1 允许单人复读形成候选。
# min_users = 2
# 单个候选含义的最大字符数。
# max_meaning_runes = 200
# 每轮最多注入多少字符的学习上下文。
# max_context_runes = 1200
# 单条观察最大字符数（rune）；超过会被截断后入库。
# max_observation_runes = 1000
# 单个会话最多保留多少条观察；超过会拒绝写入。
# max_observations_per_scope = 5000
# 每个会话每分钟最多写入多少条观察。
# max_observation_writes_per_minute = 60
# 单次挖掘最多扫描多少字符。
# max_mine_chars = 200000
# 单次挖掘超时秒数。
# mine_timeout_seconds = 10

# image_generation 已移到共享的只读 services.toml，避免多个服务各配一份。
# 旧部署仍可把 [image_generation] 写回这里；写了 services.toml 时以 services.toml 为准。

[view]
session_list_page_size = 10

[commands]
prefixes = ["/*"]

[tools]
max_rounds_per_turn = 10

[ops]
# 单次工具 / Hook / 上下文压缩的超时。0 表示不限时；生产建议设置明确上限。
tool_timeout_seconds = 600
hook_timeout_seconds = 60
compress_timeout_seconds = 300
# 同时运行的 turn / tool / hook 上限。0 表示不限制；超出上限的请求会明确拒绝。
max_concurrent_turns = 4
max_concurrent_tools = 4
max_concurrent_hooks = 4
# 可选：按用户 / 群聊限速；0 表示不限制。
user_messages_per_minute = 0
user_burst = 0
group_messages_per_minute = 0
group_burst = 0
rate_limit_idle_ttl_seconds = 600
# 可选：超过并发上限时允许短暂排队。turn 通常不建议排队，避免用户侧卡住。
queue_max_size = 0
queue_max_per_user = 0
queue_max_per_scope = 0
queue_wait_timeout_seconds = 0
queue_wait_kinds = ["tool", "hook", "compress"]
# 可选：Provider 实际调用并发上限与等待队列；0 表示不限制。
provider_max_concurrent = 0
provider_queue_max_size = 0
provider_wait_timeout_seconds = 0
# 可选：Provider 连续失败后的熔断；0 表示关闭。
circuit_breaker_failure_threshold = 0
circuit_breaker_open_cooldown_seconds = 60
circuit_breaker_half_open_max = 1

[budget_limits]
# 可选：全局 / 单用户每日额度；0 表示不限制。生图/视觉按调用次数，chat 按 token 与费用统计。
global_image_daily = 0
user_image_daily = 0
global_vision_daily = 0
user_vision_daily = 0
global_chat_tokens_daily = 0
user_chat_tokens_daily = 0
global_chat_cost_daily = 0
user_chat_cost_daily = 0

[resident_memory]
# Memory length units: CJK characters count as one each; English/digits count by word.
core_max_units = 200
normal_max_units = 300

# P1/P2 normal 写入保护。显式写 0 可关闭对应限制。
# 最小写入间隔（秒）；防止短时间内反复改写 normal。
normal_write_min_interval_seconds = 5
# 写入频率窗口（秒）与窗口内最大写入次数。
normal_write_window_seconds = 60
normal_write_max_per_window = 6
# normal 最大条目数（每条一行）；0 表示不限制。
normal_max_lines = 20
# 单条 normal 的最大长度；0 表示不限制。
normal_max_units_per_entry = 80
# 拒绝明显的指令类内容（如 "ignore previous instructions"）；false 关闭。
normal_block_instruction_patterns = true

[security]
user_max_tool_risk = "low"
superadmin_confirm_risk = "high"

[security.superadmins]
cli = ["local"]

[session]

[session.idle_expiration]
group_user_ttl_minutes = 10
group_superadmin_ttl_minutes = 10
private_user_ttl_minutes = 10
private_superadmin_ttl_minutes = 0

[session.naming]
trigger_step = 3

[platform.cli]
enabled = true
# Default CLI client profile. Used by elbot/elbot cli when -c is omitted.
default_client = "local"
# Default WebSocket URL for clients without their own clients.<name>.url.
# To connect to another machine, set url under the client profile.
default_url = "ws://127.0.0.1:32172/cli/v1/ws"

# Used only when this ElBot runs as a CLI server. It listens here; clients connect via their url.
# Container deployment: use "0.0.0.0:32172" so the host port mapping can reach it; keep the host bind on 127.0.0.1.
[platform.cli.server]
enabled = false
listen = "127.0.0.1:32172"

# Client ids allowed to log in to this CLI server and their token environment variables.
[platform.cli.server.tokens]
local = ["ELBOT_CLI_LOCAL_TOKEN"]

# Client profile used by this command. For remote servers, add url = "ws://SERVER_IP:32172/cli/v1/ws".
[platform.cli.clients.local]
token_env = ["ELBOT_CLI_LOCAL_TOKEN"]

# [platform.qqonebot]
# enabled = false
# ws_url = "ws://127.0.0.1:6700/" # native/local OneBot. Inside a container 127.0.0.1 points to ElBot itself; use a Compose service name or reachable host address.
# access_token = "" # legacy direct value; optional
# access_token_env = "QQONEBOT_ACCESS_TOKEN" # optional; reads process env, then config .env
# api_timeout_seconds = 15 # base timeout for OneBot writes and API responses
# trigger_keywords = ["bot"]
# send_file_mode = "base64" # base64 works across machines; use file_uri for a shared filesystem

# [platform.telegram]
# enabled = false
# bot_token_env = "TELEGRAM_BOT_TOKEN"
# proxy_url_env = "TELEGRAM_PROXY_URL" # optional; read OS env first, then config .env
# trigger_keywords = ["bot"]
# format = "html" # html/plain/rich
# stream_edit_interval_milliseconds = 250
`

const defaultMemoriesTOML = `# Resident memory data. Generated by ElBot.
# Stores per-platform, per-actor resident memories.
`

const defaultProvidersTOML = `# Provider/model config. Do not commit real API keys.
# Prefer api_key_env and set secrets in the OS environment or .env.

[providers.deepseek]
base_url = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"

[providers.openai]
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
models = ["gpt-4o-mini"]
# fallback_provider = "deepseek"
# fallback_model = "deepseek-chat"
# fallback_mode = "circuit"          # circuit（默认，熔断后接管）/ on_error（首个失败请求即切换）/ off
# fallback_on_error = false         # 等价于 fallback_mode = "on_error"
# fallback_timeout_seconds = 0      # 单次 Provider 尝试的总超时；0 表示沿用现有流式超时控制

# [providers.openai.model_configs."gpt-4o-mini"]
# context_window = 128000
# extra_payload = { }

[model_metadata]
default_context_window = 256000
`

const defaultImageGenerationTOML = `# Optional image generation tool. The endpoint must be OpenAI-compatible
# POST {base_url}/images/generations. Provide the API key via api_key_env.
[image_generation]
enabled = false
# base_url = "https://your-relay.example.com/v1"  # /images/generations is appended
# endpoint = ""                                    # override the full URL when the relay differs
api_key_env = "IMAGE_API_KEY"
# api_key = ""                                     # discouraged; prefer api_key_env
model = "gpt-image-2.5"
size = "1024x1024"
quality = "high"          # low / medium / high (depends on the relay)
output_format = "png"     # png / jpeg / webp
# response_format = "b64_json"  # set only if the relay requires it
timeout_seconds = 180

# 预设提示词：最终 prompt = preset_prompt + 角色预设 + 场景描述。
preset_prompt = ""
# negative_prompt = ""
max_prompt_runes = 4000

# 提示词优化：off 或 rules（使用内置的 GPT Image Prompts 大全规则库）。
optimize = "rules"
optimize_term_mode = "phrase"   # phrase：短语锚点；tag：单个单词 tag 串
optimize_max_anchors = 4
optimize_max_negatives = 10
optimize_max_added_runes = 400
optimize_max_tags = 12

# LLM 语义改写：off / auto（短或含糊的 prompt 才改写）/ always。
optimize_rewrite = "auto"
optimize_rewrite_model = "naming"   # naming / compact / chat / work
optimize_rewrite_min_runes = 40

# 自动编排：从 prompt 里识别角色名/别名并自动选角、自动拉当前群聊上下文。
auto_character = true
auto_context = true
context_default_limit = 6

# 权限与落盘。
superadmin_only = true       # 只有超级管理员能调用 image_generate
save_to_character = true     # 出图写回当前角色的 images/
send_by_default = false      # 生成后是否默认发到当前聊天

# 参考图（视中转站是否支持）。开启后会把角色图片按 reference_field 字段透传。
supports_reference = false
reference_field = "image"

# extra_payload = { }
# extra_headers = { }
# proxy = ""

# 生图独立并发限制；0 表示不限制。queue_size > 0 时超限会短暂排队。
max_concurrent = 0
queue_size = 0
queue_timeout_seconds = 0
`

// defaultImageToPromptTOML is the optional built-in image_to_prompt backend.
// It reuses a [providers.*] entry as its vision model, so no extra key is needed.
const defaultImageToPromptTOML = `# Optional image_to_prompt tool: turn a reference image into a drawing prompt.
# It calls a vision-capable model from the [providers.*] entries above, so it
# reuses the same api_key_env, proxy and retry settings. Setting provider+model
# is enough to enable it; enabled=false force-disables it. An explicitly
# enabled section with a missing provider/model fails startup instead of
# silently disappearing.
# [image_to_prompt]
# provider = "openai"
# model = "gpt-4o-mini"
# max_tokens = 400          # cap the prompt returned to the chat model
# temperature = 0.2         # 0 = do not override the provider's own temperature
# max_edge = 1536           # downscale the long edge before upload; 0 keeps original
# max_image_bytes = 12582912
# timeout_seconds = 90
`

// defaultVisionTOML documents the optional automatic vision fallback. It is
// opt-in because it adds one extra model call per described image.
const defaultVisionTOML = `# Optional automatic vision fallback: when a text-only chat model rejects image
# content, ElBot describes the images with this vision model and retries with
# the descriptions. It reuses [providers.*], so no extra key is needed.
# Disabled unless enabled = true. When provider/model are omitted they are
# inherited from [image_to_prompt] above.
# [vision]
# enabled = false
# provider = "openai"        # optional; defaults to [image_to_prompt].provider
# model = "gpt-4o-mini"      # optional; defaults to [image_to_prompt].model
# max_tokens = 400
# temperature = 0.2
# max_edge = 1536
# max_image_bytes = 12582912
# timeout_seconds = 90
# language = "zh"            # description language: zh or en
# cache_ttl_seconds = 1800
# negative_cache_ttl_seconds = 30
`

// defaultServicesTOML is the single read-only service config generated for new
// installs. It combines the legacy providers.toml and the [image_generation]
// section. Runtime state stays in state.toml; secrets stay in .env.
const defaultServicesTOML = `# Shared read-only service config for ElBot and compatible services.
# - LLM providers: [providers.*] and [model_metadata]
# - Image generation: [image_generation]
# - Image to prompt: [image_to_prompt]
# - Vision fallback: [vision]
# - Secrets: reference .env via api_key_env; never put real keys in this file.
# - Runtime state: keep state.toml separate; ElBot rewrites it at runtime.
# Other services may mount this file read-only and consume only their sections.

` + defaultProvidersTOML + `
` + defaultImageGenerationTOML + `
` + defaultImageToPromptTOML + `
` + defaultVisionTOML

const defaultStateTOML = `[session]
default_mode = "work"

[mode_models.work]
provider = "deepseek"
model = "deepseek-v4-pro"

[mode_models.chat]
provider = "deepseek"
model = "deepseek-v4-flash"

# Optional Elnis LLM model slots. If omitted, each slot falls back to work.
[mode_models.elwisp1]
provider = "deepseek"
model = "deepseek-v4-flash"

[mode_models.elwisp2]
provider = "deepseek"
model = "deepseek-v4-flash"

[mode_models.elwisp3]
provider = "deepseek"
model = "deepseek-v4-flash"
`

const defaultSoulMD = `You are ElBot, a helpful assistant. ElBot's repo is https://github.com/fujiwarashigure/elbot_mk.2.
Keep responses concise, accurate, and friendly. Follow the user's language unless they ask otherwise.
`

const defaultElnisTOML = `# Elnis listening hub config. Loaded from app.toml [config_files].elnis.

enabled = false
# Default ElBot internal tools that Elwisp events may preload.
allowed_tools = ["web_search", "web_extract"]

[http]
# Container deployment: enable Elnis and use "0.0.0.0:32170"; host port should bind 127.0.0.1.
# /healthz only exists when Elnis is enabled and does not prove model/OneBot/CLI health.
addr = "127.0.0.1:32170"
max_body_bytes = 1048576
queue_size = 128
workers = 2
read_header_timeout_seconds = 5 # Request header read deadline.
read_timeout_seconds = 30       # Full request read deadline, including the body.
write_timeout_seconds = 300     # Handler and response write deadline; direct mode can perform external I/O.
idle_timeout_seconds = 60       # Keep-alive idle connection deadline.

[tokens.home]
# Read token values from OS environment variables or the config directory .env file.
token_env = ["ELNIS_HOME_TOKEN", "ELNIS_HOME_TOKEN_ALT"]

# Delivery is allowed by default. Targets listed here are explicitly disabled.
# In disabled config, platform-only disables all delivery to that platform.
[delivery_disabled]
targets = [
  # { platform = "telegram" },
  # { platform = "telegram", type = "private", id = "123456789" },
  # { platform = "qqonebot", type = "group", id = "987654321" },
]

[segment]
max_file_bytes = 104857600  # 100MB, max per image/file segment
download_timeout_secs = 60

# Elwisp is enabled by default. Configure a named Elwisp only when you need
# token restrictions, tool overrides, delivery disables, or explicit disable.
# [elwisps.server-watchdog]
# allowed_tokens = ["home"]
# allowed_tools = ["shell", "web_search"]
# disabled_external_tools = ["danger_tool"]
# disabled_targets = [
#   { platform = "qqonebot", type = "group", id = "987654321" },
# ]

#
# [elwisps.spike-checker]
# enabled = false
`

const defaultAgentSkillCreatorSkillMD = `---
name: agent_skill_creator
description: 创建或修改 AgentSkill 的 ELBOT_SKILL.toml。
---

if a skill 带脚本:
    if 你没有该脚本的Schema只能用shell来运行 and 你觉得使用shell很麻烦:
        使用该工具创建 ELBOT_SKILL.toml，之后会自动注入你的Schema，之后可以不使用shell运行
elif:
    检查你已知Schema工具，是否有该技能的脚本，就可以直接调用，而不用shell
ELBOT_SKILL.toml写法：
只允许这些字段：
risk, superadmin_only, tags, command, timeout_seconds, expose_root, parameters, [args]

纯文档型 Skill 只限制可见性时，可以只写：
risk = "high"
superadmin_only = true

示例：
risk = "medium"
superadmin_only = false
tags = ["doc"]
command = ["python", "foo.py"]
timeout_seconds = 30
expose_root = false

parameters = '''
{
  "type": "object",
  "required": ["input"],
  "properties": {
    "input": {"type": "string", "description": "输入文本"},
    "mode": {"type": "string", "description": "处理模式"},
    "items": {"type": "array", "items": {"type": "string"}, "description": "输入项列表"},
    "options": {"type": "object", "description": "附加选项"}
  }
}
'''

[args]
input = "--input"
mode = "--mode"
items = "--items"
options = "--options"

含义：
工具调用 {"input":"abc","mode":"fast","items":["a","b"],"options":{"dry_run":true}} 会按参数名顺序执行：
python foo.py --input abc --items ["a","b"] --mode fast --options {"dry_run":true}
数组和对象会压缩为 JSON，并各自作为 flag 后的一个 argv 参数传入，不经过 shell。

command 必须是字符串数组。
parameters 必须是 JSON object schema。
parameters.properties 定义工具有哪些入参；[args] 的 key 必须对应 parameters.properties。
risk 可选；注册成普通工具时必填。纯文档型未写 risk 时按 safe 处理。
superadmin_only 可选，true 时只有超级管理员可发现或使用。
tags 可选，相当于为该工具分类。

创建 AgentSkill：
在配置目录的 skills/agent/<name>/SKILL.md 编写 AgentSkill 说明。Windows 默认位置是 %APPDATA%/ElBot/skills/agent/<name>/SKILL.md；Linux 遵循 XDG 配置目录，通常是 $XDG_CONFIG_HOME/elbot/skills/agent/<name>/SKILL.md。
AgentSkill 适合文档型任务、外部脚本包装、临时或低频流程。
如果要把该 AgentSkill 注册成普通工具，再为它创建 ELBOT_SKILL.toml。

需要媒体的 AgentSkill 才在其说明中加入以下约定：
- 普通 AgentSkill 使用稳定的 media:<64位小写SHA-256>，将它传给支持媒体的工具。
- bash 脚本通过 shell 的 media_inputs 显式声明输入，例如 {"cmd":"python generate.py --input \"$ELBOT_MEDIA_1\"","media_inputs":[{"media":"media:<sha256>"}]}。PowerShell 使用 $env:ELBOT_MEDIA_1。不要硬编码路径。
- TOML 工具的单个媒体参数声明为 {"type":"media"}；多个媒体声明为 {"type":"array","items":{"type":"media"}}，[args] 正常映射 flag。LLM 传媒体 ID，进程收到相对 Skill 根目录的临时路径；媒体数组作为保留顺序和重复项的紧凑 JSON 数组放在一个 argv 中。普通 string 或 string 数组不自动转换。调用结束会清理输入的临时副本和临时引用。
- 工具 stdout 可以返回 {"content":"完成","segments":[...]}。segments 支持 text、image、file；text 使用 text 字段，image/file 必须且只能使用 media 或 path，另可提供 name、mime_type。media 必须是稳定媒体 ID；path 必须是 Skill 根目录内的相对路径，禁止绝对路径、.. 和链接逃逸。
- 宿主会把 stdout path 指向的文件导入 Media Center，并在工具结果中替换为稳定媒体 ID。源文件不会自动删除；需要保留或清理由 Skill 自己决定。stdout 输出前不得删除该文件，因为宿主会在进程退出后读取并导入。

AgentSkill 和 EL Skill 分开选择：
高性能、强结构化、需要校验/编译/长期维护的任务，优先使用 EL Skill。
`

const defaultAgentSkillCreatorSkillTOML = `risk = "low"
superadmin_only = true
`

const defaultWriteElbotHookSkillMD = `---
name: write_elbot_hook
description: 编写或修改 ElBot 规则 Hook 配置。
---

hook路径：
windows：%AppData%/ElBot/plugins/hooks.toml
Linux：= $XDG_CONFIG_HOME/elbot/plugins/hooks.toml
若 XDG_CONFIG_HOME 未设置，按 XDG 规范使用 $HOME/.config

简单hook直接参考hooks.toml中的注释写，复杂hook看https://raw.githubusercontent.com/fujiwarashigure/elbot_mk.2/main/docs/hooks.md
修改完hooks.md后提醒用户使用 /hooks reload 重新加载
`

const defaultWriteElbotHookSkillTOML = `risk = "low"
superadmin_only = true
`
const defaultEnvExample = `# Copy this file to .env or set these variables in your OS environment.
# Variables in .env are also available to LLM shell commands.

# Provider API keys
DEEPSEEK_API_KEY=
OPENAI_API_KEY=

# Platform secrets
QQOFFICIAL_CLIENT_SECRET=
QQONEBOT_ACCESS_TOKEN=
TELEGRAM_BOT_TOKEN=
TELEGRAM_PROXY_URL=

# Web tools
JINA_API_KEY=
WEB_EXTRACT_PROXY=
TAVILY_API_KEY=

# CLI remote client/server tokens
ELBOT_CLI_LOCAL_TOKEN=
ELBOT_CLI_WINDOWS_TOKEN=

# Elnis tokens
ELNIS_HOME_TOKEN=
ELNIS_HOME_TOKEN_ALT=
`

const defaultToolTagsTOML = `# Tool tag config. Prompts are appended to system prompt only after the tag is activated by @tool:<tag>.
[tags.agent]
tools = ["read_file", "edit_file", "shell", "long_memory", "long_memory_search", "long_memory_write"]
prompt = """
ROLE: Complete the user's task safely and accurately.
MUST:
- Inspect context first; do not make things up.
- Use tools when possible; follow required syntax.
- Plan before coding; ask if unclear or risky.
- Touch only what must be changed; keep it simple.
- Validate success criteria before and after implementation.
"""
`

const defaultHookEnv = `# Shared environment for Hook processes.
# PATH only needs additional executable directories; inherited system paths are kept.
# PATH=/absolute/path/to/bin
# HTTP_PROXY=http://127.0.0.1:7890
# HTTPS_PROXY=http://127.0.0.1:7890
# NO_PROXY=localhost,127.0.0.1,::1
`

const defaultHooksTOML = `# Declarative Hook rules. Loaded at ElBot startup.
# Complex logic should be implemented as a code plugin instead.
# Full docs and examples: https://github.com/fujiwarashigure/elbot_mk.2/blob/main/docs/hooks.md
#
# Optional plugin configs:
# [[plugins]]
# name = "demo"
# enabled = true
# path = "demo/hook.toml" # optional; default is plugins/<name>/hook.toml
#
# Plugin hook.toml may include:
# [plugin]
# name = "demo" # optional metadata; [[plugins]].name is the reference name
# description = "demo plugin"
# blocked_platform = [] # applies to every rule and Worker in this plugin
# blocked_group = ["qqonebot:123456"]
# blocked_id = ["qqonebot:10001"]
#
# Rule shape:
# [[rules]]
# name = "stable_debug_name"
# description = "short summary" # optional, recommended
# on = "hook.point"
# enabled = true          # optional, default true
# priority = 1000        # optional, smaller runs earlier
# wakeup = "required"   # optional: required (default), any, or forbidden.
# Direct rules in this root hooks.toml may set blocked_platform, blocked_group,
# and blocked_id independently. Plugin rules use [plugin] instead.
# blocked_platform = []
# blocked_group = ["qqonebot:123456"]
# blocked_id = ["qqonebot:10001"]
#
# Single condition:
# if = "message.text"
# op = "contains"
# value = "hello"
#
# No condition:
# always = true
#
# Multiple conditions are AND:
# match = [
#   { field = "platform.name", op = "fullmatch", value = "qqonebot" },
#   { field = "message.text", op = "contains", value = "猫" },
# ]
#
# Single action:
# action = "send"        # send/prepend/append/replace/delete/tool/exec
# text = "..."
# timing = "after_assistant" # optional for send outputs; default immediate.
#
# Multiple actions run in order:
# actions = [
#   { type = "replace", field = "message.text", pattern = "猫", replace = "狗", all = true },
#   { type = "send", kind = "text", text = "检测到关键词", timing = "after_assistant" },
#   { type = "append", field = "message.text", text = "!" },
# ]
#
# send action with outputs (kind/text/url/path/base64/name/mime_type/user_id/message_id/emoticon_id):
# target.platform/target.scope_id/target.private_user_id/target.group_id/target.superadmins
# can redirect send outputs; omit target to send to the current context.
# actions = [
#   { type = "send", timing = "after_assistant", outputs = [
#     { kind = "text", text = "检测到关键词" },
#     { kind = "image", path = "alert.png" },
#     { kind = "image", name = "微笑", path = "emoticons/微笑/01.png" },
#     { kind = "at", user_id = "123456" },
#   ] },
# ]
#
# exec action uses hook.v2 line protocol:
# ElBot sends request system.init, then request event.handle on stdin.
# Scripts reply with response frames using the Host request ID (host:*).
# Hook-originated requests use plugin:* IDs; stdout only contains request/response/event frames.
# stderr is logged on success; on exec failure/crash/timeout/protocol error, the
# stderr tail is included in the Hook failure notice.
# A one-shot script responds to event.handle with {"status":"completed",...}
# and exits with code 0; non-zero exit means process failure.
# request frame shape: {"type":"request","id":"plugin:x","method":"platform.call","params":{...}}.
# response frame shape: {"type":"response","id":"x","ok":true,"result":{...}} or
# {"type":"response","id":"x","ok":false,"error":"..."}.
# event.handle result.result is available as {{actions.<name>.result}}.
# event.handle result.error is available as {{actions.<name>.error}}.
# event.handle result.message.text is written back to the action field.
# event.handle result.outputs, consume and stop_propagation apply Hook output/control.
# actions = [
#   { action_name = "extract", type = "exec", command = ["uv", "run", "extract.py"], field = "llm.text", timing = "after_assistant" },
# ]
#
# Supported hook points:
# platform.connected, platform.message.received, agent.input.prepared,
# llm.turn.prepared, llm.request.prepared, llm.response.received,
# tool.call.prepared, tool.call.completed, agent.output.prepared,
# agent.turn.output.prepared, platform.message.sent, error.occurred
#
# Match ops: always, exists, contains, fullmatch, startswith, endswith, regex.
# Common fields:
# platform.name/scope_id/user_id/conversation_id/message_id/reply_to_message_id
# actor.id/user_id/role/group_role/display_name
# session.id/mode/title/status
# request.id/kind/session_id/phase (kind: turn,llm,tool,compress,sub_agent; phase: idle,llm,tool,awaiting_risk_confirm,awaiting_append_confirm,compact)
# message.id/text/display_text/platform_text/intent_text/role
# message.intent_text strips wakeup keywords and bot mentions; use it for user intent matching.
# message.reply.message_id/sender_id/text/display_text
# llm.text/source_text/latest_user_text/latest_user_display_text/provider/model
# tool.name/arguments/result/risk
# error.message
#
# wakeup="any" on platform.message.received also observes ordinary group messages
# that did not mention or wake the bot. wakeup="forbidden" observes only those
# messages and skips the rule when the user explicitly wakes the bot. Hook outputs
# may still be sent, but command/LLM processing only continues for woken messages.
#
# Editable fields:
# platform.message.received / agent.input.prepared: message.text
# llm.turn.prepared / llm.request.prepared: llm.latest_user_text
# llm.response.received: llm.text
# tool.call.prepared: tool.arguments
# tool.call.completed: tool.result
# agent.output.prepared / agent.turn.output.prepared / platform.message.sent: message.text
#
# Template variables include:
# {{platform.name}}, {{platform.scope_id}}, {{platform.user_id}}, {{platform.message_id}}, {{platform.reply_to_message_id}}
# {{actor.id}}, {{actor.user_id}}, {{actor.role}}, {{actor.group_role}}
# {{message.text}}, {{message.display_text}}, {{message.platform_text}}, {{message.intent_text}}
# {{message.reply.message_id}}, {{message.reply.sender_id}}, {{message.reply.text}}, {{message.reply.display_text}}
# {{llm.text}}, {{llm.source_text}}, {{llm.latest_user_text}}, {{llm.latest_user_display_text}}
# {{tool.arguments}}, {{tool.result}}
# {{error.message}}
# {{match.regex.0.group.1}}, {{match.regex.0.<name>}}
# {{actions.<name>.result}}, {{actions.<name>.error}} from earlier tool actions.

# Notify qqonebot superadmins after OneBot connects.
[[rules]]
name = "notify_qqonebot_connected"
on = "platform.connected"
priority = 1000
if = "platform.name"
op = "fullmatch"
value = "qqonebot"
action = "send"
kind = "text"
text = "ElBot 已连接 QQ OneBot。"
target.superadmins = true

# Example: append a low-risk tool result to the same user message before the LLM request.
# [[rules]]
# name = "inject_web_search"
# on = "llm.request.prepared"
# priority = 1000
# always = true
# actions = [
#   { action_name = "search", type = "tool", tool = "web_search", arguments = '{"query":"ElBot"}' },
#   { type = "append", field = "llm.latest_user_text", text = "\n\nHook 工具结果：{{actions.search.result}}" },
# ]
#
# Example: modify the final assistant output shown for one turn without changing LLM history.
# [[rules]]
# name = "cat_to_dog_final_output"
# on = "agent.turn.output.prepared"
# always = true
# action = "replace"
# field = "message.text"
# pattern = "猫"
# replace = "狗"
# all = true
#
# Example: multiple conditions with one action.
# [[rules]]
# name = "cat_to_dog"
# on = "agent.input.prepared"
# match = [
#   { field = "platform.name", op = "fullmatch", value = "qqonebot" },
#   { field = "message.text", op = "contains", value = "猫" },
# ]
# action = "replace"
# field = "message.text"
# pattern = "猫"
# replace = "狗"
# all = true
#
# Example: emoticon extraction via exec + hook.v2.
# The script answers system.init, handles event.handle, and returns outputs plus message.text.
# [[rules]]
# name = "emoticon_extract"
# on = "llm.response.received"
# priority = 1000
# if = "llm.text"
# op = "regex"
# value = "\\[\\[[^\\[\\]]+\\]\\]"
# actions = [
#   { action_name = "extract", type = "exec", command = ["uv", "run", "emoticon_extract.py"], field = "llm.text", timing = "after_assistant" },
# ]
`
