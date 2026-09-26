## Context

`internal/launcher.Launch(opts)` 当前用 `syscall.Exec` 替换当前进程启动 Claude Code，仅 Unix 可用；权限模式列表 `cli.validModes` 与 `tui.modeItems` 是两份独立维护的硬编码，与 Claude Code CLI 自身的 `--permission-mode` choices 之间没有自动同步校验。

## Goals / Non-Goals

**Goals:**
- 让 cc-launcher 在 Windows 上以与 Unix 等价的用户体验启动 Claude Code
- 引入双重契约测试，阻止权限模式列表的漂移
- 修正 TUI 权限模式的中文描述（特别是 `dontAsk`）

**Non-Goals:**
- 不为 Windows 实现 `syscall.Exec` 完全等价的语义（不可能，且无意义）；用"spawn + 透传退出码"近似即可
- 不引入新的依赖（如 `sys`、第三方 PTY 库）
- 不改 Claude Code CLI 本身

## Decisions

### 1. 把 `Launch` 拆成跨平台 `resolveClaude` + 平台特定 `Launch`

`resolveClaude(opts) (claudePath string, args []string, err error)` 放在无 build tag 的 `launcher.go`，负责查找 `claude` 与组装参数（不含程序名）。`Launch` 由平台文件提供。

**理由**：参数组装是纯逻辑，与平台无关；只有"如何把控制权交给 claude"是平台相关的。Go 的文件级 build tag 让拆分零成本。

**备选**：把 `resolveClaude` 也放到 `_unix.go` / `_windows.go` 并重复实现 → 不接受，参数组装完全一样，重复是负担。

### 2. Windows Launch 通过 `exec.Cmd` + `os.Exit` 模拟 syscall.Exec 语义

```go
cmd.Stdin/Stdout/Stderr = os.Stdin/Stdout/Stderr
err := cmd.Run()
if errors.As(err, &exitErr) { os.Exit(exitErr.ExitCode()) }
if err != nil              { return err }
os.Exit(0)
return nil  // 让 Go 编译器满意（os.Exit 不做控制流分析）
```

**理由**：与 Unix 上"成功后进程被替换、失败时返回 error"的语义一致；调用方不需要区分平台写分支。

**备选**：用 `cmd.Start()` + 父进程等待 + 透传 exit code → 等价但 `cmd.Run()` 已经做了这件事，没必要重复。

### 3. Windows 上为 `.cmd/.bat` 走 `cmd /c`

`exec.LookPath("claude")` 在 PATH 中找到 npm 全局安装的 `claude.cmd` 时返回的是带 `.cmd` 后缀的路径；Windows `CreateProcess` 不能直接执行批处理，必须经 `cmd.exe /c` 启动。判断逻辑：路径以 `.cmd` 或 `.bat` 结尾（小写）→ 走 `exec.Command("cmd", "/c", path, args...)`。

**理由**：这是 npm 安装路径的硬性约束（社区标准方案）。

**备选**：检测不到 `.cmd` 时也强制走 `cmd /c` → 不接受，会把原生 `.exe`（scoop/winget/手动安装）搞复杂，且对性能/兼容性无益。

### 4. 契约测试用 build tag `contract` 隔离

`internal/cli/permission_contract_test.go` 加 `//go:build contract`，不参与常规 `go test ./...`。需要时通过 `go test -tags contract ./... -run ^TestContract_` 触发。

**理由**：(a) 避免常规 CI 依赖 `claude` 命令存在；(b) 契约测试是慢测试（启动子进程），与单元测试隔离便于不同节奏；(c) build tag 是 Go 工具链一等公民，无需第三方插件。

**备选**：用环境变量门控 → 不接受，build tag 更明确，且 IDE/test runner 友好。

### 5. 契约测试解析 `claude --help` 输出

help 输出按终端宽度折行；`--permission-mode` 选项的描述可能跨多行，并以 `(choices: "foo", "bar", ...)` 收尾。算法：
1. 找到以 `--permission-mode` 开头的行
2. 把后续非"以 `--` / `-x,` 开头"的行追加到同一段（终止续行收集）
3. 在段内定位 `(choices:` 和最近的 `)`，提取引号里的值

**理由**：Claude Code CLI 当前的 help 格式就是这个；写测试时已实测可解析。

**风险**：help 格式变化（`choices:` 改名 / 改为结构化输出）→ 测试会假阳性。缓解：测试报错信息打印完整的 `--permission-mode` 段，便于快速定位格式变化。

### 6. TUI / CLI 一致锁放在常规测试

`internal/tui/modes_test.go` 直接 `import "cc-launcher/internal/cli"`，把 `modeItems` 与 `cli.ValidModes` 排序后比对。

**理由**：最直接的 lock；不引入共享 internal 包的成本最小。tui 包本来就没有 cli 反向依赖，无循环。

**未来重构**（不在本次范围）：把 `ValidModes` 抽到 `internal/modes` 包，让 tui 不直接依赖 cli。

### 7. 修正 mode 描述

旧描述把 `dontAsk` 写成"不询问，直接执行"严重误导——实际是**最严格**的非交互模式（未被 allow 规则预授权则直接拒绝），适合 CI 而非"自动放行"。同步修正 `auto`（模型分类器判定）、`bypassPermissions`（危险）、`plan`（只读）等描述。

## Risks / Trade-offs

- [Windows 退出码透传不可见] 在 Windows 上 cmd.Run() 的 exit code 与 Unix syscall.Exec 的行为不完全对齐（Windows 的 ExitError.ExitCode() 可能不返回 Unix 信号导致的退出码）→ 不解决：cc-launcher 的目标是用户体验一致，不深入 Windows 进程信号语义。
- [契约测试脆性] 解析 `claude --help` 文本是脆性契约 → 缓解：测试报错信息友好，定位快；如未来 Claude Code 提供 `--print-schema` 可切换。
- [modeItems 跨包依赖 tui → cli] 短期可接受，长期应抽 `internal/modes` 包 → 留作后续重构。
- [`.cmd` 判断依赖后缀] 若 Claude Code 改名（如 `claudejs.cmd`）会失效 → 缓解：注释明确该判断的目的，后续可通过实际安装路径前缀扩展。

## Migration Plan

无需数据迁移；旧的 `validModes` 是包内未导出，导出为 `ValidModes` 是兼容改动（同包内调用方式不变）。

## Open Questions

- 是否需要在 `tui/modes_test.go` 之外再为 `tui` 单测加一个"modeItems 顺序与 Claude Code 官方顺序一致"的检查？目前不强约束（用户面向的是展示，不是 CLI 参数顺序）。
- `dist/` 加入 .gitignore 后，构建产物的发布流程怎么走？当前无 release 脚本，可能后续需要。