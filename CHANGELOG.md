## Unreleased

## [v0.6.4 - 2026-10-02]

### Added

- 新增 clean-room `group_analysis` 工具与 `internal/groupanalysis/` 统计服务：只读取本地 `chat_history` / `outbound_messages`，按天统计群消息量、活跃成员和活跃时段，不复制第三方群分析插件的模板、图片、Prompt 或素材。
- Hook 事件新增请求级临时字段：`llm.system_append`、`llm.temperature`、`llm.max_tokens`、`llm.extra_body`；Go Hook 与 `hook.v2` 进程 Hook 均可返回，Agent 只作用于当前 LLM 请求，不写入 Session 历史。
- Chat History 新增可选 `ChatHistoryRangeRepository` 批量时间窗查询；新增 `OutboundMessageRepository` 和 `outbound_messages` 表，记录实际发送的 assistant 文本，供学习和群分析复用。
- 新增可选平台能力接口：`GroupHistoryProvider`、`GroupDirectoryProvider`、`UserAvatarProvider`、`GroupAssetProvider`；OneBot 适配器已实现群历史、群信息、成员列表和头像 URL 的 best-effort 能力。
- 新增 `[group_analysis]` 配置：`enabled`、`max_messages` 和可选 Cron 日报 `report_*`；维护任务清理 chat history 时会同步清理过期 outbound messages。
- `group_analysis` 支持可选 LLM `Summarizer`，摘要使用默认 Session 模式对应模型；Cron 日报可发送统计和摘要到指定平台会话。
- 新增 clean-room `[angel_memory]`：本地 SQLite 长期记忆、`angel_remember` / `angel_recall` 工具，以及 `llm.turn.prepared` 临时 system 上下文注入。
- 新增 clean-room `[self_learning]`：消息观察、表达/黑话候选挖掘、`/learning` 与 `self_learning_review` 的 review-before-apply，只有 approved 内容会注入上下文。
- 新增 `/memory` / `/learning` 管理命令，新增 `[maintenance.privacy_cleanup]` 按功能 retention 清理 angel memory 与 self learning 数据。
- 健康/运维 HTTP 服务新增只读 `/plugins/memory` 和 `/plugins/learning` 状态接口，受 `ELBOT_OPS_TOKEN` 保护。
- Telegram 适配器新增可选群信息和管理员列表能力；QQ Official 明确以本地 chat history 回退。
- Windows 本地部署文档新增 Docker Desktop 非 C 盘安装步骤：支持使用本机 `Docker Desktop Installer.exe`，通过 `--installation-dir`、`--wsl-default-data-root`、`--no-windows-containers` 将程序文件和 WSL 数据放到指定盘。
- 新增 `deploy/portainer/portainer-compose.yml`：Portainer CE 浏览器 Docker 管理界面只绑定 `127.0.0.1:9443`，使用独立 Compose 项目名避免与 ElBot 冲突；`deploy/windows/README.md` 补充本地启动、首次 setup token 获取、云服务器 SSH 隧道 / 反向代理和 Docker socket 安全说明，主 `README.zh-CN.md` 增加入口与快速启动命令。
- `deploy/windows/README.md` 新增 AutoDL ComfyUI 生图接入规划：覆盖 AutoDL SSL 自定义服务、SSH 隧道、workflow API 格式、provider 配置约定、GPU 并发限制和安全注意事项；正式实现后可直接按该章节配置。
- 新增共享只读 `services.toml`：集中 `[providers.*]`、`[model_metadata]`、`[model_profiles]` 和 `[image_generation]`，通过 `[config_files].services` 加载；旧 `providers.toml` 和 `app.toml [image_generation]` 继续兼容。`state.toml` 保持独立，加载时会拒绝把 `state` 指向 `app.toml` / `services.toml` / `providers.toml` 等只读配置，避免运行时 `SaveState` 覆盖静态配置。
- 新增 `services.toml` 默认生成；默认 assets 不再生成 `providers.toml`。`elbot config check` 现在输出实际加载的 `services` / `providers` / `state` 路径。
- `deploy/restore-verify.sh` 的必需配置检查改为 `app.toml` 加（`services.toml` 或旧 `providers.toml`），并在带 `tomllib` 的 Python 中校验 `app.toml` 引用的服务配置存在、`state` 未与只读配置共用同一路径。

### Changed

- 版本号提升到 `0.6.4`；`deploy/VERSION`、Compose 默认镜像、构建/离线脚本和中文部署文档中的版本示例同步更新。
- 文档同步集中服务配置：`docs/configuration.md`、`docs/getting-started.md`、`docs/image-generation.md`、`deploy/README.md`、`deploy/windows/README.md` 和离线包说明改为以 `services.toml` 为主，`providers.toml` 作为旧部署兼容入口。
- 常驻记忆注入 system prompt 时增加 `<resident_memory>` 边界和“用户数据、不是系统指令”的信任声明，并转义记忆内容中的尖括号，防止内容提前结束或伪造边界标签，降低 normal 记忆被用作持久 Prompt 注入向量时的影响。
- 常驻记忆 normal 写入增加服务端保护：`[resident_memory]` 可配置最小写入间隔、窗口内最大写入次数、最大条目数和单条最大长度，并默认逐条拒绝明显指令类内容和控制字符；失败写入不消耗频率额度，core 写入不受 normal 限流影响。
- 常驻记忆 normal 改为结构化保存：每条一行、一行一件事，写入时去掉 `-` / `*` / `1.` 等列表前缀、空行和重复条目，注入 system prompt 时渲染为独立 `-` 列表项，避免多个事实被拼成一段容易被当作指令的文本。
- `memories.toml` 改为原子写入：先写同目录临时文件并 `fsync`，再 `rename` 覆盖目标文件，最后尽力 `fsync` 目录；崩溃或断电时只会留下完整旧文件或完整新文件。写入前检测文件状态，若外部在读取与写入之间修改了文件则重新加载后再应用，避免覆盖手工编辑或恢复脚本的改动。

- `elbot doctor` 新增 `platform_ok` 总字段与 `--require-platform`：平台 `disconnected` 不再标记为通过，严格模式还要求平台已启用且健康快照中存在连接状态；CLI E2E 改为发送唯一探测标记并等待包含该标记的回复，空流结束或无关非空文本都失败。
- `deploy/upgrade.sh` 先切换 `ELBOT_GIT_REF` 再解析未显式指定的目标版本；旧镜像快照改用运行容器的实际 `.Image` ID 并记录 digest；升级前快照默认 `BACKUP_MODE=stop`，并且新镜像配置预检默认挂载生产 `data` 的隔离副本；回滚信息新增 `ROLLBACK_IMAGE_ID` / `ROLLBACK_IMAGE_DIGEST` / `ROLLBACK_FROM_IMAGE`。
- `deploy/rollback.sh` 不再 `source rollback.env`，改为安全解析键值以支持空格路径并避免执行未知键；回滚前先载入旧镜像并用旧镜像 `RESTORE_VERIFY_START=required` 验证备份，恢复后等待 `/ready`、运行 doctor 验收，失败时保留失败数据并尝试恢复 `data.before-rollback-*`。
- `deploy/restore-verify.sh` 的最终状态改为区分 `passed` / `static_passed` / `static_passed_with_skips`，隔离启动跳过时不再伪装成完整 `passed`；`RESTORE_VERIFY_START` 明确接受文档里的 `required`。
- `deploy/windows/elbot.ps1` 增加 Docker Desktop 就绪等待、Git Bash（非 WSL）识别、401 明确报错，并让 `health` / `status` 在 `/ready` 未通过时返回失败；严格备份验证前预检宿主 `sqlite3` 与带 `tomllib` 的 Python。
- Release / Docker CI 增加 `pull_request` 触发、关键包 `-race`、ShellCheck 与 `deploy/tests/*.sh` 部署回归，并显式安装 sqlite3/python3 防止关键测试缺依赖后以 skip 通过。

- `deploy/backup.sh` 的在线 `sqlite` 模式改为先对所有 SQLite 做一致性 `.backup`，再复制媒体等非数据库文件；随后按数据库中的 `backend='local'` 引用补齐缺失媒体，源文件已被删除时直接判定备份失败，避免生成数据库引用完整但归档缺媒体的快照。
- `deploy/restore-verify.sh` 先把归档和 manifest 转成绝对路径，避免相对备份目录在 `sha256sum -c` 进入临时目录后失效；恢复隔离启动前移除归档里的旧服务 PID 标记；Windows Git Bash 下通过 `cygpath` 转换 Docker bind mount 宿主路径，并用 `MSYS_NO_PATHCONV` 保护容器内 `/data` 路径。
- `deploy/rollback.sh` 在恢复数据后移除旧 PID 标记，保证回滚到尚未使用文件锁的旧版本镜像时不会被陈旧标记卡住。

### Fixed

- 修复 `internal/app/service_marker*`：服务互斥从“只信 PID 文件”改为 `flock` 文件锁，进程被硬杀或容器重建后锁会由内核自动释放，不再因 PID 复用/陈旧 PID 标记导致新容器启动失败。
- 统一用户可见错误、Hook 失败和日志/audit 出口的脱敏，并为用户端失败消息附 `error_id`；上游错误包含带 token 的 URL 时，群消息和日志不再包含凭据。
- 修复 `deploy/windows/elbot.ps1` 的 `Invoke-Compose` 参数绑定：Windows PowerShell 5.1 会把 `Invoke-Compose (@(...) + @($Rest))` 折叠成单个带空格字符串，导致 `docker compose` 收到 `"compose up -d --remove-orphans"` 这类错误参数；现在改为普通 `[string[]]` 参数，并把 `Invoke-Compose @doctorArgs` / `@logArgs` / `@Rest` 调用改为直接传数组。

## [v0.6.3 - 2026-10-01]

### Added

- 新增本地 Windows 容器部署：`deploy/windows/elbot.ps1` 与 `elbot.cmd` 复用现有 `deploy/docker-compose.yml`、`Dockerfile` 和 `.env`，支持 `init`、`start`、`recreate`、`stop`、`down`、`restart`、`logs`、`shell`、`compose` 等后台控制，并可注册 `ElBot-Docker` 登录自启计划任务。
- 新增 Windows 命令行运行状态查看：`status` / `health` 读取内置 `/live`、`/ready`、`/healthz`；`tasks` / `metrics` / `diagnostics` 读取 `/tasks`、`/metrics`、`/diagnostics` 并自动携带 `ELBOT_OPS_TOKEN`；`doctor` 在容器内运行同一套 `elbot doctor`。
- 新增 Windows 运维入口：`backup` / `restore-verify` / `upgrade` / `rollback` 复用 `deploy/*.sh`（需要 Git for Windows 的 `bash.exe`），并新增 `deploy/windows/README.md` 完整说明本地部署、排错及与云服务器部署的对照。
- `image_generate` 支持同时索引多个角色：新增 `character_ids`、`reference_images` 和 `count` 参数。默认所有 `@char` / `character_ids` 角色都会写进同一张图；只有显式 `count > 1`（最多 4）时才生成多张，且每张都包含全部角色。多角色参考图以 data URL 数组发送到 `reference_field`。
- 新增发送前长消息保护：按模型窗口、`max_prompt_ratio`、`reserve_output_tokens`、`single_message_max_ratio` 估算 prompt 预算。超限时默认 `reject`，不调用模型并在群里返回报警；可全局或按群设置 `truncate` / `summarize`。新增 `/*overflow` 命令，Bot 超级管理员与当前群群主/管理员可修改 chat/work 策略，覆盖持久化在 `state.toml` 的 `context_overflow`。
- 上下文压缩新增 `[context] user_original_max_runes`（默认 4000）：压缩时每条历史用户原话先限长，避免超长单条消息让摘要自身再次超窗口。
- `deploy/restore-verify.sh` 新增可选隔离启动验收：`RESTORE_VERIFY_START=1`（或 `required`）时使用 `RESTORE_VERIFY_IMAGE` / 当前 `elbot` 容器镜像，以 `--network none` 启动一次性实例并等待 `/ready`；`auto`（默认）在 Docker 和镜像都可用时执行，否则跳过。
- 新增 `deploy/tests/upgrade_script_test.sh`、`deploy/tests/backup_restart_test.sh`、`deploy/tests/restore_verify_start_test.sh`，并扩展 `deploy/tests/watchdog_redaction_test.sh`、`deploy/tests/backup_restore_test.sh` 覆盖上述回归。 新增 `deploy/tests/windows_script_test.sh` 校验 Windows 入口的 BOM、CRLF 包装和版本号引用。

### Changed

- 版本号提升到 `0.6.3`；`deploy/VERSION`、Compose 默认镜像、构建/离线脚本以及所有 README / 部署文档中的版本示例同步更新。
- 仓库 `README.md`、`README.zh-CN.md`、`deploy/README.md` 增加 Windows 本地容器部署章节；`.gitattributes` 新增 `*.ps1` 固定 LF、`*.cmd` 固定 CRLF。

### Fixed

- 修复 `deploy/upgrade.sh` 的 `ELBOT_GIT_REF` 检测：原先假设 `.git` 位于 `deploy/`，会把正常仓库结构 `仓库/.git + 仓库/deploy/upgrade.sh` 误判为非 git 工作区；现在通过 `git -C "$DEPLOY_DIR" rev-parse --show-toplevel` 获取仓库根目录，并在根目录执行 `fetch` / `checkout`。
- 修复 `deploy/upgrade.sh` 用新镜像做配置检查时重复传入 `elbot`：镜像 ENTRYPOINT 已经是 `tini -- /usr/local/bin/elbot`，原命令会展开成 `.../elbot elbot config check`；现在只传 `config check`。
- 修复停机备份后容器恢复失败仍返回成功的问题：`deploy/backup.sh` 现在把 `compose up` 失败或恢复后健康检查超时/异常计入最终退出码，默认最多等待 60 秒（`BACKUP_RESTART_READY_TIMEOUT` 可调）。
- 修复 `deploy/restore-verify.sh` 严格模式仍可能跳过必需检查后输出 `passed` 的问题：缺少带 `tomllib` 的 `python3`、media.local_path 不是 `/data/...` 形式等情况下，严格模式现在直接失败；非严格模式的跳过项会输出 `restore_verify: passed_with_skips`，并分别报告 manifest / toml / database / media_paths / start 的结果。
- 修复 `deploy/elbot-watchdog.sh` 把“重复 sed 幂等”误当成“没有秘密”的复检：现在拆分幂等复检、独立凭据模式检测和当前环境敏感变量值的字面量检测；`mktemp` / `cp` / `sed` 等检查器自身失败会返回非 0，诊断目录从创建起使用 `umask 077`。
- 媒体清理不再在整个清理期间持有全局 `m.objects` 锁，改为按媒体 ID 互斥、最多 4 个对象并发删除；同 ID 导入 / `PresignGet` 也使用同一把对象锁，避免与删除交叉。

## [v0.6.2 - 2026-10-01]

### Fixed

- 修复 `deploy/elbot-watchdog.sh` 的 `redact_diagnostics()`：原先 sed 参数里混入了字面量 `\n` 和控制字节，导致 sed 每次都以 `can't read n` 失败（又被 `|| true` 吞掉），而 `Bearer` / `api_key=` 规则本该写反向引用 `\1` 的地方写成了 0x01 控制字符（脱敏输出会被污染）。现在改为逐文件执行同一组规则，并把 JSON 形式凭据、Telegram bot token、URL userinfo 一并纳入脱敏；新增第二遍复检，脱敏失败会写 `REDACTION-FAILED`、告警并返回非 0，同时提供 `--redact-dir` 手动重脱敏入口和 `deploy/tests/watchdog_redaction_test.sh` 自测。
- 修复 `deploy/backup.sh` 的 `write_manifest()`：同样的字面量 `\n` 会破坏 `find | xargs sha256sum` 流水线且错误被吞；清单现在从**打包好的归档**解压后生成、覆盖归档内全部 `data/` 文件，生成失败直接判定本次备份失败。
- `deploy/backup.sh` 的 `docker compose ps/stop/up` 现在显式带 `-f <compose 文件>` 并在 `deploy/` 下执行（可用 `ELBOT_COMPOSE_FILE` 覆盖）；从 cron 或任意目录调用不会再命中别的 Compose 项目，也不会把运行中的数据当成冷数据打包。
- `deploy/restore-verify.sh` 默认改为严格模式：manifest + sha256sum、SQLite integrity_check 与表结构、`app.toml` / `providers.toml`、TOML 解析（宿主有带 tomllib 的 python3 时）、本地媒体精确路径必须逐项通过，缺依赖或任一失败都不再输出 `passed`；需要降级时用 `RESTORE_VERIFY_STRICT=0`。本地媒体校验从“按文件名查找”改为 `/data/... -> data/...` 精确路径，SQL 查询失败不再回退成 0。
- `deploy/upgrade.sh` 增加版本守卫：目标版本与 `deploy/VERSION` 不一致时拒绝执行，避免“只给当前代码贴新版本号”；需要自动切源码时可用 `ELBOT_GIT_REF=vX.Y.Z`。重建后新增健康检查等待与 `elbot doctor --no-model` 验收，失败即提示回滚命令并以非 0 退出。
- 平台 / 模型的 `last_error` 与最近重启原因在写入健康快照前先做凭据脱敏，避免上游错误里的 token（例如 Telegram bot token 直接出现在请求 URL 中）通过 `/healthz`、`/metrics`、`/diagnostics` 泄露。

### Changed

- `deploy/watchdog.env.example` 新增 `WATCHDOG_READY_ALERT_THRESHOLD` / `WATCHDOG_READY_ALERT_COOLDOWN_SECONDS`：`/live` 正常但 `/ready` 连续失败时只推送一次 `not_ready` 告警（需要配置 webhook），仍然不作为重启条件；watchdog 的重启决策继续只依据 `/live`。
- 版本号提升到 `0.6.2`；`deploy/VERSION`、Compose 默认镜像以及部署/离线文档中的版本示例同步更新。

## [v0.6.1 - 2026-10-01]

### Fixed

- 修复未启用任何平台时只在启动阶段发送一次心跳，导致约 90 秒后 `/live` 过期、watchdog 可能反复重启的问题；空平台模式现在持续发送调度心跳。
- `/live` 改为只表示进程仍在运行；`/ready` 不再因为平台未连接或模型 API 故障而失败，平台和模型状态改由 `/healthz` 的 `degraded` 与状态数组单独展示。调度心跳是否新鲜会作为 `/ready` 的独立检查项。
- 群聊限速从“只命中群级或用户级中的一个”改为先检查用户额度、再检查群级总额度：单个活跃成员无法再耗尽全群配额，群级配额也仍然保护整体资源。
- 新增 `/metrics.rate_limit` 的配置阈值、用户/群级拒绝计数、最近一次限速原因与时间，便于区分“个人刷屏”和“全群过热”。
- 修复诊断包通过 `docker inspect` 写入完整环境变量、可能泄露 Provider API Key / token 的问题；现在只抓容器 State/Ports，并对日志和 JSON 文件做通用凭据脱敏。

### Changed

- Provider 备用策略现在明确区分：默认 `fallback_mode = "circuit"`，仅在熔断打开后切备用；`fallback_mode = "on_error"`（或兼容键 `fallback_on_error = true`）时首个预流式失败请求即可切换。
- 新增 `fallback_timeout_seconds`，可为单次 Provider 尝试设置总超时，避免一次请求被多个上游重试拖得过长。
- Shell / Go Skill 等子进程默认不再继承名字含 `KEY`、`TOKEN`、`SECRET`、`PASSWORD`、`PRIVATE` 的环境变量；Web 搜索、生图和媒体下载等父进程工具仍可读取 `.env` 中的凭据。
- `/tasks` 和 `/metrics` 支持 `ELBOT_OPS_TOKEN`；非回环监听且未配置 token 时启动日志会明确告警。watchdog 可用 `WATCHDOG_OPS_TOKEN` 携带同一 token。
- `deploy/backup.sh` 默认在隔离目录调用新增的 `restore-verify.sh`，验证备份中的 SQLite 完整性、配置文件、角色素材目录和本地媒体引用后才视为成功；`BACKUP_VERIFY=0` 可显式跳过。
- 版本号提升到 `0.6.1`。

### Added

- `/tasks` 新增 `queued_by_kind` 和 `pending_by_kind`，可直接看到受并发上限约束的等待队列与已取得槽位的请求。
- `provider.FallbackTimeoutSeconds` / `fallback_timeout_seconds` 与 `ProviderConfig.UsesFallbackOnError()` 配置入口。
- `deploy/restore-verify.sh`：可独立对任意备份执行隔离恢复验证，不接触生产 `data`。
- `elbot doctor` 部署验收命令：检查配置、健康端口、平台状态、模型调用；加 `--e2e` 后通过 CLI 远程协议做一次真实消息往返，报告区分 `config_ok` 与 `e2e_ok`。
- 新增 `/diagnostics` 聚合诊断接口；`/tasks` 增加排队/超时计数，健康快照增加最近一次重启原因，`/metrics`/`/diagnostics` 可看到熔断、限速和最近重启信息。
- 角色素材元数据新增 `version` / `source`，并提供 `Store.Manifest` / `WriteManifest` 清单能力；备份脚本会为角色和媒体文件生成 sha256 清单，恢复时自动 `sha256sum -c` 校验。
- 新增 `deploy/upgrade.sh` / `deploy/rollback.sh`：升级前做配置检查、数据快照和上一版镜像快照，回滚时先校验数据快照再恢复。


## [v0.6.0 - 2026-10-01]

### Fixed

- 修复 `[ops]` 熔断配置字段缺失（`circuit_breaker_failure_threshold`、`circuit_breaker_open_cooldown_seconds`、`circuit_breaker_half_open_max`）导致 `internal/config` 无法编译的问题。
- 修复 `internal/agent/context_runtime.go` 缺少 `time` 导入、`internal/app/ops_health.go` 缺少限速与生图指标类型，导致整个项目无法编译的问题。
- 修复未启用任何平台时 `elbot service run` 立即以 0 退出：在 Docker / systemd 下会被 `restart: unless-stopped` 反复拉起，健康接口也随进程消失；现在保持存活并输出 warning。
- 修复 `diskguard` / `ratelimit` 单元测试断言错误，`go test ./...` 恢复全绿。

### Changed

- 运行镜像默认保留更多 Agent 常用命令（bash、procps、iputils-ping、dnsutils、netcat-openbsd、iproute2、less、file、tree、tar、gzip、xz-utils、rsync、zip、python3），且默认只增不减。
- `XDG_CACHE_HOME` 指向 `/data/cache`：Go Skill 编译缓存与媒体临时文件落在数据卷上，重建容器后仍可复用。
- `go-runtime` 镜像内编译 Go Skill 不再因 `/go` 无写权限而失败（GOPATH / GOMODCACHE / GOCACHE 均指向 10001 可写目录）。
- 版本号统一由 `deploy/VERSION` 提供，镜像、离线包与文档不再多处硬编码。

### Added

- Docker 构建支持多架构：构建阶段固定在 `$BUILDPLATFORM` 原生交叉编译，打 arm64 不再整段走 QEMU。
- Docker 构建支持国内网络环境：新增 `GOPROXY`、`APT_MIRROR`（自动强制 https）构建参数，可用 `GO_BASE_IMAGE` / `RUNTIME_BASE_IMAGE` 覆盖基础镜像加速或 pin digest。
- 新增离线 / 预编译部署产物：`prepare-offline.sh` 生成 amd64/arm64 静态二进制、`docker load` 直接可用的 scratch 镜像与完整离线包，并附 `SHA256SUMS` 校验。


## [v0.5.0 - 2026-09-28]

### Added

- QQ 官方机器人已支持群聊
- 新增媒体中心，统一管理所有媒体文件

### Changed

- 优化给agent看的meta信息，现在显示平台、群号、群id、用户昵称、用户id
- 现在会压缩超过设置大小的图片；原始媒体和 Media ID 不变。S3 后端改为按需初始化，配置不可用时仅告警，不再阻止 ElBot 启动。
- 工具化 AgentSkill 的命令参数此前只支持字符串、数字和布尔值；现在 JSON 数组与对象会压缩为单个 argv 参数传给对应 `[args]` flag。
- 普通用户的追加重发与高风险工具确认此前会无限等待并长期占用当前 Turn；现在默认在 10 分钟无有效操作后停止，若对应 Session TTL 更短则以其为上限，追加内容或 `/detail` 会续期。超级管理员不受额外的 10 分钟限制，但仍遵守已启用的 Session TTL。
- 多模态图片此前只以 `image_url` 内容段发送，模型能看图却不知道可复用地址；现在每张图片前会派生带消息内序号、名称和 HTTP(S) URL 的用户文本标签，持久化 `content` 与视觉回退使用同一文本投影，`segments` 仍只保存原始结构且无需数据库迁移。
- `/stop`、请求超时或上游取消现在会终止一次性 exec Hook 的完整进程树，避免 Hook 派生的子进程残留；持久 Worker 则收到 `event.cancel` 并继续复用。
- CLI TUI 中不存在的 `#文件` 引用现在按普通文本原样发送，不再阻止消息提交；同一消息中存在的文件仍会正常展开。
- `/help`、详细帮助和 slash 命令补全现在按当前用户权限过滤；普通用户无法发现仅限超级管理员的命令。
- 修改Systemp prompt结构，添加meta信息
- System prompt 的会话 Meta 现在会附带精确到秒的 Session 当地创建时间；该值不会随对话轮次变化，以保持 Prompt 缓存稳定。
- `read_file` 和 `edit_file` 此前会完全拒绝超过 2 MiB 的文本文件；现在 2–100 MiB 文件可继续按行读取、grep 和编辑，仅在实际调用时根据文件大小禁用 AST、完整 diff 和过长确认内容，并保留 revision 校验、预检与原子写入。

### Fixed

- 修复 OpenAI-compatible 上游返回 HTTP 200 HTML/非 SSE 页面时被 Scanner 超长 token 错误掩盖的问题；现在会在流解析前识别异常响应，并只返回有限、脱敏的摘要。
- 修复 Session 闲置过期、执行 `/new` 或切换会话后，引用原 Session 最后一条 assistant 回复会误建新 Session 的问题；现在会自动恢复该 Session，引用较早回复仍会 Fork。
- 修复内置工具与 Go Skill 的服务级环境变量读取不一致，导致部分工具无法读取配置目录 `.env` 的问题。
- 修复 OpenAI-compatible 接口返回非 200 状态时，响应上下文在读取错误 body 前被提前取消，导致真实上游错误丢失并只显示 `failed to read body: context canceled` 的问题。
- 修复读文件工具错误将部分非 UTF-8 文本编码识别为二进制文件的问题。
- 修复elwisp发往qqonebot，文件太大导致全部阻塞的bug
- 修复查询聊天记录查不到纯图片的bug
- 修复日志可能保存base64的bug

## [v0.4.2 - 2026-08-06]

### Changed

- 优化.env、平台secret、token解析逻辑
- `read_file` 读取 `.env`、`.env.*` 及常见凭据文件名时改为高风险确认；其他文件读取仍保持低风险。
- Provider 请求从默认继承进程环境代理改为仅使用 `providers.toml` 中对应 Provider 的显式 `proxy`；未配置或留空时模型列表和聊天请求均直连。
- CLI TUI 通知支持携带日志等级并按 Debug 白、Info 绿、Warn 黄、Error 红显示；远程 CLI 的 `notice` 消息同步新增可选 `level` 字段。

### Fixed

- 修复群聊使用前缀唤起 LLM 后，后续输出 Hook 将已剥离前缀的消息误判为未唤起，导致 `agent.turn.output.prepared` 等默认规则不执行的问题。

## [v0.4.1 - 2026-07-28]

### Changed

- `/new` 与 Session 闲置过期改为仅清除当前会话指针，首条普通消息到来时才创建新 Session；过期历史会话不再立即删除，可直接通过 `/resume` 恢复。
- 更新 Shell 与 Hook 的环境变量继承和分级 `.env` 配置。
- `/hooks` 列表从逐条展示插件规则改为按插件名聚合；使用 `/hooks <插件名>` 时再展开 Worker 状态、全部规则和详情，根规则与内置 Hook 仍单独显示。

### Fixed

- 修复调用者没有当前 Session 时，`/status` 会显示进程内其他用户或 Session 活跃请求的问题；现在该状态只展示 `active requests: none`。
- 修复 QQ OneBot 发送大体积图片等消息时，JSON 编码或 WebSocket 写入长期占住共享发送锁，导致斜杠命令和其他会话的回复一并无响应；OneBot 与远程 CLI 的写入等待现在可取消并有明确超时，失败后 OneBot 会重连。
- 修复普通用户修改自己的高风险 core 常驻记忆时绕过确认；普通用户现在只能在工具权限校验通过后确认 `high`/`critical` 风险调用。

## [v0.4.0 - 2026-07-23]

### Changed

- 无法从模型元数据或配置识别 context window 时，默认窗口调整为 256k。
- `/resume <编号>` 改为直接按最近更新时间恢复非当前 Session，`1` 表示最近一项，不再要求先执行裸 `/resume` 建立编号。
- 上下文压缩改为保留历史用户原话、过滤工具结果，成功后切换到 `原标题 compacted-N` 独立 Session，并将压缩内容与新输入固定物化为首条用户消息；同时修复模型切换、`/stop` 与 Session 变更命令的并发问题。
- Hook Actor 现在同时提供平台昵称、群名片和纯展示名；聊天历史按平台用户 ID 与名称分开保存和搜索。
- Soul 和常驻记忆统一由内置 System Prompt 来源按 turn 构建；常驻记忆不再注册为 Hook，普通 Hook 的 `llm.messages` 明确为只读上下文。
- `workspace` 工具首次被发现或注入时也会加载当前目录的 `AGENTS.md`/`AGENT.md`；同一 Session 的同一路径与切换、重置入口共享一次性记录，不会重复注入。
- 优化 `read_file` 工具，支持目录搜索、搜索结果编号选择，以及按 AST 函数名精确返回完整函数内容及其起止行号。
- 优化system prompt
- AgentSkill 启动扫描不再常驻缓存 `SKILL.md` 正文，只保留摘要与路径；`discover_tool` 按名称发现或 `@skill` 预载时会读取当前正文，带 `ELBOT_SKILL.toml` 的工具化 Skill 同样如此。
- `web_extract` 工具的代理参数从 `disable_proxy` 改为 `proxy`：不填时使用 `WEB_EXTRACT_PROXY` 或系统代理环境，填 `disabled` 禁用代理，填 URL 使用指定代理。
- `send_file` 工具改为使用 `source` 参数发送文件，支持本地路径、`file://` URI 和 HTTP(S) URL，并会按 MIME/扩展名自动将图片作为图片消息发送。
- AgentSkill 不再通过 `python_skill_run` 固定包装执行 Python 脚本；没有 `ELBOT_SKILL.toml` 时保持说明型 Skill，可按文档使用 shell 等通用工具；说明型 AgentSkill 不读取 `SKILL.md` 风险，工具化后以 `ELBOT_SKILL.toml` 的 `risk` 为准。
- Skill 扫描改为启动后延迟执行，并在 `discover_tool` 首次使用时兜底确保扫描，减少启动阻塞。
- Session 闲置过期改为 `[session.idle_expiration]` 四项配置，分别控制群聊/私聊下普通用户和超级管理员的当前 Session 过期时间；默认群聊所有用户过期，私聊超级管理员不过期。
- `shell` 工具移除 `path` 参数，命令默认在当前 workspace 下执行；后台任务仍限制在各自 sandbox 内。
- `read_file`、`edit_file`、`send_file` 的相对路径改为基于当前 workspace 解析；绝对路径仍可临时使用并返回 warning。
- `llm_usage` 审计事件从 debug 级别改为 info 级别，默认 `log_level=info` 即可记录 token 消耗数据。
- QQ OneBot、QQ 官方、Telegram 平台断线重连改为指数退避（3s 起，翻倍，封顶 10s）并日志降级：连续失败只在首次记 warn，恢复后记 info，不再每轮刷屏。
- 平台媒体输出支持在 `path` 中识别 `base64://`、`file://`、`http://`、`https://` 源；普通本地路径仍按平台默认方式处理。
- qq 官方收到图片现在直接使用url而不是base64
- 重构hook，详情见docs。
- Windows 下 `shell` 工具优先使用 `pwsh`，其次 `bash`，最后回退到 `powershell.exe`。

### Fixed

- 修复当前会话仍在请求模型、执行工具或等待确认时仍可切换 Session 的问题；Session 切换命令现在会提示先使用 `/stop` 结束当前处理。
- 修复 Linux service 下进程 Hook 只能使用服务进程 PATH、无法获得配置 `.env`，导致终端可用的 `uv` 等命令无法启动的问题；一次性 exec 与 Worker 现在共用合并后的环境和 PATH 查找规则。
- 修复工具流程的最终 LLM 请求期间收到的 pending 消息会随当前 turn 结束而丢失的问题；当前回复现在正常结束，多条 pending 合并后自动开启下一轮请求。
- 修复 `llm.request.prepared` 可以临时改写本 turn 初始输入或历史消息、pending 图片在排队时丢失且 Hook 修改未持久化的问题；request Hook 现在只修改本次新 drain 的 pending。
- 修复工具 prepared Hook 改写后的参数没有同步到当前 LLM 上下文，以及进程内 Hook 可以改写工具 ID/名称的问题；实际执行、后续请求和 transcript 现在统一使用最终 arguments。
- 修复 Cron 补发使用旧 job 快照覆盖禁用、调度和任务内容，以及多个平台同时连接时重复生成、重复发送和投递状态相互覆盖的问题；LLM 返回 `completed=false` 的失败或阻塞报告现在也会冻结并完成补发。
- 修复已完成的一次性 LLM Cron 重新启用或重新调度后直接复用旧报告、未创建新后台 Session 的问题；通知失败和平台重连仍补发同一轮已持久化结果。
- 修复 AgentSkill 配置写入后 reload 失败会留下磁盘与运行 registry 不一致的问题；Skill reload 现改为串行、完整验证并原子替换，名称冲突或候选失败时保留旧 registry、catalog 和 AgentSkill 配置。
- 补齐 Elnis HTTP 请求头、请求读取、响应写入和空闲连接超时，并拒绝请求体中的尾随第二个 JSON 值；未知字段继续作为无语义字段忽略。
- 修复 Elnis LLM 报告发送前就把事件标记为 `completed` 的问题；报告改用可恢复 outbox，所有目标回执持久化后才完成，失败项会定时及在重启后重试。
- 补全默认 `.env.example` 中缺失的 `JINA_API_KEY`。
- 修复 Session 删除、归档确认可能因列表或当前 Session 变化而作用到错误目标的问题，并让存储错误正确返回给调用方。
- `read_file` 的 `start_line` 兼容 LLM 偶尔生成的整数字符串，避免有效行号因 JSON 类型偏差导致读取失败。
- CLI TUI 执行 `/stop` 后会把当前运行状态收束为 `done` 并固定耗时，不再继续累加状态栏时间。
- 修复发现或内联预载多个 ELyph Skill 时规则卡会重复注入上下文的问题；同一会话首次注入后只继续返回 Skill 内容，保留历史中的首次规则卡以利于缓存命中。
- 修复同一时间戳下会话消息可能按 UUID 错序加载，导致历史上下文顺序不稳定的问题。
- 修复 `workspace` 工具设置目录时不支持 `~`、`~/path`、Windows `~\path`、`$HOME` 和 `$HOME/path` 主目录路径的问题。
- QQ OneBot 私聊文件段缺少 `url` 时会调用 `get_file`；若返回下载地址则保存到 ElBot，若只返回 OneBot 本地路径则直接提示该路径。
- QQ OneBot 入站 @ 消息现在会优先显示群名片，其次普通昵称，格式为 `[at 名字 qq:<id>]`，无法获取时才回退 QQ 号。
- 修复 Windows 下 `shell` 工具回退到 PowerShell 时中文输出可能乱码的问题。
- 修复 Windows 下无 bash 时 shell 命令的 bash AST 解析失败导致风险分类、沙盒校验、目录切换拦截和警告分析全部异常的问题；PowerShell 环境下跳过 AST 解析，风险分类直接返回高风险需用户确认。
- OneBot 发送图片失败时，不再出现可见 fallback，但仍会记录日志。

### Added

- **重构hook系统**
- 工具完成 Hook 支持返回包含 URL、路径或 base64 图片的 `message.segments`；多模态工具结果可持久化并按 OpenAI Chat Completions 协议作为后续图片消息提供给模型。
- 用户输入和工具 pending 的前置 Hook 支持用 `message.segments` 同时改写文字和附加图片，最终多模态内容会在请求前写入会话历史。
- `/chat` 和 `/work` 支持直接携带消息，在切换 Session 模式后立即发送；Session 命令状态改为按平台 Scope 隔离。
- QQ OneBot 新增 `send_file_mode` 配置，本地图片和文件默认使用 base64 发送，也可在共享文件系统的部署中显式改用 `file_uri`。
- `/log` 新增 `-s` 和 `--system`，用于筛选并显示 `system prompt` 日志。
- CLI TUI 宽屏模式支持用鼠标拖动聊天区与通知区之间的分界线，运行期间可自由调整两侧宽度。
- `read_file` 新增 `mode=ast`，可对 Go 和 Shell 文件按名称进行轻量 AST 搜索；`mode` 同时统一为 `read`、`grep`、`ast` 三种读取模式。
- 重构AgentSkill：去掉py wrapper，直接使用shell执行对应sklll，同时支持在Agentkill根目录添加 `ELBOT_SKILL.toml` 注册为普通工具，方便 LLM 直接调用结构化参数。
- 新增隐藏元工具 `agent_skill`，用于读取或写入 AgentSkill 的 `ELBOT_SKILL.toml`，写入前校验配置并在成功后 reload。
- 首次运行会生成 `skills/agent/agent_skill_creator/SKILL.md`，用于说明如何把 AgentSkill 注册为普通工具。
- 首次运行会生成 `skills/agent/write_elbot_hook/SKILL.md`，用于提示按需求编写 ElBot 规则 Hook。
- AgentSkill 的 `ELBOT_SKILL.toml` 支持只写 `risk` / `superadmin_only` 做文档可见性限制，不写工具化字段时不会注册为普通工具；默认 `agent_skill_creator` 和 `write_elbot_hook` Skill 会生成仅超管可见、低风险的 TOML。
- 新增 `/usage` 命令：从审计日志聚合 token 消耗，支持按模型/天/会话汇总，快捷参数 `-d` 天数、`-m` 模型、`-s` 会话。
- 新增 `workspace` 工具：设置当前前台 Session 的共享工作目录，路径类工具会基于该目录解析相对路径。首次切换到含 `AGENTS.md` 或 `AGENT.md` 的目录时，会自动附带说明文件内容；文件超过 64 KiB 时会提示缩短。
- 新增 `[platform_files]` 配置，统一控制平台入站文件最大保存大小和下载超时。
- QQ OneBot 支持自动保存私聊超级管理员入站文件；纯文件消息只回复保存路径或过大提示，不唤起 LLM，群文件不自动保存。
- `/requests` 命令现在展示每个 turn 的当前运行阶段（preparing/llm/tool/sending）和阶段耗时，可区分 LLM 慢还是平台发送卡住。
- 执行中的 Hook 会显示在 `/requests`，当前 Session 的 Hook 也会显示在 `/status`；可用 `/stop` 取消长时间运行的 Hook，手动取消按正常取消记录。
- 内联预载支持工具简写 `@t:<name-or-tag>` 和 Skill 简写 `@s:<name>`，并兼容中文全角冒号 `：`。
- CLI TUI 输入框支持用 `#文件名` 模糊补全本地文件；发送时会把引用替换为文件名和文件内容，含空格路径可写作 `#"a b.txt"`。
- `web_extract` 新增 `jina` 参数，默认使用 Jina Reader；传 `jina=false` 可手动改用直接爬取。
- `web_extract` 新增 `force_refresh` 参数，可按需跳过缓存、重新获取网页并更新缓存内容。




## [v0.3.0-alpha - 2026-07-01]

### Changed

- 同一轮多个工具调用的 `[tool]` 预览会合并为一条消息发送，减少平台刷屏。
- Elvena LLM 事件和 LLM Cron 支持 `session_mode=chat|work` 选择后台 Session 模式，默认仍为 `work`。
- `/detail` 高风险工具调用详情支持工具自定义纯文本展示；未自定义时仍会把 JSON 参数格式化成更易读的多行展示，字符串里的 `\n` 会显示为真实换行。
- `edit_file` 的高风险确认详情现在会按文件、模式和编辑步骤展示替换、新增、删除、匹配等操作。
- `edit_file` 不再向 LLM 暴露 `dry_run` 参数；系统会在用户确认前自动预检并生成 diff，预检失败不会进入确认或写入文件。
- `modify_el_skill` 现在复用 `edit_file` 的 `edits` 编辑说明与执行能力，并在确认前预检编辑、ELyph 语法和 no-op 修改，在高风险确认详情中展示预检 diff。
- 更新 `ELyph` 版本至 v3
- qq heartbeat ack 和 qqofficial gateway resumed 不再记录log
- read_el_skill 现在依赖modify_el_skill，方便执行可能的修改
- 现在不在启动elbot的时候校验ELyph语法，免得拖慢启动速度
- ELyph `**`/`~` 文本末尾冒号现在作为 warning 返回给 `create_el_skill`/`finalize_el_skill`，不再阻断创建或 finalize。
- `modify_el_skill` 修改 `SKILL.elyph` 后不再自动 reload；修改完成后需调用 `finalize_el_skill` 生效。
- 工具结果支持统一 `Warnings` 输出，用于提示 LLM 后续优先使用更合适的工具。
- `read_file`/`shell` 读取 EL Skill 文件时会提示使用 `read_el_skill`；`edit_file` 或 shell 直接修改 EL Skill 文件会在确认或执行前被拒绝，需改用 `modify_el_skill`。
- 常驻记忆和长期记忆源文件纳入通用 FileGuard 保护；读取会提示使用记忆工具，通用文件工具或 shell 直接写入会被拒绝。
- hook log日志不再重复记录

### Fixed

- `long_memory_write` 的 `update` 支持填字段更新 meta，并新增 `content_edits` 复用 `edit_file` 的编辑操作修改正文；确认前会自动预检并展示 diff。
- 修复 `edit_file` 使用 `create=true` 创建新文件时，目标父目录不存在会在确认写入阶段失败的问题。
- `response_timeout_seconds` 现在控制整轮用户请求总时长，默认 `0` 表示不限时；单次 LLM 流式请求只由首包和 idle 超时控制。

## [v0.2.0-alpha - 2026-06-27]

### Added

- Elvena v3 动作通道：Elnis 支持 `calls`，首批支持 raw 平台 API 以及 `message.recall`、`member.mute`、`chat.leave` capability，未支持的可以直接调用消息平台api；Hook rules 可通过 `exec` action 执行脚本，并用 `stdout=elvena` 经内部 Elvena Bus 触发 Elnis direct/LLM/calls；direct calls-only 请求不会额外发送消息。
- `edit_file` 的 `*_match` 操作新增 `match_mode` 与 `index` 参数：`match_mode=line` 时按单行前缀匹配整行（容忍行首缩进，规避换行符匹配出错），`content`（默认）保持精确子串语义；多处匹配时可通过 `index` 选择第几处，未传 `index` 报错并列出所有匹配位置。
- Hook rules 新增角色分区与平铺控制字段：`roles`、`actor_roles`、`group_roles`、`consume`、`stop_propagation`；平台消息 Hook 输出现在会发送，`consume=true` 可阻止后续命令/LLM 处理。
- Hook rules `send` action 新增 `segments` 列表，支持多类型多段输出（text/image/file/emoticon，含 url/path/base64），格式与 Elvena segment 统一。
- Hook rules `exec` action 新增 `outputs` stdout 模式，脚本 stdout 解析为 JSON 并提取 `outputs` 数组和可选 `text`；设 `field` 时 `text` 覆写对应字段，不设时不修改原文。
- 平台入站上下文新增统一群身份 `owner/admin/member/unknown`，QQ OneBot 和 Telegram 会映射群主/管理员/普通成员。
- Hook 平台上下文现在填充当前平台消息 ID `platform.message_id` 与引用/回复目标消息 ID `platform.reply_to_message_id`，便于规则 Hook 处理引用消息，例如撤回被引用消息。
- `/hooks` 命令：列出所有已注册 Hook、查看某个 Hook 详细配置、热重载全部 Hook（修改 `hooks.toml` 后无需重启即可生效）。

### Changed

- LLM 请求超时配置改为 `first_chunk_timeout_seconds`、`stream_idle_timeout_seconds`、`response_timeout_seconds`，旧 `timeout_seconds` 已移除；默认首个流式事件等待 180 秒、流式 idle 60 秒、整次响应不限总时长。
- Provider 配置重构：删除未使用的 `[global_default]`，删除 `[model_metadata.context_windows]` 全局模型窗口表；模型级 `context_window` 和 `extra_payload` 统一收到 `[providers.<name>.model_configs."<model>"]` 下，按 `provider/model` 查找，避免跨 provider 同名模型冲突。
- Provider 新增 `proxy` 字段，支持 HTTP/SOCKS5 代理。
- 表情 Hook 从内嵌插件改为规则 Hook 示例，不再内置 emoticon 插件和 `emoticon.toml` 资产。
- LLM 建连/HTTP 可重试失败时通过 Notice 显示当前重试次数。
- `finalize_el_skill` 工具风险等级由 high 降为 medium。

### Fixed

- 修复长工具链会被 Agent 内部 5 分钟默认请求超时静默停止的问题；整轮超时时现在会提示用户。
- 修复 QQ OneBot 发图片/表情/文件时 API 超时可能取消 WebSocket 写入并触发断线重连的问题；媒体发送失败时会尝试发送同目标文字提示。
- 修复 OpenAI-compatible 流式响应中途断开但缺失 `[DONE]` 时被当作正常结束的问题；现在会明确通知 LLM 响应中断。
- 修复 OpenAI-compatible 流式请求使用单一 HTTP 超时导致模型首字慢或长输出超过 60 秒时被错误中断的问题。


## [v0.1.0-alpha] - 2026-06-24

ElBot 的首个预发布版本。轻量 Agent/Chatbot 框架，目标是个人助手、平台机器人与可编排的自动化助手。

### Added

- **轻量内核**：Go 实现，本地启动 <10ms，常驻内存约 30MB。
- **Chat/Work 双模式**：chat 模式关闭工具，适合日常聊天与低成本对话；work 模式启用工具发现与调用，两模式可独立配置模型。
- **工具发现机制**：默认只暴露 `discover_tool` 与工具名，按需注入完整 schema，减少无效上下文开销。
- **Session 服务**：持久化会话，支持恢复、归档、置顶、Fork、删除、分页与平台隔离；长对话自动上下文压缩。
- **Hook Layer**：在 Agent 输入、LLM 请求/响应、平台发送等关键点插入扩展逻辑；内置规则 Hook 与常驻记忆 Hook。
- **标准 Cron 与 LLM Cron**：标准 Cron 按计划直发固定内容；LLM Cron 用 ELyph 任务描述驱动模型执行，支持一次性与周期任务、missed 补跑与广播。
- **ELyph Task Notation**：结构化任务描述语言，用于 LLM Cron 与原生 Skill，减少自然语言歧义。
- **原生与外置 Skill**：`create_el_skill` 元工具支持 LLM 创建原生 EL Skill（纯 ELyph 或附带 Go 源码并编译）；兼容 agentskills.io 风格外置 AgentSkill（附带 Python 脚本）。
- **Elnis 监听枢纽**：接收 Elwisp 通过 Elvena HTTP 协议投递的外部事件，支持 record/direct/llm 三种模式与多目标投递。
- **多平台适配**：CLI（含 client/server 分离与远程连接）、QQ OneBot v11、QQ 官方机器人、Telegram Bot API。
- **安全策略**：工具风险分级、角色权限校验、高风险确认流程与后台 shell 轻量沙盒。
- **记忆系统**：常驻记忆（core/normal 分层）按平台与 actor 注入；长期记忆基于 Markdown 源数据与 SQLite FTS 检索。
- **日志与审计**：运行日志、审计日志与 Elnis 日志分离，支持结构化字段与按日期轮转。
- **SQLite 持久化**：Session、消息、上下文摘要、工具调用记录、Cron job、Elnis 事件统一存储。

### Known Limitations

- MCP 工具、子 Agent 与完整多模态（语音、视频、文件真实模型输入）尚未实现。
- 接口、配置与内部实现仍可能调整，更适合作为个人 Agent/bot 框架探索使用。
