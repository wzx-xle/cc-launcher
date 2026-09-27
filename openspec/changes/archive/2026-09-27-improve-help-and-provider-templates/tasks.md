## 1. 模板基础（前置依赖）

- [ ] 1.1 新增 `internal/templates/templates.go`：定义 `Template`、`Hint`、`TemplateField` 类型、`All []Template`、`ByName(name) (Template, bool)`、7 个内置模板（anthropic / deepseek / minimax / glm / aliyun-coding / aliyun-pay / aliyun-token）；每个模板包含 `Name` / `Description` / `Body` (raw JSON string) / `Hints []Hint` / `Fields []TemplateField`（按交互步骤顺序，每个 Field 含 Key/Prompt/Default/Required/Secret/Placeholder）
- [ ] 1.2 `internal/templates/templates.go`：每个非 anthropic 模板上方必须有 `// verified 2026-09-27: <URL>` 注释，记录 base_url 和默认 model 的来源 URL
- [ ] 1.3 `internal/templates/templates_test.go`：单测校验 7 个 `Name` 非空、7 个 `Description` 非空、7 个 `Body` 都能 `json.Unmarshal` 成 `map[string]any`、每个非 anthropic 模板的 `env` 含必需 key（`ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` / 3 个 `ANTHROPIC_DEFAULT_*_MODEL`）、`ANTHROPIC_BASE_URL` 必须以 `/anthropic` 或 `/apps/anthropic` 结尾、`ByName("bogus")` 返回 ok=false

## 2. --help 旗标

- [ ] 2.1 `cmd/cc-launcher/main.go`：在 `run()` 顶部识别 `-h` / `--help` / `help` 并打印顶层帮助，退出码 0
- [ ] 2.2 `internal/cli/cli.go` `Run()`：识别 `args[0] == "help"` 或 `-h` / `--help`，打印顶层帮助
- [ ] 2.3 `internal/cli/cli.go` `runProvider()`：在 `args[0]` 是 `-h` / `--help` 时打印 provider 帮助（含 `--list` / `--new` / `--delete` / `--template` 说明），退出码 0
- [ ] 2.4 `internal/cli/cli.go` `runMode()`：在 `args[0]` 是 `-h` / `--help` 时打印 mode 帮助，退出码 0
- [ ] 2.5 帮助文本提到 `ccl provider --template` 时同时给出示例调用（`ccl provider --template deepseek`），引导用户查模板

## 3. --template 子命令（短 -T）

- [ ] 3.1 `internal/cli/cli.go` `runProvider()`：识别 `--template` 与短别名 `-T` 旗标
  - 无后续参数：打印 `templates.All` 的 `Name` + `Description` 列表
  - 后续参数为类型名：调 `templates.ByName` 找到模板 → 打印 `Body` 到 stdout → 退出 0
  - 类型名无效：返回错误信息列出支持的类型，退出非 0
- [ ] 3.2 `internal/cli/cli.go` `runProvider()`：允许 `--template` 与 `--new` 组合（`ccl provider --new --template <type>`）行为同 `--template`，不进入交互流
- [ ] 3.3 `internal/cli/cli_test.go`：用 `bytes.Buffer` 替换 stdout 验证 `--template` / `--template deepseek` / `--template bogus` 三种路径

## 4. 引导式交互流

- [ ] 4.1 `internal/cli/cli.go` `newProvider()`：新增 `--type <type>` 旗标（接受 7 个值）；在 type 缺失时打印 `templates.All` 列表 + 让用户选（保留原 stdin 读取）
- [ ] 4.2 `internal/cli/cli.go` `newProvider()`：交互流遍历 `template.Fields`，每步 prompt 前打印对应 `templates.Hint.Prompt`；type 已知时跳过 "select template" 步骤
- [ ] 4.3 `internal/cli/cli.go` `newProvider()`：`Secret: true` 的字段（`ANTHROPIC_AUTH_TOKEN`）不回显（term.MakeRaw 或类似机制）
- [ ] 4.4 `internal/cli/cli.go` `newProvider()`：aliyun-pay 在 BASE_URL 前先问 WorkspaceId；写盘前对 `ANTHROPIC_BASE_URL` 做 `{WorkspaceId}` 占位符替换校验（确保不残留）
- [ ] 4.5 `internal/cli/cli.go` `newProvider()`：minimax 在 BASE_URL 前先问 国内/国际 选择
- [ ] 4.6 `internal/cli/cli.go` `newProvider()`：EOF 取消路径保持不变；新增 test 覆盖 EOF 取消 + 不写盘
- [ ] 4.7 写入文件后追加提示 "已写入 <path>，可手工编辑补充其它字段"

## 5. 测试

- [ ] 5.1 `internal/cli/cli_test.go`（新增）：`--help` 三层退出 0 + 内容包含关键字
- [ ] 5.2 `internal/cli/cli_test.go`：`--template` 三个分支（无参 / 有效 / 无效），覆盖 7 个 type 的 `--template <type>` 都返回有效 JSON
- [ ] 5.3 `internal/cli/cli_test.go`：`provider --new --type bogus` 退出非 0 且不创建文件
- [ ] 5.4 `internal/cli/cli_test.go`：用 `t.TempDir()` 模拟 `~/.cc-launcher/providers/`，跑完整 guided 流程（覆盖 anthropic/deepseek/minimax/glm/aliyun-coding/aliyun-pay/aliyun-token 全部 7 个 type）后用 `provider.Manager.Get(name)` 验证文件可解析
- [ ] 5.5 `internal/cli/cli_test.go`：aliyun-pay 跑完后断言 `ANTHROPIC_BASE_URL` 不含 `{WorkspaceId}` 字面量
- [ ] 5.6 `internal/cli/cli_test.go`：minimax 国内/国际 二选一行为验证

## 6. 文档

- [ ] 6.1 `README.md`：在"用法"与"提供商配置"之间新增"用模板配置提供商"小节，列出 4 种类型、`ccl provider --template` 用法、以及 `ccl provider --new --type <t>` 工作流
- [ ] 6.2 `README.md`：把"用法"表格补充 `--help` / `--template` 行

## 7. 验证

- [ ] 7.1 `go build ./...` + `GOOS=windows go build ./...`
- [ ] 7.2 `go vet ./...` 无警告
- [ ] 7.3 `go test ./...` 全绿（含新增 cli_test.go）
- [ ] 7.4 `go test -tags contract ./...` 全绿（不影响现有契约，但跑一遍确认无回归）
- [ ] 7.5 手动跑一次 `ccl --help` / `ccl provider --template deepseek` / `ccl provider --new --type deepseek`（非交互场景可借助 `expect` 或 echo 喂 stdin）

## 8. 提交

- [ ] 8.1 commit: feat(templates) — 新增 internal/templates 包（Template + Hint + TemplateField）与 7 个内置模板，每个含 verified 注释
- [ ] 8.2 commit: feat(cli): --help 旗标（顶层/provider/mode）
- [ ] 8.3 commit: feat(cli): provider --template 子命令
- [ ] 8.4 commit: feat(cli): provider --new --type 引导式交互流（含 WorkspaceId/region 子流程）
- [ ] 8.5 commit: test(cli): 覆盖 --help / --template / guided flow / 7-type round-trip
- [ ] 8.6 commit: docs — README 增加"用模板配置提供商"小节（按 7 个 type 列表）