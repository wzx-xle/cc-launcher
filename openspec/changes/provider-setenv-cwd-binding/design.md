## Context

ccl 当前在 `runTUI` 里走"读 provider JSON → 调 `config.Manager.Merge(providerCfg, oldSnapshot)` 写 `~/.claude/settings.json` → `injectOTelEnv` 注入 OTel → `launcher.Launch`" 的流程。`launcher.Launch` 在 Unix 调 `syscall.Exec(path, argv, os.Environ())`、Windows 调 `exec.Cmd` 默认继承父进程环境——**完全不感知 provider env**，provider env 是被 Merge 持久化到 settings.json 里、由 Claude Code 启动时自己加载的。

这种"文件式覆盖"在三个场景下暴露问题：

1. **同项目不同阶段切换 provider**——merge 流程每次都要备份 + snapshot 比对 + 写文件，且 settings.json 是用户级共享的，跨项目互相干扰。
2. **未通过 ccl 启动的 claude 进程**——settings.json 里 ccl 残留的 ANTHROPIC_* 会影响用户直接 `claude` 启动的会话，无法"无残留"。
3. **用户手写过 settings.json ANTHROPIC_***——ccl setenv 会被 settings.json 压住（CC 文档明确 settings.json env 优先级 > shell env），需要主动清理历史注入。

约束：

- OTel 注入行为必须保持兼容（仍写 settings.json 的 5 个 fallback keys + 视情况接管 per-signal metrics 端点），不能因为 provider 走 setenv 而影响 OTel。
- launcher 包保持"最小窗口包"定位，不感知 env 来源——env 由调用方准备，launcher 只透传。
- 跨 Unix/macOS/Windows + bash/zsh/PowerShell 都要工作。

## Goals / Non-Goals

**Goals:**

- ccl 在 exec claude 前通过进程环境注入 provider 配置（ANTHROPIC_AUTH_TOKEN / ANTHROPIC_BASE_URL / ANTHROPIC_MODEL / ANTHROPIC_API_KEY / CLAUDE_CODE_USE_BEDROCK 等），进程退出即释放。
- 支持按 cwd 绑定 provider，与"按 cwd 记 permission mode"对称。
- 老用户 settings.json 中 ccl 注入的 ANTHROPIC_* 一次性清理（备份到 snapshots/），保证 setenv 不会被压住。
- PowerShell 提供 tab completion（与 bash/zsh 范围一致）。
- OTel 注入行为不变。

**Non-Goals:**

- 不做"按 git branch 绑定 provider"（属于产品代码意图，超出 ccl 工具定位）。
- 不做"项目级 .claude/settings.local.json 自动生成"（属于另一种产品方案，与 setenv 路径冲突）。
- 不动 settings.json 里用户手写的非 provider env（保留 CLAUDE.md §"claudeCode 本身添加的配置要保留"）。
- 不为 PowerShell 写完整的功能 banner / 启动信息脚本（只补 Tab completion）。
- 不为 WSL / Git Bash 单独适配（它们在 Windows 上是 Unix 子系统，走 Unix 路径）。

## Decisions

### Decision 0：env 注入走 launcher.Options.Env 字段，不走 os.Setenv

**选择**：构造 env []string（含父进程 env + 替换/追加 provider keys）作为 `launcher.Options.Env`，透传给最终 exec 的 Claude Code 子进程。ccl 父进程不调 `os.Setenv`。

**替代方案考虑**：

- 替代 A：main.go runTUI 末尾 `os.Setenv("ANTHROPIC_AUTH_TOKEN", val)` 等，再让 `launcher.Launch` 沿用 `os.Environ()`。→ 拒绝：`runTUI` 流程在 `syscall.Exec claude` **之前**会调 `ensureDaemonRunning → forkDaemon`（cmd/cc-launcher/probe.go:50）fork `ccl serve` 子进程。该子进程 `cmd.Env = nil` 默认继承父进程 env——若父进程已被 `os.Setenv` 污染，daemon **永久持有 provider token**直至用户 `ccl serve --stop`，与 CLAUDE.md §"提供商配置不能残留" 的安全承诺冲突。`os.Setenv` 是全局副作用，跨单测难隔离，未来 launcher 被其他 caller 复用时边界不清。**收益仅省 ~15 分钟净工作量（替代 A 必补 forkDaemon 修复 + 跨进程回归测试），代价是 token 泄漏 + 可测性退化**。

**Match?**：✅

### Decision 1：launcher.Options 新增 `Env []string` 字段，launcher 不感知 env 内容

**选择**：launcher.Launch 接受 `opts.Env []string`，Unix 透传给 `syscall.Exec` 第 3 参数、Windows 写到 `cmd.Env`。

**替代方案考虑**：

- 替代 A：launcher 直接 `os.Environ()` + 调 `os.Setenv` 注入后透传。→ 拒绝：launcher 包不该感知 provider 字段名，违反"最小窗口包"定位；main.go 之外的别的 caller（比如 stats 子命令）不应该被影响。
- 替代 B：在 launcher 包创建 `LookupProviderEnv(name string) []string` helper。→ 拒绝：launcher 包依赖 provider 包会引入反向依赖（launcher 是被 provider 链路用的）；且 provider 配置逻辑本属于 cmd 层组装。

**Match?**：✅

### Decision 2：opts.Env 在 main.go 组装：父进程 env + 替换/追加 provider keys

```go
env := os.Environ()                                 // 父进程 env 拷一份
for k, v := range providerEnvMap(providerCfg) {     // ANTHROPIC_*/CLAUDE_CODE_USE_BEDROCK/...
    env = upsertEnv(env, k, v)                       // 替换整条 / 找不到则 append
}
opts.Env = env
```

`upsertEnv` 是新 helper（建议放在 `internal/envutil` 包，或 `internal/cli/envutil.go`），逻辑：

```go
// 替换已存在的同 key 条目；找不到则 append 到末尾。
func upsertEnv(env []string, key, value string) []string {
    prefix := key + "="
    for i, e := range env {
        if strings.HasPrefix(e, prefix) {
            env[i] = prefix + value
            return env
        }
    }
    return append(env, prefix+value)
}
```

**为什么用"替换"而不是"追加"**：父进程 env 里可能有用户 export 的同名 key（比如 `~/.zshrc` 里写了 `export ANTHROPIC_AUTH_TOKEN=sk-personal`，ccl 不删 user 变量但 override 为 provider 的值——这符合"ccl 切换覆盖"的语义。

**Windows 上 SetEnv 的等价行为**：cmd.Env 直接传 env slice，每个 entry 是 `KEY=VALUE` 字符串——Go 的 exec 包自动处理。无需 `cmd.Env = cmd.Environ()`，权限可控。

### Decision 3：一次性迁移用 snapshot 比对识别"ccl 写的"

```go
func migrateSettingsJSON(cm *config.Manager, sm *state.Manager) error {
    snapshot := sm.GetInjectedSnapshot()
    if snapshot == nil { return nil }                // 无历史，跳过

    snapshotEnv, _ := snapshot["env"].(map[string]any)
    if len(snapshotEnv) == 0 { return nil }

    // 备份 settings.json（迁移本身是写操作）
    if err := cm.Backup(); err != nil { ... }

    settings, err := cm.LoadSettings()
    if err != nil { return err }

    envAny, _ := settings["env"].(map[string]any)
    cleaned := []string{}
    for k, snapVal := range snapshotEnv {
        cur, ok := envAny[k]
        if !ok { continue }                          // 已不在 settings.json，跳过
        if cur != snapVal { continue }                // 用户手改过，**不删**（保护用户意图）
        delete(envAny, k)
        cleaned = append(cleaned, k)
    }

    if len(cleaned) > 0 {
        settings["env"] = envAny
        if err := cm.SaveSettings(settings); err != nil { return err }
    }

    sm.SetInjectedSnapshot(nil)                      // 清空 snapshot 表示已迁移
    return nil
}
```

**幂等性**：每次 runTUI 开头调一次。无 snapshot → return；有 snapshot → 清完后清空 snapshot，下次再跑无 snapshot → return。**幂等**。

**关键设计：只删值完全等于 snapshot 的 key**：

- snapshot 是 ccl 自己上次写的内容。snapshot 与 settings.json 当前值相等 → ccl 写的、用户没改 → 删掉无副作用
- snapshot 与 settings.json 不等 → 用户手改过 → 保留（CLAUDE.md §"claudeCode 本身添加的配置要保留"）
- snapshot 不在 settings.json → 已被用户/其它工具清掉 → 跳过

### Decision 4：state.json 新增 `dir_bindings`，与 `permission_modes` 同级结构

**字段命名说明**：`dir_bindings`（vs `last_permission_modes` 的 `last_` 前缀）——`last_permission_modes` 是"每次用都覆盖"的临时记忆；`dir_bindings` 是"用户主动 '记住' 才写入"的持久化项目级默认。语义略不同，故命名不强行统一。如未来觉得分裂，可统一为 `cwd_provider_bindings` + `cwd_permission_modes`，但属 breaking change，需独立 change 推动。

```jsonc
// ~/.cc-launcher/state.json
{
    "active_provider": "anthropic",
    "dir_bindings": {                                  // 本 change 新增
        "/home/u/work/proj-a": "deepseek",
        "/home/u/work/proj-b": "anthropic"
    },
    "permission_modes": {
        "/home/u/work/proj-a": "auto"
    },
    "injected_snapshot": { ... }
}
```

**匹配规则**：

- `dir_bindings[wd]` 存在 → 用之（cwd 级覆盖）
- 否则 → `active_provider`（全局兜底）

**为什么 cwd 用绝对路径而不是项目内相对路径**：项目可能被 symlink 链接，cwd 解析后绝对路径稳定；project ID（比如 homebrew）的标识需要外部工具（比如 gh 仓库路径）整合，ccl 不做。

**TUI 末尾的"记住"交互**（TUI 流程结束时）：

```
已选 deepseek (override 默认 anthropic)
记住当前目录 (/home/u/work/proj-a) 默认用 deepseek 吗？[Y/n]
```

- Y → `sm.SetProviderForDir(wd, result.Provider)`
- n / 超时 → 不记

TUI 取消（Esc）不进入此提示。

### Decision 5：`__init powershell` 输出 `Register-ArgumentCompleter` 脚本，复用 `__complete` 协议

`__complete` 协议 bash/zsh 已经在用——"`ccl __complete <args>` 返候选，每行一个"。PowerShell wrap 一层：

```powershell
# ccl __init powershell 输出
$ccl = Get-Command ccl | Select-Object -ExpandProperty Source

Register-ArgumentCompleter -Native -CommandName 'ccl' -ScriptBlock {
    param($wordToComplete, $commandAst)

    $tokens = @($commandAst.ToString() -split '\s+')
    $candidates = & $ccl __complete @tokens 2>$null

    foreach ($line in @($candidates)) {
        if ($line -like "$wordToComplete*") {
            [System.Management.Automation.CompletionResult]::new(
                $line, $line, 'ParameterName', $line
            )
        }
    }
}
```

**用户接入**：

```powershell
# 添加到 $PROFILE（= $env:USERPROFILE\Documents\PowerShell\Microsoft.PowerShell_profile.ps1）
Invoke-Expression -Command (ccl __init powershell)
```

或者管道：

```powershell
ccl __init powershell | Out-String | Invoke-Expression
```

**为什么不直接 `source <(ccl __init powershell)`**：bash/zsh 的进程替换是 bash 特性，PowerShell 不支持。换 `Invoke-Expression` 是 PowerShell 等价习惯。

**为什么 `-Native`**：ccl 是 native executable（Go 编译的 binary），不用 `-Native` 时 PowerShell 会把命令当成 cmdlet 处理，补全协议不工作。

### Decision 6：ccl provider --list 显示"cwd 当前绑定"标识

输出示例：

```
* anthropic       ← cwd 命中 dir_bindings
  deepseek
  glm
▶ aliyun-coding   ← 当前 active_provider
```

`*` 标记 cwd 绑定的 provider（比 `▶` 优先级更高，因为 cwd 覆盖全局默认）；`▶` 仍标记 active_provider。

实现：`runProvider` 调 `sm.GetProviderForDir(wd)`，与每个 provider name 比较。

## Risks / Trade-offs

**[Risk] 老用户首次切换 provider 时 settings.json 的 ANTHROPIC_* 被自动删（ccl 写的部分）** → 备份到 `~/.cc-launcher/backups/settings-<timestamp>.json`（沿用现有 Backup 机制）；`ccl serve --restore` 可还原。文档明确"首次切换会有一次 settings.json 自动清理"。

**[Risk] settings.json 里用户手写过 ANTHROPIC_* 时 ccl setenv 失效** → 文档明确告知"如需 ccl 接管 provider，请从 settings.json 删除 ANTHROPIC_* 让 ccl setenv 生效"。migrate 函数**不删**用户手写的（值与 snapshot 不等则跳过），避免误伤。

**[Risk] cwd 路径在不同 shell / 工具下解析不一致**（PowerShell 用 `Resolve-Path`，Git Bash 用 `pwd -W`）→ ccl 在 Go 进程里用 `os.Getwd()`，跨平台走 Go 标准库，不受 shell 解析差异影响。但用户**手动编辑** state.json 时需用 Go 解析后的绝对路径（文档提示）。

**[Risk] PowerShell completion 每次按 Tab fork ccl __complete 进程** → 候选是静态列表（子命令 / 模式名 / 模板名），通常 < 10ms 启动开销。如果未来性能不够，加 `--cache` 标志，但本期不优化。

**[Risk] launcher.Env 字段是新增的，外部调用者（tests / 文档）可能忽略** → cross-platform-launcher spec 加 Scenario："未传 Env 时侯选项行为——Unix 退化为 `os.Environ()`、Windows 退化为父进程 env"。

**[Risk] 现有 `ccl provider <name>` CLI 切换只改 active_provider 不改 settings.json，但 runTUI 流程改 settings.json——两条路径语义不一致** → 本 change 统一：CLI 切换也走 setenv + migrate（CLI 不直接 exec claude，所以不注入 env；但 state 记录 active_provider 仍生效）。这部分在 tasks.md 单独拆 task。

**[Risk] 一次迁移如果 ccl 在迁移中被 kill（写到一半）→ settings.json 可能处于"部分清理"状态** → 用现有 SaveSettings 原子写（`os.WriteFile` 直接覆盖，非 rename）——**这本身就是个已存在的风险**，不是本 change 引入。Backup 仍是用户的回滚手段。

**[Trade-off] setenv 不持久化到 settings.json，依赖 state.json 的 dir_bindings 作为唯一持久层** → 如果 state.json 损坏，cwd 绑定丢失。用户需要知道 `~/.cc-launcher/state.json` 的存在与作用（README 加一段）。

**[Trade-off] PowerShell completion 脚本用 `Invoke-Expression`（动态执行）——`Invoke-Expression` 是 PowerShell 安全黑名单常用项** → ccl 是用户主动安装的二进制，且 `Invoke-Expression` 在用户配置的 `$PROFILE` 里只执行一次（启动时），不等价于"任意文本 dynamic eval"。在 README 安全文档提一下。

- **[Positive] 顺手修复了 ccl 老版本 `cleanSnapshot` 误删用户手写 key 的隐 bug**：老 ccl `Manager.Merge` 流程里 `cleanSnapshot`（config.go:195）只看 key 是否在 snapshot 中、**不看值是否匹配用户手改**——若用户在某次 Merge 之后手改了 `settings.json` 的 `ANTHROPIC_AUTH_TOKEN`，下次 Merge 会**误删**用户手写内容。本方案 migrate 函数用 "snapshot 值 == settings.json 当前值才删" 原则（Decision 3），正确保护用户手写 key；migrate 跑完后 `Manager.Merge` 路径整体废弃（tasks §4.7），该隐 bug 一并消失。

## Migration Plan

### 用户感知到的迁移

1. **老 ccl 用户（settings.json 已有 ccl 注入的 ANTHROPIC_*)**：
   - 第一次 ccl 启动时 `migrateSettingsJSON` 跑一次。
   - 用户看到 stderr 提示：`⚙ 检测到 ccl 之前注入的 provider env，正在清理 settings.json（备份到 ~/.cc-launcher/backups/）... ✓`
   - settings.json 的 ANTHROPIC_* 被清（ccl 写的部分）。
   - 从此 ccl 切换 provider 走 setenv，不再动 settings.json。

2. **新 ccl 用户（首次安装）**：
   - state.json 无 snapshot → migrate 直接 return。
   - 用户用 TUI 选 provider → 写入 dir_bindings（如果用户同意"记住"）。
   - 用户首次启动 claude 时 ccl setenv 注入 provider env。

3. **settings.json 有用户手写 ANTHROPIC_***：
   - migrate 函数识别为"用户手改过"（值与 snapshot 不等）→ 跳过。
   - setenv 在这些 key 上被 settings.json 压住 → ccl 切换无效。
   - README "settings.json 历史兼容" 段明确告知如何处理。

### 回滚策略

- 单次误操作：`ccl serve --restore` 还原 settings.json（现有机制）。
- 整 change 回滚：git revert。但要意识到 dir_bindings 已写入 state.json，回滚不会自动清——需要 `ccl provider --unbind <cwd>`（一个反向命令，本 change **不包含**，写在 future work）。

### OpenSpec 之外的回滚提示

- 如果想取消整个 setenv 方案回退到老逻辑：git revert 此 change。`config.Manager.Merge` + `cleanSnapshot` + `deepMerge` 已按 tasks §4.7 删除，回滚后需 `git revert` 还原这三个符号。state.json.dir_bindings 已写入的绑定**不自动清理**（保留），用户可手动编辑 state.json 或用 `ccl provider <name>` 重写覆盖。

## Open Questions

- **cwd 路径规范化**：用 `filepath.Abs(wd)` 还是 `os.Getwd()` 直接返回？Go 的 `os.Getwd()` 在大多数情况下返回绝对路径，但 symlink 解析不统一（macOS 上 `/tmp` → `/private/tmp`）。决定：用 `os.Getwd()`，用户自管理 symlink。如果用户 symlink `proj-a` 到 `proj-b`，他们大概率自己知道这俩是同一项目。

- **migrate 触发时机**：每次 runTUI 开头调一次（每次启动） vs 仅在检测到 settings.json 变化时调。前者简单，每次启动都有 snapshot 检查成本（无 snapshot → 1 nil 判断，< 1µs）。决定：每次启动调，靠幂等性保证安全。

- **PowerShell completion 性能**：每次按 Tab fork ccl __complete 进程。如果用户在 PowerShell 里快速按 Tab，可能 10+ 次/秒。当前 __complete 逻辑是 `cli.CompleteAt*` 静态查表（不读文件），启动开销 < 20ms。如果性能不够再加缓存。

- **CLI `ccl provider <name>` 与 TUI 路径不一致**：CLI 只改 active_provider，不走 setenv（CLI 不 exec claude）。TUI 改 dir_bindings + setenv。本 change 统一语义：CLI 也写 dir_bindings（cwd 是触发当前 cwd）。决定：CLI 也写 dir_bindings（cwd = 当前 shell 的 cwd），与 TUI 对齐。但 CLI 不调 migrate（不 exec claude 不需要清 settings.json）。