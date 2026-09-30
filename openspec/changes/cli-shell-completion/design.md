## Context

`ccl` 命令树已经分层：

- 顶层子命令：`serve` / `stats` / `provider` / `mode`（外加 `-h/--help/help`）
- `ccl serve` 子命令：4 个互斥 bool flag（`--stop / --restore / --status / --restart`）+ help
- `ccl stats`：6 个 flag（`--since / --by-provider / --by-project / --project / --json / -h/--help`）
- `ccl provider`：4 个 flag（`--list / --new / --delete / --template / -T / -h/--help`）+ 模板名（位置参数）
- `ccl mode`：6 个权限模式名（位置参数）+ help

总共需要补全的位置 40+，靠 `ccl --help` 现读现翻不直观。shell tab completion 是工业惯例（`kubectl / gh / docker / helm / aws-cli` 都内置），用户一次 `source <(ccl __init bash)` 完成启用后，所有 ccl flag / 子命令 / 模板名 / 权限模式名都按 tab 提示。

约束：
- 不引入外部依赖（cobra / urfave-cli 等破坏零依赖原则）
- 不暴露 `__complete` / `__init` 在 `ccl --help` 子命令列表（这两是内部协议）
- 改 flag 后 completion 自动同步（不能"加了 --restart 但 tab 不提示"）

## Goals / Non-Goals

**Goals：**
- bash + zsh 双 shell 支持
- 隐藏子命令 `__complete` 动态输出当前位置候选（每行一个 token）
- 隐藏子命令 `__init <shell>` 输出 wrapper script，user `source` 一次即启用
- `internal/templates.All` 自动拉模板名（无需在 completion 维护副本）
- 候选集分两类：flag 候选（带短描述，便于 shell 显示）和位置参数候选

**Non-Goals：**
- fish / powershell（先跳过；fish 后续可加）
- 不补用户私有 provider 名（`<name>` 位置靠 shell 文件名补全接管）
- 不补 daemon status / db 行内容（这些是运行时数据，不适合 completion）
- 不做 `--json` 候选输出（用户明确要 human-readable；cobra 风格的 `__complete --format=json` 暂不引入）

## Decisions

### D1: 协议选最简——每行一个候选 token

**决策**：`ccl __complete <args...>` 把当前 args 解析后，**只**输出候选 token（每行一个）。不带描述、不带 type、不带 JSON——bash/zsh 都能直接消费。

```bash
# bash
complete -C 'ccl __complete' ccl
# 用户按 tab → shell 调 `ccl __complete ccl serve --` → ccl 输出
--stop
--restore
--status
--restart
-h
--help
```

**原因**：
- 工业惯例（kubectl / docker 都是这种纯行式）
- bash / zsh 原生消费；不需要 shell-side 解析器
- 实现简单

**备选**：
- A. JSON 输出（cobra 风格）：bash / zsh 都需要 side parser，复杂
- B. 带描述（`<token>\t<comment>`）：bash 不支持；zsh 支持但需要 _describe；增加复杂度
- C. 类型（`<token>:<type>`）：仅 zsh 支持

**未来扩展**：若 zsh 需要描述，可加 `ccl __complete --format=zsh`，但当前不做。

### D2: 复用 stdlib `flag.NewFlagSet` 反射提取 flag 名

**决策**：completion 函数对每个有 flag 的子命令（`serve / stats / provider`）跑一遍 `flag.NewFlagSet + flag.Bool(...) + fs.PrintDefaults()`，解析出 `  -name, --name  desc` 行，提取 flag 名。

**原因**：
- flag 改动后 `__complete` 输出自动跟随
- 不维护两份（运行时 flag + 候选表）避免脱节

**实现草案**：

```go
// extractFlagNames returns []string of flag names for a FlagSet by parsing PrintDefaults output.
func extractFlagNames(fs *flag.FlagSet) []string {
    var buf bytes.Buffer
    fs.SetOutput(&buf)
    fs.PrintDefaults()
    fs.SetOutput(os.Stderr) // restore
    var names []string
    for _, line := range strings.Split(buf.String(), "\n") {
        // line format: "  -x, --name  description"
        if i := strings.Index(line, "--"); i >= 0 {
            rest := line[i+2:]
            if j := strings.IndexAny(rest, " ,"); j >= 0 {
                names = append(names, "--"+rest[:j])
            }
        }
    }
    return names
}
```

**Trade-off**：依赖 `PrintDefaults` 输出格式；stdlib flag 的格式是稳定的。

**实施前置（关键，决定 D2 是否成立）**：每个子命令的 flag 注册必须抽成 helper，否则 D2 的"自动同步"失效。

`runServe` 里 `fs := flag.NewFlagSet("serve", ...)` 加 4 个 `fs.Bool(...)` 的代码块是 `runServe` 的局部变量；函数返回后 fs 就没了。completion 函数拿不到任何已注册的 flag——只能**重新跑一遍** `flag.NewFlagSet` + `flag.Bool(...)`。所以：

- 唯一可行方案 = 把注册代码抽成 `func registerServeFlags(fs *flag.FlagSet) (stop, restore, status, restart *bool)`（stats / provider 同理）
- `runServe` 调它拿 flag 指针 → 用作 mutex 检查 / dispatch
- `completeAtServe` 调它 → 跑 `fs.PrintDefaults()` 提取 flag 名

否则 `runServe` 加了一个新 flag（如 `--rotate-logs`）但 `completeAtServe` 没改，会出现"runtime 接受 / completion 提示不一致"的脱节——D2 卖点直接垮。

同样适用于 `ccl stats`（`registerStatsFlags`）和 `ccl provider`（`registerProviderFlags`，注意内部要 parse `--type` 二级 flag）。`ccl mode` 无 flag 不需要。

### D3: 模板名 / 权限模式名从已公开 API 拿

**决策**：

- `ccl provider --template <TAB>` → 从 `internal/templates.All` 提取 `.Name`
- `ccl mode <TAB>` → 从 `internal/modes.Names()`（公开 API）提取

**原因**：
- `internal/templates` 包有 `var All []Template`（公开变量，`templates.go:37`），直接遍历即可
- `internal/modes` 包有 `Names() []string`（公开 API，`modes.go:19`）；私有变量 `all []Mode` 不可外部访问
- **`internal/cli` 没有 `ValidModes` 常量**——早先文档误记。`runMode` 实际走 `modes.Names()`（cli.go:266, 279）

**核对**：
- `internal/templates/templates.go:37` 有 `var All = []Template`，每项有 `Name string`（共 7 个：`anthropic / deepseek / minimax / glm / aliyun-coding / aliyun-pay / aliyun-token`）
- `internal/modes/modes.go:19` 有 `func Names() []string`（共 6 个：`manual / acceptEdits / auto / bypassPermissions / dontAsk / plan`）

### D4: wrapper script（`__init bash` / `__init zsh`）

**决策**：`__init bash` 输出：

```bash
_ccl_complete() {
    local cur prev words cword
    _init_completion || return
    # 把当前 words 转成 CCLI __complete 参数
    COMPREPLY=( $(compgen -W "$(ccl __complete "${words[@]:1:$cword}")" -- "$cur") )
}
complete -F _ccl_complete -o default ccl
```

`__init zsh` 输出：

```zsh
#compdef ccl
_ccl() {
    local -a completions
    completions=("${(@f)$(ccl __complete "${words[@]:2:$CURRENT-2}")}")
    compadd -- "${completions[@]}"
}
compdef _ccl ccl
```

**原因**：bash 用 `complete -F` 优于 `-C` 模式——前者支持 stdin / 函数；后者每次 spawn 子进程。`kubectl` / `gh` 都用 `-F` 模式。

**注意**：zsh 的 `${(@f)$(...)}` 是 zsh-specific 语法（按行 split），bash 不用。

### D5: 隐藏 `__complete` / `__init` 在 topHelp 子命令列表外

**决策**：在 `main.go:run()` 顶部 switch 加 `case "__complete"` / `case "__init"`，但 `topHelp` const 的"子命令:"段不列这两条。

**原因**：用户跑 `ccl --help` 不该被"__complete"这种内部协议干扰。`__init bash` 输出的 wrapper script 里有启用说明，能引导用户启用。

### D6: 错误处理与 exit code

**决策**：`__complete` 任何错误都吞掉，**输出空字符串 + exit 0**。原因：completion 是 shell side 的"辅助"，不应让用户感受到 panic——空候选 = 没建议。

`__init <shell>` 未知 shell → exit 1 + stderr "unknown shell: <name>（支持: bash / zsh）"。

## Risks / Trade-offs

**[R1] `flag.PrintDefaults` 输出格式变化**
→ 接受。stdlib `flag` 包格式极其稳定（20+ 年不变）；如真变了 `__complete` 会输出空，shell 显示"无候选"，不致 crash。Mitigation：smoke test 锁定典型输出。

**[R2] bash / zsh 完成引擎差异**
→ bash 用 `compgen -W`，zsh 用 `compadd`。两者通过不同 wrapper script 桥接。Mitigation：`__init bash` / `__init zsh` 分别写。

**[R3] 每按一次 tab spawn 一个 ccl 进程（小开销）**
→ 接受。ccl 启动 ~10ms，spawn 完全可接受（kubectl / gh 都这么做）。Mitigation：未来可加 cache，但当前不做。

**[R4] 隐藏子命令用户可能发现"漏洞"**
→ 接受。`__complete` / `__init` 都是 bash / zsh 启用流程的必经路径，不算"漏洞"。

**[R5] provider 名称 / mode 名称位置不应补全**
→ 接受。用户的 `~/.cc-launcher/providers/*.json` 文件名靠 shell 自身文件补全（`<TAB>` 默认行为）。Mitigation：completion 函数对"位置参数 + 已有 flag"的位置不输出任何 token（让 shell 文件补全接管）。

## Migration Plan

- 启用：用户 `source <(ccl __init bash)` 到 `~/.bashrc`（一次性）
- 降级：`unalias ccl 2>/dev/null; complete -r ccl`（一次性）
- 不需要 schema 迁移或数据迁移

## Open Questions

- `ccl provider --new --type <TAB>` 的"模板名补全"是否要做？当前决定是"做"（用户体验更连贯）。成本不高：从同一 candidate 列表拿。
- `ccl provider --new` 互动流里的"字段名补全"是否要做？**当前决定不做**——provider --new 是 TUI 内部补全（不通过 ccl 二进制），跟 ccl command line flag 无关。

---

> **架构观察**（仅 info，非决策）：
> 引入 `__complete` 后，ccl 的 command dispatcher 实际上变成了**双协议入口**：
>   - 用户面：`ccl <subcmd> [args]` → 走 `run` switch
>   - Shell 助手：`ccl __complete <args>` → 走 completion 引擎
>
> 两者解析同一份 flag 集合（通过 `flag.NewFlagSet` 反射），维护成本仅 1 份。这正是 D2 的价值。