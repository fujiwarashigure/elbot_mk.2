# ElBot 本地 Windows 容器部署（Docker Desktop）

本文档面向**本地 Windows 10/11 + Docker Desktop（WSL2 后端、Linux containers）**的部署场景。
它复用仓库已有的 `deploy/docker-compose.yml`、`deploy/Dockerfile`、`.env.example` 和运维脚本，
不单独维护第二套 Compose。

> 云服务器 / 宝塔 Linux 部署请看 [`../README.md`](../README.md)。
> 离线预编译包请看 [`../pack/README-OFFLINE.md`](../pack/README-OFFLINE.md)。

---

## 1. 适用场景与边界

适合：

- 在自己的 Windows 电脑上长期常驻一个 ElBot 实例；
- 希望用 Docker 隔离运行环境，但数据、配置仍然放在仓库的 `deploy/data`；
- 希望用命令行查看 `/live`、`/ready`、`/healthz`、`/tasks`、`/metrics`、`/diagnostics`，并执行启动、停止、重启、查看日志等后台控制；
- 希望复用 `elbot doctor`、备份、恢复验证、升级、回滚等已有能力。

不适合：

- Windows containers。ElBot 只有 Linux 容器镜像，必须使用 Docker Desktop 的 Linux containers。
- 多副本。ElBot 是单实例、有状态服务，只允许一个容器访问同一份 `deploy/data`。
- 把 `32171` / `32172` / `32170` 直接暴露到公网。它们默认只绑定 `127.0.0.1`，需要公网入口时应在前面加 HTTPS/WSS 反向代理。

---

## 2. 前置条件

| 依赖 | 要求 |
| --- | --- |
| Windows | Windows 10 21H2+ / Windows 11，建议开启 WSL2 |
| Docker | Docker Desktop，设置里使用 **WSL 2 based engine**，并保持 **Linux containers** |
| PowerShell | Windows PowerShell 5.1+ |
| Git | 可选。`git clone` 需要；备份 / 恢复验证 / 升级 / 回滚复用 `deploy/*.sh`，需要 Git for Windows 的 `bash.exe` 在 `PATH` 中 |
| 磁盘 | 镜像 + 数据至少预留 10 GB，实际取决于媒体量 |

### 2.1 安装 Docker Desktop（安装包在本地 / 非 C 盘）

如果 Docker Desktop 尚未安装，且安装包已放在 `M:\GIT\Docker Desktop Installer.exe`，
可以在管理员权限下执行以下命令，把程序、WSL 数据都放到 M: 盘：

- 安装包：`M:\GIT\Docker Desktop Installer.exe`
- 程序目录：`M:\Docker\Docker`
- WSL 数据目录：`M:\Docker\wsl`
- Windows containers：ElBot 不需要，安装时禁用

```powershell
$installer = 'M:\GIT\Docker Desktop Installer.exe'
$installDir = 'M:\Docker\Docker'
$wslDataRoot = 'M:\Docker\wsl'

if (-not (Test-Path -LiteralPath $installer)) {
    throw "安装包不存在: $installer"
}

Start-Process -FilePath $installer -Wait -Verb RunAs -ArgumentList @(
    'install'
    '--quiet'
    '--accept-license'
    '--backend=wsl-2'
    '--no-windows-containers'
    "--installation-dir=$installDir"
    "--wsl-default-data-root=$wslDataRoot"
)
```

> 需要管理员权限；如果当前 PowerShell 不是管理员，`-Verb RunAs` 会弹出 UAC。
> 目标路径可以按需修改，但不要放到 C 盘。

安装完成后启动：

```powershell
Start-Process 'M:\Docker\Docker\Docker Desktop.exe'
```

首次启动后确认：

1. **Settings → General**：`Use the WSL 2 based engine` 已勾选；
2. **Settings → Resources → WSL Integration**：勾选 Ubuntu；
3. **Settings → Resources → Advanced**：`Disk image location` 指向 `M:\Docker\wsl`。

> `--installation-dir` 和 `--wsl-default-data-root` 会把最大的程序、镜像和容器数据移出 C 盘。
> Windows 仍可能在 `C:\Users\<用户>\AppData\Local\Docker`、`C:\ProgramData\Docker*`
> 留下少量配置、日志或服务文件，无法做到 100% 不占 C 盘。

### 2.2 检查 Docker Desktop

检查 Docker Desktop：

```powershell
docker version
docker compose version
docker info
```

> 如果 `docker info` 报错，先启动 Docker Desktop，等托盘状态变成 **Running** 再继续。

---

## 3. 文件说明

```
deploy/windows/
├── elbot.ps1      # 主入口：部署、状态、后台控制、诊断、备份、计划任务
├── elbot.cmd      # cmd.exe 轻量包装：deploy\windows\elbot.cmd status
└── README.md      # 本文档
```

以下目录仍使用仓库已有约定：

```
deploy/
├── docker-compose.yml   # 单实例编排，Windows / Linux 共用
├── Dockerfile           # 运行镜像 / go-runtime 镜像
├── .env.example         # 环境变量模板
├── data/                # 容器数据卷（首次 init 自动创建）
├── backups/             # backup.sh 默认备份目录
├── portainer/           # 可选：Portainer 浏览器 Docker 管理界面
│   └── portainer-compose.yml  # 仅绑定 127.0.0.1:9443，本地 / 云共用
├── VERSION              # 版本号单点来源，当前 0.6.8
└── ...
```

---

## 4. 首次部署

### 4.1 克隆仓库

```powershell
git clone https://github.com/fujiwarashigure/elbot_mk.2.git
cd ".\elbot_mk.2"
```

### 4.2 初始化

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\windows\elbot.ps1 init
```

`init` 会：

1. 创建 `deploy/data/{config,run,logs,cache}`；
2. 如果 `deploy/.env` 不存在，从 `deploy/.env.example` 复制一份；
3. 打印首次启动检查清单。

也可以直接在 Git Bash / PowerShell 中调用：

```powershell
.\deploy\windows\elbot.cmd init
```

### 4.3 填写 `.env`

至少填写：

```dotenv
DEEPSEEK_API_KEY=sk-...
ELBOT_CLI_LOCAL_TOKEN=随机长字符串
ELNIS_HOME_TOKEN=随机长字符串
```

如果启用 QQ OneBot / Telegram / 生图，再填写对应 Key：

```dotenv
QQONEBOT_ACCESS_TOKEN=...
TELEGRAM_BOT_TOKEN=...
IMAGE_API_KEY=...
```

> `.env` 同时会作为 ElBot 进程环境和 Compose 注入源。不要把面向群聊用户开放的实例配置成无限额度主 Key。

### 4.4 构建并后台启动

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\windows\elbot.ps1 start
```

`start` 默认执行等价于：

```powershell
docker compose --env-file deploy\.env -f deploy\docker-compose.yml up -d --build --remove-orphans
```

脚本会：

- 使用 `deploy/VERSION` 作为镜像版本（当前 `0.6.8`）；
- 后台启动 `elbot` 容器；
- 等待 `/ready`；
- 打印容器状态与 `/live`、`/ready`、`/healthz`。

如果只想使用已经构建好的镜像，不重新构建：

```powershell
.\deploy\windows\elbot.ps1 start -NoBuild
```

### 4.5 首次启动后检查配置

```powershell
Get-ChildItem .\deploy\data\config\elbot
```

重点检查：

- `services.toml`（旧部署 `providers.toml`）：`api_key_env` 名称与 `.env` 一致，Provider 和生图配置都在这里；
- `state.toml`：provider / model 是否真实可用；
- `app.toml`：CLI / OneBot / Elnis / 行为 / 安全配置。

改 TOML 后：

```powershell
.\deploy\windows\elbot.ps1 restart
```

改 `.env` 后必须重建容器重新注入环境变量：

```powershell
.\deploy\windows\elbot.ps1 recreate
```

---

## 5. 查看运行状态与内置诊断

### 5.1 一条命令看总览

```powershell
.\deploy\windows\elbot.ps1 status
```

输出包含：

- 源码版本、Compose / `.env` / `data` 路径；
- Docker / Compose 是否可用；
- 容器 `State` / `Health` / 镜像；
- Windows 计划任务是否注册；
- `/live`、`/ready`、`/healthz` 状态；
- `ELBOT_OPS_TOKEN` 是否配置。

需要接入脚本或 CI 时：

```powershell
.\deploy\windows\elbot.ps1 status -Json
```

### 5.2 独立健康接口

```powershell
.\deploy\windows\elbot.ps1 health
```

对应：

| 接口 | 含义 |
| --- | --- |
| `/live` | 进程是否仍在运行 |
| `/ready` | 初始化、数据目录、调度心跳是否就绪 |
| `/healthz` | 聚合状态；设置 `ELBOT_OPS_TOKEN` 后需要鉴权；平台/模型故障显示 `degraded` |

### 5.3 活跃任务、资源与聚合诊断

```powershell
.\deploy\windows\elbot.ps1 tasks
.\deploy\windows\elbot.ps1 metrics
.\deploy\windows\elbot.ps1 diagnostics
```

分别读取容器内 ElBot 的 `/tasks`、`/metrics`、`/diagnostics`：

- `/tasks`：活跃 turn / tool / hook / 上下文压缩任务、阶段、排队积压；
- `/metrics`：任务、资源、平台、模型、熔断、限速、生图队列；
- `/diagnostics`：面向“机器人没回复”的聚合诊断。

如果设置了 `ELBOT_OPS_TOKEN`，脚本会自动读取 `deploy/.env` 并带上
`Authorization: Bearer <token>` 与 `X-Elbot-Ops-Token`。未设置时敏感运维接口默认不注册，只保留 `/live` 和 `/ready`；排障时如需临时无鉴权访问，可在 `.env` 显式设置 `ELBOT_OPS_ALLOW_UNAUTHENTICATED=1`，但只应用于本机回环/可信网络。

### 5.4 复用 `elbot doctor`

```powershell
# 容器内运行；只做配置 / 存储 / 端口 / 平台 / 模型检查
.\deploy\windows\elbot.ps1 doctor --no-model

# 完整部署验收；需要 CLI 远程 server 已启用
.\deploy\windows\elbot.ps1 doctor --e2e

# JSON 输出
.\deploy\windows\elbot.ps1 doctor --e2e --json
```

它内部执行的是：

```powershell
docker compose ... exec -T elbot elbot doctor ...
```

与云服务器部署使用同一套 `elbot doctor`，不额外实现 Windows 专用验收逻辑。

---

## 6. 后台控制

| 动作 | 命令 | 说明 |
| --- | --- | --- |
| 后台启动 / 确保运行 | `.\deploy\windows\elbot.ps1 start` | 默认构建并 `up -d` |
| 停止容器 | `.\deploy\windows\elbot.ps1 stop` | 保留容器和 `deploy/data` |
| 停止并移除容器 | `.\deploy\windows\elbot.ps1 down` | 保留 `deploy/data` |
| 重启进程 | `.\deploy\windows\elbot.ps1 restart` | 不重新注入 `.env` |
| 强制重建 | `.\deploy\windows\elbot.ps1 recreate` | 改 `.env` / Dockerfile 后用 |
| 查看日志 | `.\deploy\windows\elbot.ps1 logs -Follow -Tail 200` | 等价于 `docker compose logs` |
| 进入容器 | `.\deploy\windows\elbot.ps1 shell` | 进入容器内 `bash` |
| 容器内执行命令 | `.\deploy\windows\elbot.ps1 exec elbot config check` | `docker compose exec -T` |
| 透传 Compose | `.\deploy\windows\elbot.ps1 compose ps` | 直接执行 `docker compose ...` |

### 6.1 登录后自动启动

Docker Desktop 只有在运行后，容器才会被 `restart: unless-stopped` 自动拉起。
如果希望 Windows 登录后自动确保 ElBot 启动：

```powershell
.\deploy\windows\elbot.ps1 install-service
```

这会注册计划任务 `ElBot-Docker`，登录后执行：

```text
powershell -NoProfile -ExecutionPolicy Bypass -File <repo>\deploy\windows\elbot.ps1 start -NoBuild
```

要求已经成功执行过一次 `start`，镜像存在；否则计划任务会失败并记录在任务历史中。

查看 / 移除：

```powershell
Get-ScheduledTask -TaskName ElBot-Docker
.\deploy\windows\elbot.ps1 uninstall-service
```

> 如果注册计划任务提示权限不足，请用“以管理员身份运行”的 PowerShell 再执行一次，
> 或用 Windows“任务计划程序”手动创建“登录时触发”的任务。

---

## 7. 数据、备份、恢复、升级与回滚

### 7.1 数据目录

所有持久化数据都在：

```text
deploy\data\
├── config\elbot\   # app.toml / services.toml / state.toml（旧部署为 providers.toml）
├── elbot\          # SQLite、日志、sandbox
├── cache\          # Go Skill / 媒体临时缓存
└── run\            # 运行标记、重启原因
```

不要同时启动多个容器挂载同一个 `deploy/data`。

### 7.2 备份

```powershell
.\deploy\windows\elbot.ps1 backup
```

该动作调用 `deploy/backup.sh`，与 Linux 部署使用同一套：

- 有 `sqlite3`：热备 SQLite + 归档；
- 没有 `sqlite3`：短暂停止容器、归档后自动启动并等待 `/ready`；
- 生成 sha256 manifest，默认保留最近 14 份。

默认备份到 `deploy\backups\elbot-data-*.tar.gz`。

### 7.3 恢复验证

```powershell
.\deploy\windows\elbot.ps1 restore-verify .\deploy\backups\elbot-data-YYYYmmdd-HHMMSS.tar.gz
```

该动作调用 `deploy/restore-verify.sh`，在隔离目录校验 manifest、SQLite、TOML、
角色素材与本地媒体路径；不会接触生产 `deploy/data`。

### 7.4 升级与回滚

```powershell
.\deploy\windows\elbot.ps1 upgrade
.\deploy\windows\elbot.ps1 rollback
```

分别调用 `deploy/upgrade.sh` / `deploy/rollback.sh`：

- 升级前配置检查、数据快照、镜像快照；
- 构建新版本镜像并重建；
- 升级后等待健康检查；
- 回滚时先校验数据快照，再恢复。

> 上述动作需要 `bash.exe`。推荐安装 Git for Windows，并在 PowerShell 中确认
> `Get-Command bash.exe` 能找到它。找不到时脚本会给出明确提示。
> 如果使用 WSL2，也可以进入 WSL 后按 `deploy/README.md` 的 Bash 命令手动执行。

---

## 8. 与已有部署方式 / 内置工具的关系

| 已有能力 | Windows 本地容器部署如何复用 |
| --- | --- |
| `deploy/docker-compose.yml` | 直接使用，不复制第二份 Compose |
| `deploy/Dockerfile` | `start` / `recreate` / `upgrade` 构建同一镜像 |
| `.env.example` | `init` 复制为 `deploy/.env`，字段与 Linux 完全一致 |
| `deploy/VERSION` | 本地镜像版本的单点来源，脚本会设置 `ELBOT_VERSION` / `ELBOT_IMAGE` |
| `elbot doctor` | `doctor` 动作通过 `docker compose exec` 在容器内执行 |
| `/live` `/ready` `/healthz` | `status` / `health` 直接请求宿主机映射端口 |
| `/tasks` `/metrics` `/diagnostics` | `tasks` / `metrics` / `diagnostics` 读取同一组接口，自动带 `ELBOT_OPS_TOKEN` |
| `backup.sh` / `restore-verify.sh` | `backup` / `restore-verify` 通过 Git Bash 调用 |
| `upgrade.sh` / `rollback.sh` | `upgrade` / `rollback` 通过 Git Bash 调用 |
| `elbot-compose.service` | Windows 下对应计划任务 `ElBot-Docker`，登录后 `start -NoBuild` |

---

## 9. 网络配置

### 9.1 端口映射

`deploy/docker-compose.yml` 只把端口绑定到宿主机回环：

```yaml
ports:
  - "127.0.0.1:32172:32172"   # CLI 远程 WebSocket
  - "127.0.0.1:32171:32171"   # 独立健康接口
  - "127.0.0.1:32170:32170"   # Elnis HTTP（默认关闭）
```

Windows 本机可以直接访问 `http://127.0.0.1:32171/...`；
局域网或公网访问请使用反向代理 / 隧道，不要直接把端口绑到 `0.0.0.0`。

### 9.2 QQ OneBot 跑在 Windows 宿主机

Docker Desktop 通常已经内置 `host.docker.internal`。把 `app.toml` 的 OneBot `ws_url` 写成：

```toml
ws_url = "ws://host.docker.internal:6700/"
```

不要写 `127.0.0.1`，因为那是容器自己的回环地址。

### 9.3 数据卷权限

容器以 UID/GID `10001` 运行。Windows 绑定挂载通常可写；如果日志出现
`permission denied` 或 SQLite 无法写入，按顺序检查：

```powershell
.\deploy\windows\elbot.ps1 logs -Tail 200
docker compose -f deploy\docker-compose.yml exec -u root elbot chown -R 10001:10001 /data
```

如果仍然失败，建议把仓库放到 WSL2 文件系统（例如 `\\wsl$\Ubuntu\home\...`），
或在 Docker Desktop 中重新启用该磁盘的文件共享。

---

## 10. 常见问题

| 现象 | 排查 |
| --- | --- |
| `docker info` 失败 | Docker Desktop 未启动，或切换到了 Windows containers；启动并切回 Linux containers |
| `start` 很慢 | 首次构建要拉基础镜像并编译 Go，后续会使用 Docker 层缓存 |
| 容器反复重启 | `.\deploy\windows\elbot.ps1 logs -Tail 200`；多数是 TOML 语法错误 / Key 未填 / 数据目录不可写 |
| `32171` 连接被拒绝 | 容器未运行、端口映射未生效，或 `ELBOT_HEALTH_ADDR` 被错误改成不监听；先看 `status` |
| `/ready` 长期 `not_ready` | 检查数据目录可写、调度心跳是否过期、`ELBOT_HEALTH_ADDR` 是否配置；平台/模型故障不会让它失败 |
| `/healthz` 为 `degraded` | 通常是模型 API / 中转站异常；先查上游，不要直接重启 |
| 计划任务没有自动启动 | 确认 Docker Desktop 已随 Windows 启动、任务 `ElBot-Docker` 已注册、镜像已构建；查看任务历史 |
| `backup` 提示找不到 `bash.exe` | 安装 Git for Windows 并确保 `bash` 在 `PATH`；或在 Git Bash 中直接运行 `deploy/backup.sh` |
| 端口被占用 | `netstat -ano | findstr :32171` 后释放占用进程，或修改 `deploy/docker-compose.yml` 映射 |
| 想同时跑多个实例 | 当前不支持共享 `deploy/data` 的多实例；需要复制仓库目录并改端口 / `COMPOSE_PROJECT_NAME`，并明确各自数据独立 |

---

## 11. 一句话流程

```powershell
# 首次
git clone https://github.com/fujiwarashigure/elbot_mk.2.git
cd .\elbot_mk.2
.\deploy\windows\elbot.ps1 init
notepad .\deploy\.env
.\deploy\windows\elbot.ps1 start

# 日常
.\deploy\windows\elbot.ps1 status
.\deploy\windows\elbot.ps1 health
.\deploy\windows\elbot.ps1 doctor --no-model
.\deploy\windows\elbot.ps1 logs -Follow -Tail 200

# 后台自启
.\deploy\windows\elbot.ps1 install-service
```

---

## 12. 与云服务器部署对照

| 操作 | 云服务器 / 宝塔 Linux | 本地 Windows + Docker Desktop |
| --- | --- | --- |
| 初始化 | `bash deploy/init-host.sh` | `.\deploy\windows\elbot.ps1 init` |
| 启动 | `bash deploy/init-host.sh` / `docker compose up -d` | `.\deploy\windows\elbot.ps1 start` |
| 状态 | `docker compose ps` + `curl /live` | `.\deploy\windows\elbot.ps1 status` |
| 诊断 | `/metrics` `/diagnostics` + watchdog | `.\deploy\windows\elbot.ps1 metrics/diagnostics` |
| 验收 | `docker compose exec -T elbot elbot doctor` | `.\deploy\windows\elbot.ps1 doctor` |
| 自启 | `systemctl enable --now elbot-compose` | `.\deploy\windows\elbot.ps1 install-service` |
| 备份 / 升级 | `bash deploy/backup.sh` / `bash deploy/upgrade.sh` | 同名脚本，由 PowerShell 封装调用 |

---

## 13. 静态自测

在 Windows 上执行 PowerShell 语法检查和帮助输出：

```powershell
powershell -NoProfile -Command "$e=$null; $t=$null; [System.Management.Automation.Language.Parser]::ParseFile((Resolve-Path '.\deploy\windows\elbot.ps1'),[ref]$t,[ref]$e) > $null; if ($e.Count) { $e } else { 'elbot.ps1 parse ok' }"
.\deploy\windows\elbot.ps1 help
```

在任意有 `bash` / `python` 的环境执行字节级约束自测：

```bash
bash deploy/tests/windows_script_test.sh
```

该测试检查 `elbot.ps1` 的 UTF-8 BOM、`elbot.cmd` 的 CRLF、以及各 README / CHANGELOG 引用的版本号是否与 `deploy/VERSION` 一致。

---

## 14. Portainer 浏览器可视化运维（可选）

ElBot 本身没有通用的镜像 / 容器管理 Web UI；日常可以用 Docker Desktop 和 `elbot.ps1`。
如果希望本地 Windows 和云服务器使用同一套浏览器界面管理 Docker，可选用仓库自带的：

```text
deploy/portainer/portainer-compose.yml
```

它只绑定宿主机回环地址：

```text
127.0.0.1:9443 -> 9443/tcp
```

不会直接把 Portainer 暴露到公网。

### 14.1 本地启动与访问

```powershell
cd 'M:\GIT\elbot\elbot_mk.2 0.6.1'
docker compose -f deploy\portainer\portainer-compose.yml up -d
```

浏览器访问：

```text
https://127.0.0.1:9443
```

第一次访问会要求设置管理员密码。Portainer 使用自签名证书，浏览器会提示证书不受信任，本地确认继续即可。

> Portainer 2.45.x 首次进入还可能要求填写 **setup token**。执行下面的命令，
> 从日志里取 `setup_token=...`，粘贴到 Setup token 输入框；设置完管理员后该 token 即失效：
>
> ```powershell
> docker logs portainer 2>&1 | Select-String 'setup_token='
> ```

查看 / 停止：

```powershell
docker compose -f deploy\portainer\portainer-compose.yml ps
docker compose -f deploy\portainer\portainer-compose.yml down
```

`down` 只停容器并保留 `portainer_data` 卷；要同时删除 Portainer 自身配置数据才用：

```powershell
docker compose -f deploy\portainer\portainer-compose.yml down -v
```

ElBot 数据在 `deploy\data`，不会因为删除 `portainer_data` 而丢失。

### 14.2 云服务器同端口访问

把同一份 `deploy/portainer/portainer-compose.yml` 传到云服务器后：

```bash
docker compose -f deploy/portainer/portainer-compose.yml up -d
```

端口仍然是 `127.0.0.1:9443`，不要改成 `0.0.0.0`，也不要在云安全组开放 `9443`。

推荐用 SSH 隧道：

```powershell
ssh -L 9443:127.0.0.1:9443 user@你的云服务器
```

然后本地浏览器打开同一个地址：

```text
https://127.0.0.1:9443
```

如果必须走域名，则由宝塔 Nginx / HTTPS 反向代理到：

```text
https://127.0.0.1:9443
```

安全组只放行 `443`，不要直接放行 `9443`。

### 14.3 安全注意事项

- Portainer 挂载了 `/var/run/docker.sock`，等同于 Docker 管理员权限；
- 它能查看和操作所有容器，也能读取容器环境变量中的 token / API Key；
- 必须设置强管理员密码，不要复用 `ELBOT_OPS_TOKEN` / `ELBOT_CLI_LOCAL_TOKEN`；
- 保持 `127.0.0.1:9443` 只绑定回环地址；
- 公网访问只能通过 SSH 隧道或带 HTTPS / 鉴权的反向代理；
- 升级 Portainer：

```bash
docker compose -f deploy/portainer/portainer-compose.yml pull
docker compose -f deploy/portainer/portainer-compose.yml up -d
```

---

## 15. AutoDL ComfyUI 生图接入（规划）

> 状态：当前 ElBot 生图客户端只实现了 OpenAI 兼容的 `/images/generations`。
> 本节先固定 AutoDL / ComfyUI 的接入方式、配置约定、workflow 要求和安全边界；
> 等 `provider = "comfyui"` 实现后，按本节配置即可直接把图发回群。

### 15.1 目标链路

```text
群消息 / image_generate
  -> ElBot provider = "comfyui"
  -> AutoDL ComfyUI HTTP(S) API
       POST /prompt
       GET  /history/{prompt_id}
       GET  /view?filename=...&subfolder=...&type=output
  -> imagegen.Result{Data, MIMEType}
  -> Media Center
  -> 图片段发回当前群
```

发回群这一段复用现有逻辑，不需要为 AutoDL 单独实现发送通道。

### 15.2 三种连接方式

| 方式 | base_url 示例 | 适用场景 | 安全等级 |
| --- | --- | --- | --- |
| SSH 隧道 | `http://host.docker.internal:8188` | 本机/云服务器能 SSH 到 AutoDL | 最高 |
| AutoDL SSL 自定义服务 | `https://uXXXX-xxxx.seetacloud.com:8443` | 云端镜像开机后直接开放 SSL 端口 | 中；URL 泄露等于 GPU 暴露 |
| 自有 HTTPS 反向代理 | `https://comfy.example.com` | 有域名、证书和访问控制 | 高 |

### 15.3 AutoDL SSL 自定义服务方式（推荐日常使用）

1. AutoDL 启动实例，确认 ComfyUI 已在实例内监听，例如 `0.0.0.0:6006` 或 `0.0.0.0:8188`；
2. 在 AutoDL 控制台打开 **自定义服务**，把 ComfyUI 端口映射为 HTTPS 地址；
3. 浏览器能正常打开 ComfyUI 页面后，记录这个 HTTPS 地址；
4. 在 ElBot 容器内验证网络连通：

   ```powershell
   # 在仓库目录执行；把 URL 换成 AutoDL 给出的 SSL 地址
   curl.exe -sS https://uXXXX-xxxx.seetacloud.com:8443/system_stats
   curl.exe -sS https://uXXXX-xxxx.seetacloud.com:8443/queue
   ```

   返回 JSON 才说明 ElBot 能直接调用 ComfyUI API；
5. 后续 `provider = "comfyui"` 实现后，配置写在 `services.toml` 的 `[image_generation]`：

   ```toml
   [image_generation]
   enabled = true
   provider = "comfyui"

   # 方案 A：SSH 隧道
   # base_url = "http://host.docker.internal:8188"

   # 方案 B：AutoDL 自定义服务 SSL 直连
   base_url_env = "COMFYUI_BASE_URL"

   timeout_seconds = 900
   max_concurrent = 1
   queue_size = 4
   queue_timeout_seconds = 600

   workflow_file = "comfyui/workflow_api.json"

   prompt_node_id = "6"
   prompt_input = "text"
   negative_node_id = "7"
   negative_input = "text"
   seed_node_id = "3"
   seed_input = "seed"
   width_node_id = "5"
   width_input = "width"
   height_node_id = "5"
   height_input = "height"
   batch_size_node_id = "5"
   batch_size_input = "batch_size"
   output_node_id = "9"
   poll_interval_seconds = 2
   ```

   对应 `deploy/.env`：

   ```dotenv
   COMFYUI_BASE_URL=https://uXXXX-xxxx.seetacloud.com:8443
   ```

   AutoDL 实例重启后如果 SSL 地址变化，只改 `.env` 再 `recreate`，不用改 `app.toml`。

6. 如果 AutoDL 自定义服务需要额外 Token / Cookie：

   ```toml
   extra_headers = { Authorization = "Bearer <token>" }
   ```

7. 如果证书不受信任，且你确认目标地址可信：

   ```toml
   insecure_skip_verify = true
   ```

   能正常验证证书时不要开启。

### 15.4 SSH 隧道方式

Windows 宿主机开隧道：

```powershell
ssh -p <AutoDL端口> -L 8188:127.0.0.1:8188 root@<AutoDL连接地址> -N
```

ComfyUI 配置：

```toml
provider = "comfyui"
base_url = "http://host.docker.internal:8188"
```

云服务器 Linux 上如果 ElBot 容器访问不到宿主机回环，需要在 Compose 增加：

```yaml
extra_hosts:
  - "host.docker.internal:host-gateway"
```

或让 SSH 隧道运行在与 ElBot 同一 Docker 网络中的 sidecar 容器里。

### 15.5 ComfyUI workflow 要求

- 必须使用 ComfyUI **API 格式** workflow JSON，不是普通 UI 导出格式；
- 在 ComfyUI 开启开发者模式后使用 **Save (API Format)** 导出；
- ElBot 只允许覆盖白名单字段：
  - 正向 prompt 节点
  - negative prompt 节点
  - seed
  - width / height
  - batch size
- 不允许模型直接编辑 workflow JSON 或节点 ID；
- `batch_size` 第一版固定为 `1`，ElBot 的 `count > 1` 仍通过多次调用实现；
- AutoDL 实例重启后 workflow 节点 ID 变化时，需要同步更新配置。

### 15.6 安全与成本控制

- ComfyUI 默认没有用户系统，拿到 AutoDL SSL 地址的人可以占用 GPU、查看输出、提交 workflow；
- 不要把 SSL 地址、Token、Cookie 发到群里或写进公开文档；
- `max_concurrent = 1`，避免 AutoDL GPU OOM 或被刷爆；
- `queue_size` 建议不超过 `4`，`queue_timeout_seconds` 按单张耗时设置；
- 不需要生图时关闭 AutoDL 实例，控制 GPU 计费；
- 优先使用 SSH 隧道或带 Token / 访问控制的自有 HTTPS 反向代理。

### 15.7 待实现与验证

实现 `provider = "comfyui"` 后需要补充：

- `internal/imagegen/comfyui.go`
- `internal/imagegen/provider.go` 工厂
- `internal/config/config.go` 的 provider / workflow / TLS 配置
- `internal/tool/builtin/runtime.go` 按 provider 创建客户端
- `internal/tool/builtin/image_generate.go` 正负 prompt 拆分
- `internal/app/config_check.go` 的 ComfyUI 配置检查
- `docs/image-generation.md` 的正式配置说明
- `httptest` 假 ComfyUI 服务测试
