## Why

Windows 上 `ccl` 启动 Claude Code 后，cc-launcher.exe 进程在任务管理器里持续存在——ccl 同步等待 claude 进程退出再 `os.Exit`。这与 Unix 上 `syscall.Exec` 替换进程 image 的用户感知（ccl 消失，claude 接管）不一致：Windows 上 ccl 进程在 claude 整个生命周期内一直可见（任务管理器里 cc-launcher.exe / cmd.exe / node.exe / claude.exe 四进程并列），用户感知是"ccl 还在"。

实际是 `internal/launcher/launcher_windows.go:45` 用 `cmd.Run()` 同步 wait 子进程——ccl **逻辑上**等价 claude（stdio 继承、env 透传），但 Windows 上进程 image 不会被替换。改成 `cmd.Start()` 不 wait + `os.Exit(0)` 立即退出，让 ccl 进程**消失**（与 Unix 行为对齐），claude 独立运行。

代价：ccl 不再透传 claude exit code 到调用者（PowerShell 看到 ccl 返 0 即使 claude 返 1）。这与 Unix 上 `syscall.Exec` 行为更接近（`syscall.Exec` 成功也是 ccl 进程消失，claude 进程接管 exit code），用户感知一致。

## What Changes

- **修改** `internal/launcher/launcher_windows.go`：`cmd.Run()` → `cmd.Start()`，启动后立即 `os.Exit(0)`（不再 wait 也不透传 exit code）
- **修改** spec `cross-platform-launcher` 的 Windows launch Requirement：移除"on child exit, propagate the exit code via os.Exit(exitErr.ExitCode()) to mirror Unix syscall.Exec semantics"；新增 detach 语义
- **修改** spec `cross-platform-launcher` 的两个 Scenario："successful Launch on Windows does not return" + "child exits non-zero on Windows" 重写为 detach 语义
- **保留** spec Scenario "spawn itself fails on Windows"：`cmd.Start()` 失败仍 `return err`（不 os.Exit）
- **保留** spec Scenario "opts.Env is nil leaves cmd.Env unset on Windows" + "opts.Env is passed to cmd.Env on Windows"：env 透传逻辑不变

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `cross-platform-launcher`：Windows Launch 行为从"wait 子进程退出 + 透传 exit code"改为"启动后立即 os.Exit(0)，不 wait 子进程"。Unix 端行为不变（`syscall.Exec` 仍是同步替换进程 image）。

## Impact

- 代码：
  - `internal/launcher/launcher_windows.go`：`cmd.Run()` → `cmd.Start()`，删除 ExitError 处理 + `os.Exit(exitErr.ExitCode())`；保留 `return err` 路径处理 `cmd.Start()` 失败
  - 函数注释从"等待其结束后以相同退出码退出"改为"启动后立即 os.Exit(0)，与 Unix syscall.Exec 进程 image 替换的用户感知一致"
- spec：
  - `openspec/specs/cross-platform-launcher/spec.md`：Windows launch 段（含 5 个 Scenario）改写为 detach 语义；新增 Scenario "successful Launch on Windows exits ccl immediately" 替代原"does not return"；Scenario "child exits non-zero on Windows" 移除（ccl 不再 wait 子进程）
- 测试：
  - `internal/launcher/launcher_windows_test.go`：现有两个测试（`TestLaunch_Windows_EnvSetOnCmd` + `TestLaunch_Windows_NilEnvLeavesCmdEnvUnset`）用 `configureCmd` hook + panic 中断——**不**影响（hook 在 cmd.Run/Start 之前执行，只验证 cmd.Env 字段）
  - 跨平台编译验证：`GOOS=linux go build ./...` + `GOOS=windows go build ./...`（无 Linux 修改，但需确认 Linux 端没意外回归）
- 兼容性：
  - **BREAKING**（用户感知）：PowerShell 启动 `ccl` 后，claude 退出时 PowerShell 看到的 exit code 是 0（ccl 启动后立即 os.Exit(0)），不再是 claude 真实 exit code
  - **BREAKING**（shell 行为）：写 `ccl && echo "claude exited OK"` 的 PowerShell 脚本，claude 失败时 `ccl` 仍返 0 → 误判成功
  - **符合用户预期**：任务管理器不再看到 cc-launcher.exe（ccl 立即退），claude 独立运行（cmd.exe / node.exe / claude.exe 三进程链）
  - 与 Unix 行为对齐：Unix 上 ccl 进程被 claude 替换，PowerShell 看到的是 claude exit code——Windows 上 ccl 立即退 0，claude 独立运行，claude 自己的 exit 由其父进程（cmd.exe → PowerShell）传递，等价语义
