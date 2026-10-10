## Context

`internal/cli/cli.go:newProvider` 当前 160 行（行 347-507），是 `cli.go` 568 行膨胀的唯一主因。它混合了 4 件事：

1. **模板解析**（~30 行）：根据 `--type` 或交互式选择模板
2. **名字选择**（~10 行）：读用户输入的名字 + 冲突检测
3. **特殊模板分支**（~50 行）：aliyun-pay 的 WorkspaceId 单独询问；aliyun-pay 的 ANTHROPIC_BASE_URL 跳过手动输入并用 WorkspaceId 拼装；minimax 的 region 在字段收集前单独询问；minimax 的 ANTHROPIC_BASE_URL 按 region 选 cn 或 io
4. **字段收集**（~70 行）：遍历 `tpl.Fields`，按 Default / Required / Secret 走 readLine 循环

特殊模板分支用 `if tpl.Name == "aliyun-pay"` / `tpl.Name == "minimax"` 硬编码在 cli 里——任何新增特殊模板都必须改 cli + templates 两处，且 cli 不感知"这是模板的特殊行为"还是"通用机制"。

## Goals / Non-Goals

**Goals：**
- `cli.newProvider` 行数 < 130（拆成 3 个阶段函数，每个 < 50 行）
- 模板可声明"字段收集前的额外问答"（minimax 的 region）
- 模板可声明"字段收集后的字段值覆盖"（minimax 的 ANTHROPIC_BASE_URL 按 region 选）
- cli 端**零** `tpl.Name == "..."` 硬编码
- 行为完全不变（所有现有 cli_test.go 测试原样通过）
- `ccl provider --template minimax` 输出 JSON 不变

**Non-Goals：**
- 不动 `provider` / `state` / `config` 包（cwd binding / setenv / ordered map 等不相关）
- 不引入 stdin 抽象到 templates 包（pre-ask 只声明"问什么 + 怎么验证"，问答本身仍在 cli）
- 不支持 pre-ask 字段的 Optional / 多值 / 嵌套（YAGNI——当前需求只有必填场景）
- 不改 `TemplateField` / `Hint` struct（只在 `Template` struct 上加字段）
- 不顺手改 templates 包其他能改进的地方（如缺省值的 UX、Hints 与 Fields 同步等）
- 不支持 templates 包的注册中心 / 反射能力（FieldOverride.Apply 是模板自带闭包）

## Decisions

### D1：PreAsk 与 PostOverride 是两个独立 hook，而非单一 PreCollectHook

**选择**：拆成 `PreAsk []PreAsk`（字段收集前的问答）+ `PostOverrides []FieldOverride`（字段收集后的覆盖）两个独立扩展点。

**理由**：
- aliyun-pay 的"WorkspaceId"是字段收集问题（属于 PreAsk 或 TemplateField），但 aliyun-pay 的"ANTHROPIC_BASE_URL 用 WorkspaceId 拼装"是字段值覆盖问题（PostOverride）——两者在 minimax 上**同时出现**（region PreAsk + ANTHROPIC_BASE_URL PostOverride），如果用单一 hook，每个 minimax-style 模板都要在同一 hook 函数里同时实现两件事。
- 两个独立 hook 让模板元数据**自描述**：看 `Template.PreAsks` 知道要问什么，看 `Template.PostOverrides` 知道字段值怎么被覆盖，不用读 hook 函数实现。
- 每个 hook 签名单一：`PreAsk.Validate func(string) error` 只关心"输入合法吗"，`FieldOverride.Apply func(env) string` 只关心"给定已收集的值，新值是什么"。

**考虑的替代**：
- 单一 `PreCollectHook func(*PreCollectCtx) error`——灵活度过高，模板定义失去静态可发现性（"看模板定义就知道有 pre-ask"这个目标丢失）。

### D2：通用占位符替换机制在 cli 端实现，而非 templates 包

**选择**：cli 端遍历 `Fields` 时，如果 `Field.Default` 含 `{key}` 占位符，用 `env[key]` 自动替换。

**理由**：
- 现有 `TemplateField.Placeholder` 字段已经在用——`aliyun-pay` 的 WorkspaceId 用 `{WorkspaceId}` 表达，cli 端 `strings.Replace(f.Default, "{WorkspaceId}", ws, 1)` 替换。当前 cli 端硬编码 `tpl.Name == "aliyun-pay"` 才做替换，重构后改成"任何字段的 Default 含 `{key}` 都替换"。
- 占位符替换是字段收集步骤的自然延伸，逻辑跟 readLine 循环紧耦合，放在 cli 端不让 templates 包多一个"渲染函数"职责。
- minimax 的 `ANTHROPIC_BASE_URL` 默认值**字面值不动**——保证 `--template minimax` 输出 JSON 不变（spec 行为不变）；minimax 的 region → URL 选择走 PostOverride 路径。

**考虑的替代**：
- 在 templates 包加 `func (Template) Resolve(env map[string]string) map[string]string` 把占位符替换做成模板方法——但这会让 templates 包承担"渲染"职责，与"声明字段元数据"职责混淆；且 PostOverride 是更强的渲染逻辑（不是替换，是按值选字面值），两者重叠。

### D3：aliyun-pay 模板零修改，minimax 加 PreAsk + PostOverride

**选择**：
- `aliyunPayTpl`：**零修改**。WorkspaceId 仍是 TemplateField（带 `Placeholder: "{WorkspaceId}"`），ANTHROPIC_BASE_URL 的 `Default` 仍是 `https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/apps/anthropic`。cli 端通用占位符替换机制接管 WorkspaceId → BASE_URL 的拼装。
- `minimaxTpl`：加 `PreAsks: [{Key:"region", Prompt:"地域 [cn/io]...", Validate:enum(cn/io), Default:"cn"}]` + `PostOverrides: [{Field:"ANTHROPIC_BASE_URL", Apply: func(env){...}}]`。`ANTHROPIC_BASE_URL.Default` 字面值 `https://api.minimax.cn/anthropic` **不动**（保持 Body 模板和 Field.Default 一致）。

**理由**：
- aliyun-pay 的特殊流程**已经被 TemplateField.Placeholder 机制表达了一半**，只是 cli 端硬编码去掉了这个机制的通用化——不需要引入新 hook。
- minimax 的 region → URL 选择是**逻辑分支**（不是字符串替换），不能用占位符机制表达——必须用 PostOverride hook。
- aliyun-pay 的 WorkspaceId 仍是 TemplateField（写盘前 delete）——保留 CLI 测试 `TestGuidedFlow_AliyunPay_SubstitutesWorkspaceId` 的预期行为（字段收集阶段 WorkspaceId 出现在 BASE_URL 之前）。
- minimax 的 `ANTHROPIC_BASE_URL.Default` 不动保证 `--template minimax` 输出 JSON 与 spec 一致（spec scenario 期望 `api.minimax.cn` 字面值）。

**考虑的替代**：
- aliyun-pay 的 WorkspaceId 改成 PreAsk 字段（从 TemplateField 退到 PreAsk）——但这会让 WorkspaceId 不再作为字段写盘前的临时清理对象，而是 PreAsk 永不写盘的字段；语义清晰但破坏现有 cli_test.go 的预期输入流顺序（pre-ask 阶段问 WorkspaceId vs 字段收集阶段问 WorkspaceId，readLine 调用次数不同）。
- minimax 的 ANTHROPIC_BASE_URL 改成 `https://api.minimax.{region}/anthropic` 占位符——但这会改 Body 模板和 spec scenario（spec 期望 `--template minimax` 输出 `api.minimax.cn`），不是纯 refactor。

### D4：PreAsk.Validate 用 func(string) error，不引入 Enum 字段

**选择**：`PreAsk.Validate func(string) error` 单一验证入口。

**理由**：
- 当前需求只有"非空"和"枚举（cn/io 二选一）"两类验证，都可以用 `func(s string) error` 表达。
- minimax 的 enum 验证一行实现：`func(s string) error { if s != "cn" && s != "io" { return fmt.Errorf("无效地域 %q（只接受 cn / io）", s) }; return nil }`。
- 引入 `Enum []string` 字段会冗余——`Validate` 已经能表达 enum，且 enum 不止一种（cn/io、http/https、自定义等），每种都要单独字段不可持续。

**考虑的替代**：
- `Enum []string` 字段 + 可选 `Validate func(string) error`——冗余：枚举字段是 validate 的常见子集，加 `Enum` 时还要决定与 `Validate` 优先级。

### D5：TemplateField 加 SkipPrompt bool 字段，让模板声明"此字段自动写入"

**选择**：`TemplateField.SkipPrompt bool`——为 true 时 cli 不读 stdin，直接用 Default（先做通用占位符替换）写入 env。

**理由**：
- aliyun-pay / minimax 的 `ANTHROPIC_BASE_URL` 字段**自动写入**（不读用户输入）：aliyun-pay 用 WorkspaceId 拼装，minimax 用 region 选字面值。原版 cli 用 `tpl.Name == "..."` 硬编码 `continue` 跳过 prompt，重构后必须把这个"跳过"声明挪到模板元数据里。
- `SkipPrompt` 是字段元数据，跟 `Required` / `Secret` / `Placeholder` 并列，符合"模板自描述"目标。
- `SkipPrompt` 与 `PostOverrides` 是互补关系：`SkipPrompt` 在 collectFields 阶段省掉 prompt；`PostOverride` 在 collectFields 之后覆盖最终值。两者**顺序**：`SkipPrompt` 写占位符替换后的 Default → `PostOverride` 用 env（含 SkipPrompt 写入的值）做最终覆盖。

**考虑的替代**：
- 用 `PostOverrides` 完全替代字段（aliyun-pay / minimax 的 ANTHROPIC_BASE_URL 不在 Fields 里）——会改 `PostOverride` 语义（从"后置覆盖"变"完全替代"），且 `claude-code-env-config` spec 受影响。
- 保留 cli 端 `tpl.Name == "aliyun-pay" || "minimax"` 的特殊 `continue` 跳过逻辑——违反"cli 端去除模板名硬编码"目标。
- 让 ANTHROPIC_BASE_URL 也走 prompt（用户回车用默认）——UX 变化，且 cli_test.go 期望 cli **不读**这个字段（`guidedLines` 显式 skip mockStdin）。

**Apply 签名补充**：实际实施时 `FieldOverride.Apply` 签名定为 `func(env map[string]any) any`（而非 design 草稿里的 `func(env map[string]string) string`）——cli 端 env map 类型是 `map[string]any`，Apply 签名需对齐避免重复转换。语义不变。

## Risks / Trade-offs

- **[风险]** `FieldOverride.Apply func(env map[string]string) string` 是 Go 函数类型，作为 Template 字段会让模板定义不能简单 JSON 序列化 → **[缓解]** 模板定义本来就不是 JSON 配置（是 Go var 声明）；如果未来需要 JSON 化模板，再引入 `ApplyID string` + 注册表。本次 YAGNI。
- **[风险]** 通用占位符替换机制可能误触发：某个模板的 `Field.Default` 是用户文档（如"https://example.com/{请填入 workspace id}"），会被错误替换 → **[缓解]** 占位符替换只在 `Field.Default` 含**完整 `{key}` 形式**（key 是 alphanumeric，且 key 在已收集的 env 或 pre-ask 结果中存在）时触发，否则跳过；且 cli_test.go round-trip 测试覆盖所有 7 个模板的默认 URL 形态，回归保护到位。
- **[风险]** `cli.newProvider` 拆成 3 个阶段函数后，每个阶段单独测试比 160 行单 func 难——错误信息跨阶段传递变复杂 → **[缓解]** 阶段间用 `error` 包装保留上下文；claude-code-env-config spec 不变（行为一致），现有 cli_test.go 不变（错误信息断言兼容）。
- **[风险]** minimax 模板的 PostOverride.Apply 函数持有 `env` 引用，env 在 cli 端后续会被修改（`delete(env, "WorkspaceId")` 等清理）——PostOverride.Apply 调用时机必须在清理之前 → **[缓解]** 在 `applyPostOverrides` 阶段（清理之前）调用 Apply 函数；applyPostOverrides 是 newProvider 流程的固定阶段，顺序固定。
- **[风险]** cli 端通用占位符替换机制 + minimax PostOverride 同时作用：`minimax` 的 ANTHROPIC_BASE_URL 字段 Default 不含占位符，占位符替换不会触发；PostOverride 直接覆盖 Final 值——两者不会冲突 → **[缓解]** 占位符替换和 PostOverride 是两个独立阶段，后者优先级更高（执行顺序：`collectFields` 内含占位符替换 → `applyPostOverrides` 覆盖）。

## Migration Plan

不需要。本次 change 是纯 refactor：

- 不改 CLI 公开命令（`ccl provider --new` / `--template` / `--list` 等）
- 不改已写盘的 `~/.cc-launcher/providers/*.json` 文件 schema（只动创建流程，不动读取）
- 不改 `state.json` schema
- 不改 `~/.claude/settings.json` schema
- 不需要数据库迁移 / 二进制升级步骤
- 部署后直接生效，无需用户操作

回滚策略：直接 revert commit 即可。所有 cli_test.go 测试原样通过 = 行为不变 = 任意时刻 revert 不影响用户。

## Open Questions

无。设计已收敛，四个核心决策（D1-D4）已与用户确认；待实施阶段如发现新约束，按 OpenSpec `continue-change` 流程补充。