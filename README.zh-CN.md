# ElBot

中文 | [English](README.md)

ElBot 是一个使用 Go 编写的轻量级 Agent/Chatbot 框架，目标是在保留可扩展性的同时，尽量降低运行成本、上下文成本和维护复杂度。
支持普通聊天、工具调用、Hook 扩展、长期任务调度、持久化会话与上下文压缩，适合个人助理、平台机器人和可编排自动化助手等场景。

## 特色

### 一、轻量高效

**极致轻量的 Go 实现**：

| 指标           | 数值                            |
| -------------- | ------------------------------- |
| 本地启动耗时   | <10ms（N5105，SATA 固态上测试） |
| 常驻内存       | ~30MB                           |
| 二进制文件大小 | <30MB                           |

> 上表是原版 v0.5.0 的参考数据（N5105 + SATA SSD），用来说明设计目标，不是本仓库的实测承诺：0.6.x 增加了独立健康接口、诊断、素材清单与 `doctor` 等组件，尚未重新做基准测试。生图、工具子进程和并发 turn 的峰值内存更要看实际配置，请以自己机器上的测量为准：
>
> ```bash
> ls -lh elbot                                   # 二进制大小
> /usr/bin/time -v ./elbot config check 2>&1 | grep -E 'Elapsed|Maximum resident'
> docker stats --no-stream elbot                 # 容器常驻内存
> ```

**极致节省 Token 的工具发现**： 研究表明，许多普通用户仍主要将 LLM 类产品用作更高级的搜索引擎、写作助手和倾听对象，频繁工具调用并不是所有对话的常态。
参考：Chatterji et al., _How People Use ChatGPT_, NBER, 2025；Yan et al., _ShareChat: A Dataset of Chatbot Conversations in the Wild_, arXiv:2512.17843, 2025。

ElBot 不会在每轮对话中默认注入所有工具的完整 schema，而是仅暴露 `discover_tool` 和当前可用工具名称。模型需要使用工具时，先按需发现工具详情，再由 Agent 注入对应 schema。极大程度减少无效上下文开销。

**Chat / Work 双模式**：两种模式可独立配置模型，让低成本模型承担闲聊，让强模型专注处理复杂任务。

**常驻记忆与长期记忆分层**： 常驻记忆只保存短小、稳定、真正需要每轮注入的信息，并在内部区分需确认修改的 core 与可整理的 normal；更长、更复杂的记忆由 LLM 按需通过 `long_memory` 查询。长期记忆使用 Markdown 源数据和 SQLite FTS，兼顾透明性和检索效率。

| 模式   | 工具               | 适用场景                                 | 第一次请求 Token 消耗      |
| ------ | ------------------ | ---------------------------------------- | -------------------------- |
| `chat` | 不注入             | 闲聊、陪伴、轻量问答、低成本对话         | <500 （后续缓存命中95%+）  |
| `work` | 启用工具发现与调用 | 搜索、文件、命令、Cron、Skill 等复杂任务 | <1000 （后续缓存命中90%+） |

### 二、强大可扩展

**可扩展的 Hook 系统**： ElBot 内置 Hook Layer，可在 Agent 输入、LLM 请求、LLM 响应、平台发送、平台连接等关键事件点插入扩展逻辑。Hook 可以修改消息、追加输出意图、调用脚本等。Hook 支持用**任意语言**编写插件。

**普通 Cron 与 LLM Cron**： ElBot 内置 Cron Runtime 和 LLM 可编排 Cron 服务。普通 Cron 按计划直接发送固定内容；LLM Cron 用任务描述驱动模型执行，适合需要分析、归纳或使用工具的定时任务。

**ELyph 任务表示法**： ELyph 用于描述 LLM Cron 与原生 Skill。目标是减少自然语言任务描述中的歧义，用更短、更稳定的结构表达输入、输出、步骤、条件和约束。相比随意 Markdown，ELyph 更适合 LLM 之间复用和传递任务，也便于 lint、审计和工具化处理。

**可由 LLM 创建的 EL Skill**： ElBot 内置 `create_el_skill` 元工具，允许 LLM 将可复用经验沉淀为 EL Skill。创建时自动校验 ELyph 语法，可选附带 Go 源码并编译；创建后的纯 ELyph 文本或 Go 源码由统一的 `read_el_skill` / `modify_el_skill` 维护，源码改完后通过 `finalize_el_skill` 统一格式化、编译并返回检查结果。

**兼容外置 AgentSkill**： ElBot 兼容遵从 agentskills.io 风格的外置 AgentSkill。可通过配置将任意 AgentSKill 脚本作为工具使用。

### 三、Elnis 事件感知系统

传统 Agent 通常只会等待用户输入；Cron 只能响应时间。Elnis 让 ElBot 多了一种触发方式：外部事件。

Elnis 是 ElBot 的监听枢纽，Elwisp 是分布在各地的外部监听器，Elvena 是统一的 JSON over HTTP 事件协议。三者协作，让外部世界的任何信号如服务器告警、RSS 更新、Webhook、游戏事件、甚至外部计算机信息都能送入 Elnis，再交由 ElBot 处理并返回。

详细说明见 [Elnis 监听枢纽](docs/elnis.md)。

### 四、灵活部署与完善会话

**多平台与富输出抽象**： ElBot 抽象了平台层与输出层，目前支持 CLI、QQ OneBot、QQ Official 和 Telegram，并预留扩展其他平台的空间。

**CLI 客户端/服务端分离**： 支持任何电脑使用 ElBot 作为客户端连接 ElBot 服务端。 **前端自由定制**，可以随便制作自己喜欢的前端界面。以下截图展示不同的前端形态，除 TUI 外均为概念性 HTML mockup，不代表最终 UI。

<p align="center">
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/frontend_1.png" width="260" />
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/frontend_2.jpg" width="260" />
  <img src="https://raw.githubusercontent.com/Elfreese/elbot-showcase/main/frontend/assets/tui.png" width="260" />
</p>

更多截图见 [elbot-showcase/frontend](https://github.com/Elfreese/elbot-showcase/tree/main/frontend)。

**会话、Fork 与上下文压缩**： 内置持久化 Session 服务，支持会话恢复、归档、置顶、Fork、删除、分页查看和平台隔离。长对话自动触发上下文压缩，保持窗口可控，压缩后可继续正常对话。

### 五、安全可靠

**安全策略与风险确认**： 工具系统内置风险等级、角色权限判断和高风险确认流程。普通用户只能发现和调用低风险工具；超级管理员调用高风险工具时也需逐项确认。

**轻量沙盒隔离**： 后台 Shell 执行受到 AST 级沙盒约束。后台任务拥有独立 sandbox 工作目录，降低误操作影响。

**完善的日志与审计**： 区分运行日志、Elwisp 日志与审计日志，支持结构化字段、日志查询、审计查询和运行期调试。

## 使用方法

常用启动方式：

```bash
elbot              # 自动模式：优先尝试默认远程 CLI client；本地不可达时回退完整前台启动
elbot run          # 完整前台：本地 CLI + 已启用平台 + Cron
elbot cli [-c name]# 远程 CLI 客户端：连接常驻 ElBot 服务端
elbot -c name      # 直接用指定 CLI client profile 连接服务端
elbot service run  # Linux/headless 服务模式：不启动本地 CLI，可启用远程 CLI server、平台和 Cron
```

Shell 补全可通过 `elbot completion <shell>` 生成，支持 `bash`、`zsh`、`fish`、`nushell`、`powershell` 和 `auto`。

最小使用流程：

1. 在 `config/providers.toml` 配置 OpenAI-compatible Provider。
2. 通过系统环境变量或配置目录 `.env` 设置 `api_key_env` 对应的 API Key。
3. 启动后使用命令 `/*models` 查看然后使用 `/*model xx` 选择模型。或手动在 `config/state.toml` 选择默认 `chat` / `work` 模式和模型。
4. 输入 `/*help` 查看命令，或直接开始对话。

详细说明见：

- [快速开始](docs/getting-started.md)
- [配置说明](docs/configuration.md)
- [命令速查](docs/commands.md)
- [核心概念](docs/concepts.md)
- [Elnis 监听枢纽](docs/elnis.md)
- [Elnis 配置与使用](docs/elnis-usage.md)
- [前端 API](docs/frontend-api.md)

开发计划和任务拆分： [devdocs](devdocs/)。

相对原版 v0.5.0 的新增功能与最短使用方法见下一节。

## 本地定制版：相对原版 v0.5.0 的新增功能

本 fork 保留官方 ElBot 的 Agent/Chatbot 核心，并围绕“稳定、可观测、可部署、可扩展”增加了一批新能力：角色素材库、图像生成、系统信息与定时报告、单轮模型/生图/工具声明、命令前缀与配置检查、Docker / 离线部署、独立健康接口、watchdog、备份恢复、升级回滚、验收工具和故障诊断面板。目标很明确：避免“容器显示 healthy，但机器人已经卡死”的情况，并且绝不做“CPU 高就杀进程”的粗暴自愈。

| 新增能力 | 最短使用入口 |
| --- | --- |
| 角色素材库 | `@char:<id>`、`/*chars`、`character_*` 工具 |
| 图像生成 | `image_generate`、`@image:<profile>` |
| 系统信息与定时报告 | `/metrics.resources`、`[maintenance.daily_report]` |
| 单轮模型 / 生图 / 工具声明 | `@model:<profile>`、`@image:<profile>`、`@use:<profile>` |
| 命令前缀与配置检查 | `[commands].prefixes`、`elbot config check` |
| Docker / systemd / 离线部署 | `deploy/`、`deploy/pack/` |
| 独立健康接口与 watchdog | `/live`、`/ready`、`/healthz`、`elbot-watchdog.sh` |
| 限速、熔断、磁盘保护 | `[ops]`、`[storage].disk_*` |
| 备份、恢复、升级、回滚 | `backup.sh`、`restore-verify.sh`、`upgrade.sh`、`rollback.sh` |
| 验收与故障诊断 | `elbot doctor`、`/tasks`、`/metrics`、`/diagnostics` |


### 独立健康接口

ElBot 提供不依赖 Elnis 的独立运维 HTTP 接口：

| 接口 | 含义 |
| --- | --- |
| `/live` | 只表示进程仍在运行；不判断调度心跳、模型或平台。 |
| `/ready` | 进程已初始化、SQLite / 数据目录可写，且调度心跳已开始且未过期；平台/模型故障不会让它失败。 |
| `/healthz` | 汇总状态。平台/模型故障显示为 `degraded`；调度心跳过期时 `/ready` 与 `/healthz` 返回 `not_ready`。不应仅凭它自动重启。 |
| `/tasks` | 当前活跃的 turn / tool / hook / 上下文压缩任务、阶段、开始时间、最近进展和 `queued_by_kind` 排队积压。 |
| `/metrics` | 任务数量、最老任务时长、goroutine / 堆 / RSS / 磁盘、平台/模型/熔断状态、限速阈值与拒绝原因、生图队列状态。 |
| `/diagnostics` | 面向“机器人没回复”的聚合诊断：排队/超时、限速命中、熔断状态和最近一次重启原因。 |

```dotenv
ELBOT_HEALTH_ADDR=0.0.0.0:32171
ELBOT_HEALTH_LIVE_STALE_SECONDS=90
# /tasks 和 /metrics 的访问 token；反代或非回环访问前必须设置。
# ELBOT_OPS_TOKEN=请替换为随机长字符串
```

Compose 只映射到宿主机回环：`127.0.0.1:32171:32171`。标准 Dockerfile 的 `HEALTHCHECK` 已改为 `curl -fsS http://127.0.0.1:32171/live`，不再只看 PID。`32171` 不应加入 Nginx 公网路由。

### 数据卷、首次启动与配置

- Compose 把 `./data` 挂到 `/data`，并设置 `XDG_CONFIG_HOME`、`XDG_DATA_HOME`、`XDG_RUNTIME_DIR`。
- 容器以 UID/GID `10001` 运行，`deploy/data` 必须对该 UID 可写。
- `deploy/init-host.sh` 会创建目录、设置属主，并打印首次启动检查清单。
- 首次启动后检查 `providers.toml`（`api_key_env` 变量名）、`state.toml`（provider/model 是否匹配且真实可用）、`app.toml`（CLI / OneBot / Elnis / 生图 / 安全配置）。
- 改 `.env` 必须执行 `docker compose up -d --force-recreate`；只改 TOML 可以用 `docker compose restart`。

### 容器网络

- QQ OneBot 的 `ws_url` 必须是容器内视角：同一 Compose 网络用服务名；OneBot 在宿主机时用 `host.docker.internal` + `extra_hosts`，或可访问的远程 IP。容器内不要写 `127.0.0.1`。
- 不共享文件系统时保持 `send_file_mode = "base64"`。
- 远程 CLI server 必须在容器内监听 `0.0.0.0:32172`；宿主机只绑定 `127.0.0.1`，通过 HTTPS/WSS 反向代理对外。
- `32170` 是 Elnis 入口，默认关闭；只有启用 Elnis 后该端口的 `/healthz` 才有意义，且不能代表 ElBot / 模型 / OneBot 健康。

### 安全

- Compose `env_file` 会把变量注入 ElBot 进程环境；Shell / Go Skill 子进程会移除名字含 `KEY`、`TOKEN`、`SECRET`、`PASSWORD`、`PRIVATE` 词段的凭据变量，但 Web 搜索、生图、媒体下载等父进程工具仍可读取。不要只依赖“容器不是 root”来保护 API Key。
- 向群聊用户开放工具前，检查 `security.user_max_tool_risk`、`security.superadmins`、Shell 可用范围、外部 Skill 和 Hook 权限。
- 生图建议保持 superadmin-only，或在上游中转站按 Key 设置额度、限速和每日上限。
- CLI / Elnis 继续只绑定宿主机 `127.0.0.1`，对外走 HTTPS/WSS，并使用强随机 token。

### 备份与恢复

`deploy/backup.sh` 不再直接热 tar SQLite：

- 有 `sqlite3`：执行 SQLite `.backup` 一致性备份，不中断服务。
- 没有 `sqlite3` 但有 Docker Compose：短暂停止容器，打包后自动启动。
- 两者都没有：回退热打包并明确警告。
- 备份成功后默认生成 `*.manifest` 文件级 sha256 清单，并调用 `deploy/restore-verify.sh` 在隔离目录验证 SQLite、配置、角色素材、本地媒体和 manifest；`BACKUP_VERIFY=0` 可跳过。
- 单机升级/回滚可用 `deploy/upgrade.sh` / `deploy/rollback.sh`：升级前保存数据快照和上一版镜像，回滚前先校验数据快照。
- `deploy/README.md` 包含恢复演练步骤：解压到临时目录、SQLite 完整性校验、停止服务、替换 `data`、恢复属主，并实际发消息验证。

### 分级 watchdog 与自愈

`deploy/elbot-watchdog.sh`、`elbot-watchdog.service`、`elbot-watchdog.timer` 提供外部 watchdog：

- 只检查 `/live` 判断进程是否还能响应，不会因为 CPU 高、模型 API 暂时失败或平台重连就重启。
- 连续失败达到 `WATCHDOG_FAILURE_THRESHOLD` 后，先收集诊断信息再处置：`/live`、`/ready`、`/healthz`、`/tasks`、`/metrics`、容器 State/Ports/logs/stats、磁盘和 `data` 大小；诊断输出会做凭据脱敏。
- 按 `WATCHDOG_COOLDOWN_SECONDS` 冷却，并在 `WATCHDOG_WINDOW_SECONDS` 内最多重启 `WATCHDOG_MAX_RESTARTS` 次。
- 超限后写入 `watchdog-state/paused`，停止自动重启，等待人工恢复。
- `WATCHDOG_WEBHOOK_URL` 可推送 `restarted`、`paused`、`restart_failed` 事件；若设置了 `ELBOT_OPS_TOKEN`，用 `WATCHDOG_OPS_TOKEN` 填同一个值以访问诊断接口。
- `WATCHDOG_DRY_RUN=1` 时只诊断、不重启。
- watchdog 会把重启 / 暂停原因写入 `data/run/elbot/last_restart_reason`；ElBot 启动后读取该文件，并在 `/healthz`、`/metrics`、`/diagnostics` 中展示最近一次重启原因。
- 不要让宝塔 Docker 管理器和 `elbot-compose.service` 同时管理同一套 Compose。

### P1：限速、有界队列、超时与实时指标

```toml
[ops]
# 按用户 / 群聊限速；0 表示不限制。
user_messages_per_minute = 0
user_burst = 0
group_messages_per_minute = 0
group_burst = 0
rate_limit_idle_ttl_seconds = 600

# 并发满时的有界队列。
queue_max_size = 0
queue_wait_timeout_seconds = 0
queue_wait_kinds = ["tool", "hook", "compress"]

# 运行超时。
tool_timeout_seconds = 600
hook_timeout_seconds = 60
compress_timeout_seconds = 300

# 并发上限。
max_concurrent_turns = 4
max_concurrent_tools = 4
max_concurrent_hooks = 4
```

- 群聊限速会先检查用户级额度，再检查群级总额度；用户级防止单个成员刷屏，群级保护全群资源。超级管理员不受限速影响。
- 限速只是自我保护，不是公平调度：用户级额度按 `平台 + 用户` 统计，同一用户在多个群共享同一份额度；群级拒绝时，本次请求此前消耗的用户额度不会退回。因此“单个成员能否耗尽全群额度”取决于两组阈值怎么配，建议按群规模调整 `[ops]` 的 `rate_limit_*`。
- `turn` 默认不排队；tool / hook / compress 可短暂排队；队列满或超时会明确拒绝。
- `/tasks` 和 `/metrics` 暴露活跃任务与资源状态。

### P1：模型熔断与备用 Provider

```toml
[ops]
circuit_breaker_failure_threshold = 0
circuit_breaker_open_cooldown_seconds = 60
circuit_breaker_half_open_max = 1

[providers.openai]
fallback_provider = "deepseek"
fallback_model = "deepseek-chat"
fallback_mode = "circuit"      # circuit（默认）/ on_error / off
# fallback_timeout_seconds = 0 # 单次 Provider 尝试总超时
```

- 熔断统计连接失败、首包超时、上游 5xx 和 stream error。
- `context.Canceled` 和整轮 response timeout 不计入。
- 默认 `fallback_mode = "circuit"`：熔断打开时，有 fallback 就切备用；没有则返回明确错误，不再无限重试。
- `fallback_mode = "on_error"`（或 `fallback_on_error = true`）时，首个预流式失败请求即可切换；`fallback_timeout_seconds` 可限制单次 Provider 尝试总时长。
- 部署验收可运行 `elbot doctor`：默认检查配置/存储/端口/平台/模型；加 `--e2e` 后通过 CLI 远程协议做真实消息往返，报告区分 `config_ok` 与 `e2e_ok`。
- 外部模型异常只显示为 `degraded`，不会触发自动重启。

### P1：生图并发与降级

```toml
[image_generation]
max_concurrent = 0
queue_size = 0
queue_timeout_seconds = 0
```

- 生图独立于普通聊天限流。
- 队列满或排队超时返回“生图繁忙 / 排队超时”，不拖垮聊天。
- `/metrics` 暴露 `image_limit.active` 和 `image_limit.waiting`。

### P2：磁盘保护与图片 worker 隔离

```toml
[storage]
disk_warn_ratio = 0.85
disk_critical_ratio = 0.95
disk_min_free_bytes = 0
```

- `disk_warn_ratio` / `disk_critical_ratio` 是已用空间比例；`disk_min_free_bytes` 大于 0 时剩余空间过低直接 critical。
- critical 时拒绝非必要媒体写入（图片、语音、视频、普通文件），保留 SQLite 会话和配置写入。
- 图片解码 / 缩放 / JPEG 压缩现在运行在同一二进制的隐藏 worker 子进程中；超时或取消会终止整个 worker 进程树。
- Shell、Hook、AgentSkill、Go Skill 也已经在超时 / 取消时终止各自进程树。

### 角色素材库：角色人设与图片素材

角色库用来保存角色文本设定和角色图片，支持可见性、检索、指令启用和工具调用。

目录结构：

```text
config/characters/
  catgirl/
    character.toml        # 元数据 + 图片索引
    profile.md            # 人设，@char 注入的就是它
    world.md              # 世界观、背景（可选）
    greeting.md           # 开场白（可选）
    examples.md           # few-shot 示例（可选）
    image_prompt.md       # 生图预设（可选）
    notes/*.md            # 补充素材（可选）
    images/avatar.png     # 角色图片原图
```

配置：

```toml
[character_library]
enabled = true
root = "characters"
```

最短使用方式：

```text
@char:catgirl 你好呀
```

- `@char:<id>` / `@c:<id>` 只在本轮生效，不切换 Session、不写入会话元数据，回复结束即失效。
- 角色不存在或无权访问时会提示原因。
- 普通用户默认只能创建、修改和读取自己的私有角色；超级管理员可以管理公共角色。
- 角色素材元数据支持 `version` 和 `source`，更新时版本自动递增；图片索引也带版本和来源。
- 备份时会为角色和媒体文件生成 sha256 manifest，`restore-verify.sh` 恢复时会校验。

常用命令和工具：

- `/*chars`：列出当前可见角色，支持关键词过滤。
- `/*chars reload`：超级管理员重建索引。
- `character_list`：列出可见角色。
- `character_read`：读取 `profile` / `world` / `greeting` / `examples` / `notes/<name>`，可选返回图片。
- `character_search`：按名称、别名、tags 和正文检索。
- `character_manage`：创建 / 修改角色，写入 `docs`，添加或删除角色图片。
- `character_delete`：永久删除角色，高风险确认。

详见 [角色素材库](docs/character-library.md)。

### 图像生成：`image_generate`

`image_generate` 对接 OpenAI 兼容的 `/images/generations`，默认按“全局预设 + 角色图片预设 + 场景描述”组装最终提示词。

基础配置：

```toml
[image_generation]
enabled = true
base_url = "https://your-relay.example.com/v1"  # 自动拼 /images/generations
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

配合角色库使用时：

```text
@char:catgirl 画一张她站在雨里的霓虹街道回眸
```

也可以让模型显式传参：

```json
{"prompt": "在雨里的霓虹街道回眸", "character_id": "catgirl"}
```

`image_generate` 默认 `mode = "auto"`：

- 自动从 prompt 识别角色名/别名，带上角色图片预设和参考图；
- prompt 出现“刚才 / 上一条 / 那张图 / 群里”等指代词时，自动检索当前群聊上下文；
- 生图配置 `auto_character` / `auto_context` / `context_default_limit` 可控制自动编排开关和上下文条数；
- 工具参数 `mode=manual` 可关闭自动编排，只用显式参数。

内置提示词优化：

```toml
[image_generation]
optimize = "rules"              # off / rules
optimize_term_mode = "phrase"   # phrase 或 tag
optimize_max_anchors = 4
optimize_max_negatives = 10
optimize_max_added_runes = 400
```

- `rules` 会从内置 GPT Image Prompts 规则库补用途、比例、画风锚点和负面词。
- `phrase` 追加完整短语，`tag` 追加单词 tag 串。
- 规则库只追加、不重写用户原句。
- 可选 LLM 语义改写：`optimize_rewrite = "off" / "auto" / "always"`，用 `optimize_rewrite_model = "naming"` 指定低成本模型槽位；短或含糊的提示词会自动改写，长提示词保持原样。
- 规则库源文件在 `scripts/data/`，可用 `python scripts/convert_image_prompts.py ...` 重新生成 `internal/imagegen/prompts/library.json`，重新编译后生效。
- 失败和成功都可能记录审计；失败不计入定时报告里的生图成功量。

生图并发与降级：

```toml
[image_generation]
max_concurrent = 0
queue_size = 0
queue_timeout_seconds = 0
```

- 0 表示不限制；队列满或排队超时返回“生图繁忙 / 排队超时”，不会拖垮普通聊天。
- `/metrics.image_limit` 展示当前 `active` 和 `waiting`。

完整配置和提示词优化规则见 [生图服务](docs/image-generation.md)。

### 系统信息与定时报告

新增跨平台系统信息采集和定时报告，方便单机部署观察资源。

系统信息会用于：

- `/metrics.resources`：数据目录大小、文件系统已用/剩余、RSS、Go 堆、goroutine。
- `[maintenance.daily_report]`：定时向超级管理员发送生图量、Token、费用、磁盘、内存报告。
- 自动维护任务：日志清理、会话清理、聊天历史清理、sandbox / 媒体清理按 cron 调度；会话清理也可手动使用 `/clean`，日志和审计分别用 `/log`、`/audit` 查询。

配置：

```toml
[maintenance.daily_report]
enabled = true
schedule = "0 9,21 * * *"    # 每天 09:00 和 21:00
window_hours = 12
provider = "deepseek"        # 留空统计所有 provider
currency = "CNY"
image_price_per_image = 0.05
peak_pricing = true

[maintenance.daily_report.prices."deepseek-v4-pro"]
input_per_million = 9.0
cache_input_per_million = 0.30
output_per_million = 27.0
```

- 报告按 `[security.superadmins]` 发送，发送前请配置对应平台超管 ID。
- Token 和生图量来自审计日志；默认保留 30 天，统计窗口不要超过保留期。
- 计费只是按配置单价做乘法；实际账单以提供商为准。

完整说明见 [定时报告](docs/reports.md)。

### 单轮模型 / 生图 / 工具声明

除 `@char` 外，还可以在群聊消息里声明单轮 profile：

```text
@model:pro 用强模型分析这个问题
@image:fast 给这张图生成一个头像
@use:admin 检查一下服务器
```

也支持中文关键字和全角冒号：

```text
#模型:pro 你好
#生图:fast 画个头像
#工具:admin 看看磁盘
```

配置：

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

规则：

- 只对当前一轮生效，回复结束即失效，不写入 Session。
- 只有超级管理员可以声明。
- profile 别名可在 `aliases` 里配置。
- 未配置或无权声明时会被剥离并提示，不会把声明原样发给模型。

### 命令前缀与配置检查

命令前缀可配置：

```toml
[commands]
prefixes = ["/*"]
```

- 默认支持 `/*help`、`/*model` 等。
- 可追加 `/`、`!` 等前缀；命令执行和补全会按配置识别。
- 修改前缀后，本文档里出现的 `/*` 命令请按你的实际配置替换。

配置检查：

```bash
elbot config check [--config path]
```

它会加载 `app.toml`、`providers.toml`、`state.toml`，打印 Provider、模型、角色库、生图、profile、定时报告摘要和 warning；配置错误时返回非零退出码，适合升级前或 CI 使用。

### 部署产物与离线安装

除普通 Go 二进制外，fork 增加了面向单机的部署方案：

- `deploy/Dockerfile`：运行镜像和 `go-runtime` 镜像。
- `deploy/docker-compose.yml`：单机常驻服务，数据卷、回环端口、日志限制、资源限制。
- `deploy/elbot.service` / `elbot-compose.service`：systemd 单元。
- `deploy/init-host.sh`：宝塔/VPS 初始化目录和权限。
- `deploy/nginx-elbot.conf`：CLI WebSocket / Elnis 反向代理片段。
- `deploy/pack/`：离线安装包，提供 amd64/arm64 静态二进制、预构建 `docker load` tar 和 `SHA256SUMS`。

常用命令：

```bash
cd deploy
cp .env.example .env
vi .env
docker compose build
docker compose up -d
```

离线 / 预编译：

```bash
bash deploy/pack/prepare-offline.sh
# 或使用已下载产物
docker load -i elbot-0.6.1-linux-amd64.tar.gz
```

多架构构建：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -f deploy/Dockerfile --build-arg VERSION=0.6.1 \
  --push -t <registry>/<namespace>/elbot:0.6.1 .
```

构建参数：

```text
GOPROXY       Go 模块代理，国内建议 https://goproxy.cn,direct
APT_MIRROR    apt 镜像主机名，如 mirrors.ustc.edu.cn
GO_BASE_IMAGE / RUNTIME_BASE_IMAGE  覆盖基础镜像或 pin digest
EXTRA_TOOLS   运行镜像额外命令
```

### 部署验收与故障诊断

一条命令做部署验收：

```bash
elbot doctor [--config path] [--json] [--no-model] [--e2e]
```

- 默认检查：配置、存储目录、健康端口、平台状态、真实模型调用。
- `--e2e`：通过 CLI 远程 WebSocket 协议发送一条真实消息并等待回复。
- `config_ok` 与 `e2e_ok` 分开报告；未加 `--e2e` 时 `e2e_ok=false` 并显示 `skipped`。
- `--json` 适合接入 CI 或发布流水线。
- Docker 中执行示例：

```bash
docker compose exec -T elbot elbot doctor
docker compose exec -T elbot elbot doctor --e2e --json
```

故障诊断接口：

```bash
curl -sS http://127.0.0.1:32171/diagnostics
curl -sS -H 'Authorization: Bearer <ELBOT_OPS_TOKEN>' http://127.0.0.1:32171/diagnostics
```

`/diagnostics` 会聚合：

- 活跃任务、排队积压、累计超时；
- 限速命中、用户级/群级拒绝原因；
- Provider 熔断状态；
- 平台和模型状态；
- 最近一次 watchdog 重启原因。

相关 token：

```dotenv
ELBOT_OPS_TOKEN=随机长字符串
WATCHDOG_OPS_TOKEN=同一个随机长字符串
```

### 单机升级与回滚

升级：

```bash
cd deploy
bash upgrade.sh
```

`upgrade.sh` 的顺序：

1. 当前容器内 `elbot config check`；
2. `backup.sh` 生成一致性数据快照；
3. `restore-verify.sh` 校验快照；
4. 保存上一版镜像为 `deploy/rollback/previous-image.tar`；
5. 构建新镜像并用新镜像执行配置兼容性检查；
6. 通过后重建服务。

回滚：

```bash
cd deploy
bash rollback.sh
# 跳过交互确认：
ROLLBACK_CONFIRM=1 bash rollback.sh
```

回滚会：

1. 校验数据快照的 SQLite、配置、角色、媒体和 manifest；
2. 载入上一版镜像；
3. 停止服务并保留当前 `data` 为 `data.before-rollback-*`；
4. 恢复数据快照，使用上一版镜像重建服务。

### 更多细节

- 部署与 watchdog 指南：[`deploy/README.md`](deploy/README.md)
- 配置说明：[`docs/configuration.md`](docs/configuration.md)
- 英文文档：[`README.md`](README.md)

## 开发状态

ElBot 仍在快速开发中，接口、配置和内部实现可能继续调整。当前更适合作为个人 Agent/机器人框架探索使用。
