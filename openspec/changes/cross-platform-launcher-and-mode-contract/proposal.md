## Why

`cc-launcher` 当前 `internal/launcher` 只在 Unix 上工作（`syscall.Exec`），无法在 Windows 上替换进程；此外，`internal/cli.ValidModes` 与 `internal/tui.modeItems` 是两份独立维护的列表，历史已经出现过只改一处导致的漂移，且二者都没有跟 Claude Code CLI 自身的 `--permission-mode` choices 同步校验的机制。本次改动解决这两个问题。

## What Changes

- **新增 `internal/launcher/launcher_unix.go`（`//go:build !windows`）与 `internal/launcher/launcher_windows.go`（`//go:build windows`）**。把 `Launch` 拆成跨平台的 `resolveClaude(opts) (path, args, err)`（留在 `launcher.go`）和平台特定的 `Launch(opts) error`。
- **Windows 实现**：没有 `exec` 系统调用，改为 spawn 子进程、继承 stdio；为 npm 全局安装生成的 `claude.cmd/.bat` 走 `cmd /c` 启动；`os.Exit` 透传子进程退出码，模拟 Unix `syscall.Exec` 成功时不返回的语义。
- **导出 `cli.ValidModes`**，让其他包可以引用。
- **新增 build tag `contract` 的契约测试** `internal/cli/permission_contract_test.go`：执行本机 `claude --help`，解析 `(choices: ...)` 段，与 `cli.ValidModes` 比对。本机无 `claude` 时自动 SKIP。修复位置同时标注 CLI 和 TUI 两份列表。
- **新增常规测试** `internal/tui/modes_test.go`：锁死 TUI `modeItems` 的 mode 集合与 `cli.ValidModes` 排序后一致，并校验每个 mode 都有非空中文描述。
- **修正 `modeItems` 的中文描述**：旧描述把 `dontAsk` 写成"不询问，直接执行"会误导（实际是最严格模式、未预授权则直接拒绝）；同步更新 README 的权限模式表。
- **`build.sh`**：新增 `contract` / `windows` / `release` 子命令；`clean` 同步清理 `dist/`。

## Capabilities

### New Capabilities
- `permission-mode-contract`: 维护"项目硬编码的权限模式列表"和"Claude Code CLI 实际接受的 choices"之间的契约，由双重测试（TUI↔CLI 一致锁、CLI↔Claude CLI 契约锁）保证。
- `cross-platform-launcher`: 在 Unix / Windows 上以一致的用户体验启动 Claude Code；Windows 通过 spawn 子进程 + 透传退出码模拟 `syscall.Exec` 的语义。

### Modified Capabilities
<!-- 无：这是第一个 change，没有历史 spec。 -->

## Impact

- 代码：`internal/launcher/`（拆分 + 新增 Windows 文件）、`internal/cli/cli.go`（导出 ValidModes）、`internal/tui/tui.go`（描述修正）、`internal/cli/permission_contract_test.go`（新增）、`internal/tui/modes_test.go`（新增）。
- 构建：`build.sh` 新增 3 个子命令；新增 `.gitignore` 条目 `/dist/`。
- 文档：`README.md` 权限模式表与契约测试说明。
- 外部依赖：无新增；契约测试需要本机已安装 `claude`（未安装时自动 SKIP）。
- 兼容性：Unix 行为不变（仍是 `syscall.Exec`）；Windows 是新增平台。