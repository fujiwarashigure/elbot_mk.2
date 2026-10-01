<!-- This file is auto-translated from CHANGELOG.md. Do not edit manually. -->

# Changelog

All notable changes to ElBot will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

## [v0.6.3 - 2026-10-01]

### Added

- Local Windows container deployment: `deploy/windows/elbot.ps1` and `elbot.cmd` wrap the existing `deploy/docker-compose.yml`, `Dockerfile`, and `.env`, and support `init`, `start`, `recreate`, `stop`, `down`, `restart`, `logs`, `shell`, and `compose`. A logon scheduled task named `ElBot-Docker` can keep the service running after Windows sign-in.
- Windows command-line status and diagnostics: `status` / `health` read the built-in `/live`, `/ready`, and `/healthz` endpoints; `tasks` / `metrics` / `diagnostics` read `/tasks`, `/metrics`, and `/diagnostics` with automatic `ELBOT_OPS_TOKEN` handling; `doctor` runs the same `elbot doctor` inside the container. `backup` / `restore-verify` / `upgrade` / `rollback` reuse `deploy/*.sh` through Git for Windows `bash.exe`. Added `deploy/windows/README.md` with the complete guide.
- `image_generate` now accepts `character_ids`, `reference_images`, and `count`. By default all `@char` / `character_ids` characters are drawn into one image; only `count > 1` (maximum 4) generates multiple images, each containing all characters. Multi-character reference images are sent as a data URL array to `reference_field`.
- Added pre-send long-message protection: prompt budget is estimated from the model context window, `max_prompt_ratio`, `reserve_output_tokens`, and `single_message_max_ratio`. The default `reject` mode does not call the model and sends an alert in the group; `truncate` / `summarize` can be configured globally or per chat. The `/*overflow` command lets Bot superadmins and current group owners/admins change the chat/work policy, persisted in `state.toml` under `context_overflow`.
- Added `[context] user_original_max_runes` (default `4000`): during compaction each historical user original is capped first so that a single oversized message cannot overflow the summarization request again.
- `deploy/restore-verify.sh` gained optional isolated startup verification: `RESTORE_VERIFY_START=1` (or `required`) starts a one-off instance with `--network none` using `RESTORE_VERIFY_IMAGE` / the current `elbot` image and waits for `/ready`; `auto` (default) does this when Docker and the image are available, otherwise it skips.
- Added `deploy/tests/upgrade_script_test.sh`, `deploy/tests/backup_restart_test.sh`, and `deploy/tests/restore_verify_start_test.sh`, and extended `deploy/tests/watchdog_redaction_test.sh` and `deploy/tests/backup_restore_test.sh` to cover the regressions above. Added `deploy/tests/windows_script_test.sh` to verify the Windows entry BOM, CRLF wrapper, and version references.

### Changed

- Version raised to `0.6.3`; `deploy/VERSION`, the Compose default image, build/offline scripts, and all README / deployment examples were updated.
- Added a Windows local container deployment chapter to `README.md`, `README.zh-CN.md`, and `deploy/README.md`; `.gitattributes` now pins `*.ps1` to LF and `*.cmd` to CRLF.

### Fixed

- Fixed `deploy/upgrade.sh` `ELBOT_GIT_REF` detection: the script previously assumed `.git` lived under `deploy/` and treated the normal `repo/.git + repo/deploy/upgrade.sh` layout as a non-git worktree; it now resolves the repository root with `git -C "$DEPLOY_DIR" rev-parse --show-toplevel` and runs `fetch` / `checkout` there.
- Fixed `deploy/upgrade.sh` passing `elbot` twice during the new-image config check: the image ENTRYPOINT is already `tini -- /usr/local/bin/elbot`, so the old command became `.../elbot elbot config check`; it now passes only `config check`.
- Fixed stop-the-world backups reporting success when the container failed to restart: `deploy/backup.sh` now includes `compose up` failures and post-restart health-check timeouts/errors in the final exit code, waiting up to 60 seconds by default (`BACKUP_RESTART_READY_TIMEOUT`).
- Fixed `deploy/restore-verify.sh` strict mode still being able to print `passed` after skipping required checks: missing `python3` with `tomllib`, media `local_path` values that are not `/data/...`, and similar cases now fail immediately in strict mode. Non-strict runs print `restore_verify: passed_with_skips` and report manifest / toml / database / media_paths / start separately.
- Fixed `deploy/elbot-watchdog.sh` treating "idempotent repeated sed" as "no secrets": idempotence rechecks, independent credential pattern detection, and literal checks of current environment secrets are now separated; failures in `mktemp` / `cp` / `sed` and similar tools return non-zero, and the diagnostics directory is created with `umask 077`.
- Media cleanup no longer holds the global `m.objects` lock for the whole cleanup. It now uses per-media-ID locking and deletes at most four objects concurrently; import / `PresignGet` for the same ID use the same object lock, avoiding overlap with deletion.

## [v0.6.2 - 2026-10-01]

### Fixed

- Fixed `deploy/elbot-watchdog.sh` `redact_diagnostics()`: the sed arguments contained literal `\n` and control bytes, so sed failed with `can't read n` on every run (swallowed by `|| true`), and the `Bearer` / `api_key=` rules wrote a 0x01 control byte where they should have written backreference `\1`. Rules now run per file and also cover JSON credentials, Telegram bot tokens, and URL userinfo. A second idempotence recheck writes `REDACTION-FAILED`, alerts, and returns non-zero when redaction fails, with a `--redact-dir` manual entry point and a `deploy/tests/watchdog_redaction_test.sh` self-test.
- Fixed `deploy/backup.sh` `write_manifest()`: the same literal `\n` broke the `find | xargs sha256sum` pipeline while the error was swallowed. The manifest is now generated from the packaged archive, covers every `data/` file in the archive, and generation failure marks the backup as failed.
- `deploy/backup.sh` now passes an explicit `-f <compose file>` and runs from `deploy/` (overridable with `ELBOT_COMPOSE_FILE`), so calls from cron or other directories cannot hit another Compose project or package live data as cold data.
- `deploy/restore-verify.sh` defaults to strict mode: manifest + sha256sum, SQLite `integrity_check` and schema, `app.toml` / `providers.toml`, TOML parsing (when the host has a `tomllib`-capable `python3`), and local media exact paths must all pass. Missing dependencies or any failure no longer print `passed`; use `RESTORE_VERIFY_STRICT=0` to downgrade. Local media verification now uses exact `/data/... -> data/...` paths instead of filename lookup, and SQL query failures no longer fall back to zero.
- `deploy/upgrade.sh` gained a version guard: it refuses to run when the target version differs from `deploy/VERSION`, avoiding "just tag the current code with a new version"; `ELBOT_GIT_REF=vX.Y.Z` can switch source automatically. After rebuild it waits for health checks and runs `elbot doctor --no-model`, printing the rollback command and exiting non-zero on failure.
- Platform / model `last_error` values and the latest restart reason are redacted before being written into health snapshots, preventing upstream tokens (for example Telegram bot tokens appearing directly in request URLs) from leaking through `/healthz`, `/metrics`, and `/diagnostics`.

### Changed

- `deploy/watchdog.env.example` gained `WATCHDOG_READY_ALERT_THRESHOLD` / `WATCHDOG_READY_ALERT_COOLDOWN_SECONDS`: when `/live` is healthy but `/ready` fails repeatedly, a single `not_ready` alert is sent (a webhook is required) and it never becomes a restart trigger; restart decisions still depend only on `/live`.
- Version raised to `0.6.2`; `deploy/VERSION`, the Compose default image, and deployment / offline examples were updated.

## [v0.6.1 - 2026-10-01]

### Fixed

- Fixed only one startup heartbeat when no platform is enabled, which made `/live` expire after about 90 seconds and could cause the watchdog to restart repeatedly; the empty-platform mode now keeps sending scheduler heartbeats.
- `/live` now only means the process is still running; `/ready` no longer fails because a platform is disconnected or a model API is unhealthy. Platform and model state moved to the `degraded` status and separate arrays in `/healthz`, while scheduler heartbeat freshness remains an independent `/ready` check.
- Group rate limiting changed from "hit either the group limit or the user limit" to checking the user quota first and then the group quota, so one active member cannot exhaust the whole group budget while the group cap still protects overall resources.
- Added configuration thresholds, user/group rejection counters, the latest rejection reason, and the timestamp to `/metrics.rate_limit` to distinguish "one user flooding" from "the whole group overheating".
- Fixed the diagnostics bundle writing full environment variables from `docker inspect` and potentially leaking provider API keys / tokens; it now captures only container State/Ports and applies generic credential redaction to logs and JSON files.

### Changed

- Provider fallback now has explicit semantics: the default `fallback_mode = "circuit"` switches only after the circuit breaker opens; `fallback_mode = "on_error"` (or the compatible `fallback_on_error = true`) switches on the first pre-stream failure.
- Added `fallback_timeout_seconds` to bound one provider attempt and prevent a single request from being dragged through several upstream retries.
- Shell / Go Skill subprocesses no longer inherit environment variables whose names contain `KEY`, `TOKEN`, `SECRET`, `PASSWORD`, or `PRIVATE`; parent-process tools such as web search, image generation, and media download can still read `.env` credentials.
- `/tasks` and `/metrics` support `ELBOT_OPS_TOKEN`; when listening on a non-loopback address without a token the startup log warns explicitly. The watchdog can send the same token with `WATCHDOG_OPS_TOKEN`.
- `deploy/backup.sh` now runs the new `restore-verify.sh` in an isolated directory by default and treats a backup as successful only after SQLite integrity, configuration files, character assets, and local media references pass; `BACKUP_VERIFY=0` explicitly skips this.
- Version raised to `0.6.1`.

### Added

- `/tasks` gained `queued_by_kind` and `pending_by_kind`, exposing the wait queues bounded by concurrency limits and requests that already hold slots.
- Added `provider.FallbackTimeoutSeconds` / `fallback_timeout_seconds` and the `ProviderConfig.UsesFallbackOnError()` configuration entry point.
- Added `deploy/restore-verify.sh`: independently verify any backup in isolation without touching production `data`.
- Added the `elbot doctor` deployment acceptance command: checks configuration, health port, platform state, and model calls; with `--e2e` it performs a real CLI remote-protocol message round trip and reports `config_ok` and `e2e_ok` separately.
- Added the `/diagnostics` aggregate diagnostics endpoint; `/tasks` gained queue/timeout counters, the health snapshot gained the latest restart reason, and `/metrics` / `/diagnostics` show circuit-breaker, rate-limit, and restart information.
- Character asset metadata gained `version` / `source`, and `Store.Manifest` / `WriteManifest` produce sha256 manifests; the backup script generates manifests for character and media files and verifies them during restore.
- Added `deploy/upgrade.sh` / `deploy/rollback.sh`: upgrades take a config check, data snapshot, and previous-image snapshot first, and rollback verifies the data snapshot before restoring it.

## [v0.6.0 - 2026-10-01]

### Fixed

- Fixed missing `[ops]` circuit-breaker fields (`circuit_breaker_failure_threshold`, `circuit_breaker_open_cooldown_seconds`, `circuit_breaker_half_open_max`) that prevented `internal/config` from compiling.
- Fixed the missing `time` import in `internal/agent/context_runtime.go` and the missing rate-limit / image-generation metric types in `internal/app/ops_health.go`, which broke the whole build.
- Fixed `elbot service run` exiting immediately with code 0 when no platform is enabled: under Docker / systemd the container was restarted endlessly by `restart: unless-stopped` and the health endpoints disappeared with the process; it now stays alive and logs a warning.
- Fixed wrong assertions in the `diskguard` / `ratelimit` unit tests; `go test ./...` is green again.

### Changed

- The runtime image now ships more of the CLI tools agents commonly need (bash, procps, iputils-ping, dnsutils, netcat-openbsd, iproute2, less, file, tree, tar, gzip, xz-utils, rsync, zip, python3), and the list is additive by default.
- `XDG_CACHE_HOME` now points at `/data/cache`, so Go Skill build caches and media temp files live on the data volume and survive container recreation.
- Inside the `go-runtime` image, building Go Skills no longer fails with permission errors on `/go`: GOPATH / GOMODCACHE / GOCACHE now point to directories writable by UID 10001.
- The version number now comes from a single source (`deploy/VERSION`); images, offline bundles and docs no longer duplicate it.

### Added

- Multi-architecture Docker builds: the build stage is pinned to `$BUILDPLATFORM` and cross-compiles natively, so arm64 builds no longer run the whole Go build under QEMU.
- Docker builds now support restricted networks: new `GOPROXY` and `APT_MIRROR` build arguments (the latter also forces https), plus `GO_BASE_IMAGE` / `RUNTIME_BASE_IMAGE` overrides to use faster mirrors or to pin digests.
- New offline / prebuilt deployment artifacts: `prepare-offline.sh` produces amd64/arm64 static binaries, a `docker load`-ready scratch image and a full offline bundle, with `SHA256SUMS` checksums.


## [v0.5.0 - 2026-09-28]

### Added

- QQ official bot now supports group chats
- Added Media Center for unified management of all media files

### Changed

- Optimized meta information for the Agent; it now displays the platform, group number, group ID, user nickname, and user ID.
- Now compresses images that exceed the configured size; original media and Media IDs remain unchanged. The S3 backend is now initialized on demand; it will only issue a warning if the configuration is unavailable, and will no longer prevent ElBot from starting.
- Command parameters for toolized AgentSkill previously only supported strings, numbers, and booleans; Now JSON arrays and objects will be compressed into a single argv parameter and passed to the corresponding `[args]` flag.
- Previously, append-and-resend for regular users and high-risk tool confirmations would wait indefinitely and occupy the current Turn for a long time; Now, it stops by default after 10 minutes of no valid operation; if the corresponding Session TTL is shorter, that will be the limit. Appended content or `/detail` will renew the timeout. Superadmins are not subject to the additional 10-minute limit but still adhere to the enabled Session TTL.
- Multimodal images were previously only sent as `image_url` content segments; the model could see the images but did not know the reusable addresses; Now, a user text label containing the sequence number within the message, name, and HTTP(S) URL will be derived before each image; persistence `content` and visual fallback use the same text projection, while `segments` still only saves the original structure and requires no database migration.
- `/stop`, request timeouts, or upstream cancellations will now terminate the full process tree of one-time exec Hooks to avoid residual child processes spawned by the Hook; Persistent Workers will instead receive `event.cancel` and continue to be reused.
- Non-existent `#文件` references in the CLI TUI are now sent as plain text as-is and no longer block message submission; files that exist within the same message will still be expanded normally.
- `/help`, detailed help, and slash command completion are now filtered by the current user's permissions; regular users cannot discover commands restricted to superadmins.
- Modified System prompt structure and added meta information
- The Session Meta of the system prompt now includes the local creation time of the Session, accurate to the second; this value will not change with conversation turns to maintain Prompt cache stability.
- `read_file` and `edit_file` previously completely rejected text files exceeding 2 MiB; Now, files between 2–100 MiB can still be read line-by-line, grepped, and edited; AST, full diffs, and overly long confirmation content are disabled based on file size only during actual calls, while revision verification, pre-checks, and atomic writes are preserved.

### Fixed

- Fixed an issue where OpenAI-compatible upstreams returning HTTP 200 HTML/non-SSE pages were masked by Scanner's over-length token error; Abnormal responses will now be identified before stream parsing, and only a limited, desensitized summary will be returned.
- Fixed an issue where referencing the last assistant reply of the original Session would incorrectly create a new Session after the Session timed out due to inactivity, `/new` was executed, or the Session was switched; Now, the Session will be automatically restored; referencing an earlier reply will still result in a Fork.
- Fixed the inconsistency in reading service-level environment variables between built-in tools and Go Skills, which caused some tools to be unable to read the configuration directory `.env`.
- Fixed an issue where the response context was cancelled prematurely before reading the error body when the OpenAI-compatible interface returned a non-200 status, causing the actual upstream error to be lost and only `failed to read body: context canceled` to be displayed.
- Fixed an issue where the read file tool incorrectly identified some non-UTF-8 text encodings as binary files.
- Fixed a bug where large files sent from Elwisp to qqonebot caused total blockage.
- Fixed bug where querying chat history could not find pure images.
- Fixed a bug where logs might be saved as base64

## [v0.4.2 - 2026-08-06]

### Changed

- Optimized .env, platform secret, and token parsing logic
- `read_file` now requires high-risk confirmation when reading `.env`, `.env.*`, and common credential filenames; reading other files remains low-risk.
- Provider requests have been changed from defaulting to inheriting the process environment proxy to only using the explicit `proxy` of the corresponding Provider in `providers.toml`; Model lists and chat requests will connect directly when not configured or left blank.
- CLI TUI notifications now support log levels and are displayed in White for Debug, Green for Info, Yellow for Warn, and Red for Error; Added an optional `level` field to the `notice` message synchronization of the remote CLI.

### Fixed

- Fixed an issue where in group chats, after triggering the LLM with a prefix, the subsequent output Hook misidentified messages with the stripped prefix as not triggered, preventing default rules such as `agent.turn.output.prepared` from executing.

## [v0.4.1 - 2026-07-28]

### Changed

- `/new` and Session idle expiration are changed to only clear the current session pointer; a new Session is created only when the first ordinary message arrives; Expired historical Sessions are no longer deleted immediately and can be restored directly via `/resume`.
- Update environment variable inheritance and hierarchical `.env` configuration for Shell and Hook.
- `/hooks` list changed from displaying plugin rules item by item to aggregating them by plugin name; Worker status, all rules, and details are now expanded only when using `/hooks <插件名>`; root rules and built-in Hooks are still displayed separately.

### Fixed

- Fixed the issue where `/status` would display active requests from other users or Sessions within the process when the caller has no current Session; Now this status only displays `active requests: none`.
- Fixed an issue where JSON encoding or WebSocket writes would hold the shared send lock for a long time when QQ OneBot sent messages such as large images, causing slash commands and replies from other Sessions to become unresponsive; Write waits for OneBot and remote CLI are now cancellable and have a clear timeout; OneBot will reconnect upon failure.
- Fixed an issue where regular users could bypass confirmation when modifying their own high-risk core resident memory; regular users can now only confirm `high`/`critical` risk calls after tool permission validation passes.

## [v0.4.0 - 2026-07-23]

### Changed

- When the context window cannot be identified from model metadata or configuration, the default window is adjusted to 256k.
- `/resume <编号>` now restores non-current Sessions directly based on the most recent update time; `1` represents the most recent item, and it is no longer required to first execute a bare `/resume` to establish numbering.
- Context compaction is changed to retain original user utterances from history and filter tool results; upon success, it switches to an independent `原标题 compacted-N` Session, and the compacted content along with the new input is fixedly materialized as the first user message; Simultaneously fixed concurrency issues related to model switching, `/stop`, and Session change commands.
- Hook Actor now provides platform nicknames, group nicknames, and pure display names; chat history is saved and searched separately by platform user ID and name.
- Soul and resident memory are now uniformly constructed by turn from the built-in System Prompt source; Resident memory is no longer registered as a Hook; the `llm.messages` of ordinary Hooks is explicitly defined as read-only context.
- `workspace` tools will also load the `AGENTS.md`/`AGENT.md` of the current directory when they are first discovered or injected; The same path within the same Session shares a one-time record with the switch and reset entries, so it will not be injected repeatedly.
- Optimized the `read_file` tool, supporting directory search, selection of search results by index, and precise return of complete function content and its start and end line numbers based on AST function names.
- Optimize system prompt
- AgentSkill startup scan no longer caches `SKILL.md` body permanently, keeping only summaries and paths; The current body will be read when `discover_tool` is discovered by name or `@skill` is preloaded; the same applies to tool-based Skills with `ELBOT_SKILL.toml`.
- The proxy parameter of the `web_extract` tool has been changed from `disable_proxy` to `proxy`: use `WEB_EXTRACT_PROXY` or the system proxy environment when left blank, fill in `disabled` to disable the proxy, or fill in a URL to use a specified proxy.
- The `send_file` tool now uses the `source` parameter to send files, supporting local paths, `file://` URIs, and HTTP(S) URLs, and will automatically send images as image messages based on MIME type/extension.
- AgentSkill no longer uses `python_skill_run` for fixed wrapping to execute Python scripts; When `ELBOT_SKILL.toml` is absent, it remains a descriptive Skill, and general-purpose tools such as shell can be used according to the documentation; Descriptive AgentSkills do not read `SKILL.md`, avoiding risk; after toolization, `risk` of `ELBOT_SKILL.toml` shall prevail.
- Skill scanning has been changed to delayed execution after startup, with a fallback to ensure scanning upon the first use of `discover_tool`, reducing startup blocking.
- Session idle expiration is now managed by four `[session.idle_expiration]` configurations, which separately control the current Session expiration time for ordinary users and superadmins in group chats and private chats; By default, all users in group chats expire, while superadmins in private chats do not expire.
- ``shell`` tool removed the ``path`` parameter; commands are executed in the current workspace by default, while background tasks remain restricted to their respective sandboxes.
- Relative paths for ``read_file``, ``edit_file``, and ``send_file`` are now resolved based on the current workspace; Absolute paths can still be used temporarily and will return a warning.
- `llm_usage` audit events changed from debug level to info level; token consumption data can now be recorded by default with `log_level=info`.
- Disconnection reconnection for QQ OneBot, QQ Official, and Telegram platforms has been changed to exponential backoff (starting at 3s, doubling, capped at 10s) with downgraded logging: consecutive failures are logged as 'warn' only on the first occurrence and 'info' upon recovery, preventing log flooding in every round.
- Platform media output now supports identifying `base64://`, `file://`, `http://`, and `https://` sources in `path`; Regular local paths are still handled according to the platform's default method.
- QQ official now uses URLs instead of base64 when receiving images
- Refactored hooks, see docs for details.
- On Windows, the `shell` tool prioritizes `pwsh`, followed by `bash`, and finally falls back to `powershell.exe`.

### Fixed

- Fixed the issue where the Session could still be switched while the current Session was requesting the model, executing a tool, or waiting for confirmation; the Session switching command now prompts to first use `/stop` to end the current process.
- Fixed an issue where process Hooks under Linux services could only use the service process PATH and were unable to obtain the configuration `.env`, causing commands available in the terminal, such as `uv`, to fail to start; One-off execs and Workers now share the merged environment and PATH lookup rules.
- Fixed an issue where pending messages received during the final LLM request of a tool flow were lost when the current turn ended; The current response now ends normally, and the next round of requests is automatically started after multiple pending messages are merged.
- Fixed an issue where `llm.request.prepared` could temporarily rewrite the initial input or historical messages of the current turn, and pending images were lost during queuing while Hook modifications were not persisted; The request Hook now only modifies pending messages from the current new drain.
- Fixed an issue where parameters rewritten by the tool prepared Hook were not synchronized to the current LLM context, and in-process Hooks could rewrite tool IDs/names; Actual execution, subsequent requests, and transcripts now uniformly use the final arguments.
- Fixed issues where Cron re-delivery used old job snapshots to overwrite disable status, scheduling, and task content, as well as duplicate generation, duplicate sending, and delivery statuses overwriting each other when multiple platforms were connected simultaneously; Failure or blocking reports from LLM returning `completed=false` will now also be frozen and complete the re-delivery.
- Fixed an issue where a completed one-time LLM Cron would directly reuse the old report and fail to create a new background Session after being re-enabled or rescheduled; notification failures and platform reconnections still resend the same round of persisted results.
- Fixed an issue where a reload failure after writing AgentSkill configuration would leave an inconsistency between the disk and the runtime registry; Skill reload is now changed to be serial, fully validated, and atomically replaced; the old registry, catalog, and AgentSkill configuration are retained in case of name conflicts or candidate failures.
- Completed Elnis HTTP request headers, request reading, response writing, and idle connection timeouts, and now rejects a trailing second JSON value in the request body; unknown fields continue to be ignored as non-semantic fields.
- Fixed an issue where Elnis LLM marked events as `completed` before sending the report; Reports now use a recoverable outbox, completing only after all target acknowledgments are persisted; failed items will be retried periodically and after restart.
- Completed the missing `JINA_API_KEY` in the default `.env.example`.
- Fixed an issue where Session deletion or archive confirmation might affect the wrong target due to changes in the list or the current Session, and ensured that storage errors are correctly returned to the caller.
- `read_file`'s `start_line` now supports integer strings occasionally generated by the LLM to prevent read failures of valid line numbers caused by JSON type mismatches.
- After executing `/stop`, the CLI TUI will converge the current running state into `done` and fix the elapsed time, no longer accumulating time in the status bar.
- Fixed the issue where rule cards were repeatedly injected into the context when performing tool discovery or inline preloading of multiple ELyph Skills; In the same Session, only Skill content is returned after the first injection, preserving the first rule card in history to facilitate cache hits.
- Fixed the issue where Session messages under the same timestamp might be loaded out of order by UUID, leading to unstable historical context order.
- Fixed the issue where `workspace` tools did not support `~`, `~/path`, Windows `~\path`, `$HOME`, and `$HOME/path` home directory paths when setting the directory.
- When the file segment in a QQ OneBot private chat is missing `url`, `get_file` will be called; If a download URL is returned, it will be saved to ElBot; if only a OneBot local path is returned, that path will be displayed directly.
- Inbound @ messages in QQ OneBot will now prioritize displaying the group business card, followed by the regular nickname, in the format `[at 名字 qq:<id>]`, and will fall back to the QQ number if neither can be retrieved.
- Fixed an issue where Chinese output might be garbled when the `shell` tool falls back to PowerShell on Windows.
- Fixed an issue where bash AST parsing failure for shell commands on Windows (when bash is missing) caused risk classification, sandbox validation, directory change interception, and warning analysis to all fail; In PowerShell environments, AST parsing is skipped, and risk classification directly returns high-risk, requiring user confirmation.
- When OneBot fails to send an image, a visible fallback will no longer appear, but it will still be logged.

### Added

- **Refactor hook system**
- Tool completion Hook supports returning `message.segments` containing URLs, paths, or base64 images; Multimodal tool results can be persisted and provided to the model as subsequent image messages according to the OpenAI Chat Completions protocol.
- Pre-hooks for user input and tool pending support using `message.segments` to simultaneously rewrite text and attach images; the final multimodal content will be written to the Session history before the request.
- `/chat` and `/work` now support carrying messages directly, which are sent immediately after switching the Session mode; Session command status is now isolated by platform Scope.
- QQ OneBot added `send_file_mode` configuration; local images and files are sent using base64 by default, but can be explicitly changed to `file_uri` in shared file system deployments.
- `/log` added `-s` and `--system` to filter and display `system prompt` logs.
- CLI TUI wide-screen mode now supports using the mouse to drag the divider between the chat area and the notification area, allowing the widths of both sides to be freely adjusted during runtime.
- `read_file` added `mode=ast`, enabling lightweight AST search by name for Go and Shell files; `mode` is also unified into three reading modes: `read`, `grep`, and `ast`.
- Refactor AgentSkill: remove the py wrapper and execute the corresponding skill directly via shell; also support adding `ELBOT_SKILL.toml` in the AgentSkill root directory to register it as a normal tool, facilitating the LLM's direct call of structured parameters.
- Added a hidden meta-tool `agent_skill` for reading or writing the `ELBOT_SKILL.toml` of AgentSkill; it validates the configuration before writing and reloads upon success.
- The first run will generate `skills/agent/agent_skill_creator/SKILL.md`, which explains how to register an AgentSkill as a regular tool.
- The first run will generate `skills/agent/write_elbot_hook/SKILL.md`, which serves as a prompt to write ElBot rule Hooks according to your needs.
- `ELBOT_SKILL.toml` of AgentSkill supports writing only `risk` / `superadmin_only` for document visibility restrictions; it will not be registered as a normal tool when tool-related fields are not provided. By default, `agent_skill_creator` and `write_elbot_hook` Skills will generate TOML that is low-risk and visible only to the superadmin.
- Added `/usage` command: aggregates token consumption from the audit log, supporting summaries by model/day/Session, with shortcut parameters `-d` for days, `-m` for model, and `-s` for Session.
- Added ``workspace`` tool: sets the shared working directory of the current foreground Session; path-related tools will resolve relative paths based on this directory. When switching to a directory containing `AGENTS.md` or `AGENT.md` for the first time, the contents of the documentation file will be automatically attached; A prompt to shorten will be displayed when the file exceeds 64 KiB.
- Added `[platform_files]` configuration to uniformly control the maximum save size and download timeout for platform inbound files.
- QQ OneBot now supports automatically saving inbound files from superadmins in private chats; messages containing only files will only reply with the save path or a "too large" prompt without invoking the LLM; group files are not automatically saved.
- The `/requests` command now displays the current execution stage (preparing/llm/tool/sending) and the duration of each stage for every turn, allowing you to distinguish whether the LLM is slow or the platform delivery is stuck.
- Executing Hooks will be displayed in `/requests`, and Hooks for the current Session will also be displayed in `/status`; Use `/stop` to cancel long-running Hooks; manual cancellations are recorded as normal cancellations.
- Inline preloading supports tool shorthand `@t:<name-or-tag>` and Skill shorthand `@s:<name>`, and is compatible with Chinese full-width colon `：`.
- The CLI TUI input box now supports fuzzy completion of local files using `#文件名`; references are replaced with the filename and file content upon sending, and paths containing spaces can be written as `#"a b.txt"`.
- `web_extract` added the `jina` parameter, which defaults to using Jina Reader; passing `jina=false` allows manually switching to direct crawling.
- `web_extract` added the `force_refresh` parameter, which can skip the cache, re-fetch the webpage, and update the cached content as needed.




## [v0.3.0-alpha - 2026-07-01]

### Changed

- `[tool]` previews for multiple tool calls in the same round will be merged into a single message to reduce platform spam.
- Elvena LLM events and LLM Cron now support `session_mode=chat|work` selecting background Session mode, with the default remaining `work`.
- `/detail` high-risk tool call details now support custom plain text display for tools; When not customized, JSON parameters will still be formatted into a more readable multi-line display, and `\n` within strings will be displayed as actual line breaks.
- High-risk confirmation details for `edit_file` now display operations such as replace, add, delete, and match by file, mode, and editing step.
- `edit_file` no longer exposes the `dry_run` parameter to the LLM; the system will automatically pre-check and generate a diff before user confirmation, and if the pre-check fails, it will not proceed to confirmation or write to the file.
- `modify_el_skill` now reuses the `edits` editing instructions and execution capabilities of `edit_file`, and pre-checks edits, ELyph syntax, and no-op modifications before confirmation, displaying the pre-check diff in the high-risk confirmation details.
- Update `ELyph` version to v3
- qq heartbeat ack and qqofficial gateway resumed are no longer logged
- read_el_skill now depends on modify_el_skill to facilitate possible modifications
- ELyph syntax is no longer validated during ElBot startup to avoid slowing down the startup speed.
- Colons at the end of ELyph `**`/`~` text are now returned as warnings to `create_el_skill`/`finalize_el_skill`, and no longer block creation or finalization.
- `modify_el_skill` no longer automatically reloads after modifying `SKILL.elyph`; `finalize_el_skill` must be called after modification to take effect.
- Tool results now support unified `Warnings` output, used to prompt the LLM to prioritize more appropriate tools in the future.
- `read_file`/`shell` will suggest using `read_el_skill` when reading EL Skill files; Direct modification of EL Skill files by `edit_file` or shell will be rejected before confirmation or execution; `modify_el_skill` should be used instead.
- Source files for resident memory and long-term memory are now included in general FileGuard protection; reading will suggest using memory tools, and direct writing via general file tools or shell will be rejected.
- Hook logs are no longer duplicated

### Fixed

- `long_memory_write`'s `update` now supports updating meta via filled fields, and `content_edits` has been added to reuse the edit operation of `edit_file` to modify the body; A pre-check will be performed automatically and the diff will be displayed before confirmation.
- Fixed the issue where `edit_file` fails during the write confirmation stage when creating a new file using `create=true` if the target parent directory does not exist.
- `response_timeout_seconds` now controls the total duration of a full round of user requests; by default, `0` indicates no time limit; A single LLM streaming request is controlled only by the first packet and idle timeout.

## [v0.2.0-alpha - 2026-06-27]

### Added

- Elvena v3 action channel: Elnis supports `calls`, initially supporting raw platform APIs as well as `message.recall`, `member.mute`, and `chat.leave` capabilities; unsupported ones can directly call the messaging platform API. Hook rules can execute scripts via the `exec` action and use `stdout=elvena` to trigger Elnis direct/LLM/calls via the internal Elvena Bus. Direct calls-only requests will not send additional messages.
- Added `match_mode` and `index` parameters to the `*_match` operation of `edit_file`: when `match_mode=line`, it matches the entire line by single-line prefix (tolerating leading indentation to avoid newline character matching errors); `content` (default) maintains exact substring semantics. When there are multiple matches, the specific match can be selected via `index`; if `index` is not provided, an error will be reported and all matching positions will be listed.
- Hook rules added role partitioning and tiling control fields: `roles`, `actor_roles`, `group_roles`, `consume`, `stop_propagation`. Platform message Hook output will now be sent; `consume=true` can block subsequent command/LLM processing.
- Hook rules `send` action added `segments` list, supporting multi-type multi-segment output (text/image/file/emoticon, including url/path/base64), with a format unified with Elvena segments.
- Hook rules `exec` action added `outputs` stdout mode; script stdout is parsed as JSON to extract the `outputs` array and optional `text`; When `field` is set, `text` overwrites the corresponding fields; otherwise, the original text remains unchanged.
- Platform inbound context added a unified group identity `owner/admin/member/unknown`; QQ OneBot and Telegram will map group owner/administrator/ordinary member.
- Hook platform context now populates the current platform message ID `platform.message_id` and the referenced/reply target message ID `platform.reply_to_message_id`, facilitating the processing of referenced messages by rule Hooks, such as recalling a referenced message.
- `/hooks` command: list all registered Hooks, view detailed configuration of a specific Hook, and hot-reload all Hooks (takes effect after modifying `hooks.toml` without requiring a restart).

### Changed

- LLM request timeout configuration changed to `first_chunk_timeout_seconds`, `stream_idle_timeout_seconds`, and `response_timeout_seconds`; the old `timeout_seconds` has been removed; By default, the wait time for the first streaming event is 180 seconds, streaming idle is 60 seconds, and there is no total duration limit for the entire response.
- Provider configuration refactoring: removed unused `[global_default]`, removed `[model_metadata.context_windows]` global model window table; Model-level `context_window` and `extra_payload` are now unified under `[providers.<name>.model_configs."<model>"]` and looked up by `provider/model`, avoiding conflicts between models with the same name across providers.
- Added `proxy` field to Provider, supporting HTTP/SOCKS5 proxies.
- Emoticon Hook changed from an embedded plugin to a rule Hook example; the emoticon plugin and `emoticon.toml` assets are no longer built-in.
- Display the current retry count via Notice when LLM connection/HTTP retriable requests fail.
- The risk level of the `finalize_el_skill` tool has been downgraded from high to medium.

### Fixed

- Fixed an issue where long tool chains would be silently stopped by the Agent's internal 5-minute default request timeout; users will now be notified upon a full round timeout.
- Fixed an issue where API timeouts when sending images/emojis/files via QQ OneBot might cancel WebSocket writes and trigger disconnection and reconnection; a text notification will now be attempted to the same target when media sending fails.
- Fixed an issue where OpenAI-compatible streaming responses that disconnected midway but were missing `[DONE]` were treated as normal terminations; now it will explicitly notify that the LLM response was interrupted.
- Fixed an issue where OpenAI-compatible streaming requests used a single HTTP timeout, causing them to be incorrectly interrupted when the model's first token was slow or long outputs exceeded 60 seconds.


## [v0.1.0-alpha] - 2026-06-24

The first pre-release version of ElBot. A lightweight Agent/Chatbot framework aimed at personal assistants, platform bots, and orchestratable automation assistants.

### Added

- **Lightweight Core**: Implemented in Go, local startup <10ms, resident memory approximately 30MB.
- **Chat/Work Dual Mode**: chat mode disables tools, suitable for daily chatting and low-cost conversations; work mode enables tool discovery and invocation; models can be configured independently for both modes.
- **Tool Discovery Mechanism**: By default, only `discover_tool` and the tool name are exposed, with the full schema injected on demand to reduce unnecessary context overhead.
- **Session Service**: Persistent sessions supporting recovery, archiving, pinning, Forking, deletion, pagination, and platform isolation; automatic context compaction for long conversations.
- **Hook Layer**: Inserts extension logic at key points such as Agent input, LLM request/response, and platform sending; includes built-in rule Hooks and resident memory Hooks.
- **Standard Cron and LLM Cron**: Standard Cron sends fixed content directly according to a schedule; LLM Cron uses ELyph task descriptions to drive model execution, supporting one-time and periodic tasks, missed run recovery, and broadcasting.
- **ELyph Task Notation**: A structured task description language used for LLM Cron and native Skills to reduce natural language ambiguity.
- **Native and External Skills**: The `create_el_skill` meta-tool supports LLMs in creating native EL Skills (pure ELyph or accompanied by Go source code and compiled); Compatible with agentskills.io style external AgentSkills (accompanied by Python scripts).
- **Elnis listening hub**: Receives external events delivered by Elwisp via the Elvena HTTP protocol, supporting three modes (record/direct/llm) and multi-target delivery.
- **Multi-platform adapter**: CLI (including client/server separation and remote connection), QQ OneBot v11, QQ Official Bot, Telegram Bot API.
- **Security Policy**: Tool risk grading, role permission verification, high-risk confirmation workflows, and a lightweight background shell sandbox.
- **Memory System**: Resident memory (core/normal layers) injected by platform and actor; long-term memory based on Markdown source data and SQLite FTS retrieval.
- **Logs and Audit**: Runtime logs, audit logs, and Elnis logs are separated, supporting structured fields and date-based rotation.
- **SQLite Persistence**: Unified storage for Sessions, messages, context summaries, tool call records, Cron jobs, and Elnis events.

### Known Limitations

- MCP tools, sub-Agents, and full multimodality (actual model input for voice, video, and files) are not yet implemented.
- Interfaces, configurations, and internal implementations may still be adjusted; it is more suitable for exploratory use as a personal Agent/bot framework.
