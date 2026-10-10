## Why

`internal/cli/cli.go:newProvider` 当前 160 行，混合了 4 件事：模板解析、名字选择、特殊模板分支（aliyun-pay 的 WorkspaceId 拼装 + minimax 的 region 选 BASE_URL）、字段收集。其中 aliyun-pay / minimax 两个特殊分支是**硬编码在 cli 里的**——任何新增特殊模板都必须改 `cli.go` 而非"在 templates 包加模板"，违反了"模板自身应描述自己的特殊流程"的直觉。

按当前架构约定（provider 配置结构必须与 Claude Code 自身一致——架构约束 #2），特殊模板分支应该**作为模板元数据的一部分声明**，cli 只负责"按模板元数据走流程"，不感知具体模板名。

## What Changes

- **新增** `templates.Template.PreAsks []PreAsk` 字段——模板可在字段收集前声明额外问答（minimax 用此声明 region 预问）。
- **新增** `templates.Template.PostOverrides []FieldOverride` 字段——模板可在字段收集后覆盖字段值（minimax 用此覆盖 ANTHROPIC_BASE_URL 为 region 决定的值）。
- **新增** `templates.PreAsk{Key, Prompt, Default, Validate}` 与 `templates.FieldOverride{Field, Apply}` 类型。
- **修改** `cli.newProvider` 拆成 3 个阶段函数（每个 < 40 行）：`collectPreAsks` / `collectFields` / `applyPostOverrides`。
- **修改** cli 端**去除所有** `tpl.Name == "aliyun-pay"` / `"minimax"` 硬编码分支（当前共 3 处：aliyun-pay 的 WorkspaceId skip、aliyun-pay 的 BASE_URL 自动拼装、minimax 的 region 选 BASE_URL）。
- **新增** cli 端通用占位符替换机制：遍历 `Fields` 时，如果 `Field.Default` 含 `{key}` 占位符，自动用 `env[key]` 替换（aliyun-pay 的 ANTHROPIC_BASE_URL 用此机制从 Default 字面值 `https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/...` 拼装出最终 URL——之前是硬编码 `tpl.Name == "aliyun-pay"` 时替换）。
- **修改** `minimaxTpl`：加 `PreAsks: [{Key:"region", Prompt:"...", Validate:enum(cn/io), Default:"cn"}]` + `PostOverrides: [{Field:"ANTHROPIC_BASE_URL", Apply: func(env){...}}]`。**不改** `ANTHROPIC_BASE_URL.Default` 字面值（保持 `https://api.minimax.cn/anthropic`，Body 模板和字段 Default 都不动——保证 `--template minimax` 输出不变）。
- `aliyunPayTpl`：**零修改**（已用 `TemplateField.Placeholder` + `Default` 字面值的 `{WorkspaceId}` 表达拼装逻辑，cli 硬编码去除后由通用占位符替换机制接管）。
- **新增** `internal/templates/templates_test.go` 单元测试：PreAsk 数据驱动、PostOverride 应用、占位符替换。
- `cli_test.go`：**零修改**（行为不变——所有现有 aliyun-pay / minimax / EOF 取消 / round-trip 测试原样通过）。
- **无 BREAKING**——纯 refactor，`ccl provider --new` / `ccl provider --template` 行为完全不变。

## Capabilities

### New Capabilities

- `template-preask`：templates 包提供 `PreAsk` 与 `FieldOverride` 两个扩展点，让模板可声明字段收集前的额外问答（PreAsks）和字段收集后的字段值覆盖（PostOverrides）。cli 流程自动按模板元数据驱动——不再硬编码模板名。

### Modified Capabilities

无。本次 change 不修改任何 spec 的 REQUIREMENTS——`provider-templates` / `provider-new-guided-flow` 等已有 spec 的场景与行为完全不变。

## Impact

- 代码：
  - `internal/templates/templates.go`：`Template` struct 加 `PreAsks` + `PostOverrides` 字段；新增 `PreAsk` / `FieldOverride` 类型；`minimaxTpl` 加 PreAsk + PostOverride 声明（aliyunPayTpl 不动）。
  - `internal/templates/templates_test.go`：新增 PreAsk / PostOverride / 占位符替换的单元测试。
  - `internal/cli/cli.go:newProvider` (行 347-507)：拆成 3 个阶段函数（`collectPreAsks` / `collectFields` / `applyPostOverrides`）；去除 `tpl.Name == "aliyun-pay"` / `"minimax"` 三处硬编码；新增通用占位符替换循环。
  - `internal/cli/cli.go`：文件整体行数从 568 → 估计 ~510（newProvider 从 160 → 3×~40 = ~120，但去重和函数提取抵消部分）。
- 测试：
  - `internal/templates/templates_test.go`：加 PreAsk / PostOverride / 占位符替换单元测试（YAGNI：暂不测试 stdin 交互——cli 端已覆盖）。
  - `internal/cli/cli_test.go`：**0 修改**。现有 7 个相关测试原样通过：
    - `TestGuidedFlow_AliyunPay_SubstitutesWorkspaceId`（531）：行为不变——WorkspaceId 仍 TemplateField（写盘前 delete），BASE_URL 仍自动用 WorkspaceId 拼装。
    - `TestGuidedFlow_MiniMax_RegionCN` / `_RegionIO`（568 / 589）：行为不变——region 仍 pre-ask，BASE_URL 仍按 region 选。
    - `TestGuidedFlow_AllTypes_RoundTrip`（494）：行为不变——输入流仍 WorkspaceId → 其他字段 / region → 其他字段。
    - `TestGuidedFlow_EOF_CancelsNoWrite`（611）：行为不变——EOF 取消逻辑保留。
    - `TestProvider_Help` / `TestTemplate_*` / `TestNewProvider_TypeBogus` / `TestProvider_ShortNewTypePositional_PassesThrough`：行为不变。
- 文档：
  - 不改 README / CLAUDE.md。
- 兼容性：
  - `--template minimax` 输出不变（Body 模板字面值不动）。
  - `--template aliyun-pay` 输出不变（Body 模板已有 `{WorkspaceId}` 占位符，spec 没规定 aliyun-pay BASE_URL 输出值）。
  - 已有的 `~/.cc-launcher/providers/*.json` 配置不受影响（重构只动创建流程，不动已写盘文件的读取）。
  - state.json schema 不变。
  - settings.json schema 不变。
- 跨平台：所有改动限于 `cli` + `templates` 包，与 `launcher_unix.go` / `launcher_windows.go` 无关。
- 包边界：
  - `templates` 包：不读 stdin（PreAsk 只声明 Key + Prompt + Validate，不含 reader），不依赖 `cli` 包。
  - `cli` 包：仍负责 `readLine` / `readLineSecret` 调用，但不再感知具体模板名（去除 `tpl.Name == "..."` 硬编码）。
  - `provider` / `state` / `config` 包：不动。