# 配置说明

ElBot 使用一个主配置入口加载应用配置、Provider 配置和运行态状态。默认配置由程序内置 assets 生成到平台配置目录；已有配置文件不会被覆盖。

## 配置文件职责

所有配置文件均由程序内置 assets 首次运行时自动生成到平台配置目录，已有文件不会被覆盖；源码中不再保留 `config/` 目录。

| 文件或目录 | 职责 |
| --- | --- |
| `elnis.toml` | Elnis 监听枢纽配置，保存 HTTP、token、delivery、allowed_tools 和 Elwisp 策略。 |
| `state.toml` | 运行态状态，例如默认 Session 模式、chat/work/compact/naming 模型选择。 |
| `tool_tags.toml` | 给工具添加 tag 和 prompt 的配置文件。 |
| `SOUL.md` | Agent 的 System Prompt 来源文件。 |
| `.env` | 可选，本地密钥文件，不建议提交；首次自动生成的是 `.env.example`，不会直接生成 `.env`。 |
| `plugins/.env` | 自动生成的 Hook 公共环境文件；默认只有注释，可补充公共变量和额外 `PATH`。 |
| `plugins/` | Hook 和插件配置目录。 |
| `skills/` | 用户侧 Skill 目录，默认位于配置目录下；当前子目录为 `skills/agent/` 和 `skills/go/`。AgentSkill 可在根目录放置 `ELBOT_SKILL.toml` 配置可见性或注册为普通工具。 |
| `memories.toml` | 常驻记忆文件，默认位于配置目录下。 |
| `long_memory/` | 长期记忆 Markdown 源数据目录，默认位于配置目录下。 |

## 主配置查找顺序

启动时主配置按以下顺序查找：

1. 命令行 `--config`。
2. 环境变量 `ELBOT_CONFIG_FILE`。
3. 平台配置目录：Windows `%APPDATA%/ElBot/app.toml`；Linux 使用 XDG 配置目录。
4. 若平台配置不存在，则自动在平台配置目录生成默认配置文件。已有配置不会被覆盖。自动生成只在没有显式 `--config` 和 `ELBOT_CONFIG_FILE` 时触发。若显式指定的配置路径不存在，ElBot 会报错而不是偷偷生成，避免掩盖路径拼写错误。

开发期直接运行即可使用平台配置目录：

```bash
go run ./cmd/elbot
```

如需使用临时配置文件，也可以显式指定 `--config`。

## 相对路径规则

相对路径默认基于主配置文件所在目录解析。

例如平台配置目录下的 `app.toml` 写入：

```toml
[config_files]
providers = "providers.toml"
state = "state.toml"
elnis = "elnis.toml"

[soul]
path = "SOUL.md"
```

这些路径都会解析到主配置文件所在目录下；默认情况下就是平台配置目录。

## 环境变量与进程环境继承

ElBot 使用以下环境来源：

- ElBot 进程环境：由终端、服务管理器或容器在启动时提供。
- 配置根 `.env`：与主配置文件同目录，用于 Provider Key、平台 Secret、CLI/Elnis token、内置工具变量和 LLM Shell。
- `plugins/.env`：所有 Hook 共用的环境层。
- `plugins/<plugin-id>/.env`：单个插件 exec 和 Worker 的环境层。

### 配置变量与密钥

`api_key_env`、`token_env`、`client_secret_env`、`access_token_env`、`bot_token_env`、`proxy_url_env` 等字段保存的是环境变量名。读取对应值时，优先级统一为：

1. ElBot 进程环境。
2. 配置根 `.env`。

Docker Compose 的 `env_file`、`docker run -e`、systemd `EnvironmentFile` 注入的变量都属于 ElBot 进程环境，因此也可能被 Shell / Hook 子进程继承；如果向群聊用户开放工具，不能只依赖“容器不是 root”来保护其中的 API Key。

推荐把真实密钥放在系统环境或配置根 `.env`，不要直接写入 TOML 或提交到仓库。例如：

```dotenv
DEEPSEEK_API_KEY=your-api-key
OPENAI_API_KEY=your-api-key
QQOFFICIAL_CLIENT_SECRET=your-client-secret
QQONEBOT_ACCESS_TOKEN=your-access-token
TELEGRAM_BOT_TOKEN=your-bot-token
ELBOT_CLI_LOCAL_TOKEN=your-cli-token
ELNIS_HOME_TOKEN=your-elnis-token
```

配置根 `.env` 中的全部变量还会补充给 LLM Shell，因此只有在信任当前模型及 Shell 权限策略时才应启用相关工具。

### Shell 与 Hook 环境

| 进程入口 | 环境来源与优先级 |
| --- | --- |
| 内置工具与 Go Skill（含 LLM Shell） | ElBot 进程环境为基础，配置根 `.env` 只补充尚不存在的普通变量。 |
| 根 Hook 规则 | ElBot 进程环境为基础，`plugins/.env` 覆盖同名普通变量。 |
| 插件 exec 与 Worker | 根 Hook 环境为基础，插件配置文件同目录的 `.env` 再覆盖同名普通变量。 |

ElBot 自行读取配置文件时，配置根 `.env` 不会注入 Hook。所有 Hook 共用的代理、工具路径或变量应放在 `plugins/.env`，仅供单个插件使用的变量应放在该插件的 `.env`。一次性 exec、Persistent Worker 和 Transient Worker 使用相同规则。

需要代理时，可在对应环境层设置标准变量；子进程是否使用仍取决于具体程序：

```dotenv
HTTP_PROXY=http://127.0.0.1:7890
HTTPS_PROXY=http://127.0.0.1:7890
NO_PROXY=localhost,127.0.0.1,::1
```

### PATH 规则与 systemd

ElBot 自行合并上述环境层时，`PATH` 不按普通变量覆盖，而是按照层级依次追加并去重。这里的基础 PATH 是 ElBot 启动时已经获得的进程 PATH；后续 `.env` 只需填写要增加的绝对目录：

```dotenv
PATH=/home/elbot/.local/bin:/usr/local/go/bin
```

不要填写 `$PATH`、`%PATH%` 或 `~`，dotenv 不执行变量替换或路径展开。没有额外目录时可以省略 `PATH`。

systemd 用户服务没有显式设置 PATH 时，ElBot 使用服务管理器提供的 PATH。`EnvironmentFile` 本身不会影响 PATH，但文件中一旦包含 `PATH=`，systemd 会在启动 ElBot 前完整覆盖原值；ElBot 只能在这个结果上继续追加，无法恢复已经丢失的系统目录。systemd 的 `EnvironmentFile` 同样不会展开 `$PATH`。

由 ElBot 自行读取配置根 `.env` 时，systemd unit 不需要重复加载该文件；其中的 PATH 按上面的追加规则处理。若 unit 使用 `EnvironmentFile` 加载同一文件，则其中的 PATH 必须写成完整值

通过 `EnvironmentFile` 加载后，该文件中的全部变量都已经属于 ElBot 进程环境，因而也会被 Shell 和 Hook 继承，不再具有“配置根 `.env` 不注入 Hook”的隔离效果。

### 生效时机

配置根 `.env` 或 systemd 环境在重启 ElBot 后生效。`plugins/.env` 和插件 `.env` 在启动及 `/*hooks reload` 时重新读取，reload 会按新环境重建 Worker；文件不存在时视为空配置。

## 运维健康接口

设置 `ELBOT_HEALTH_ADDR`（进程环境或配置根 `.env`）后，ElBot 会启动一个**不依赖 Elnis** 的独立 HTTP 健康接口：

```dotenv
ELBOT_HEALTH_ADDR=127.0.0.1:32171
ELBOT_HEALTH_LIVE_STALE_SECONDS=90
```

`ELBOT_HEALTH_LIVE_STALE_SECONDS` 建议明显大于调度心跳间隔；当前实现约每 10 秒更新一次心跳。

- `GET /live`：关键调度循环是否仍在推进；watchdog 应只据此判断是否需要重启。
- `GET /ready`：SQLite / 数据目录是否可写、必要组件是否初始化、已配置平台是否已成功连接过。
- `GET /healthz`：汇总状态；模型 API 故障显示为 `degraded`，HTTP 仍可能返回 200，不应自动重启本机服务。

该接口只应监听容器内部或宿主机回环地址；不要在 Nginx 中暴露到公网。

## Workspace 工具

在 work 模式中，超级管理员可以让 LLM 调用 `workspace` 工具切换当前 Session 的共享工作目录。切换后，`read_file`、`edit_file`、`send_file` 和前台 `shell` 等路径类工具会基于该目录解析相对路径，避免每次都传完整路径。

某个目录的 `workspace` 工具首次被发现或注入，或者首次切换、重置到该目录时，如果目录根部存在 `AGENTS.md` 或 `AGENT.md`，系统会自动把文件内容附带给 LLM，供其读取当前目录的工作约定。文件名主体必须是大写 `AGENTS` 或 `AGENT`，`.md` 后缀大小写不限；`AGENTS.md` 优先于 `AGENT.md`。

自动附带的说明文件最大为 64 KiB。超过限制时不会读取内容，也不会标记为已附带，工具结果会提示需要缩短或拆分该文件后再切换 workspace。

## 常驻记忆配置

常驻记忆数据默认保存在配置目录的 `memories.toml`，文件由程序首次运行时生成。主配置中可以设置 core/normal 两段常驻记忆的长度上限：

```toml
[resident_memory]
core_max_units = 200
normal_max_units = 300
```

长度单位 `units` 可以近似理解为“中文按字数、英文按单词”：中日韩字符按单字计数，英文/数字连续片段按一个词计数。core 是高风险核心记忆，普通用户修改自己的 core 时也必须由本人确认。normal 是可直接整理的低风险普通记忆，不需要确认；注入 Prompt 时两段会合并成一段自然文本。

## Provider 配置

Provider 写在 `providers.toml`：

```toml
[providers.deepseek]
base_url = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"
proxy = ""                          # 可选，HTTP/SOCKS5 代理地址
extra_payload = { provider_field = "xxx" }  # 可选，Provider 级 extra payload

[providers.openai]
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
models = ["gpt-4o-mini"]             # 手动补充模型列表（API 获取不到时使用）

# 可选：为特定模型配置 context_window 或 extra_payload
# [providers.openai.model_configs."gpt-4o-mini"]
# context_window = 128000
# extra_payload = { }

[model_metadata]
default_context_window = 256000
```

说明：

- `base_url` 使用 Provider 的 OpenAI-compatible API 地址。
- `api_key_env` 指向环境变量名，推荐用这种方式保存密钥。
- `proxy` 可选，支持 `http://` 和 `socks5://` 代理地址；省略或留空时直连，不继承 ElBot 进程的 `HTTP_PROXY`、`HTTPS_PROXY` 等环境代理。该设置同时作用于模型列表和聊天请求。
- `models` 是手动补充的模型名列表，当 Provider 的模型列表接口获取不到某些模型时使用。
- `[providers.<name>.model_configs."<model>"]` 为特定模型配置 `context_window` 和 `extra_payload`，两者都是可选的。
- `extra_payload` 会合并到 LLM 请求 JSON 中，模型级覆盖 Provider 级。
- `[model_metadata]` 的 `default_context_window` 是全局回退值，默认 `256000`，没有在 `model_configs` 里配 `context_window` 时使用。

## 内置 Web 工具配置

`web_search` 使用 `tavily`：

```env
TAVILY_API_KEY=your_tavily_api_key
```

`web_extract` 默认优先使用 Jina Reader。配置 `JINA_API_KEY` 后会通过 Jina Reader 提取网页；未配置时回退为直接抓取：

```env
JINA_API_KEY=your_jina_api_key
```

`web_extract` 的 `proxy` 参数用于控制网页提取请求的代理：
如果希望所有默认 `web_extract` 调用都走固定代理，可以设置 `WEB_EXTRACT_PROXY`：

```env
WEB_EXTRACT_PROXY=http://127.0.0.1:7890
```

## LLM 请求与整轮超时

`app.toml` 的 `[llm_request]` 控制 OpenAI-compatible 流式请求、整轮处理和重试：

```toml
[llm_request]
first_chunk_timeout_seconds = 180
stream_idle_timeout_seconds = 60
response_timeout_seconds = 0
max_retries = 3
retry_initial_delay_seconds = 2
```

- `first_chunk_timeout_seconds`：从响应开始到首个流式事件的等待上限，默认 180 秒，适合首字较慢的模型。
- `stream_idle_timeout_seconds`：流式过程中两次事件之间的静默上限，默认 60 秒；每收到一个新事件都会重新计时。
- `response_timeout_seconds`：整轮用户请求总时长上限，从收到用户输入到最终回复结束，默认 0 表示不限时；设置为正数时，到时会停止本轮处理并提示用户。单次 LLM 流式请求不受这个字段限制。
- `max_retries` 和 `retry_initial_delay_seconds` 用于建连或 HTTP 可重试失败，重试延迟按指数退避增长。

旧版 `timeout_seconds` 已移除；已有配置需要改为上述三个新字段。


## CLI 远程配置

`[platform.cli]` 同时保存 CLI 服务端和客户端配置。`server` 是当前 ElBot 作为服务端运行时读取的配置，`clients` 是当前命令作为 CLI 客户端连接服务端时读取的配置。

```toml
[platform.cli]
enabled = true
default_client = "local"
default_url = "ws://127.0.0.1:32172/cli/v1/ws"

[platform.cli.server]
enabled = false
listen = "127.0.0.1:32172"

[platform.cli.server.tokens]
local = ["ELBOT_CLI_LOCAL_TOKEN"]
windows = ["ELBOT_CLI_WINDOWS_TOKEN"]

[platform.cli.clients.local]
token_env = ["ELBOT_CLI_LOCAL_TOKEN"]

[platform.cli.clients.windows]
url = "ws://192.168.1.10:32172/cli/v1/ws"
token_env = ["ELBOT_CLI_WINDOWS_TOKEN"]
```

- `server.enabled=true` 时，`elbot service run` 会启动 CLI WebSocket 服务端。
- `server.listen` 是服务端监听地址。容器部署远程 CLI 时必须写 `0.0.0.0:32172`，只写 `127.0.0.1:32172` 会只监听容器自身回环，宿主机端口映射也无法访问；对外仍由宿主机只绑定 `127.0.0.1`，再走 HTTPS/WSS 反向代理。
- `default_url` 是客户端默认连接地址；连接其他机器时在 `clients.<name>.url` 写远程 WebSocket 地址。
- `server.tokens` 是服务端允许登录的 CLI client id 与 token 环境变量列表。
- `clients.<name>` 是客户端 profile；`id` 可省略，默认等于 `<name>`；`url` 可省略，默认使用 `default_url`。
- `elbot cli -c <name>` 使用指定客户端 profile；未指定时使用 `default_client`。

## AgentSkill 工具化配置

AgentSkill 默认只作为说明文档使用；若按文档执行脚本，风险由实际调用的 `shell` 等工具承担。若要限制文档型 Skill 的可见性，或把 `skills/agent/<skill>/` 注册成普通工具，在该 Skill 根目录添加 `ELBOT_SKILL.toml`。未写 `command`/`parameters`/`[args]` 时只作为可见性配置，不会注册成普通工具；写了任一工具化字段后必须补齐完整工具化配置。默认生成的 `agent_skill_creator` Skill 可用于查看说明并辅助创建该文件：

```toml
risk = "high"
superadmin_only = true
```

```toml
risk = "medium"
superadmin_only = false
tags = ["doc"]
command = ["python", "foo.py"]
timeout_seconds = 30
expose_root = false

parameters = '''
{
  "type": "object",
  "required": ["input"],
  "properties": {
    "input": {"type": "string", "description": "输入文本"}
  }
}
'''

[args]
input = "--input"
```

字段说明：

- `risk`：可选，允许 `safe`、`low`、`medium`、`high`、`critical`；注册成普通工具时必填。文档型 Skill 未写时按 `safe` 处理。
- `superadmin_only`：可选，`true` 表示只有 ElBot 超级管理员能发现、预载或调用该 Skill。
- `tags`：可选，相当于为该工具分类，可用于 `@tool:<tag>` 预载。
- `command`：工具化时必填，命令数组，不使用 shell 字符串。
- `parameters`：工具化时必填，JSON object schema，决定 LLM 看到的工具参数。
- `[args]`：工具化时必填，扁平参数映射；`input = "--input"` 会把工具参数 `input` 翻译成 `--input <value>`。
- `timeout_seconds`：可选，命令超时时间。
- `expose_root`：可选，默认 `false`；为 `true` 时，发现该 Skill 时会暴露 Skill 根路径。

ElBot 只读取 Skill 根目录下的 `ELBOT_SKILL.toml`，不递归扫描。执行时工作目录固定为该 Skill 根目录，stdout 会作为工具结果；若 stdout 是 `{"content":"..."}` JSON，会取 `content` 字段。

媒体参数可在 `parameters.properties` 中声明为 `{"type":"media"}`，仍通过 `[args]` 映射 flag。LLM 看到字符串 schema 并传入完整 `media:<sha256>`，宿主仅在执行时将该参数导出为相对 Skill 根目录的临时文件路径。未声明为媒体的字符串不转换。stdout 还支持 `segments` 返回媒体 ID 或受控相对文件路径，具体格式与生命周期见 [Skill 使用媒体](concepts.md#skill-使用媒体)。

通过 `agent_skill` 元工具写入配置时，ElBot 会在完整 reload 成功后才确认写入；若 reload 失败，会恢复原有 TOML，原先没有该文件时则删除本次新建文件。

## Go Skill 编译器路径

修改 Go skill 的 `code_source` 后，ElBot 会自动执行 `gofmt`、`go build` 并 reload。Go 可执行文件按以下规则定位。

推荐在配置目录 `.env` 中指定 Go 可执行文件：

```dotenv
ELBOT_GO_BINARY=/usr/local/go/bin/go
```

查找顺序：

1. 系统环境变量 `ELBOT_GO_BINARY`。
2. 配置目录 `.env` 中的 `ELBOT_GO_BINARY`。
3. `GOROOT/bin/go`，如果 service 环境配置了 `GOROOT`。
4. ElBot 进程 `PATH` 中的 `go`。

如果使用 asdf、mise、Nix、Linuxbrew、Snap 或自定义安装路径，推荐直接把实际 `go` 路径写入 `ELBOT_GO_BINARY`，不要依赖交互 shell 的初始化脚本。

高级部署也可以在 systemd service 中指定：

```ini
[Service]
Environment=ELBOT_GO_BINARY=/usr/local/go/bin/go
```

## Session 配置

`app.toml` 中的 `[session.idle_expiration]` 控制当前 Session 的闲置过期时间，单位为分钟：

```toml
[session.idle_expiration]
group_user_ttl_minutes = 10
group_superadmin_ttl_minutes = 10
private_user_ttl_minutes = 10
private_superadmin_ttl_minutes = 0
```

字段说明：

- `group_user_ttl_minutes`：群聊中普通用户当前 Session 的闲置过期时间。
- `group_superadmin_ttl_minutes`：群聊中超级管理员当前 Session 的闲置过期时间。
- `private_user_ttl_minutes`：私聊中普通用户当前 Session 的闲置过期时间。
- `private_superadmin_ttl_minutes`：私聊中超级管理员当前 Session 的闲置过期时间。
- 任一字段设为 `0` 表示禁用对应场景的闲置过期。

默认配置下，群聊中的普通用户和超级管理员都会在闲置 10 分钟后开启新 Session；私聊中普通用户闲置 10 分钟后开启新 Session；私聊中超级管理员不过期。

这里的“超级管理员”指 `[security.superadmins]` 中配置的 ElBot 超级管理员。平台群主或群管理员如果不在超级管理员列表中，仍按普通用户规则处理。

## 模型状态配置

`state.toml` 保存运行态模型选择：


```toml
[session]
default_mode = "work"

[mode_models.work]
provider = "deepseek"
model = "deepseek-chat"

[mode_models.chat]
provider = "deepseek"
model = "deepseek-chat"

[mode_models.elwisp1]
provider = "deepseek"
model = "deepseek-chat"

[mode_models.elwisp2]
provider = "deepseek"
model = "deepseek-chat"

[mode_models.elwisp3]
provider = "deepseek"
model = "deepseek-chat"
```

- `default_mode` 决定新 Session 默认进入 `chat` 还是 `work`。
- `work` 模式启用工具发现和工具调用。
- `chat` 模式不注入工具，适合闲聊和低成本对话。
- `elwisp1`、`elwisp2`、`elwisp3` 是 Elnis LLM 事件可选模型槽位；Elvena 请求可通过 `model_slot` 指定，未配置时回退到 `work`。
- 运行时使用 `/*model` 切换模型后，状态会写回 `state.toml`。

## 存储与运行数据

`app.toml` 中 storage 相关配置：

```toml
[storage]
sessions_sqlite_path = ""
chat_history_sqlite_path = ""
# 磁盘保护：比例为已用空间比例；critical 时拒绝非必要媒体写入。
disk_warn_ratio = 0.85
disk_critical_ratio = 0.95
disk_min_free_bytes = 0
```

留空时使用平台默认数据目录：

- Windows：`%APPDATA%/ElBot/data`
- Linux：`$XDG_DATA_HOME/elbot` 或 `~/.local/share/elbot`
- `disk_warn_ratio` / `disk_critical_ratio` 按已用空间比例分级；`disk_min_free_bytes` 大于 0 时，剩余空间低于该值直接进入 critical。critical 时生图、图片、语音、视频和普通文件保存会被拒绝，SQLite 会话和配置写入保留。

运行日志、SQLite、sandbox 等运行数据也会按配置或默认数据目录存放。

平台入站附件下载限制使用 `[platform_files]`：

```toml
[platform_files]
max_receive_file_bytes = 104857600
download_timeout_secs = 60
```

- `max_receive_file_bytes`：媒体接收大小上限，默认 100MB；超过上限或无法取得时提示媒体不可用。
- `download_timeout_secs`：媒体下载超时，默认 60 秒；Telegram 使用平台 API 超时。
- 平台媒体在实际处理时才下载，不会收到就自动保存到 sandbox。纯文件消息也按平台统一唤醒规则处理。


### 媒体交付

`app.toml` 的 `[file_delivery]` 控制模型请求中的媒体交付：`base64` 使用内嵌数据，`s3` 使用预签名下载链接，`hybrid` 在请求媒体总大小超过 `max_direct_base64_bytes` 时使用 S3。切换到 S3 后，已有本地媒体会在需要远程交付时上传，并复用已记录的对象键。

S3 交付使用 `s3_endpoint`、`s3_region`、`s3_bucket` 以及 `s3_access_key_env`、`s3_secret_key_env` 指定的变量。模型服务通过有效期为 1 小时的预签名 URL 下载对象，不需要另外提供密钥。Cloudflare R2 可保持 bucket 私有，无需开启公共访问；实际下载文件的模型服务或中转服务必须能访问该链接。

`[media]` 控制图片入库压缩：`llm_image_compression_threshold_bytes` 是触发压缩的原图字节数阈值（默认 4 MiB），`llm_image_max_length` 是边长限制（默认 4096）。原图字节数超过阈值或任一边长达到限制时，等比例压缩图片。


## 日志与维护任务

运行日志配置：

```toml
[runtime]
log_level = "info"
log_retention_days = 30
```

维护任务示例：

```toml
[maintenance.log_cleanup]
enabled = true
schedule = "0 3 * * *"
```

Cron 表达式由内部 Cron Runtime 调度，使用 Linux crontab 风格的 5 字段格式：`分钟 小时 日 月 星期`。默认维护任务包含日志、Session、sandbox 和聊天历史清理，例如 Session 清理默认保留 30 天，sandbox 默认每天 04:00 清理 7 天前内容，聊天历史默认每天 04:35 执行：

```toml
[maintenance.session_cleanup]
enabled = false
schedule = "15 3 * * *"
retention_days = 30
```

```toml
[maintenance.sandbox_cleanup]
enabled = true
schedule = "0 4 * * *"
retention_days = 7
```

```toml
[maintenance.chat_history_cleanup]
enabled = true
schedule = "35 4 * * *"
retention_days = 180
```

## Context 与压缩

```toml
[context]
compact_enabled = true
compact_trigger_ratio = 0.8
```

- 开启后，Session 上下文接近窗口上限时会触发压缩。
- 也可以通过 `/*compact` 手动压缩当前 Session。
- 压缩成功后会切换到独立的新 Session，不修改原 Session 的历史。

模型窗口在 `providers.toml` 的 `model_configs` 中配置：

```toml
[providers.deepseek.model_configs."deepseek-chat"]
context_window = 64000

[model_metadata]
default_context_window = 256000
```

- `[model_metadata].default_context_window` 是全局回退值，默认 `256000`，模型块里没配 `context_window` 时使用。

## 命令前缀

```toml
[commands]
prefixes = ["/*"]
```

默认使用 `/`。如果要支持其他命令前缀，可以在这里添加。

## 工具与安全

```toml
[config_files]
tool_tags = "tool_tags.toml"

[tools]
max_rounds_per_turn = 10

[security]
user_max_tool_risk = "low"
superadmin_confirm_risk = "high"

[security.superadmins]
cli = ["local"]
```

- 普通用户只能发现和调用允许风险范围内的工具；工具通过权限校验后，`high`/`critical` 风险调用还需要当前用户确认。
- `OwnerScoped` 工具只能访问调用者自己的数据，因此普通用户可以调用；其中 `high`/`critical` 风险操作仍需本人确认。
- 超级管理员的确认阈值由 `superadmin_confirm_risk` 配置。
- CLI 默认本地用户 `local` 是超级管理员。
- `tool_tags.toml` 用来配置 `@tool:<tag>` 可注入的工具组，以及 tag 激活后追加到 system prompt 的工具使用策略。
- 如果向普通用户开放生图，建议保持 `[image_generation] superadmin_only = true`，或在中转站按 Key 设置额度、限速和每日上限；详见[生图服务](image-generation.md#权限与费用)。

### `tool_tags.toml`

`tool_tags.toml` 是独立配置文件，路径由 `[config_files].tool_tags` 指定。相对路径以 `app.toml` 所在目录为基准：

```toml
[config_files]
tool_tags = "tool_tags.toml"
```

文件格式以 tag 为入口：

```toml
[tags.agent]
tools = ["read_file", "edit_file", "shell"]
prompt = """
ROLE:
- The goal is to complete the user's task safely and accurately.

MUST:
- Inspect relevant files before editing.
- Prefer minimal, verifiable changes.
- Evaluate command safety before running shell commands.
"""
```

字段说明：

- `[tags.<tag-name>]`：定义一个可在聊天里使用的 tag，例如 `[tags.agent]` 对应 `@tool:agent` 或简写 `@t:agent`。
- `tools`：这个 tag 会预载的工具名列表。工具名必须是已注册且当前用户有权限访问的工具。
- `prompt`：这个 tag 成功激活后追加到 system prompt 的工具使用策略。内容会直接给模型看，不会自动添加 tag 名标题。

使用方式：

```text
@tool:agent 帮我检查这个项目的问题
@t:agent 帮我检查这个项目的问题
```

这会把 `agent` 下配置的工具预载到当前 Session。如果 `prompt` 非空，也会从本轮开始追加到 system prompt。

注意事项：

- 配置 tag 会追加到内置 tag，不覆盖内置 tag。
- 只有 `@tool:<tag>` 或 `@t:<tag>` 成功命中至少一个工具后，当前 Session 才会激活该 tag 的 prompt。
- 直接 `@tool:<tool-name>` 或 `@t:<tool-name>` 只预载指定工具，不激活 tag prompt。
- 激活的 tag 会写入 Session metadata，`/*resume` 后仍生效。
- prompt 文本从 `tool_tags.toml` 动态读取；文件变更后影响后续请求，行为类似 `SOUL.md`。
- 重复预载已经存在的工具时不会重复添加，平台会提示 `已存在工具：<name>`。
- 建议把 `prompt` 写成具体工具使用策略，不要写“当前 tag 是 xxx”这类模型不需要知道的配置机制。

## 运维超时与并发上限

```toml
[ops]
# 单次工具 / Hook / 上下文压缩超时；0 表示不限时。
tool_timeout_seconds = 600
hook_timeout_seconds = 60
compress_timeout_seconds = 300
# 同时运行的 turn / tool / hook 上限；0 表示不限制。
max_concurrent_turns = 4
max_concurrent_tools = 4
max_concurrent_hooks = 4
# 可选：按用户 / 群聊限速；0 表示不限制。
user_messages_per_minute = 12
user_burst = 3
group_messages_per_minute = 60
group_burst = 10
rate_limit_idle_ttl_seconds = 600
# 可选：超过并发上限时允许短暂排队。turn 通常不建议排队。
queue_max_size = 20
queue_wait_timeout_seconds = 10
queue_wait_kinds = ["tool", "hook", "compress"]
# 可选：Provider 连续失败后的熔断；0 表示关闭。
circuit_breaker_failure_threshold = 0
circuit_breaker_open_cooldown_seconds = 60
circuit_breaker_half_open_max = 1
```

- 工具、Hook 和上下文压缩的超时通过 `context` 取消；超时后请求会结束并记录错误，释放并发占用。
- 用户 / 群聊限速按 `平台 + scope` 维度使用令牌桶；超限时直接提示重试，不进入 LLM 或工具链。超级管理员不受限速影响。
- `queue_wait_kinds` 决定哪些请求类型在并发满时允许排队；`turn` 默认不允许排队，队列满或排队超时会明确拒绝。
- 熔断按 Provider 统计连接失败、首包超时、5xx 和 stream error；连续失败达到阈值后打开，冷却后放少量半开探测。用户取消和整轮 response timeout 不计入熔断。

Provider 可配置备用模型：

```toml
[providers.openai]
fallback_provider = "deepseek"
fallback_model = "deepseek-chat"
```

熔断打开时优先切备用 Provider；没有备用时返回明确错误，不再无限重试。外部模型异常只显示为 `degraded`，不会触发自动重启。
- 并发上限按请求类型限制当前活跃任务数；达到上限后新请求会返回 `request concurrency limit reached`，不会无限排队。
- 活跃任务、开始时间、阶段、最近进展和资源指标可通过独立健康接口的 `/tasks` 与 `/metrics` 查看。
- Shell、Hook、AgentSkill / Go Skill 在支持平台上会在超时或取消时终止整个子进程树；图片处理等进程内任务只能依赖 `context` 取消，遇到不响应取消的第三方库仍可能延迟释放。

## 命名 profile 与单轮声明

模型、生图、工具都支持"多个命名 profile + 一个默认"。非默认 profile 需要在群聊消息里显式声明，**只对当前这一轮生效**（回复结束即失效，下条消息要重新声明），**只有超级管理员**能声明。

| 声明 | 作用 | 配置 |
| --- | --- | --- |
| `@model:<名字>` / `#模型:<名字>` | 本轮换模型 | `[model_profiles.<name>]` |
| `@image:<名字>` / `#生图:<名字>` | 本轮换生图端点 | `[image_generation.profiles.<name>]` |
| `@use:<名字>` / `#工具:<名字>` | 本轮额外注入工具 | `[tool_profiles.<name>]` |

**触发符号和关键字都可以自定义**，中文关键字默认就支持：

```toml
[turn_directives]
prefixes = ["@", "#"]                 # 触发符号，可再加 "！" 等
model_keywords = ["model", "m", "模型", "用模型"]
image_keywords = ["image", "img", "生图", "出图"]
tool_keywords = ["use", "工具", "用工具"]
```

**每个 profile 可以配中文/短别名**，声明时就不用打全名：

```toml
# 模型 profile：默认模型仍是 state.toml 的 mode_models.*
[model_profiles.pro]
provider = "deepseek"
model = "deepseek-v4-pro"
aliases = ["强", "强模型", "pro"]

[model_profiles.cheap]
provider = "deepseek"
model = "deepseek-flash"
aliases = ["快", "便宜", "flash"]

# 工具 profile：只在本轮额外注入（不写 Session）
[tool_profiles.admin]
tools = ["shell", "read_file", "edit_file"]
aliases = ["管理", "运维"]

# 生图 profile：未写的字段沿用 [image_generation] 基础配置
[image_generation]
enabled = true
base_url = "https://relay-a.example.com/v1"
api_key_env = "IMAGE_API_KEY"
model = "gpt-image-2.5"
# default_profile = "fast"   # 可选：把某个 profile 设为默认

[image_generation.profiles.fast]
base_url = "https://relay-b.example.com/v1"
api_key_env = "IMAGE_API_KEY_FAST"
quality = "medium"
superadmin_only = true
aliases = ["高清", "fast"]
```

使用示例（全角冒号 `：` 也可以）：

```text
#模型:强 帮我看看这张图的排版        # 本轮用 deepseek-v4-pro
#生图:高清 画一张赛博朋克城市        # 本轮用 relay-b
#工具:管理 跑一下 ls 看看目录        # 本轮注入 shell
@m:flash @image:fast 生成一张产品图  # 可以叠加、可以混用 @ / #
下一条消息不带声明 → 自动回到默认模型 / 默认生图 / 默认工具集
```

- profile 名和别名都支持中文；别名匹配不区分英文大小写。
- 声明会被从消息里剥离，不会发给模型；ElBot 会回一条"本轮已启用：…（仅本轮有效）"。
- 普通用户声明会被拒绝并提示"仅超级管理员可以声明"。
- `#生图:` 只影响 `image_generate`；也可以直接给工具传 `profile` 参数。
- `#工具:` 注入的工具仍要经过原有的角色/风险校验，不会绕过安全策略。

## 角色素材库

```toml
[character_library]
enabled = true
root = "characters"
```

- `enabled`：默认 `true`；为 `false` 时不注册角色工具，`@char:` 也会被忽略。
- `root`：角色库根目录。相对路径以 `app.toml` 所在目录为基准，默认 `characters`，即 `data/config/elbot/characters/`。

目录结构、可见性、工具和 `@char:<id>` 用法见 [角色素材库](character-library.md)。

## 生图服务

```toml
[image_generation]
enabled = true
base_url = "https://your-relay.example.com/v1"
api_key_env = "IMAGE_API_KEY"
model = "gpt-image-2.5"
size = "1024x1024"
quality = "high"
output_format = "png"
timeout_seconds = 180
preset_prompt = ""
max_prompt_runes = 4000
optimize = "rules"          # off / rules（内置 GPT Image Prompts 大全规则库）
optimize_term_mode = "phrase"  # phrase / tag（tag 追加单个单词 tag 串）
optimize_max_anchors = 4
optimize_max_negatives = 10
optimize_max_added_runes = 400
optimize_max_tags = 12
optimize_rewrite = "auto"          # off / auto / always
optimize_rewrite_model = "naming"  # naming / compact / chat / work
optimize_rewrite_min_runes = 40
auto_character = true
auto_context = true
context_default_limit = 6
superadmin_only = true
save_to_character = true
send_by_default = false
supports_reference = false
# 生图独立并发限制；0 表示不限制。queue_size > 0 时超限会短暂排队。
max_concurrent = 0
queue_size = 0
queue_timeout_seconds = 0
```

- 端点必须兼容 `POST {base_url}/images/generations`；端点不同时用 `endpoint` 写全路径。
- API Key 从 `api_key_env` 指定的环境变量读取，也可以直接写 `api_key`（不推荐）。
- `preset_prompt` 是全局预设；角色预设来自 `characters/<id>/image_prompt.md` 和 `character.toml` 的 `[image]`。
- `extra_payload` / `extra_headers` 用来透传中转站特有字段。
- 完整说明见 [生图服务](image-generation.md)。
- `max_concurrent` / `queue_size` / `queue_timeout_seconds` 用于限制生图服务并发；队列满或排队超时返回“生图繁忙/排队超时”，不会拖垮普通聊天。

## 定时报告

```toml
[maintenance.daily_report]
enabled = true
schedule = "0 9,21 * * *"
window_hours = 12
provider = "deepseek"
currency = "CNY"
image_price_per_image = 0.05
peak_pricing = true

[maintenance.daily_report.prices."deepseek-v4-pro"]
input_per_million = 9.0
cache_input_per_million = 0.30
output_per_million = 27.0
offpeak_input_per_million = 4.5
offpeak_cache_input_per_million = 0.15
offpeak_output_per_million = 13.5

[maintenance.daily_report.prices."deepseek-flash"]
input_per_million = 2.0
cache_input_per_million = 0.04
output_per_million = 8.0
offpeak_input_per_million = 1.0
offpeak_cache_input_per_million = 0.02
offpeak_output_per_million = 4.0
```

每天两次推送生图量、Token 消耗、按单价换算的费用（含高峰/空闲双档和生图 0.05/张）、磁盘占用和内存占用。完整说明见 [定时报告](reports.md)。

## Elnis 监听枢纽

Elnis 默认关闭。启用后，ElBot 会启动本地 HTTP ingress，接收 Elwisp 按 Elvena 协议投递的事件。Elnis 配置建议拆到独立 `elnis.toml`，`app.toml` 只保留入口路径。

```toml
[config_files]
elnis = "elnis.toml"

# elnis.toml
enabled = true
allowed_tools = ["shell", "web_search"]

[http]
addr = "127.0.0.1:32170"
max_body_bytes = 1048576
queue_size = 128
workers = 2

[tokens.home]
token_env = ["ELNIS_HOME_TOKEN", "ELNIS_HOME_TOKEN_ALT"]

[delivery_disabled]
targets = [
  # { platform = "telegram" },
  # { platform = "telegram", type = "private", id = "123456789" },
  # { platform = "qqonebot", type = "group", id = "987654321" },
]

[elwisps.server-watchdog]
allowed_tokens = ["home"]
allowed_tools = ["shell"]
disabled_external_tools = ["danger_tool"]
disabled_targets = [
  # { platform = "qqonebot", type = "group", id = "987654321" },
]
```

说明：

- `allowed_tools` 是 Elnis 内部工具白名单；未单独配置的 Elwisp 继承全局默认。
- 单个 Elwisp 若配置 `allowed_tools`，会覆盖全局默认。
- 外部工具默认允许；只有单个 Elwisp 配置 `disabled_external_tools` 时才禁用指定外部工具。
- Elnis 投递默认允许；`[delivery_disabled].targets` 和单 Elwisp `disabled_targets` 用于显式禁止平台、私聊或群聊，配置中的 platform-only 表示禁用整个平台所有投递。
- Elnis 日志只记录 token name，不记录 token 原文。
- `token_env` 支持写成列表，按顺序尝试多个环境变量名；适合临时切换 token 或做多环境兼容。
- 容器部署启用 Elnis 时，`[http].addr` 要写 `0.0.0.0:32170`，宿主机端口只映射 `127.0.0.1`；ElBot 默认不启用 Elnis，未启用时 `/healthz` 不能代表 ElBot / 模型 / OneBot 健康。
- Elwisp 默认启用；只有显式配置 `enabled=false` 才会禁用对应 Elwisp。
- 当前支持 `record`、`direct` 和 `llm` 模式；`llm` 模式使用后台 Session runner 执行。
- `llm` 模式可在 Elvena 请求中指定 `model_slot` 为 `elwisp1`、`elwisp2` 或 `elwisp3`；未指定或对应槽位未配置时回退到 `work` 模型。
- `direct` 和 `llm` 报告只支持按 Elnis 裁决后的平台发送给 superadmins，不支持任意 user/group 目标。
- Elvena 请求中的 `tools` 进入校验、持久化和执行链路；外部工具名仍需由单个 Elwisp 的禁用列表控制。

更多说明见 [Elnis 配置与使用](elnis-usage.md)。

## 平台配置

CLI 默认启用：

```toml
[platform.cli]
enabled = true
```

QQ 官方机器人、QQ OneBot 和 Telegram 配置在示例中默认注释。启用时需要补齐平台自己的认证信息和触发关键词。媒体在实际处理时按 `[platform_files]` 限制下载。

QQ 官方机器人最小配置示例：

```toml
[platform.qqofficial]
enabled = true
app_id = "your-app-id"
client_secret_env = "QQOFFICIAL_CLIENT_SECRET"
trigger_keywords = ["bot"]
```

对应的配置目录 `.env`：

```dotenv
QQOFFICIAL_CLIENT_SECRET=your-client-secret
```

`client_secret_env` 指向保存 Client Secret 的环境变量名；也可以用 `client_secret` 直接写入配置，但不建议提交真实 Secret。

QQ 官方机器人会处理私聊、群内 @ 和平台实际下发的普通群消息。群聊沿用统一唤醒规则：slash 命令、`trigger_keywords`、@ 机器人或引用机器人的历史回复会触发响应；未触发的普通群消息仍会写入聊天历史。普通群消息是否下发取决于 QQ 开放平台为机器人启用的群消息能力。

QQ OneBot 最小配置示例：

```toml
[platform.qqonebot]
enabled = true
ws_url = "ws://127.0.0.1:6700/"
access_token_env = "QQONEBOT_ACCESS_TOKEN"
api_timeout_seconds = 15 # OneBot 写入和响应等待的基础超时
trigger_keywords = ["bot"]
send_file_mode = "base64" # 本地图片、文件、语音默认用 base64；共享文件系统可改为 file_uri
```

> **容器部署注意**：`ws_url` 是 **ElBot 容器内**要访问的地址。`ws://127.0.0.1:6700/` 在容器内指向 ElBot 自身。OneBot 在同一个 Compose 项目（或同一 Docker 网络）的服务中用服务名，例如 `ws://onebot:6700/`；OneBot 在宿主机时，需要配置容器可访问的宿主机地址（Linux 可加 `extra_hosts: ["host.docker.internal:host-gateway"]` 后写 `ws://host.docker.internal:6700/`）；OneBot 在另一台机器时写其 IP / 域名。

`access_token_env` 指向保存 Access Token 的环境变量名；原有 `access_token` 仍然兼容且优先于 `access_token_env`。OneBot 不要求鉴权时，两项都可省略。

`send_file_mode` 同时控制 QQ OneBot 本地图片、文件和 `record` 语音的发送方式。`base64` 适用于 ElBot 与 OneBot 不共享文件系统的部署；`file_uri` 仅适用于双方能访问同一本地路径的场景。

`api_timeout_seconds` 分别作为 OneBot 帧写入和 API 响应等待的基础超时。大帧写入会按编码后大小每完整 1 MiB 增加 1 秒，最多增加到基础超时本身；写入失败或超时后 ElBot 会断开并重连 OneBot，避免一个慢发送长期占住后续消息。发送仍会同步等待平台回执。

Telegram 使用 Bot API long polling。最小配置示例：

```toml
[security.superadmins]
cli = ["local"]
telegram = ["123456789"]

[platform.telegram]
enabled = true
bot_token_env = "TELEGRAM_BOT_TOKEN"
proxy_url_env = "TELEGRAM_PROXY_URL" # 可选
trigger_keywords = ["bot"]
format = "html" # html/plain/rich
stream_edit_interval_milliseconds = 250
```

说明：

- `bot_token_env` 指向保存 Bot Token 的环境变量名；也可以用 `bot_token` 直接写入配置，但不建议提交真实 token。
- `proxy_url_env` 指向保存代理地址的环境变量名；也可以用 `proxy_url` 直接写入配置。代理地址示例：`http://127.0.0.1:7890`、`socks5://127.0.0.1:1080`。
- `format="html"` 是默认值：使用普通 `sendMessage` + `parse_mode="HTML"`，并把常见 Markdown 轻量转换成 Telegram HTML；支持标题、引用、分割线、代码块和表格的可读渲染，失败时自动纯文本重试。
- `format="plain"` 关闭格式化，只发送纯文本。
- `format="rich"` 是实验模式：使用 `sendRichMessage` / 私聊 `sendRichMessageDraft`，Rich Message 失败时会自动退回 HTML；部分客户端可能无法查看 Rich Message。
- `stream_edit_interval_milliseconds` 控制流式刷新节流间隔，默认 250ms，避免触发平台限频。
- 启动连接成功后，ElBot 会把内置 slash 命令同步到 Telegram bot 命令菜单；只同步主命令名，不同步 alias。
- 群聊/超级群组中，命令前缀、触发关键词、`@bot_username` 或回复 bot 消息都会触发处理。私聊默认处理。
- 高风险工具确认消息会附带 Telegram inline keyboard，点击按钮会转换为 `/*confirm`、`/*reject` 等现有确认命令。
- `security.superadmins.telegram` 填 Telegram 用户 ID 或可直接发送的私聊 chat ID，用于超级管理员权限与通知投递。


## 插件和 Hook 配置

插件配置固定放在配置目录的 `plugins/` 下：

- `plugins/hooks.toml`：规则 Hook 配置。
- `plugins/<plugin-id>/hook.toml`：被 `hooks.toml` 引用的插件 Hook；可包含 `[plugin.runtime]` 持久运行配置。
- `plugins/_shared/`：ElBot 创建的跨 Hook 文件协作目录，不作为插件扫描。

Hook 不要直接发平台消息，应返回输出意图，由 Agent 统一交给 Output Manager 发送。

规则与持久 Hook 的完整配置说明见 [Hook](hooks.md)。

## 建议的维护方式


- 用户可编辑配置集中放在平台配置目录，避免直接改源码示例。
- 新增配置项时同步更新本文档。
- 改变默认路径或启动行为时同步更新 [快速开始](getting-started.md) 和 README。
