## Context

`internal/launcher/launcher_windows.go` 当前用 `cmd.Run()` 同步 wait Claude Code 子进程：

```go
err = cmd.Run()
var exitErr *exec.ExitError
if errors.As(err, &exitErr) {
    os.Exit(exitErr.ExitCode())
}
if err != nil { return err }
os.Exit(0)
```

这是为**对齐 Unix `syscall.Exec` 语义**（替换进程 image + 透传 exit code）设计的——但 Windows 没有 exec syscall，只能用 `exec.Cmd` 子进程。

实际效果：ccl.exe 进程在 Claude Code 整个生命周期内**持续存在**（任务管理器里与 cmd.exe / node.exe / claude.exe 并列），stdio 同步共享给子进程。claude 退出时 `cmd.Run()` 返回，ccl 调 `os.Exit(exitCode)`。

**用户感知问题**：
- Unix 上 ccl.exe 进程 image 被 claude 替换 → 任务管理器"看 ccl 消失，claude 接管"
- Windows 上 ccl.exe 进程**仍在**（不是 image 替换，是父进程 wait）→ 任务管理器看到 4 个进程并列
- 用户期望 Windows 上 ccl 行为与 Unix 一致（ccl 消失）

**实际可行方案**：
- A. **detach**：`cmd.Start()` + 立即 `os.Exit(0)`——ccl.exe 立即退，claude 独立运行（cmd.exe → node.exe → claude.exe 三进程链，无 cc-launcher.exe）
- B. 现状：ccl.exe wait claude → ccl 退，claude 退出 code 透传给 PowerShell

## Goals / Non-Goals

**Goals:**
- Windows 上 ccl 行为与 Unix `syscall.Exec` **用户感知一致**：ccl 启动 claude 后立即退，claude 独立运行
- 任务管理器不再出现 cc-launcher.exe（与 claude 生命周期绑定的 ccl 进程）
- 保留 env 透传 + stdio 继承（与 Unix 一致）

**Non-Goals:**
- 不改 Unix 端（`launcher_unix.go` 仍是 `syscall.Exec` 同步替换）
- 不改 `provider-setenv-cwd-binding` 逻辑（cwd binding 在 runTUI 完成，与 launcher 行为正交）
- 不加 `--detach` flag（无用户场景需要 wait-then-pass——退出 code 透传对交互式 REPL 没意义）

## Decisions

### 1. `cmd.Run()` → `cmd.Start()` + `os.Exit(0)`

**决策**：

```go
err = cmd.Start()
if err != nil {
    return err
}
// 启动成功：立即退出 ccl，claude 独立运行（cmd.exe / node.exe / claude.exe 三进程链）
os.Exit(0)
return nil  // 满足编译器（os.Exit 是 no return）
```

**备选**：
- (A) `cmd.Run()` wait → `os.Exit(exitCode)`（现状）——拒绝：用户感知与 Unix 不一致
- (B) detach + 透传 exit code（用 goroutine wait 子进程后 `os.Exit(exitCode)`）——拒绝：ccl 进程在 wait 期间仍在任务管理器，没解决问题
- (C) detach + 输出 PID 给 PowerShell：`ccl & echo $LASTPID` —— 拒绝：增加复杂度、要求调用方改用 `&` 后台语法

**理由**：
- 用户原话："任务管理器里有个 cc-launcher.exe 的进程" + 选"改 detach"——明确要 ccl 立即消失
- exit code 透传对**交互式 REPL**（claude code）没意义：claude 退出 0 = 正常 `/exit`，claude 退出非 0 = crash/panic——两种情况 ccl 都该消失
- 与 Unix `syscall.Exec` 的 ccl 进程消失 + claude 接管 stdio 行为对齐（ccl 立即退 vs ccl 立即被替换——用户感知都是"ccl 没了"）

### 2. spawn 失败仍 `return err`

**决策**：`cmd.Start()` 返回 error（找不到 claude 二进制、权限拒绝等）仍 `return err` 给调用者（main.go 处理），ccl 自然退出（非 0 code）。**不** `os.Exit(0)`（掩盖错误）。

**理由**：保留诊断能力。`return err` 让 main 报错 + 退非 0，调用方（PowerShell / 脚本）能看到启动失败。

### 3. env 透传逻辑不变

**决策**：`configureCmd` hook（注入 `cmd.Env`）行为不变。env 仍是 launcher 包透传给 Claude Code 子进程的方式。

**理由**：env 透传是**与 launcher 行为正交**的 cross-cutting concern（spec `launcher-provider-env` / `provider-setenv-cwd-binding` 管），不因 detach 改 launcher 而变。

## Risks / Trade-offs

- [exit code 透传放弃] → `ccl && echo OK` 类 PowerShell 脚本在 claude 失败时误判成功。**用户接受**（用户选 detach 而非保留透传）。
- [claude 进程独立运行后 ccl 拿不到 PID] → 用户想 kill claude 需用 task manager / `Stop-Process -Name node`。**可接受**（与 Unix 行为一致：Unix 上 ccl 进程被替换后也不能 kill ccl，只能 kill claude）。
- [cmd.exe 中间层] → 仍存在（`cmd.exe /c claude.cmd`）——ccl 退后，cmd.exe 成为孤儿进程的祖父（cmd.exe 继承自 ccl 的 cgroup/PID namespace，但 Windows 没 cgroup）。**Windows 默认行为**：ccl 退后 cmd.exe / node.exe / claude.exe 由 Windows Service Control Manager 接管，无父进程。**可接受**（与 `npx claude &` 在 PowerShell 行为一致）。
- [stdio 行为] → claude code 仍用 ccl 继承的 stdin/stdout/stderr（启动时已分配）。ccl 退后这些 fd 仍由 cmd.exe 持有，claude 继续用。**可接受**（detach 不会打断已分配的 fd）。

## Migration Plan

- 无需数据迁移
- 无需 spec 之外的文档
- CHANGELOG `[Unreleased]` 段加 breaking change 条目

## Open Questions

- 现有 v0.11.0 用户的 PowerShell 脚本是否依赖 `ccl` exit code 透传？**没有用户报告**——安全改
- 是否需要在 README 警告"ccl 启动后立即退 0，claude exit code 不传给 ccl 调用者"？**值得**——加到 README 启动行为小节
