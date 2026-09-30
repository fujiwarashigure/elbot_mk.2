# ElBot 云服务器部署指南（阿里云 / 腾讯云轻量 VPS + 宝塔面板）

适用环境：**2 核 4G / 40G 硬盘 / 宝塔 Linux 面板**的阿里云或腾讯云轻量应用服务器。

部署模式：**Docker 单容器常驻 + systemd 开机自启 + 宝塔 Nginx 反向代理 + HTTPS**。

> **不想在服务器上编译 Go？**
> 仓库**不提交**预编译 / 离线产物，`deploy/dist/` 默认不存在；需要时先在本地或 CI 运行
> `deploy/pack/prepare-offline.sh` 生成，再上传服务器。生成方式见 [`pack/README-OFFLINE.md`](pack/README-OFFLINE.md)：
> - `deploy/dist/elbot-0.5.0-linux-amd64.tar.gz`：`docker load` 直接可用（scratch 精简版，无 shell）；
> - `deploy/dist/offline-amd64/`：预编译二进制 + Debian 运行时，服务器只需拉约 30MB debian 基础镜像，功能完整；
> - 如果从仓库里找不到 `deploy/dist/`，属于正常现象，请先自行生成。

ElBot 是单实例、有状态（SQLite + 本地文件）服务，**只允许 1 个副本**，不要多开容器共享同一份 `data/`。

---

## 0. 目录说明

```
deploy/
├── Dockerfile                # ElBot 容器镜像（多阶段构建）
├── Dockerfile.dockerignore   # 构建上下文裁剪
├── docker-compose.yml        # 单机常驻编排
├── .env.example              # 环境变量 / 密钥模板
├── init-host.sh              # 宝塔服务器一键初始化
├── elbot-compose.service     # systemd 服务单元：开机自动 docker compose up
├── elbot.service             # 备选：原生二进制 systemd 单元（不用容器时）
├── nginx-elbot.conf          # 宝塔 Nginx 反向代理片段
├── build-push.sh             # 构建并推送镜像到阿里云 ACR / 腾讯云 TCR
├── backup.sh                 # 数据备份脚本（可挂到宝塔计划任务）
├── elbot-watchdog.sh         # 外部 watchdog：/live 探活 + 冷却限次自愈
├── elbot-watchdog.service    # systemd 一次性执行单元
├── elbot-watchdog.timer      # 每分钟触发 watchdog
├── watchdog.env.example      # watchdog 阈值 / webhook 模板
└── README.md                 # 本文档
```

> 说明：仓库本身没有官方 Dockerfile，本目录是本次新增的部署封装。

---

## 1. 前置条件

### 1.1 云服务器侧

- 系统建议：Ubuntu 22.04 / Debian 12 / AlmaLinux 9。
- 安全组（云控制台）只开放：
  - `22`（SSH，建议改成非默认端口）
  - `宝塔面板端口`（默认 8888，建议改掉并限制来源 IP）
  - `80`、`443`（Nginx + HTTPS）
- **不要**在安全组开放 `32170` / `32171` / `32172`。它们只在宿主机绑定 `127.0.0.1`；其中 `32170`/`32172` 由宝塔 Nginx 反代，`32171` 只给本机 watchdog / 监控使用。

### 1.2 宝塔面板侧

1. 安装宝塔并登录，安装 `Nginx`。
2. 软件商店 → **Docker 管理器** → 安装 Docker。
3. 确认命令可用：

```bash
docker version
docker compose version    # 没有的话安装 docker-compose-plugin，或使用 docker-compose
```

4. 如果服务器拉取 Docker Hub 很慢，在宝塔 Docker 管理器的配置里，或编辑 `/etc/docker/daemon.json` 配置阿里云镜像加速：

```json
{
  "registry-mirrors": ["https://<你的ID>.mirror.aliyuncs.com"]
}
```

然后 `systemctl restart docker`。

### 1.3 磁盘规划（40G 很关键）

| 项目 | 预估 |
| --- | --- |
| 宝塔 + Nginx + 系统 | 3–6G |
| Docker 引擎 + 镜像 | 2–5G |
| 构建缓存（构建后清理） | 1–3G |
| ElBot 常驻数据（SQLite/日志/媒体） | 1–10G，取决于媒体量 |

构建阶段建议放到本地电脑或云厂商容器镜像服务（ACR/TCR）完成，服务器只拉运行镜像，可以省下大量磁盘和流量。40G 服务器上直接 `docker compose build` 也能跑，但务必按第 8 节定期清理。

---

## 2. 上传代码

假设部署到 `/opt/elbot`：

```bash
mkdir -p /opt/elbot
cd /opt/elbot

# 方式一：git clone
git clone https://github.com/fujiwarashigure/elbot_mk.2.git .

# 方式二：宝塔【文件】上传源码压缩包后解压到 /opt/elbot
```

确认目录结构：

```bash
ls /opt/elbot
# cmd  internal  go.mod  deploy  ...
cd /opt/elbot/deploy
```

---

## 3. 一键初始化

```bash
cd /opt/elbot/deploy
bash init-host.sh
```

首次运行会：
1. 检查 Docker / Compose；
2. 创建 `deploy/data/{config,run,logs}`，并把属主设为容器内的 **UID/GID 10001**；
3. 从 `.env.example` 生成 `.env`，然后提示你填写。

> **数据卷权限**：Compose 把 `./data` 挂到容器 `/data`，容器以 `10001:10001` 运行；部署前只要 `deploy/data` 不属于该 UID，SQLite、配置和日志就可能写入失败。请使用 `init-host.sh`，或在启动前手动执行：
> ```bash
> mkdir -p /opt/elbot/deploy/data
> chown -R 10001:10001 /opt/elbot/deploy/data
> chmod 750 /opt/elbot/deploy/data
> ```

编辑 `.env`（**至少填写 LLM API Key 和两个 token**）：

```bash
vi /opt/elbot/deploy/.env
chmod 600 /opt/elbot/deploy/.env
```

关键字段：

```dotenv
TZ=Asia/Shanghai
DEEPSEEK_API_KEY=sk-xxxx
OPENAI_API_KEY=sk-xxxx
# 可选：生图服务（[image_generation]）的 API Key
IMAGE_API_KEY=sk-xxxx
ELBOT_CLI_LOCAL_TOKEN=请换成随机长字符串
ELNIS_HOME_TOKEN=请换成随机长字符串

# 独立健康接口（Compose 默认启用；绑定 0.0.0.0 仅容器内，映射到宿主机 127.0.0.1）
ELBOT_HEALTH_ADDR=0.0.0.0:32171
ELBOT_HEALTH_LIVE_STALE_SECONDS=90
```

> 生成随机 token：`openssl rand -hex 32`
>
> `.env` 通过 Compose `env_file` 注入 ElBot 进程环境，因此 Shell / Hook 子进程也可能继承这些变量。如果准备向群聊用户开放工具，不要只依赖“容器是非 root”来保护 API Key，务必阅读第 11 节。

再次运行初始化：

```bash
bash init-host.sh
```

它会构建镜像并启动容器。查看状态：

```bash
docker compose ps
docker inspect --format '{{.State.Health.Status}}' elbot
docker compose logs -f --tail=200
```

---

## 4. 首次启动：生成并修改配置

容器第一次启动时，ElBot 会把默认配置生成到宿主机挂载目录：

```
deploy/data/config/elbot/
├── app.toml
├── providers.toml
├── state.toml
├── SOUL.md
├── memories.toml
├── elnis.toml
├── tool_tags.toml
├── plugins/
├── skills/
└── long_memory/
```

运行数据（SQLite、日志、sandbox）在：

```
deploy/data/elbot/
├── elbot_sessions.db
├── elbot_chat_history.db
├── logs/
└── sandbox/
```

> **首次启动后先做一次配置核对**：
> 1. 确认 `app.toml`、`providers.toml`、`state.toml` 都已生成在 `deploy/data/config/elbot/`；
> 2. `providers.toml` 里的 `api_key_env` 必须与 `.env` 的变量名逐个对应；
> 3. `state.toml` / `providers.toml` 里的 `provider`、`model` 必须互相匹配，且模型名是 Provider 实际支持的；
> 4. 如启用 CLI / OneBot / Elnis / 生图，继续按下面小节检查对应监听地址、工具权限和额度。
>
> 可以用下面的命令快速查看：
> ```bash
> cd /opt/elbot/deploy
> grep -nE '^\[providers|api_key_env|models|proxy' data/config/elbot/providers.toml
> grep -nE 'default_mode|provider|model' data/config/elbot/state.toml
> grep -nE 'enabled|listen|user_max_tool_risk|superadmins|api_key_env|superadmin_only' data/config/elbot/app.toml
> ```

### 4.1 `providers.toml`

确认 Provider 的 `api_key_env` 和 `.env` 中的变量名一致：

```toml
[providers.deepseek]
base_url = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"

[providers.openai]
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
models = ["gpt-4o-mini"]
```

> 阿里云/腾讯云大陆机器访问 OpenAI 通常需要代理。Provider 支持 `proxy` 字段，填你的代理地址；或者直接使用 DeepSeek / 通义 / 混元等国内 Provider（只要是 OpenAI 兼容接口）。

这里的环境变量名（如 `DEEPSEEK_API_KEY`）必须和 `deploy/.env` 完全一致；改完 `.env` 后必须执行 `docker compose up -d --force-recreate`，`restart` 不会重新注入。

### 4.2 `state.toml`

默认生成的模型名可能不存在，请改成你 Provider 实际支持的模型，并确保 `provider` 是 `providers.toml` 中已存在的 Provider（可先用 `/models` 查看）：

```toml
[session]
default_mode = "work"

[mode_models.work]
provider = "deepseek"
model = "deepseek-chat"

[mode_models.chat]
provider = "deepseek"
model = "deepseek-chat"
```

### 4.3 `app.toml`：启用 CLI 远程服务端（推荐）

这样你可以从任何电脑/手机前端连上来：

```toml
[platform.cli]
enabled = true

[platform.cli.server]
enabled = true
listen = "0.0.0.0:32172"     # 容器内监听 0.0.0.0，宿主机只映射到 127.0.0.1

[platform.cli.server.tokens]
local = ["ELBOT_CLI_LOCAL_TOKEN"]

[security.superadmins]
cli = ["local"]
```

客户端连接地址：

```
wss://你的域名/cli/v1/ws
```

`hello` 消息中的 `client_id = "local"`、`token` 用 `.env` 里的 `ELBOT_CLI_LOCAL_TOKEN`。

> 安全提醒：`client_id=local` 默认是超级管理员。生产环境建议改成别的 client_id，并同步修改 `security.superadmins.cli`；token 要足够长且不可泄露。

### 4.4 `app.toml`：启用 QQ OneBot（可选，容器内网络地址最重要）

```toml
[platform.qqonebot]
enabled = true
ws_url = "ws://onebot:6700/"   # 必须从 ElBot 容器视角写，见下
access_token_env = "QQONEBOT_ACCESS_TOKEN"
trigger_keywords = ["bot"]
send_file_mode = "base64"      # 不共享文件系统时保持 base64
```

`ws_url` 是 **ElBot 容器内**要访问的 OneBot 地址，不能照抄 `ws://127.0.0.1:6700/`——那会指向 ElBot 容器自身：

| OneBot 所在位置 | `ws_url` 写法 |
| --- | --- |
| 与 ElBot 在同一个 Compose 项目（或已加入同一 Docker 网络）的另一个 service | 用 service 名，例如 `ws://onebot:6700/` |
| 在宿主机上 | 用容器可访问的宿主机地址。Linux 可先给 ElBot 加 `extra_hosts`，再写 `ws://host.docker.internal:6700/` |
| 在另一台机器 | 写该机器的 IP / 域名，并确认 ElBot 容器能访问 |

宿主机方案的 Compose 片段：

```yaml
services:
  elbot:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

`send_file_mode` 默认就是 `base64`，适合 ElBot 与 OneBot 不共享文件系统；只有两边能访问同一份本地路径时才考虑 `file_uri`。OneBot Access Token 放 `.env` 的 `QQONEBOT_ACCESS_TOKEN`（不鉴权可省略）。

### 4.5 `app.toml`：启用 Telegram（示例）

```toml
[security.superadmins]
telegram = ["你的Telegram用户ID"]

[platform.telegram]
enabled = true
bot_token_env = "TELEGRAM_BOT_TOKEN"
trigger_keywords = ["bot"]
format = "html"
```

Telegram 使用 long polling，只需要服务器能出网，不需要开放入站端口。

### 4.6 `elnis.toml`：启用 Elnis 事件入口（可选）

`app.toml` 默认已包含 `[config_files] elnis = "elnis.toml"`，只需：

```toml
enabled = true

[http]
addr = "0.0.0.0:32170"       # 容器内监听，宿主机只映射 127.0.0.1

[tokens.home]
token_env = ["ELNIS_HOME_TOKEN"]
```

外部程序投递地址（走宝塔反代后）：

```
https://你的域名/elvena/v3/events
```

使用 `Authorization: Bearer <ELNIS_HOME_TOKEN>`。

> **Elnis 默认关闭**，只有 `elnis.toml` 里 `enabled = true` 时，`32170` 才会提供 `/healthz`。如果不用 Elnis，请删除或注释 Compose 中的 `32170` 端口映射，并同步删除 Nginx 的 `/elbot/healthz`；否则 `/healthz` 失败不能证明 ElBot 服务异常，只会增加运维误判。

### 4.7 应用配置

- 只改了上面这些 **TOML 配置文件**：`docker compose restart`
- 改了 `.env`（密钥/token）：`docker compose up -d --force-recreate`（`restart` 不会重新注入环境变量！）
- 生产建议同时设置 `[ops]` 的工具 / Hook / 上下文压缩超时和并发上限，避免单个任务无限占用资源；生成配置默认已给出保守值。

```bash
cd /opt/elbot/deploy
docker compose up -d --force-recreate  # 改了 .env 用这个
docker compose restart        # 只改 TOML 用这个
docker compose logs -f --tail=100
```

### 4.8 独立健康接口（不依赖 Elnis）

Compose 默认通过环境变量启用独立健康接口，并只映射到宿主机 `127.0.0.1:32171`：

- `GET /live`：关键调度循环最近是否还有心跳。watchdog 只应依据它判断是否需要重启。
- `GET /ready`：SQLite / 数据目录是否可写、组件是否初始化、已配置平台是否已成功连接过。
- `GET /healthz`：汇总状态。外部模型 API 故障显示为 `degraded`，HTTP 仍返回 200，**不应触发本机自动重启**。
- `GET /tasks`：当前活跃的 turn / tool / hook / 上下文压缩任务、阶段、开始时间和最近进展。
- `GET /metrics`：任务数量、最老任务时长、goroutine / 堆 / RSS / 磁盘等资源指标，以及平台和模型健康状态。

```bash
# 在宿主机执行
curl -sS http://127.0.0.1:32171/live
curl -sS http://127.0.0.1:32171/ready
curl -sS http://127.0.0.1:32171/healthz
```

环境变量：

```dotenv
# 容器内监听地址；Compose 映射 127.0.0.1:32171:32171
ELBOT_HEALTH_ADDR=0.0.0.0:32171
# 多久没有调度心跳视为 not_live，默认 90 秒
ELBOT_HEALTH_LIVE_STALE_SECONDS=90
```

> `ELBOT_HEALTH_LIVE_STALE_SECONDS` 应明显大于调度心跳间隔（当前 10 秒），建议保持默认 90。
>
> 32171 **不要**加入宝塔 Nginx 的公网 location；它只供宿主机 watchdog、宝塔监控或本机 curl 使用。标准 Dockerfile 已把 `HEALTHCHECK` 切到 `curl /live`；如果你使用无 curl 的 scratch 离线镜像或自定义镜像，自动处置仍应直接请求宿主机 `127.0.0.1:32171/live`，不要只看 Docker 的 `healthy/unhealthy`。

---

## 5. 宝塔 Nginx 反向代理 + HTTPS

### 5.1 添加站点与证书

1. 宝塔 → 网站 → 添加站点（域名，不创建数据库/PHP）。
2. 站点设置 → SSL → Let's Encrypt，申请并开启强制 HTTPS。

### 5.2 配置反向代理

把 `deploy/nginx-elbot.conf` 里的内容复制到：

```
/www/server/panel/vhost/nginx/你的域名.conf
```

的 `server { ... }` 内。

或者在宝塔「反向代理」中手动添加：

| 代理目录 | 目标 URL | 启用条件 |
| --- | --- | --- |
| `/cli/v1/ws` | `http://127.0.0.1:32172` | `app.toml` 启用 CLI server |
| `/elvena/` | `http://127.0.0.1:32170` | `elnis.toml` 启用 Elnis |
| `/elbot/healthz` | `http://127.0.0.1:32170/healthz` | 仅 Elnis 启用时；不能代表 ElBot / 模型 / OneBot 健康 |

校验并重载：

```bash
nginx -t && nginx -s reload
```

自助验证：

```bash
# 仅启用 Elnis 时才请求；没有启用 Elnis 时该探测无意义
curl -sS http://127.0.0.1:32170/healthz

# 走域名（仅启用 Elnis 时）
curl -sS -o /dev/null -w '%{http_code}\n' https://你的域名/elbot/healthz
```

> `/healthz` 只能说明 Elnis HTTP 入口活着；即使它返回 200，模型 API、QQ OneBot/Telegram、CLI 仍可能不通。完整验收见第 12 节。

---

## 6. 开机自启（容器镜像服务单元）

`docker-compose.yml` 已经设置了 `restart: unless-stopped`，Docker 守护进程随系统启动时容器会自动恢复。为了更可控（明确依赖 docker.service、开机自动 `up -d`），安装仓库提供的 systemd 单元：

```bash
# 1. 确认路径
which docker
# 若不是 /usr/bin/docker，编辑 deploy/elbot-compose.service 里的 ExecStart

# 2. 安装
cp /opt/elbot/deploy/elbot-compose.service /etc/systemd/system/elbot-compose.service
systemctl daemon-reload
systemctl enable --now elbot-compose.service

# 3. 查看
systemctl status elbot-compose.service
```

常用操作：

```bash
systemctl restart elbot-compose     # 重启整个栈
systemctl stop elbot-compose        # 停服（会执行 docker compose down）
systemctl reload elbot-compose      # 重新 up -d
```

> 如果宝塔 Docker 管理器已经托管了 Compose，二选一即可，不要重复管理同一个栈。`restart: unless-stopped` 只负责进程退出后的拉起；应用层卡死由下面的 watchdog 负责。

### 6.1 外部 watchdog：分级自愈而不是遇事就重启

`deploy/elbot-watchdog.sh` 只依据独立 `/live` 判断关键调度循环是否卡死，不会因为 CPU 高、模型 API 暂时失败或队列瞬时堆积就杀进程：

1. `curl /live` 连续失败达到 `WATCHDOG_FAILURE_THRESHOLD` 次才开始处置；
2. 重启前收集 `docker ps/inspect/logs/stats`、`/live`、`/ready`、`/healthz`、磁盘和 data 大小到 `diagnostics/`；
3. 启用 `WATCHDOG_COOLDOWN_SECONDS` 冷却时间；
4. 在 `WATCHDOG_WINDOW_SECONDS` 内最多自动重启 `WATCHDOG_MAX_RESTARTS` 次；
5. 超过上限后写入 `watchdog-state/paused` 并停止自动重启，保留现场，等待人工处理。

安装：

```bash
cd /opt/elbot/deploy
cp watchdog.env.example watchdog.env
vi watchdog.env
cp elbot-watchdog.service elbot-watchdog.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now elbot-watchdog.timer
```

配置 `/ready` 长期失败、模型 API `degraded` 等状态不会触发 watchdog 重启；它们只用于告警和排障。需要临时暂停自动重启时：

```bash
touch /opt/elbot/deploy/watchdog-state/paused
# 处理完后恢复：
rm -f /opt/elbot/deploy/watchdog-state/paused
```

`WATCHDOG_WEBHOOK_URL` 可用于把 `restarted` / `paused` / `restart_failed` 事件推送到现有告警系统。

### 6.2 任务超时、并发上限与实时指标

- `app.toml [ops]` 可为工具、Hook、上下文压缩设置超时，并限制并发 turn / tool / hook 数量；可通过 `queue_wait_kinds` 允许 tool / hook / compress 短暂排队，turn 和队列满时明确拒绝。
- 独立健康接口新增 `/tasks`（活跃任务、阶段、开始/进展时间）和 `/metrics`（任务数、最老任务时长、goroutine / 堆 / RSS / 磁盘、平台连接次数、模型状态、限速命中/拒绝数）。
- Shell、Hook、AgentSkill / Go Skill 在支持平台上会在超时或取消时终止整个子进程树；图片压缩 / 缩放通过同一二进制的隐藏 worker 子进程执行，超时或取消时终止整个 worker 进程树。
- `[storage]` 的 `disk_warn_ratio` / `disk_critical_ratio` / `disk_min_free_bytes` 提供磁盘分级保护；critical 时拒绝非必要媒体写入，保护 SQLite 和配置写入。
- `[ops]` 还可开启 Provider 熔断并配置 `fallback_provider` / `fallback_model`；模型 API 熔断只让 `/healthz` 变 `degraded` 并切备用，不会触发本机重启。
- `[image_generation]` 的 `max_concurrent` / `queue_size` / `queue_timeout_seconds` 用于限制生图并发；`/metrics` 会显示 `image_limit.active` 和 `image_limit.waiting`。
- 告警系统可以轮询 `/metrics` 与 `/healthz`，但自动重启只应由 `elbot-watchdog` 根据 `/live` 触发。

---

## 7. 备份与恢复

`deploy/backup.sh` 默认执行**一致性备份**，备份到 `deploy/backups/`，默认保留 14 份：

- 宿主机有 `sqlite3`：对 SQLite 数据库（`*.db` / `*.sqlite` / `*.sqlite3`）执行 `.backup`，其余文件归档，不中断服务；
- 没有 `sqlite3`：短暂停止 Compose 容器，打包完成后自动 `up -d`；
- 两者都不可用（例如原生部署且未装 `sqlite3`）：回退到热打包并明确警告，不建议生产环境使用。

```bash
chmod +x /opt/elbot/deploy/*.sh
bash /opt/elbot/deploy/backup.sh
# 强制短暂停机备份：
BACKUP_MODE=stop bash /opt/elbot/deploy/backup.sh
# 明确接受热打包风险（不推荐）：
BACKUP_MODE=hot bash /opt/elbot/deploy/backup.sh
```

在宝塔【计划任务】中加一条 Shell 脚本，每天凌晨执行：

```bash
bash /opt/elbot/deploy/backup.sh >/dev/null 2>&1
```

需要备份的核心内容都在 `deploy/data`：

- `data/config/elbot/`：配置、Soul、常驻记忆、Skill、Hook、长期记忆
- `data/config/elbot/characters/`：角色素材库（角色文本、图片；详见 [角色素材库](../docs/character-library.md)）
- 定时报告按 `[security.superadmins]` 发送，记得给对应平台配好超管用户 ID（详见 [定时报告](../docs/reports.md)）
- `data/elbot/*.db`：Session 和聊天历史 SQLite
- `data/elbot/logs/`：运行/审计/Elnis 日志

### 7.1 恢复演练（建议至少做一次）

```bash
# 1. 解压到临时目录（不要直接覆盖生产 data）
mkdir -p /tmp/elbot-restore && tar -xzf /opt/elbot/deploy/backups/elbot-data-*.tar.gz -C /tmp/elbot-restore

# 2. 校验 SQLite 一致性（需要 sqlite3；没有可就先装）
find /tmp/elbot-restore/data -name '*.db' -print0 | xargs -0 -r -n1 sh -c 'sqlite3 "$1" "PRAGMA integrity_check;"' sh

# 3. 短暂停机后替换；保留现网 data 备份
cd /opt/elbot/deploy
docker compose stop
mv data "data.before-restore-$(date +%F-%H%M%S)"
cp -a /tmp/elbot-restore/data ./data
chown -R 10001:10001 data
chmod 750 data
docker compose up -d

# 4. 实际发一条消息，并重启容器后再次确认会话仍在
```

---

## 8. 升级、日志与 40G 磁盘维护

### 8.1 升级

```bash
cd /opt/elbot
git pull
cd deploy
docker compose build --no-cache
docker compose up -d
docker image prune -f
```

如果使用 ACR/TCR 镜像，服务器上只需：

```bash
cd /opt/elbot/deploy
docker compose pull
docker compose up -d
```

### 8.2 查看日志

```bash
cd /opt/elbot/deploy
docker compose logs -f --tail=200          # 容器 stdout
tail -f data/elbot/logs/elbot-$(date +%F).log   # 应用结构化日志
```

### 8.3 40G 磁盘维护（建议每周）

```bash
# 清理未使用镜像、构建缓存、停止容器
docker system df
docker system prune -af
docker builder prune -f

# 查看磁盘占用
du -sh /opt/elbot/deploy/data
du -sh /var/lib/docker
df -h
```

应用侧限制已在配置中：

- `app.toml [runtime] log_retention_days = 30`
- 维护任务会自动清理日志、sandbox、聊天历史
- `docker-compose.yml` 已限制容器日志 `20m × 5`

如果媒体文件很多，建议启用 S3/R2：

```toml
[file_delivery]
backend = "hybrid"
max_direct_base64_bytes = 8388608
s3_endpoint = "https://<accountid>.r2.cloudflarestorage.com"
s3_region = "auto"
s3_bucket = "elbot-files"
s3_access_key_env = "ELBOT_S3_ACCESS_KEY_ID"
s3_secret_key_env = "ELBOT_S3_SECRET_ACCESS_KEY"
```

---

## 9. 构建并推送到阿里云 ACR / 腾讯云 TCR（容器镜像服务）

镜像不是必须在服务器上构建。推荐本地/CI 构建后推送到云厂商的**容器镜像服务**，服务器只拉取，省磁盘省流量。

### 9.1 阿里云 ACR

```bash
docker login registry.cn-hangzhou.aliyuncs.com
bash deploy/build-push.sh registry.cn-hangzhou.aliyuncs.com/<命名空间>/elbot:0.5.0
```

### 9.2 腾讯云 TCR

```bash
docker login ccr.ccs.tencentyun.com
bash deploy/build-push.sh ccr.ccs.tencentyun.com/<命名空间>/elbot:0.5.0
```

### 9.3 服务器使用远端镜像

编辑 `deploy/.env`：

```dotenv
ELBOT_IMAGE=registry.cn-hangzhou.aliyuncs.com/<命名空间>/elbot:0.5.0
```

服务器登录私有仓库后：

```bash
cd /opt/elbot/deploy
docker compose pull
docker compose up -d
docker compose up -d --build   # 如需覆盖为本地构建
```

> ARM 实例（部分腾讯云/阿里云规格）用：`PLATFORM=linux/arm64 bash deploy/build-push.sh <image>`。

---

## 10. 备选：原生 systemd 部署（不用 Docker）

如果你不想用容器，也可以直接跑官方静态二进制：

```bash
# 在本地或服务器交叉编译
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o elbot ./cmd/elbot

# 服务器上
useradd -r -s /usr/sbin/nologin elbot
mkdir -p /opt/elbot/data/config /opt/elbot/data/run
cp elbot /opt/elbot/elbot
chown -R elbot:elbot /opt/elbot

# 安装服务单元
cp deploy/elbot.service /etc/systemd/system/elbot.service
systemctl daemon-reload
systemctl enable --now elbot
```

配置路径为 `/opt/elbot/data/config/elbot/`，`.env` 放 `/opt/elbot/data/config/.env`。

> `deploy/elbot.service` 默认只允许写入 `/opt/elbot`。ElBot 的 `shell` / `workspace` 工具如需操作其他目录，请在单元里追加 `ReadWritePaths=` 或放宽 `ProtectSystem`。

---

## 11. 安全与资源建议

1. **只暴露 80/443/22/面板端口**；`32170` / `32171` / `32172` 只在宿主机绑定 `127.0.0.1`，公网统一走 HTTPS/WSS 反代。
2. CLI 与 Elnis 都有 token 鉴权，但**必须**配合 HTTPS（宝塔 SSL）；不要用明文 `ws://` 暴露公网。token 用 `openssl rand -hex 32` 生成，定期轮换。
3. 宝塔面板改端口、强密码、开启二次验证，并限制来源 IP。
4. Elnis 建议再加一层限制：只允许已知来源 IP，或由内网机器投递。
5. 2C4G 上建议保留 `mem_limit: 1536m`；如果同机还有 MySQL 等，降到 `1024m`。
6. **不要只依赖“容器是非 root”来保护 Key**：Compose 的 `env_file` 会把 `.env` 注入 ElBot 进程环境，Shell / Hook 子进程同样可能继承这些变量；systemd `EnvironmentFile` 也有同样效果。详见 [环境变量与进程环境继承](../docs/configuration.md#环境变量与进程环境继承)。

### 11.1 向群聊用户开放工具时的检查项

如果普通群成员可以使用工具，建议逐项确认：

- `app.toml`：
  ```toml
  [security]
  user_max_tool_risk = "low"       # 普通用户不要开放 high / critical
  superadmin_confirm_risk = "high"

  [security.superadmins]
  cli = ["local"]
  qqonebot = ["你的QQ号"]          # 只列真正信任的 ID
  ```
- `tool_tags.toml`：不要给普通用户开放包含 `shell`、文件写入、外部请求等高风险工具的 tag。
- `skills/` 与 `plugins/`：外部 Skill / Hook 不经过 ElBot 的工具风险确认；只安装可信来源，并检查 `ELBOT_SKILL.toml` 中的 `command` / `risk`。
- `data/config/elbot/.env` 与 `deploy/.env` 使用独立、最小权限的 API Key；不要把能访问其他系统的长期密钥放进同一进程环境。

### 11.2 生图接口额度

- 默认保持 `[image_generation] superadmin_only = true`；如果改为 `false`，普通用户还要 `security.user_max_tool_risk >= "medium"` 才能调用。
- 建议在中转站 / 上游按 Key 设置 **额度、限速和每日上限**，并使用和 LLM 分开的 `IMAGE_API_KEY`；不要把无限额度的主 Key 注入面向群聊的进程。
- 详细说明见 [生图服务](../docs/image-generation.md#权限与费用)。

---

## 12. 部署验收

> 标准 Dockerfile 的 `HEALTHCHECK` 已请求独立 `/live`，比 PID 更接近真实存活性，但 **仍不代表模型 API、QQ OneBot、Telegram、CLI 或数据库可写已经全部就绪**。上线前至少完成以下验收：

### 12.1 数据卷权限

```bash
cd /opt/elbot/deploy
stat -c '%u:%g %a %n' data
# 期望：10001:10001；至少确认容器进程可写 test -w
docker compose exec -T elbot sh -c 'test -w /data && test -w /data/config && test -w /data/elbot && echo data-writable'
```

> 如果使用 scratch 离线镜像（无 shell），用 `docker inspect` / 备份写入测试代替 `exec`。

### 12.2 首次配置检查

```bash
ls -l data/config/elbot/{app,providers,state}.toml
grep -nE '^\[providers|api_key_env|models' data/config/elbot/providers.toml
grep -nE 'default_mode|provider|model' data/config/elbot/state.toml
grep -nE 'enabled|listen|user_max_tool_risk|superadmins|superadmin_only' data/config/elbot/app.toml
```

确认 Key 变量名、Provider 和模型互相对应。

### 12.3 独立健康接口

```bash
curl -sS http://127.0.0.1:32171/live
curl -sS http://127.0.0.1:32171/ready
curl -sS http://127.0.0.1:32171/healthz
```

- `/live` 失败才说明关键调度循环可能卡死；
- `/ready` 失败说明数据目录、SQLite 目录或平台连接尚未就绪；
- `/healthz` 为 `degraded` 时说明模型 API 等外部依赖异常，但不应自动重启本地服务。

### 12.4 实际连通与会话持久化

1. 在目标平台（CLI / QQ OneBot / Telegram）实际发一条消息，确认能收到回复；
2. 检查 `docker compose logs --tail=200`，确认没有模型鉴权 / OneBot 连接 / CLI listen 错误；
3. `docker compose restart` 后再次发消息，确认原会话和聊天历史仍在；
4. 如果是 QQ OneBot，重点确认 `ws_url` 用的是容器视角地址，且 `send_file_mode = "base64"`（不共享文件系统时）；
5. 如果启用 Elnis，单独执行 `curl -sS http://127.0.0.1:32170/healthz`；未启用 Elnis 时不要把该探测结果计入验收。

### 12.5 备份恢复演练

至少执行一次第 7.1 节的解压、SQLite 完整性校验和临时恢复验证；不要等到生产故障时才第一次验证备份包。

---

## 13. 常见问题

| 现象 | 排查 |
| --- | --- |
| 容器一直重启 | `docker compose logs --tail=200`；多为配置 TOML 语法错误或 Key 未填 |
| 界面/客户端连不上 | 确认 `platform.cli.server.enabled=true`、`listen="0.0.0.0:32172"`、容器端口映射为 `127.0.0.1:32172`、Nginx 已 reload |
| Nginx 502 | 容器没起、端口没映射、应用监听在 127.0.0.1（容器内需 0.0.0.0） |
| 日志报 permission denied | `chown -R 10001:10001 /opt/elbot/deploy/data`；用宝塔文件管理器编辑后也要重跑 |
| 改了 `.env` 不生效 | 必须 `docker compose up -d --force-recreate`，`restart` 不会重注环境变量 |
| `/ready` 长期 `not_ready` | 检查数据目录/SQLite 目录是否可写、日志中的平台连接错误、`ELBOT_HEALTH_ADDR` 是否配置 |
| `/healthz` 为 `degraded` | 通常是模型 API 或中转站异常；先查上游，不要直接重启容器 |
| 启动报 `service already appears to be running` | 多为 PID 文件残留：`rm -f /opt/elbot/deploy/data/run/elbot/elbot.pid` 后重启 |
| 大陆机器调用 OpenAI 超时 | 配 Provider `proxy`，或换 DeepSeek/通义/混元等国内兼容接口 |
| Go Skill 编译失败 | `.env` 设 `ELBOT_BUILD_TARGET=go-runtime` 后 `docker compose build && docker compose up -d` |
| 构建镜像很慢/磁盘不够 | 用 `deploy/build-push.sh` 在本地构建推 ACR/TCR，服务器只 `pull` |
| 想多云高可用 | 当前架构是单实例本地 SQLite，不支持多副本；需要共享存储 + 外部编排，属于改造范围 |

---

## 14. 一句话流程

```bash
# 服务器
cd /opt/elbot/deploy
bash init-host.sh          # 第一次生成 .env
vi .env                    # 填 Key / token
bash init-host.sh          # 构建 + 启动
# 启动一次后编辑 data/config/elbot/*.toml
docker compose restart          # 只改 TOML
# 如果改了 .env，则必须用：
docker compose up -d --force-recreate
# 宝塔添加站点 + SSL + 反代（deploy/nginx-elbot.conf）
systemctl enable --now elbot-compose
bash backup.sh             # 加入宝塔计划任务
```
