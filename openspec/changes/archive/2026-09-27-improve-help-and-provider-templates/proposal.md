## Why

`ccl` 当前没有任何 `--help` 旗标：直接 `ccl --help` 会落到"未知子命令"，新手只能去看 README。同时 `ccl provider --new` 的交互流只能写入 `name / base_url / auth_token` 三个字段，写不进 `ANTHROPIC_MODEL` —— 而 deepseek / MiniMax / glm / aliyun 等 Anthropic-兼容接入必须设置 `ANTHROPIC_MODEL` + 3 个 `DEFAULT_*_MODEL` 才能在 Claude Code 里正确路由（每个套餐原页官方文档都是 6-8 个 env 字段）。这次把这两条短板一起补上。

## What Changes

- **新增 `--help` 旗标**：在 `ccl`、`ccl provider`、`ccl mode` 三个入口点都识别 `-h / --help`，打印分层的帮助文本。
- **新增 `ccl provider --template [type]` 子命令**：打印 7 种 Claude Code 提供商接入方式的 JSON 配置模板：`anthropic`（官方）、`deepseek`、`minimax`（MiniMax）、`glm`（智谱）、`aliyun-coding` / `aliyun-pay` / `aliyun-token`（阿里云百炼三个套餐分别拆开）。`type` 省略时列出支持的类型。不写盘，仅 stdout。
- 4 家非官方供应商都是 Anthropic-兼容接入，但每家 env 字段数从 6 到 8 不等（要写 `ANTHROPIC_DEFAULT_HAIKU/SONNET/OPUS_MODEL` 模型映射），不再是最初误以为的"三件套"。
- **改造 `ccl provider --new` 交互流**：
  - 新增 `--type <anthropic|deepseek|minimax|glm|aliyun-coding|aliyun-pay|aliyun-token>` 旗标，可跳过"提供商名称"后的选模板步骤
  - 每步 prompt 前打印当前步骤的解释 + 对应模板的关键片段，避免用户盲填
  - 支持 EOF 取消（现有逻辑已覆盖）
- **新增 `internal/templates` 包**：把 4 个 JSON 模板集中维护，加单测防漂移。
- **README 增加"模板 vs 手工 JSON"小节**：明示 `--template` 是查模板的最短路径。

## Capabilities

### New Capabilities
- `cli-help`: 所有面向用户的子命令（ccl、provider、mode）必须识别 `-h / --help` 并打印分层帮助；非 `--help` 的未知旗标仍走"未知选项"路径以避免误吞。
- `provider-templates`: 提供 7 份可打印的 JSON 模板（anthropic、deepseek、minimax、glm、aliyun-coding、aliyun-pay、aliyun-token），每份的 env 字段数从 2 到 8 不等；模板内容必须与各家原页官方文档一致，并在 `internal/templates/templates.go` 中标注来源 URL 与校对日期。
- `provider-new-guided-flow`: `ccl provider --new` 交互流按类型引导，每步展示该步要填什么、为什么、以及模板对应片段；支持 `--type` 跳过选模板步骤。

### Modified Capabilities
<!-- 无现有 capability 的需求需要变更。 -->

## Impact

- 代码：`internal/cli/cli.go`（新增 `--help` 路由 + `--template` 子路由 + `--type` 旗标 + 交互流改造）、`cmd/cc-launcher/main.go`（顶层 `--help` 识别）、新增 `internal/templates/templates.go` + `templates_test.go`。
- 用户文档：`README.md` 增加"用模板配置提供商"小节并指向 `--template`。
- 兼容性：`--help` 是新增旗标；`--type` 是 `provider --new` 的新增可选旗标；都不破坏现有调用。
- 不引入新依赖。