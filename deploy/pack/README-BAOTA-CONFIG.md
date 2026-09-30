# 宝塔面板改配置速查（ElBot）

本文对应离线包里的 `data/config/elbot/`。宝塔面板只负责"改文件 / 重启 / 看日志"，配置本身还是 TOML。

## 1. 宝塔里对应哪些操作

| 要做的事 | 宝塔面板位置 | 具体路径 |
| --- | --- | --- |
| 改配置 | 【文件】→ 进入站点目录 → 双击 `app.toml` | `.../deploy/data/config/elbot/app.toml` |
| 改 API Key | 【文件】→ 编辑 `.env` | `.../deploy/.env` |
| 改 Soul / 角色库 | 【文件】 | `.../data/config/elbot/SOUL.md`、`characters/<id>/` |
| 校验配置 | 【终端】 | `cd /opt/elbot/deploy && docker compose exec elbot elbot config check --config /data/config/elbot/app.toml` |
| 重启生效 | 【Docker】（装了 Docker 管理器）或【终端】 | 只改 TOML：`docker compose restart`；改 `.env`：`docker compose up -d --force-recreate` |
| 看日志排错 | 【Docker】→ 容器 → 日志，或【终端】 | `docker compose logs --tail=100 elbot` |
| 看健康状态 | 【终端】 | `curl -sS http://127.0.0.1:32171/live`、`/ready`、`/healthz` |
| 定时备份 | 【计划任务】→ Shell 脚本 | `bash /opt/elbot/deploy/backup.sh` |
| 反向代理/HTTPS | 【网站】→ 反向代理 + SSL | 参考 `nginx-elbot.conf` |

> 宝塔的文本编辑器不会校验 TOML。**保存后一定先跑上面的 `config check`**，确认输出 `config OK` 再重启。

## 2. 保存后校验 / 重启

```bash
cd /opt/elbot/deploy

# 1) 校验（不启动服务，只解析配置）
docker compose exec elbot elbot config check --config /data/config/elbot/app.toml
# scratch 精简镜像没有 shell，用这条：
# docker compose run --rm --no-deps elbot config check --config /data/config/elbot/app.toml

# 2) 重启
docker compose up -d
```

校验输出示例：

```text
config OK
path: /data/config/elbot/app.toml
commands.prefixes: /*
character_library: enabled=true root=/data/config/elbot/characters
image_generation: model=gpt-image-2.5 profiles=2 default_profile="fast"
model_profiles: 2
tool_profiles: 1
turn_directives: prefixes=@ # model=model/m/模型/用模型 image=image/img/生图/出图 tool=use/工具/用工具
daily_report: enabled=true schedule="0 9,21 * * *" window_hours=12 provider="deepseek"
warnings: none
```

有 `warnings:` 就按提示补；有语法错会直接报错并退出非 0。

## 3. 可直接粘贴的配置片段

把需要的段**追加到 `app.toml` 末尾**。注意：同一个 `[section]` 只能出现一次，改已有配置就直接改那一段，不要重复粘贴。

### 3.1 命令前缀

```toml
[commands]
prefixes = ["/*"]
```

### 3.2 角色素材库

```toml
[character_library]
enabled = true
root = "characters"
```

### 3.3 生图基础配置 + 多端点

```toml
[image_generation]
enabled = true
base_url = "https://relay-a.example.com/v1"
api_key_env = "IMAGE_API_KEY"
model = "gpt-image-2.5"
size = "1024x1024"
quality = "high"
output_format = "png"
timeout_seconds = 180
preset_prompt = ""
max_prompt_runes = 4000
optimize = "rules"
optimize_term_mode = "phrase"
optimize_rewrite = "auto"
optimize_rewrite_model = "naming"
auto_character = true
auto_context = true
context_default_limit = 6
superadmin_only = true
save_to_character = true
send_by_default = false
supports_reference = false
image_price_per_image = 0.05
# default_profile = "fast"

[image_generation.profiles.fast]
base_url = "https://relay-b.example.com/v1"
api_key_env = "IMAGE_API_KEY_FAST"
quality = "medium"
superadmin_only = true
aliases = ["高清", "fast"]
```

### 3.4 命名 profile（模型 / 工具）+ 触发写法

```toml
[turn_directives]
prefixes = ["@", "#"]
model_keywords = ["model", "m", "模型", "用模型"]
image_keywords = ["image", "img", "生图", "出图"]
tool_keywords = ["use", "工具", "用工具"]

[model_profiles.pro]
provider = "deepseek"
model = "deepseek-v4-pro"
aliases = ["强", "强模型"]

[model_profiles.cheap]
provider = "deepseek"
model = "deepseek-flash"
aliases = ["快", "便宜"]

[tool_profiles.admin]
tools = ["shell", "read_file", "edit_file"]
aliases = ["管理", "运维"]
```

群聊里就能用：`#模型:强 帮我看看`、`#生图:高清 画一张`、`#工具:管理 跑一下 ls`。

### 3.5 每天两次定时报告

```toml
[maintenance.daily_report]
enabled = true
schedule = "0 9,21 * * *"
window_hours = 12
provider = "deepseek"
currency = "CNY"
image_price_per_image = 0.05
peak_pricing = true
# holidays = ["2026-10-01", "2026-10-02", "2026-10-03"]

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

报告按 `[security.superadmins]` 发送，记得配好平台用户 ID：

```toml
[security.superadmins]
qqonebot = ["你的QQ号"]
```

### 3.6 群聊开放工具时的安全配置

如果群聊里的普通成员也能使用工具，不要只依赖“容器不是 root”保护 API Key：`deploy/.env` 通过 Compose 注入进程环境，Shell / Hook 子进程也可能继承。至少检查：

```toml
[security]
user_max_tool_risk = "low"       # 普通用户不要开放 high / critical
superadmin_confirm_risk = "high"

[security.superadmins]
qqonebot = ["只填信任的QQ号"]
```

- 不要给普通用户开放含 `shell` / 文件写入 / 外部请求的 `tool_tags`；
- 外部 Skill 与 Hook 不经过 ElBot 的风险确认，只安装可信来源；
- 生图建议保持 `[image_generation] superadmin_only = true`，或在上游 / 中转站按 Key 设置额度、限速和每日上限；
- CLI / Elnis 继续只映射到宿主机 `127.0.0.1`，公网走 HTTPS/WSS 反向代理，并使用强随机 token。

### 3.7 QQ OneBot 容器地址

```toml
[platform.qqonebot]
enabled = true
ws_url = "ws://onebot:6700/"   # 容器视角地址，按实际接入方式改
access_token_env = "QQONEBOT_ACCESS_TOKEN"
trigger_keywords = ["bot"]
send_file_mode = "base64"      # 不共享文件系统时保持 base64
```

- OneBot 在同一 Compose 项目 / Docker 网络：用服务名，如 `ws://onebot:6700/`；
- OneBot 在宿主机：用 `ws://host.docker.internal:6700/`，并在 Compose 加 `extra_hosts: ["host.docker.internal:host-gateway"]`；
- 不要写 `127.0.0.1`，那会指向 ElBot 容器自身。

## 4. 常见问题

| 现象 | 处理 |
| --- | --- |
| `config check` 报 TOML 解析错误 | 多半是重复的 `[section]`、中文引号 `“”`、或漏了引号；用面板编辑器看报错行号 |
| 改完没生效 | 只改 TOML 用 `docker compose restart`；改 `.env` 必须用 `docker compose up -d --force-recreate`，`restart` 不会重新注入环境变量 |
| `/ready` 长期失败 | 检查 `data` 目录 / SQLite 目录可写、平台连接日志，以及 `ELBOT_HEALTH_ADDR` 配置 |
| `/healthz` 为 `degraded` | 模型 API / 中转站可能异常；先查上游，不要直接重启容器 |
| 报告没收到 | 检查 `[security.superadmins]` 是否配了当前平台用户 ID，以及 `docker compose logs` 里的 warning |
| 生图 404 | `base_url` 少/多了 `/v1`；用 `endpoint` 直接写全路径 |
| 模型 profile 声明无效 | `provider` 必须是 `providers.toml` 里存在的名字，`config check` 会给出 warning |
| 权限提示"仅超级管理员" | 你的平台用户 ID 不在 `[security.superadmins]` |
