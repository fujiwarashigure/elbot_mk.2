# ElBot 离线 / 预编译部署包

> 本地 Windows 10/11 + Docker Desktop 部署见 [`../windows/README.md`](../windows/README.md)。


本文档说明如何用 `prepare-offline.sh` 生成预编译 / 离线产物，并在**不安装 Go**的云服务器
（阿里云 / 腾讯云轻量 VPS + 宝塔）上直接部署。

> 仓库的 `deploy/pack/` 目录只包含脚本和文档，**不包含现成产物**；`deploy/dist/` 默认不存在，
> 需要先在本地或 CI 运行 `prepare-offline.sh` 生成。生成后的 `offline-<arch>/README.md`
> 就是本文件。

## 产物一览

| 文件 | 说明 |
| --- | --- |
| `elbot-0.6.8-linux-amd64.tar.gz` | 手工构造的 `docker save` 格式镜像，`docker load` 后直接可跑（scratch 精简版，**无 /bin/sh**） |
| `elbot-0.6.8-linux-arm64.tar.gz` | 同上，ARM64 |
| `elbot-linux-amd64` / `elbot-linux-arm64` | 静态 Linux 二进制（CGO_ENABLED=0） |
| `offline-amd64/` / `offline-arm64/` | **完整功能版**部署包（预编译二进制 + Debian 运行时 Dockerfile + compose + 反代/服务单元/备份脚本） |
| `elbot-0.6.8-offline-amd64.tar.gz` / `-arm64` | 上面两个目录的单文件打包，便于上传 |
| `prepare-offline.sh` | 重新生成所有产物的脚本（需要 Go 1.26） |
| `build-image-tar.py` / `verify-image-tar.py` | 构造 / 离线校验镜像 tar 的脚本 |
| `README-BAOTA-CONFIG.md` | 宝塔面板改配置速查 + 可直接粘贴的配置片段 |

> 上表中的 `elbot-*` / `offline-*` 都是 `prepare-offline.sh` 的**输出**，不是仓库里的现成文件。

> 默认按 **amd64**（绝大多数云服务器）操作；ARM 实例请把 `amd64` 换成 `arm64`。

---

## 通用准备（A / B 方案都要做）

```bash
# 上传 elbot-0.6.8-offline-amd64.tar.gz 到服务器后解压
mkdir -p /opt/elbot && cd /opt/elbot
tar -xzf /path/to/elbot-0.6.8-offline-amd64.tar.gz   # 得到 offline-amd64/
cd /opt/elbot/offline-amd64

# 关键：bind mount 会用宿主机目录的属主，容器内进程是 uid 10001，
# 所以必须把 data 目录改属主，否则容器会因为写不了 SQLite/日志而反复重启。
mkdir -p data
chown -R 10001:10001 data
chmod 750 data

# 准备环境变量
cp .env.example .env
vi .env
chmod 600 .env
```

`.env` 至少填写：

```dotenv
DEEPSEEK_API_KEY=sk-xxxx
ELBOT_CLI_LOCAL_TOKEN=请换成随机长字符串
ELNIS_HOME_TOKEN=请换成随机长字符串
```

生成随机 token：`openssl rand -hex 32`

> 如果之后用宝塔文件管理器编辑了 `data/` 里的文件，可能产生 root 属主的新文件，
> 再次执行 `chown -R 10001:10001 data` 即可。

---

## 方案 A：直接 `docker load`（完全离线、零依赖）

适合：只需要对话、Web 工具、文件工具、记忆、Cron、平台机器人；
**不使用 `shell` 工具**。

该镜像是 `scratch` 基础，内部只有：elbot 静态二进制、CA 证书、`/etc/passwd`、时区数据。
**没有 `/bin/sh`、`ls`、`curl` 等命令**，所以 ElBot 的 `shell` 工具会失败。
> 该镜像的 Docker `HEALTHCHECK` 仍使用 `--version`；运行状态请从宿主机请求
> `http://127.0.0.1:32171/live`、`/ready`、`/healthz`。
需要 shell 功能请用方案 B。

```bash
cd /opt/elbot/offline-amd64

# 1. 载入镜像（约 9MB）
docker load -i ../elbot-0.6.8-linux-amd64.tar.gz
docker images | grep elbot

# 2. 直接启动（镜像已存在，compose 有 build 段也不会重新构建）
docker compose up -d
docker compose ps
docker inspect --format '{{.State.Health.Status}}' elbot
docker compose logs -f --tail=100
```

首次启动会在 `./data/config/elbot/` 生成默认配置，按 `deploy/README.md` 第 4 节修改：
改 TOML 用 `docker compose restart`，改 `.env` 用 `docker compose up -d --force-recreate`。

---

## 方案 B：预编译二进制 + Debian 运行时（推荐，功能完整）

适合：需要 shell、文件、git、curl 等完整工具链。

服务器只需要能拉 `debian:bookworm-slim`（约 30MB）并 apt 安装运行时依赖，
**不需要 Go，也不需要拉 `golang:1.26`（约 1GB）**。

```bash
cd /opt/elbot/offline-amd64

docker compose build
docker compose up -d
docker compose ps
docker inspect --format '{{.State.Health.Status}}' elbot
```

国内拉 debian 慢时：

```bash
docker compose build \
  --build-arg BASE_IMAGE=registry.cn-hangzhou.aliyuncs.com/library/debian:bookworm-slim
```

---

## 宝塔 Nginx 反代 / 开机自启 / 备份

- 反代：把 `nginx-elbot.conf` 中的 CLI / Elnis 部分加入域名站点配置；`/elbot/healthz` **仅在 Elnis 启用时有意义**，不启用 Elnis 时应删除该 location。
- 独立健康接口：Compose 默认映射 `127.0.0.1:32171`，用 `curl http://127.0.0.1:32171/live`、`/ready`、`/healthz`、`/tasks`、`/metrics` 分别验证；**不要**加入 Nginx 公网 location。
- 开机自启：`cp elbot-compose.service /etc/systemd/system/`，改好里面的 `WorkingDirectory`
  为 `/opt/elbot/offline-amd64`，然后 `systemctl daemon-reload && systemctl enable --now elbot-compose`。
- 备份：`bash backup.sh`，默认使用 SQLite `.backup` 或短暂停机打包；可加入宝塔【计划任务】每天执行。
- 自愈：`bash elbot-watchdog.sh` 只检查 `/live`，按冷却和次数上限受控重启；可安装 `elbot-watchdog.service` + `.timer`，配置见 `watchdog.env.example`。
- 部署验收：实际发一条消息、重启后确认会话仍在、检查 `data` 属主为 10001、检查 `32171` 健康接口、做一次备份恢复演练。
- 完整配置项、端口、防火墙、40G 磁盘维护、常见问题：见仓库 `deploy/README.md`。

---

## 方案 C：从源码构建（与原仓库一致）

需要 Go 1.26：

```bash
cd /opt/elbot
docker compose -f deploy/docker-compose.yml build
docker compose -f deploy/docker-compose.yml up -d
```

---

## 校验与重新生成

```bash
# 不需要 Docker：校验镜像 tar 的 manifest / config / layer 摘要
# 建议带 --tag 校验版本号；不带则只做结构校验（不会因为版本号不同而误报）
python3 verify-image-tar.py --tag elbot:0.6.8 elbot-0.6.8-linux-amd64.tar.gz

# 校验传输后的文件完整性
sha256sum -c SHA256SUMS

# 重新生成全部产物（需要 Go 1.26 和 Python 3）
# CA 证书优先取系统 bundle（/etc/ssl/certs/ca-certificates.crt），
# 不需要 pip install certifi；必要时可用 --ca-bundle 指定
GOROOT=/usr/local/go PATH=/usr/local/go/bin:$PATH bash prepare-offline.sh
```

## 已知限制

- 单实例有状态服务（SQLite + 本地文件），**只能 1 个副本**，不要多机共享同一个 `data/`。
- 方案 A 的 scratch 镜像没有 shell；方案 B/C 才有完整 shell 工具能力。
- `docker load`/`docker build` 后如果容器反复重启，优先看 `docker compose logs`；
  最常见原因是 `data/` 属主不是 10001，或 `.env` 里的 Key/token 没填。
