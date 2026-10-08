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
- 已落地：命名对齐（`api` → `api_mode`）、P0 全部 8 项、P1#3 工具发现/预载事务化、P1#5 会话整行快照 → 事务内字段更新、P1#4 后台接管（fork 原生最小版，`f9882b5`）。
- 剩余：P1#1 日志与信号整改、P1#2 模型/会话服务化、P1#6 通知规则集中化；P2 换基路线 B（原生 Responses，待决策）。

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

## 2. [ ] P1#1 日志与信号整改（5–8 人日）

- **上游来源**：`internal/signal/*`、`internal/logging/{record,signals}.go`、`internal/events/*` 以及各产生来源的迁移；上游自己的目标契约见其 `devdocs/signals-and-logging.md`。
- **fork 落点**：fork 没有 `internal/events`，`internal/logging` 也没有 `record.go` / `signals.go`；需要先定"来源标识 + 事件类型"的 fork 版最小契约，再逐包把日志来源补齐（跨 30+ 包，是这项的主要成本）。
- **前置**：无。**被依赖**：P1#6 通知规则集中化（任务 4）以本项为前置。
- **建议拆法**：① 契约与命名定稿（含 `--log`/`/log` 过滤参数兼容）② logging 包内 record/signals 骨架 ③ 按包分批迁移来源（每批可独立提交）④ 文档。
- **验收**：`/log`、`/audit` 的筛选参数在新来源下仍可用；每批迁移后跑受影响包测试 + `internal/app`。

## 3. [ ] P1#2 模型/会话服务化（5–8 人日）

- **上游来源**：`internal/modelmgr/*`、`internal/session/*`、`internal/app/{signals,lifecycle,services,agent_status}.go`、`internal/turn/execution.go`。
- **要拿到的能力**：`state.toml` 原子保存、命名模型快照、会话绑定统一失效、运行中会话拒绝切离。
- **前置**：无（但会与 P1#1 抢同一批 app 层文件，建议两项顺序做，不要并行改 `internal/app`）。
- **注意**：fork 没有上游的 `session.Binding`；本项是"忠实移植上游后台接管"（P1#4 的完整版）的前置，若将来要把接管语义对齐上游，先做本项。
- **验收**：并发切换模型/新建会话不再出现半写 `state.toml`；运行中会话切模型/切模式给出明确拒绝而非静默生效；相关包测试 + `internal/app` 全绿。

## 4. [ ] P1#6 通知规则集中化（2–3 人日）

- **上游来源**：`internal/notification/*`。
- **前置**：P1#1 日志与信号整改（通知规则建立在统一来源/事件契约之上）。
- **验收**：通知目标的裁决逻辑只有一处入口；cron / Elnis / Hook 三条投递路径都走它；补"通知被禁用时不投递"的负向测试。

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
- `internal/tool/builtin`：4 个 `TestAnalyzeBashShellAdvice*`。
- `internal/tool/skill`：`TestFinalizeElSkillReturnsBuildFailure`（测试内部跑 `go build`）。
- `internal/hook/rules`：`TestExecCancellationKillsDescendantProcesses`（依赖 TCP 回连，本机 accept 超时）。

**本机 Go 工具链事实（本次验证环境，供后续复核）**：

- Go 不在 `PATH` 上，工具链在 `C:\Users\shigure\go-sdk\go\bin`（`go1.26.0 windows/amd64`）；直接用绝对路径或把它加进 `PATH`。
- `proxy.golang.org` 在本机不通（连接超时），需要 `GOPROXY=https://goproxy.cn,direct`；缺失的模块正是靠它补齐的。
- dpx 环境把 `GOPATH`/`GOCACHE` 收容到环境目录，`go` 默认写不进去（`Access is denied`）；用 `danger-full-access` 运行，或把 `GOPATH`/`GOCACHE` 指到可写目录。

## 7. 待盘点（增量）

- [ ] 上游是否有新的 `### Added` / `### Fixed` 需要挑拣：`git log --oneline HEAD..upstream/main` + 上游 `CHANGELOG.md` 的 Unreleased 段。
