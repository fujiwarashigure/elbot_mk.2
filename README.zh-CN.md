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

## 本地定制版：部署与运维增强

本 fork 保留官方 ElBot 的 Agent/Chatbot 核心，同时增加了面向宝塔 / VPS 的 Docker 部署与运维能力。目标很明确：避免“容器显示 healthy，但机器人已经卡死”的情况，并且绝不做“CPU 高就杀进程”的粗暴自愈。

### 独立健康接口

ElBot 提供不依赖 Elnis 的独立运维 HTTP 接口：

| 接口 | 含义 |
| --- | --- |
| `/live` | 关键调度循环是否仍在推进。 |
| `/ready` | SQLite / 数据目录是否可写、必要组件是否初始化、已配置平台是否至少成功连接过一次。 |
| `/healthz` | 汇总状态。外部模型故障显示为 `degraded`，不应触发本机自动重启。 |
| `/tasks` | 当前活跃的 turn / tool / hook / 上下文压缩任务、阶段、开始时间和最近进展。 |
| `/metrics` | 任务数量、最老任务时长、goroutine / 堆 / RSS / 磁盘、平台连接次数、模型健康状态、限速计数和生图队列状态。 |

```dotenv
ELBOT_HEALTH_ADDR=0.0.0.0:32171
ELBOT_HEALTH_LIVE_STALE_SECONDS=90
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

- Compose `env_file` 会把变量注入 ElBot 进程环境，Shell / Hook 子进程可能继承。不要只依赖“容器不是 root”来保护 API Key。
- 向群聊用户开放工具前，检查 `security.user_max_tool_risk`、`security.superadmins`、Shell 可用范围、外部 Skill 和 Hook 权限。
- 生图建议保持 superadmin-only，或在上游中转站按 Key 设置额度、限速和每日上限。
- CLI / Elnis 继续只绑定宿主机 `127.0.0.1`，对外走 HTTPS/WSS，并使用强随机 token。

### 备份与恢复

`deploy/backup.sh` 不再直接热 tar SQLite：

- 有 `sqlite3`：执行 SQLite `.backup` 一致性备份，不中断服务。
- 没有 `sqlite3` 但有 Docker Compose：短暂停止容器，打包后自动启动。
- 两者都没有：回退热打包并明确警告。
- `deploy/README.md` 包含恢复演练步骤：解压到临时目录、SQLite 完整性校验、停止服务、替换 `data`、恢复属主，并实际发消息验证。

### 分级 watchdog 与自愈

`deploy/elbot-watchdog.sh`、`elbot-watchdog.service`、`elbot-watchdog.timer` 提供外部 watchdog：

- 只检查 `/live`，不会因为 CPU 高或模型 API 暂时失败就重启。
- 连续失败达到 `WATCHDOG_FAILURE_THRESHOLD` 后，先收集诊断信息再处置：`/live`、`/ready`、`/healthz`、`/tasks`、`/metrics`、`docker ps/inspect/logs/stats`、磁盘和 `data` 大小。
- 按 `WATCHDOG_COOLDOWN_SECONDS` 冷却，并在 `WATCHDOG_WINDOW_SECONDS` 内最多重启 `WATCHDOG_MAX_RESTARTS` 次。
- 超限后写入 `watchdog-state/paused`，停止自动重启，等待人工恢复。
- `WATCHDOG_WEBHOOK_URL` 可推送 `restarted`、`paused`、`restart_failed` 事件。
- `WATCHDOG_DRY_RUN=1` 时只诊断、不重启。
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

- 限速按 `平台 + scope` 使用令牌桶；超级管理员不受限速影响。
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
```

- 熔断统计连接失败、首包超时、上游 5xx 和 stream error。
- `context.Canceled` 和整轮 response timeout 不计入。
- 熔断打开时，有 fallback 就切备用；没有则返回明确错误，不再无限重试。
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

### 更多细节

- 部署与 watchdog 指南：[`deploy/README.md`](deploy/README.md)
- 配置说明：[`docs/configuration.md`](docs/configuration.md)
- 英文文档：[`README.md`](README.md)

## 开发状态

ElBot 仍在快速开发中，接口、配置和内部实现可能继续调整。当前更适合作为个人 Agent/机器人框架探索使用。
