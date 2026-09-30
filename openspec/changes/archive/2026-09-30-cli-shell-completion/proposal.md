## Why

`ccl` 的子命令接口已经复杂化（`ccl serve` 现在有 4 个互斥 flag：`--stop / --restore / --status / --restart`），用户每次调子命令都靠记忆或 `ccl serve --help` 翻文档——shell tab 补全是工业惯例，能把"先打 `--help` 再调"压缩成"打 `--s<Tab>` 直接补全"。

实现走 `kubectl` / `gh` / `docker` 模式：ccl 内置 `__complete` 隐藏子命令（动态返回当前位置候选）+ `__init <shell>` 隐藏子命令（输出 wrapper script）。用户一次 `source <(ccl __init bash)` 完成启用，零外部依赖，flag 改动自动跟随。

## What Changes

- **新增** `ccl __complete <args...>` 隐藏子命令（不在 `topHelp` 子命令列表里）：根据当前 args 解析语境，输出当前位置候选（每行一个 token）到 stdout。这是 shell completion 引擎的"事实标准"协议。
  - 顶层 `ccl <TAB>` → `serve / stats / provider / mode / -h / --help / help`
  - `ccl serve <TAB>` → `--stop / --restore / --status / --restart / -h / --help`
  - `ccl provider <TAB>` → `--list / --new / --delete / --template / -T / -h / --help`
  - `ccl provider --template <TAB>` → `anthropic / deepseek / minimax / glm / aliyun-coding / aliyun-pay / aliyun-token`（从 `internal/templates` 提取）
  - `ccl provider --new --type <TAB>` → 同上模板名
  - `ccl stats <TAB>` → `--since / --by-provider / --by-project / --project / --json / -h / --help`
  - `ccl provider <name>` 位置 → 不补（provider name 是用户文件，shell 文件名补全接管）
  - `ccl provider --delete <name>` 位置 → 不补（同上）
- **新增** `ccl __init <shell>` 隐藏子命令：`shell ∈ {bash, zsh}`，stdout 输出 wrapper script（~30 行 shell），调 `complete -C 'ccl __complete' ccl` (bash) 或 `compdef _ccl ccl` (zsh)。
- **修改** `topHelp` 顶部 help：新增"Tab completion"段落，说明 `source <(ccl __init bash)` / `source <(ccl __init zsh)` 启用方式。
- **新增** smoke test：调 `ccl __complete ccl ""`（位置参数用空字符串表示待补全）验证候选集。
- **BREAKING**：无。`__complete` / `__init` 是隐藏子命令，不影响现有 `ccl` 接口。
- **不引入**：fish（先跳过）、powershell（ccl serve 在 Windows 上 build tag 隔离，completion 复杂度翻倍）、cobra / urfave-cli（破坏零依赖原则）。

## Capabilities

### New Capabilities
- `cli-shell-completion`：ccl 的 shell tab completion 协议（`__complete` 隐藏子命令）+ 用户侧启用脚本生成（`__init <shell>`）。覆盖 bash + zsh。

### Modified Capabilities
- 采纳 `2026-09-30-provider-flag-migration` 已归档 change 成果作为本 change 的实施前置：`internal/cli/cli.go::registerProviderFlags`（已实现于 `cli.go:105-141`）已被 `runProvider` 调用，本 change 不重写 provider flag 注册代码，直接消费其返回值作为 `completeAtProvider` 的 flag 候选源。
- 现有 `cli-help` capability 仍只管 `ccl --help` 文案输出，跟 shell completion 是独立维度——`topHelp` 段落变更属新增内容，不算修改 spec。

## Impact

- 代码：
  - `cmd/cc-launcher/completion.go`（新增，~150 行）：`runCompletion(args)` + `runShellInit(shell)` + 候选生成（顶层/serve/stats/provider/mode 各自的 flag 集合）
  - `cmd/cc-launcher/main.go`：路由表 `run` 顶部 switch 加 `case "__complete"` / `case "__init"` 两个分支（紧跟现有 `-h/--help/help` 块，但不进 `topHelp` 子命令列表）
  - `cmd/cc-launcher/main.go`：`topHelp` 子命令列表保持不变（不暴露 `__complete` / `__init`），仅顶部说明加"启用 tab completion"段落
  - `internal/templates`（仅消费，不改）：模板名列表通过已公开 `var All []Template` 直接遍历（`templates.go:37`，7 项均为公开 `Name string`）
  - `internal/cli/cli.go` 复用现有 `registerProviderFlags(fs *flag.FlagSet) (list, new, delete, template *bool, typeFlag *string, help *bool)`（已实现于 `cli.go:105-141`）。`completeAtProvider` 直接消费其返回值。**实施时**需将 `registerProviderFlags` 改名为 `RegisterProviderFlags`（exported）以便 `cmd/cc-launcher/completion.go` 跨包调用——这是包边界最小改动，注释里"cli-shell-completion 兼容性"的承诺
  - **Out of scope**：本 change 不重写 `registerProviderFlags` 也不重构 provider runtime flag 解析；已采纳 `2026-09-30-provider-flag-migration` 已归档 change 成果。`runProvider` 已迁到 `flag.NewFlagSet`，与 `registerServeFlags` / `registerStatsFlags`（仍 inline 注册）走不同路径
- 文档：
  - README 新增"Tab completion"小节
  - `topHelp` 顶部说明加一段
- 测试：smoke test 覆盖 `ccl __complete ccl ""` 等关键位置
- 依赖：零
- 跨平台：bash/zsh 脚本不跨 Windows；Windows 用户用现有 `ccl --help`（隐藏子命令在 Windows 上也存在，只是用户不能 source——可接受，ccl 在 Windows 上覆盖率本来就低）
- archive 目录：不动