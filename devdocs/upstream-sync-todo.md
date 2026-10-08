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
- 已落地（P1#1 最小版 A）：契约与事实丢失修复 `2fca418`、结果契约两批 `04e255a` / `fb17b9f`、收尾事件 `1c31637`、app 来源标识 `e6fa8c6`、端到端验证 `7a762d8` → 见任务 2。
- 已落地（P1#6 本轮可做部分）：工具预览识别规则集中到交付层 `ed602e8` → 见任务 4。
- **状态**：任务 1–4（P1#4 / P1#1 / P1#2 / P1#6）在 fork 选定范围内均已完成，无未勾选项。以下四项是**可选增强或本项之外的新功能**，不做也不影响上述任务的完成度：
  1. P1#1 是否要完整对齐上游九字段契约（含 JSONL 落盘与 Reader 改写）——会改日志文件格式并连带重写 `/log`、`/audit`、`/usage`、`/elwisp`、维护报告的解析端。**建议不做**：最小版 A 已让来源与结果可筛选，额外收益主要是内部整洁，代价是破坏现有日志查询。
  2. 命名模型快照——属新增用户可见功能（保存/应用/删除入口、与 `/model` 的关系、是否落 `state.toml`）。若要，建议最小形态：只做保存/应用两个入口、落 `state.toml`、不新增 `/model` 语法。
  3. P1#6 剩余平台级跳过规则是否合并、以及是否按上游 `internal/notification/*` 形态重做——需先逐个确认语义是否相同，语义不同的不能为了"集中"强行统一。
  4. P2 换基路线 B / C（用户已确认本轮不决策）——决策材料见 [`p2-native-responses-decision.md`](p2-native-responses-decision.md)。

---

## 1. [x] P1#4 后台会话被前台接管（fork 原生最小版）— 提交 `f9882b5`

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

## 2. [x] P1#1 日志与信号整改（fork 最小版 A 已完成）— 提交 `2fca418` / `04e255a` / `fb17b9f` / `1c31637` / `e6fa8c6`

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

## 3. [x] P1#2 模型/会话服务化（四项能力全部落实，含后续追加的命名模型快照）

> 四项能力中一项（`state.toml` 并发写回）已修复，另三项（写盘原子性、运行中会话拒绝切离、会话绑定统一失效）经核对 fork 原本就具备。表中"命名模型快照"一行原本单列等用户决策，**用户已批准后已实施**（见本节的"命名快照（后续追加）"）。

- **上游来源**：`internal/modelmgr/*`、`internal/session/*`、`internal/app/{signals,lifecycle,services,agent_status}.go`、`internal/turn/execution.go`。
- **要拿到的能力**：`state.toml` 原子保存、命名模型快照、会话绑定统一失效、运行中会话拒绝切离。

**本轮实测（逐项核对后只做确有缺陷的一项）**：

| 要拿到的能力 | 现状 | 处置 |
| --- | --- | --- |
| `state.toml` 原子保存 | 写盘本身已是原子替换（临时文件 + `Sync` + 备份换名 + 崩溃后从 `.bak` 恢复，见 `config.SaveState` / `LoadState`）。**缺陷在并发写回**：`saveRuntimeState` 只用保护 `stateModTime` 的 `stateMu`，"重新读文件合并 → 取内存快照 → 替换文件"整段是并发的，两个写入者会互相覆盖分片；mtime 门在同一时间戳精度内也看不出差别。 | **已修**：新增 `stateWriteMu` 串行化整个合并-快照-落盘过程；22 处调用点（8 个文件）全部经过它。验证方式：手工复现（8 个并发写入者各自只把一份分片放进内存再保存）——修前只剩 1 个分片落盘，修后稳定保留全部 8 个；该缺陷没有做成单元测试，因为确定性地暴露它需要在锁内部插桩，而并发冒烟测试会与其它测试的 `state.toml` 清理互相干扰（反而制造无关的 flake）。 |
| 运行中会话拒绝切离 | 已有：`commandExecutor.Handle` 按 `turn.Snapshot` 的 phase 拒绝 `SessionEffect` 会切走当前会话的命令（`activeTurnCommandBlockedText`），压缩中另有 `compactCommandBlockedText`。 | 无需改动；`/new`、`/resume`、`/fork`、`/chat`、`/work`、`/delete`、`/compact` 均在拒绝集合内，已有测试覆盖。 |
| 会话绑定统一失效 | 已有：`session.Service` 的当前会话映射只在 `setCurrent` / `clearCurrentIf` 两处写，删除、归档、空闲过期分别经 `clearCurrentIf` 失效；读取侧 `Current` 每次都回 store 取行，进程内不缓存会话内容。 | 无需改动；未发现绕过这两个入口的写点。 |
| 命名模型快照 | fork 的 `model_profiles` 是 **services.toml 里的静态单模型选择**（用于 `@model:<profile>` 解析），没有"把一个名字绑定到一组按模式/用途保存的模型选择"的快照能力。 | **已实施（用户批准后追加）**：见下。 |

**命名快照（后续追加，用户批准后实施）**：

| 文件 | 改动 |
| --- | --- |
| `internal/config/config.go` | `StateConfig.ModelSnapshots map[string]StateModelSnapshot`（`[model_snapshots]`）+ `StateModelSnapshot{ModeModels, CompactModel, NamingModel}`；注释写明它为什么在 `state.toml` 而不是只读共享的 `app.toml` / `services.toml` |
| `internal/agent/model_snapshot.go`（新） | `ModelSnapshots` / `SaveModelSnapshot` / `ApplyModelSnapshot` / `DeleteModelSnapshot`；名字校验（字母/数字/`_`/`-`，≤32）、"不与静态 profile/alias 同名"的拒绝、应用前整体校验 provider（缺一个就整条拒绝、不做部分切换）、应用顺序固定（`work` 最后，因为 `applyModeModelSelection` 会把最后一次的 provider 记为进程主 provider） |
| `internal/agent/core.go` | `modelSnapshotsMu` + `modelSnapshots`（运行态内存副本） |
| `internal/agent/model.go` | 快照接入 `applyRuntimeState` / `saveRuntimeState` / `runtimeStateDigest` / `runtimeStateSections`（因此参与外部编辑热加载并出现在 `/state` 的变更摘要里）；新增 `mergeExternalRuntimeState`，并让 `SelectModelForMode` / `SelectCompactModel` / `SelectNamingModel` 在改内存**之前**先合并外部编辑 |
| `internal/agent/commands/model.go`、`register.go` | `/model --snapshots` / `--save <名字>` / `--apply <名字>` / `--delete <名字>`；`--apply` / `--delete` 后面补全已保存的快照名（`Kind: "model_snapshot"`），`optionOnlyModelArgs` 覆盖新选项；`ModelService` 增加四个方法 |
| `internal/agent/model_snapshot_test.go`（新） | 落盘回读、槽位顺序、静态 profile 同名拒绝、非法名字、整体应用（含"work 最后应用"断言）、未知 provider 不做部分应用、缺名报错区分 profile 与快照、外部编辑先合并（apply 用过期快照必须失败、save 不被外部编辑吞掉）、删除落盘、跨 Agent 重载后可应用 |
| `internal/agent/commands/model_test.go` | 6 个新命令测试：save/apply/delete/snapshots 的输出与不切换模型、失败透传、`--apply` 后补全快照名而不是模型名、`--sna` 补全选项 |

- **前置**：无（但会与 P1#1 抢同一批 app 层文件，建议两项顺序做，不要并行改 `internal/app`）。
- **注意**：fork 没有上游的 `session.Binding`；本项是"忠实移植上游后台接管"（P1#4 的完整版）的前置，若将来要把接管语义对齐上游，先做本项。
- **验收**：并发切换模型/新建会话不再出现半写 `state.toml`（写盘原子性已有，并发写回已串行化并手工复现验证：修前 8 个并发写入者只剩 1 个分片，修后稳定保留全部分片；该缺陷无法做成确定性单元测试，因为要暴露它需要在锁内部插桩）；运行中会话切模型/切模式给出明确拒绝而非静默生效（已有行为，测试覆盖）；命名快照的保存/应用/删除有落盘级测试，且"外部编辑后紧接着的内部改动不被吞掉"有专门测试；相关包测试 + `internal/app` 全绿。

## 4. [x] P1#6 通知规则集中化（两轮已完成：`ed602e8` + `66826c7`）

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

**剩余步骤（已按用户给定的边界做完第二轮，见下）**：

- "通知被禁用时不投递"的负向测试已有：`internal/agent/group_runtime_test.go` 的 `TestSelfMutePausesGroupCallsOutputAndPersists` 断言被静音群里显式群通知被丢弃、只有状态变更通知发出一次。没有再做"某个通知目标被禁用"矩阵——禁用维度（群运行状态 / 通知目标开关 / 平台能力）里的目标开关与平台能力目前各自只有一处实现，做成矩阵只是把同一判定抄成表，不增加保护。
- 其余平台级跳过规则已逐个盘点（第二轮），结果见下面的表。

**第二轮：平台级跳过规则盘点与合并（提交 `66826c7`）**：

| 规则 | 出现位置 | 语义 | 处置 |
| --- | --- | --- | --- |
| 群聊里不投递工具调用进度预览 | `qq-onebot`、`qqofficial` | **完全相同**：通知无显式目标 + 当前上下文是群聊 + 该通知就是预览（单条文本 + `"[tool] "` 前缀）⇒ 静默丢弃、不算失败 | **已合并**：规则整体收敛为 `delivery.ShouldDropGroupToolPreview(target, outputs, groupContext)`，两个适配器只保留 `isGroupContext`（各自的上下文键类型不同：OneBot 看 `target.MessageType`，QQ 官方看 `sendTarget.Kind`） |
| 空白文本不发 | `qq-onebot`、`qqofficial`、`telegram` 各自的 `sendText` / `sendContextText` | 语义相同（`strings.TrimSpace(text) == ""` ⇒ 空回执），但只有一行、无共享状态 | **不合并**：抽成公共函数不增加任何保护，只是把 `TrimSpace` 换个名字；三处已用同一写法 |
| 主动消息被禁用时报错 | `qqofficial`（`Proactive && !allowProactive()`） | 平台配置特有 | **保持平台内**：只有 QQ 官方有"主动消息"概念 |
| 本地 CLI / headless 不过滤 | `cli`、`headless` | CLI 的预览就是终端输出；headless 无投递 | **不改** |
| Telegram 不过滤群聊预览 | `telegram` | **与 QQ 两处不同**：Telegram 从未有这条规则（QQ 侧来自 `8129dd8 "qqonebot will not send notify in group"`） | **未统一**：无法从代码断定是"漏移植"还是"Telegram 群聊就是要看进度"，按"不同语义不强行统一"保留原行为，**留给用户决策**（若确认要一致，加一行 `delivery.ShouldDropGroupToolPreview(...)` + 目标是否群聊的判断即可） |

**验收**：通知目标的裁决逻辑只有一处入口（`Agent.SendNotice`，已确认）；cron / Elnis / Hook 三条投递路径都走它（已确认）；平台级跳过规则中"语义相同"的那条已只有一份实现，且边界（群聊普通通知照发、私聊预览照发、显式目标预览照发、多输出不算预览）都有测试固定。

## 5. [x] P2 决策：**不换基，走路线 D（按需补接口）**——三个接缝已实施

**决策材料：[`p2-native-responses-decision.md`](p2-native-responses-decision.md)**（提交 `2a26c17`）。**用户已决策：本轮按路线 D 补接口，不做换基（路线 B/C 暂不启动）。**

- **目标收益**：服务端续链（`previous_response_id`）、原生压缩、reasoning 加密内容、`additional_tools` 增量工具定义、原生 checkpoint。
- **现实结论**：这不是"再写一个客户端"，而是换内核——LLM 分层（fork 的 `internal/llm/openai` 在上游已删除）、agent 路由（会替换 fork 的 `chat.go` / `chat_llm.go` / `chat_tools.go` / `core.go`，正是 P0/P1 刚加固过的地方）、压缩、存储（3 个新迁移：`session_llm_origin`、`native_dialogue`、`native_material_roots`）、模型来源、工具可用性。
- **取证结论（本轮再次确认）**：fork 的 Responses 支持是协议翻译层——`store=false` 明确写在 `responses.go` 注释里（重放完整历史、从不读回已存响应），`previous_response_id` 与 `additional_tools` 在全仓库出现 **0** 次，压缩仍是客户端的。因此新增的协议能力查询接口对 Responses 适配器**如实报告"服务端能力全 false"**，而不是照抄协议文档。
- **为什么不做 B**：会替换的 `chat*.go` / `core.go` 正是前几轮刚加固的地方（后台接管安全点、工具白名单、事务化字段更新、预载事实区分、预算预占），在同一个文件上换内核等于把刚建立的回归保护推倒重来；20–35 人日的成本也远超短期省下的 token。

**路线 D 已实施（三个接缝，全部不改变现有行为）**：

| 接缝 | 实现 | 换基时省掉什么 |
| --- | --- | --- |
| 协议能力查询 | `internal/llm/protocol.go`：`Protocol`（`chat_completions` / `responses`）、`ProtocolCapabilities`（`ServerSideConversation` / `NativeCompaction` / `IncrementalTools` / `ServerSideStore`）、可选接口 `ProtocolCapabilityReporter`、保守入口 `ProtocolCapabilitiesOf(client, model)`。Chat 适配器报告 chat、Responses 适配器报告 responses（服务端能力全 false），`internal/app/models.go` 的 `protocolRouter` **按模型**回答（同一 provider 混用两种协议时以实际适配器为准） | 不必在 agent 各处写 `if protocol == responses` 式判断；换基只需改这里的返回值 |
| 压缩分派点 | `internal/contextmgr`：`Compactor` 接口（`Name` / `Compact` / `SummarizeText`）+ `CompactionChoice`；`contextRuntimeState.compactorFor(provider, model)` 按协议能力选后端，目前恒定选中客户端后端 `client_summary`；协议声称支持服务端原生压缩而 fork 没有实现时，**显式回退**并在压缩后写一条审计（`event=context_compaction_backend`，含 `backend` / `reason`），而不是静默失效或静默假装走了服务端路径 | 新增服务端压缩时只加一个 `Compactor` 实现并在分派点登记，不动压缩流程 |
| 存储协议来源 | 会话 metadata 的 `llm_origin`（`{protocol, provider, model}`，`internal/agent/session_metadata.go`）：每轮模型调用后由 `recordLLMOrigin` 记录，值未变时**不写事务**。上游对应的是 `session_llm_origin` 迁移；fork 用加法式 metadata 字段承载同一事实，避免为一个诊断字段引入 schema 迁移 | 换基时"哪些会话能续链/需要迁移"有据可查，不必考古 |

**验收**：`gofmt` 干净、`go build -p 1 ./...` 通过；`internal/llm`、`internal/app`、`internal/contextmgr`、`internal/agent` 相关测试通过；新增测试覆盖"未实现查询接口时保守默认值""按模型回答协议能力""Responses 适配器不虚报服务端能力""协议声称原生压缩但没有后端时必须回退且留下审计""`llm_origin` 只在变化时写入"。

**仍未做（明确不在本轮范围）**：服务端续链、原生压缩实现、reasoning 加密载荷、`additional_tools`、`native_dialogue` / `native_material_roots` 迁移；`internal/llm/openai` 的协议翻译与会话语义拆分（路线 C 的第 2 步）也未启动。

---

## 6. 通用收尾流程（每项都要做）

1. `gofmt -l` 干净；跑改动包 + `internal/app`，用 `go test -count=1 -vet=off -p 1`。
2. 涉及存储/迁移：在既有库副本上跑一次启动迁移，新增迁移必须加法式。
3. 涉及权限：补"普通用户不能操作他人资源"的负向测试。
4. 文档：`docs/*.md`（中文）+ `CHANGELOG.md`（补丁式：旧 → 新）；`docs.en/`、`README.md`、`CHANGELOG.en.md` 不手改；`devdocs/{architecture,code-map}.md` 同步职责。
5. 提交：中文前缀（`feat:` / `fix:` / `docs:` / `change:`），一个任务一次提交，正文说明改动与验证命令。

**本机既有环境失败（与上述改动无关，不要计入本次回归）**：

- `internal/agent`：2 个 `TestEmoticonHook*`（`exec: "sh": executable file not found`）。
- `internal/agent`：`TestMemberPanelShowsOwnTasksAndQuota` / `TestGroupServicesCommandIntegration` / `TestModelsGroupsProvidersAndSwitchPersistsState` / `TestGroupKnowledge*` / `TestReminderCreateListRemoveAndDispatch` / `TestLearningModerationActionsAreGranularAndRevocable` 等**偶发**失败，报错是 `TempDir RemoveAll cleanup: unlinkat ...: The directory is not empty` —— 测试返回后仍有迟到的 `state.toml` 写入落在那次 `t.TempDir()` 里，属于测试夹具的清理竞态，与 `stateWriteMu`（只串行化同一实例的写回）无关；单独跑这些测试稳定通过，全包连跑约每次命中 1 例。
  - **归属已实测**：用 `git worktree add` 检出改动前的 `fe0d08a`，在同样的并行负载下（`./internal/agent/ ./internal/tool/builtin/ ./internal/platform/refcontext/`）跑 4 轮，同样出现 `TestLearningModerationActionsAreGranularAndRevocable` / `TestModelsGroupsProvidersAndSwitchPersistsState` 等偶发失败；单独跑 `./internal/agent/` 3 轮则稳定只有 2 个 `TestEmoticonHook*`。因此这些 flake 是既有的负载敏感问题，不是本次改动引入。
- `internal/tool/builtin`：4 个 `TestAnalyzeBashShellAdvice*`，以及 `TestShellToolUsesConfiguredEnvironment` / `TestResolveWindowsShellCachedAndValid` / `TestShellMediaInputsAndCleanup`（本机 `bash` 来自 WSL 但 `sh` 不存在、且 shell 解析结果随 PATH 变化）。
- `internal/tool/skill`：`TestFinalizeElSkillReturnsBuildFailure`（测试内部跑 `go build`）。
- `internal/hook/rules`：`TestExecCancellationKillsDescendantProcesses`（依赖 TCP 回连，本机 accept 超时）。
- `internal/llm/openai`：`TestChatStream_ActiveStreamCompletesWithoutResponseTimeout` 等基于真实计时的用例在并行跑全量时偶发失败（单独跑 3 轮均通过）。
- `internal/character` / `internal/request`：偶发 `[build failed]`，原因是 `go test` 写测试二进制时被拒（`...\eac-beta\tmp\go-build*\b001\*.test.exe: Access is denied`），属本机 Go 构建缓存/临时目录权限问题；`go vet` 这两个包通过，说明代码本身可编译。

**本机 Go 工具链事实（本次验证环境，供后续复核）**：

- Go 不在 `PATH` 上，工具链在 `C:\Users\shigure\go-sdk\go\bin`（`go1.26.0 windows/amd64`）；直接用绝对路径或把它加进 `PATH`。
- `proxy.golang.org` 在本机不通（连接超时），需要 `GOPROXY=https://goproxy.cn,direct`；缺失的模块正是靠它补齐的。
- dpx 环境把 `GOPATH`/`GOCACHE` 收容到环境目录，`go` 默认写不进去（`Access is denied`）；用 `danger-full-access` 运行，或把 `GOPATH`/`GOCACHE` 指到可写目录。

## 7. 待盘点（增量）

- [x] 上游是否有新的 `### Added` / `### Fixed` 需要挑拣：`git log --oneline HEAD..upstream/main` + 上游 `CHANGELOG.md` 的 Unreleased 段。
  - **本轮盘点结果（无需挑拣）**：本地 `upstream/main` 仍是 `8457fdd`（"update dev docs"），未移动；`git ls-remote https://github.com/Elflare/elbot.git main` 返回同一个 `8457fdd`，与上一轮盘点一致。截至本次盘点，上游没有新的提交可供挑拣，`HEAD..upstream/main` 为空。
  - **注意**：本仓库**没有配置 `upstream` remote**（只有 `origin`）。`refs/remotes/upstream/main` 是之前手工 fetch 留下的 ref；下一次增量盘点需要先 `git fetch https://github.com/Elflare/elbot.git main:refs/remotes/upstream/main`，否则会拿到过期结论。
