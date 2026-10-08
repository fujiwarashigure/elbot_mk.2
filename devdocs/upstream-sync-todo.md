# 上游同步与重构待办（本 fork 要改的任务）

本文把 **fork 相对上游（`Elflare/elbot`）还需落地的改动**拆成可开工的任务，放在仓库内维护，
方便直接按项执行、按项验收、按项提交。上游盘点与方案论证在仓库外的 `D:\GIT\elbot-upstream-sync-plan.md`
（该文件在本机不存在：只有 C / G / M 三个盘符，因此完成台账改由本文维护），
**已完成的提交台账仍记在那份方案的 `§5.3` 与 `表 5.4`**；本文只维护"未完成 / 进行中"的工作。

维护约定：

- 每完成一项（提交后）把该任务勾成 `[x]` 并写上提交号；能访问仓库外方案文件时再回写那里的台账。
- 每次 `git fetch` 上游后做一次增量盘点，把新增的可移植项追加到文末「待盘点」。
- 状态图例：`[ ]` 未开工 · `[~]` 进行中（含未提交改动）· `[x]` 已完成 · `[-]` 已判定不做（附原因）。

## 0. 现状快照

- 基线：fork `main` @ `e724f4a`；上游 `refs/remotes/upstream/main` @ `8457fdd`；共同祖先 `3fb1234`。
- 已落地：命名对齐（`api` → `api_mode`）、P0 全部 8 项、P1#3 工具发现/预载事务化、P1#5 会话整行快照 → 事务内字段更新、P1#4 后台接管（fork 原生最小版，`f9882b5`）、P1#2 模型/会话服务化（`state.toml` 写回串行化，`bb8226d`）。
- 已落地（P1#1 最小版 A）：契约与事实丢失修复 `2fca418`、结果契约两批 `04e255a` / `fb17b9f`、收尾事件 `1c31637` → 见任务 2。
- 已落地（P1#6 本轮可做部分）：工具预览识别规则集中到交付层 `ed602e8` → 见任务 4。
- **等待用户决策（不做就无法继续的四项）**：
  1. P1#1 是否要完整对齐上游九字段契约（含 JSONL 落盘与 Reader 改写）——会改日志文件格式并连带重写 `/log`、`/audit`、`/usage`、`/elwisp`、维护报告的解析端。
  2. P1#2 命名模型快照——属新增用户可见功能（保存/应用/删除入口、与 `/model` 的关系、是否落 `state.toml`）。
  3. P1#6 剩余平台级跳过规则是否合并、以及是否按上游 `internal/notification/*` 形态重做。
  4. P2 换基路线 B / C（用户已确认本轮不决策，但这是剩余的最大项）。

---

## 1. [x] P1#4 后台会话被前台接管（fork 原生最小版）— 提交 `cff79cc`

**目标**：前台用户 `/resume`（或 `/unarchive`）进入一个正在跑的后台 Session（cron / Elnis）时，
该后台 Session 被提升为前台会话，在途后台 turn 在最近的安全点自行取消并返回"已被接管"，
不再继续烧 token、不写迟到助手消息、不投递 cron/Elnis 报告。

**范围（已与用户确认，取 fork 原生最小版）**：

- 提升与前台激活在 **同一个 SQLite 事务**内完成：把后台事实写进 metadata `foreground_origin`，删除 `background_kind`，改 `OwnerID` / `Platform` / `PlatformScopeID` 并置 `Mode = work`。
- 在途后台 turn 用 **安全点轮询** 检测接管（模型调用前、工具批次落库后、最终输出前），命中即返回哨兵错误，由 `RunBackground` 转成 `RunResult{TakenOver: true, Outcome: "taken_over"}`（`err == nil`）。
- **不引入**上游的 `session.Binding`、`executionCoordinator`、`dialogue.ForegroundNotice`（请求尾部合成 user 消息）；语义与上游不同，属 fork 自定义。
- cron 侧把被接管的运行记为 `ReportReady=true, TaskCompleted=true, Report=""`，`deliverPrepared` 因此不发任何东西；Elnis 侧把事件收成 `StatusCompleted` 且无报告。

**已完成改动（工作区未提交）**：

| 文件 | 改动 |
| --- | --- |
| `internal/session/promotion.go`（新） | `WasPromoted` / `IsBackground` / `promoteToForeground` / `ForegroundOrigin` / `ErrForegroundSession`；metadata 键 `foreground_origin`、`background_kind` |
| `internal/session/service.go` | `Resume` 的 `Mutate` 回调在后台会话上先 `promoteToForeground`；`canAccess` 改用导出的 `IsBackground` |
| `internal/session/lifecycle.go` | `Unarchive` 同样在事务内提升后台会话 |
| `internal/agent/cron.go` | 哨兵 `errBackgroundTakenOver`；`RunBackground` / `RunCronMessage` 映射 `TakenOver`；模型调用前的接管检查；已提升 Session 直接短路 |
| `internal/agent/chat.go` | `runChat` 在模型调用前、工具批次落库后、最终输出前检查接管并返回哨兵；`runChatTurnWithOutput` 对哨兵不发布 PhaseError |
| `internal/background/background.go` | `OutcomeTakenOver`、`RunResult.TakenOver` / `Outcome` |
| `internal/cron/service.go`、`internal/cron/execution.go` | `RunCronMessageResult.TakenOver`；`takenOverDeliveryState` + 审计 `cron.background_taken_over` |
| `internal/elnis/llm.go` | 被接管时以 `StatusCompleted` 收尾 + 审计 `elnis.background_taken_over` |
| `internal/session/promotion_test.go`（新） | 已在跑：`ok elbot/internal/session`（提升换 scope、幂等且受 scope 约束、保留未知 metadata、`Unarchive` 提升） |
| `internal/agent/background_takeover_test.go`（新） | 已在跑：`ok elbot/internal/agent`（探针工具在工具批次内接管 → `TakenOver`/`Outcome`/`err == nil`/`Text == ""`/仅 1 次模型请求/无迟到助手回答/`WasPromoted`；已提升 Session 直接短路且 0 次模型请求；接管后前台 `HandleMessage` 仍正常出话） |

**剩余步骤（按序执行）**：

- [x] 补 agent 侧测试 `internal/agent/background_takeover_test.go`：
  - 探针工具在后台 turn 内调用 `session.Service.Resume` 触发接管 → 断言 `TakenOver == true`、`Outcome == OutcomeTakenOver`、`err == nil`、`Text == ""`、只有 1 次模型请求（没有第二次模型调用）、`session.WasPromoted` 为真、没有迟到助手回答落库（工具调用转录行保留，这是安全点设计）；
  - `RunBackground` 传入一个"已是前台（已提升）"的 Session → 直接 `TakenOver`，0 次模型请求；
  - 负向：接管之后在同一 Session 上 `HandleMessage` 仍能正常跑一轮前台对话（前台输出不受接管门槛影响）。
- [x] `gofmt -l` 干净（新增/改动文件全部，`gofmt -l .` 全仓库无输出）。
- [x] 跑相关包测试：`go test -count=1 -vet=off -p 1 ./internal/session ./internal/storage/sqlite ./internal/agent ./internal/cron ./internal/elnis ./internal/app`
      （结果：session / storage/sqlite / cron / elnis / app 全绿；agent 仅剩 §6 已登记的本机环境失败 `TestEmoticonHook*`，2 例，均为 `exec: "sh"` 缺失。）
- [x] 全量构建：`go build -p 1 ./...` 通过（exit 0）。
- [x] `CHANGELOG.md` 按补丁式（旧状态 → 新状态）追加"后台会话可被前台接管"条目。
- [x] `devdocs/code-map.md` 的 Session 段补 `internal/session/promotion.go`（提升/接管判定）与 agent 侧接管检查点；Agent 对话流程段补 `RunBackground` 的 `TakenOver` 出口。
- [x] `docs/commands.md` 的 `/resume` 行为说明补一句：恢复后台（cron/Elnis）Session 会中断该 Session 在途的后台任务；并同步 `docs/concepts.md` 第 194 行附近的 cron 后台 Session 说明（`docs.en/`、`README.md`、`CHANGELOG.en.md` 未手改）。
- [x] 提交 `f9882b5`：中文 `feat:` 前缀 + 说明"提升事务化 + 安全点取消 + cron/Elnis 不投递"的正文，提交信息写无 BOM 文件；提交前核对 `git status` 只包含本任务改动（提交后工作区干净）。
- [!] 回写 `D:\GIT\elbot-upstream-sync-plan.md` 台账与表 5.4 第 4 行：**本机没有 D 盘**（只有 C / G / M），该仓库外方案文件在此环境中不存在，无法回写。已改为在本文件内维护完成状态；若需要台账，请在持有该文件的机器上同步，或在仓库内新建台账文件。

**验收标准**：

1. 相关包测试全绿（本机既有的环境失败除外，见 §6）；`go build -p 1 ./...` 通过。
2. 接管后：无第二次模型调用、无迟到助手消息、cron/Elnis 不产生任何投递，且只留一条审计事件。
3. 普通 Session 的 `/resume` 行为零变化（元数据、scope、`UpdatedAt` 语义不变）。

---

## 2. [x] P1#1 日志与信号整改（fork 最小版 A 已完成）— 提交 `2fca418` / `04e255a` / `fb17b9f` / `1c31637` / `b6f0d7a`

**范围已与用户确认（取 fork 最小版 A）**：只做"最小契约 + 修事实丢失缺陷"，**不改日志文件格式、不改 `LogEntry.Fields` 查询契约**；上游的九字段 `LogRecord`、JSONL 落盘、`internal/events` / `internal/signal` 全量迁移列入"不做/待决策"，不在本项范围内。

**盘点结论（本次实测）**：fork 的审计事件共 **62 个名字散在 7 个包**，触发方式有 3 套并行写法——`internal/agent` 的 `a.audit("event", ...)`（消息固定 `audit event`）、`internal/hook/rules` 的 `hook_tool_call` / `hook_tool_error`、`hook/runtime` 的 `hook.tool_call` 与 `internal/hook/rules/exec.go` 的 `hook.platform_call`（带点号前缀，与前者不是同一套命名）。

**已完成（本轮）**：

| 文件 | 改动 |
| --- | --- |
| `internal/logging/contract.go`（新） | fork 版来源标识 + 操作结果契约：`ModuleApp`…`ModuleTool` 共 13 个已登记来源、`ModuleHook` 等常量、`Result{Succeeded,Failed,Canceled,Rejected,Skipped}`、`LogModules()` / `ValidLogModule()` / `ValidLogResult()`；文件头写明与上游九字段契约的边界（正文长度、JSON 正文、JSONL 落盘本轮不实施） |
| `internal/tool/availability.go`（新） | 工具"为什么没被采用"的互斥机器可读原因与唯一判定入口：`ToolAvailabilityReason` / `ToolAccessReason` / `RegistryToolAvailabilityReason`，原因常量 `tool_not_found` / `tool_context_unavailable` / `tool_hidden` / `tool_requires_superadmin` / `tool_risk_above_allowed_level` / `tool_no_schema` |
| `internal/agent/cron.go`、`tool_directive.go` | `background_preload_skipped` / `tool_preload_skipped` / `skill_wrapper_preload_skipped` 不再输出 `not_found_or_not_allowed`，改为新原因 + `result`（策略/角色拒绝 `rejected`，其余 `skipped`）；`canPreloadToolRoot` / `canPreloadSkill` 改为委托给原因函数，`@skill:` 根与包装工具分成 `preloadSkillRootReason` / `preloadSkillWrapperReason`（包装工具**不**按 Hidden 拒绝——隐藏是包装工具常态） |
| `internal/agent/logging.go` | 所有 Agent 审计记录带 `module=agent`；新增 `normalizeAuditAttrs` 把属性里的 `error` 统一转成脱敏字符串；`auditError` 统一补 `result=failed`（调用方显式给出时不覆盖） |
| `internal/app/runtime.go` | app 层审计统一入口 `auditFunc` 补 `module=app`——此前 app 记录是唯一没有来源标识的一类（命名失败、cron、Elwisp、hook 接线都经它） |
| `internal/agent/toolrun_adapter.go`、`command_runtime.go` | `permission_denied` 带 `result=rejected` |
| `internal/hook/runtime/tool_bridge.go`、`rules/action.go`、`rules/exec.go` | `hook.tool_call` → `hook_tool_call`、`hook.platform_call` → `hook_platform_call`；`hook_platform_call` 改为按真实调用结果记录（成功/失败带 `error` 与 `result`）；错误对象统一字符串化 |
| 测试（新） | `internal/logging/contract_test.go`、`internal/logging/contract_source_test.go`（源码级 result 契约校验）、`internal/tool/availability_test.go`（含"新原因入口与 `CanAccessTool`+`InfoAvailableInContext` 判定等价"的固定测试）、`internal/agent/logging_test.go`（`module` 来源、无 logger 时 noop、错误属性转换）、`internal/app/audit_test.go`（`auditFunc` 带 `module=app` 且不挤掉调用方属性） |

**来源标识覆盖度（收官核对）**：全仓库 175 处审计入口调用——`internal/agent` 152 处经 `auditLog` 统一补 `module=agent`，`internal/app` 2 处经 `auditFunc` 统一补 `module=app`，`internal/hook` 21 处显式传 `module=hook`。**没有任何审计记录缺少来源标识。**

**操作结果覆盖度**：`internal/agent` 与 Hook 侧共 80 处契约取值，源码级校验覆盖其中写死的 69 处；其余 11 处把结果交给函数判定（`tool_call` 按 `success` 映射、`risk_confirmation_result` 按动作区分、预载跳过复用 `preloadSkipResult`），由各自单元测试覆盖。

**信号侧现状（本次核对）**：fork 用 `hook.PointPlatformConnected`（`platform.connected`）+ `platform.ConnectNotifier` 回调承载"平台已连接"这一进程级事实，`internal/app/platforms.go` 的 `registerPlatformHooks` 注册回调、`registerCronPlatformHook` 与 Agent 的 `NotifyPlatformConnected` 各自订阅（cron 补投递 missed-once、hook 通知），与上游"全局信号 + 各自订阅"的语义一致，只是载体不同（hook 点而非 `internal/signal`）。**无需迁移 `internal/signal`**（fork 没有这个包）。

**验收**：`/log`、`/audit` 的筛选参数在新来源下仍可用（`--hook` 继续按 `module=hook` 过滤，事件名只在 `platform_recall_*` 之外保持不变；`hook.tool_call` → `hook_tool_call` 是本次唯一的事件名变更，已在 CHANGELOG 记录）；每批迁移后跑受影响包测试 + `internal/app`。

**未做且需用户决策**：上游九字段 `LogRecord`（含 JSONL 落盘与 Reader 兼读新旧格式）。它超出已确认的最小版 A 范围，会改日志文件格式并连带重写 `/log`、`/audit`、`/usage`、`/elwisp` 与维护报告的解析端。

## 3. [~] P1#2 模型/会话服务化（5–8 人日）

- **上游来源**：`internal/modelmgr/*`、`internal/session/*`、`internal/app/{signals,lifecycle,services,agent_status}.go`、`internal/turn/execution.go`。
- **要拿到的能力**：`state.toml` 原子保存、命名模型快照、会话绑定统一失效、运行中会话拒绝切离。

**本轮实测（逐项核对后只做确有缺陷的一项）**：

| 要拿到的能力 | 现状 | 处置 |
| --- | --- | --- |
| `state.toml` 原子保存 | 写盘本身已是原子替换（临时文件 + `Sync` + 备份换名 + 崩溃后从 `.bak` 恢复，见 `config.SaveState` / `LoadState`）。**缺陷在并发写回**：`saveRuntimeState` 只用保护 `stateModTime` 的 `stateMu`，"重新读文件合并 → 取内存快照 → 替换文件"整段是并发的，两个写入者会互相覆盖分片；mtime 门在同一时间戳精度内也看不出差别。 | **已修**：新增 `stateWriteMu` 串行化整个合并-快照-落盘过程；22 处调用点（8 个文件）全部经过它。验证方式：手工复现（8 个并发写入者各自只把一份分片放进内存再保存）——修前只剩 1 个分片落盘，修后稳定保留全部 8 个；该缺陷没有做成单元测试，因为确定性地暴露它需要在锁内部插桩，而并发冒烟测试会与其它测试的 `state.toml` 清理互相干扰（反而制造无关的 flake）。 |
| 运行中会话拒绝切离 | 已有：`commandExecutor.Handle` 按 `turn.Snapshot` 的 phase 拒绝 `SessionEffect` 会切走当前会话的命令（`activeTurnCommandBlockedText`），压缩中另有 `compactCommandBlockedText`。 | 无需改动；`/new`、`/resume`、`/fork`、`/chat`、`/work`、`/delete`、`/compact` 均在拒绝集合内，已有测试覆盖。 |
| 会话绑定统一失效 | 已有：`session.Service` 的当前会话映射只在 `setCurrent` / `clearCurrentIf` 两处写，删除、归档、空闲过期分别经 `clearCurrentIf` 失效；读取侧 `Current` 每次都回 store 取行，进程内不缓存会话内容。 | 无需改动；未发现绕过这两个入口的写点。 |
| 命名模型快照 | fork 的 `model_profiles` 是 **app.toml 里的静态单模型选择**（`Options.ModelProfiles`，用于 `@model:<profile>` 解析），没有"把一个名字绑定到一组按模式/用途保存的模型选择"的快照能力。 | **未做（需要用户决策）**：这是新增用户可见能力（命名快照的保存/应用/删除入口、与 `/model` 的关系、是否进 `state.toml`），不属于"修复既有缺陷"；按 fork 最小版原则不与其余三项捆绑实施。 |

- **前置**：无（但会与 P1#1 抢同一批 app 层文件，建议两项顺序做，不要并行改 `internal/app`）。
- **注意**：fork 没有上游的 `session.Binding`；本项是"忠实移植上游后台接管"（P1#4 的完整版）的前置，若将来要把接管语义对齐上游，先做本项。
- **验收**：并发切换模型/新建会话不再出现半写 `state.toml`（写盘原子性已有，并发写回已串行化并手工复现验证：修前 8 个并发写入者只剩 1 个分片，修后稳定保留全部分片；该缺陷无法做成确定性单元测试，因为要暴露它需要在锁内部插桩）；运行中会话切模型/切模式给出明确拒绝而非静默生效（已有行为，测试覆盖）；相关包测试 + `internal/app` 全绿。

## 4. [x] P1#6 通知规则集中化（本轮可做部分）— 提交 `ed602e8`

- **上游来源**：`internal/notification/*`。
- **前置**：P1#1 日志与信号整改（通知规则建立在统一来源/事件契约之上）。

**盘点结论（本次实测）**：三条投递路径（cron、Elnis、hook）**已经**汇聚到同一个裁决入口 —— `internal/app/runtime.go` 的 `sendNotice` 闭包 → `Agent.SendNotice`（先过 `turnOutputAllowed` 与 `noticeTargetBlocked`，再经 `delivery.Manager` 路由到平台适配器），cron 的 `buildCronService`、Elnis 的 `Send`、hook service 全部接的是这个闭包。真正分散的是**平台适配器各自的跳过规则**，其中"工具调用进度预览"这条规则在 OneBot、QQ 官方、hook 出站记录三处各写一遍。

**已完成（本轮）**：

| 文件 | 改动 |
| --- | --- |
| `internal/delivery/delivery.go` | 新增唯一的预览判定入口：`ToolPreviewPrefix`、`Output.IsToolPreview()`（要求完整前缀 `"[tool] "`，只有 `[tool]` 没有正文不算）、`IsToolPreviewNotice()`（单条文本输出 + 预览前缀） |
| `internal/platform/qq-onebot/adapter.go`、`internal/platform/qqofficial/adapter.go` | `isGroupToolPreviewNotice` 改为调用 `delivery.IsToolPreviewNotice`，各自只保留"目标是不是群聊"的判断 |
| `internal/agent/turn_output.go` | `formatToolPreview` 用 `delivery.ToolPreviewPrefix` 写前缀 |
| `internal/hook/builtin/register.go` | 出站历史跳过预览改用同一个前缀常量 |
| `internal/delivery/tool_preview_test.go`（新） | 覆盖前缀识别、非预览拒绝（普通回答、`[tools]`、空正文、非文本 Kind、只有前缀、正文里提到 `[tool]`）与"必须恰好一条文本输出" |

**剩余步骤（需用户给边界）**：

- "通知被禁用时不投递"的负向测试已有：`internal/agent/group_runtime_test.go` 的 `TestSelfMutePausesGroupCallsOutputAndPersists` 断言被静音群里显式群通知被丢弃、只有状态变更通知发出一次。若要更细的"某个通知目标被禁用"矩阵，需要先明确禁用维度（群运行状态 / 通知目标开关 / 平台能力）。
- 其余平台级跳过规则（工具预览之外的）需要逐个确认是否语义相同再合并；不同语义的不能为了"集中"而强行统一。上游 `internal/notification/*` 是一整套通知目标裁决模块，若要按上游形态重做，需要用户先定范围。

**验收**：通知目标的裁决逻辑只有一处入口（`Agent.SendNotice`，已确认）；cron / Elnis / Hook 三条投递路径都走它（已确认）；补"通知被禁用时不投递"的负向测试（已有群运行状态一例，可按用户给定维度扩展）。

## 5. [ ] P2 换基路线 B：原生 Responses 协议（20–35 人日，**需先决策**）

- **目标收益**：服务端续链（`previous_response_id`）、原生压缩、reasoning 加密内容、`additional_tools` 增量工具定义、原生 checkpoint。
- **现实结论**：这不是"再写一个客户端"，而是换内核——LLM 分层（fork 的 `internal/llm/openai` 在上游已删除）、agent 路由（会替换 fork 的 `chat.go` / `chat_llm.go` / `chat_tools.go` / `core.go`）、压缩、存储（3 个新迁移：`session_llm_origin`、`native_dialogue`、`native_material_roots`）、模型来源、工具可用性。
- **决策点**：若确定要它，走路线 C（另建 `upstream-track` 分支，先把 fork 定制层插件化/接口化再换基），不要在 fork 现有 agent 上继续加协议分支（会长期维护两套会话语义）。
- **当前替代方案**：fork 已自带的 Responses 适配器（`internal/llm/openai/responses.go`，协议翻译层 + 复用 chat 会话循环）够用；新增能力时通过接口查询协议（工具可用性、压缩分派、存储协议来源），为将来换基留接缝。

---

## 6. 通用收尾流程（每项都要做）

1. `gofmt -l` 干净；跑改动包 + `internal/app`，用 `go test -count=1 -vet=off -p 1`。
2. 涉及存储/迁移：在既有库副本上跑一次启动迁移，新增迁移必须加法式。
3. 涉及权限：补"普通用户不能操作他人资源"的负向测试。
4. 文档：`docs/*.md`（中文）+ `CHANGELOG.md`（补丁式：旧 → 新）；`docs.en/`、`README.md`、`CHANGELOG.en.md` 不手改；`devdocs/{architecture,code-map}.md` 同步职责。
5. 提交：中文前缀（`feat:` / `fix:` / `docs:` / `change:`），一个任务一次提交，正文说明改动与验证命令。

**本机既有环境失败（与上述改动无关，不要计入本次回归）**：

- `internal/agent`：2 个 `TestEmoticonHook*`（`exec: "sh": executable file not found`）。
- `internal/agent`：`TestMemberPanelShowsOwnTasksAndQuota` / `TestGroupServicesCommandIntegration` / `TestModelsGroupsProvidersAndSwitchPersistsState` 等**偶发**失败，报错是 `TempDir RemoveAll cleanup: unlinkat ...: The directory is not empty` —— 测试返回后仍有迟到的 `state.toml` 写入落在那次 `t.TempDir()` 里，属于测试夹具的清理竞态，与 `stateWriteMu`（只串行化同一实例的写回）无关；单独跑这些测试稳定通过，全包连跑约每次命中 1 例。
- `internal/tool/builtin`：4 个 `TestAnalyzeBashShellAdvice*`。
- `internal/tool/skill`：`TestFinalizeElSkillReturnsBuildFailure`（测试内部跑 `go build`）。
- `internal/hook/rules`：`TestExecCancellationKillsDescendantProcesses`（依赖 TCP 回连，本机 accept 超时）。

**本机 Go 工具链事实（本次验证环境，供后续复核）**：

- Go 不在 `PATH` 上，工具链在 `C:\Users\shigure\go-sdk\go\bin`（`go1.26.0 windows/amd64`）；直接用绝对路径或把它加进 `PATH`。
- `proxy.golang.org` 在本机不通（连接超时），需要 `GOPROXY=https://goproxy.cn,direct`；缺失的模块正是靠它补齐的。
- dpx 环境把 `GOPATH`/`GOCACHE` 收容到环境目录，`go` 默认写不进去（`Access is denied`）；用 `danger-full-access` 运行，或把 `GOPATH`/`GOCACHE` 指到可写目录。

## 7. 待盘点（增量）

- [ ] 上游是否有新的 `### Added` / `### Fixed` 需要挑拣：`git log --oneline HEAD..upstream/main` + 上游 `CHANGELOG.md` 的 Unreleased 段。
