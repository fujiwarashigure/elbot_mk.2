## Unreleased

### Added

- 新增仅超级管理员可用的 `/doctor`：只读检查当前配置的加载错误与缺失项、`app.toml` 未知字段（拼写错误不再被静默忽略）、内置 Skill 文件与默认版本的缺失/差异，逐条列出；无问题回复 `Everything is OK`，不修改任何文件、不调用模型。
- QQ OneBot 超长回复（超过 3000 rune）从“拆成多条消息”改为**一条合并转发**：每页一个 `node`，用 `send_group_forward_msg` / `send_private_forward_msg` 发送，分页不再附加 `……（N/M）` 标记；引用回复里的合并转发会展开一层内容并保留“转发内容为引用”标记。
- 新增文件回滚：`edit_file` 每次成功写入前在进程内保留该文件上一版内容（每个文件只保留最近一次，按 Session 隔离，上限 1024 条 / 256 MiB，超出淘汰最旧），新增仅超级管理员可用的 `/rollback [编号]` 与内置工具 `rollback_file`（发现 `read_file` / `edit_file` 时自动展开，可用 `@tool:rollback_file` 预载）。回滚会校验文件自编辑后未被改动，Shell 或外部程序的修改一律拒绝覆盖并提示未回滚；新建文件被回滚时直接删除；记录在切换 Session、重启进程或容量淘汰后失效。
- 新增 `view_image` 内置工具：把图片本身交给模型，而不是只给地址。`source` 传媒体 ID、HTTP(S) URL 或本地路径（本地路径仅超级管理员，读取敏感文件走高风险确认），或用 `message_id`（可带 `#`）+ `media_index` 取当前聊天历史里的图片，省略 `media_index` 时取每条消息的首张图片；单次最多 5 次未入库媒体下载尝试，失败只返回文本提示，不回显上游错误。
- 工具新增按模型能力可用：`tool.Info.VisionRequired` + 请求上下文里的 `tool.Capabilities`。声明 `vision = false` 的 Provider 或模型（`[providers.*].vision` / `model_configs.<model>.vision`）在当前 work Session 中不会发现、预载或执行 `view_image`；未声明能力（`VisionUnknown`）时保持原有行为。`view_image` 是第一个使用该机制的工具。
- OneBot 入站消息新增平台级去重：按 `平台 + 机器人 self_id + scope + message_id` 记录 `processing` / `completed` / `failed`，TTL 内重连重放不会再次唤醒模型或重复计费；去重状态独立于 `history` 开关，并有 `inbound_dedup_max_entries` 硬上限。
- OneBot 新增有界预处理通道：`@` 解析、引用拉取和合并转发展开在 `preprocess_workers` / `preprocess_queue_size` 限定的 worker 池中执行，队列满时普通消息本地拒绝而不是创建无界 goroutine；撤回、成员和管理事件走独立的高优先级 worker / 队列，满时在读循环内联处理，不被普通聊天流量堵塞。
- 新增可选聊天硬预算模式 `[budget_limits].chat_hard_limit`：在现有 token/费用账本上增加“调用前原子预占、返回 usage 后结算释放差额”的路径。并发请求不能同时穿透剩余额度；上游缺失 usage 时按预占量保守记账并写入 `budget.uncertain`；启用费用硬限制但模型缺价格时明确拒绝；取消/超时/重启后的未结算预占保持保守占用，不自动退款。
- 新增群运行状态机并持久化到 `state.toml [group_runtime]`：机器人自身被禁言/踢出时，当前群进入 `muted` / `removed`，服务端立即取消该群在途请求、拒绝新的模型调用，并丢弃发往该群的定时/后台通知；解除禁言或重新入群后恢复 `active`。状态变化只向超管发送一次聚合通知，重复事件不再重复通知，暂停期间的通知不集中补发。
- 新增群会话线程模式：`/*grouppolicy thread-mode group` 可让同一群内所有成员共享一个 Session，普通消息按到达顺序串行执行，每条用户消息带服务端生成的发言成员标记与 `speakers` 元数据，避免多成员输入互相覆盖；开启不迁移旧的每人独立 Session，共享会话的切换/修改命令限群主/群管理员/超管。
- 新增连续消息合并窗口 `/*grouppolicy merge-window <0-10000>`：窗口内同一成员的连续消息会合并为同一个 turn，不同成员仍保持串行且不会共享权限主体；基础队列在请求进入模型前完成合并，撤回/成员退群会同步丢弃尚未运行的队列项，并有每 Session 队列上限避免无限积压。
- 新增本地确定性群知识库 / FAQ：`[group_knowledge]` 提供全局开关和上限，`/*faq add` / `add-contains` / `add-keyword` / `remove` / `clear` / `test` / `on` / `off` 提供管理入口；条目按 `平台 + 群 scope` 写入 `state.toml [group_knowledge]`，命中后由服务端直接回答，不创建 Session、不调用 LLM、不消耗 chat token，并继续受唤醒、静默时段、入站限流和群运行状态约束。
- 新增普通成员自助面板 `/*me [tasks|quota|all]`：按当前 actor 的 FairKey 展示自己的进行中请求、排队消息、当前会话阶段，以及生图/视觉/聊天 token/费用的本群和本人额度；隐藏跨群全局聚合用量，只读且不调用模型。`/*requests` 仍保留全局/管理视角。
- 新增群内确定性提醒 / 投票 / 报名：`[group_services]` 提供全局开关与上限，`/*grouppolicy services on|off` 提供群级开关；提醒支持相对时间、时刻和日期时间并在本地调度发送，投票支持创建/改票/关闭和结果统计，报名支持容量、加入/退出/关闭。全部状态写入 `state.toml [group_services]`，不调用模型、不消耗 chat token，并受群运行状态约束。
- 长期记忆新增来源关联与删除入口：`angel_memory` 写入时记录来源类型、来源用户、平台消息 ID 和 Session ID；`/memory list|show|delete|source` 提供超管管理入口，`/forget list|<id>|source <消息id>` 提供普通成员可用的删除入口，群聊中普通成员只能删除来源成员为自己的记忆，群主/群管理员/超级管理员可删除当前群 scope 的任意记忆。`/forget resident normal|core|all [--confirm]` 可清空自己的常驻记忆。`[angel_memory].forget_on_recall` 默认 `true`，平台消息撤回时按来源消息 ID 删除派生记忆；`/delete` 删除 Session 时也会按 `source_session_id` 清理该 Session 写入的记忆，旧版没有结构化来源列的记忆不会匹配，避免误删。
- `state.toml` 支持外部编辑热加载：进程每 15 秒按 mtime 检测一次外部修改并合并生效，新增 `/*state` 查看加载状态和未生效修改、`/*state reload` 立即生效；生效后会记录 `runtime_state_reloaded` 审计事件，并在确实有变更时给超级管理员发送一条通知。每次内部写回前会先合并外部修改，手工编辑不再需要重启，也不会被内部写回直接覆盖。热加载覆盖 `mode_models`、`compact_model`、`naming_model`、`context_overflow`、`group_policy`、`group_knowledge`、`group_services`、`group_runtime`；`[budget]` 额度账本仍由运行中的进程独占，只在启动时从文件恢复，避免丢掉在途预占。
- 新增可选模型删除长期记忆工具 `angel_forget`（`[angel_memory].allow_tool_forget`，默认关闭）：只能删除来源成员为当前发言人、且属于当前平台/会话 scope 的单条记忆，别人的记忆、其他 scope 和旧版没有来源成员的记忆一律拒绝；风险等级 `high` 会进入工具确认流程（`/*detail`、`/*confirm`、`/*reject`），工具自身还要求 `confirm=true` 二次调用，首次调用只返回待删除内容；同一范围内每分钟最多删除 3 条。该工具为隐藏工具，通过 `angel_recall` 的依赖注入模型，批量删除仍走 `/memory delete` 与 `/forget`。
- 新增 `/memory backfill` 旧记忆来源迁移入口：旧版长期记忆只写自由文本 `source = "tool"`，没有结构化来源列，因此可确定地回填 `source_kind`（预检需要 `--confirm` 才执行），并明确输出无法回填的来源成员/消息 ID/Session ID 条数。这些旧记忆继续按“不匹配、不误删”处理：不会被 `/forget source`、撤回清理和 `/delete` Session 命中，也不会被 `angel_forget` 删除。
- 新增可选语音转写 `[asr]`：复用已有 `[providers.*]` 的 OpenAI 兼容 `/audio/transcriptions` 端点，被唤醒的语音/录音消息先转写为 `[语音 N 自动转写（可能有误）：...]` 文本段，再进入聊天、工具和上下文流程；未开启或转写失败时保留原 `[语音]` 引用。provider/model 可用 `audio = true/false` 声明能力，全局 `[asr].enabled`、群策略 `asr on|off` 和四级每日额度 `asr-quota` / `user-asr-quota` / `global_asr_daily` / `user_asr_daily` 共同约束；单条消息按 `max_segments` / `max_concurrent` 有界并行，录音读取受 `max_audio_bytes` 限制，成功转写按 MediaID 缓存、确定性 4xx 进入短负缓存。
- 新增 Responses 协议接入：`[providers.<name>].api_mode`（`chat` 默认 / `response`）和 `[providers.<name>.model_configs.<model>].api_mode` 让同一个 Provider 的模型分别走 `POST {base_url}/chat/completions` 或 `POST {base_url}/responses`，未配置时行为与之前完全一致；非法值在启动阶段报错而不是静默回退。Responses 请求把 system 段合并为 `instructions`、把工具调用与工具结果转成 `function_call` / `function_call_output` item、把工具定义扁平化，并固定带 `store = false`（该字段不保留，可用 `extra_payload` 覆盖）；流式事件（`response.output_text.delta`、reasoning 文本、`function_call_arguments.*`、`response.completed` / `failed` / `error`）归一化回现有 `llm.StreamChunk`，文本、reasoning、工具调用 delta、usage 与错误分类（含 `vision_unsupported`）语义不变，因此熔断、健康状态、视觉兜底、用量统计和 `/model` 列表不需要感知协议差异。Provider 混用两种协议时由 `internal/app/models.go` 的路由客户端按请求模型分发，`ListModels` / `ListModelMetadata` 仍走协议无关的 `/models`。
- 后台运行中的 Session（cron / Elnis）可被前台接管：此前 `/resume` 或 `/unarchive` 进入一个正在跑的后台 Session 只是把它设为当前会话，在途的后台 turn 仍把它当自己的任务 Session 继续调用模型、写入助手消息并投递汇报。现在前台激活会在**同一个 SQLite 事务**内把它提升为普通前台 Session：metadata 写入 `foreground_origin`（记录接管前的 `background_kind` / owner / platform / scope 以便诊断）、删除 `background_kind`、切到当前前台 scope 并置 `Mode = work`；`session.IsBackground` / `session.WasPromoted` 是两个判定入口，`canAccess` 也用 `IsBackground` 放行后台 Session 的跨 scope 恢复。在途后台 turn 改为**安全点轮询**：每次模型调用前、工具批次落库后与最终输出前各查一次接管标记，命中即停止（不再发起下一次模型调用、不写迟到的助手回答、不发布 PhaseError），由 `RunBackground` 归一成 `RunResult{TakenOver: true, Outcome: "taken_over"}` 且 `err == nil`。cron 侧把被接管的运行记为 `ReportReady=true, TaskCompleted=true, Report=""`（`deliverPrepared` 因此不发任何文本），Elnis 侧把事件收成 `StatusCompleted` 且不准备汇报，两边各留一条审计事件（`cron.background_taken_over` / `elnis.background_taken_over`）；已提升的 Session 在 `RunBackground` 入口就短路，一次模型请求都不会发出。这是 fork 原生最小版：不引入上游的 `session.Binding` / `executionCoordinator`，接管后的会话不会像上游那样在请求尾部合成一条 user 消息。

### Fixed

- 修复 `state.toml` 并发写回互相覆盖：`saveRuntimeState` 此前只用一个保护 `stateModTime` 的互斥锁，而"重新读文件合并外部修改 → 取内存快照 → 替换文件"这一整段是并发执行的，两个写入者（例如同时切换模型与写预算账本）会各自用自己读到的旧文件覆盖对方刚写入的分片，表现为随机丢字段；mtime 门在同一时间戳精度内也看不出差别。现在新增 `stateWriteMu` 串行化整个合并-快照-落盘过程，22 处调用点（分布在 8 个文件：预算账本、上下文溢出、群策略、群知识、群服务、群运行时、模型切换）全部经过它，并有并发写入的回归测试守住"每个写入者的分片都不丢、文件始终完整可解析"。
- 修复同一 Session 的并发写入互相丢字段：此前工具发现/预载、`workspace_dir`、上下文用量、压缩种子、后台会话 metadata、重命名/归档/置顶、恢复与心跳、模式激活、空闲过期和自动命名各自把手上读到的一份 Session 快照整行写回，两个写入重叠时后写的一方会把对方刚改的字段（例如预载工具时覆盖掉刚设置的 `workspace_dir`，或心跳覆盖掉工具缓存）一起回滚。`storage.SessionRepository` 新增 `Mutate(ctx, id, update)`：在单个 SQLite 事务里读取当前行、执行回调、写回并返回最新行；上述写路径全部改为在事务内基于最新行合并自己的字段，`mutateSessionMetadata` / `mutateMetadata` 只改动已知 metadata 键、保留未知键。
- 审计日志恢复"事实可区分"：此前预载与发现路径把四类完全不同的判定压成同一个自由字符串 `reason="not_found_or_not_allowed"`（注册表里没有、当前上下文不可用、角色/策略拒绝、隐藏工具），事后无法区分策略拒绝与工具不存在。现在 `internal/tool` 提供唯一的判定入口（`ToolAvailabilityReason` / `ToolAccessReason` / `RegistryToolAvailabilityReason`），按互斥原因输出 `tool_not_found` / `tool_context_unavailable` / `tool_hidden` / `tool_requires_superadmin` / `tool_risk_above_allowed_level` / `tool_no_schema`，判定顺序与既有 `CanAccessTool` / `InfoAvailableInContext` 完全一致（有等价性测试固定），`background_preload_skipped` / `tool_preload_skipped` / `skill_wrapper_preload_skipped` 三类事件都带上新原因与 `result`；策略与角色拒绝记为 `rejected`，其余记为 `skipped`。
- 统一 Hook 审计事件命名：`hook.tool_call`、`hook.platform_call` 改为 `hook_tool_call`、`hook_platform_call`，与 `hook_tool_call`、`hook_tool_error` 同属一套下划线命名，`/audit --event hook_tool_call` 不再漏掉 Hook 运行时桥接的工具调用；`hook_platform_call` 改为按真实调用结果记录（成功 `result=succeeded`、失败带 `error` 与 `result=failed`），不再只看"发起过调用"。
- 审计属性里的错误对象统一转成脱敏字符串：此前部分调用点直接传 `error`（如 `skill_preload_failed`），输出取决于动态类型，可能变成无法按字段查询的结构。
- 修复两处依赖过期快照的竞态：空闲过期现在在事务内重新判定 Session 是否仍然闲置，迟到的自动命名在事务内重新检查 `title_renamed`，不再覆盖刚完成的手动改名；后台会话创建也不再整行回写，metadata 合并与模式设置在同一事务内完成。
- 修复后台任务的工具白名单可被绕过：此前 `toolrun.Resolve` 在缓存未命中时回落到全局 registry，后台任务按名字仍能解析并执行未预载的工具（包括本应禁止的 `discover_tool`）；现在后台上下文不再回落，`discover_tool` / `workspace` 一律不可用，`Schemas` 也按同一白名单过滤，外部声明的 `tool_cache` 在写入后台会话前先过滤。
- 修复工具缓存重建时丢失 `ForegroundOnly` 标记：从会话 metadata 的 `discovered_tools` 重建 `toolrun.CachedTool` 时现在带上 `ForegroundOnly`，前台专用工具不会再进入后台会话。
- 修复模型重试只在用户通知里可见的问题：重试现在同时写一条运行日志（`event=model_retry`，含 provider、次数、延迟与脱敏错误），服务模式或通知目标被屏蔽时也能在 `/log` 里看到。
- 修复一次 Hook 失败被记录两次的问题：Hook 失败统一由 Hook 管理器记录（含规则、point 与 error），Agent 侧不再重复写同一条 `hook error`；平台连接通知的发送失败改为独立的 `hook notice send failed` 记录。
- 修复审计日志被运行时等级过滤的问题：`audit-*.log` 独立保留下限（至少 info），`log_level=warn/error` 时用量事实与命名失败诊断不再丢失。
- 修复模型重试与命名的诊断缺失：专门命名模型失败此前被完全静默吞掉，现在记录 `event=session_naming_failed`（含 provider/model 与脱敏错误）；命名失败同时写入审计事件 `session_naming_failed`。
- 文档型 AgentSkill 的详情此前向所有角色都追加 `agent_skill_creator` 引导；现在只有超级管理员可见：`tool.LazyDetailProvider.LoadDetail` 与 `Registry.DiscoverDetails` 改为透传带 actor 的 context，`discover_tool`、`@skill:` 预载与后台预载三条路径都显式传入 actor，普通用户或身份缺失时不再出现该引导（`ELBOT_SKILL.toml` 无效提示仍对所有角色保留）。
- 修复 `/stop` 的越权：此前任何用户都能用 `/stop <request_id>` 或 `/requests` 显示的编号停止进程内其它会话的请求，Tab 补全也会把所有人的 request ID 列出来。现在普通用户的可停止集合、编号解析和补全都限定在自己当前 Session 的请求，只有超级管理员保留全局视角；`/requests`、`/stopall` 仍为超级管理员专用。
- 修复唤醒词、工具/技能/角色指令与命令续接剥离时把整条消息的全部文字段合并成一段并插到首个文字段位置的问题；现在只改写实际变化的文字区间，段间的图片与文件段保持原位置和原始顺序。
- 修复 `shell` 执行期间内存随输出增长的问题：此前用无界 `bytes.Buffer` 缓存 stdout/stderr、命令结束后才截断；现在收集时即只保留前 16 KiB 前缀并继续排空丢弃剩余输出，截断时在末尾标注，返回上限不变。
- 修复压缩期间仍会预载工具的问题：此前压缩中收到的消息虽然被拒绝回复，但 `@tool:` / `@skill:` 剥离与 `tool_cache` 写入已经完成；现在压缩准入位于输入 Hook 与指令处理之前，被拒绝的消息不再预载或写入工具缓存。
- 修复 chat 模式仍可能执行工具的问题：Hook 在 `llm.turn.prepared` / `llm.request.prepared` / `llm.response.received` 注入的工具 schema 或模型返回的 tool call 此前都会进入工具轮次；现在 chat 会话在这三个 Hook 之后都会清空工具与工具调用，`executeToolCalls` 与 `toolrun.Run` 也会拒绝 chat 会话。
- 长回复分包与部分发送追踪覆盖主要平台：OneBot `sendContextText()` 在后续分页失败时返回带 `Failed` / `Failure` 的部分回执；QQ 官方新增按 rune 分页并保留后续页失败回执，Markdown 已有页面可见时不再回退纯文本造成重复发送；Telegram 的 HTML→纯文本、rich→HTML、流式最终替换以及 OneBot/Telegram/QQ 官方的多输出、多目标发送循环都会合并已成功页/目标的回执并标记部分失败。`delivery.Manager` 新增 `SendNoticesWithReceipt`，Agent 批量输出部分失败时会在审计日志中留下平台消息数量，方便对账而不是重发整批。

### Changed

- 新增 fork 版日志契约（`internal/logging/contract.go`）：登记合法的来源标识（`module`：app / agent / session / hook / platform / delivery / cron / elnis / model / storage / media / maintenance / tool）与操作结果（`result`：succeeded / failed / canceled / rejected / skipped），并提供 `ValidLogModule` / `ValidLogResult`。所有 Agent 审计记录现在都带 `module=agent`；此前只有 Hook 侧写 `module=hook`，而 `/log --hook`、`/audit --hook` 依赖这个字段，Agent 侧却没有任何写入端约束。新增来源必须先登记，避免出现第三套命名。日志文件格式与 `LogEntry.Fields` 查询契约本轮不变：上游的九字段 `LogRecord` 与 JSONL 落盘未实施。
- `/resume` 与 `/fork` 返回的历史消息预览现在每条最多保留 200 个 Unicode 字符（超出加 `...`），单条长消息不再整段进入回复；`/messages` 的 40 字预览不变。
- `/log --hook` 与 `/audit --hook` 改为筛选 `module=hook` 记录，不再使用并不存在的 `event=hook`；`--hook` 也不再覆盖同一命令里已经给出的 `--event` 筛选。Hook 运行日志与 Hook 审计事件现在统一带 `module=hook`。
- 清理 v0.6.8 里“额度账本不含 token/货币与每用户/全局预算”的阶段性旧描述，改为与四维 token/费用账本一致的能力说明。
- 命令前缀默认值由 `/*` 改为 `/`：`internal/command` 的 `defaultCommandPrefix`、`internal/agent` 与 `internal/platform` 的兜底前缀、`internal/config` 的运行时默认值和内置 `app.toml` 模板，以及 `deploy/data/config/elbot/app.toml` 全部切到 `/`；仓库内 `docs/*.md` 的命令示例也同步替换为 `/`（只保留 `notes/*.md`、`/plugins/*` 这类真正的通配符写法）。已有部署若在 `app.toml` 显式保留 `prefixes = ["/*"]` 则行为不变，配置里出现了什么前缀就仍按什么前缀解析。`docs.en/`、`README.md`、`README.zh-CN.md` 以及本文件的历史条目未同步替换。
- `image_to_prompt`、`image_generate`、`angel_remember` 三个内置工具的风险等级由 `medium` 降为 `low`，在默认 `[security] user_max_tool_risk = "low"` 下普通用户即可调用，不再需要整体放宽工具风险上限。`image_generate` 仍额外受 `[image_generation] superadmin_only` 约束，只有该开关为 `false` 时才真正对普通用户开放；两个生图相关工具和长期记忆写入都建议同时配置群级额度（`image-quota` / `user-image-quota` / `vision-quota` / `user-vision-quota`）。

## [v0.6.8 - 2026-10-04]

### Added

- 版本号提升到 `0.6.8`；`deploy/VERSION`、Compose 默认镜像、构建/离线脚本和中文部署文档中的版本示例同步更新。
- `get_forward_msg` 增加 websocket 读取阶段限制：`forward_max_result_bytes` 现在在读取途中生效，超限响应不会先完整读入再截断。
- 额度账本扩展为群 / 群内单用户 / 全局 / 全局单用户四维，生图/视觉按调用次数、chat 按 token/费用分别计量；新增 `[budget_limits]`、群策略 `user-image-quota` / `user-vision-quota` / `chat-tokens-quota` / `chat-cost-quota`，并记录 `budget.tokens` / `budget.costs`。
- 新增工具执行幂等账本 `budget.executions`：同一 scope + actor + 工具调用 ID 只允许一个参数摘要首次执行，重放被抑制、ID 参数不一致被拒绝；provider 重试单独写入 `budget.retries`。
- 新增按 provider 的实际调用并发上限 `provider_max_concurrent` / `provider_queue_max_size` / `provider_wait_timeout_seconds`；请求队列补充队满、超时、取消、平均等待和最老等待指标。
- 群分析摘要模型现在也在执行前按当前群模型目录重新授权；生图模型继续由全局 image generation 配置、群工具白名单和生图额度独立约束。
- 新增统一历史写入门 `internal/historygate`：所有适配器的 `chat_history` / `outbound_messages` 写入都经过可信 `平台 + scope` 策略判断；`history=off` 时同时停止入站、助手、工具转录、媒体关联、摘要/命名和工具结果预览的新增落盘，当前轮仍在内存中处理，旧记录不删除。
- `learning=off` 现在覆盖完整学习生命周期：观察 hook、挖掘、审核、撤销、历史查询和上下文注入均在服务层再次检查策略；关闭后不再产生新候选，也不会继续使用已有知识注入模型。
- `state.toml` 改为 fsync + 备份替换的安全写入，移除 Windows 下 rename 失败直接覆盖的回退；启动时可从 `state.toml.bak` 恢复中断的替换。额度账本新增参数摘要校验，同一调用 ID 重放相同参数不重复计数，参数不同则拒绝；账本无法可靠落盘时受限调用会失败而不是继续执行。
- 撤回/取消新增晚到输出闸门：turn 终止状态会检查流式 flush、工具结果交回 Agent、最终消息进入发送队列前；provider 或工具忽略 `context.Cancel` 并返回晚到结果时也不会继续发送新消息。
- 群模型目录现在覆盖实际执行的模型目标：turn hook、cron override、压缩模型和视觉 fallback 在调用 provider 前重新授权；`discover_tool` 结果同时受当前群白名单过滤器、执行前二次授权和解析后工具名校验约束。
- 请求队列新增 `queue_max_per_user` / `queue_max_per_scope` 容量上限，避免单个用户或单个群无限排队；公平调度继续使用等待时间兜底。
- 新增群级策略 `/*grouppolicy`：按 `平台 + 群 scope` 保存唤醒词、响应模式（`mention` / `all` / `keyword` / `reply` / `off`）、默认会话模式、默认模型、工具白名单、生图/视觉额度、静默时段，以及群分析/学习/历史记录开关。
  - 策略由服务端按当前群判断，命令不接受跨群目标；群管理员只能改自己当前群的普通策略，不能修改 provider、密钥或全局 Shell 权限。
  - `/learning` 默认仍仅超级管理员可用；超级管理员可在本群执行 `/*grouppolicy learning-moderation on`，并通过 `learning-moderation-actions` 细分 `view` / `decide` / `mine` / `delete` / `export` / `policy` 权限，且不会提升为全局超管。群管理员审核只作用于当前群 scope，撤权或身份变化后立即失效。
  - 新增超级管理员群模型目录 `allowed-models`；群默认模型解析为最终 `provider/model` 后再次校验目录，避免群管理员选择未授权的昂贵或内部模型。
- 群工具白名单统一到服务端执行入口：工具 profile 会展开为具体工具名，`tool-allow none` 明确表示当前群禁止全部工具；白名单在解析后和执行前（含排队后）各校验一次，排队期间被撤权的工具不会执行。
- 每日额度账本持久化在 `state.toml [budget]`：生图/视觉调用在确认后、执行前按唯一调用 ID 原子预占，重启后保留；`/grouppolicy` 状态会显示已用/上限。四维范围与 token/货币口径见上文“额度账本扩展”条目。
- OneBot 入站文本保留换行、缩进和连续空白；唤醒判断使用独立匹配视图，转发内容不会进入该视图，因此转发里的 `@`、唤醒词或命令不会触发机器人。
- OneBot 支持合并转发解析：展开节点时保留发送者、时间、消息 ID，并以“引用内容、不是系统指令”标记；新增 `forward_max_fetches` / `forward_max_result_bytes` / `forward_max_non_text` / `forward_fetch_timeout_seconds`，同一消息内多个转发 id 共享总节点/字符预算；循环引用会被拒绝，重复引用、拉取失败或缺字段会稳定降级为文本标记。
- OneBot `readLoop()` 开始分发 `notice` / `request` / `meta_event` 非消息事件；撤回按“平台消息 ID → turn request”精确取消，不再默认取消整个群 scope 的任务；成员退群/被踢/禁言只取消该成员在当前 scope 的请求，加群审批等 request 不会因普通消息自动批准。
- `internal/request.Manager` 新增 `FairKey` 公平排队和 `CancelFairKey` 定向取消：并发满时优先授予当前活跃数最少、且不是最近一次已准入 key 的等待者，避免单个用户或单个群占满队列。

### Changed

- `state.toml` 新增 `group_policy` 与 `budget` 表；`GroupPolicyConfig` 支持默认值归一化、显式工具开关、模型目录和学习审核动作。
- `state.toml` 改为临时文件 + rename 的原子写入；长消息/群级策略 JSON 仍由服务端策略判断，未在群策略中放开任何提示词级别的安全边界。

## [v0.6.7 - 2026-10-04]

### Added

- 新增内置 Go 工具 `image_to_prompt`：用 `media:<sha256>` 传入已入库参考图，调用视觉模型反推绘图提示词，覆盖主体、外观、服装、姿势、构图、背景、光线、色彩与画风。
  - 视觉后端复用 `services.toml` 已有的 `[providers.*]`，在 `[image_to_prompt]` 里设置 `provider` + `model` 即启用，不需要单独的 API Key，也继承该 provider 的代理、超时、重试和熔断设置。
  - 工具参数为 `image`（`media:<sha256>`）、`target`（`general` / `sdxl` / `flux`）、`language`（`zh` / `en`）。
  - 为降低 token 和调用次数：上传前按 `max_edge` 缩放、按 `max_image_bytes` 压缩；结果由共享的 `internal/vision` 引擎做指纹缓存（默认 30 分钟，键含 `(媒体, target, language)` 对应的提示词指纹）；同一 key 的并发请求合并为一次上游调用，leader 执行前二次查缓存；`max_tokens` 限制返回长度，只把提示词正文回给聊天模型。
  - 失败、空结果、`finish_reason = length` 的截断结果，以及超过 64 KiB 的异常长输出都不写缓存；`timeout_seconds` 作为整次操作（预处理 + 请求重试 + 流式读取）的总预算，预处理和编码循环可被取消。
  - 图片在完整解码前先做边界检查（单文件 64 MiB、长边 20000 px、总像素 2400 万、并发解码闸），读取入库对象前按元数据预检大小；对本地可解码格式保证成功返回时 `长边 <= max_edge` 且 `字节数 <= max_image_bytes`，未知格式仅在字节预算内透传且只保证字节上限。透传时会按文件头纠正与内容不符的 MIME。
  - 未配置 `[image_to_prompt]` provider/model 时不注册该工具；显式 `enabled = true` 却缺少 provider/model 或 provider 不存在时改为启动报错；配置后按 `risk = medium` 走 `[security] user_max_tool_risk` 权限判断。
- 新增可选配置段 `[vision]`（默认关闭）：主聊天模型是纯文本模型、上游明确拒绝图片内容时，先用视觉模型把图片转写成文字描述，再用描述重发一次请求，解决“图片被降级成文本引用但模型仍然看不到内容”的问题。
  - 触发判定改为结构化：适配器保留上游 `status/code/type/param`，只有明确的图片/视觉相关 400/422/404 才触发；普通 400、429、5xx、超时、取消都不再误判，也不再依赖 `unsupported` 这类泛化关键词。
  - provider/model 留空时自动继承 `[image_to_prompt]`，已有配置只需加 `enabled = true`；显式 `enabled = true` 但配置不完整或 provider 不存在时启动报错。
  - 任一张图片无法描述（媒体缺失、超限、视觉调用失败）时整体降级为原来的文本引用，主模型失败状态保持原样。
- 新增内部包 `internal/vision` 作为共享的图片描述引擎：图片预处理 + LLM 流式调用 + 版本化指纹缓存 + 成功/负缓存 + 同 key 并发合并 + 轻量指标 + panic 隔离。
  - 缓存键是对固定结构体 JSON 后的 SHA-256，包含 schema 版本、MediaID、provider、endpoint、model、提示词版本与内容哈希、预处理版本、请求参数和可选凭据纪元；改模型/提示词/预处理参数必定 miss，改日志级别不失效。
  - 负缓存默认 30 秒，只存明确的确定性失败（如 `model_not_found`、命名参数的非法请求）；400 无可靠错误码、401/403、429/408、5xx、网络错误、取消超时一律不缓存。
  - 只保存结构化字段与安全摘要，不保存原始响应体、请求 body、Base64 或凭据；API Key 不进入缓存键或日志。

- `internal/vision` 现在是 `image_to_prompt` 工具与 `[vision]` 兜底共用的唯一图片描述引擎：工具不再自带缓存和 singleflight，两者的预处理、指纹缓存、负缓存、同 key 合并、输出上限与超时策略完全一致。
- 多图兜底改为有界并行：默认最多同时描述 4 张图、单轮最多 8 张、整批共享一个时间预算（调用方已有 deadline 时以调用方为准，否则默认 3 分钟）；超过张数上限或预算用尽时整体降级为文本引用。三个上限可通过 agent options 调整。
- 视觉引擎新增运行器与等待者上限：默认同时最多 4 个上游任务、最多排队 16 个、同一任务最多 16 个等待者，超出直接快速失败而不是无限排队。
- `vision.Service.Stats()` 把共享计数器和运行器占用接入运维 `/metrics` 的 `vision` 段（`image_to_prompt` 与 `fallback` 分别上报占用），只包含计数、上限与占用，不含图片内容或媒体 ID。

### Changed

- 版本号提升到 `0.6.7`；`deploy/VERSION`、Compose 默认镜像、构建/离线脚本和中文部署文档中的版本示例同步更新。
- `internal/llm` 新增结构化 `APIError`（`StatusCode`/`Code`/`Type`/`Param`/`Message`/`Cause`），OpenAI 适配器的 `parseError` 不再只返回格式化字符串，并保留 `Unwrap`，`errors.Is(err, context.Canceled)` 等行为不变。
- `[providers.*]` 与 `[providers.*.model_configs.*]` 新增三态 `vision` 能力声明（`true`/`false`/未设置），model 级覆盖 provider 级；用 `Config.VisionSupportFor(provider, model)` 查询，为后续按模型能力校验打基础。
- 模型列表缓存改为带 TTL：成功列表缓存 10 分钟，失败只保留 30 秒，避免一次临时网络错误被长期缓存。
  - 新增 per-provider 合并与 last-known-good：并发刷新同一 provider 只发一次上游请求；刷新失败时保留上一次成功列表（并与本地配置的模型合并）同时上报错误，避免一次抖动清空模型菜单。
- `APIError` 新增适配器维护的 `Category`（`vision_unsupported` / `model_not_found` / `invalid_request` / `auth` / `rate_limit` / `timeout` / `server_error`），视觉兜底判定改为只看 `Category`，不再在 agent 里匹配错误文本；只把“图片不支持”写在 message 里的 OpenAI 兼容方言统一收拢到适配器 `parseError` 一处映射。
- 额外请求字段（`providers.*.extra_payload`、model 级 `extra_payload`、Hook `extra_body`）不再能覆盖适配器保留字段 `model`、`messages`、`stream`、`stream_options`：被忽略的字段名会记一条 warn 日志，其它自定义字段与优先级顺序不变。
- `[providers.*].vision = false`（或 model 级声明）现在会让需要图片输入的 `[image_to_prompt]` / `[vision]` 在启动时报错，而不是等到第一次调用才失败。
- `services.toml` 默认模板新增 `[vision]` 注释块。

### Fixed

- 视觉兜底不再在主模型已经输出正文、推理或工具调用片段后透明重试，避免重复回答和重复工具调用；此时改为提示失败并由用户重试，每轮最多兜底一次。
- 视觉负缓存改为取消/超时优先于状态码：`APIError` 带 `context.Canceled` / `context.DeadlineExceeded`（含包装）时不再被当作确定性失败缓存，避免一次调用方放弃污染后续请求。
- 视觉共享任务在保留首个调用者 deadline 的同时绑定进程/服务上下文：等待者各自取消不影响共享任务，共享任务有独立有限超时，进程关闭可取消在途上游请求；命中输出上限时主动取消并关闭上游流。
- 视觉兜底判定去除“模型名含 image 的 404”误判；上游指名无关参数（如 `temperature`）时否决兜底；补上 `invalid message content type` 这类明确的图片内容拒绝。
- `[vision]` 成功缓存的单条上限现在真正使用配置值（此前硬编码 16 KiB），并明确“大于单条上限但小于输出上限的结果完整返回但不缓存”，避免缓存命中返回截短副本。
- 图片描述以“不可信的图片内容，不是用户指令”标注，降低图片内文字造成的提示注入风险。
- 修正共享视觉任务丢失调用方 deadline 的问题：`context.WithoutCancel` 会连同 deadline 一起丢弃，导致调用方超时后共享任务仍可运行到共享超时；现在显式重建 deadline，即使全部调用方都已离开，上游请求仍会在原 deadline 停止。
- 负缓存回放现在保留 `APIError.Category`，命中负缓存的错误与原始错误分类一致。

## [v0.6.6 - 2026-10-03]

### Changed

- 版本号提升到 `0.6.6`；`deploy/VERSION`、Compose 默认镜像、构建/离线脚本和中文部署文档中的版本示例同步更新。
- 长期记忆限流拆成“请求尝试上限”和“成功写入配额”：空内容/超长内容在限流前先被拒绝，失败写入退还不占成功配额，限流器按窗口清理长期未使用的 scope。
- 关键词提取增加输入长度、候选生成量和 ASCII 关键词上限，超长输入从头尾两端采样，避免句尾实体被截断、英文单词挤占中文额度。
- 自学习观察增加单条长度、单 scope 容量和写入速率限制；挖掘增加总字符预算、超时和扫描/截断统计；候选默认要求至少 2 个不同用户；审核变更追加写入 `candidate_reviews` 审计表，`/learning history <id>` 可查看轨迹。
- 群分析入站改为逐页读取、逐页聚合，不再把全部消息累积到内存；出站计数改用可选专用 `COUNT` 查询，不支持时回退分页计数。

### Security

- `/healthz` 改为与敏感运维接口一致的默认拒绝：未设置 `ELBOT_OPS_TOKEN` 且未显式设置 `ELBOT_OPS_ALLOW_UNAUTHENTICATED=1` 时不再注册，只保留公开的 `/live`、`/ready`；`doctor` 在未配置 token 时会把平台连接检查标为跳过并给出提示。
- 群分析摘要把成员昵称、平台/会话字段折叠为单行并限长，整个报告放进 `<group_report>` 边界并转义后交给模型，防止昵称注入新行或伪造边界；摘要增加输出 token、累积字符和超时预算。
- 长期记忆与自学习的“检查容量 + 写入”改为在同一 SQLite 写事务内完成，避免并发下突破单会话容量上限。

### Fixed

- 修复长期记忆失败写入会消耗每分钟成功配额的问题；修复限流器 `hits` 只清理当前 key、长期运行后 map 持续膨胀的问题。
- 修复自学习 `Mine` 在字符预算提前结束时仍持有查询游标，导致单连接 SQLite 上后续候选写入超时的问题。
- 修复群分析单个 `truncated` 无法区分入站/出站的问题，新增 `inbound_truncated` / `outbound_truncated` 并按方向输出警告。
- 摘要超时或失败时继续发送确定性统计报告，不影响日报主体。

### CI

- `-race` 关键包列表补充 `angelmemory`、`selflearning`、`groupanalysis`、`health`、`ratelimit`、`safecontext`、`textmatch`。
- Windows 原生 job 扩展为运行安全上下文、关键词、限流、长期记忆、自主学习和群分析包的测试。
- Docker workflow 在 PR 上新增 amd64 不推送镜像构建 smoke test，避免 Dockerfile 问题拖到打标签才暴露。

## [v0.6.5 - 2026-10-02]

### Changed

- 版本号提升到 `0.6.5`；`deploy/VERSION`、Compose 默认镜像、构建/离线脚本和中文部署文档中的版本示例同步更新。

### Security

- 长期记忆与自学习上下文统一走安全渲染：转义边界字符、剥离控制/零宽字符、折叠多行，并按条和总量限制长度；`angel_memory` 增加单条长度、单会话条数和每会话写入频率限制，`self_learning` 增加含义/注入长度限制。
- 运维接口改为安全默认：未设置 `ELBOT_OPS_TOKEN` 时默认不注册 `/tasks`、`/metrics`、`/diagnostics`、`/plugins/*`，只保留 `/live`、`/ready`；只有显式设置 `ELBOT_OPS_ALLOW_UNAUTHENTICATED=1` 才允许无鉴权暴露。设置 token 后 `/healthz` 也需要鉴权，`doctor` 与 watchdog 已自动携带 token。
- 自学习审核改为按 `id + platform + scope_id` 定位并检查影响行数，记录审核人和审核时间；新增 `/learning undo`，不存在的候选返回明确错误。

### Fixed

- 长期记忆召回从整句 `LIKE` 改为关键词 / 中文 2-4 字 n-gram 匹配与相关度排序，未命中时可谨慎回退到少量高强度记忆；修复单条超长记忆会挡住后续短记忆的问题，并统一工具 schema 的最大条数说明。
- 群分析按消息记录计数（纯图片/文件等无文本消息也计入消息量和活跃成员），按 `AfterSeq` 分页读取完整窗口，达到上限时输出 `truncated` 警告和实际扫描条数，并明确本地时区与“消息数/字数”口径；摘要提示词不再要求模型编造同比变化。
- 自学习挖掘按连续词段提取、不跨标点，单条消息内去重，记录不同用户数并返回 `created/updated/skipped`；已审核上下文按当前话题相关度优先排序。
- Windows 原生版单实例保护改为命名互斥体，并增加 Windows 原生测试与 CI job。

## [v0.6.4 - 2026-10-02]

### Added

- 新增 clean-room `group_analysis` 工具与 `internal/groupanalysis/` 统计服务：只读取本地 `chat_history` / `outbound_messages`，按天统计群消息量、活跃成员和活跃时段，不复制第三方群分析插件的模板、图片、Prompt 或素材。
- Hook 事件新增请求级临时字段：`llm.system_append`、`llm.temperature`、`llm.max_tokens`、`llm.extra_body`；Go Hook 与 `hook.v2` 进程 Hook 均可返回，Agent 只作用于当前 LLM 请求，不写入 Session 历史。
- Chat History 新增可选 `ChatHistoryRangeRepository` 批量时间窗查询；新增 `OutboundMessageRepository` 和 `outbound_messages` 表，记录实际发送的 assistant 文本，供学习和群分析复用。
- 新增可选平台能力接口：`GroupHistoryProvider`、`GroupDirectoryProvider`、`UserAvatarProvider`、`GroupAssetProvider`；OneBot 适配器已实现群历史、群信息、成员列表和头像 URL 的 best-effort 能力。
- 新增 `[group_analysis]` 配置：`enabled`、`max_messages` 和可选 Cron 日报 `report_*`；维护任务清理 chat history 时会同步清理过期 outbound messages。
- `group_analysis` 支持可选 LLM `Summarizer`，摘要使用默认 Session 模式对应模型；Cron 日报可发送统计和摘要到指定平台会话。
- 新增 clean-room `[angel_memory]`：本地 SQLite 长期记忆、`angel_remember` / `angel_recall` 工具，以及 `llm.turn.prepared` 临时 system 上下文注入。
- 新增 clean-room `[self_learning]`：消息观察、表达/黑话候选挖掘、`/learning` 与 `self_learning_review` 的 review-before-apply，只有 approved 内容会注入上下文。
- 新增 `/memory` / `/learning` 管理命令，新增 `[maintenance.privacy_cleanup]` 按功能 retention 清理 angel memory 与 self learning 数据。
- 健康/运维 HTTP 服务新增只读 `/plugins/memory` 和 `/plugins/learning` 状态接口，受 `ELBOT_OPS_TOKEN` 保护。
- Telegram 适配器新增可选群信息和管理员列表能力；QQ Official 明确以本地 chat history 回退。
- Windows 本地部署文档新增 Docker Desktop 非 C 盘安装步骤：支持使用本机 `Docker Desktop Installer.exe`，通过 `--installation-dir`、`--wsl-default-data-root`、`--no-windows-containers` 将程序文件和 WSL 数据放到指定盘。
- 新增 `deploy/portainer/portainer-compose.yml`：Portainer CE 浏览器 Docker 管理界面只绑定 `127.0.0.1:9443`，使用独立 Compose 项目名避免与 ElBot 冲突；`deploy/windows/README.md` 补充本地启动、首次 setup token 获取、云服务器 SSH 隧道 / 反向代理和 Docker socket 安全说明，主 `README.zh-CN.md` 增加入口与快速启动命令。
- `deploy/windows/README.md` 新增 AutoDL ComfyUI 生图接入规划：覆盖 AutoDL SSL 自定义服务、SSH 隧道、workflow API 格式、provider 配置约定、GPU 并发限制和安全注意事项；正式实现后可直接按该章节配置。
- 新增共享只读 `services.toml`：集中 `[providers.*]`、`[model_metadata]`、`[model_profiles]` 和 `[image_generation]`，通过 `[config_files].services` 加载；旧 `providers.toml` 和 `app.toml [image_generation]` 继续兼容。`state.toml` 保持独立，加载时会拒绝把 `state` 指向 `app.toml` / `services.toml` / `providers.toml` 等只读配置，避免运行时 `SaveState` 覆盖静态配置。
- 新增 `services.toml` 默认生成；默认 assets 不再生成 `providers.toml`。`elbot config check` 现在输出实际加载的 `services` / `providers` / `state` 路径。
- `deploy/restore-verify.sh` 的必需配置检查改为 `app.toml` 加（`services.toml` 或旧 `providers.toml`），并在带 `tomllib` 的 Python 中校验 `app.toml` 引用的服务配置存在、`state` 未与只读配置共用同一路径。

### Changed

- 版本号提升到 `0.6.4`；`deploy/VERSION`、Compose 默认镜像、构建/离线脚本和中文部署文档中的版本示例同步更新。
- 文档同步集中服务配置：`docs/configuration.md`、`docs/getting-started.md`、`docs/image-generation.md`、`deploy/README.md`、`deploy/windows/README.md` 和离线包说明改为以 `services.toml` 为主，`providers.toml` 作为旧部署兼容入口。
- 常驻记忆注入 system prompt 时增加 `<resident_memory>` 边界和“用户数据、不是系统指令”的信任声明，并转义记忆内容中的尖括号，防止内容提前结束或伪造边界标签，降低 normal 记忆被用作持久 Prompt 注入向量时的影响。
- 常驻记忆 normal 写入增加服务端保护：`[resident_memory]` 可配置最小写入间隔、窗口内最大写入次数、最大条目数和单条最大长度，并默认逐条拒绝明显指令类内容和控制字符；失败写入不消耗频率额度，core 写入不受 normal 限流影响。
- 常驻记忆 normal 改为结构化保存：每条一行、一行一件事，写入时去掉 `-` / `*` / `1.` 等列表前缀、空行和重复条目，注入 system prompt 时渲染为独立 `-` 列表项，避免多个事实被拼成一段容易被当作指令的文本。
- `memories.toml` 改为原子写入：先写同目录临时文件并 `fsync`，再 `rename` 覆盖目标文件，最后尽力 `fsync` 目录；崩溃或断电时只会留下完整旧文件或完整新文件。写入前检测文件状态，若外部在读取与写入之间修改了文件则重新加载后再应用，避免覆盖手工编辑或恢复脚本的改动。

- `elbot doctor` 新增 `platform_ok` 总字段与 `--require-platform`：平台 `disconnected` 不再标记为通过，严格模式还要求平台已启用且健康快照中存在连接状态；CLI E2E 改为发送唯一探测标记并等待包含该标记的回复，空流结束或无关非空文本都失败。
- `deploy/upgrade.sh` 先切换 `ELBOT_GIT_REF` 再解析未显式指定的目标版本；旧镜像快照改用运行容器的实际 `.Image` ID 并记录 digest；升级前快照默认 `BACKUP_MODE=stop`，并且新镜像配置预检默认挂载生产 `data` 的隔离副本；回滚信息新增 `ROLLBACK_IMAGE_ID` / `ROLLBACK_IMAGE_DIGEST` / `ROLLBACK_FROM_IMAGE`。
- `deploy/rollback.sh` 不再 `source rollback.env`，改为安全解析键值以支持空格路径并避免执行未知键；回滚前先载入旧镜像并用旧镜像 `RESTORE_VERIFY_START=required` 验证备份，恢复后等待 `/ready`、运行 doctor 验收，失败时保留失败数据并尝试恢复 `data.before-rollback-*`。
- `deploy/restore-verify.sh` 的最终状态改为区分 `passed` / `static_passed` / `static_passed_with_skips`，隔离启动跳过时不再伪装成完整 `passed`；`RESTORE_VERIFY_START` 明确接受文档里的 `required`。
- `deploy/windows/elbot.ps1` 增加 Docker Desktop 就绪等待、Git Bash（非 WSL）识别、401 明确报错，并让 `health` / `status` 在 `/ready` 未通过时返回失败；严格备份验证前预检宿主 `sqlite3` 与带 `tomllib` 的 Python。
- Release / Docker CI 增加 `pull_request` 触发、关键包 `-race`、ShellCheck 与 `deploy/tests/*.sh` 部署回归，并显式安装 sqlite3/python3 防止关键测试缺依赖后以 skip 通过。

- `deploy/backup.sh` 的在线 `sqlite` 模式改为先对所有 SQLite 做一致性 `.backup`，再复制媒体等非数据库文件；随后按数据库中的 `backend='local'` 引用补齐缺失媒体，源文件已被删除时直接判定备份失败，避免生成数据库引用完整但归档缺媒体的快照。
- `deploy/restore-verify.sh` 先把归档和 manifest 转成绝对路径，避免相对备份目录在 `sha256sum -c` 进入临时目录后失效；恢复隔离启动前移除归档里的旧服务 PID 标记；Windows Git Bash 下通过 `cygpath` 转换 Docker bind mount 宿主路径，并用 `MSYS_NO_PATHCONV` 保护容器内 `/data` 路径。
- `deploy/rollback.sh` 在恢复数据后移除旧 PID 标记，保证回滚到尚未使用文件锁的旧版本镜像时不会被陈旧标记卡住。

### Fixed

- 修复 `internal/app/service_marker*`：服务互斥从“只信 PID 文件”改为 `flock` 文件锁，进程被硬杀或容器重建后锁会由内核自动释放，不再因 PID 复用/陈旧 PID 标记导致新容器启动失败。
- 统一用户可见错误、Hook 失败和日志/audit 出口的脱敏，并为用户端失败消息附 `error_id`；上游错误包含带 token 的 URL 时，群消息和日志不再包含凭据。
- 修复 `deploy/windows/elbot.ps1` 的 `Invoke-Compose` 参数绑定：Windows PowerShell 5.1 会把 `Invoke-Compose (@(...) + @($Rest))` 折叠成单个带空格字符串，导致 `docker compose` 收到 `"compose up -d --remove-orphans"` 这类错误参数；现在改为普通 `[string[]]` 参数，并把 `Invoke-Compose @doctorArgs` / `@logArgs` / `@Rest` 调用改为直接传数组。

## [v0.6.3 - 2026-10-01]

### Added

- 新增本地 Windows 容器部署：`deploy/windows/elbot.ps1` 与 `elbot.cmd` 复用现有 `deploy/docker-compose.yml`、`Dockerfile` 和 `.env`，支持 `init`、`start`、`recreate`、`stop`、`down`、`restart`、`logs`、`shell`、`compose` 等后台控制，并可注册 `ElBot-Docker` 登录自启计划任务。
- 新增 Windows 命令行运行状态查看：`status` / `health` 读取内置 `/live`、`/ready`、`/healthz`；`tasks` / `metrics` / `diagnostics` 读取 `/tasks`、`/metrics`、`/diagnostics` 并自动携带 `ELBOT_OPS_TOKEN`；`doctor` 在容器内运行同一套 `elbot doctor`。
- 新增 Windows 运维入口：`backup` / `restore-verify` / `upgrade` / `rollback` 复用 `deploy/*.sh`（需要 Git for Windows 的 `bash.exe`），并新增 `deploy/windows/README.md` 完整说明本地部署、排错及与云服务器部署的对照。
- `image_generate` 支持同时索引多个角色：新增 `character_ids`、`reference_images` 和 `count` 参数。默认所有 `@char` / `character_ids` 角色都会写进同一张图；只有显式 `count > 1`（最多 4）时才生成多张，且每张都包含全部角色。多角色参考图以 data URL 数组发送到 `reference_field`。
- 新增发送前长消息保护：按模型窗口、`max_prompt_ratio`、`reserve_output_tokens`、`single_message_max_ratio` 估算 prompt 预算。超限时默认 `reject`，不调用模型并在群里返回报警；可全局或按群设置 `truncate` / `summarize`。新增 `/*overflow` 命令，Bot 超级管理员与当前群群主/管理员可修改 chat/work 策略，覆盖持久化在 `state.toml` 的 `context_overflow`。
- 上下文压缩新增 `[context] user_original_max_runes`（默认 4000）：压缩时每条历史用户原话先限长，避免超长单条消息让摘要自身再次超窗口。
- `deploy/restore-verify.sh` 新增可选隔离启动验收：`RESTORE_VERIFY_START=1`（或 `required`）时使用 `RESTORE_VERIFY_IMAGE` / 当前 `elbot` 容器镜像，以 `--network none` 启动一次性实例并等待 `/ready`；`auto`（默认）在 Docker 和镜像都可用时执行，否则跳过。
- 新增 `deploy/tests/upgrade_script_test.sh`、`deploy/tests/backup_restart_test.sh`、`deploy/tests/restore_verify_start_test.sh`，并扩展 `deploy/tests/watchdog_redaction_test.sh`、`deploy/tests/backup_restore_test.sh` 覆盖上述回归。 新增 `deploy/tests/windows_script_test.sh` 校验 Windows 入口的 BOM、CRLF 包装和版本号引用。

### Changed

- 版本号提升到 `0.6.3`；`deploy/VERSION`、Compose 默认镜像、构建/离线脚本以及所有 README / 部署文档中的版本示例同步更新。
- 仓库 `README.md`、`README.zh-CN.md`、`deploy/README.md` 增加 Windows 本地容器部署章节；`.gitattributes` 新增 `*.ps1` 固定 LF、`*.cmd` 固定 CRLF。

### Fixed

- 修复 `deploy/upgrade.sh` 的 `ELBOT_GIT_REF` 检测：原先假设 `.git` 位于 `deploy/`，会把正常仓库结构 `仓库/.git + 仓库/deploy/upgrade.sh` 误判为非 git 工作区；现在通过 `git -C "$DEPLOY_DIR" rev-parse --show-toplevel` 获取仓库根目录，并在根目录执行 `fetch` / `checkout`。
- 修复 `deploy/upgrade.sh` 用新镜像做配置检查时重复传入 `elbot`：镜像 ENTRYPOINT 已经是 `tini -- /usr/local/bin/elbot`，原命令会展开成 `.../elbot elbot config check`；现在只传 `config check`。
- 修复停机备份后容器恢复失败仍返回成功的问题：`deploy/backup.sh` 现在把 `compose up` 失败或恢复后健康检查超时/异常计入最终退出码，默认最多等待 60 秒（`BACKUP_RESTART_READY_TIMEOUT` 可调）。
- 修复 `deploy/restore-verify.sh` 严格模式仍可能跳过必需检查后输出 `passed` 的问题：缺少带 `tomllib` 的 `python3`、media.local_path 不是 `/data/...` 形式等情况下，严格模式现在直接失败；非严格模式的跳过项会输出 `restore_verify: passed_with_skips`，并分别报告 manifest / toml / database / media_paths / start 的结果。
- 修复 `deploy/elbot-watchdog.sh` 把“重复 sed 幂等”误当成“没有秘密”的复检：现在拆分幂等复检、独立凭据模式检测和当前环境敏感变量值的字面量检测；`mktemp` / `cp` / `sed` 等检查器自身失败会返回非 0，诊断目录从创建起使用 `umask 077`。
- 媒体清理不再在整个清理期间持有全局 `m.objects` 锁，改为按媒体 ID 互斥、最多 4 个对象并发删除；同 ID 导入 / `PresignGet` 也使用同一把对象锁，避免与删除交叉。

## [v0.6.2 - 2026-10-01]

### Fixed

- 修复 `deploy/elbot-watchdog.sh` 的 `redact_diagnostics()`：原先 sed 参数里混入了字面量 `\n` 和控制字节，导致 sed 每次都以 `can't read n` 失败（又被 `|| true` 吞掉），而 `Bearer` / `api_key=` 规则本该写反向引用 `\1` 的地方写成了 0x01 控制字符（脱敏输出会被污染）。现在改为逐文件执行同一组规则，并把 JSON 形式凭据、Telegram bot token、URL userinfo 一并纳入脱敏；新增第二遍复检，脱敏失败会写 `REDACTION-FAILED`、告警并返回非 0，同时提供 `--redact-dir` 手动重脱敏入口和 `deploy/tests/watchdog_redaction_test.sh` 自测。
- 修复 `deploy/backup.sh` 的 `write_manifest()`：同样的字面量 `\n` 会破坏 `find | xargs sha256sum` 流水线且错误被吞；清单现在从**打包好的归档**解压后生成、覆盖归档内全部 `data/` 文件，生成失败直接判定本次备份失败。
- `deploy/backup.sh` 的 `docker compose ps/stop/up` 现在显式带 `-f <compose 文件>` 并在 `deploy/` 下执行（可用 `ELBOT_COMPOSE_FILE` 覆盖）；从 cron 或任意目录调用不会再命中别的 Compose 项目，也不会把运行中的数据当成冷数据打包。
- `deploy/restore-verify.sh` 默认改为严格模式：manifest + sha256sum、SQLite integrity_check 与表结构、`app.toml` / `providers.toml`、TOML 解析（宿主有带 tomllib 的 python3 时）、本地媒体精确路径必须逐项通过，缺依赖或任一失败都不再输出 `passed`；需要降级时用 `RESTORE_VERIFY_STRICT=0`。本地媒体校验从“按文件名查找”改为 `/data/... -> data/...` 精确路径，SQL 查询失败不再回退成 0。
- `deploy/upgrade.sh` 增加版本守卫：目标版本与 `deploy/VERSION` 不一致时拒绝执行，避免“只给当前代码贴新版本号”；需要自动切源码时可用 `ELBOT_GIT_REF=vX.Y.Z`。重建后新增健康检查等待与 `elbot doctor --no-model` 验收，失败即提示回滚命令并以非 0 退出。
- 平台 / 模型的 `last_error` 与最近重启原因在写入健康快照前先做凭据脱敏，避免上游错误里的 token（例如 Telegram bot token 直接出现在请求 URL 中）通过 `/healthz`、`/metrics`、`/diagnostics` 泄露。

### Changed

- `deploy/watchdog.env.example` 新增 `WATCHDOG_READY_ALERT_THRESHOLD` / `WATCHDOG_READY_ALERT_COOLDOWN_SECONDS`：`/live` 正常但 `/ready` 连续失败时只推送一次 `not_ready` 告警（需要配置 webhook），仍然不作为重启条件；watchdog 的重启决策继续只依据 `/live`。
- 版本号提升到 `0.6.2`；`deploy/VERSION`、Compose 默认镜像以及部署/离线文档中的版本示例同步更新。

## [v0.6.1 - 2026-10-01]

### Fixed

- 修复未启用任何平台时只在启动阶段发送一次心跳，导致约 90 秒后 `/live` 过期、watchdog 可能反复重启的问题；空平台模式现在持续发送调度心跳。
- `/live` 改为只表示进程仍在运行；`/ready` 不再因为平台未连接或模型 API 故障而失败，平台和模型状态改由 `/healthz` 的 `degraded` 与状态数组单独展示。调度心跳是否新鲜会作为 `/ready` 的独立检查项。
- 群聊限速从“只命中群级或用户级中的一个”改为先检查用户额度、再检查群级总额度：单个活跃成员无法再耗尽全群配额，群级配额也仍然保护整体资源。
- 新增 `/metrics.rate_limit` 的配置阈值、用户/群级拒绝计数、最近一次限速原因与时间，便于区分“个人刷屏”和“全群过热”。
- 修复诊断包通过 `docker inspect` 写入完整环境变量、可能泄露 Provider API Key / token 的问题；现在只抓容器 State/Ports，并对日志和 JSON 文件做通用凭据脱敏。

### Changed

- Provider 备用策略现在明确区分：默认 `fallback_mode = "circuit"`，仅在熔断打开后切备用；`fallback_mode = "on_error"`（或兼容键 `fallback_on_error = true`）时首个预流式失败请求即可切换。
- 新增 `fallback_timeout_seconds`，可为单次 Provider 尝试设置总超时，避免一次请求被多个上游重试拖得过长。
- Shell / Go Skill 等子进程默认不再继承名字含 `KEY`、`TOKEN`、`SECRET`、`PASSWORD`、`PRIVATE` 的环境变量；Web 搜索、生图和媒体下载等父进程工具仍可读取 `.env` 中的凭据。
- `/tasks` 和 `/metrics` 支持 `ELBOT_OPS_TOKEN`；非回环监听且未配置 token 时启动日志会明确告警。watchdog 可用 `WATCHDOG_OPS_TOKEN` 携带同一 token。
- `deploy/backup.sh` 默认在隔离目录调用新增的 `restore-verify.sh`，验证备份中的 SQLite 完整性、配置文件、角色素材目录和本地媒体引用后才视为成功；`BACKUP_VERIFY=0` 可显式跳过。
- 版本号提升到 `0.6.1`。

### Added

- `/tasks` 新增 `queued_by_kind` 和 `pending_by_kind`，可直接看到受并发上限约束的等待队列与已取得槽位的请求。
- `provider.FallbackTimeoutSeconds` / `fallback_timeout_seconds` 与 `ProviderConfig.UsesFallbackOnError()` 配置入口。
- `deploy/restore-verify.sh`：可独立对任意备份执行隔离恢复验证，不接触生产 `data`。
- `elbot doctor` 部署验收命令：检查配置、健康端口、平台状态、模型调用；加 `--e2e` 后通过 CLI 远程协议做一次真实消息往返，报告区分 `config_ok` 与 `e2e_ok`。
- 新增 `/diagnostics` 聚合诊断接口；`/tasks` 增加排队/超时计数，健康快照增加最近一次重启原因，`/metrics`/`/diagnostics` 可看到熔断、限速和最近重启信息。
- 角色素材元数据新增 `version` / `source`，并提供 `Store.Manifest` / `WriteManifest` 清单能力；备份脚本会为角色和媒体文件生成 sha256 清单，恢复时自动 `sha256sum -c` 校验。
- 新增 `deploy/upgrade.sh` / `deploy/rollback.sh`：升级前做配置检查、数据快照和上一版镜像快照，回滚时先校验数据快照再恢复。


## [v0.6.0 - 2026-10-01]

### Fixed

- 修复 `[ops]` 熔断配置字段缺失（`circuit_breaker_failure_threshold`、`circuit_breaker_open_cooldown_seconds`、`circuit_breaker_half_open_max`）导致 `internal/config` 无法编译的问题。
- 修复 `internal/agent/context_runtime.go` 缺少 `time` 导入、`internal/app/ops_health.go` 缺少限速与生图指标类型，导致整个项目无法编译的问题。
- 修复未启用任何平台时 `elbot service run` 立即以 0 退出：在 Docker / systemd 下会被 `restart: unless-stopped` 反复拉起，健康接口也随进程消失；现在保持存活并输出 warning。
- 修复 `diskguard` / `ratelimit` 单元测试断言错误，`go test ./...` 恢复全绿。

### Changed

- 运行镜像默认保留更多 Agent 常用命令（bash、procps、iputils-ping、dnsutils、netcat-openbsd、iproute2、less、file、tree、tar、gzip、xz-utils、rsync、zip、python3），且默认只增不减。
- `XDG_CACHE_HOME` 指向 `/data/cache`：Go Skill 编译缓存与媒体临时文件落在数据卷上，重建容器后仍可复用。
- `go-runtime` 镜像内编译 Go Skill 不再因 `/go` 无写权限而失败（GOPATH / GOMODCACHE / GOCACHE 均指向 10001 可写目录）。
- 版本号统一由 `deploy/VERSION` 提供，镜像、离线包与文档不再多处硬编码。

### Added

- Docker 构建支持多架构：构建阶段固定在 `$BUILDPLATFORM` 原生交叉编译，打 arm64 不再整段走 QEMU。
- Docker 构建支持国内网络环境：新增 `GOPROXY`、`APT_MIRROR`（自动强制 https）构建参数，可用 `GO_BASE_IMAGE` / `RUNTIME_BASE_IMAGE` 覆盖基础镜像加速或 pin digest。
- 新增离线 / 预编译部署产物：`prepare-offline.sh` 生成 amd64/arm64 静态二进制、`docker load` 直接可用的 scratch 镜像与完整离线包，并附 `SHA256SUMS` 校验。


## [v0.5.0 - 2026-09-28]

### Added

- QQ 官方机器人已支持群聊
- 新增媒体中心，统一管理所有媒体文件

### Changed

- 优化给agent看的meta信息，现在显示平台、群号、群id、用户昵称、用户id
- 现在会压缩超过设置大小的图片；原始媒体和 Media ID 不变。S3 后端改为按需初始化，配置不可用时仅告警，不再阻止 ElBot 启动。
- 工具化 AgentSkill 的命令参数此前只支持字符串、数字和布尔值；现在 JSON 数组与对象会压缩为单个 argv 参数传给对应 `[args]` flag。
- 普通用户的追加重发与高风险工具确认此前会无限等待并长期占用当前 Turn；现在默认在 10 分钟无有效操作后停止，若对应 Session TTL 更短则以其为上限，追加内容或 `/detail` 会续期。超级管理员不受额外的 10 分钟限制，但仍遵守已启用的 Session TTL。
- 多模态图片此前只以 `image_url` 内容段发送，模型能看图却不知道可复用地址；现在每张图片前会派生带消息内序号、名称和 HTTP(S) URL 的用户文本标签，持久化 `content` 与视觉回退使用同一文本投影，`segments` 仍只保存原始结构且无需数据库迁移。
- `/stop`、请求超时或上游取消现在会终止一次性 exec Hook 的完整进程树，避免 Hook 派生的子进程残留；持久 Worker 则收到 `event.cancel` 并继续复用。
- CLI TUI 中不存在的 `#文件` 引用现在按普通文本原样发送，不再阻止消息提交；同一消息中存在的文件仍会正常展开。
- `/help`、详细帮助和 slash 命令补全现在按当前用户权限过滤；普通用户无法发现仅限超级管理员的命令。
- 修改Systemp prompt结构，添加meta信息
- System prompt 的会话 Meta 现在会附带精确到秒的 Session 当地创建时间；该值不会随对话轮次变化，以保持 Prompt 缓存稳定。
- `read_file` 和 `edit_file` 此前会完全拒绝超过 2 MiB 的文本文件；现在 2–100 MiB 文件可继续按行读取、grep 和编辑，仅在实际调用时根据文件大小禁用 AST、完整 diff 和过长确认内容，并保留 revision 校验、预检与原子写入。

### Fixed

- 修复 OpenAI-compatible 上游返回 HTTP 200 HTML/非 SSE 页面时被 Scanner 超长 token 错误掩盖的问题；现在会在流解析前识别异常响应，并只返回有限、脱敏的摘要。
- 修复 Session 闲置过期、执行 `/new` 或切换会话后，引用原 Session 最后一条 assistant 回复会误建新 Session 的问题；现在会自动恢复该 Session，引用较早回复仍会 Fork。
- 修复内置工具与 Go Skill 的服务级环境变量读取不一致，导致部分工具无法读取配置目录 `.env` 的问题。
- 修复 OpenAI-compatible 接口返回非 200 状态时，响应上下文在读取错误 body 前被提前取消，导致真实上游错误丢失并只显示 `failed to read body: context canceled` 的问题。
- 修复读文件工具错误将部分非 UTF-8 文本编码识别为二进制文件的问题。
- 修复elwisp发往qqonebot，文件太大导致全部阻塞的bug
- 修复查询聊天记录查不到纯图片的bug
- 修复日志可能保存base64的bug

## [v0.4.2 - 2026-08-06]

### Changed

- 优化.env、平台secret、token解析逻辑
- `read_file` 读取 `.env`、`.env.*` 及常见凭据文件名时改为高风险确认；其他文件读取仍保持低风险。
- Provider 请求从默认继承进程环境代理改为仅使用 `providers.toml` 中对应 Provider 的显式 `proxy`；未配置或留空时模型列表和聊天请求均直连。
- CLI TUI 通知支持携带日志等级并按 Debug 白、Info 绿、Warn 黄、Error 红显示；远程 CLI 的 `notice` 消息同步新增可选 `level` 字段。

### Fixed

- 修复群聊使用前缀唤起 LLM 后，后续输出 Hook 将已剥离前缀的消息误判为未唤起，导致 `agent.turn.output.prepared` 等默认规则不执行的问题。

## [v0.4.1 - 2026-07-28]

### Changed

- `/new` 与 Session 闲置过期改为仅清除当前会话指针，首条普通消息到来时才创建新 Session；过期历史会话不再立即删除，可直接通过 `/resume` 恢复。
- 更新 Shell 与 Hook 的环境变量继承和分级 `.env` 配置。
- `/hooks` 列表从逐条展示插件规则改为按插件名聚合；使用 `/hooks <插件名>` 时再展开 Worker 状态、全部规则和详情，根规则与内置 Hook 仍单独显示。

### Fixed

- 修复调用者没有当前 Session 时，`/status` 会显示进程内其他用户或 Session 活跃请求的问题；现在该状态只展示 `active requests: none`。
- 修复 QQ OneBot 发送大体积图片等消息时，JSON 编码或 WebSocket 写入长期占住共享发送锁，导致斜杠命令和其他会话的回复一并无响应；OneBot 与远程 CLI 的写入等待现在可取消并有明确超时，失败后 OneBot 会重连。
- 修复普通用户修改自己的高风险 core 常驻记忆时绕过确认；普通用户现在只能在工具权限校验通过后确认 `high`/`critical` 风险调用。

## [v0.4.0 - 2026-07-23]

### Changed

- 无法从模型元数据或配置识别 context window 时，默认窗口调整为 256k。
- `/resume <编号>` 改为直接按最近更新时间恢复非当前 Session，`1` 表示最近一项，不再要求先执行裸 `/resume` 建立编号。
- 上下文压缩改为保留历史用户原话、过滤工具结果，成功后切换到 `原标题 compacted-N` 独立 Session，并将压缩内容与新输入固定物化为首条用户消息；同时修复模型切换、`/stop` 与 Session 变更命令的并发问题。
- Hook Actor 现在同时提供平台昵称、群名片和纯展示名；聊天历史按平台用户 ID 与名称分开保存和搜索。
- Soul 和常驻记忆统一由内置 System Prompt 来源按 turn 构建；常驻记忆不再注册为 Hook，普通 Hook 的 `llm.messages` 明确为只读上下文。
- `workspace` 工具首次被发现或注入时也会加载当前目录的 `AGENTS.md`/`AGENT.md`；同一 Session 的同一路径与切换、重置入口共享一次性记录，不会重复注入。
- 优化 `read_file` 工具，支持目录搜索、搜索结果编号选择，以及按 AST 函数名精确返回完整函数内容及其起止行号。
- 优化system prompt
- AgentSkill 启动扫描不再常驻缓存 `SKILL.md` 正文，只保留摘要与路径；`discover_tool` 按名称发现或 `@skill` 预载时会读取当前正文，带 `ELBOT_SKILL.toml` 的工具化 Skill 同样如此。
- `web_extract` 工具的代理参数从 `disable_proxy` 改为 `proxy`：不填时使用 `WEB_EXTRACT_PROXY` 或系统代理环境，填 `disabled` 禁用代理，填 URL 使用指定代理。
- `send_file` 工具改为使用 `source` 参数发送文件，支持本地路径、`file://` URI 和 HTTP(S) URL，并会按 MIME/扩展名自动将图片作为图片消息发送。
- AgentSkill 不再通过 `python_skill_run` 固定包装执行 Python 脚本；没有 `ELBOT_SKILL.toml` 时保持说明型 Skill，可按文档使用 shell 等通用工具；说明型 AgentSkill 不读取 `SKILL.md` 风险，工具化后以 `ELBOT_SKILL.toml` 的 `risk` 为准。
- Skill 扫描改为启动后延迟执行，并在 `discover_tool` 首次使用时兜底确保扫描，减少启动阻塞。
- Session 闲置过期改为 `[session.idle_expiration]` 四项配置，分别控制群聊/私聊下普通用户和超级管理员的当前 Session 过期时间；默认群聊所有用户过期，私聊超级管理员不过期。
- `shell` 工具移除 `path` 参数，命令默认在当前 workspace 下执行；后台任务仍限制在各自 sandbox 内。
- `read_file`、`edit_file`、`send_file` 的相对路径改为基于当前 workspace 解析；绝对路径仍可临时使用并返回 warning。
- `llm_usage` 审计事件从 debug 级别改为 info 级别，默认 `log_level=info` 即可记录 token 消耗数据。
- QQ OneBot、QQ 官方、Telegram 平台断线重连改为指数退避（3s 起，翻倍，封顶 10s）并日志降级：连续失败只在首次记 warn，恢复后记 info，不再每轮刷屏。
- 平台媒体输出支持在 `path` 中识别 `base64://`、`file://`、`http://`、`https://` 源；普通本地路径仍按平台默认方式处理。
- qq 官方收到图片现在直接使用url而不是base64
- 重构hook，详情见docs。
- Windows 下 `shell` 工具优先使用 `pwsh`，其次 `bash`，最后回退到 `powershell.exe`。

### Fixed

- 修复当前会话仍在请求模型、执行工具或等待确认时仍可切换 Session 的问题；Session 切换命令现在会提示先使用 `/stop` 结束当前处理。
- 修复 Linux service 下进程 Hook 只能使用服务进程 PATH、无法获得配置 `.env`，导致终端可用的 `uv` 等命令无法启动的问题；一次性 exec 与 Worker 现在共用合并后的环境和 PATH 查找规则。
- 修复工具流程的最终 LLM 请求期间收到的 pending 消息会随当前 turn 结束而丢失的问题；当前回复现在正常结束，多条 pending 合并后自动开启下一轮请求。
- 修复 `llm.request.prepared` 可以临时改写本 turn 初始输入或历史消息、pending 图片在排队时丢失且 Hook 修改未持久化的问题；request Hook 现在只修改本次新 drain 的 pending。
- 修复工具 prepared Hook 改写后的参数没有同步到当前 LLM 上下文，以及进程内 Hook 可以改写工具 ID/名称的问题；实际执行、后续请求和 transcript 现在统一使用最终 arguments。
- 修复 Cron 补发使用旧 job 快照覆盖禁用、调度和任务内容，以及多个平台同时连接时重复生成、重复发送和投递状态相互覆盖的问题；LLM 返回 `completed=false` 的失败或阻塞报告现在也会冻结并完成补发。
- 修复已完成的一次性 LLM Cron 重新启用或重新调度后直接复用旧报告、未创建新后台 Session 的问题；通知失败和平台重连仍补发同一轮已持久化结果。
- 修复 AgentSkill 配置写入后 reload 失败会留下磁盘与运行 registry 不一致的问题；Skill reload 现改为串行、完整验证并原子替换，名称冲突或候选失败时保留旧 registry、catalog 和 AgentSkill 配置。
- 补齐 Elnis HTTP 请求头、请求读取、响应写入和空闲连接超时，并拒绝请求体中的尾随第二个 JSON 值；未知字段继续作为无语义字段忽略。
- 修复 Elnis LLM 报告发送前就把事件标记为 `completed` 的问题；报告改用可恢复 outbox，所有目标回执持久化后才完成，失败项会定时及在重启后重试。
- 补全默认 `.env.example` 中缺失的 `JINA_API_KEY`。
- 修复 Session 删除、归档确认可能因列表或当前 Session 变化而作用到错误目标的问题，并让存储错误正确返回给调用方。
- `read_file` 的 `start_line` 兼容 LLM 偶尔生成的整数字符串，避免有效行号因 JSON 类型偏差导致读取失败。
- CLI TUI 执行 `/stop` 后会把当前运行状态收束为 `done` 并固定耗时，不再继续累加状态栏时间。
- 修复发现或内联预载多个 ELyph Skill 时规则卡会重复注入上下文的问题；同一会话首次注入后只继续返回 Skill 内容，保留历史中的首次规则卡以利于缓存命中。
- 修复同一时间戳下会话消息可能按 UUID 错序加载，导致历史上下文顺序不稳定的问题。
- 修复 `workspace` 工具设置目录时不支持 `~`、`~/path`、Windows `~\path`、`$HOME` 和 `$HOME/path` 主目录路径的问题。
- QQ OneBot 私聊文件段缺少 `url` 时会调用 `get_file`；若返回下载地址则保存到 ElBot，若只返回 OneBot 本地路径则直接提示该路径。
- QQ OneBot 入站 @ 消息现在会优先显示群名片，其次普通昵称，格式为 `[at 名字 qq:<id>]`，无法获取时才回退 QQ 号。
- 修复 Windows 下 `shell` 工具回退到 PowerShell 时中文输出可能乱码的问题。
- 修复 Windows 下无 bash 时 shell 命令的 bash AST 解析失败导致风险分类、沙盒校验、目录切换拦截和警告分析全部异常的问题；PowerShell 环境下跳过 AST 解析，风险分类直接返回高风险需用户确认。
- OneBot 发送图片失败时，不再出现可见 fallback，但仍会记录日志。

### Added

- **重构hook系统**
- 工具完成 Hook 支持返回包含 URL、路径或 base64 图片的 `message.segments`；多模态工具结果可持久化并按 OpenAI Chat Completions 协议作为后续图片消息提供给模型。
- 用户输入和工具 pending 的前置 Hook 支持用 `message.segments` 同时改写文字和附加图片，最终多模态内容会在请求前写入会话历史。
- `/chat` 和 `/work` 支持直接携带消息，在切换 Session 模式后立即发送；Session 命令状态改为按平台 Scope 隔离。
- QQ OneBot 新增 `send_file_mode` 配置，本地图片和文件默认使用 base64 发送，也可在共享文件系统的部署中显式改用 `file_uri`。
- `/log` 新增 `-s` 和 `--system`，用于筛选并显示 `system prompt` 日志。
- CLI TUI 宽屏模式支持用鼠标拖动聊天区与通知区之间的分界线，运行期间可自由调整两侧宽度。
- `read_file` 新增 `mode=ast`，可对 Go 和 Shell 文件按名称进行轻量 AST 搜索；`mode` 同时统一为 `read`、`grep`、`ast` 三种读取模式。
- 重构AgentSkill：去掉py wrapper，直接使用shell执行对应sklll，同时支持在Agentkill根目录添加 `ELBOT_SKILL.toml` 注册为普通工具，方便 LLM 直接调用结构化参数。
- 新增隐藏元工具 `agent_skill`，用于读取或写入 AgentSkill 的 `ELBOT_SKILL.toml`，写入前校验配置并在成功后 reload。
- 首次运行会生成 `skills/agent/agent_skill_creator/SKILL.md`，用于说明如何把 AgentSkill 注册为普通工具。
- 首次运行会生成 `skills/agent/write_elbot_hook/SKILL.md`，用于提示按需求编写 ElBot 规则 Hook。
- AgentSkill 的 `ELBOT_SKILL.toml` 支持只写 `risk` / `superadmin_only` 做文档可见性限制，不写工具化字段时不会注册为普通工具；默认 `agent_skill_creator` 和 `write_elbot_hook` Skill 会生成仅超管可见、低风险的 TOML。
- 新增 `/usage` 命令：从审计日志聚合 token 消耗，支持按模型/天/会话汇总，快捷参数 `-d` 天数、`-m` 模型、`-s` 会话。
- 新增 `workspace` 工具：设置当前前台 Session 的共享工作目录，路径类工具会基于该目录解析相对路径。首次切换到含 `AGENTS.md` 或 `AGENT.md` 的目录时，会自动附带说明文件内容；文件超过 64 KiB 时会提示缩短。
- 新增 `[platform_files]` 配置，统一控制平台入站文件最大保存大小和下载超时。
- QQ OneBot 支持自动保存私聊超级管理员入站文件；纯文件消息只回复保存路径或过大提示，不唤起 LLM，群文件不自动保存。
- `/requests` 命令现在展示每个 turn 的当前运行阶段（preparing/llm/tool/sending）和阶段耗时，可区分 LLM 慢还是平台发送卡住。
- 执行中的 Hook 会显示在 `/requests`，当前 Session 的 Hook 也会显示在 `/status`；可用 `/stop` 取消长时间运行的 Hook，手动取消按正常取消记录。
- 内联预载支持工具简写 `@t:<name-or-tag>` 和 Skill 简写 `@s:<name>`，并兼容中文全角冒号 `：`。
- CLI TUI 输入框支持用 `#文件名` 模糊补全本地文件；发送时会把引用替换为文件名和文件内容，含空格路径可写作 `#"a b.txt"`。
- `web_extract` 新增 `jina` 参数，默认使用 Jina Reader；传 `jina=false` 可手动改用直接爬取。
- `web_extract` 新增 `force_refresh` 参数，可按需跳过缓存、重新获取网页并更新缓存内容。




## [v0.3.0-alpha - 2026-07-01]

### Changed

- 同一轮多个工具调用的 `[tool]` 预览会合并为一条消息发送，减少平台刷屏。
- Elvena LLM 事件和 LLM Cron 支持 `session_mode=chat|work` 选择后台 Session 模式，默认仍为 `work`。
- `/detail` 高风险工具调用详情支持工具自定义纯文本展示；未自定义时仍会把 JSON 参数格式化成更易读的多行展示，字符串里的 `\n` 会显示为真实换行。
- `edit_file` 的高风险确认详情现在会按文件、模式和编辑步骤展示替换、新增、删除、匹配等操作。
- `edit_file` 不再向 LLM 暴露 `dry_run` 参数；系统会在用户确认前自动预检并生成 diff，预检失败不会进入确认或写入文件。
- `modify_el_skill` 现在复用 `edit_file` 的 `edits` 编辑说明与执行能力，并在确认前预检编辑、ELyph 语法和 no-op 修改，在高风险确认详情中展示预检 diff。
- 更新 `ELyph` 版本至 v3
- qq heartbeat ack 和 qqofficial gateway resumed 不再记录log
- read_el_skill 现在依赖modify_el_skill，方便执行可能的修改
- 现在不在启动elbot的时候校验ELyph语法，免得拖慢启动速度
- ELyph `**`/`~` 文本末尾冒号现在作为 warning 返回给 `create_el_skill`/`finalize_el_skill`，不再阻断创建或 finalize。
- `modify_el_skill` 修改 `SKILL.elyph` 后不再自动 reload；修改完成后需调用 `finalize_el_skill` 生效。
- 工具结果支持统一 `Warnings` 输出，用于提示 LLM 后续优先使用更合适的工具。
- `read_file`/`shell` 读取 EL Skill 文件时会提示使用 `read_el_skill`；`edit_file` 或 shell 直接修改 EL Skill 文件会在确认或执行前被拒绝，需改用 `modify_el_skill`。
- 常驻记忆和长期记忆源文件纳入通用 FileGuard 保护；读取会提示使用记忆工具，通用文件工具或 shell 直接写入会被拒绝。
- hook log日志不再重复记录

### Fixed

- `long_memory_write` 的 `update` 支持填字段更新 meta，并新增 `content_edits` 复用 `edit_file` 的编辑操作修改正文；确认前会自动预检并展示 diff。
- 修复 `edit_file` 使用 `create=true` 创建新文件时，目标父目录不存在会在确认写入阶段失败的问题。
- `response_timeout_seconds` 现在控制整轮用户请求总时长，默认 `0` 表示不限时；单次 LLM 流式请求只由首包和 idle 超时控制。

## [v0.2.0-alpha - 2026-06-27]

### Added

- Elvena v3 动作通道：Elnis 支持 `calls`，首批支持 raw 平台 API 以及 `message.recall`、`member.mute`、`chat.leave` capability，未支持的可以直接调用消息平台api；Hook rules 可通过 `exec` action 执行脚本，并用 `stdout=elvena` 经内部 Elvena Bus 触发 Elnis direct/LLM/calls；direct calls-only 请求不会额外发送消息。
- `edit_file` 的 `*_match` 操作新增 `match_mode` 与 `index` 参数：`match_mode=line` 时按单行前缀匹配整行（容忍行首缩进，规避换行符匹配出错），`content`（默认）保持精确子串语义；多处匹配时可通过 `index` 选择第几处，未传 `index` 报错并列出所有匹配位置。
- Hook rules 新增角色分区与平铺控制字段：`roles`、`actor_roles`、`group_roles`、`consume`、`stop_propagation`；平台消息 Hook 输出现在会发送，`consume=true` 可阻止后续命令/LLM 处理。
- Hook rules `send` action 新增 `segments` 列表，支持多类型多段输出（text/image/file/emoticon，含 url/path/base64），格式与 Elvena segment 统一。
- Hook rules `exec` action 新增 `outputs` stdout 模式，脚本 stdout 解析为 JSON 并提取 `outputs` 数组和可选 `text`；设 `field` 时 `text` 覆写对应字段，不设时不修改原文。
- 平台入站上下文新增统一群身份 `owner/admin/member/unknown`，QQ OneBot 和 Telegram 会映射群主/管理员/普通成员。
- Hook 平台上下文现在填充当前平台消息 ID `platform.message_id` 与引用/回复目标消息 ID `platform.reply_to_message_id`，便于规则 Hook 处理引用消息，例如撤回被引用消息。
- `/hooks` 命令：列出所有已注册 Hook、查看某个 Hook 详细配置、热重载全部 Hook（修改 `hooks.toml` 后无需重启即可生效）。

### Changed

- LLM 请求超时配置改为 `first_chunk_timeout_seconds`、`stream_idle_timeout_seconds`、`response_timeout_seconds`，旧 `timeout_seconds` 已移除；默认首个流式事件等待 180 秒、流式 idle 60 秒、整次响应不限总时长。
- Provider 配置重构：删除未使用的 `[global_default]`，删除 `[model_metadata.context_windows]` 全局模型窗口表；模型级 `context_window` 和 `extra_payload` 统一收到 `[providers.<name>.model_configs."<model>"]` 下，按 `provider/model` 查找，避免跨 provider 同名模型冲突。
- Provider 新增 `proxy` 字段，支持 HTTP/SOCKS5 代理。
- 表情 Hook 从内嵌插件改为规则 Hook 示例，不再内置 emoticon 插件和 `emoticon.toml` 资产。
- LLM 建连/HTTP 可重试失败时通过 Notice 显示当前重试次数。
- `finalize_el_skill` 工具风险等级由 high 降为 medium。

### Fixed

- 修复长工具链会被 Agent 内部 5 分钟默认请求超时静默停止的问题；整轮超时时现在会提示用户。
- 修复 QQ OneBot 发图片/表情/文件时 API 超时可能取消 WebSocket 写入并触发断线重连的问题；媒体发送失败时会尝试发送同目标文字提示。
- 修复 OpenAI-compatible 流式响应中途断开但缺失 `[DONE]` 时被当作正常结束的问题；现在会明确通知 LLM 响应中断。
- 修复 OpenAI-compatible 流式请求使用单一 HTTP 超时导致模型首字慢或长输出超过 60 秒时被错误中断的问题。


## [v0.1.0-alpha] - 2026-06-24

ElBot 的首个预发布版本。轻量 Agent/Chatbot 框架，目标是个人助手、平台机器人与可编排的自动化助手。

### Added

- **轻量内核**：Go 实现，本地启动 <10ms，常驻内存约 30MB。
- **Chat/Work 双模式**：chat 模式关闭工具，适合日常聊天与低成本对话；work 模式启用工具发现与调用，两模式可独立配置模型。
- **工具发现机制**：默认只暴露 `discover_tool` 与工具名，按需注入完整 schema，减少无效上下文开销。
- **Session 服务**：持久化会话，支持恢复、归档、置顶、Fork、删除、分页与平台隔离；长对话自动上下文压缩。
- **Hook Layer**：在 Agent 输入、LLM 请求/响应、平台发送等关键点插入扩展逻辑；内置规则 Hook 与常驻记忆 Hook。
- **标准 Cron 与 LLM Cron**：标准 Cron 按计划直发固定内容；LLM Cron 用 ELyph 任务描述驱动模型执行，支持一次性与周期任务、missed 补跑与广播。
- **ELyph Task Notation**：结构化任务描述语言，用于 LLM Cron 与原生 Skill，减少自然语言歧义。
- **原生与外置 Skill**：`create_el_skill` 元工具支持 LLM 创建原生 EL Skill（纯 ELyph 或附带 Go 源码并编译）；兼容 agentskills.io 风格外置 AgentSkill（附带 Python 脚本）。
- **Elnis 监听枢纽**：接收 Elwisp 通过 Elvena HTTP 协议投递的外部事件，支持 record/direct/llm 三种模式与多目标投递。
- **多平台适配**：CLI（含 client/server 分离与远程连接）、QQ OneBot v11、QQ 官方机器人、Telegram Bot API。
- **安全策略**：工具风险分级、角色权限校验、高风险确认流程与后台 shell 轻量沙盒。
- **记忆系统**：常驻记忆（core/normal 分层）按平台与 actor 注入；长期记忆基于 Markdown 源数据与 SQLite FTS 检索。
- **日志与审计**：运行日志、审计日志与 Elnis 日志分离，支持结构化字段与按日期轮转。
- **SQLite 持久化**：Session、消息、上下文摘要、工具调用记录、Cron job、Elnis 事件统一存储。

### Known Limitations

- MCP 工具、子 Agent 与完整多模态（语音、视频、文件真实模型输入）尚未实现。
- 接口、配置与内部实现仍可能调整，更适合作为个人 Agent/bot 框架探索使用。
