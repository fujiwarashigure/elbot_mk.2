<!-- This file is auto-translated from CHANGELOG.md. Do not edit manually. -->

# Changelog

All notable changes to ElBot will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

### Added

- Added platform-level deduplication for OneBot inbound messages: `processing` / `completed` / `failed` are keyed by `platform + bot self_id + scope + message_id`, and a reconnect replay within the TTL no longer wakes the model again or double-bills; the deduplication state is independent of the `history` switch and has a hard cap of `inbound_dedup_max_entries`.
- Added a bounded preprocessing channel to OneBot: `@` parsing, quoted-message fetching, and merged-forward expansion run in a worker pool bounded by `preprocess_workers` / `preprocess_queue_size`, and when the queue is full ordinary messages are rejected locally instead of creating unbounded goroutines; recall, member, and management events go through a separate high-priority worker / queue and are handled inline in the read loop when it is full, so they are not blocked by ordinary chat traffic.
- Added an optional chat hard budget mode `[budget_limits].chat_hard_limit`: on top of the existing token/cost ledger it adds a path of "atomic reservation before the call, then settle and release the difference after usage is returned". Concurrent requests cannot pass through the remaining quota at the same time; when the upstream is missing usage, accounting is conservative against the reservation and `budget.uncertain` is written; when cost hard limits are enabled but the model has no price, the request is explicitly rejected; unsettled reservations left by cancellation/timeout/restart stay conservatively reserved and are not refunded automatically.
- Added a group runtime state machine persisted to `state.toml [group_runtime]`: when the bot itself is muted or kicked, the current group enters `muted` / `removed`, and the server immediately cancels in-flight requests for that group, rejects new model calls, and drops scheduled/background notifications destined for that group; after the mute is lifted or the bot rejoins, the group returns to `active`. A state change sends only one aggregated notification to the superadmin, repeated events do not notify again, and notifications from the paused period are not backfilled in a batch.
- Added group Session thread mode: `/*grouppolicy thread-mode group` lets all members in the same group share one Session, ordinary messages are executed serially in arrival order, and each user message carries a server-generated speaker-member marker and `speakers` metadata, preventing multi-member inputs from overwriting each other; enabling it does not migrate the old per-person independent Sessions, and switch/modify commands for the shared Session are restricted to the group owner / group admin / superadmin.
- Added a consecutive-message merge window `/*grouppolicy merge-window <0-10000>`: consecutive messages from the same member within the window are merged into the same turn, while different members still stay serial and do not share a permission subject; the base queue completes the merge before the request enters the model, a recall / member leaving the group synchronously drops queue items that have not run yet, and a per-Session queue cap prevents unbounded backlog.
- Added a local deterministic group knowledge base / FAQ: `[group_knowledge]` provides the global switch and caps, and `/*faq add` / `add-contains` / `add-keyword` / `remove` / `clear` / `test` / `on` / `off` provide the management entry points; entries are written to `state.toml [group_knowledge]` by `platform + group scope`, and on a hit the server answers directly without creating a Session, without calling the LLM, and without consuming chat tokens, while remaining subject to wake words, quiet hours, inbound rate limiting, and group runtime state.
- Added the ordinary-member self-service panel `/*me [tasks|quota|all]`: by the current actor's FairKey it shows that member's in-flight requests, queued messages, current Session stage, and the group and personal quotas for image generation / vision / chat tokens / cost; cross-group global aggregate usage is hidden, and it is read-only and does not call the model. `/*requests` still keeps the global / administrative view.
- Added in-group deterministic reminders / polls / signups: `[group_services]` provides the global switch and caps, and `/*grouppolicy services on|off` provides the group-level switch; reminders support relative time, time of day, and date-time and are scheduled and sent locally, polls support create / vote change / close and result tallies, and signups support capacity, join / leave / close. All state is written to `state.toml [group_services]`, does not call the model, does not consume chat tokens, and is subject to group runtime state.
- Long-term memory gained source association and deletion entry points: `angel_memory` records the source kind, source user, platform message ID, and Session ID on write; `/memory list|show|delete|source` provides the superadmin management entry, and `/forget list|<id>|source <message id>` provides a deletion entry usable by ordinary members, where in group chats ordinary members can only delete memories whose source member is themselves, while the group owner / group admin / superadmin can delete any memory in the current group scope. `/forget resident normal|core|all [--confirm]` can clear one's own resident memory. `[angel_memory].forget_on_recall` defaults to `true`, and when a platform message is recalled, derived memories are deleted by source message ID; when `/delete` deletes a Session, memories written by that Session are also cleaned up by `source_session_id`, while memories from the old version that have no structured source columns do not match, avoiding accidental deletion.
- `state.toml` now supports hot reload of external edits: the process detects external modifications by mtime once every 15 seconds and merges them into effect, and added `/*state` to view the load state and modifications not yet in effect and `/*state reload` to apply them immediately; after they take effect a `runtime_state_reloaded` audit event is recorded, and a notification is sent to the superadmin when there really are changes. External modifications are merged before every internal write-back, so manual edits no longer require a restart and are not directly overwritten by internal write-back. Hot reload covers `mode_models`, `compact_model`, `naming_model`, `context_overflow`, `group_policy`, `group_knowledge`, `group_services`, `group_runtime`; the `[budget]` quota ledger is still owned exclusively by the running process and is only restored from the file at startup, to avoid losing in-flight reservations.
- Added an optional model tool for deleting long-term memory, `angel_forget` (`[angel_memory].allow_tool_forget`, off by default): it can only delete a single memory whose source member is the current speaker and which belongs to the current platform/Session scope, while memories of others, other scopes, and old memories with no source member are all rejected; risk level `high` enters the tool confirmation flow (`/*detail`, `/*confirm`, `/*reject`), and the tool itself also requires a second call with `confirm=true`, with the first call returning only the content to be deleted; at most 3 deletions per minute within the same scope. This tool is a hidden tool, wired in through `angel_recall`'s dependency injection model, and batch deletion still goes through `/memory delete` and `/forget`.
- Added the `/memory backfill` migration entry for old memory sources: old long-term memories only wrote the free-text `source = "tool"` and have no structured source columns, so `source_kind` can be backfilled deterministically (the precheck requires `--confirm` to run), and the number of source members / message IDs / Session IDs that cannot be backfilled is explicitly reported. These old memories continue to be handled as "no match, no accidental deletion": they are not hit by `/forget source`, recall cleanup, or `/delete` of a Session, and are not deleted by `angel_forget` either.
- Added optional transcription `[asr]`: it reuses the OpenAI-compatible `/audio/transcriptions` endpoint of the existing `[providers.*]`, and a woken voice/recording message is first transcribed into a `[语音 N 自动转写（可能有误）：...]` text segment before entering the chat, tool, and context flows; when it is not enabled or transcription fails, the original `[语音]` reference is kept. provider/model can declare the capability with `audio = true/false`, jointly constrained by the global `[asr].enabled`, the group policy `asr on|off`, and the four-level daily quotas `asr-quota` / `user-asr-quota` / `global_asr_daily` / `user_asr_daily`; a single message uses bounded parallelism by `max_segments` / `max_concurrent`, recording reads are limited by `max_audio_bytes`, successful transcriptions are cached by MediaID, and deterministic 4xx responses enter a short negative cache.

### Fixed

- Long-reply chunked sending and partial-send tracking now cover the major platforms: OneBot `sendContextText()` returns a partial receipt carrying `Failed` / `Failure` when a later page fails; official QQ gained rune-based paging and keeps the failure receipt for later pages, and when Markdown pages are already visible it no longer falls back to plain text and causes duplicate sending; Telegram's HTML→plain text, rich→HTML, streaming final replacement, and the multi-output, multi-target sending loops of OneBot/Telegram/official QQ all merge the receipts of already-succeeded pages/targets and mark partial failure. `delivery.Manager` gained `SendNoticesWithReceipt`, so when a batched Agent output partially fails the platform message count is left in the audit log, easing reconciliation instead of resending the whole batch.

### Changed

- Cleaned up the staged old description in v0.6.8 that "the quota ledger did not include token/currency and per-user/global budgets", replacing it with a capability description consistent with the four-dimensional token/cost ledger.

## [v0.6.8 - 2026-10-04]

### Added

- Version raised to `0.6.8`; the version examples in `deploy/VERSION`, the Compose default image, the build/offline scripts, and the Chinese deployment docs were updated in sync.
- `get_forward_msg` gained a websocket read-stage limit: `forward_max_result_bytes` now takes effect during the read, and an over-limit response is not read in full before being truncated.
- The quota ledger was extended to four dimensions - group / per-user within a group / global / per-user global - with image generation and vision metered by call count and chat metered separately by token/cost; added `[budget_limits]`, the group policies `user-image-quota` / `user-vision-quota` / `chat-tokens-quota` / `chat-cost-quota`, and recorded `budget.tokens` / `budget.costs`.
- Added the tool execution idempotent ledger `budget.executions`: for the same scope + actor + tool call ID only one argument digest is allowed to execute first, replays are suppressed, and calls with inconsistent ID arguments are rejected; provider retries are written separately to `budget.retries`.
- Added actual call concurrency caps per provider, `provider_max_concurrent` / `provider_queue_max_size` / `provider_wait_timeout_seconds`; the request queue gained queue-full, timeout, cancellation, average wait, and oldest wait metrics.
- The group analysis summary model is now also re-authorized before execution against the current group model catalog; image generation models remain constrained independently by the global image generation configuration, the group tool allowlist, and the image generation quota.
- Added the unified history write gate `internal/historygate`: every adapter's `chat_history` / `outbound_messages` writes go through a trusted `platform + scope` policy decision; when `history=off` it also stops new persistence of inbound, assistant, tool transcripts, media associations, summaries/naming, and tool result previews, while the current turn is still processed in memory and old records are not deleted.
- `learning=off` now covers the complete learning lifecycle: the observation hook, mining, moderation, revocation, history queries, and context injection all check the policy again in the service layer; after it is turned off no new candidates are produced, and existing knowledge is no longer used to inject into the model.
- `state.toml` changed to safe writing with fsync + backup replace, removing the fallback that overwrote directly when rename failed on Windows; an interrupted replacement can be recovered from `state.toml.bak` at startup. The quota ledger gained argument digest validation, so replaying the same call ID with the same arguments is not counted again while different arguments are rejected; when the ledger cannot be reliably persisted, restricted calls fail instead of continuing.
- Recall/cancellation gained a late-output gate: the turn terminal state is checked before streaming flush, before tool results are handed back to the Agent, and before the final message enters the send queue; when a provider or tool ignores `context.Cancel` and returns late results, new messages are still not sent.
- The group model catalog now covers the model targets actually executed: turn hooks, cron overrides, the compaction model, and the vision fallback are re-authorized before calling the provider; `discover_tool` results are constrained at the same time by the current group allowlist filter, the second authorization before execution, and tool-name validation after parsing.
- The request queue gained the capacity caps `queue_max_per_user` / `queue_max_per_scope`, preventing a single user or a single group from queuing without bound; fair scheduling continues to use waiting time as the fallback.
- Added the group-level policy `/*grouppolicy`: wake words, response mode (`mention` / `all` / `keyword` / `reply` / `off`), default Session mode, default model, tool allowlist, image generation/vision quotas, quiet hours, and group analysis/learning/history switches are stored by `platform + group scope`.
  - The policy is judged by the server for the current group, and the command does not accept cross-group targets; group admins can only change the ordinary policy of their own current group and cannot modify provider, secrets, or global Shell permissions.
  - `/learning` is still available only to the superadmin by default; the superadmin can run `/*grouppolicy learning-moderation on` in this group and subdivide `view` / `decide` / `mine` / `delete` / `export` / `policy` permissions through `learning-moderation-actions`, and this does not elevate them to a global superadmin. Group admin moderation only applies to the current group scope and becomes invalid immediately after the permission is revoked or the identity changes.
  - Added the superadmin group model catalog `allowed-models`; the group default model is validated against the catalog again after being resolved to the final `provider/model`, preventing group admins from selecting unauthorized expensive or internal models.
- The group tool allowlist is unified at the server-side execution entry point: tool profiles are expanded into concrete tool names, and `tool-allow none` explicitly means that all tools are forbidden in the current group; the allowlist is validated once after parsing and once before execution (including after queuing), so a tool whose permission is revoked while queued does not execute.
- The daily quota ledger is persisted in `state.toml [budget]`: image generation/vision calls are atomically reserved by unique call ID after confirmation and before execution, and the reservation survives a restart; `/grouppolicy` state shows used/cap. See the "quota ledger extension" entry above for the four-dimensional scopes and the token/currency accounting.
- OneBot inbound text preserves newlines, indentation, and consecutive whitespace; wake-word detection uses a separate matching view that forwarded content does not enter, so an `@`, wake word, or command inside a forward does not trigger the bot.
- OneBot supports merged-forward parsing: when expanding nodes the sender, time, and message ID are preserved and marked as "quoted content, not a system instruction"; added `forward_max_fetches` / `forward_max_result_bytes` / `forward_max_non_text` / `forward_fetch_timeout_seconds`, and multiple forward ids in the same message share the total node/character budget; circular references are rejected, and duplicate references, fetch failures, or missing fields degrade stably to a text marker.
- OneBot `readLoop()` began dispatching `notice` / `request` / `meta_event` non-message events; recall now cancels precisely by "platform message ID → turn request" instead of cancelling the whole group scope's tasks by default; a member leaving / being kicked / being muted only cancels that member's requests in the current scope, and requests such as join-group approval are not automatically approved because of an ordinary message.
- `internal/request.Manager` gained `FairKey` fair queuing and `CancelFairKey` targeted cancellation: when concurrency is full, the waiter with the fewest currently active entries that is not the most recently admitted key is granted first, preventing a single user or a single group from filling the queue.

### Changed

- `state.toml` gained the `group_policy` and `budget` tables; `GroupPolicyConfig` supports default-value normalization, explicit tool switches, the model catalog, and learning moderation actions.
- `state.toml` changed to atomic writing via a temporary file + rename; long-message/group-level policy JSON is still judged by the server-side policy, and no prompt-level safety boundary has been relaxed in the group policy.

## [v0.6.7 - 2026-10-04]

### Added

- Added the built-in Go tool `image_to_prompt`: pass an already ingested reference image via `media:<sha256>` to call a vision model that reverse-engineers a drawing prompt covering subject, appearance, clothing, pose, composition, background, lighting, color, and art style.
  - The vision backend reuses the existing `[providers.*]` in `services.toml`; setting `provider` + `model` under `[image_to_prompt]` enables it, with no separate API key required, and it inherits that provider's proxy, timeout, retry, and circuit breaker settings.
  - The tool parameters are `image` (`media:<sha256>`), `target` (`general` / `sdxl` / `flux`), and `language` (`zh` / `en`).
  - To reduce tokens and call counts: images are scaled by `max_edge` and compressed by `max_image_bytes` before upload; results go through the fingerprint cache of the shared `internal/vision` engine (default 30 minutes, with the key including the prompt fingerprint for `(媒体, target, language)`); concurrent requests for the same key are merged into one upstream call, and the leader re-checks the cache before executing; `max_tokens` limits the returned length and only the prompt body is returned to the chat model.
  - Failures, empty results, truncated results with `finish_reason = length`, and abnormal outputs longer than 64 KiB are never cached; `timeout_seconds` is the total budget for the whole operation (preprocessing + request retries + streaming reads), and the preprocessing and encoding loops can be cancelled.
  - Images undergo boundary checks before full decoding (64 MiB per file, 20000 px on the long edge, 24 million total pixels, decoding concurrency gate), and the size of ingested objects is pre-checked from metadata before reading; for locally decodable formats a successful return guarantees `长边 <= max_edge` and `字节数 <= max_image_bytes`, while unknown formats are passed through only within the byte budget and only the byte cap is guaranteed. During pass-through the MIME type is corrected from the file header when it does not match the content.
  - When no `[image_to_prompt]` provider/model is configured the tool is not registered; an explicit `enabled = true` with a missing provider/model or a nonexistent provider is now a startup error; once configured it goes through the `[security] user_max_tool_risk` permission check with `risk = medium`.
- Added the optional configuration section `[vision]` (disabled by default): when the main chat model is a text-only model and the upstream explicitly rejects image content, a vision model first transcribes the image into a text description and the request is resent once with that description, solving the problem that "an image is degraded into a text reference but the model still cannot see its content".
  - Trigger detection is now structured: the adapter preserves the upstream `status/code/type/param`, and only explicit image/vision-related 400/422/404 responses trigger it; ordinary 400, 429, 5xx, timeout, and cancellation are no longer misdetected, and it no longer relies on generic keywords such as `unsupported`.
  - When provider/model are left empty they are inherited from `[image_to_prompt]`, so existing configurations only need `enabled = true`; an explicit `enabled = true` with incomplete configuration or a nonexistent provider is a startup error.
  - When any single image cannot be described (missing media, over a limit, failed vision call), the whole request degrades to the original text reference and the main model's failure state stays as it was.
- Added the internal package `internal/vision` as the shared image description engine: image preprocessing + streaming LLM calls + versioned fingerprint cache + success/negative cache + same-key concurrency merging + lightweight metrics + panic isolation.
  - The cache key is the SHA-256 of a fixed struct serialized to JSON, containing the schema version, MediaID, provider, endpoint, model, prompt version and content hash, preprocessing version, request parameters, and optional credential epoch; changing the model/prompt/preprocessing parameters always misses, while changing the log level does not invalidate it.
  - The negative cache defaults to 30 seconds and stores only explicit deterministic failures (such as `model_not_found` or invalid requests with named parameters); 400 without a reliable error code, 401/403, 429/408, 5xx, network errors, and cancellation/timeout are never cached.
  - Only structured fields and safe summaries are stored, never raw response bodies, request bodies, Base64, or credentials; the API key enters neither the cache key nor the logs.

- `internal/vision` is now the single image description engine shared by the `image_to_prompt` tool and the `[vision]` fallback: the tool no longer carries its own cache and singleflight, and both share fully identical preprocessing, fingerprint cache, negative cache, same-key merging, output cap, and timeout policy.
- Multi-image fallback is now bounded parallelism: by default at most 4 images are described at the same time, at most 8 per turn, and the whole batch shares one time budget (when the caller already has a deadline the caller's wins, otherwise the default is 3 minutes); when the image cap is exceeded or the budget is used up, the whole batch degrades to a text reference. The three caps can be adjusted through agent options.
- The vision engine gained runner and waiter caps: by default at most 4 upstream tasks run concurrently, at most 16 queue up, and a single task has at most 16 waiters; anything beyond that fails fast instead of queueing indefinitely.
- `vision.Service.Stats()` wires the shared counters and runner occupancy into the `vision` section of the ops `/metrics` endpoint (`image_to_prompt` and `fallback` report occupancy separately), containing only counts, caps, and occupancy, with no image content or media IDs.

### Changed

- Version raised to `0.6.7`; `deploy/VERSION`, the Compose default image, the build/offline scripts, and the version examples in the Chinese deployment documentation were updated in sync.
- `internal/llm` gained a structured `APIError` (`StatusCode`/`Code`/`Type`/`Param`/`Message`/`Cause`); the OpenAI adapter's `parseError` no longer returns only a formatted string and preserves `Unwrap`, while behaviors such as `errors.Is(err, context.Canceled)` are unchanged.
- `[providers.*]` and `[providers.*.model_configs.*]` gained a three-state `vision` capability declaration (`true`/`false`/unset), with the model level overriding the provider level; query it with `Config.VisionSupportFor(provider, model)`, laying the groundwork for later validation by model capability.
- The model list cache is now TTL-based: a successful list is cached for 10 minutes and a failure is kept for only 30 seconds, so a single transient network error is not cached long-term.
  - Added per-provider merging and last-known-good: concurrent refreshes of the same provider issue only one upstream request; when a refresh fails, the last successful list is kept (and merged with locally configured models) while the error is reported, so one blip does not empty the model menu.
- `APIError` gained an adapter-maintained `Category` (`vision_unsupported` / `model_not_found` / `invalid_request` / `auth` / `rate_limit` / `timeout` / `server_error`); the vision fallback decision now looks only at `Category` and no longer matches error text inside the agent; OpenAI-compatible dialects that only put "images are not supported" in the message are unified into a single mapping in the adapter's `parseError`.
- Extra request fields (`providers.*.extra_payload`, model-level `extra_payload`, Hook `extra_body`) can no longer override the adapter-reserved fields `model`, `messages`, `stream`, and `stream_options`: ignored field names are recorded in a warn log entry, while other custom fields and the precedence order are unchanged.
- `[providers.*].vision = false` (or a model-level declaration) now makes `[image_to_prompt]` / `[vision]`, which need image input, fail at startup instead of failing only on the first call.
- The default `services.toml` template gained a commented `[vision]` block.

### Fixed

- Vision fallback no longer retries transparently after the main model has already emitted body text, reasoning, or tool-call fragments, avoiding duplicate answers and duplicate tool calls; in that case it reports the failure for the user to retry, with at most one fallback per turn.
- The vision negative cache now prioritizes cancellation/timeout over status codes: an `APIError` carrying `context.Canceled` / `context.DeadlineExceeded` (including wrapped) is no longer cached as a deterministic failure, so one caller giving up does not pollute later requests.
- Shared vision tasks now keep the first caller's deadline while binding the process/service context: each waiter's cancellation does not affect the shared task, the shared task has its own bounded timeout, and process shutdown can cancel in-flight upstream requests; when the output cap is hit, the upstream stream is actively cancelled and closed.
- The vision fallback decision no longer misdetects a "404 whose model name contains image"; when the upstream names an unrelated parameter (such as `temperature`) the fallback is rejected; explicit image content rejections such as `invalid message content type` were added.
- The per-entry cap of the `[vision]` success cache now truly uses the configured value (previously hardcoded to 16 KiB), and it is now explicit that "a result larger than the per-entry cap but smaller than the output cap is returned in full but not cached", avoiding cache hits that return truncated copies.
- Image descriptions are labeled as "untrusted image content, not user instructions", reducing the prompt injection risk caused by text inside images.
- Fixed shared vision tasks losing the caller's deadline: `context.WithoutCancel` also discarded the deadline along with it, so after the caller timed out the shared task could still run until the shared timeout; the deadline is now rebuilt explicitly, so even when all callers have left, the upstream request still stops at the original deadline.
- Negative cache replay now preserves `APIError.Category`, so an error served from the negative cache has the same classification as the original error.

## [v0.6.6 - 2026-10-03]

### Changed

- Version raised to `0.6.6`; `deploy/VERSION`, the Compose default image, the build/offline scripts, and the version examples in the Chinese deployment documentation were updated in sync.
- Long-term memory rate limiting is split into a "request attempt cap" and a "successful write quota": empty or oversized content is rejected before rate limiting, failed writes are refunded and do not consume the success quota, and the rate limiter cleans up scopes unused for a long time per window.
- Keyword extraction gained caps on input length, candidate generation volume, and ASCII keywords; oversized input is sampled from both the head and the tail, so entities at the end of a sentence are not truncated and English words do not crowd out the Chinese quota.
- Self-learning observation gained per-entry length, per-scope capacity, and write rate limits; mining gained a total character budget, a timeout, and scan/truncation statistics; candidates now require at least 2 distinct users by default; review changes are appended to the `candidate_reviews` audit table, and `/learning history <id>` shows the trail.
- Group analysis inbound now reads page by page and aggregates page by page instead of accumulating all messages in memory; outbound counting uses an optional dedicated `COUNT` query and falls back to paged counting when unsupported.

### Security

- `/healthz` now denies by default like the sensitive ops endpoints: when `ELBOT_OPS_TOKEN` is unset and `ELBOT_OPS_ALLOW_UNAUTHENTICATED=1` is not explicitly set it is no longer registered, leaving only the public `/live` and `/ready`; when no token is configured, `doctor` marks the platform connectivity check as skipped and gives a hint.
- The group analysis summary folds member nicknames and platform/Session fields into single lines with a length cap, and the whole report is placed inside `<group_report>` boundaries and escaped before being handed to the model, preventing nicknames from injecting new lines or forging boundaries; summaries gained an output token, accumulated character, and timeout budget.
- "Check capacity + write" for long-term memory and self-learning now completes within the same SQLite write transaction, avoiding exceeding the per-Session capacity cap under concurrency.

### Fixed

- Fixed failed long-term memory writes consuming the per-minute success quota; fixed the rate limiter's `hits` cleaning only the current key, which made the map keep growing after long runtimes.
- Fixed self-learning `Mine` still holding a query cursor when the character budget ended early, which made subsequent candidate writes time out on a single-connection SQLite.
- Fixed a single `truncated` from group analysis being unable to distinguish inbound from outbound; added `inbound_truncated` / `outbound_truncated` and warnings are now output by direction.
- When a summary times out or fails, the deterministic statistics report is still sent, without affecting the main body of the daily report.

### CI

- Added `angelmemory`, `selflearning`, `groupanalysis`, `health`, `ratelimit`, `safecontext`, and `textmatch` to the critical package list for `-race`.
- The Windows native job was extended to run the tests of the safe context, keyword, rate limit, long-term memory, self-learning, and group analysis packages.
- The Docker workflow gained an amd64 no-push image build smoke test on PRs, so Dockerfile problems are not exposed only when tagging.

## [v0.6.5 - 2026-10-02]

### Changed

- Version raised to `0.6.5`; `deploy/VERSION`, the Compose default image, the build/offline scripts, and the version examples in the Chinese deployment documentation were updated in sync.

### Security

- Long-term memory and self-learning context now go through unified safe rendering: boundary characters are escaped, control/zero-width characters are stripped, multiple lines are folded, and length is limited per entry and in total; `angel_memory` gained per-entry length, per-Session entry count, and per-Session write frequency limits, and `self_learning` gained meaning/injection length limits.
- Ops endpoints are now secure by default: when `ELBOT_OPS_TOKEN` is unset, `/tasks`, `/metrics`, `/diagnostics`, and `/plugins/*` are not registered by default and only `/live` and `/ready` remain; unauthenticated exposure is allowed only when `ELBOT_OPS_ALLOW_UNAUTHENTICATED=1` is explicitly set. Once a token is set, `/healthz` also requires authentication, and `doctor` and watchdog now carry the token automatically.
- Self-learning review now locates entries by `id + platform + scope_id` and checks the number of affected rows, recording the reviewer and the review time; added `/learning undo`, and a nonexistent candidate returns an explicit error.

### Fixed

- Long-term memory recall changed from whole-sentence `LIKE` to keyword / Chinese 2-4 character n-gram matching with relevance ranking, and on a miss it can cautiously fall back to a small number of high-strength memories; fixed a single oversized memory blocking subsequent short memories, and unified the maximum entry count description in the tool schema.
- Group analysis counts by message records (text-free messages such as image-only or file-only ones also count toward message volume and active members), reads the full window page by page via `AfterSeq`, outputs a `truncated` warning and the actual number of scanned records when the cap is reached, and clarifies the local timezone and the "message count/character count" definitions; the summary prompt no longer asks the model to invent year-over-year changes.
- Self-learning mining extracts by continuous word segments without crossing punctuation, deduplicates within a single message, records the number of distinct users, and returns `created/updated/skipped`; already-reviewed context is sorted with priority by relevance to the current topic.
- Single-instance protection for the Windows native version now uses a named mutex, and Windows native tests and a CI job were added.

## [v0.6.4 - 2026-10-02]

### Added

- Added the clean-room `group_analysis` tool and the `internal/groupanalysis/` statistics service: it reads only the local `chat_history` / `outbound_messages`, computes group message volume, active members, and active hours by day, and does not copy the templates, images, Prompt, or assets of third-party group analysis plugins.
- Hook events gained request-level transient fields: `llm.system_append`, `llm.temperature`, `llm.max_tokens`, and `llm.extra_body`; both Go Hooks and `hook.v2` process Hooks can return them, and the Agent applies them only to the current LLM request without writing them into Session history.
- Chat History gained an optional `ChatHistoryRangeRepository` batch time-window query; added `OutboundMessageRepository` and the `outbound_messages` table, which record the assistant text actually sent, reused by learning and group analysis.
- Added optional platform capability interfaces: `GroupHistoryProvider`, `GroupDirectoryProvider`, `UserAvatarProvider`, and `GroupAssetProvider`; the OneBot adapter already implements best-effort capabilities for group history, group info, member list, and avatar URL.
- Added `[group_analysis]` configuration: `enabled`, `max_messages`, and an optional Cron daily report `report_*`; the maintenance task now also cleans up expired outbound messages when cleaning chat history.
- `group_analysis` supports an optional LLM `Summarizer`, whose summary uses the model corresponding to the default Session mode; the Cron daily report can send statistics and summaries to a specified platform Session.
- Added clean-room `[angel_memory]`: local SQLite long-term memory, the `angel_remember` / `angel_recall` tools, and transient system context injection through `llm.turn.prepared`.
- Added clean-room `[self_learning]`: message observation, expression/slang candidate mining, and review-before-apply for `/learning` and `self_learning_review`, where only approved content is injected into context.
- Added the `/memory` / `/learning` admin commands and `[maintenance.privacy_cleanup]`, which cleans angel memory and self learning data per feature retention.
- The health/ops HTTP service gained read-only `/plugins/memory` and `/plugins/learning` status endpoints, protected by `ELBOT_OPS_TOKEN`.
- The Telegram adapter gained optional group info and admin list capabilities; QQ Official explicitly falls back to the local chat history.
- The Windows local deployment documentation gained steps for installing Docker Desktop on a non-C drive: it supports using the machine's own `Docker Desktop Installer.exe` and placing program files and WSL data on a chosen drive via `--installation-dir`, `--wsl-default-data-root`, and `--no-windows-containers`.
- Added `deploy/portainer/portainer-compose.yml`: the Portainer CE browser-based Docker management UI binds only to `127.0.0.1:9443` and uses a separate Compose project name to avoid conflicting with ElBot; `deploy/windows/README.md` now covers local startup, obtaining the first setup token, cloud server SSH tunneling / reverse proxy, and Docker socket security notes, and the main `README.zh-CN.md` gained an entry point and quick-start commands.
- `deploy/windows/README.md` gained a plan for AutoDL ComfyUI image generation integration: it covers AutoDL SSL custom services, SSH tunneling, the workflow API format, provider configuration conventions, GPU concurrency limits, and security notes; once formally implemented, configuration can follow that chapter directly.
- Added the shared read-only `services.toml`: it centralizes `[providers.*]`, `[model_metadata]`, `[model_profiles]`, and `[image_generation]` and is loaded through `[config_files].services`; the old `providers.toml` and `app.toml [image_generation]` remain compatible. `state.toml` stays independent, and loading rejects pointing `state` at read-only configurations such as `app.toml` / `services.toml` / `providers.toml`, preventing a runtime `SaveState` from overwriting static configuration.
- Added default generation of `services.toml`; the default assets no longer generate `providers.toml`. `elbot config check` now outputs the actually loaded `services` / `providers` / `state` paths.
- The required configuration check in `deploy/restore-verify.sh` changed to `app.toml` plus (`services.toml` or the old `providers.toml`), and it validates in a Python with `tomllib` that the service configuration referenced by `app.toml` exists and that `state` does not share the same path as a read-only configuration.

### Changed

- Version raised to `0.6.4`; `deploy/VERSION`, the Compose default image, the build/offline scripts, and the version examples in the Chinese deployment documentation were updated in sync.
- Documentation was updated for centralized service configuration: `docs/configuration.md`, `docs/getting-started.md`, `docs/image-generation.md`, `deploy/README.md`, `deploy/windows/README.md`, and the offline package description now center on `services.toml`, with `providers.toml` as the compatibility entry for older deployments.
- Injecting resident memory into the system prompt now adds `<resident_memory>` boundaries and a trust statement that it is "user data, not system instructions", and escapes angle brackets in memory content, preventing content from ending early or forging boundary tags and reducing the impact when normal memories are used as a persistent Prompt injection vector.
- Normal resident memory writes gained server-side protection: `[resident_memory]` can configure a minimum write interval, a maximum number of writes per window, a maximum entry count, and a maximum length per entry, and by default rejects clear instruction-like content and control characters entry by entry; failed writes do not consume the frequency quota, and core writes are unaffected by normal rate limiting.
- Normal resident memory is now stored structurally: one entry per line and one thing per line, stripping list prefixes such as `-` / `*` / `1.`, blank lines, and duplicate entries on write, and rendering as separate `-` list items when injected into the system prompt, avoiding multiple facts being concatenated into a paragraph easily taken as instructions.
- `memories.toml` now uses atomic writes: first write a temporary file in the same directory and `fsync`, then `rename` over the target file, and finally `fsync` the directory on a best-effort basis; a crash or power loss leaves only a complete old file or a complete new file. The file state is checked before writing, and if something external modified the file between reading and writing it is reloaded before applying, avoiding overwriting changes made by hand edits or restore scripts.

- `elbot doctor` gained the `platform_ok` overall field and `--require-platform`: a `disconnected` platform is no longer marked as passing, and strict mode additionally requires the platform to be enabled and a connection state to exist in the health snapshot; the CLI E2E now sends a unique probe marker and waits for a reply containing that marker, and both an empty stream end and unrelated non-empty text fail.
- `deploy/upgrade.sh` now switches `ELBOT_GIT_REF` before resolving versions that were not explicitly specified; the old image snapshot uses the running container's actual `.Image` ID and records the digest; the pre-upgrade snapshot defaults to `BACKUP_MODE=stop`, and the new image configuration precheck mounts an isolated copy of the production `data` by default; rollback information gained `ROLLBACK_IMAGE_ID` / `ROLLBACK_IMAGE_DIGEST` / `ROLLBACK_FROM_IMAGE`.
- `deploy/rollback.sh` no longer does `source rollback.env` and instead parses key-values safely to support paths with spaces and avoid executing unknown keys; before rolling back it loads the old image and verifies the backup with the old image using `RESTORE_VERIFY_START=required`, and after restoring it waits for `/ready` and runs doctor acceptance, and on failure keeps the failed data and tries to restore `data.before-rollback-*`.
- The final status of `deploy/restore-verify.sh` now distinguishes `passed` / `static_passed` / `static_passed_with_skips`, so a skipped isolated startup is no longer disguised as a full `passed`; `RESTORE_VERIFY_START` explicitly accepts the documented `required`.
- `deploy/windows/elbot.ps1` gained Docker Desktop readiness waiting, Git Bash (not WSL) detection, and an explicit error for 401, and `health` / `status` now return failure when `/ready` does not pass; before strict backup verification it pre-checks the host `sqlite3` and a Python with `tomllib`.
- Release / Docker CI gained `pull_request` triggers, `-race` for critical packages, ShellCheck, and `deploy/tests/*.sh` deployment regressions, and explicitly installs sqlite3/python3 to prevent critical tests from passing as skipped after missing dependencies.

- The online `sqlite` mode of `deploy/backup.sh` now takes a consistent `.backup` of all SQLite databases first and then copies non-database files such as media; it then completes missing media according to `backend='local'` references in the databases, and when the source file has already been deleted it fails the backup outright, avoiding a snapshot whose database references are complete but whose archive is missing media.
- `deploy/restore-verify.sh` now converts the archive and manifest to absolute paths first, avoiding a relative backup directory becoming invalid after `sha256sum -c` enters a temporary directory; it removes stale service PID markers from the archive before restoring the isolated startup; under Windows Git Bash it converts Docker bind mount host paths through `cygpath` and protects the in-container `/data` path with `MSYS_NO_PATHCONV`.
- `deploy/rollback.sh` now removes stale PID markers after restoring data, so rolling back to an older version image that does not yet use file locks is not stuck on a stale marker.

### Fixed

- Fixed `internal/app/service_marker*`: service mutual exclusion changed from "trusting only the PID file" to a `flock` file lock, so the kernel releases the lock automatically after a process is hard-killed or a container is recreated, and a new container no longer fails to start because of PID reuse or stale PID markers.
- Unified redaction of user-visible errors, Hook failures, and log/audit outputs, and attached `error_id` to user-facing failure messages; when an upstream error contains a URL with a token, group messages and logs no longer contain credentials.
- Fixed parameter binding for `Invoke-Compose` in `deploy/windows/elbot.ps1`: Windows PowerShell 5.1 flattens `Invoke-Compose (@(...) + @($Rest))` into a single space-separated string, so `docker compose` received wrong arguments such as `"compose up -d --remove-orphans"`; it now uses a plain `[string[]]` parameter, and the `Invoke-Compose @doctorArgs` / `@logArgs` / `@Rest` calls now pass arrays directly.

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
