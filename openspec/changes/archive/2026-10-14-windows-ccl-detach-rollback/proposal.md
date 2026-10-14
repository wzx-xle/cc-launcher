## Why

v0.11.1 引入的 Windows detach 设计（`9f9c110 feat(launcher): Windows ccl 启动 Claude Code 后立即退出 (detach)`）让 `ccl.exe` 启动 Claude Code 后立即 `os.Exit(0)`，ccl 进程从任务管理器消失。该设计在 Windows Terminal 下直接破坏 Claude Code 的全部交互能力：

- 现象：`ccl` 启动后 Claude Code 的 TUI **能正常渲染**（输出走 console output queue 不受影响），但**收不到任何键盘事件**，连 `Ctrl+C` 也无效（按 Ctrl+C 不会终止 Claude Code）
- 复现：v0.11.2 + Windows Terminal + claude.exe (WinGet 安装) + PowerShell；用户在 v0.11.0 一切正常
- 控制组验证：直接在 PowerShell 跑 `claude.exe --permission-mode plan`（不经 ccl）输入完全正常
- 隔离验证：唯一 launcher 行为改动 = `9f9c110`；v0.11.0 → v0.11.2 之间 build / CI / docs 改动不影响运行时 launcher 行为

**根因**：ccl.exe 立即 `os.Exit(0)` → PowerShell `$?` 立刻拿到 0 → PowerShell prompt 立即回到 interactive read 状态 → **PowerShell 抢在 Claude Code 初始化之前从 console input queue 读 keyboard events** → Claude Code 终于进入 read state 时 input queue 已被 PowerShell 截走 → 永远等不到输入。Ctrl+C 同路径失效。

**这是 Windows console 模型的根本限制，不是代码 bug**：Windows console input 是 kernel-managed 的**结构化事件流**（不是 Unix 那种 generic byte stream），属于具体 console，read 是 atomic + blocking。在 PowerShell prompt 抢到 input 后 Claude Code 没机会再读。**ConPTY 看起来是 Windows 为此设计的方案，但 ConPTY 要求 parent 进程持续 pump 数据，等价于 wait 模式**，无法做到"既 detach 又功能正常"。

## What Changes

- **撤销** `internal/launcher/launcher_windows.go` 的 detach 改动：恢复 v0.11.0 之前的 `cmd.Run()` 同步 wait + `os.Exit(exitErr.ExitCode())` 透传 exit code
- **修改** `openspec/specs/cross-platform-launcher/spec.md` 的 "Windows Launch" Requirement：删掉 detach 段（"After cmd.Start() succeeds, call os.Exit(0) immediately…"），恢复 "On child exit, propagate the exit code via os.Exit(exitErr.ExitCode()) to mirror Unix syscall.Exec semantics" 段；Scenario 同步从 detach 语义改回 wait 语义（"successful Launch on Windows does not return" + "child exits non-zero on Windows" + "spawn itself fails on Windows"）
- **保留** 其他 Scenario：launch package 编译、resolveClaude 行为、env 透传、`.cmd`/`.bat` 经 `cmd.exe /c` 启动、opts.Env nil / non-nil 设置
- **保留** Unix 端行为不变（`launcher_unix.go` 仍用 `syscall.Exec` 同步替换进程 image）

archive 的 `2026-10-13-windows-ccl-detach-after-launch` 不动 —— archive 是历史记录，spec 现在的状态以 archive 时的 `specs/cross-platform-launcher/spec.md` 为准；本次 change 让 archive 那次改的 spec 段"反向生效"，让 `openspec/specs/cross-platform-launcher/spec.md` 的 Windows 段恢复成 archive 之前的样子。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `cross-platform-launcher`：Windows Launch 行为从 "detach（cmd.Start() + 立即 os.Exit(0)）" 改回 "wait + 透传 exit code（cmd.Run() + os.Exit(exitErr.ExitCode())）"。Unix 端行为不变。

## Impact

- 代码：
  - `internal/launcher/launcher_windows.go`：`cmd.Start() + os.Exit(0)` → `cmd.Run() + ExitError 透传 + os.Exit(0)`；恢复 `errors.As` + `*exec.ExitError` 处理；恢复 `errors` import；configureCmd 注释从 "cmd.Start() 之前" 改回 "cmd.Run() 之前"；函数 doc 注释从 "启动后立即退出" 改回 "启动子进程、继承当前终端的 stdio，等待其结束后以相同退出码退出"
  - **该 commit 字面等价 v0.11.0 之前的 `internal/launcher/launcher_windows.go`**，可通过 `git show 9f9c110~1:internal/launcher/launcher_windows.go` 对照
- spec：
  - `openspec/specs/cross-platform-launcher/spec.md`：Windows launch 段（含 7 个 Scenario）从 detach 语义改回 wait 语义（**字面等价 archive 之前的状态**，通过 `git show 9f9c110~1:openspec/specs/cross-platform-launcher/spec.md` 对照）
- 测试：
  - `internal/launcher/launcher_windows_test.go`：现有两个测试（`TestLaunch_Windows_EnvSetOnCmd` + `TestLaunch_Windows_NilEnvLeavesCmdEnvUnset`）用 `configureCmd` hook + panic 中断 cmd.Run/Start（参数 cmd.StopFromCmdRun）—— **不**影响（hook 行为与 Run/Start 正交，只验证 cmd.Env 字段）
  - 跨平台编译验证：`GOOS=linux go build ./...` + `GOOS=windows go build ./...`
- 兼容性：
  - 任务管理器中 `cc-launcher.exe` 重新出现于 Claude Code 整个生命周期（v0.11.0 之前就是这个状态）
  - **`ccl && echo OK` 类 PowerShell 脚本能正确拿到 Claude Code exit code**（这是 v0.11.1 detach 放弃的 trade-off，现在恢复）
  - **无 BREAKING 变更**（恢复到 v0.11.0 行为，v0.11.0 是已知正常工作的版本）