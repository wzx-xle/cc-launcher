## 1. Templates 包类型扩展

- [x] 1.1 在 `internal/templates/templates.go` 新增 `type PreAsk struct { Key, Prompt, Default string; Validate func(string) error }`
- [x] 1.2 在 `internal/templates/templates.go` 新增 `type FieldOverride struct { Field string; Apply func(map[string]any) any }`（注意 env 类型对齐 cli 端 map[string]any）
- [x] 1.3 在 `internal/templates/templates.go` 的 `Template` struct 加 `PreAsks []PreAsk` 与 `PostOverrides []FieldOverride` 字段
- [x] 1.4 在 `internal/templates/templates.go` 的 `TemplateField` struct 加 `SkipPrompt bool` 字段（design D5 决策：aliyun-pay / minimax 的 ANTHROPIC_BASE_URL 字段标 SkipPrompt: true）

## 2. minimax 模板声明 PreAsk + PostOverride + SkipPrompt

- [x] 2.1 `minimaxTpl` 加 `PreAsks: []PreAsk{{Key:"region", Prompt:"地域 [cn/io]（国内 api.minimax.cn / 国际 api.minimax.io；默认 cn）: ", Default:"cn", Validate: 验证 s=="cn" || s=="io"}}`
- [x] 2.2 `minimaxTpl` 加 `PostOverrides: []FieldOverride{{Field:"ANTHROPIC_BASE_URL", Apply: func(env map[string]any) any { if region == "io" { return "https://api.minimax.io/anthropic" }; return "https://api.minimax.cn/anthropic" }}}`
- [x] 2.3 `minimaxTpl` 的 `ANTHROPIC_BASE_URL` TemplateField 加 `SkipPrompt: true`（cli 不读 prompt）
- [x] 2.4 保持 `minimaxTpl.Body` 与 `ANTHROPIC_BASE_URL.Default` 字面值不动（仍为 `"https://api.minimax.cn/anthropic"`）——保证 `ccl provider --template minimax` 输出 JSON 不变
- [x] 2.5 `aliyunPayTpl` 的 `ANTHROPIC_BASE_URL` TemplateField 加 `SkipPrompt: true`（cli 不读 prompt，自动用 WorkspaceId 拼装）

## 3. TDD: templates 包单元测试

- [x] 3.1 在 `internal/templates/templates_test.go` 新增 `TestMinimax_PreAskRegion`：断言 `ByName("minimax").PreAsks` 含 1 个 PreAsk，`Key == "region"`，`Default == "cn"`，`Validate("io") == nil`，`Validate("xx") != nil`
- [x] 3.2 新增 `TestMinimax_PostOverride_BaseURL`：断言 `ByName("minimax").PostOverrides` 含 1 个 FieldOverride，`Field == "ANTHROPIC_BASE_URL"`；调 `Apply(map[string]any{"region":"cn"})` 得 `"https://api.minimax.cn/anthropic"`；调 `Apply(map[string]any{"region":"io"})` 得 `"https://api.minimax.io/anthropic"`
- [x] 3.3 新增 `TestAliyunPay_NoPreAsk`：断言 `ByName("aliyun-pay").PreAsks` 为空 / nil（保证 aliyun-pay 不走 pre-ask 流程，行为不变）
- [x] 3.4 新增 `TestAliyunPay_BaseURL_SkipPrompt`：断言 `ByName("aliyun-pay").Fields` 找到 `Key == "ANTHROPIC_BASE_URL"` 字段，`SkipPrompt == true`
- [x] 3.5 跑 `go test ./internal/templates/...`，4 个新测试全过

## 4. cli.newProvider 拆 3 个阶段函数

- [x] 4.1 在 `internal/cli/cli.go` 新增 `func (c *CLI) collectPreAsks(tpl templates.Template, env map[string]any) error`：遍历 `tpl.PreAsks`，按 `Prompt` 读用户输入（用包内 `readLine`），空输入用 `Default`，验证失败重试；结果写入 `env[PreAsk.Key]`
- [x] 4.2 重构 `collectFields` 内联为新函数 `func (c *CLI) collectFields(tpl templates.Template, env map[string]any) error`：遍历 `tpl.Fields`，通用占位符替换（在写 `env[f.Key]` 之前遍历 env 替换 `{key}` 占位符），SkipPrompt 字段跳过 readLine 直接用替换后的 defaultVal 写入 env；其余字段按 `Prompt` / `Default` / `Required` / `Secret` 走 readLine 循环
- [x] 4.3 新增 `func (c *CLI) applyPostOverrides(tpl templates.Template, env map[string]any)`：遍历 `tpl.PostOverrides`，调 `ovr.Apply(env)` 拿新值，`env[ovr.Field] = newVal`
- [x] 4.4 `newProvider` 主体重写为：模板解析 → 选 name → 没 --type 时让用户选 → 打印预览 → `collectPreAsks` → `collectFields` → `applyPostOverrides` → 清理 WorkspaceId 临时字段 → `providers.CreateWithEnv` 写盘；行数 < 60

## 5. cli 硬编码清零验证

- [x] 5.1 `grep -nE 'tpl\.Name == "aliyun-pay"|tpl\.Name == "minimax"' internal/cli/cli.go` 应返回空（确认硬编码全清）
- [x] 5.2 `grep -nE 'minimax|aliyun-pay' internal/cli/cli.go` 仅在 help 文本 / 注释处出现，不在条件分支里出现

## 6. 回归验证：cli_test.go 不变 + 全套绿

- [x] 6.1 `git diff internal/cli/cli_test.go` 应为空（确认测试代码零修改）
- [x] 6.2 `go test ./internal/cli/... -run 'TestGuidedFlow'` 全过：`_AliyunPay_SubstitutesWorkspaceId` / `_MiniMax_RegionCN` / `_MiniMax_RegionIO` / `_AllTypes_RoundTrip` / `_EOF_CancelsNoWrite` 5 个测试原样通过
- [x] 6.3 `go test ./...` 全包绿（cli + templates + collector + config + state + stats + provider + launcher + modes + tui + envutil + cmd）

## 7. Build + 跨平台编译验证

- [x] 7.1 `go build ./...` 通过
- [x] 7.2 `GOOS=darwin go build ./...` 与 `GOOS=windows go build ./...` 通过（与 v0.7.0 §7 同款验证清单）
- [x] 7.3 跑 `ccl provider --template minimax` 输出 JSON 含 `"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"`（与 v0.9.0 行为一致）
- [x] 7.4 跑 `ccl provider --template aliyun-pay` 输出 JSON 含 `"ANTHROPIC_BASE_URL": "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/apps/anthropic"`（占位符原样，与重构前一致）

## 8. 提交与归档

- [x] 8.1 `git add` + `git commit -m "refactor(templates): 把 aliyun-pay / minimax 特殊流程下移到 templates.PreAsks / PostOverrides / SkipPrompt"`
- [x] 8.2 跑 `openspec archive template-driven-preask` 归档（自动同步 `template-preask` capability 到 main specs）
- [x] 8.3 跑 `bump-version auto` 或手动 bump（如有用户可见的版本边界——本次纯 refactor 按 minor bump v0.10.0 处理）