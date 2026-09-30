# 阿里云轻量应用服务器部署 ElBot

> 先澄清：本项目提供的是 **Docker 镜像归档**（`docker save` 格式），**不是 Nginx 镜像**，
> 也**不能导入到阿里云轻量控制台的“系统镜像 / 应用镜像”**里。正确流程是：
> 进入服务器操作系统 → 安装 Docker → `docker load` 或 `docker build` → `docker compose up`。
> Nginx 由宝塔装在宿主机上，只做反向代理。

```
公网用户 / 外部 Elwisp
        │  https / wss
        ▼
宝塔 Nginx（宿主机，443 + TLS）
        │  proxy_pass 127.0.0.1
        ▼
Docker 容器 elbot（只映射 127.0.0.1:32172 / 32170）
        │
        └── 数据卷：宿主 ./data  ──►  /data（配置 / SQLite / 日志 / sandbox）
```

---

## 0. 服务器规格与系统

- 规格：2 核 4G / 40G 即可（ElBot 常驻约 30MB，主要是 LLM 调用和媒体占磁盘）。
- 系统：Ubuntu 22.04 / Debian 12 均可；如果你要用宝塔面板，直接选轻量应用服务器里的
  **“宝塔 Linux 面板”应用镜像**最省事（安装好后面板地址、账号密码在控制台可见）。
- 架构：轻量服务器绝大多数是 x86_64（用 `amd64` 包）；少数 ARM 实例用 `arm64`。
  登录后 `uname -m` 确认：`x86_64` → amd64，`aarch64` → arm64。

## 1. 阿里云控制台：防火墙 / 安全组

阿里云轻量在控制台有独立的**防火墙**（不是 ECS 安全组）：

- 放行：`22`（SSH，建议改端口）、`80`、`443`、`宝塔面板端口`（默认 `8888`，建议改并限制来源 IP）。
- **不要放行** `32170`、`32172`。这两个端口只映射到宿主机 `127.0.0.1`，由 Nginx 反代出去。
- 如果系统内还开了 `ufw` / `firewalld`，也要同步放行。

> 大陆服务器绑域名走 80/443 需要 **ICP 备案**；没有备案可以先用「IP + 非 80/443 端口」自己访问面板。

## 2. 登录服务器并安装 Docker

```bash
# 控制台“远程连接”或本地 SSH
ssh root@<轻量服务器公网IP>

# 宝塔面板用户：软件商店 -> Docker管理器 -> 安装（会自动装 docker + compose）
# 手动安装（Ubuntu/Debian）：
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
docker version && docker compose version
```

国内拉镜像慢时，给 Docker 配阿里云镜像加速（控制台“容器镜像服务 ACR”里可拿到专属地址）：

```bash
cat >/etc/docker/daemon.json <<'JSON'
{ "registry-mirrors": ["https://<你的专属ID>.mirror.aliyuncs.com"] }
JSON
systemctl restart docker
```

## 3. 上传部署产物

把 `deploy/dist/` 里的对应文件传到服务器，例如 `/opt/elbot`：

- **宝塔文件管理器**：直接上传 `elbot-0.5.0-offline-amd64.tar.gz` 到 `/opt/elbot`。
- **SCP**（本地执行）：

```bash
scp elbot-0.5.0-offline-amd64.tar.gz root@<公网IP>:/opt/elbot/
# 如果要用 docker load 方案，再传：
scp elbot-0.5.0-linux-amd64.tar.gz root@<公网IP>:/opt/elbot/
```

- **阿里云 OSS**：先传到 OSS，服务器上用带签名的 URL `wget`。

> 轻量服务器有月流量包，方案 B 只需拉约 30MB 的 `debian:bookworm-slim` + apt 依赖；
> 千万不要在服务器上跑源码构建（会拉 `golang:1.26`，约 1GB）。

## 4. 一键部署

```bash
mkdir -p /opt/elbot && cd /opt/elbot
tar -xzf elbot-0.5.0-offline-amd64.tar.gz
cd offline-amd64

# 首次运行会生成 .env 并提示你填写，然后退出
bash deploy.sh

# 填 Key / token
vi .env          # DEEPSEEK_API_KEY、ELBOT_CLI_LOCAL_TOKEN、ELNIS_HOME_TOKEN
chmod 600 .env

# 再执行
bash deploy.sh                 # 推荐：预编译二进制 + Debian 运行时，功能完整
# 或完全离线（scratch 镜像，无 shell 工具）：
# bash deploy.sh --load ../elbot-0.5.0-linux-amd64.tar.gz
```

脚本会做：架构检测 → Docker 检查 → 创建并 `chown 10001:10001` 数据目录 → 构建/加载镜像 → `docker compose up -d`。

查看状态：

```bash
docker compose ps
docker inspect --format '{{.State.Health.Status}}' elbot
docker compose logs -f --tail=200
```

## 5. 配置 ElBot

首次启动后在宿主机生成：

```
offline-amd64/data/config/elbot/app.toml
offline-amd64/data/config/elbot/providers.toml
offline-amd64/data/config/elbot/state.toml
offline-amd64/data/config/elbot/elnis.toml
```

按仓库 `deploy/README.md` 第 4 节改：

- `providers.toml`：确认 `api_key_env` 与 `.env` 变量名一致；大陆机器访问 OpenAI 需要配 `proxy`，或换 DeepSeek 等国内 Provider。
- `state.toml`：把模型名改成你 Provider 实际支持的。
- `app.toml`：启用 CLI 远程服务端：
  ```toml
  [platform.cli.server]
  enabled = true
  listen = "0.0.0.0:32172"     # 容器内监听，宿主机只映射 127.0.0.1
  ```
- `elnis.toml`：需要外部事件时 `enabled = true`，`[http] addr = "0.0.0.0:32170"`。

改完配置：

```bash
docker compose up -d     # 改 .env 必须用 up -d；只改 toml 可以用 restart
```

## 6. 宝塔 Nginx 反代 + HTTPS

1. 宝塔 → 网站 → 添加站点（你的域名）。
2. 站点设置 → SSL → Let's Encrypt，开启强制 HTTPS。
3. 把 `nginx-elbot.conf` 的内容加入该站点配置（或宝塔“反向代理”手动添加）：
   - `/cli/v1/ws` → `http://127.0.0.1:32172`（WebSocket）
   - `/elvena/` → `http://127.0.0.1:32170`
   - `/elbot/healthz` → `http://127.0.0.1:32170/healthz`
4. `nginx -t && nginx -s reload`。

完成后：CLI 客户端连 `wss://你的域名/cli/v1/ws`；外部 Elwisp 投递 `https://你的域名/elvena/v3/events`。

## 7. 开机自启

```bash
cp elbot-compose.service /etc/systemd/system/
# 编辑 WorkingDirectory 为 /opt/elbot/offline-amd64
vi /etc/systemd/system/elbot-compose.service
systemctl daemon-reload
systemctl enable --now elbot-compose
systemctl status elbot-compose
```

## 8. 验证清单

```bash
uname -m                                  # 确认架构
docker compose ps                         # 容器 Up / healthy
curl -sS http://127.0.0.1:32170/healthz   # Elnis 健康检查（若启用）
curl -sS -o /dev/null -w '%{http_code}\n' https://你的域名/elbot/healthz
docker compose logs --tail=100            # 无 TOML / Key 报错
```

## 常见问题（阿里云轻量特有）

| 现象 | 处理 |
| --- | --- |
| 外网访问不了 80/443 | 检查**轻量控制台防火墙**是否放行；再检查系统 ufw/firewalld |
| 容器反复重启 | `docker compose logs`；多半是 `data/` 属主不是 10001，或 `.env` Key/token 没填 |
| Nginx 502 | 容器没起、端口没映射、或 app 监听在 127.0.0.1（容器内要监听 0.0.0.0） |
| 拉取 debian 很慢/失败 | 配阿里云镜像加速，或 `docker compose build --build-arg BASE_IMAGE=registry.cn-hangzhou.aliyuncs.com/library/debian:bookworm-slim` |
| 磁盘不够（40G） | 不要用源码构建；定期 `docker system prune -af`、`docker builder prune -f`；日志/媒体见 `deploy/README.md` 第 8 节 |
| 想用阿里云容器镜像服务 ACR | 本地/CI `bash deploy/build-push.sh registry.cn-hangzhou.aliyuncs.com/<命名空间>/elbot:0.5.0`，服务器 `docker login` 后 `docker compose pull && up -d` |
