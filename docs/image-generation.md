# 生图服务（image_generate）

ElBot 内置 `image_generate` 工具，用于对接 OpenAI 兼容的 `/images/generations` 端点，例如中转站提供的 GPT Image 2.5 服务。它会在发送请求前自动拼接**预设提示词**，并把结果导入 Media Center、作为图片段返回给模型，可选发到当前聊天、写回角色素材库。

## 配置

生图的 `[image_generation]` 位于共享的 `services.toml`（旧部署仍可放在 `app.toml`）。本节后面的 `[image_generation]` 片段都指同一个 section：

```toml
[image_generation]
enabled = true
base_url = "https://your-relay.example.com/v1"  # 会自动拼 /images/generations
# endpoint = "https://your-relay.example.com/v1/images/generations"  # 端点不同时直接写全
api_key_env = "IMAGE_API_KEY"
# api_key = ""                                     # 不推荐，优先用环境变量
model = "gpt-image-2.5"
size = "1024x1024"
quality = "high"          # 具体取值以中转站为准（常见 low/medium/high）
output_format = "png"     # png / jpeg / webp
# response_format = "b64_json"  # 只有中转站要求时才填
timeout_seconds = 180

preset_prompt = ""        # 全局预设提示词
# negative_prompt = ""
max_prompt_runes = 4000

superadmin_only = true    # 只有超级管理员能调用
save_to_character = true  # 出图写回当前角色的 images/
send_by_default = false   # 生成后是否默认发到当前聊天

supports_reference = false  # 中转站是否支持参考图
reference_field = "image"   # 参考图透传字段名

# extra_payload = { }
# extra_headers = { }
# proxy = ""
```

API Key 从 `api_key_env` 指定的环境变量读取；Docker 部署时写在 `deploy/.env`：

```bash
IMAGE_API_KEY=sk-xxxxxxxx
```

## 提示词是怎么拼的

最终提示词按顺序拼成：

```text
最终 prompt = 全局预设 preset_prompt
            + 每个角色预设（image_prompt.md 和 character.toml 的 [image] preset_prompt）
            + 场景描述（prompt 参数）
            + 多角色同框约束（索引了 2 个及以上角色时）
            + "avoid: <negative_prompt>, <各角色 negative_prompt>"
```

- 角色预设只在"指定了 `character_id` / `character_ids`"或"本轮用 `@char:<id>` 启用了角色"时加入；
- 总长度受 `max_prompt_runes` 限制；多角色时每个角色块会带 `角色 名称（id）：` 标签；
- 全局预设用来放统一画风/画质/安全词，角色预设用来放人物外貌、发色、服装、画风一致性关键词。

**触发角色预设的方式：**

1. 模型显式传单个角色：

```json
{"prompt": "在雨里的霓虹街道回眸", "character_id": "catgirl"}
```

2. 模型显式传多个角色：

```json
{"prompt": "两人在雨夜街道同框", "character_ids": ["catgirl", "foxgirl"]}
```

3. 消息里先启用一个或多个角色（推荐，确定性自动带上）：

```text
@char:catgirl @char:foxgirl 画一张两人站在雨里的霓虹街道同框
```

`@char:` 激活的角色会随本轮上下文传给 `image_generate`。默认情况下，所有被索引的角色都会写进**同一张图**；只有当工具参数 `count > 1` 时，才会生成多张图，并且每张图仍然包含全部被索引角色，不会把角色拆到不同图片里。

参考图还支持多张：

```json
{
  "prompt": "两人同框",
  "character_ids": ["catgirl", "foxgirl"],
  "reference_images": ["catgirl:avatar.png", "foxgirl:avatar.png"]
}
```

`reference_images` 每项可以是 `media:<sha256>`、角色图片名，或多角色时的 `<character_id>:<图片名>`。是否支持多张参考图取决于中转站；开启 `supports_reference = true` 后，单张会按原格式发送，多张会以数组发送到 `reference_field` 指定的字段。

## 内置提示词优化（rules）

`image_generate` 内置了从 `GPT_Image_Prompts_大全.xlsx` 转换来的规则库（16 个大类、226 条场景提示词、15 个万能模板、关键词词库、10 组负面提示词、10 个用途比例方案、50 条进阶技巧），会在调用生图服务前对场景描述做**针对性补全**。

它做四件事：

1. **识别用途**：从「参数速查」匹配头像 / 壁纸 / 海报 / 电商主图 / 封面 / 详情页等，补推荐比例 + 用途补充句；
2. **识别大类与场景**：从「全部提示词汇总」匹配最接近的一条，提取它的英文固定短语作为画风/镜头/光线锚点；
3. **补负面词**：按「负面提示词」类别选择性追加 `Avoid: ...`（有人物 → 人体结构/面部/重复；有文字 → 文字与标识；产品/海报 → 构图；始终包含画质与安全合规）；
4. **编辑保护**：识别"换背景 / 局部修改 / 扩图 / 修图"等意图时追加 `Keep the subject, pose, composition, lighting, and background unchanged; do not redraw the whole image`；使用参考图时追加角色一致性要求。

配置：

```toml
[image_generation]
optimize = "rules"          # off 关闭，rules 使用内置规则库
optimize_term_mode = "phrase"  # phrase：短语锚点；tag：单个单词 tag 串
optimize_max_anchors = 4
optimize_max_negatives = 10
optimize_max_added_runes = 400
optimize_max_tags = 12
```

工具参数 `optimize` 可以按次覆盖。优化器**只追加、不重写**用户原句，原句永远在最前面；总追加长度受 `optimize_max_added_runes` 限制。

示例：

```text
输入：给猫娘画一个头像
输出：给猫娘画一个头像 1:1 square composition, Square avatar, centered face, clean background, ultra-detailed
负面：low quality, blurry, out of focus, bad anatomy, extra fingers, ...

输入：赛博朋克城市夜景，16:9
输出：赛博朋克城市夜景，16:9 Cyberpunk city street, neon signs, holograms, flying cars
（已有比例时不会再补比例）
```

### 两种输出模式

`optimize_term_mode` 控制追加内容的形态，工具参数 `term_mode` 可按次覆盖：

| 模式 | 追加内容 | 示例 |
| --- | --- | --- |
| `phrase`（默认） | 用途补充句 + 短语锚点 | `Square avatar, centered face, clean background, Watercolor illustration, loose brush strokes` |
| `tag` | 比例 + 单个单词 tag 串 | `1:1, centered, face, avatar, square, ultra-detailed` |

tag 模式的选词顺序：

1. 命中场景提示词的**单词表**（按词频 + 相关性排序，如 `neon / city / rain / holograms`）；
2. 用途补充句里的有效单词（如 `square / avatar / centered / face`）；
3. `画质` 词库里 1-2 个风格中性的词（如 `ultra-detailed`、`8k`）；
4. 比例（如 `1:1`、`16:9`）。

tag 模式**不会**从风格/光线/色彩/构图词库里乱挑词，避免出现"水彩 + photorealistic"这种自相矛盾；命中不了场景时只加画质词 + 编辑保护句。原句仍然保持在最前面。

实际示例：

```text
输入：a cyberpunk street at night
tag ：a cyberpunk street at night neon, city, rain, signs, crowded, holograms, ultra-detailed

输入：给猫娘画一个头像
tag ：给猫娘画一个头像 1:1, centered, face, avatar, square, ultra-detailed

输入：水彩风格的森林小路
tag ：水彩风格的森林小路 background, illustration, white, brush, watercolor, loose, ultra-detailed

输入：局部修改：把背景换成沙滩
tag ：局部修改：把背景换成沙滩 Keep the subject, pose, composition, lighting, and background unchanged; do not redraw the whole image
```

> 规则库只能做关键词/短句匹配，不做翻译和意图理解。中文口语化、复合意图（例如"猫娘在海边吃冰淇淋"）主要靠匹配到的类别锚点兜底；如果你需要更强的改写，可以在此基础上再加一层 LLM 优化（见下方"后续可扩展"）。

### 数据拆分层级

转换脚本对原表格做了三层拆分：

| 层级 | 说明 | 示例 |
| --- | --- | --- |
| 条目级 | 按逗号把同格的多个标签拆成独立条目 | `low quality, blurry, out of focus` → 3 条负面词 |
| 短语级 | 英文提示词按逗号拆成静态短语锚点 | `soft natural light`、`shallow depth of field` |
| 单词级 | 每条提示词/关键词/负面词再分词，去停用词后建单词索引 | `soft`、`natural`、`light`；`keyword_terms`、`entries[].terms`、`vocabulary` |

- `keywords` / `negatives[].items` 是条目级，可直接当标签用；
- `entries[].anchors` 是短语级，优化时优先使用（语义完整）；
- `entries[].terms`、`keyword_terms`、`vocabulary` 是单词级，用于匹配用户输入和补齐锚点空位；
- 单词级只作为补充：优先放短语，单词最多再补 2 个，并且会过滤 `photo/light/style` 这类通用词，避免把语义拆碎后堆砌。

### 更新规则库

规则库是预生成的 JSON，源文件放在 `scripts/data/`：

```bash
python scripts/convert_image_prompts.py   --input scripts/data/GPT_Image_Prompts_大全.xlsx   --output internal/imagegen/prompts/library.json
```

改完重新编译即可（JSON 通过 `go:embed` 打进二进制）。

### 后续可扩展

当前是纯本地规则，不调用模型。如果需要"语义级改写"（长句 → 结构化英文提示词、中文 → 英文、复合意图拆解），可以在 `OptimizeResult` 之后再加一次 LLM 调用：用匹配到的模板/关键词作为参考，让配置的模型重写，然后与全局/角色预设合并。这一步需要指定一个模型槽位（建议用 compact/naming 这类低成本模型），需要时再加。

## 角色侧配置

在角色文件夹里加图片预设：

```text
characters/catgirl/
  image_prompt.md        # 推荐：自由文本外貌/画风描述
  character.toml         # [image] 结构化预设
  images/avatar.png      # 可作为参考图
```

```toml
[character]
id = "catgirl"
name = "猫娘"
# ...

[image]
preset_prompt = "银白色短发，猫耳，琥珀色眼睛，黑色露肩上衣"
negative_prompt = "extra fingers, blurry"
size = "1024x1536"       # 覆盖全局 size
quality = "high"
references = ["avatar.png"]  # 作为参考图的角色图片名
```

也可以让模型通过 `character_manage` 写入：

```json
{
  "operation": "update",
  "id": "catgirl",
  "docs": {"image_prompt": "银白色短发，猫耳，琥珀色眼睛"},
  "image": {"negative_prompt": "extra fingers", "references": ["avatar.png"]}
}
```

## 自动编排（mode = auto）

`image_generate` 默认 `mode = "auto"`，会按需自动组合四件事：

| 能力 | 触发条件 | 说明 |
| --- | --- | --- |
| **自动选角** | prompt 里出现多个角色名/别名，或传了 `character_id: "auto"` / `character_ids` / `character_query` | 命中后自动带上所有命中角色的图片预设和参考图，合并到同一张图 |
| **自动拉群聊上下文** | prompt 里出现"刚才/上一条/那张图/群里/大家"等指代词 | 从当前群历史检索相关消息，作为参考对话；写进 rewrite 输入，未做改写时追加到 prompt |
| **TAG 库优化** | `optimize = "rules"` | 自动补用途比例、画风锚点/单词 tag、负面词 |
| **LLM 语义改写** | `optimize_rewrite = "auto"` 且 prompt 太短（默认 < 40 字）或拉了群聊上下文 | 用 `optimize_rewrite_model` 指定的低成本模型改写；失败自动回退到规则结果 |

`mode = "manual"` 时只用显式参数，不自动选角/拉上下文/改写。

新增配置：

```toml
[image_generation]
optimize_rewrite = "auto"          # off / auto / always
optimize_rewrite_model = "naming"  # naming / compact / chat / work
optimize_rewrite_min_runes = 40
auto_character = true
auto_context = true
context_default_limit = 6
```

### 让模型显式查 TAG 库

新增了 `prompt_library_search` 工具，可以按关键词查内置词库：

```json
{"query": "cyberpunk", "scope": "entry", "limit": 3}
```

返回命中条目的 `anchors`、`terms`、关键词分组、负面词、用途比例和进阶技巧。适合模型在写 `prompt` 前先查"赛博朋克该配什么镜头/光线"。

## 工具参数

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `prompt` | 是 | 画面/场景描述；不要重复角色外貌，会由角色预设自动拼接 |
| `character_id` | 否 | 指定单个角色；不填时使用本轮 `@char:<id>` 启用的角色 |
| `character_ids` | 否 | 指定多个角色；默认所有角色画进同一张图，最多 4 个 |
| `size` | 否 | 如 `1024x1024`、`1536x1024`、`1024x1536`；优先级：参数 > 第一个角色 `[image].size` > 全局 |
| `quality` | 否 | `low` / `medium` / `high`；优先级同上 |
| `reference_image` | 否 | 单张参考图：`media:<sha256>`、角色图片名，或多角色时 `<character_id>:<图片名>`；需要 `supports_reference = true` |
| `reference_images` | 否 | 多张参考图；每项同上，多个角色时建议用 `<character_id>:<图片名>` 标明归属 |
| `count` | 否 | 强制生成张数，默认 1，单次最多 4；每张都包含全部已索引角色 |
| `save_to_character` | 否 | 是否写回角色 `images/`；多角色时写回第一个角色；默认跟 `save_to_character` 配置 |
| `send` | 否 | 是否直接发到当前聊天；默认 `false` |
| `mode` | 否 | `auto`（默认）/ `manual` |
| `character_query` | 否 | `character_id: "auto"` 时的检索词；不填用 prompt |
| `context_query` | 否 | 拉群聊上下文的关键词；`auto` 从 prompt 提取，`off` 关闭 |
| `context_limit` | 否 | 拉取条数，默认 6 |
| `rewrite` | 否 | 本次是否做 LLM 语义改写 |

`count` 通过最多 4 个并发子请求实现，每个上游请求仍是 `n = 1`；这样不依赖中转站是否支持 `n > 1`，每张图也都会包含全部被索引角色。

## 出图之后

- 图片导入 Media Center，返回图片段，模型能直接看到结果；`count > 1` 时会返回多张图片段，每张图都包含全部被索引角色；
- `save_to_character = true` 且解析到角色时，图片写回第一个角色的 `characters/<id>/images/`，并在 `character.toml` 的 `[[images]]` 里登记 `media:<sha256>`；
- `send = true` 或 `send_by_default = true` 时会把图片发到当前会话（群聊可用）。

## 中转站兼容性说明

`image_generate` 发送的是 OpenAI 风格的 JSON：

```json
{
  "model": "gpt-image-2.5",
  "prompt": "...",
  "n": 1,
  "size": "1024x1024",
  "quality": "high",
  "output_format": "png"
}
```

响应支持两种常见形态：

```json
{"data": [{"b64_json": "..."}]}
{"data": [{"url": "https://..."}]}
```

- 返回 `url` 时会自动下载（带超时和 20MB 上限）；
- 单张参考图按 `reference_field` 发送为字符串；`reference_images` 有多张且 `supports_reference=true` 时，同一字段会发送为 data URL 数组；中转站若不支持数组，请每次只传一张参考图。
- 非 2xx 会读取 `error.message` 作为错误信息；
- 中转站如果有额外字段（`aspect_ratio`、`seed`、`watermark` 等），用 `extra_payload` 透传；需要额外请求头用 `extra_headers`；
- GPT Image 系列有的中转不接受 `response_format`，默认不发送；只有中转站明确要求时才配置 `response_format = "b64_json"`；
- `quality` 的可选值以中转站为准，如果报错可先改成 `medium` 或删掉该字段（用 `extra_payload` 覆盖为空）。

## 权限与费用

- `superadmin_only = true`（默认）时，只有 `[security.superadmins]` 里的用户能调用；
- 设为 `false` 后工具风险是 `medium`，普通用户还需要 `security.user_max_tool_risk >= "medium"` 才能调用；
- 建议配合中转站侧的额度和限速一起使用；单次调用失败会以工具结果形式返回，不会影响 Session。

## 排错

| 现象 | 原因 |
| --- | --- |
| `生图服务未启用` | `services.toml` 的 `[image_generation] enabled = false`（旧部署为 `app.toml`）或没重启 |
| `缺少 API Key` | `IMAGE_API_KEY` 没设置，或 `.env` 没被加载 |
| `生图服务返回 404` | `base_url` 少/多了 `/v1`；用 `endpoint` 直接写全路径 |
| `生图服务返回 400` | `quality` / `size` / `output_format` 不被中转支持；用 `extra_payload` 调整 |
| `没有返回图片` | 中转返回结构不是 `data[].b64_json` / `data[].url` |
| 结果写回角色失败 | 角色不属于调用者；超级管理员或角色拥有者才能写 |
