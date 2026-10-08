# 冒烟与稳定性测试记录

本文记录本机对 fork 做的冒烟/稳定性测试：跑了什么、实测结果、以及本机 Docker 的两个坑。
**只写实测到的事实**，没有跑到的项明确标出。

## 1. 结论速览

| 测试 | 结果 |
| --- | --- |
| 仓库自带部署脚本测试（`deploy/tests/*.sh`，7 个） | **7/7 通过** |
| Go 测试稳定性（受影响 11 个包 × 3 轮，`-shuffle=on`） | 16 个包全绿；**仅 2 个测试确定性失败**（Windows 无 `sh`，见 §2） |
| 同样 17 个包在 **Linux 容器**内跑 | **全部 `ok`，0 失败**（含 `internal/agent`） |
| 生产镜像构建（`deploy/Dockerfile` → `runtime` target） | **成功**，镜像内 `elbot --version` = `0.6.8` |
| 容器运行冒烟 | **通过**：`/live`、`/ready`、`/healthz`（带 token）、`/metrics`（带 token）均 200；无 token 时 `/healthz` 为 401；数据目录自动生成；SIGTERM 退出码 0 |

## 2. 那两个失败只是"本机没有 sh"

`TestEmoticonHookSendsSeparateOutputAndCleansPersistedContent` 与
`TestToolCallAssistantEmoticonSendsBeforeFinalResponse` 报 `exec: "sh": executable file
not found in %PATH%`。3 轮随机顺序运行中 **3/3 稳定失败**（不是 flake）。

判定为环境问题而非代码缺陷的依据：用 `deploy/Dockerfile` 的 `go-runtime` 阶段（真
Linux + 自带 `sh`）在容器内跑同一个包：

```
ok  elbot/internal/agent  4.140s
```

在 Linux 上这两个测试**通过**。Windows 的 Go 走 Windows 的 PATH，即使从 WSL 里启动
Windows 版 `go.exe` 也找不到 WSL 的 `/usr/bin/sh`（已实测确认），所以只能在 Linux 里验。

## 3. 稳定性测试数值

3 轮，每轮 `go test -count=1 -vet=off -shuffle=on -p 1`，覆盖 `logging`、`session`、
`config`、`storage/sqlite`、`cron`、`elnis`、`app`、`delivery`、`tool`、`hook/...`、`agent`：

- 16 个包每轮全部 `ok`；
- 每轮仅 `internal/agent` FAIL，且失败集合恒定为上述 2 个测试（3/3，确定性）；
- 单轮耗时 46–58 秒；
- **没有出现任何偶发（flake）失败**——包括之前在完整套件里偶发的 `TempDir RemoveAll
  cleanup: directory is not empty` 也没有复现。

同一组包在 Linux 容器内一次性全绿（`internal/agent` `ok 4.322s`）。

## 4. 容器冒烟细节

镜像：`elbot:smoke-0.6.8`（`--build-arg VERSION=0.6.8 GOPROXY=https://goproxy.cn,direct
APT_MIRROR=mirrors.aliyun.com`）。

实测到的端点行为（宿主端口映射到容器 `32171`）：

| 请求 | 结果 |
| --- | --- |
| `GET /live` | `200 {"status":"live","version":"0.6.8",...}` |
| `GET /ready` | `200 {"status":"ready","ready":true,"checks":{...:"ok"}}` |
| `GET /healthz`（无 token） | `401` |
| `GET /healthz`（`X-Elbot-Ops-Token`） | `200 {"status":"ok","live":true,"ready":true,"degraded":false,...}` |
| `GET /metrics`（`X-Elbot-Ops-Token`） | `200` |

首次启动自动生成数据目录：`app.toml`、`services.toml`、`state.toml`、`elnis.toml`、
`tool_tags.toml`、`memories.toml`、`SOUL.md`、`angel_memory.db`、`self_learning.db`、
`plugins/`、`skills/`、`long_memory/`。

优雅停止：`docker stop` 后 `exit code = 0`、`OOMKilled = false`。

> 注意：不带 `ELBOT_OPS_TOKEN` 时 `/healthz` 期望返回 **404**（未注册，安全默认），
> 与带 token 时的 200 是两种不同行为；`deploy/README.md` 已说明该默认。Elwis 的
> `32170/healthz` 是另一回事（仅 Elnis 启用时），不要混用。

## 5. 本机 Docker 的两个坑（实测）

### 5.1 从 M: 盘挂载数据卷会静默卡死

仓库位于 `M:\`。用 `-v "M:\...\.smoke\data:/data"` 启动时，容器**永远停在
`Created`**，`docker inspect` 的 `.State.Error` 为空、没有日志、容器从不进入
`running`；`docker run -d` 却返回了容器 ID，看起来像"启动很慢"。同一个镜像不挂载
时立刻 `running`，从 `C:\` 挂载也正常。

- 影响：`deploy/docker-compose.yml` 用 `./data:/data`，因此在 `M:\` 下直接
  `docker compose up -d` 会踩到这个坑。
- 规避（本次冒烟采用）：把数据卷放在 `C:` 下，或把仓库移到 `C:`，或在 Docker Desktop
  的 File Sharing 里共享 `M:`。
- 这是**本机 Docker Desktop 配置问题**，不是 ElBot 缺陷；未去改 Docker Desktop 设置。

### 5.2 反复创建/删除容器后 daemon 会变慢

连续多轮 `docker run` + `docker rm -f` 之后，新容器也会停在 `Created`（同样的静默
表现）。此时 `docker ps` 本身也开始变慢。实测**把卡住的容器删掉后，普通容器又能正常
启动**，因此不是镜像问题；如果再次出现，优先怀疑 daemon 需要重启，而不是应用回归。

## 6. 未覆盖的部分（诚实标注）

- 没有跑完整 `go test ./...` 全仓（只覆盖了改动相关的包集合 + `hook/...`）。
- 没有用真实平台（OneBot / Telegram / QQ 官方）做端到端消息冒烟：这些需要外部服务与
  密钥，本次没有条件。
- 没有跑 `-race`：本机 Go 报 `-race requires cgo`，本机未启用 cgo。
- 没有跑 `deploy/tests` 之外的运维脚本（`deploy/*.sh` 的备份/升级/回滚主脚本），
  只跑了它们对应的测试脚本。
- 没有验证 QQ OneBot 的 3000 rune 合并转发、媒体入库到 S3 等需要真实依赖的路径。
