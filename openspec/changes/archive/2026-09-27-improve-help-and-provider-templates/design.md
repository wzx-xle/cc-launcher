## Context

`ccl` 当前没有任何 `--help` 旗标，新手只能读 README；`ccl provider --new` 只能写入 3 个字段，没法配置 deepseek / MiniMax / glm / aliyun 这类需要 6-8 个 env 字段的场景（官方文档里都是写 `ANTHROPIC_MODEL` + `ANTHROPIC_DEFAULT_HAIKU/SONNET/OPUS_MODEL` + `CLAUDE_CODE_AUTO_COMPACT_WINDOW` 等）。

本次新增：
- 三层 `--help`（顶层 / provider / mode）
- `ccl provider --template [type]` 打印 7 种 JSON 模板：`anthropic`、`deepseek`、`minimax`、`glm`、`aliyun-coding`、`aliyun-pay`、`aliyun-token`
- `ccl provider --new --type <t>` 引导式交互流，每步打印对应模板片段

## Goals / Non-Goals

**Goals:**
- 用户在终端里能自我服务地配置 Anthropic + 6 种国内 Anthropic-兼容接入（deepseek / minimax / glm / aliyun 三套餐）
- `--help` 输出可读、明确，不与已有错误路径冲突
- 模板内容是单一事实源：CLI 输出和 README 示例必须与各家官方原页文档同步

**Non-Goals:**
- 不做模型选择 UI（Claude Code 自己选择模型）
- 不做 dry-run 预览（避免状态爆炸）
- 不为 6 种以外的边缘接入做模板（用户手工编辑 JSON 已可覆盖）

## Decisions

### 1. 模板集中放在 `internal/templates` 包

模板是 7 段 JSON 字符串 + 每段对应的字段定义。集中放能让 `templates_test.go` 单测校验"7 个名字都非空 + 7 个 JSON 都能解析 + 7 个 Fields 都不重复"。

```go
type Template struct {
    Name        string  // anthropic / deepseek / minimax / glm / aliyun-{coding,pay,token}
    Description string  // 一行说明，写到 --template 列表
    Body        string  // 完整 JSON 模板（不带 // 注释）
    Hints       []Hint  // 每个 prompt 步骤的解释
    Fields      []TemplateField  // 每步要问什么
}

type Hint struct {
    Step   string  // "name" / "base_url" / "auth_token" / "workspace_id" / "model"
    Prompt string  // 打印给用户的提示语（含模板片段）
}

type TemplateField struct {
    Key         string  // env 中的 key，如 "ANTHROPIC_BASE_URL"
    Prompt      string  // 用户看到的提示
    Default     string  // 模板默认值（用户回车跳过用）
    Required    bool    // 是否必填
    Secret      bool    // 不回显（如 auth_token）
    Placeholder string  // 用于 URL 的占位符（如 aliyun-pay 的 WorkspaceId）
}
```

**理由**：(a) 模板与 CLI 行为耦合，应该在 internal 包内可被 cli 包直接调用；(b) 单测可以保证 7 个名字都在、7 个 JSON 都能 unmarshal、7 个 Fields 不重复；(c) `Fields` 让交互流知道每步要问什么 + 是否必填 + 是否回显，cli 包不用硬编码。

**备选 A**：模板塞进 `internal/cli` 字符串常量 → 不接受，cli 包已经 217 行，再塞 7 段多行 JSON 会臃肿。

**备选 B**：从 README 自动生成 → 不接受，会让 `--template` 输出和源不一致，反而更难维护。

### 2. 类型列表硬编码，不用 reflection / 自动发现

```go
var All = []Template{
    anthropicTpl,
    deepseekTpl,
    minimaxTpl,
    glmTpl,
    aliyunCodingTpl,
    aliyunPayTpl,
    aliyunTokenTpl,
}

func ByName(name string) (Template, bool) { ... }
```

**理由**：7 个模板是 cc-launcher 的固定支持范围（基于已验证的 4 家官方文档），硬编码 + 单测覆盖远胜动态发现。

**备选**：扫描 `~/.cc-launcher/providers/*.json` 反推模板 → 不接受，那些是用户自己的配置不是模板。

### 3. `--help` 输出硬编码字符串，不从 flag 定义反射生成

```go
const helpText = `用法: ccl [子命令] [选项]
...`
```

**理由**：(a) 帮助文字是面向用户的，需要排版可控，反射生成的帮助往往粗糙；(b) cc-launcher 命令面小，硬编码可读性远胜抽象；(c) 与 README 中"用法"章节保持一致只是拷贝问题。

**备选**：用 `flag.PrintDefaults` → 不接受，Go 标准 flag 输出英文 + 排版不友好。

### 4. `--help` 不与"未知选项"路径合并

```go
case "-h", "--help", "help":
    return printTopHelp()
default:
    return fmt.Errorf("未知子命令: %s", args[0])
```

**理由**：用户敲 `--help` 几乎一定是想看帮助，吞到"未知子命令"会让人困惑。**但**未知旗标（如 `--bogus`）仍走错误路径——保留语义清晰度。

**备选**：`--help` 仅顶层支持，子命令下未输入子命令名才显示顶层帮助 → 不接受，子命令内 `--help` 用户期望高。

### 5. 交互流每步先打印解释再读输入

```
$ ccl provider --new --type deepseek
模板预览:
  {
    "env": {
      "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic",
      "ANTHROPIC_AUTH_TOKEN": "sk-xxx",
      "ANTHROPIC_MODEL": "deepseek-flash[1m]",
      "ANTHROPIC_DEFAULT_OPUS_MODEL": "deepseek-flash[1m]",
      "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-flash[1m]",
      "ANTHROPIC_DEFAULT_HAIKU_MODEL": "deepseek-flash",
      "CLAUDE_CODE_SUBAGENT_MODEL": "deepseek-flash"
    }
  }

步骤 1/7 — 提供商名称（用于文件名，如 'deepseek-prod'）
> deepseek-prod

步骤 2/7 — ANTHROPIC_BASE_URL（直接回车用模板默认）
> 

步骤 3/7 — ANTHROPIC_AUTH_TOKEN（不回显）
> sk-xxx

步骤 4/7 — ANTHROPIC_MODEL（直接回车用 deepseek-flash[1m]）
> 

步骤 5/7 — ANTHROPIC_DEFAULT_OPUS_MODEL（直接回车用 deepseek-flash[1m]）
> 

步骤 6/7 — ANTHROPIC_DEFAULT_SONNET_MODEL（直接回车用 deepseek-flash[1m]）
> 

步骤 7/7 — CLAUDE_CODE_SUBAGENT_MODEL（直接回车用 deepseek-flash）
> 

已写入 ~/.cc-launcher/providers/deepseek-prod.json
```

**理由**：用户在填之前就看到要填什么，盲填率显著降低。每个 Step 的文案从 `Template.Hints` 取，cli 包不硬编码。

**风险**：交互流变长（deepseek 7 步、glm 8 步）。缓解：所有提示用一行 + 简化措辞；提供 `--type` 让老用户能跳过模板展示；默认值充分的字段直接回车用默认。

### 6. 模板 JSON 不带注释，避免解析歧义

Go 字符串里的模板用 raw string literal，但 JSON 不允许 `//` 注释；用注释会破坏 `--template | jq` 之类的管道。

```go
var anthropicTpl = Template{
    Name: "anthropic",
    Body: `{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-ant-xxx",
    "ANTHROPIC_MODEL": "sonnet"
  }
}`,
}
```

**理由**：模板应该可被 `jq` / 其他 JSON 工具二次加工；注释挪到 `Hint.Prompt` 里。

**备选**：用 `//` 注释 + 自定义解析器 → 不接受，违反 JSON 兼容性。

### 7. 7 个模板的真实字段配置（已基于各家原页校对）

> 以下每一行的字段值都来自对应官方原页（[DeepSeek](https://api-docs.deepseek.com/zh-cn/quick_start/agent_integrations/claude_code)、[MiniMax](https://platform.minimax.cn/docs)、[智谱 BigModel](https://docs.bigmodel.cn/cn/coding-plan)、[阿里云百炼](https://help.aliyun.com/zh/model-studio/claude-code)），校对日期 2026-09-27。每个模板在 `internal/templates/templates.go` 里必须有 `// verified 2026-09-27: <URL>` 注释。

#### `anthropic` (官方直连，2 字段)
```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-ant-xxx"
  }
}
```

#### `deepseek` (7 字段)
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "sk-xxx",
    "ANTHROPIC_MODEL": "deepseek-flash[1m]",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "deepseek-flash[1m]",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-flash[1m]",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "deepseek-flash",
    "CLAUDE_CODE_SUBAGENT_MODEL": "deepseek-flash"
  }
}
```
> DeepSeek 官方模型映射：`claude-opus` → `deepseek-v4-pro`（按 V4 Pro 计费），`claude-haiku/sonnet` → `deepseek-flash`。

#### `minimax` (6 字段，国内/国际二选一)
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "<MINIMAX_API_KEY>",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1000000",
    "ANTHROPIC_MODEL": "MiniMax-M3[1m]",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "MiniMax-M3[1m]",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "MiniMax-M3[1m]",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "MiniMax-M3[1m]"
  }
}
```
> 国际用户用 `https://api.minimax.io/anthropic` 替换 BASE_URL。`--type minimax` 提示用户选 国内/国际。

#### `glm` (8 字段，智谱 Coding Plan)
```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>",
    "ANTHROPIC_BASE_URL": "https://open.bigmodel.cn/api/anthropic",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.3-flash[1m]",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3[1m]",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3[1m]",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1000000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": 1,
    "API_TIMEOUT_MS": "3000000"
  }
}
```
> 智谱是**套餐制**，需要单独的 Coding Plan API Key（不是平台普通 API Key）。模型走 Anthropic 路由，Claude Code 自己根据 `DEFAULT_*_MODEL` 选。

#### `aliyun-coding` (6 字段，阿里云百炼 Coding Plan)
```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "<Coding_Plan_API_Key>",
    "ANTHROPIC_BASE_URL": "https://coding.dashscope.aliyuncs.com/apps/anthropic",
    "ANTHROPIC_MODEL": "qwen3.7-plus",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "qwen3.7-plus",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "qwen3.7-plus",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "qwen3.7-plus"
  }
}
```

#### `aliyun-pay` (7 字段，阿里云百炼按量计费)
```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "<百炼_API_KEY>",
    "ANTHROPIC_BASE_URL": "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/apps/anthropic",
    "ANTHROPIC_MODEL": "qwen3.7-max",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "qwen3.6-flash",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "qwen3.7-max",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "qwen3.7-max",
    "CLAUDE_CODE_SUBAGENT_MODEL": "qwen3.7-max"
  }
}
```
> BASE_URL 中的 `{WorkspaceId}` 是用户**必须**填自己的，按量计费的 WorkspaceId 在阿里云百炼控制台获取。地域还有 新加坡/美国弗吉尼亚 等变体，本模板默认 华北2（北京）。

#### `aliyun-token` (7 字段，阿里云百炼 Token Plan 个人版/团队版)
```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "<Token_Plan_API_Key>",
    "ANTHROPIC_BASE_URL": "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic",
    "ANTHROPIC_MODEL": "auto",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "qwen3.6-flash",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "qwen3.8-flash",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "qwen3.8-max",
    "CLAUDE_CODE_SUBAGENT_MODEL": "auto"
  }
}
```
> 个人版和团队版的 URL 相同；主模型用 `auto` 让 Claude Code 自动路由。

#### 跨模板的统一约束（已修正）

1. **URL 必须带 Anthropic 兼容路径前缀**：`/anthropic`（deepseek/minimax/glm）或 `/apps/anthropic`（aliyun）。**不能**用各家通用 OpenAI 端点，否则 Claude Code 发出的 Anthropic 格式请求会被各家以"协议不匹配"拒绝。
2. **统一用 `ANTHROPIC_AUTH_TOKEN`** 而非 `ANTHROPIC_API_KEY`——Claude Code 内部按 Anthropic SDK 约定读这个变量（DeepSeek 官方 API 文档用的是 `ANTHROPIC_API_KEY`，但那是给他们的 Python SDK 用的；Claude Code 接的是 Anthropic 约定，所以用 `ANTHROPIC_AUTH_TOKEN`）。
3. **URL 末尾不带 `/v1`**。
4. **每个非官方模板都需要 4 个 model 字段**（`ANTHROPIC_MODEL` + 3 个 `DEFAULT_*_MODEL`），不只是 `ANTHROPIC_MODEL`——这是 Claude Code 内部 sonnet/opus/haiku 路由的依据。

**所有 5 个模板都不需要 `modelOverrides`**——它们都是 OpenAI-兼容接口直连，模型名就是单个字符串字段，不再需要 Bedrock 时代的 ARN 映射。这意味着：
- 交互流只问 4 个字段：name / base_url（可选）/ auth_token / model（可改默认）
- 交互流写出的 JSON 体只包含用户填的字段，不写入 synthetic fields

## Risks / Trade-offs

- [模板内容漂移] `--template` 输出与各家官方原页可能不同步 → 缓解：每个模板在 `templates.go` 里硬编码 `// verified <date>:<URL>` 注释；`templates_test.go` 校验每个 Body 的 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_DEFAULT_*_MODEL` 等关键 key 存在；定期人工 fetch 原页校对（建议季度）。
- [Type 列表固定 7 个] 硬编码 7 个 type，新的 Anthropic-兼容服务商接入不会自动出现 → 接受：手动加条目 + 单测；README 末尾留一段"未列出的提供商可直接编辑 JSON"。
- [4 家 URL/model 变化] DeepSeek / MiniMax / 智谱 / 阿里云的 base URL 和默认 model 会随版本变化（实测 MiniMax M2 → M3 已发生过）→ 缓解：模板由 `internal/templates` 集中维护，单点更新；spec 强制要求每次修改模板必须更新 verified 日期。
- [aliyun-pay 占位符] 用户必须填自己的 WorkspaceId，否则 URL 无效 → 缓解：交互流在 base_url 步骤前先问 WorkspaceId（Required + Placeholder 字段）；写盘前对 `https://{WorkspaceId}....` 做正则校验，确保占位符被替换。
- [minimax 国内/国际] 两套 URL 必须让用户二选一 → 缓解：交互流在 base_url 步骤前先问 国内/国际（Required + 枚举）。
- [glm 字段最多 8 个] 交互流步骤多 → 缓解：所有 Step 提示一行 + 简化措辞；默认值充分的字段直接回车跳过。
- [Help 文本重复] 三层 `--help` 内容部分字面重叠 → 接受：可读性 > DRY；后续如重复过多再抽常量。

## Migration Plan

无状态迁移。`--help`、`--template`、`--type` 全部是新增旗标 / 子命令，不影响现有调用。

## Open Questions

- 是否要给 `ccl mode` 也加 `--template`？目前 `mode` 是一份 6 项的列表，不存在 JSON 模板概念，决定不加。
- 模板是否要支持 `--output <path>` 直接写盘？决定先不做，用户可以 `ccl provider --template deepseek > ~/.cc-launcher/providers/deepseek.json` 临时凑合，后续如果需求明确再加。
- `--type` 是否要带 `--no-prompt`（读 stdin / 环境变量）做无交互模式？决定先不做；当前 cc-launcher 本来就是交互工具，无交互模式范围更大。