<!-- This file is auto-translated from README.zh-CN.md. Do not edit manually. -->

# ElBot

[中文](README.zh-CN.md) | English

ElBot is a lightweight Agent/Chatbot framework written in Go, aiming to minimize running cost, context cost, and maintenance complexity while preserving extensibility.
It supports ordinary chat, tool calling, Hook extensions, long-running task scheduling, persistent sessions, and context compaction, and is suitable for scenarios such as personal assistants, platform bots, and orchestratable automation assistants.

## Features

### I. Lightweight and Efficient

**Extremely lightweight Go implementation**:

| Metric            | Value                            |
| ----------------- | -------------------------------- |
| Local startup time | <10ms (tested on N5105, SATA SSD) |
| Resident memory   | ~30MB                            |
| Binary size       | <30MB                            |

> The table above is reference data from the original v0.5.0 (N5105 + SATA SSD), used to illustrate the design goals, not a measured commitment of this repository: 0.6.x added standalone health endpoints, diagnostics, asset manifests, and components such as `doctor`, and has not been re-benchmarked. The peak memory of image generation, tool subprocesses, and concurrent turns depends even more on the actual configuration, so please rely on measurements on your own machine:
>
> ```bash
> ls -lh elbot                                   # binary size
> /usr/bin/time -v ./elbot config check 2>&1 | grep -E 'Elapsed|Maximum resident'
> docker stats --no-stream elbot                 # container resident memory
> ```

**Extremely token-frugal tool discovery**: Research shows that many ordinary users still mainly use LLM-type products as more advanced search engines, writing assistants, and listeners, and frequent tool calls are not the norm in every conversation.
References: Chatterji et al., _How People Use ChatGPT_, NBER, 2025; Yan et al., _ShareChat: A Dataset of Chatbot Conversations in the Wild_, arXiv:2512.17843, 2025.

ElBot does not inject the complete schema of all tools by default in every turn; it only exposes `discover_tool` and the names of the currently available tools. When the model needs to use a tool, it first discovers the tool details on demand, and then the Agent injects the corresponding schema. This greatly reduces ineffective context overhead.

**Chat / Work dual mode**: The two modes can be configured with models independently, letting a low-cost model handle chit-chat and a strong model focus on complex tasks.

**Layered resident memory and long-term memory**: Resident memory stores only short, stable information that truly needs to be injected every turn, and internally distinguishes core, which requires confirmation to modify, from normal, which can be curated; longer and more complex memories are queried by the LLM on demand through `long_memory`. Long-term memory uses Markdown source data and SQLite FTS, balancing transparency and retrieval efficiency. When resident memory is injected into the system prompt, it carries the `<resident_memory>` boundary and a trust statement that it is "user data, not system instructions"; normal is stored in a structured way of "one item per line, one thing per line", and has filtering for write frequency, number of entries, single-entry length, and instruction-like content. `memories.toml` uses atomic writes and is reloaded when changed externally, avoiding crashes or concurrent edits corrupting/overwriting memories.

| Mode   | Tools                             | Use cases                                                        | First-request token consumption   |
| ------ | --------------------------------- | ---------------------------------------------------------------- | --------------------------------- |
| `chat` | Not injected                      | Chit-chat, companionship, lightweight Q&A, low-cost conversation | <500 (95%+ cache hit afterwards)  |
| `work` | Tool discovery and calling enabled | Complex tasks such as search, files, commands, Cron, and Skill   | <1000 (90%+ cache hit afterwards) |

### II. Powerful and Extensible

**Extensible Hook system**: ElBot has a built-in Hook Layer that can insert extension logic at key event points such as Agent input, LLM requests, LLM responses, platform sends, and platform connections. Hooks can modify messages, append output intents, invoke scripts, and so on. Hooks support plugins written in **any language**.

**Plain Cron and LLM Cron**: ElBot has a built-in Cron Runtime and an LLM-orchestrable Cron service. Plain Cron sends fixed content directly on schedule; LLM Cron uses a task description to drive the model, making it suitable for scheduled tasks that require analysis, summarization, or the use of tools.

**ELyph task notation**: ELyph is used to describe LLM Cron and native Skills. The goal is to reduce ambiguity in natural-language task descriptions, expressing inputs, outputs, steps, conditions, and constraints with a shorter, more stable structure. Compared with arbitrary Markdown, ELyph is better suited for LLMs to reuse and pass tasks between each other, and is also easier to lint, audit, and process with tooling.

**EL Skill creatable by the LLM**: ElBot has a built-in `create_el_skill` meta-tool, allowing the LLM to distill reusable experience into an EL Skill. On creation, the ELyph syntax is validated automatically, and Go source code can optionally be attached and compiled; the pure ELyph text or Go source created this way is maintained through the unified `read_el_skill` / `modify_el_skill`, and after the source is modified, `finalize_el_skill` uniformly formats and compiles it and returns the check results.

**Compatible with external AgentSkill**: ElBot is compatible with external AgentSkills that follow the agentskills.io style. Any AgentSKill script can be used as a tool through configuration.

### III. Elnis Event Perception System

Traditional Agents usually only wait for user input; Cron can only respond to time. Elnis gives ElBot one more way to be triggered: external events.

Elnis is ElBot's listening hub, Elwisp is the external listeners distributed in various places, and Elvena is the unified JSON over HTTP event protocol. Working together, the three let any signal from the outside world, such as server alerts, RSS updates, Webhooks, game events, or even information from external computers, be sent into Elnis and then handed to ElBot for processing and reply.

See [Elnis Listening Hub](docs.en/elnis.md) for details.

### IV. Flexible Deployment and Enhanced Sessions

**Multi-platform and rich-output abstraction**: ElBot abstracts the platform layer and the output layer, currently supports CLI, QQ OneBot, QQ Official, and Telegram, and leaves room for extending to other platforms.

**CLI client/server separation**: Any computer can use ElBot as a client to connect to an ElBot server. **Freely customizable frontend**: you can create whatever frontend interface you like. The screenshots below show different frontend forms; except for the TUI, they are all conceptual HTML mockups and do not represent the final UI.

<p align="center">
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/frontend_1.png" width="260" />
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/frontend_2.jpg" width="260" />
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/tui.png" width="260" />
</p>

See [elbot-showcase/frontend](https://github.com/Elfreese/elbot-showcase/tree/main/frontend) for more screenshots.

**Sessions, Fork, and context compaction**: A built-in persistent Session service supports session restore, archiving, pinning, Fork, deletion, paginated viewing, and platform isolation. Long conversations automatically trigger context compaction to keep the window controllable, and normal conversation can continue after compaction; before sending, a prompt token budget check is also performed, and over-long messages are rejected by default with an alert, while group admins can use `/*overflow` to switch the `truncate` / `summarize` strategy.

### V. Secure and Reliable

**Security policies and risk confirmation**: The tool system has built-in risk levels, role permission checks, and a high-risk confirmation flow. Ordinary users can only discover and call low-risk tools; the superadmin also needs to confirm each item when calling high-risk tools.

**Lightweight sandbox isolation**: Background Shell execution is constrained by an AST-level sandbox. Background tasks have an independent sandbox working directory, reducing the impact of misoperations.

**Comprehensive logging and auditing**: It distinguishes runtime logs, Elwisp logs, and audit logs, and supports structured fields, log queries, audit queries, and runtime debugging.

## Usage

Common startup methods:

```bash
elbot              # auto mode: try the default remote CLI client first; fall back to full foreground startup when local is unreachable
elbot run          # full foreground: local CLI + enabled platforms + Cron
elbot cli [-c name]# remote CLI client: connect to a resident ElBot server
elbot -c name      # connect to the server directly using the specified CLI client profile
elbot service run  # Linux/headless service mode: do not start a local CLI; can enable remote CLI server, platforms, and Cron
```

Shell completion can be generated with `elbot completion <shell>`, supporting `bash`, `zsh`, `fish`, `nushell`, `powershell`, and `auto`.

Minimal usage flow:

1. Configure an OpenAI-compatible Provider in `config/services.toml` (older deployments can still use `config/providers.toml`).
2. Set the API Key corresponding to `api_key_env` through a system environment variable or the `.env` file in the configuration directory.
3. After startup, use the command `/*models` to view and then `/*model xx` to select a model. Or manually select the default `chat` / `work` mode and model in `config/state.toml`.
4. Type `/*help` to view commands, or simply start a conversation.

See the detailed documentation:

- [Getting Started](docs.en/getting-started.md)
- [Configuration](docs.en/configuration.md)
- [Command Reference](docs.en/commands.md)
- [Core Concepts](docs.en/concepts.md)
- [Elnis Listening Hub](docs.en/elnis.md)
- [Elnis Configuration and Usage](docs.en/elnis-usage.md)
- [Frontend API](docs.en/frontend-api.md)

Development plans and task breakdown: [devdocs](devdocs/).

See the next section for the features added relative to the original v0.5.0 and the shortest way to use them.

## Local Fork: Deployment & Operations Enhancements

Current fork version: `0.6.8`.

### v0.6.8 Highlights

- Closed loop for group-level permissions and policy enforcement: tool allowlist default-deny, authorization only after model alias resolution, finer-grained learning review actions, re-check after revocation/queueing.
- `history=off` / `learning=off` cover the full lifecycle: new writes stop and existing knowledge stops being injected, but the current turn is still processed normally and old records are not deleted automatically.
- Four-dimensional quota ledger: group / per-user in group / global / global per-user, with image generation, vision, chat tokens and cost metered separately; tool execution idempotency and provider retries are accounted separately and survive restarts.
- Recall and cancellation gain a late-output gate: streaming output, tool results and the final send all check the turn termination state.
- Unified authorization for the actually executing model: chat, context compaction, vision fallback and group analysis summaries are all re-validated against the group model catalog before calling the provider.
- The request queue gains per-user/per-group limits and queue-full/timeout/cancellation/wait-time metrics; provider-level concurrency limits avoid passing transient pressure through to upstream.
- OneBot merged forwards are size-limited at the websocket read stage, and forwarded content is always treated as untrusted user data.
- Deterministic in-group capabilities consume no tokens: local knowledge base / FAQ, reminders, polls and signups are answered or scheduled directly by the server, creating no Session and not calling the LLM.
- Group session thread mode and consecutive-message merge window: members of the same group can share one Session and run serially in arrival order, and consecutive messages from the same member are merged into one turn.
- Group runtime state machine: after the bot itself is muted / kicked, in-flight requests for that group are cancelled immediately, new model calls are rejected, and reminders and background notifications destined for that group are dropped.
- Inbound and outbound are more controllable: OneBot platform-level message deduplication + bounded preprocessing pipeline; when long-reply chunking fails, a partial receipt is returned instead of resending the whole batch.
- Optional chat hard budget `[budget_limits].chat_hard_limit`: atomic reserve before the call and settle after usage is returned, so concurrent requests cannot all penetrate the remaining quota at the same time.
- Optional transcription `[asr]`: voice messages are transcribed to text before entering the chat, tool and context flows; when disabled, the original reference is kept.
- Long-term memory is locatable, deletable and migratable: the source is recorded on write, `/memory` / `/forget` deletion entries and an optional `angel_forget` tool are provided, and `/memory backfill` handles old data.
- `state.toml` supports hot reload of external edits: `/*state` shows the load state, `/*state reload` takes effect immediately, and external modifications are merged before each internal write-back.

This fork keeps the official ElBot Agent/Chatbot core and adds a set of new capabilities around "stable, observable, deployable, extensible": character asset library, image generation, group analysis, long-term memory, self-learning, deterministic in-group services (local knowledge base / reminders / polls / signups), group session threads and group runtime state, transcription, system information and scheduled reports, per-turn model/image generation/tool declarations, command prefixes and config checks, Docker / offline deployment, a standalone health endpoint, watchdog, backup and restore, upgrade and rollback, acceptance tooling and a fault diagnostics panel. The goal is explicit: avoid the situation where "the container shows healthy but the bot is already stuck", and never do crude self-healing like "kill the process when CPU is high".

| Capability | Entry point |
| --- | --- |
| Character asset library | `@char:<id>`, `/*chars`, `character_*` tools |
| Image generation | `image_generate`, `@image:<profile>` |
| Image to drawing prompt | `image_to_prompt` built-in tool (reuses the vision provider, shares the description engine with the vision fallback) |
| Vision fallback (text-only model reads images) | `[vision]` config section (disabled by default, reuses the vision provider, with fingerprint cache) |
| Group analysis clean-room statistics and summaries | `group_analysis`, `[group_analysis]`, optional Cron daily report |
| Long-term memory clean-room | `angel_remember` / `angel_recall`, `/memory` |
| Self-learning clean-room | `/learning`, `self_learning_review`, review-before-apply |
| Group knowledge base / FAQ | `[group_knowledge]`, `/*faq add\|add-contains\|add-keyword\|remove\|clear\|test\|on\|off` |
| In-group reminders / polls / signups | `[group_services]`, `/*remind`, `/*poll`, `/*vote`, `/*signup`, `/*join` |
| Group session threads and message merging | `/*grouppolicy thread-mode group`, `/*grouppolicy merge-window <milliseconds>` |
| Group runtime state machine | `state.toml [group_runtime]`, `/*grouppolicy` |
| Regular member self-service panel | `/*me [tasks\|quota\|all]` |
| Transcription (optional) | `[asr]`, `/*grouppolicy asr <on\|off>`, `/*grouppolicy asr-quota <count>` |
| Chat hard budget (optional) | `[budget_limits].chat_hard_limit` |
| Long-term memory deletion and migration | `/forget list\|<id>\|source <message id>`, `/memory delete\|source\|backfill`, `[angel_memory].allow_tool_forget` |
| `state.toml` hot reload | `/*state`, `/*state reload` |
| Platform capability extensions | OneBot group history/group directory/avatar; Telegram group info/admin; QQ Official local fallback |
| System information and scheduled reports | `/metrics.resources`, `[maintenance.daily_report]` |
| Per-turn model / image generation / tool declarations | `@model:<profile>`, `@image:<profile>`, `@use:<profile>` |
| Command prefixes and config checks | `[commands].prefixes`, `elbot config check` |
| Docker / systemd / Windows local container / offline deployment | `deploy/`, `deploy/windows/`, `deploy/pack/`, `deploy/portainer/` |
| Standalone health endpoint and watchdog | `/live`, `/ready`, `/healthz`, `elbot-watchdog.sh` |
| Rate limiting, circuit breaking, disk protection | `[ops]`, `[storage].disk_*` |
| Backup, restore, upgrade, rollback | `backup.sh`, `restore-verify.sh`, `upgrade.sh`, `rollback.sh` |
| Acceptance and fault diagnostics | `elbot doctor`, `/tasks`, `/metrics`, `/diagnostics` |

> Group analysis, long-term memory and self-learning are all clean-room implementations: they only use ElBot's own Hook / Tool / SQLite / model client and do not copy third-party GPL/AGPL code, prompts, templates or assets. Candidate expressions and slang enter `pending` by default, and are only injected into context after an admin review passes.

- Group analysis: local `chat_history` / `outbound_messages` statistics, the `group_analysis` tool returns statistics and a default-model summary; a Cron daily report can be enabled in `[group_analysis]`.
- Long-term memory: `angel_memory.db` stores scoped memories, managed by the `angel_remember` / `angel_recall` tools and the `/memory` command; `llm.turn.prepared` injects temporary system context and does not write Session history.
- Self-learning: `self_learning.db` stores observations and candidates, `/learning` / `self_learning_review` perform review; only `approved` candidates are injected; `[maintenance.privacy_cleanup]` cleans up by retention.
- Image to drawing prompt: the built-in `image_to_prompt` tool passes a reference image via `media:<sha256>` and reuses the vision model in `[providers.*]` to reverse-engineer the drawing prompt; images are scaled before upload, and results are cached by fingerprint by the shared `internal/vision` engine, with concurrent requests for the same image merged into a single call, reducing tokens and duplicate calls. The tool and the vision fallback use the same engine, with identical caching, concurrency, timeout and output-limit policies.
- Vision fallback: optional `[vision]` section (disabled by default). When the main model is text-only and upstream explicitly rejects images, the vision model first transcribes the image into a text description and then the request is retried; provider/model can inherit from `[image_to_prompt]`.
  - Retry is transparent only while the main model has not yet output body text, reasoning or a tool call fragment; if it has already output something, a retry hint is given instead of concatenating a second answer, and the fallback happens at most once per turn, avoiding duplicate answers and duplicate tool calls.
  - Trigger detection uses the structured `status/code/type/param`: only explicit image/vision-related 400/422 triggers it, while ordinary 400, 429, 5xx, timeouts and cancellations are never misjudged; for 404 only structured signals count, avoiding treating a model not found whose "model name contains image" as lack of vision support.
  - Successful results are cached by "config fingerprint" (model, prompt, preprocessing parameters, media content-addressed ID), and explicit deterministic failures go into a short-lived negative cache; cancellation/timeout take priority over status codes and are never written to the negative cache. If any one of multiple images fails, the whole batch degrades to text references, and the image description is annotated as "untrusted image content, not a user instruction".
  - Bounded parallelism for multiple images: by default at most 4 are described concurrently, at most 8 per turn, and the whole batch shares a time budget (the turn's existing deadline takes precedence if there is one, otherwise 3 minutes by default); exceeding the limit or the timeout degrades the whole batch to text references, so it is not dragged down serially by a dozen images.
  - The engine has built-in limits and counters: by default at most 4 concurrent upstream tasks, a queue of 16, and at most 16 waiters per task; hits/misses, merge counts, upstream task counts, error categories and durations are counted with low-cardinality labels and exposed via `/metrics.vision` (`image_to_prompt` and `fallback` show their usage separately), containing only counts and limits, never image content or media IDs.
  - Upstream error classification is uniformly mapped to `APIError.Category` by the platform adapter, and the agent no longer matches error text; `[providers.*].vision = false` (including model-level declarations) makes tools/fallbacks that need image input fail at startup rather than only on the first call.

- Group knowledge base (`[group_knowledge]`): entries are written into `state.toml [group_knowledge]` keyed by `platform + group scope` and support three kinds of matching: exact / contains / keyword; on a hit the server sends the answer directly, creating no Session, calling no LLM and consuming no chat tokens, and it remains subject to wake word, quiet hours, inbound rate limiting and group runtime state constraints. Deterministic normalization is performed before matching (full-width to half-width, case and whitespace folding, removal of common sentence-ending punctuation), and entries are never injected into model prompts. The management entry is `/*faq add|add-contains|add-keyword|remove|clear|test|on|off`, available only to the current group owner/group admin or superadmin, and cannot operate across groups.
- In-group reminders / polls / signups (`[group_services]`): reminders support time formats such as `10m`, `1h30m`, `2d`, `15:04`, `YYYY-MM-DD HH:MM` and are sent by the local scheduler through the normal Output Layer when due; polls are single-choice, allow vote changes, and only show counts as results; signups support capacity and join/leave/close. State is written to `state.toml [group_services]`; regular members can create and participate, while the creator, the current group owner/group admin or a superadmin can close/delete them; when the group is unavailable, retries are deferred or the item is marked as skipped.
- Group session thread mode and consecutive-message merge: `/*grouppolicy thread-mode group` lets members of the same group share one Session, ordinary messages execute serially in arrival order, each user message carries a server-generated speaking-member marker, so input from multiple members does not overwrite each other; `/*grouppolicy merge-window <0-10000>` merges consecutive messages from the same member within the window into the same turn, while different members remain serialized and do not share the permission subject. Enabling thread mode does not migrate the old per-person independent Sessions, and recall or a member leaving the group also discards queue items that have not yet run.
- Group runtime state machine: after the bot itself is muted / kicked, the current group enters `muted` / `removed`, the server immediately cancels in-flight requests for that group, rejects new model calls, and drops scheduled tasks, reminders and background notifications destined for that group, avoiding "the model keeps billing but the result cannot be sent"; after the mute is lifted or the bot rejoins, it returns to `active`. State changes send only one aggregated notification to the superadmin, and repeated events are not notified repeatedly.
- Inbound deduplication and bounded preprocessing: OneBot inbound records processing state by `platform + self_id + scope + message_id`, so reconnect replays within the TTL do not wake the model again or bill twice, and the deduplication state is independent of the `history` switch; `@` parsing, quote fetching and merged-forward expansion run in a bounded worker pool, and when the queue is full ordinary messages are rejected locally instead of spawning unbounded goroutines, while recall and member events go through a separate high-priority channel.
- Transcription (`[asr]`, disabled by default): a woken voice message is first transcribed into text segments using the OpenAI-compatible `/audio/transcriptions` endpoint of the existing `[providers.*]`, and then enters the chat, tool and context flows; when it is disabled or transcription fails, the original `[voice]` reference is kept. provider/model can declare the capability with `audio = true/false`, and it is jointly constrained by the global `[asr].enabled`, the group policy `/*grouppolicy asr <on|off>` and daily quotas (`asr-quota`, `user-asr-quota`, `global_asr_daily`, `user_asr_daily`); on success results are cached by media ID, and deterministic 4xx goes into a short negative cache.
- Chat hard budget (`[budget_limits].chat_hard_limit`, disabled by default): adds a path of "atomic reserve before the call, settle and release the difference after usage is returned" on top of the existing token/cost ledger, so concurrent requests cannot all penetrate the remaining quota at the same time; when upstream is missing usage, accounting is done conservatively by the reserved amount and `budget.uncertain` is written; when a cost hard limit is enabled but the model has no price, the request is explicitly rejected; unsettled reservations after cancellation/timeout/restart stay held rather than being refunded automatically.
- Long-term memory source and deletion: the source type, source member, platform message ID and Session ID are recorded on write. `/memory list|show|delete|source|backfill` is for superadmins and `/forget list|<id>|source <message id>` is for regular members; in group chats a regular member can only delete memories whose source member is themselves, while the group owner/group admin/superadmin can delete any memory in the current group scope; `/forget resident normal|core|all [--confirm]` can clear one's own resident memory. By default, recalling the source message deletes the derived memories (can be disabled with `[angel_memory].forget_on_recall`), and `/*delete` cleans up by source Session ID when permanently deleting a Session; memories from older versions without a structured source column are neither matched nor mistakenly deleted, and `/memory backfill [--confirm]` can deterministically backfill the source type.
- Optional `angel_forget` tool (`[angel_memory].allow_tool_forget`, disabled by default): lets the model proactively delete long-term memory, but only a single memory whose source member is the current speaker and which belongs to the current platform/session scope; memories of others, other scopes and older memories without a source member are all rejected; risk level `high` enters the tool confirmation flow (`/*detail`, `/*confirm`, `/*reject`), and the tool itself also requires a second call with `confirm=true`, where the first call only returns the content to be deleted, at most 3 per minute within the same scope. This tool is a hidden tool that reaches the model through dependency injection from `angel_recall`, and bulk deletion still goes through `/memory delete` and `/forget`.
- `state.toml` external edit hot reload: the process detects external modifications by mtime every 15 seconds and merges them into effect; `/*state` shows the path, load time and modifications not yet in effect, and `/*state reload` takes effect immediately; after taking effect a `runtime_state_reloaded` audit event is recorded, and one notification is sent to the superadmin when there actually is a change. External modifications are merged before each internal write-back, so manual edits no longer require a restart and are not overwritten by internal write-back. Hot reload covers `mode_models`, `compact_model`, `naming_model`, `context_overflow`, `group_policy`, `group_knowledge`, `group_services`, `group_runtime`; the `[budget]` quota ledger is still exclusively owned by the running process and is only restored from the file at startup, to avoid losing in-flight reservations.
- Long-reply sending is more reliable: chunked sending on major platforms merges the receipts of already-successful pages/targets and marks partial failures; on partial failure the platform message count is left in the audit log, making reconciliation easier instead of resending the whole batch.

### Independent Health Endpoints

ElBot provides standalone ops HTTP endpoints that do not depend on Elnis:

| Endpoint | Meaning |
| --- | --- |
| `/live` | Only indicates that the process is still running; it does not judge the scheduling heartbeat, model, or platform. |
| `/ready` | The process is initialized, SQLite / data directory is writable, and the scheduling heartbeat has started and has not expired; platform/model failures will not make it fail. |
| `/healthz` | Aggregated status. Requires auth after `ELBOT_OPS_TOKEN` is set; when it is not set and unauthenticated access is not explicitly allowed, this endpoint is not registered (only `/live` and `/ready` remain). Platform/model failures show as `degraded`; when the scheduling heartbeat is expired, `/ready` and `/healthz` return `not_ready`. It should not be used alone to auto-restart. |
| `/tasks` | Currently active turn / tool / hook / context compaction tasks, stage, start time, latest progress, and the `queued_by_kind` queue backlog. |
| `/metrics` | Task counts, oldest task duration, goroutine / heap / RSS / disk, platform/model/circuit breaker status, rate limit thresholds and rejection reasons, image generation queue status, image description engine counts and usage (`vision`). |
| `/diagnostics` | Aggregated diagnostics aimed at "the bot is not replying": queueing/timeouts, rate limit hits, circuit breaker status, and the most recent restart reason. |

```dotenv
ELBOT_HEALTH_ADDR=0.0.0.0:32171
ELBOT_HEALTH_LIVE_STALE_SECONDS=90
# Access token for /tasks, /metrics, /diagnostics, /plugins/* and /healthz.
# When unset, sensitive ops endpoints are not registered by default; only /live and /ready remain.
# ELBOT_OPS_TOKEN=replace-with-a-random-long-string
# Troubleshooting only: explicitly allow exposing sensitive ops endpoints without auth (must be limited to loopback/trusted networks).
# ELBOT_OPS_ALLOW_UNAUTHENTICATED=1
```

The default security policy for `/tasks`, `/metrics`, `/diagnostics`, and `/plugins/*` is: **they are not registered when `ELBOT_OPS_TOKEN` is not set**, and they are exposed without authentication only when insecure mode is explicitly enabled. Compose maps only to the host loopback: `127.0.0.1:32171:32171`. The `HEALTHCHECK` in the standard Dockerfile has been changed to `curl -fsS http://127.0.0.1:32171/live` instead of only checking the PID. `32171` should not be added to the Nginx public route.

### Data Volume, First Start, and Config

- Compose mounts `./data` to `/data` and sets `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, and `XDG_RUNTIME_DIR`.
- The container runs with UID/GID `10001`, and `deploy/data` must be writable by that UID.
- `deploy/init-host.sh` creates the directories, sets ownership, and prints a first-start checklist.
- After the first start, check `services.toml` (`api_key_env` variable names, Provider, image generation config; `providers.toml` for older deployments), `state.toml` (whether provider/model match and are actually usable), and `app.toml` (CLI / OneBot / Elnis / behavior / security config).
- Changing `.env` requires `docker compose up -d --force-recreate`; changing only TOML can use `docker compose restart`.
- Windows local Docker Desktop users use `.\deploy\windows\elbot.ps1 init` / `start` to create the same kind of `deploy/data`; Linux containers must be used, and for auto-start on login you can use `install-service` to register a scheduled task.

### Container Networking

- QQ OneBot's `ws_url` must be from the container's perspective: on the same Compose network use the service name; when OneBot is on the host, use `host.docker.internal` + `extra_hosts`, or a reachable remote IP. Do not write `127.0.0.1` inside the container.
- Keep `send_file_mode = "base64"` when the filesystem is not shared.
- A remote CLI server must listen on `0.0.0.0:32172` inside the container; on the host bind only `127.0.0.1` and expose it externally through an HTTPS/WSS reverse proxy.
- `32170` is the Elnis entry point and is disabled by default; the `/healthz` on that port is meaningful only after Elnis is enabled, and it cannot represent ElBot / model / OneBot health.

### Security

- The Compose `env_file` injects variables into the ElBot process environment; Shell / Go Skill subprocesses remove credential variables whose names contain the `KEY`, `TOKEN`, `SECRET`, `PASSWORD`, or `PRIVATE` word segments, but parent-process tools such as web search, image generation, and media download can still read them. Do not rely only on "the container is not root" to protect the API key.
- Before exposing tools to group chat users, check `security.user_max_tool_risk`, `security.superadmins`, the available scope of Shell, and the permissions of external Skills and Hooks.
- Image generation should preferably stay superadmin-only, or set the quota, rate limit, and daily cap per key at the upstream relay station.
- CLI / Elnis continue to bind only to the host's `127.0.0.1`, use HTTPS/WSS externally, and use a strong random token.

### Backup and Restore

`deploy/backup.sh` no longer hot-tars SQLite directly:

- With `sqlite3`: first run a SQLite `.backup` consistent snapshot for each database, then copy non-database files such as media, and fill in missing files according to the local media references in the database; when a source file has already been cleaned up it fails explicitly, rather than producing an archive with incomplete references.
- Without `sqlite3` but with Docker Compose: briefly stop the container, package, then start automatically and wait for the healthcheck to be `healthy`; a failed restore or a timeout waiting for readiness makes the backup exit non-zero (`BACKUP_RESTART_READY_TIMEOUT` defaults to 60 seconds).
- With neither: fall back to hot packaging with an explicit warning.
- After a successful backup, a file-level sha256 manifest `*.manifest` is generated by default, and `deploy/restore-verify.sh` is invoked to verify SQLite, config, role assets, local media, and the manifest in an isolated directory; `BACKUP_VERIFY=0` can skip it.
- `restore-verify.sh` strict mode requires the manifest, `sqlite3`, `python3`/`python` with `tomllib`, the required config, and exact `/data/...` media paths all to pass; the final state distinguishes `restore_verify: passed` (both static and isolated startup pass), `static_passed` (static passed but no startup acceptance performed), and `static_passed_with_skips`. `RESTORE_VERIFY_START=auto` (default) additionally starts a one-off restore instance with `--network none` and waits for `/ready` when Docker and the image are available; old service PID markers are removed before restoring.
- For single-machine upgrade/rollback you can use `deploy/upgrade.sh` / `deploy/rollback.sh`: before an upgrade, a data snapshot is taken by default with `BACKUP_MODE=stop`, the old image is saved by the running container's actual image ID, and the new image config pre-check uses an isolated data copy; before a rollback the snapshot is first verified with the old image, and after restoring it waits for `/ready` and runs doctor acceptance.
- `deploy/README.md` contains restore drill steps: extract to a temporary directory, SQLite integrity check, stop the service, replace `data`, restore ownership, and actually send a message to verify.

### Tiered Watchdog / Self-Healing

`deploy/elbot-watchdog.sh`, `elbot-watchdog.service`, and `elbot-watchdog.timer` provide an external watchdog:

- It only checks `/live` to judge whether the process can still respond, and will not restart because of high CPU, a temporary model API failure, or a platform reconnect.
- After consecutive failures reach `WATCHDOG_FAILURE_THRESHOLD`, it collects diagnostic information first and then acts: `/live`, `/ready`, `/healthz`, `/tasks`, `/metrics`, container State/Ports/logs/stats, disk and `data` size; the diagnostic directory uses `umask 077` from creation, and after redaction it performs a three-layer re-check of idempotency, standalone mode, and known environment-sensitive values, and a failure of the checker itself is also reported as an error.
- It cools down according to `WATCHDOG_COOLDOWN_SECONDS` and restarts at most `WATCHDOG_MAX_RESTARTS` times within `WATCHDOG_WINDOW_SECONDS`.
- After exceeding the limit it writes `watchdog-state/paused`, stops automatic restarts, and waits for manual recovery.
- `WATCHDOG_WEBHOOK_URL` can push `restarted`, `paused`, and `restart_failed` events; if `ELBOT_OPS_TOKEN` is set, fill the same value into `WATCHDOG_OPS_TOKEN` to access the diagnostic endpoints.
- With `WATCHDOG_DRY_RUN=1` it only diagnoses and does not restart.
- The watchdog writes the restart / pause reason to `data/run/elbot/last_restart_reason`; after ElBot starts it reads that file and shows the most recent restart reason in `/healthz`, `/metrics`, and `/diagnostics`.
- Do not let the Baota Docker manager and `elbot-compose.service` manage the same Compose setup at the same time.

### P1: Rate Limits, Bounded Queue, Timeouts, and Metrics

```toml
[ops]
# Per-user / per-group rate limits; 0 disables them.
user_messages_per_minute = 0
user_burst = 0
group_messages_per_minute = 0
group_burst = 0
rate_limit_idle_ttl_seconds = 600

# Bounded queue used when concurrency is saturated.
queue_max_size = 0
queue_wait_timeout_seconds = 0
queue_wait_kinds = ["tool", "hook", "compress"]

# Runtime timeouts.
tool_timeout_seconds = 600
hook_timeout_seconds = 60
compress_timeout_seconds = 300

# Concurrency limits.
max_concurrent_turns = 4
max_concurrent_tools = 4
max_concurrent_hooks = 4
```

- Group chat rate limiting checks the user-level quota first, then the group-level total quota; the user level prevents a single member from flooding, and the group level protects the resources of the whole group. Superadmins are not affected by rate limits.
- Rate limiting is only self-protection, not fair scheduling: user-level quota is counted by `platform + user`, so the same user shares one quota across multiple groups; when the group level rejects a request, the user quota already consumed by that request is not refunded. Therefore whether "a single member can exhaust the whole group's quota" depends on how the two sets of thresholds are configured, and it is recommended to adjust `[ops]`'s `rate_limit_*` according to group size.
- `turn` is not queued by default; tool / hook / compress can be briefly queued; a full queue or a timeout is rejected explicitly.
- `/tasks` and `/metrics` expose active tasks and resource status; when `ELBOT_OPS_TOKEN` is not set, sensitive ops endpoints are not registered by default and only `/live` and `/ready` remain.

### P1: Model Circuit Breaker and Fallback Provider

```toml
[ops]
circuit_breaker_failure_threshold = 0
circuit_breaker_open_cooldown_seconds = 60
circuit_breaker_half_open_max = 1

[providers.openai]
fallback_provider = "deepseek"
fallback_model = "deepseek-chat"
fallback_mode = "circuit"      # circuit (default) / on_error / off
# fallback_timeout_seconds = 0 # total timeout for a single Provider attempt
```

- The circuit breaker counts connection failures, first-packet timeouts, upstream 5xx, and stream errors.
- `context.Canceled` and a whole-turn response timeout are not counted.
- By default `fallback_mode = "circuit"`: when the circuit breaker is open, it switches to the fallback if there is one; otherwise it returns an explicit error and no longer retries indefinitely.
- With `fallback_mode = "on_error"` (or `fallback_on_error = true`), the first failed pre-stream request can already switch; `fallback_timeout_seconds` can limit the total duration of a single Provider attempt.
- Deployment acceptance can run `elbot doctor`: by default it checks config/storage/ports/platform/model; a platform `disconnected` no longer counts as passing, and `--require-platform` further requires the platform to be enabled and to have a health status; with `--e2e` it sends a unique probe marker through the CLI remote protocol and matches the reply, and the report distinguishes `config_ok`, `platform_ok`, and `e2e_ok`.
- External model anomalies only show as `degraded` and do not trigger an automatic restart.

### P1: Image Generation Concurrency and Degradation

The following configuration lives in `services.toml`'s `[image_generation]` (`app.toml` for older deployments):

```toml
[image_generation]
max_concurrent = 0
queue_size = 0
queue_timeout_seconds = 0
```

- Image generation is independent of normal chat rate limiting.
- A full queue or a queue wait timeout returns "image generation busy / queue timeout", without dragging down chat.
- `/metrics` exposes `image_limit.active` and `image_limit.waiting`.

### P2: Disk Protection and Image Worker Isolation

```toml
[storage]
disk_warn_ratio = 0.85
disk_critical_ratio = 0.95
disk_min_free_bytes = 0
```

- `disk_warn_ratio` / `disk_critical_ratio` are used-space ratios; when `disk_min_free_bytes` is greater than 0, too little free space goes directly to critical.
- In critical it rejects non-essential media writes (images, voice, video, ordinary files) and keeps SQLite Session and config writes.
- Image decoding / scaling / JPEG compression now run in a hidden worker subprocess of the same binary; a timeout or cancellation terminates the entire worker process tree.
- Shell, Hook, AgentSkill, and Go Skill also already terminate their own process trees on timeout / cancellation.

### Character Library: Personas and Image Assets

The character library stores character text settings and character images, and supports visibility, search, command enablement, and tool calls.

Directory structure:

```text
config/characters/
  catgirl/
    character.toml        # metadata + image index
    profile.md            # persona; this is what @char injects
    world.md              # world view, background (optional)
    greeting.md           # greeting (optional)
    examples.md           # few-shot examples (optional)
    image_prompt.md       # image generation preset (optional)
    notes/*.md            # supplementary material (optional)
    images/avatar.png     # original character image
```

Configuration:

```toml
[character_library]
enabled = true
root = "characters"
```

Shortest usage:

```text
@char:catgirl hello there
```

- `@char:<id>` / `@c:<id>` only takes effect for the current turn; it does not switch Session or write to session metadata, and it expires as soon as the reply ends.
- If the character does not exist or you have no access to it, the reason is shown.
- By default, ordinary users can only create, modify, and read their own private characters; superadmin can manage public characters.
- Character asset metadata supports `version` and `source`, and the version increments automatically on update; the image index also carries version and source.
- During backup, a sha256 manifest is generated for characters and media files, and `restore-verify.sh` verifies it on restore.

Common commands and tools:

- `/*chars`: list currently visible characters, supports keyword filtering.
- `/*chars reload`: superadmin rebuilds the index.
- `character_list`: list visible characters.
- `character_read`: read `profile` / `world` / `greeting` / `examples` / `notes/<name>`, optionally returning images.
- `character_search`: search by name, alias, tags, and body text.
- `character_manage`: create / modify a character, write `docs`, add or delete character images.
- `character_delete`: permanently delete a character, high-risk confirmation.

See [Character Library](docs.en/character-library.md).

### Image Generation: `image_generate`

`image_generate` connects to the OpenAI-compatible `/images/generations`, and by default assembles the final prompt from "global preset + character image preset + scene description". Image generation configuration lives in `[image_generation]` in `services.toml` (older deployments may still put it in `app.toml`).

Basic configuration:

```toml
[image_generation]
enabled = true
base_url = "https://your-relay.example.com/v1"  # /images/generations is appended automatically
api_key_env = "IMAGE_API_KEY"
model = "gpt-image-2.5"
size = "1024x1024"
quality = "high"
output_format = "png"
timeout_seconds = 180
preset_prompt = ""
max_prompt_runes = 4000
superadmin_only = true
save_to_character = true
send_by_default = false
```

When used with the character library:

```text
@char:catgirl @char:foxgirl draw the two of them together on a neon street in the rain
```

You can also have the model pass parameters explicitly:

```json
{"prompt": "the two of them together on a rainy night street", "character_ids": ["catgirl", "foxgirl"]}
```

By default all indexed characters are drawn into **the same image**; multiple images are generated only when `count > 1` is passed explicitly (up to 4), and each image contains all characters, so characters are never split across different images. Multi-character auto-matching recognizes multiple character names/aliases in the prompt.

`image_generate` defaults to `mode = "auto"`:

- Automatically recognizes multiple character names/aliases from the prompt, and merges the matched character image presets and reference images into the same image;
- When the prompt contains referential words such as "just now / the previous one / that image / in the group", it automatically searches the current group chat context;
- The image generation configuration `auto_character` / `auto_context` / `context_default_limit` controls the auto-orchestration switch and the number of context entries;
- The tool parameter `mode=manual` disables auto-orchestration and uses only explicit parameters.

Built-in prompt optimization:

```toml
[image_generation]
optimize = "rules"              # off / rules
optimize_term_mode = "phrase"   # phrase or tag
optimize_max_anchors = 4
optimize_max_negatives = 10
optimize_max_added_runes = 400
```

- `rules` adds purpose, aspect ratio, style anchors, and negative terms from the built-in GPT Image Prompts rule library.
- `phrase` appends a full phrase, `tag` appends a word tag string.
- The rule library only appends; it does not rewrite the user's original sentence.
- Optional LLM semantic rewrite: `optimize_rewrite = "off" / "auto" / "always"`, using `optimize_rewrite_model = "naming"` to specify a low-cost model slot; short or vague prompts are rewritten automatically, while long prompts are kept as is.
- The rule library source files are in `scripts/data/`; you can use `python scripts/convert_image_prompts.py ...` to regenerate `internal/imagegen/prompts/library.json`, which takes effect after recompiling.
- Both failures and successes may be recorded in the audit log; failures are not counted toward the image generation success count in scheduled reports.

Image generation concurrency and degradation:

```toml
[image_generation]
max_concurrent = 0
queue_size = 0
queue_timeout_seconds = 0
```

- 0 means unlimited; when the queue is full or queueing times out, it returns "image generation busy / queue timeout" and will not drag down normal chat.
- `/metrics.image_limit` shows the current `active` and `waiting`.

See [Image Generation Service](docs.en/image-generation.md) for the full configuration and prompt optimization rules.

### System Information and Scheduled Reports

Cross-platform system information collection and scheduled reports are added, making it convenient to observe resources in single-machine deployments.

System information is used for:

- `/metrics.resources`: data directory size, filesystem used/free, RSS, Go heap, goroutines.
- `[maintenance.daily_report]`: periodically sends image generation volume, Token, cost, disk, and memory reports to the superadmin.
- Automatic maintenance tasks: log cleanup, Session cleanup, chat history cleanup, and sandbox / media cleanup are scheduled by cron; Session cleanup can also be done manually with `/clean`, and logs and audits are queried with `/log` and `/audit` respectively.

Configuration:

```toml
[maintenance.daily_report]
enabled = true
schedule = "0 9,21 * * *"    # every day at 09:00 and 21:00
window_hours = 12
provider = "deepseek"        # leave empty to count all providers
currency = "CNY"
image_price_per_image = 0.05
peak_pricing = true

[maintenance.daily_report.prices."deepseek-v4-pro"]
input_per_million = 9.0
cache_input_per_million = 0.30
output_per_million = 27.0
```

- Reports are sent according to `[security.superadmins]`; configure the corresponding platform superadmin ID before sending.
- Token and image generation volume come from the audit log; the default retention is 30 days, so do not let the statistics window exceed the retention period.
- Billing is only multiplication by the configured unit price; the actual bill is determined by the provider.

The complete description is in [Scheduled Reports](docs.en/reports.md).

### Per-Turn Model / Image / Tool Declarations

Besides `@char`, you can also declare a per-turn profile in a group chat message:

```text
@model:pro analyze this question with a strong model
@image:fast generate an avatar from this image
@use:admin check the server
```

Chinese keywords and full-width colons are also supported:

```text
#模型:pro 你好
#生图:fast 画个头像
#工具:admin 看看磁盘
```

Configuration:

```toml
[turn_directives]
prefixes = ["@", "#"]
model_keywords = ["model", "m", "模型", "用模型"]
image_keywords = ["image", "img", "生图", "出图"]
tool_keywords = ["use", "工具", "用工具"]

[model_profiles.pro]
provider = "deepseek"
model = "deepseek-v4-pro"
aliases = ["强", "pro"]

[tool_profiles.admin]
tools = ["shell", "read_file", "edit_file"]
aliases = ["管理", "运维"]

[image_generation.profiles.fast]
base_url = "https://relay-b.example.com/v1"
api_key_env = "IMAGE_API_KEY_FAST"
model = "gpt-image-2.5"
aliases = ["快", "fast"]
```

Rules:

- It takes effect only for the current turn and expires as soon as the reply ends; it is not written to the Session.
- Only the superadmin can declare.
- profile aliases can be configured in `aliases`.
- When it is not configured or you have no permission to declare, it is stripped with a notice, and the declaration is not sent to the model as is.

### Command Prefixes and Config Check

Command prefixes are configurable:

```toml
[commands]
prefixes = ["/*"]
```

- `/*help`, `/*model`, etc. are supported by default.
- Additional prefixes such as `/` and `!` can be appended; command execution and completion are recognized according to the configuration.
- After changing the prefix, replace the `/*` commands appearing in this document according to your actual configuration.

Config check:

```bash
elbot config check [--config path]
```

It loads `app.toml`, `services.toml` (for legacy deployments, `providers.toml`), and `state.toml`, prints the actual loaded paths, Provider, model, character library, image generation, profile, scheduled report summary, and warnings; it returns a non-zero exit code on configuration errors, which is suitable for use before upgrades or in CI.

### Deployment Artifacts and Offline Install

Besides the ordinary Go binary, the fork adds a deployment scheme for single machines:

- `deploy/Dockerfile`: runtime image and `go-runtime` image.
- `deploy/docker-compose.yml`: single-machine resident service, data volumes, loopback ports, log limits, resource limits.
- `deploy/elbot.service` / `elbot-compose.service`: systemd units.
- `deploy/init-host.sh`: initializes directories and permissions for BaoTa/VPS.
- `deploy/nginx-elbot.conf`: CLI WebSocket / Elnis reverse proxy snippet.
- `deploy/pack/`: offline install package, providing amd64/arm64 static binaries, prebuilt `docker load` tar, and `SHA256SUMS`.
- `deploy/windows/`: entry points for Windows local Docker Desktop deployment, status viewing, and background control.
- `deploy/portainer/`: optional Portainer CE browser Docker management UI, bound only to `127.0.0.1:9443`, shared by local / cloud servers.

Common commands:

```bash
cd deploy
cp .env.example .env
vi .env
docker compose build
docker compose up -d
```

Offline / precompiled:

```bash
bash deploy/pack/prepare-offline.sh
# or use the already downloaded artifacts
docker load -i elbot-0.6.8-linux-amd64.tar.gz
```

Multi-architecture build:

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -f deploy/Dockerfile --build-arg VERSION=0.6.8 \
  --push -t <registry>/<namespace>/elbot:0.6.8 .
```

Build arguments:

```text
GOPROXY       Go module proxy; in mainland China https://goproxy.cn,direct is recommended
APT_MIRROR    apt mirror hostname, e.g. mirrors.ustc.edu.cn
GO_BASE_IMAGE / RUNTIME_BASE_IMAGE  override the base image or pin its digest
EXTRA_TOOLS   extra commands for the runtime image
```

### Windows Local Container Deployment (Docker Desktop)

Besides cloud servers, the repository provides a Windows local Docker Desktop entry point that reuses the same `deploy/docker-compose.yml`, `deploy/Dockerfile`, and `.env`:

```powershell
.\deploy\windows\elbot.ps1 init
notepad .\deploy\.env
.\deploy\windows\elbot.ps1 start
.\deploy\windows\elbot.ps1 status
.\deploy\windows\elbot.ps1 health
.\deploy\windows\elbot.ps1 doctor --no-model
.\deploy\windows\elbot.ps1 logs -Follow -Tail 200
.\deploy\windows\elbot.ps1 install-service
```

- `start` / `stop` / `restart` / `recreate` / `logs` / `shell` manage the backend container;
- `status` / `health` read `/live`, `/ready`, `/healthz`;
- `tasks` / `metrics` / `diagnostics` read the built-in ops endpoints of the project and automatically carry `ELBOT_OPS_TOKEN`;
- `doctor` runs `elbot doctor` inside the container;
- `backup` / `restore-verify` / `upgrade` / `rollback` reuse `deploy/*.sh` and require `bash.exe` from Git for Windows;
- `install-service` registers a logon auto-start scheduled task, corresponding to `elbot-compose.service` on Linux.

See [`deploy/windows/README.md`](deploy/windows/README.md) for full details.

#### Optional: Portainer Browser Operations

ElBot itself has no general-purpose container management Web UI; if you want local Windows and cloud servers to use the same browser interface, you can start the Portainer CE bundled with the repository:

```powershell
docker compose -f deploy\portainer\portainer-compose.yml up -d
```

Open `https://127.0.0.1:9443` in a browser. Portainer binds only to the loopback address and is not exposed directly to the public network; for cloud servers, prefer an SSH tunnel (`ssh -L 9443:127.0.0.1:9443 user@host`), do not change it to `0.0.0.0`, and do not allow `9443` directly in the security group. `down` only stops the container and keeps the `portainer_data` volume; for the first-time setup token, SSH tunnel, and Docker socket security notes, see section 14 of [`deploy/windows/README.md`](deploy/windows/README.md).

### Deployment Acceptance and Troubleshooting

Deployment acceptance in one command:

```bash
elbot doctor [--config path] [--json] [--no-model] [--e2e] [--require-platform]
```

- Default checks: config, storage directories, health ports, platform status, and real model calls.
- The platform check fails when the platform is `disconnected`; `--require-platform` additionally requires that at least one platform is enabled and that the health snapshot contains a corresponding status.
- `--e2e`: sends a unique probe marker over the CLI remote WebSocket protocol, and succeeds only when the reply contains that marker; ending with an empty stream or receiving only unrelated text both count as failure.
- `config_ok`, `platform_ok`, and `e2e_ok` are reported separately; without `--e2e`, `e2e_ok=false` and `skipped` is displayed.
- `--json` is suitable for integration into CI or release pipelines.
- Example of running in Docker:

```bash
docker compose exec -T elbot elbot doctor
docker compose exec -T elbot elbot doctor --e2e --json
```

Troubleshooting endpoints:

```bash
curl -sS http://127.0.0.1:32171/diagnostics
curl -sS -H 'Authorization: Bearer <ELBOT_OPS_TOKEN>' http://127.0.0.1:32171/diagnostics
```

`/diagnostics` aggregates:

- Active tasks, queued backlog, and cumulative timeouts;
- Rate limit hits and user-level/group-level rejection reasons;
- Provider circuit breaker status;
- Platform and model status;
- The reason for the most recent watchdog restart.

Related tokens:

```dotenv
ELBOT_OPS_TOKEN=replace-with-a-random-long-string
WATCHDOG_OPS_TOKEN=the-same-random-long-string
```

### Single-Host Upgrade and Rollback

Upgrade:

```bash
cd deploy
bash upgrade.sh
```

The order of `upgrade.sh`:

1. `elbot config check` in the current container;
2. Generate a consistent data snapshot with `BACKUP_MODE=stop`, and verify restore using the current actual image ID;
3. Save the previous version's actual image ID/tar and digest to `deploy/rollback/rollback.env`;
4. Build the new image, and run a config compatibility check using an isolated copy of the production `data`;
5. After it passes, recreate the services, wait for the health check, and run `elbot doctor --no-model` for acceptance.

Rollback:

```bash
cd deploy
bash rollback.sh
# Skip the interactive confirmation:
ROLLBACK_CONFIRM=1 bash rollback.sh
```

Rollback does the following:

1. Safely parse `rollback.env` and load the previous version's image;
2. Use the previous version's image to verify the data snapshot's SQLite, config, roles, media, and manifest, and run an isolated startup `/ready` acceptance;
3. Stop the services and keep the current `data` as `data.before-rollback-*`;
4. Restore the data snapshot, clean up old PID markers, and recreate the services with the previous version's image;
5. Wait for `/ready` and run `elbot doctor --no-model`; on acceptance failure it attempts to restore the data before rollback.

### More Details

- Deployment and watchdog guide: [`deploy/README.md`](deploy/README.md)
- Windows local container guide: [`deploy/windows/README.md`](deploy/windows/README.md)
- Operational config: [`docs.en/configuration.md`](docs.en/configuration.md)
- Chinese docs: [`README.zh-CN.md`](README.zh-CN.md)

## Development Status

ElBot is still under rapid development, and its interfaces, configuration, and internal implementation may continue to change. For now it is better suited for exploration as a personal Agent/bot framework.
