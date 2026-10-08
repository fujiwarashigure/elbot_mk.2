# ElBot 代码地图

本文用于快速定位“某类任务应该看哪些文件”。需要理解调用链路时，读 `devdocs/architecture.md`。

定位方法：

```bash
rg -n "locator:tool" devdocs/code-map.md
```

原则：

- 同一职责集中在目录内时，只写目录。
- 只有核心入口、跨目录桥接、容易混淆或独特文件才单独列出。
- 每节只写入口和边界，不展开完整架构说明。

<!-- locator:startup -->
## 启动、运行模式与装配

适用任务：命令行入口、运行模式判断、app 层依赖装配、远程 CLI client/service 入口。

先看：

- `cmd/elbot/main.go`：程序入口。
- `internal/launcher/cli.go`：命令行解析和补全生成。
- `internal/app/app.go`、`runner.go`、`dependencies.go`：稳定启动入口、分阶段 Runner 和可替换依赖组。
- `internal/app/foundation.go`、`models.go`、`runtime.go`：配置/存储基础设施、模型客户端，以及 Cron/Tool/Hook/Agent 核心装配。
- `internal/app/image_rewriter.go`、`image_to_prompt.go`：把 `image_generate` 的低成本改写模型和 `image_to_prompt` 的视觉模型接到已有 `ModelClients`，再以接口形式注入 builtin 工具；`image_to_prompt` 显式启用但 provider/model 有误时在启动阶段报错。
- `internal/app/vision.go`、`internal/agent/vision.go`、`internal/vision/`：自动视觉兜底。`[vision]` 段显式开启后，`agent.callLLM` 在 `APIError.Category == vision_unsupported` 时调用 `VisionDescriber` 把图片段替换成文字描述（有界并行 + 整批时间预算），失败则降级回文本引用；`internal/vision` 是 `image_to_prompt` 工具与兜底共用的唯一描述引擎（预处理 + 流式调用 + 版本化指纹缓存 + 成功/负缓存 + 同 key 合并 + 运行器/等待者上限 + `Stats()` 接入 `/metrics` + panic 隔离），不反向依赖 `internal/media`。`internal/llm/openai` 的 `parseError` 负责把上游错误映射为 `APIError.Category`，`shouldFallbackVision` 只读该字段。
- `internal/app/asr.go`、`internal/agent/asr.go`、`internal/asr/`：可选语音转写。`[asr]` 显式开启后，`HandleMessage` 在已唤醒的入站消息上调用 `AudioTranscriber` 把语音段替换成 `[语音 N 自动转写（可能有误）：...]` 文本段；`internal/asr` 负责 OpenAI 兼容 multipart 上传、有界并发/队列、录音大小与响应上限、成功/负缓存和重试；预算按 `asr` kind 预占，受全局、群和群内单用户四级每日额度约束。
- `internal/llm/errors.go`：结构化 `APIError`（`StatusCode`/`Code`/`Type`/`Param`/`Message`/`Cause`）与确定性失败分类；`openai` 适配器的 `parseError` 在此保留上游错误码，供视觉兜底判定和负缓存使用。
- `internal/app/platforms.go`、`integrations.go`：平台运行、Elnis 和平台能力接线；同目录 `service_marker*.go` 使用 `flock` 文件锁做服务单实例互斥，避免陈旧 PID 在容器重建后误判。

常用搜索：

```bash
rg -n "func Run|service run|completion|--client|RunCron" cmd internal/app internal/launcher
```

<!-- locator:config -->
## 配置、资产与日志

适用任务：配置读取、默认配置资产、provider/state/tool_tags 合并、日志写入/轮转/读取。

先看：

- `internal/config/`
- `internal/logging/`：`logging.go` 是日志中心与三份按日文件（`elbot-*` / `audit-*` / `elnis-*`，审计下限至少 info）；`reader.go` 是 `/log`、`/audit`、`/elwisp`、`/usage` 与维护报告共用的反向分块查询，读取 `LogEntry.Fields` 这种文本键值契约；`contract.go` 是 fork 版"来源标识（`module`）+ 操作结果（`result`）"最小契约的唯一出处（`ModuleAgent` / `ModuleHook` / `ResultRejected` 等常量、`ValidLogModule` / `ValidLogResult`）。**日志文件格式与字段查询契约本轮不变**：上游的九字段 LogRecord 与 JSONL 落盘未实施。
- `internal/tool/availability.go`：`ToolAvailabilityReason` / `ToolAccessReason` / `RegistryToolAvailabilityReason` 把"工具为什么没被采用"拆成互斥的机器可读原因（`tool_not_found` / `tool_context_unavailable` / `tool_hidden` / `tool_requires_superadmin` / `tool_risk_above_allowed_level` / `tool_no_schema`），供预载与审计使用；判定顺序与 `CanAccessTool` / `InfoAvailableInContext` 一致，迁移调用点不得顺带改变准入。
- `docs/configuration.md`

常用搜索：

```bash
rg -n "ELBOT_CONFIG_FILE|services.toml|providers.toml|state.toml|tool_tags.toml|TextHandler|audit|LogModule|ResultRejected|ToolReason" internal/config internal/logging internal/tool docs/configuration.md
```

<!-- locator:health-ops -->
## 健康状态、限速、熔断与运维接口

适用任务：`/live`、`/ready`、`/healthz` 语义，watchdog 诊断，Provider 熔断/备用，群聊限速，`/tasks`、`/metrics` 和 ops token。

先看：

- `internal/health/`：健康状态、`/live`、`/ready`、`/healthz`、extra handler token；`state.go` 区分进程存活、readiness、调度心跳、平台和模型状态。
- `internal/app/health.go`、`ops_health.go`、`health_llm.go`、`health_handler.go`：启动健康接口、读取 `ELBOT_HEALTH_*` / `ELBOT_OPS_TOKEN` / 重启原因文件、组装 `/metrics`、`/diagnostics` 以及只读插件状态 `/plugins/memory`、`/plugins/learning`。
- `internal/app/doctor.go`、`internal/launcher/cli.go`、`cmd/elbot/main.go`：`elbot doctor` 配置/端口/平台/模型验收；`platform_ok` 独立于 `config_ok`，`--require-platform` 要求平台状态存在，`--e2e` 使用唯一探测标记做 CLI 真实消息往返。
- `internal/character/store.go`、`write.go`：角色/图片 `version`、`source` 与 `Manifest`/`WriteManifest` 备份清单。
- `internal/app/breaker_llm.go`、`internal/app/models.go`、`internal/llm/breaker/`：Provider 熔断、`fallback_mode` / `fallback_on_error`、备用 Provider 和总超时；`models.go` 的 `newProviderLLM` 按 `[providers.*].api_mode` / `model_configs.<model>.api_mode` 选择 chat 或 responses 适配器，混用两种协议时返回按请求模型分发的 `protocolRouter`。
- `internal/agent/ratelimit.go`、`internal/ops/ratelimit/ratelimit.go`：用户级/群级令牌桶叠加；阈值和拒绝原因进入 `/metrics.rate_limit`。
- `internal/processenv/environment.go`：Shell / Go Skill 子进程凭据变量过滤。
- `deploy/elbot-watchdog.sh`、`deploy/restore-verify.sh`、`deploy/backup.sh`：阈值/冷却/诊断脱敏、重启原因文件、sha256 备份清单和隔离恢复验证；在线 SQLite 模式先做数据库快照再复制媒体并按引用补齐，恢复结果区分 `passed` / `static_passed` / `static_passed_with_skips`，并在启动隔离实例前移除旧 PID 标记。
- `deploy/upgrade.sh`、`deploy/rollback.sh`：配置兼容性检查、源码版本守卫、按实际 image ID 保存旧镜像、升级前 stop 模式快照、隔离配置预检；回滚先载入旧镜像验证备份，重建后等待 `/ready` 并跑 `doctor`，失败时尝试恢复回滚前 data。
- `internal/redact/`：用户可见错误、Hook 失败、日志/audit 和健康快照共用的凭据脱敏与错误 ID；`internal/health/redact.go` 保留健康包的兼容入口；`deploy/tests/watchdog_redaction_test.sh` 覆盖诊断包脱敏。

常用搜索：

```bash
rg -n "IsProcessLive|SchedulerKnown|ExtraHandlerToken|RateLimitStatus|UsesFallbackOnError|WithoutSensitiveKeys|RunDoctor|WriteManifest" internal
rg -n "ELBOT_OPS_TOKEN|WATCHDOG_OPS_TOKEN|restore-verify|fallback_mode|doctor|upgrade.sh|rollback.sh" docs deploy README.zh-CN.md
```

<!-- locator:agent-chat -->
## Agent 对话流程

适用任务：普通聊天主流程、LLM 调用、流式输出、Prompt、system prompt、工具 transcript、pending 输入、风险确认。

先看：

- `internal/agent/core.go`：Agent 状态和构造装配；构造参数集中在 `Options`。
- `internal/agent/message.go`：消息入口、slash/普通输入分发和用户错误通知。
- `internal/agent/command_runtime.go`：命令权限、Turn 冲突、通知和 continuation 的统一编排。
- `internal/agent/input.go`：普通输入预处理、命令 continuation、pending 和风险确认入口。
- `internal/agent/inbox.go`、`inbound_speaker.go`：群共享线程的有序 Session 队列与同一成员连续消息合并窗口；每条输入保留独立 Actor/Speaker 与平台消息钮，不同成员串行而不合并权限主体；共享会话的用户消息写入服务端生成的发言成员标记与 `speakers` 元数据。
- `internal/agent/segments.go`：平台入站 Segment 与 LLM Segment 转换。
- `internal/agent/inbound_media.go`：实际消费前的平台 resolver 与 Media Center 桥接、大小校验和不可用降级。
- `internal/tool/builtin/chat_history.go`：当前聊天历史查询与媒体位置/下载状态展示，查询不下载；`get_media.go`：显式选定媒体位置获取，仅返回文本 ID，单次最多 5 次未入库媒体获取尝试；`view_image.go`：把图片本身交给模型（`source` 的媒体 ID / HTTP(S) URL / 本地路径，或 `message_id` + `media_index` 取历史图片），声明 `tool.Info.VisionRequired`，本地路径限超级管理员。
- `internal/tool/capability.go`：请求级工具能力（当前只有 `Vision`），随 context 传播；`InfoAvailableInContext` 据此隐藏 `VisionRequired` 工具，agent 在 `runChat` / `handleSessionInput` / 后台预载入口用 `withToolCapabilities` 按当前 work 模型的 `vision` 声明注入。
- `internal/config/inspect.go`：只读配置体检（`UnknownAppConfigKeys` 用严格解码列出 `app.toml` 未消费的键；`BuiltinAssetDrift` 对比内置 Skill 文件与默认版本），由 `internal/app/config_check.go` 汇入警告；`internal/agent/commands/doctor.go` + `internal/app/doctor.go` 的 `doctorService` 提供仅超管的 `/doctor`（复用 `RunDoctor(SkipModel)`，无问题返回 `Everything is OK`）。
- `internal/toolrun/toolrun.go`：后台任务工具白名单 `BackgroundToolAllowed`（排除 `discover_tool`/`workspace`），后台上下文里 `Resolve` 不再回落到全局 registry、`Schemas` 按同一白名单过滤；`internal/agent/cron.go` 的 `backgroundCachedTools` 在写入后台 metadata 前过滤外部声明。
- `internal/platform/qq-onebot/adapter.go`、`transport.go`：超过 `qqTextPageRunes`（3000 rune）的文本按页包成 `node`，用 `send_group_forward_msg` / `send_private_forward_msg` 发一条合并转发（不再分页加标记）；`referenceFetcher` 对引用回复里的合并转发展开一层。
- `internal/utils/fileops/rollback.go`：进程内、按 Session 的编辑前内容备份（每文件仅保留最近一次，1024 条 / 256 MiB 上限，超出淘汰最旧），`Restore` 用 `RevisionAfter` 校验文件自编辑后未被改动，Shell 或外部程序的修改一律拒绝覆盖；`internal/tool/builtin/file_rollback.go` 与 `internal/agent/commands/rollback.go` 分别是仅超管的 `rollback_file` 工具与 `/rollback [编号]` 命令，`edit_file` 通过 `editTool.Backups` 写入记录，Session 身份由 `agentToolRunDeps.PrepareToolContext` 注入的 `tool.WithSessionID` 传递。
- `internal/media/platform.go`：共享平台导入、历史媒体位置关联与本地 ID 查询；`manager.go`、`image.go`：统一媒体入库和持久化前图片压缩；`resolver.go`：LLM 媒体解析与传输选择；`history.go`：跨库历史 owner 分页对账。
- `internal/storage/sqlite/media_history.go`：主库历史媒体关联与引用事务，区别于机器人发送输出索引。
- `internal/agent/reference.go`：只读提供当前 Session ID，供平台引用续聊/fork 判定。
- `internal/agent/media_output.go`：发送前归一、发送副本解析与有序媒体回执缓存。
- `internal/agent/options.go`、`logging.go`、`identity.go`：运行配置、日志和 Actor/Scope 解析。
- `internal/agent/chat.go`：普通对话主流程；群级默认模型只在本轮没有显式 `@model:` 覆盖时生效。后台 turn（`backgroundTurnOutput`）在每次模型调用前、工具批次落库后与最终输出前各查一次前台接管标记（`backgroundSessionTakenOver` → `session.WasPromoted`），命中即返回 `errBackgroundTakenOver`，不发布 PhaseError、不写迟到助手消息。
- `internal/agent/group_policy.go`：群级策略解析、唤醒/静默判断、模型目录、learning 审核动作授权；策略保存在 `state.toml` 的 `group_policy`。
- `internal/agent/group_knowledge.go`、`internal/groupkb/`：按群 scope 的本地确定性 FAQ 匹配与状态；`groupkb` 只做归一化和 exact/contains/keywords 匹配，不调用模型；Agent 负责权限、上限、`state.toml [group_knowledge]` 持久化和命中后的直接回答。
- `internal/agent/member_panel.go`、`internal/agent/commands/member_panel.go`：普通成员 `/me` 自助面板；按 FairKey 过滤自己的请求和排队消息，并汇总自己的生图/视觉/聊天额度，不暴露跨群全局聚合用量。
- `internal/agent/group_services_state.go`、`group_services_reminders.go`、`group_services_polls.go`、`group_services_signups.go`、`internal/agent/commands/group_services.go`：群内提醒/投票/报名；状态写入 `state.toml [group_services]`，提醒由 Agent 本地 ticker 调度，投票/报名为纯确定性状态机，全部受群运行状态与全局/群级开关约束。
- `internal/agent/model_policy.go`：实际执行模型目标的最后一道目录校验，覆盖 turn hook、cron override、压缩、视觉 fallback 和 group_analysis 摘要。
- `internal/agent/state_watch.go`、`internal/agent/commands/state.go`：`state.toml` 外部编辑热加载；`refreshRuntimeState`/`applyRuntimeState`（`model.go`）负责 mtime 判定、分区合并和变更摘要，`StartRuntimeStateWatch` 每 15 秒轮询，`/state` 查看状态、`/state reload` 强制生效；`saveRuntimeState` 写回前先合并外部修改，`[budget]` 账本不参与热加载。
- `internal/angelmemory/`、`internal/tool/builtin/angel_memory.go`、`internal/tool/builtin/angel_forget.go`：scope-local 长期记忆与工具；`angel_forget` 默认关闭（`[angel_memory].allow_tool_forget`），只删除来源为当前发言人的单条记忆，高风险 + `confirm=true` 二次确认 + 每分钟上限，作为 `angel_recall` 的依赖注入；`/memory backfill` 只回填旧数据可确定的 `source_kind`。
- `internal/agent/tool_auth.go`：群工具白名单统一授权入口与执行前复核；展开工具 profile，区分“继承全局”和“禁止全部”。
- `internal/agent/budget.go`：群 / 单用户 / 全局每日账本；生图/视觉/语音转写调用预占、chat token/费用、provider 重试、工具执行幂等分别写入 `budget`；账本落盘失败时回滚并拒绝受限调用。可选 `chat_hard_limit` 在调用前按估算输入 + 输出预留原子预占，usage 返回后结算差额；usage 缺失或重启未结算时保持保守占用并写入 `budget.uncertain`。
- `internal/historygate/`：统一历史写入门；适配器的 `chat_history` / `outbound_messages` 写入按可信 `平台 + scope` 策略过滤，`history=off` 不删除旧记录但停止新增。
- `internal/agent/turn_gate.go`：turn 终止状态和晚到输出闸门；流式 flush、工具结果回传和最终发送前检查。
- `internal/agent/events.go`：notice/request/meta_event 的确定性处理；撤回按消息 ID → turn request 精确取消，成员退群/禁用按 FairKey 定向取消，不调用 LLM；机器人自身被禁言/踢出时由 `group_runtime.go` 转入 `muted` / `removed`，暂停该群模型调用与输出。群运行状态持久化在 `state.toml [group_runtime]`。
- `internal/agent/chat_llm.go`：LLM 调用和消息转换。
- `internal/agent/chat_tools.go`：工具执行与确认。
- `internal/agent/turn_output.go`：turn 输出适配。
- `internal/agent/prompt.go`：Prompt 构建。
- `internal/agent/system_prompt*.go`：Soul、常驻记忆、工具提示等 system prompt 来源和组合。
- `internal/agent/cron.go`：`RunBackground` / `RunCronMessage` 的后台入口；已提升（被前台接管）的 Session 在 `backgroundSession` / `ensureBackgroundSession` 阶段就短路，出口统一为 `RunResult{TakenOver: true, Outcome: OutcomeTakenOver}`（`err == nil`），cron 侧记为 `ReportReady=true, TaskCompleted=true, Report=""`，因此不投递任何汇报。
- `internal/agent/tool_transcript.go`：工具 transcript 持久化。

常用搜索：

```bash
rg -n "Handle|Run|Prompt|tool_calls|reasoning|usage|pending|prepared" internal/agent
```

<!-- locator:commands -->
## Slash 命令与补全

适用任务：新增/修改 slash 命令、命令帮助、命令参数补全。

先看：

- `internal/agent/commands/`：内置命令实现。
- `internal/agent/commands/register.go`：命令模块注册入口。
- `internal/agent/commands/session_*.go`：按模式、核心、导航、生命周期和格式化拆分的 Session 命令；共享状态由 `SessionCommandState` 按 Scope 隔离。
- `internal/agent/commands/group_policy.go`：`/grouppolicy` 群级策略命令；修改入口只接受当前群，`learning-moderation` 额外要求机器人超级管理员。
- `internal/command/`：通用命令框架和 Router；`Info.GroupAdminNeedsGrant` 用于把群管理员访问绑定到服务端群级授权。
- `internal/completion/`：平台补全服务。
- `docs/commands.md`：用户侧命令文档。

常用搜索：

```bash
rg -n "Register|Info\{|Help:|Complete|Alias|/requests|/model" internal/agent/commands internal/command internal/completion docs/commands.md
```

<!-- locator:request-turn -->
## Request、Turn 与运行状态

适用任务：active request 树、取消/停止/超时、阶段展示、工具 pending、确认状态、runtime status。

先看：

- `internal/request/`：并发限制、有界等待队列、全局及每用户/每 scope 队列上限、按 `FairKey` 的公平调度和 `CancelFairKey` 定向取消；`Snapshot` 提供排队长度、平均/最老等待和队满/超时/取消拒绝指标。
- `internal/turn/`
- `internal/runtime/`
- `internal/agent/status.go`：Agent runtime status 发布。
- `internal/agent/request_context.go`：父子 request context。
- `internal/agent/risk_confirmation.go`：高风险确认命令文案和识别。

常用搜索：

```bash
rg -n "Phase|Request|Cancel|pending|confirm|runtime status|sending" internal/request internal/turn internal/runtime internal/agent
```

<!-- locator:tool -->
## Tool Runtime、工具发现与内置工具

适用任务：内置工具、工具注册、schema、风险等级、确认详情、工具发现、工具缓存、工具 tag、文件/shell/web/cron/memory 工具。

先看：

- `internal/tool/`：Tool Runtime 核心类型、builder、discover、executor、sandbox/workspace helper。
- `internal/tool/media_runtime.go`：shell/Skill 的显式媒体准备、调用期引用、sandbox 导出缓存与受控结果导入；缓存复用刷新 ModTime，沿用既有 sandbox 清理。
- `internal/tool/runtimeinfo/`：工具运行期常用信息入口，如配置路径、sandbox、文件发送配置、时间源和规则卡转发。
- `internal/toolrun/`：工具调用中间层、工具视图、命名解析、风险确认、执行前 Session 工具参数媒体引用，以及排队后的授权/额度二次校验钩子。
- `internal/tool/builtin/`：内置工具。
- `internal/tool/builtin/group_analysis.go`：`group_analysis` 工具，读取本地历史并输出群统计，可选 LLM 摘要。
- `internal/tool/builtin/angel_memory.go`：`angel_remember` / `angel_recall` 工具。
- `internal/tool/builtin/self_learning.go`：`self_learning_review` 工具。
- `internal/tool/builtin/image_to_prompt.go`：`image_to_prompt` 内置工具，读取 Media Center 图片并通过 `ImagePromptService` 委托给共享的 `internal/vision.Service`（app 层用已有 provider 配置它）；工具自身不缓存、不做 singleflight，缓存键、并发合并与失败/空/截断处理都在 `internal/vision`。
- `internal/groupanalysis/`：群分析 clean-room 统计与可选 `Summarizer`；只依赖 `chat_history` / `outbound_messages`。
- `internal/groupkb/`：确定性 FAQ 匹配引擎；归一化（全角/大小写/空白/标点）和 exact/contains/keywords 规则，纯本地、无模型依赖。
- `internal/angelmemory/`：clean-room SQLite 长期记忆、召回和上下文构造；写入来源类型/用户/消息 ID/Session ID，提供 scope 限定的查询、前缀解析和按来源删除。
- `internal/selflearning/`：clean-room 观察、候选挖掘、review-before-apply 和上下文构造。
- `internal/agent/commands/memory_learning.go`：`/memory`、`/learning` 管理命令。
- `internal/agent/commands/memory_forget.go`、`memory_common.go`：普通成员 `/forget` 入口、可见性过滤、按来源删除和常驻记忆清空。
- `internal/tool/builtin/file_tools_ast.go`：`read_file` 的 Go/Shell AST 名称搜索与结果渲染。
- `internal/agent/tools.go`：Agent 工具运行态和命令依赖适配。
- `internal/agent/toolrun_*.go`：Agent 到 ToolRun 的桥接。
- `internal/agent/tool_cache.go`：Session 级工具 schema 缓存；工具发现与 `@tool:` / `@skill:` 预载的写入都经 `mutateSessionMetadata` 在事务内合并进最新 metadata。
- `internal/agent/tool_directive.go`：`@tool:` / `@skill:` 预处理。
- `internal/agent/tool_tag_config.go`：工具 tag 配置。
- `internal/security/`：工具权限和风险策略。
- `internal/utils/fileops/{file,encoding,text}.go`：文件生命周期、编码与通用文本处理。
- `internal/utils/fileops/{edit,match,diff}.go`：原子编辑解析、目标匹配与 unified diff。

常用搜索：

```bash
rg -n "discover_tool|NewBuilder|Risk|Confirm|ToolRun|Result\{|Outputs|workspace|shell" internal/tool internal/toolrun internal/agent
```

<!-- locator:tool-flow -->
## 工具调用链路相关文件

适用任务：LLM tool call 到工具执行、工具结果回灌 LLM、工具调用记录、工具确认、批量预览。

先看：

- `internal/agent/chat_tools.go`：Agent 工具执行主入口。
- `internal/toolrun/`：执行前解析、过滤、确认、排队后授权复核、额度预占和预览。
- `internal/tool/executor.go`：Tool Runtime 执行适配。
- `internal/tool/tool.go`：Tool 核心类型。
- `internal/agent/tool_transcript.go`：tool message/transcript 落库。
- `internal/storage/sqlite/tool_call_repository.go`：工具调用记录持久化。

常用搜索：

```bash
rg -n "ToolCall|tool message|transcript|ToolCallRecord|confirm|preview" internal/agent internal/tool internal/toolrun internal/storage
```

<!-- locator:skill -->
## Skill 与 ELyph

适用任务：AgentSkill 解析或工具化、ELyph parser/linter、原生 EL Skill 创建/修改/finalize、Go skill 扫描/编译/运行。

先看：

- `internal/elyph/`：ELyph 语言层。
- `internal/tool/skill/`：Skill 解析、扫描、catalog、创建、修改、finalize、runner。
- `internal/tool/skill/agent_manifest.go`：AgentSkill 工具化 manifest。
- `internal/tool/skill/media.go`：Go payload/TOML 媒体参数的执行副本转换和 stdout 媒体结果解析；`internal/config/assets.go` 中的 `defaultAgentSkillCreatorSkillMD` 是 Agent Skill Creator 的内置说明。
- `internal/tool/skill/go_source.go`：原生 Go skill 源码维护和编译。

常用搜索：

```bash
rg -n "SKILL.elyph|ELBOT_SKILL|AgentSkill|go_skill_run|finalize|Lint|Catalog" internal/elyph internal/tool/skill
```

<!-- locator:hook -->
## Hook 与插件

适用任务：Hook 事件、控制字段、注册、列表、热重载、规则 Hook TOML、exec action、hook.v2 协议、持久 Hook 和 SharedState。

先看：

- `internal/hook/event.go`：Hook 点、事件 payload 和 Handler 基础类型。
- `internal/hook/media.go`：Go Hook 使用的宿主 Media API；`internal/hook/runtime/media.go`：hook.v2 媒体 RPC、安全导出和临时引用生命周期。
- `internal/hook/output/`：规则、一次性 exec 与 runtime 共用的输出协议、消息图片 segment 规范化、校验和 delivery 转换。
- `internal/hook/protocol/`：进程 Hook 共用的 `hook.v2` 帧、ID 校验和 `event.handle` 公共结果字段。
- `internal/processenv/`：Shell 与进程 Hook 共用的环境分层、PATH 补充和可执行文件解析；`internal/hook/process.go` 保留 Hook 侧适配入口。
- `internal/hook/match.go`：Hook 条件匹配、字段读取和模板值。
- `internal/hook/manager.go`：普通 Hook 注册、排序、执行与原子 handler 快照替换。
- `internal/hook/control/`：`/hooks` 的列表、重载和持久进程生命周期管理入口。
- `internal/hook/builtin/`、`internal/hook/plugins/`：内置 Hook 注册与内置插件。
- `internal/hook/rules/`：规则 Hook；`rules.go` 提供类型和模块入口，`config.go`/`toml_error.go` 负责配置加载与诊断，`rule.go`/`action.go`/`exec.go` 负责规则及 Action 执行，`exec_process_*.go` 负责一次性 exec 的跨平台进程树终止，`detail.go` 负责列表详情。
- `internal/hook/runtime/`：Worker Hook 配置、进程、双向 Pipe RPC、waiting 路由、工具桥接和进程内 SharedState。
- `internal/agent/hooks.go`：Agent 的 Hook 执行、上下文和 continuation 接入。
- `internal/agent/output.go`：Agent 的 Output Manager 与平台 sender 接入。
- `internal/agent/media_output.go`：平台发送前解析 Hook/Tool 输出中的 media，并清理受控临时导出。
- `docs/hooks.md`：用户侧 Hook 文档。

常用搜索：

```bash
rg -n "Event|Handler|Control|plugins/hooks.toml|exec|hook.v2|runtime|SharedState|message.segments|llm.messages" internal/hook internal/agent docs/hooks.md
```

<!-- locator:output -->
## Output、Delivery 与发送

适用任务：输出意图结构、文本/图片/文件/语音/at/reply/emoticon 发送、流式输出、notice、reasoning、runtime status。

先看：

- `internal/delivery/`：平台无关输出意图和发送管理。`ToolPreviewPrefix` / `Output.IsToolPreview` / `IsToolPreviewNotice` 是"这条通知是不是工具调用进度预览"的唯一判定入口：Agent 写前缀（`formatToolPreview`）、hook 出站记录用它跳过预览；`ShouldDropGroupToolPreview(target, outputs, groupContext)` 是"群聊里不投递进度预览"这条平台跳过规则的唯一入口（OneBot 与 QQ 官方调用它，各自只提供"当前上下文是不是群聊"：`isGroupContext`）。Telegram 与本地 CLI 不跳过预览。
- `internal/agent/turn_output.go`：Agent turn 输出适配。
- `internal/agent/output.go`：Agent 的 Hook/工具输出意图和平台 sender 接入；`Agent.SendNotice` 是全部通知投递（含 cron / Elnis / hook 经 `app` 注入的 `sendNotice` 闭包）的统一裁决点，先过 `turnOutputAllowed` 与 `noticeTargetBlocked` 再下发。
- `internal/platform/platform.go`：平台发送抽象。

常用搜索：

```bash
rg -n "Output|SendChat|SendNotice|Stream|Reasoning|emoticon|receipt|ToolPreview" internal/delivery internal/agent internal/platform
```

<!-- locator:platform -->
## 平台适配

适用任务：平台输入解析、平台发送、CLI/TUI、远程 CLI、OneBot、QQ 官方、Telegram。

先看：

- `internal/platform/platform.go`：平台抽象，以及可选的群历史/群目录/头像/群素材能力接口；`PlatformEventHandler` 提供 notice/request/meta_event 的非消息事件入口。
- `internal/platform/qq-onebot/capabilities.go`：OneBot 可选群历史、群信息、成员列表和头像 URL 能力。
- `internal/platform/qq-onebot/message.go`：入站文本保留原始空白，并生成不含转发内容的唤醒匹配视图；按共享总预算展开 `forward`/`node` 合并转发。`inbound.go` 提供 `platform+bot+scope+message_id` 入站去重和有界预处理 worker / 高优先级事件通道；`adapter.go` 的 `readLoop` 不再逐条无界起 goroutine，并限制 `get_forward_msg` 拉取次数、超时、返回体和拉取失败降级。
- `internal/platform/telegram/capabilities.go`：Telegram 可选群信息和管理员列表能力；历史与全量成员回退本地历史。
- `internal/platform/media.go`：Chat History 原始有序 segments 编解码与敏感来源清洗。
- `internal/platform/refcontext/`：按输出索引、Chat History、平台兜底恢复引用；自己 Session 的最后一条 assistant 自动 Resume，较早 assistant 自动 Fork。
- `internal/platform/config.go`：平台配置解码。
- `internal/platform/builtin/`：内置平台装配。
- `internal/platform/cli/`：本地/远程 CLI 和 TUI。
- `internal/platform/cli/tui.go`、`tui_mouse.go`、`tui_copy.go`：TUI 主模型、鼠标交互与 copy mode。
- `internal/platform/qq-onebot/`
- `internal/platform/qqofficial/`
- `internal/platform/telegram/`
- `internal/platform/headless/`

常用搜索：

```bash
rg -n "PlatformAdapter|SendChat|MessageSegment|Actor|Scope|remote|websocket|long polling" internal/platform
```

<!-- locator:session -->
## Session

适用任务：session 创建/恢复/列表/归档/置顶/删除、Fork、模式切换、命名、过期清理、平台隔离、cron session 可见性。

先看：

- `internal/session/service.go`、`types.go`：Session 服务主体和领域请求/结果类型；字段写入统一走 `storage.SessionRepository.Mutate`（事务内读-改-写），`Resume` / `Touch` 的 `UpdatedAt` 也在事务内设置。
- `internal/session/shared.go`：群共享线程的 Session 所有者、`Scope.Shared` key、元数据标记与访问判定。
- `internal/session/mode.go`：模式激活和 work 历史限制。
- `internal/session/lifecycle.go`、`query.go`、`fork.go`、`expiration.go`：生命周期、查询、Fork 和闲置过期策略；重命名/归档/置顶与闲置过期都在事务内基于最新行重新判定（手动改名标记、空闲判定）；`Unarchive` 对后台 Session 同样执行提升。
- `internal/session/promotion.go`：后台 Session 的前台接管判定与提升。`IsBackground` 按 metadata `background_kind` 或 `cron:` / `elnis:` scope 前缀判断，`WasPromoted` 按 metadata `foreground_origin` 判断；`promoteToForeground` 必须在 `Sessions().Mutate` 事务内调用，一次性写 `foreground_origin`（记录接管前的 kind/owner/platform/scope）、删除 `background_kind`、切到当前前台 scope 并置 `Mode = work`。`Resume` / `Unarchive` 用它做原地提升，`canAccess` 用 `IsBackground` 放行后台 Session 的跨 scope 恢复。
- `internal/session/naming.go`：异步 Session 命名；迟到的命名写入在事务内检查 `title_renamed`，不覆盖手动改名。
- `internal/agent/session_metadata.go`：Session metadata 编解码；`encodeSessionMetadataInto` 基于原始 JSON 只改写已知字段，保留未知键。
- `internal/agent/workspace.go`：Agent workspace 持久化适配；`workspace_dir` 与通知目录经 `mutateMetadata` 在事务内合并。
- `internal/tool/workspace.go`：工具 workspace context 和路径解析。

常用搜索：

```bash
rg -n "Fork|Archive|Pinned|Expire|SessionMode|metadata|workspace|cron:" internal/session internal/agent internal/tool
```

<!-- locator:context -->
## Context、Prompt 与压缩

适用任务：上下文加载、压缩摘要、context window、usage 展示、system prompt source。

先看：

- `internal/contextmgr/`：按加载/Fork、窗口、usage、压缩器和摘要 prompt 拆分的上下文基础能力。
- `internal/agent/context_runtime.go`、`context_compact.go`、`context_seed.go`、`context_usage.go`：Agent 上下文运行态、独立 Session 压缩编排、首消息 seed 物化与 usage/动态阈值。
- `internal/agent/prompt.go`：Prompt Builder。
- `internal/agent/system_prompt*.go`：system prompt 管理和来源。
- `internal/llm/segment.go`：MessageSegment helper。

常用搜索：

```bash
rg -n "ContextLoader|Compress|Window|System Prompt|MessageSegment|usage" internal/contextmgr internal/agent internal/llm
```

<!-- locator:llm -->
## LLM Adapter

适用任务：LLM 抽象、OpenAI-compatible 请求（chat / responses 两种协议）、SSE、usage、reasoning、tool call delta、多模态消息转换。

先看：

- `internal/llm/`：LLM 抽象和 MessageSegment。
- `internal/llm/openai/`：OpenAI-compatible adapter。`openai.go` 是 Chat Completions 适配器（`{base_url}/chat/completions`），`responses.go` 是 Responses 适配器（`{base_url}/responses`，内嵌前者以复用重试、`/models`、SSE 行扫描与 `parseError` 错误分类，只替换请求信封和流式事件翻译）；`[providers.*].api_mode` 决定用哪个，两者对上层输出同一套 `llm.StreamChunk`。
- `internal/agent/model.go`：模型运行态、模型切换、provider client 缓存；`Agent.ModelProfiles()` 把 `services.toml` 的 `model_profiles` / `aliases` 汇成 `/model --profiles` 的列表（按别名键列出用户实际输入的名字，`Available` 反映 provider 在当前进程里有没有 client）。
- `internal/agent/chat_llm.go`：Agent LLM 调用适配。

常用搜索：

```bash
rg -n "ChatCompletion|Stream|SSE|reasoning|usage|ToolCall|MessageSegment|Models" internal/llm internal/agent
```

<!-- locator:storage -->
## Storage 与 SQLite

适用任务：领域模型、repository interface、migration、session/message/context summary/chat history/cron/elnis/tool call 持久化。

先看：

- `internal/storage/storage.go`：领域模型和 repository interfaces；Message 使用 `content` 作为纯文本快速路径，`segments` 保存可选多模态正文；可选 `ChatHistoryRangeRepository` 提供批量历史，`OutboundMessageRepository` 保存实际发送的 assistant 消息。`SessionRepository.Mutate` 在单个事务里读-改-写同一行并返回最新 Session，是会话字段的唯一并发安全写入口；`Update` 只用于测试夹具。
- `internal/storage/id.go`、`internal/storage/time.go`：通用 ID/时间 helper。
- `internal/storage/sqlite/`：SQLite store、migration 和 repository 实现；`chat_history_repository.go` 内含 `outbound_messages` 表；`session_repository.go` 的 `Mutate` 用 `sessionSelectByID` / `sessionUpdateStatement` / `sessionUpdateArgs` 与 `Get` / `Update` 共享同一份行读写语句。

常用搜索：

```bash
rg -n "Migration|Repository|Upsert|List|Archive|Fork|ToolCall|CronJob|ElnisEvent" internal/storage
```

<!-- locator:elnis -->
## Elnis / Elvena / Elwisp

适用任务：Elvena 协议类型、Elnis HTTP/鉴权/去重/事件准备、direct/llm 投递、segments、background、Elwisp 文档或指南工具。

先看：

- `internal/elvena/`：公共协议层。
- `internal/elnis/`：Elnis HTTP、鉴权、准备、投递和后台任务；`outbox.go` 负责 LLM 报告持久化投递、重试与恢复。
- `internal/elnis/media.go`：Elvena 媒体来源校验、按需入库、宿主 workspace 双向读写；通过 `media.Manager.MaterializeWithLimits` 传递本次接收限制，复用共享 Manager；发送回执缓存由 Agent 通用边界处理。
- `internal/media/lifecycle.go`、`reference.go`：引用保护、1 小时孤儿宽限期和可重试后端清理；`internal/storage/sqlite/media_lifecycle.go`：输出关联、清理认领、启动恢复和只读一致性检查；`media_json_test.go`、`media_fork_test.go` 验证媒体 JSON 引用事务和 fork 检查点范围。
- `internal/storage/sqlite/elnis_event_repository.go`：Elnis event 与 report outbox 的事务、claim、receipt 和完成状态持久化。
- `internal/background/`：cron/Elnis 共用后台 LLM 类型与结果 helper。
- `internal/tool/builtin/elwisp_creator.go`：Elwisp 创建指南工具。
- `docs/elnis.md`
- `docs/elnis-usage.md`
- `devdocs/elnis-elwisp.md`

常用搜索：

```bash
rg -n "Elvena|Elwisp|/elvena/v2/events|direct|segments|session_mode|background" internal/elvena internal/elnis internal/background docs devdocs
```

<!-- locator:cron -->
## Cron 与维护任务

适用任务：中央 Cron Runtime、LLM 可编排 cron 服务、维护类清理任务、cron 工具。

先看：

- `internal/cron/service.go`：Cron Service 装配、CRUD 与公开入口。
- `internal/cron/model.go`：任务 Metadata、Delivery 状态类型、校验与规范化。
- `internal/cron/execution.go`：Direct/LLM 执行、报告生成和 JSON 格式重试。
- `internal/cron/delivery.go`：逐目标逐输出发送、状态持久化、降级与 receipt mapping。

- `internal/cron/recovery.go`：平台连接跟踪、过期 once 扫描与补发入口。
- `internal/maintenance/`
- `internal/agent/cron*.go`：Agent 后台 runner 和后台工具确认特例。
- `internal/tool/builtin/cron.go`：cron 内置工具。
- `internal/storage/sqlite/cron_job_repository.go`：cron job 持久化。

常用搜索：

```bash
rg -n "Cron|Job|Schedule|RunCron|maintenance|include_completed|tool_list_names" internal/cron internal/maintenance internal/agent internal/tool internal/storage
```

<!-- locator:docs -->
## 文档与变更记录

适用任务：用户文档、开发文档、自动翻译流程、changelog。

先看：

- `docs/`：中文用户文档。
- `devdocs/`：维护者/Agent 开发文档。
- `scripts/translate_docs.py`：用户文档增量翻译脚本。
- `CHANGELOG.md`：中文变更记录。

不要手动修改：

- `docs.en/`
- `README.md`
- `CHANGELOG.en.md`

常用搜索：

```bash
rg -n "locator:|CHANGELOG|docs.en|translate" AGENT.md docs devdocs scripts
```

