<!-- This file is auto-translated from README.zh-CN.md. Do not edit manually. -->

# ElBot

[中文](README.zh-CN.md) | English

ElBot is a lightweight Agent/Chatbot framework written in Go, aiming to minimize operating costs, context costs, and maintenance complexity while preserving extensibility.
It supports general chat, tool calling, Hook extensions, long-term task scheduling, persistent Sessions, and context compaction, making it suitable for scenarios such as personal assistants, platform bots, and orchestratable automation assistants.

## Features

### I. Lightweight and Efficient

**Ultra-lightweight Go implementation**:

| Metric           | Value                            |
| -------------- | ------------------------------- |
| Local startup time   | <10ms (tested on N5105, SATA SSD) |
| Resident memory       | ~30MB                           |
| Binary file size | <30MB                           |

**Token-efficient tool discovery**: Research shows that many ordinary users still primarily use LLM-like products as advanced search engines, writing assistants, and listening objects; frequent tool calls are not the norm for all conversations.
Reference: Chatterji et al., _How People Use ChatGPT_, NBER, 2025;Yan et al., _ShareChat: A Dataset of Chatbot Conversations in the Wild_, arXiv:2512.17843, 2025。

ElBot does not inject the full schema of all tools by default in every round of conversation, but only exposes `discover_tool` and the names of currently available tools. When the model needs to use a tool, it first discovers the tool details on demand, and then the Agent injects the corresponding schema. Greatly reduces invalid context overhead.

**Chat / Work dual mode**: Both modes can be configured with independent models, allowing low-cost models to handle casual chat and powerful models to focus on complex tasks.

**Layering of resident memory and long-term memory**: Resident memory only saves short, stable information that truly needs to be injected into every round, and internally distinguishes between 'core' (requiring confirmation for modification) and 'normal' (organizable); Longer and more complex memories are queried by the LLM on demand via `long_memory`. Long-term memory uses Markdown source data and SQLite FTS, balancing transparency and retrieval efficiency.

| Mode   | Tool               | Applicable Scenarios                                 | Token consumption for the first request      |
| ------ | ------------------ | ---------------------------------------- | -------------------------- |
| `chat` | No injection             | Small talk, companionship, lightweight Q&A, low-cost conversation         | <500 (subsequent cache hit 95%+)  |
| `work` | Enable tool discovery and invocation | Complex tasks such as search, files, commands, Cron, Skills, etc. | <1000 (subsequent cache hit 90%+) |

### II. Powerful and Extensible

**Extensible Hook system**: ElBot has a built-in Hook Layer, allowing extension logic to be inserted at key event points such as Agent input, LLM request, LLM response, platform sending, and platform connection. Hooks can modify messages, append output intents, call scripts, and more. Hook supports writing plugins in **any language**.

**Standard Cron and LLM Cron**: ElBot features a built-in Cron Runtime and an LLM-orchestratable Cron service. Standard Cron sends fixed content directly according to a schedule; LLM Cron drives model execution using task descriptions, making it suitable for scheduled tasks that require analysis, summarization, or the use of tools.

**ELyph Task Notation**: ELyph is used to describe LLM Cron and native skills. The goal is to reduce ambiguity in natural language task descriptions and use a shorter, more stable structure to express inputs, outputs, steps, conditions, and constraints. Compared to arbitrary Markdown, ELyph is better suited for reusing and passing tasks between LLMs, and is also easier to lint, audit, and process with tools.

**EL Skills creatable by LLM**: ElBot has a built-in `create_el_skill` meta-tool, allowing the LLM to crystallize reusable experience into EL Skills. Automatically validate ELyph syntax upon creation, with optional Go source code attachment and compilation; The pure ELyph text or Go source code created is maintained by a unified `read_el_skill` / `modify_el_skill`; after the source code is modified, it is uniformly formatted and compiled via `finalize_el_skill`, and the check results are returned.

**Compatible with external AgentSkill**: ElBot is compatible with external AgentSkills that follow the agentskills.io style. Any AgentSkill script can be used as a tool through configuration.

### III. Elnis Event Perception System

Traditional Agents usually only wait for user input; Cron can only respond to time. Elnis provides ElBot with another trigger method: external events.

Elnis is the listening hub of ElBot, Elwisp consists of external listeners distributed in various locations, and Elvena is a unified JSON over HTTP event protocol. Working together, these three allow any signal from the external world—such as server alerts, RSS updates, Webhooks, game events, or even external computer information—to be sent to Elnis, then processed and returned by ElBot.

For detailed information, see [Elnis Listening Hub](docs.en/elnis.md).

### IV. Flexible Deployment and Enhanced Sessions

**Multi-platform and Rich Output Abstraction**: ElBot abstracts the platform and output layers, currently supporting CLI, QQ OneBot, QQ Official, and Telegram, while reserving space for extending to other platforms.

**CLI Client/Server Separation**: Supports using ElBot as a client on any computer to connect to the ElBot server. **Free Frontend Customization**, allowing you to create any frontend interface you prefer. The following screenshots demonstrate different frontend forms; except for the TUI, all are conceptual HTML mockups and do not represent the final UI.

<p align="center">
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/frontend_1.png" width="260" />
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/frontend_2.jpg" width="260" />
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/tui.png" width="260" />
</p>

For more screenshots, see [elbot-showcase/frontend](https://github.com/Elfreese/elbot-showcase/tree/main/frontend).

**Session, Fork, and Context Compaction**: Built-in persistent Session service, supporting Session recovery, archiving, pinning, Forking, deletion, paginated viewing, and platform isolation. Long conversations automatically trigger context compaction to keep the window controllable; normal conversation can continue after compaction.

### V. Secure and Reliable

**Security Policies and Risk Confirmation**: The tool system has built-in risk levels, role permission checks, and high-risk confirmation processes. Regular users can only discover and invoke low-risk tools; even superadmins must confirm each item when invoking high-risk tools.

**Lightweight Sandbox Isolation**: Background Shell execution is subject to AST-level sandbox constraints. Background tasks have an independent sandbox working directory, reducing the impact of misoperations.

**Comprehensive Logging and Auditing**: Distinguishes between runtime logs, Elwisp logs, and audit logs, supporting structured fields, log queries, audit queries, and runtime debugging.

## Usage

Common startup methods:

```bash
elbot              # Automatic mode: Prioritize attempting the default remote CLI client; fall back to full foreground startup when local is unreachable
elbot run          # Full foreground: Local CLI + Enabled platforms + Cron
elbot cli [-c name]# Remote CLI client: Connect to a resident ElBot server
elbot -c name      # Connect to the server directly using a specified CLI client profile
elbot service run  # Linux/headless service mode: Do not start local CLI; remote CLI server, platforms, and Cron can be enabled
```

Shell completion can be generated via `elbot completion <shell>`, supporting `bash`, `zsh`, `fish`, `nushell`, `powershell`, and `auto`.

Minimum usage flow:

1. Configure the OpenAI-compatible Provider in `config/providers.toml`.
2. Set the API Key corresponding to `api_key_env` via system environment variables or the configuration directory `.env`.
3. After starting, use the command `/models` to view and then use `/model xx` to select the model. Or manually select the default `chat` / `work` mode and model in `config/state.toml`.
4. Enter `/help` to view commands, or start a conversation directly.

For detailed instructions, see:

- [Quick Start](docs.en/getting-started.md)
- [Configuration Guide](docs.en/configuration.md)
- [Command Cheat Sheet](docs.en/commands.md)
- [Core Concepts](docs.en/concepts.md)
- [Elnis Listening Hub](docs.en/elnis.md)
- [Elnis Configuration and Usage](docs.en/elnis-usage.md)
- [Frontend API](docs.en/frontend-api.md)

Development plan and task decomposition: [devdocs](devdocs/).

## Local Fork: Deployment & Operations Enhancements

This fork keeps the upstream Agent/Chatbot core and adds a production-oriented Docker deployment and operations layer for VPS / BaoTa environments. The goal is to avoid the "container is healthy, but the bot is already stuck" failure mode, without ever implementing "kill the process whenever CPU is high".

### Independent Health Endpoints

ElBot exposes an operations HTTP server that does not depend on Elnis:

| Endpoint | Meaning |
| --- | --- |
| `/live` | Whether the critical scheduling loop is still progressing. |
| `/ready` | SQLite/data directories writable, required components initialized, configured platforms connected at least once. |
| `/healthz` | Aggregate status. External model failures appear as `degraded` and must not trigger an automatic restart. |
| `/tasks` | Active turn / tool / hook / context-compaction tasks, stage, start time, latest progress. |
| `/metrics` | Task counts, oldest task age, goroutine / heap / RSS / disk, platform connect counts, model health, rate-limit counters, image-generation limiter state. |

```dotenv
ELBOT_HEALTH_ADDR=0.0.0.0:32171
ELBOT_HEALTH_LIVE_STALE_SECONDS=90
```

Compose maps the port only to the host loopback: `127.0.0.1:32171:32171`. The standard Dockerfile `HEALTHCHECK` now calls `curl -fsS http://127.0.0.1:32171/live` instead of checking a PID file. Do not expose `32171` through Nginx.

### Data Volume, First Start, and Config

- Compose mounts `./data` to `/data` and sets `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_RUNTIME_DIR`.
- The container runs as UID/GID `10001`; `deploy/data` must be writable by that UID.
- `deploy/init-host.sh` creates directories, applies ownership, and prints a first-start checklist.
- After first start check `providers.toml` (`api_key_env` names), `state.toml` (provider/model match), and `app.toml` (CLI / OneBot / Elnis / image / security).
- `.env` changes require `docker compose up -d --force-recreate`; TOML-only changes can use `docker compose restart`.

### Container Networking

- QQ OneBot `ws_url` must be resolved from inside the ElBot container: service name for the same Compose network, `host.docker.internal` plus `extra_hosts` for the host, or a reachable remote IP. Never use `127.0.0.1` inside the container.
- Keep `send_file_mode = "base64"` unless ElBot and OneBot share a filesystem.
- Remote CLI server must listen on `0.0.0.0:32172` inside the container; the host still binds only `127.0.0.1` and exposes it through HTTPS/WSS reverse proxy.
- `32170` is the Elnis entry. Elnis is disabled by default; `/healthz` on that port is only meaningful when Elnis is enabled and does not prove ElBot / model / OneBot health.

### Security

- Compose `env_file` injects variables into the ElBot process environment; Shell and Hook subprocesses may inherit them. Do not rely only on "the container is not root" to protect API keys.
- Review `security.user_max_tool_risk`, `security.superadmins`, Shell availability, external Skills, and Hook permissions before exposing tools to group-chat users.
- Keep image generation superadmin-only by default, or set quotas / rate limits at the upstream relay.
- Keep CLI/Elnis host bindings on `127.0.0.1`, put HTTPS/WSS in front, and use strong random tokens.

### Backup and Restore

`deploy/backup.sh` no longer hot-tars SQLite blindly:

- With `sqlite3`: SQLite `.backup` snapshots plus archive, no service stop.
- Without `sqlite3` but with Docker Compose: briefly stop the container, archive, then start it again.
- Otherwise: hot tar fallback with an explicit warning.
- `deploy/README.md` includes a restore drill: extract to a temporary directory, run SQLite integrity checks, stop the service, replace `data`, restore ownership, and verify by sending a real message.

### Tiered Watchdog / Self-Healing

`deploy/elbot-watchdog.sh`, `elbot-watchdog.service`, and `elbot-watchdog.timer` provide an external watchdog:

- Only uses `/live`; CPU load or a temporarily degraded model API do not trigger restarts.
- After `WATCHDOG_FAILURE_THRESHOLD` consecutive failures, captures diagnostics first: `/live`, `/ready`, `/healthz`, `/tasks`, `/metrics`, `docker ps/inspect/logs/stats`, disk and `data` size.
- Restarts with `WATCHDOG_COOLDOWN_SECONDS` and at most `WATCHDOG_MAX_RESTARTS` inside `WATCHDOG_WINDOW_SECONDS`.
- After the limit, writes `watchdog-state/paused` and stops automatic restarts until an operator removes it.
- `WATCHDOG_WEBHOOK_URL` can send `restarted`, `paused`, and `restart_failed` events.
- `WATCHDOG_DRY_RUN=1` performs diagnostics only.
- Do not let the BaoTa Docker manager and `elbot-compose.service` manage the same Compose stack at the same time.

### P1: Rate Limits, Bounded Queue, Timeouts, and Metrics

```toml
[ops]
# Per-user / per-group message rate limits. 0 disables them.
user_messages_per_minute = 0
user_burst = 0
group_messages_per_minute = 0
group_burst = 0
rate_limit_idle_ttl_seconds = 600

# Bounded queue when concurrency is saturated.
queue_max_size = 0
queue_wait_timeout_seconds = 0
queue_wait_kinds = ["tool", "hook", "compress"]

# Operational timeouts.
tool_timeout_seconds = 600
hook_timeout_seconds = 60
compress_timeout_seconds = 300

# Concurrency caps.
max_concurrent_turns = 4
max_concurrent_tools = 4
max_concurrent_hooks = 4
```

- Rate limits use a token bucket keyed by platform + scope; superadmins are exempt.
- `turn` is not queued by default; tool / hook / compress may wait briefly; full or timed-out queues are explicitly rejected.
- `/tasks` and `/metrics` expose the active task registry and resource state.

### P1: Model Circuit Breaker and Fallback Provider

```toml
[ops]
circuit_breaker_failure_threshold = 0
circuit_breaker_open_cooldown_seconds = 60
circuit_breaker_half_open_max = 1

[providers.openai]
fallback_provider = "deepseek"
fallback_model = "deepseek-chat"
```

- Tracks provider connection failures, first-chunk timeouts, upstream 5xx responses, and stream errors.
- `context.Canceled` and whole-turn response timeouts do not count.
- When open, ElBot uses the fallback provider if configured; otherwise returns a clear error instead of retrying forever.
- Model-side failures appear as `degraded` and never trigger an automatic restart.

### P1: Image Generation Concurrency and Degradation

```toml
[image_generation]
max_concurrent = 0
queue_size = 0
queue_timeout_seconds = 0
```

- Limits image generation independently from normal chat.
- Queue full or wait timeout returns a clear "image generation is busy / timed out" message.
- `/metrics` exposes `image_limit.active` and `image_limit.waiting`.

### P2: Disk Protection and Image Worker Isolation

```toml
[storage]
disk_warn_ratio = 0.85
disk_critical_ratio = 0.95
disk_min_free_bytes = 0
```

- `disk_warn_ratio` / `disk_critical_ratio` are used-space ratios; `disk_min_free_bytes` forces critical when free space is too low.
- In `critical`, ElBot refuses non-essential media writes (images, voice, video, ordinary files) while preserving SQLite sessions and configuration writes.
- Image decode / resize / JPEG compression now runs in a hidden worker subprocess of the same binary, with timeout and process-tree termination on cancellation.
- Shell, Hooks, AgentSkill, and Go Skill already terminate their process trees on timeout / cancellation.

### More Details

- Deployment and watchdog guide: [`deploy/README.md`](deploy/README.md)
- Operational config: [`docs.en/configuration.md`](docs.en/configuration.md)
- Chinese docs: [`README.zh-CN.md`](README.zh-CN.md)

## Development Status

ElBot is still under rapid development; interfaces, configurations, and internal implementations may continue to be adjusted. It is currently more suitable for exploration as a personal Agent/bot framework.
