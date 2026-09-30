# ElBot 云服务器部署指南（阿里云 / 腾讯云轻量 VPS + 宝塔面板）

适用环境：**2 核 4G / 40G 硬盘 / 宝塔 Linux 面板**的阿里云或腾讯云轻量应用服务器。

部署模式：**Docker 单容器常驻 + systemd 开机自启 + 宝塔 Nginx 反向代理 + HTTPS**。

> **不想在服务器上编译 Go？**
> 仓库新增了预编译 / 离线镜像方案，见 [`pack/README-OFFLINE.md`](pack/README-OFFLINE.md)：
> - `deploy/dist/elbot-0.5.0-linux-amd64.tar.gz`：`docker load` 直接可用（scratch 精简版，无 shell）；
> - `deploy/dist/offline-amd64/`：预编译二进制 + Debian 运行时，服务器只需拉约 30MB debian 基础镜像，功能完整；
> - `deploy/pack/prepare-offline.sh`：需要时重新生成以上产物。

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
- **不要**在安全组开放 `32170` / `32172`。这两个端口只监听 `127.0.0.1`，由宝塔 Nginx 反代出去。

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
git clone https://github.com/Elflare/elbot.git .

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
2. 创建 `deploy/data/{config,run,logs}` 并把属主设为容器内的 `10001:10001`；
3. 从 `.env.example` 生成 `.env`，然后提示你填写。

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
```

> 生成随机 token：`openssl rand -hex 32`

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

### 4.2 `state.toml`

默认生成的模型名可能不存在，请改成你 Provider 实际支持的模型（可先用 `/models` 查看）：

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

### 4.4 `app.toml`：启用 Telegram（示例）

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

### 4.5 `elnis.toml`：启用 Elnis 事件入口（可选）

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

### 4.6 应用配置

- 只改了上面这些**配置文件**：`docker compose restart`
- 改了 `.env`（密钥/token）：`docker compose up -d`（`restart` 不会重新注入环境变量！）

```bash
cd /opt/elbot/deploy
docker compose up -d
docker compose logs -f --tail=100
```

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

| 代理目录 | 目标 URL |
| --- | --- |
| `/cli/v1/ws` | `http://127.0.0.1:32172` |
| `/elvena/` | `http://127.0.0.1:32170` |
| `/elbot/healthz` | `http://127.0.0.1:32170/healthz` |

校验并重载：

```bash
nginx -t && nginx -s reload
```

自助验证：

```bash
# 本机直连健康检查
curl -sS http://127.0.0.1:32170/healthz

# 走域名
curl -sS -o /dev/null -w '%{http_code}\n' https://你的域名/elbot/healthz
```

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

> 如果宝塔 Docker 管理器已经托管了 Compose，二选一即可，不要重复管理同一个栈。

---

## 7. 备份

`deploy/backup.sh` 会把整个 `deploy/data` 打包到 `deploy/backups/`，默认保留 14 份。

```bash
chmod +x /opt/elbot/deploy/*.sh
bash /opt/elbot/deploy/backup.sh
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

> 小提示：SQLite 开启 WAL 时直接热备可能不一致。重要数据建议在低峰期备份，或先 `docker compose stop` 再打包，然后 `docker compose up -d`。

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

1. **只暴露 80/443/22/面板端口**，32170/32172 永远只绑定 `127.0.0.1`。
2. CLI 与 Elnis 都有 token 鉴权，但**必须**配合 HTTPS（宝塔 SSL）；不要用明文 ws:// 暴露公网。
3. 宝塔面板改端口、强密码、开启二次验证，并限制来源 IP。
4. Elnis 建议再加一层限制：只允许已知来源 IP，或由内网机器投递。
5. 2C4G 上建议保留 `mem_limit: 1536m`；如果同机还有 MySQL 等，降到 `1024m`。
6. ElBot 以非 root 用户运行，且有安全策略 / 高风险确认。日常使用建议先只给低风险工具权限。
7. 40G 磁盘上不要长期保留多份构建缓存和旧镜像，按第 8 节定期清理。

---

## 12. 常见问题

| 现象 | 排查 |
| --- | --- |
| 容器一直重启 | `docker compose logs --tail=200`；多为配置 TOML 语法错误或 Key 未填 |
| 界面/客户端连不上 | 确认 `platform.cli.server.enabled=true`、`listen="0.0.0.0:32172"`、容器端口映射为 `127.0.0.1:32172`、Nginx 已 reload |
| Nginx 502 | 容器没起、端口没映射、应用监听在 127.0.0.1（容器内需 0.0.0.0） |
| 日志报 permission denied | `chown -R 10001:10001 /opt/elbot/deploy/data`；用宝塔文件管理器编辑后也要重跑 |
| 改了 `.env` 不生效 | 必须 `docker compose up -d`，`restart` 不会重注环境变量 |
| 启动报 `service already appears to be running` | 多为 PID 文件残留：`rm -f /opt/elbot/deploy/data/run/elbot/elbot.pid` 后重启 |
| 大陆机器调用 OpenAI 超时 | 配 Provider `proxy`，或换 DeepSeek/通义/混元等国内兼容接口 |
| Go Skill 编译失败 | `.env` 设 `ELBOT_BUILD_TARGET=go-runtime` 后 `docker compose build && docker compose up -d` |
| 构建镜像很慢/磁盘不够 | 用 `deploy/build-push.sh` 在本地构建推 ACR/TCR，服务器只 `pull` |
| 想多云高可用 | 当前架构是单实例本地 SQLite，不支持多副本；需要共享存储 + 外部编排，属于改造范围 |

---

## 13. 一句话流程

```bash
# 服务器
cd /opt/elbot/deploy
bash init-host.sh          # 第一次生成 .env
vi .env                    # 填 Key / token
bash init-host.sh          # 构建 + 启动
# 启动一次后编辑 data/config/elbot/*.toml
docker compose up -d
# 宝塔添加站点 + SSL + 反代（deploy/nginx-elbot.conf）
systemctl enable --now elbot-compose
bash backup.sh             # 加入宝塔计划任务
```
