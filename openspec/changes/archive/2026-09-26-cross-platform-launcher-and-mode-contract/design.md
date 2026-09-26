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

### 5. 契约测试用行为探测取代 `--help` 文本解析（修订）

> 决策 5 的最初版本要求解析 `claude --help` 的 `(choices: ...)` 段。实施时发现更稳定的来源：claude 在收到未知 `--permission-mode` 值时会输出 `Allowed choices are A, B, C.`（实测 v2.1.267，单行、机器可读，跨 `--help` / 裸调用 / `--print` 一致）。改为基于这个错误信息做契约测试。

算法：
1. 探测参数 `__cc_launcher_probe__`（绝不可能合法）触发错误路径
2. 在 CombinedOutput 里正则抓 `Allowed choices are (.*?)\.`
3. 在抓到的列表里用 `[A-Za-z][A-Za-z0-9]*` 提项并去重

**理由**：行为探测走的是 Claude Code 的运行时参数校验器，不依赖 help 文本的排版（help 文本会按终端宽度折行、可能改格式）；错误信息是单行字符串，更稳。

**风险**：Claude Code 改 `Allowed choices are` 的措辞 → 测试会假阳性。缓解：错误信息是 SDK / CLI 通用约定（commander.js / yargs 都这么写），跨大版本变化概率低；报错信息打印完整原始输出便于快速修复。

**实测样本**（v2.1.267）：
```
$ claude --permission-mode bogusMode
error: option '--permission-mode <mode>' argument 'bogusMode' is invalid. Allowed choices are acceptEdits, auto, bypassPermissions, manual, dontAsk, plan.
```

### 6. 抽 `internal/modes` 包作为单一事实源（新增）

`internal/modes` 包持有 `Mode{Name, Description}`、`Names()`、`All()`、`ByName()` 与包级 `all` 切片。`internal/cli` 用 `modes.Names()` 校验；`internal/tui` 用 `modes.All()` 渲染；`internal/modes/modes_test.go` 锁 names 顺序、唯一性、描述非空、`ByName` 双路径。

**理由**：(a) 消除 `internal/tui/modes_test.go` 跨包 import `internal/cli` 的方向性问题；(b) 描述是用户做选择的依据，属于领域知识而不是 TUI 展示细节；(c) 三处共用一份字面量，新增 / 删除模式只改一处。

**备选 A**：`modes` 包只放 names，descriptions 留在 `tui` → 不接受。描述是模式本身的语义，不是 TUI 的展示细节。

**备选 B**：把 `Names()` / `All()` 改成全局变量（而不是函数返回值）→ 不接受。每次访问都 copy 一份切片避免调用方误改内部状态。

### 7. 修正 mode 描述

旧描述把 `dontAsk` 写成"不询问，直接执行"严重误导——实际是**最严格**的非交互模式（未被 allow 规则预授权则直接拒绝），适合 CI 而非"自动放行"。同步修正 `auto`（模型分类器判定）、`bypassPermissions`（危险）、`plan`（只读）等描述。

## Risks / Trade-offs

- [Windows 退出码透传不可见] 在 Windows 上 cmd.Run() 的 exit code 与 Unix syscall.Exec 的行为不完全对齐（Windows 的 ExitError.ExitCode() 可能不返回 Unix 信号导致的退出码）→ 不解决：cc-launcher 的目标是用户体验一致，不深入 Windows 进程信号语义。
- [契约测试措辞依赖] 探测依赖 `Allowed choices are` 这串英文 → 缓解：错误信息是 SDK / CLI 通用约定，跨大版本变化概率低；测试报错信息打印完整原始输出便于快速修复；未来如 Claude Code 提供 `--print-schema` 可一键切换。
- [`.cmd` 判断依赖后缀] 若 Claude Code 改名（如 `claudejs.cmd`）会失效 → 缓解：注释明确该判断的目的，后续可通过实际安装路径前缀扩展。
- [modes 包描述是中文] 当前 descriptions 是中文，与 TUI / README 一致；如果未来要支持英文环境 → 后续可加 `modes.DescriptionEN` 或本地化层。

## Migration Plan

旧的 `cli.validModes`（小写、未导出）→ `cli.ValidModes`（导出）→ 移除，改用 `modes.Names()`。这是一个三阶段的兼容改动，因为：
- 阶段 1（已合并 `5077e1b`）：导出 `ValidModes`，让测试可以引用。
- 阶段 2（本次 7.x）：移除 `ValidModes`，统一走 `modes.Names()`。

如果项目里还有其他调用方引用 `cli.ValidModes`，阶段 2 会破坏它们——但 cc-launcher 是单一仓库，且 grep 已确认只有 cli / tui / 测试三处引用，安全。

## Open Questions

- `dist/` 加入 .gitignore 后，构建产物的发布流程怎么走？当前无 release 脚本，可能后续需要。
- 是否需要在 `modes_test.go` 之外再为 TUI 加一个"渲染时把 Description 当成一行内文本而不是段落"的测试？目前由 `renderModeView` 的拼接逻辑保证，但未单独测。