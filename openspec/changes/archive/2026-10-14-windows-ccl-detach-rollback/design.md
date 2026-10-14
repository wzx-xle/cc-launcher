## Context

`internal/launcher/launcher_windows.go` 在 v0.11.0 之前用 `cmd.Run()` 同步 wait 子进程：

```go
err = cmd.Run()
var exitErr *exec.ExitError
if errors.As(err, &exitErr) {
    os.Exit(exitErr.ExitCode())
}
if err != nil {
    return err
}
os.Exit(0)
```

v0.11.1（commit `9f9c110 feat(launcher): Windows ccl 启动 Claude Code 后立即退出 (detach)`）改为：

```go
if err := cmd.Start(); err != nil {
    return err
}
os.Exit(0)
return nil
```

v0.11.0 之前的代码是 **Windows console 模型下的标准做法**：ccl.exe 启动 Claude Code 后**同步 wait**，等 Claude Code 退出再 `os.Exit(exitErr.ExitCode())` 透传 exit code。stdio 同步共享给子进程（child inherit ccl.exe 的 os.Stdin/Stdout/Stderr）。

**v0.11.0 → v0.11.1 的设计动机**（archive `2026-10-13-windows-ccl-detach-after-launch/proposal.md` Why 段）：
> "Windows 上 `ccl` 启动 Claude Code 后，cc-launcher.exe 进程在任务管理器里持续存在——ccl 同步等待 claude 进程退出再 `os.Exit`。这与 Unix 上 `syscall.Exec` 替换进程 image 的用户感知（ccl 消失，claude 接管）不一致：Windows 上 ccl 进程在 claude 整个生命周期内一直可见（任务管理器里 cc-launcher.exe / cmd.exe / node.exe / claude.exe 四进程并列），用户感知是'ccl 还在'。"

设计目标是让 ccl.exe 启动 Claude Code 后立即退 0，ccl 进程从任务管理器消失，**对齐 Unix 上 `syscall.Exec` 替换进程 image 的用户感知**。

## 根因（v0.11.1 detach 引入的 bug）

v0.11.1 detach 引入的功能回归（v0.11.0 一切正常 → v0.11.1 收不到键盘事件）根因：

**Windows console input 是 kernel-managed 的结构化事件流，不是 Unix 那种 generic byte stream**。它属于具体 console，由 kernel 在 foreground process 持有该 console 时把 keystroke 投递到 console input queue；`ReadConsoleInput` 是 atomic + blocking —— 一次只 read 一个 event，event 出队。

ccl.exe 立即 `os.Exit(0)` 之后：
1. ccl.exe 进程销毁（PID 没了）
2. **ccl.exe 是 console 关联进程之一**（跟 Claude Code 在同一 console）
3. PowerShell 启动 ccl.exe 后一直在 wait ccl.exe 退出 —— ccl 退 0 → PowerShell 立刻拿到 `$?`=0 → PowerShell prompt 立即进入 interactive read 状态
4. **PowerShell prompt 立即调 `ReadConsoleInput` 等下一个命令**（PowerShell 启动 ccl 之前已经持有 console input 的 read 句柄，回到 prompt 立即 active）
5. Claude Code（node.js + Ink TUI）还在初始化（启动 node.js、加载 Claude Code、初始化 Ink，需要几百毫秒到几秒）
6. Claude Code 终于进入 read state，但 console input queue 已经被 PowerShell 抢走
7. **用户按键 → PowerShell 第一个拿到 → 把字符合并到自己的 line buffer（甚至回显到 PS> 后面）**，Claude Code 永远等不到
8. **Ctrl+C 同路径失效**：Ctrl+C 是 console event 走同一路径，被 PowerShell 截走

这解释了所有症状：
- Claude Code TUI **能渲染**（output 走 console output queue，**不依赖** input queue 的 ownership）
- Claude Code **收不到键盘事件**（input queue 被 PowerShell 抢走）
- **Ctrl+C 无效**（同路径）
- **直接 `claude` 能用**（那时 PowerShell prompt 在 wait Claude Code 退出，没回到 interactive read）
- **v0.11.0 OK / v0.11.1 挂**（v0.11.1 引入 detach）

## Goals / Non-Goals

**Goals:**
- 恢复 Windows 上 ccl 启动 Claude Code 后的键盘交互能力（v0.11.0 之前的行为）
- Claude Code 的 exit code 重新透传给 ccl 调用者（PowerShell `$LASTEXITCODE`）
- 保留 `provider-setenv-cwd-binding` 的 env 透传逻辑（不动 `cmd.Env` 设置、`launcher-provider-env` spec）

**Non-Goals:**
- 不尝试"既 detach 又功能正常"的折中（已论证不存在，详见 Decision 1）
- 不改 Unix 端（`launcher_unix.go` 仍用 `syscall.Exec`）
- 不改 `provider-setenv-cwd-binding`、`cross-platform-launcher` 的 Unix launch 部分
- 不改 `cmd.Run() → cmd.Start() + os.Exit(0)` 之外的 launcher 行为
- 不引入 ConPTY（详见 Decision 2）

## Decisions

### Decision 1：撤销 detach，恢复 `cmd.Run()` wait + 透传 exit code

**决策**：把 `internal/launcher/launcher_windows.go` 的 `cmd.Start() + os.Exit(0)` 改回 `cmd.Run() + ExitError 透传 + os.Exit(0)`（字面等价 v0.11.0 之前的代码）。

**为什么是唯一可行方案**（候选对比）：

| 方案 | 任务管理器 ccl.exe | Claude Code 输入 | 复杂度 | 评估 |
|---|---|---|---|---|
| **A. 撤销 detach**（本 change） | 在（与 v0.11.0 一致） | 通 | 低 | **采纳** |
| **B. ConPTY pump** | 在 | 通 | 高 | ❌ 见 Decision 2 |
| **C. 保留 detach** | 不在 | **永远不通** | 低 | ❌ 不可修复 |

**B 不接受的理由**：ConPTY（Windows Pseudo Console）是微软为"parent process 想 spawn console child 并自己 pump 数据"设计的。`CreatePseudoConsole` 创建伪 console，`child attach via PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`，parent 通过 input/output pipe 与伪 console 通信。**但 ConPTY 要求 parent 持续 pump 数据**（input pipe ↔ 伪 console input queue 的转发桥）—— 跟 wait 模式本质相同：ccl.exe 必须存活直到 Claude Code 退出，任务管理器照样有 ccl.exe。**没有任何收益**（ccl.exe 本身就是 PowerShell 的 child，已 attach 到 PowerShell 的 console，不需要再 detached + ConPTY 重新连接）。徒增依赖（`microsoft/go-winconpty` 或自己绑 syscall）+ 增加 ccl.exe 维护负担。

### Decision 2：不引入 ConPTY

详见 Decision 1 表格。**ConPTY 在 ccl 场景下是 over-engineering，没有收益**（跟 wait 模式等价但更复杂）。

### Decision 3：保留 `cmd.Run()` wait 的所有 spec Scenario 文本

v0.11.0 之前的 Scenario 文本：

```markdown
#### Scenario: successful Launch on Windows does not return
- **WHEN** the child process exits with code 0
- **THEN** `Launch` calls `os.Exit(0)` and does not return

#### Scenario: child exits non-zero on Windows
- **WHEN** the child process exits with a non-zero code
- **THEN** `Launch` calls `os.Exit(child.ExitCode())` and does not return

#### Scenario: spawn itself fails on Windows
- **WHEN** `cmd.Run()` returns an error that is not an `*exec.ExitError`
- **THEN** `Launch` returns the error to the caller (no `os.Exit`)
```

恢复这些 Scenario（字面等价 v0.11.0 之前的 spec）。

### Decision 4：不改 archive 的 `2026-10-13-windows-ccl-detach-after-launch`

archive 是历史快照，不动。archive 的 `specs/cross-platform-launcher/spec.md` 是 v0.11.1 那次 change 引入 Windows 字样的快照 —— 本次 change 让 `openspec/specs/cross-platform-launcher/spec.md` 恢复到 archive 之前的状态（与 archive 内容上"反向"，但 archive 不动）。

## Risks / Trade-offs

- [任务管理器有 ccl.exe] → 用户在 v0.11.1 之前**先验已知**（v0.11.0 一直这样），可接受
- [`ccl && echo OK` 类 PowerShell 脚本能拿到 Claude Code exit code] → 跟 v0.11.0 一致
- [无 BREAKING 变更] → 恢复到 v0.11.0 行为（v0.11.0 是已知正常版本）

## Migration Plan

- 无需数据迁移
- 无需配置迁移
- CHANGELOG `[Unreleased]` 段加 revert 条目
- v0.11.3 patch release（bug fix，符合 semver）

## Open Questions

无。