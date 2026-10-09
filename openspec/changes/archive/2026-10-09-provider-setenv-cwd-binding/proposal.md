## Why

ccl 当前通过 `config.Manager.Merge` 把 provider 配置（ANTHROPIC_AUTH_TOKEN / ANTHROPIC_BASE_URL / ANTHROPIC_MODEL）写进 `~/.claude/settings.json` 的 `env` 块，再 `syscall.Exec` / `exec.Cmd` 启动 claude。这种"全局文件式覆盖"在两个场景下有摩擦：

1. **同一项目不同阶段要换 provider**——每次切换都要走完整 merge 流程（备份 + snapshot 比对 + 写文件），且 settings.json 是用户级共享的，跨项目互相干扰。
2. **动态切换需要"无残留"**——claude 进程退出后，settings.json 里残留的 provider env 会影响下一个未通过 ccl 启动的 claude 进程。

把 provider 配置从 settings.json 移到"exec 前 setenv 到子进程环境"可以同时解决——进程退出 env 即释放**，且能与 OTel 整合完全解耦（OTel 仍写 settings.json）。同时增加"按 cwd 记住 provider"功能，与现有"按 cwd 记 permission mode"语义对称。

## What Changes

- **新增** launcher.Launch 通过 `Env []string` 字段接收环境变量；Unix 传给 `syscall.Exec` 第 3 个参数；Windows 写到 `exec.Cmd.Env`（保留父进程 env，仅替换/追加 provider keys）。
- **修改** runTUI 流程：选择 provider 后不再调 `config.Manager.Merge` 写 settings.json，改为在 exec 前 `os.Setenv`。
- **新增** state.json 新增 `dir_bindings map[string]string` 字段（cwd → provider）；TUI 启动时优先匹配 cwd 默认值；用户在 TUI 末尾可选"记住当前目录"。
- **新增** 一次性迁移：扫描 settings.json 的 env 块，识别 ccl 历史注入的 ANTHROPIC_*（基于 snapshot 比对），从 settings.json 删除（备份到 `~/.cc-launcher/snapshots/`），保证 setenv 不会被 settings.json 压住。OTel / CC_ORIG_* / FORWORD_* 等其他 env 不动；用户手写（非 ccl 写入）的 ANTHROPIC_* 不清理。
- **新增** `__init powershell` 子命令：输出 PowerShell `Register-ArgumentCompleter` 脚本，用户加到 `$PROFILE` 后启用 tab completion。**BREAKING**（表面）：补全范围与 bash/zsh 对齐。
- **修改** `ccl provider --list` 输出新增"按 cwd 当前绑定"标识（cwd 命中 dir_bindings 的 provider 在列表里以 `*` 标记）。

## Capabilities

### New Capabilities

- `launcher-provider-env`：launcher 包接受调用方注入的子进程环境变量，跨平台（Linux/macOS/Windows）。
- `provider-cwd-binding`：state.json 维护 cwd → provider 映射；按 cwd 自动选择默认 provider；TUI 末尾可"记住当前目录"。
- `provider-setenv-migration`：一次性识别并清理 settings.json 中 ccl 历史注入的 ANTHROPIC_* env（基于 snapshot 比对），保留用户手写的 key。
- `powershell-completion`：ccl `__init powershell` 子命令输出 PowerShell Register-ArgumentCompleter 脚本。

### Modified Capabilities

- `cross-platform-launcher`：`launcher.Options` 新增 `Env []string` 字段，Unix `syscall.Exec` 第 3 参数、 Windows `exec.Cmd.Env` 必须使用此字段（不再直接传 `os.Environ()`）。

## Impact

- 代码：
  - `internal/launcher/launcher.go`：Options 新增 Env 字段。
  - `internal/launcher/launcher_unix.go` / `launcher_windows.go`：用 Env 字段替换 `os.Environ()` 直接调用。
  - `cmd/cc-launcher/main.go:runTUI`：删除 `cm.Merge(providerCfg, oldSnapshot)` 调用；改为 `os.Setenv` 注入 + `sm.SetInjectedSnapshot(providerCfg)` 记录。
  - `cmd/cc-launcher/main.go`：新增 `migrateSettingsJSON(cm, sm)` 在 runTUI 开头执行一次性迁移。
  - `internal/state/manager.go`：state.json 读写支持 `dir_bindings` 字段；新增 `GetProviderForDir(wd) string` / `SetProviderForDir(wd, name string) error`。
  - `internal/tui/`：启动时传入 cwd 默认 provider；末尾新增"是否记住此目录"交互。
  - `internal/cli/cli.go:runInit`：新增 `powershell` 分支，输出 `Register-ArgumentCompleter` 脚本。
- 测试：
  - launcher unit：注入 env 与不注入 env 时 cc 子进程读到的值（Unix + Windows 分文件测）。
  - state unit：dir_bindings 读/写/列表。
  - migrate unit：snapshot 中存在的 key 被清，snapshot 之外的 key 保留。
  - TUI unit：cwd 命中 dir_bindings 时默认 provider 填充。
- 文档：
  - README 增加"按目录记 provider"章节。
  - README 增加"PowerShell tab completion"章节。
  - README 增加"settings.json 历史兼容"段落，说明 snapshot 之外的 ANTHROPIC_* 由用户自管。
- 兼容性：
  - 现有 user 通过 `ccl` 默认 ccl 控制 settings.json 的用户：首次切换后 settings.json 里的 ANTHROPIC_* 被一次性清掉（ccl 之前自己写的，备份到 snapshots/）；之后 settings.json 不再被 ccl 动 provider 字段。
  - settings.json 里有用户手写 ANTHROPIC_* 的：setenv 在该 key 上会被 settings.json 压住；文档明确告知"请从 settings.json 删除 ANTHROPIC_* 交给 ccl 管理"。
  - OTel 注入行为不变。
  - Permission mode 按 cwd 绑定行为不变。
- 跨平台：Linux / macOS / Windows 通用；PowerShell 在 Windows 原生支持，macOS/Linux 上 PowerShell Core 7+ 也可作为可选终端。