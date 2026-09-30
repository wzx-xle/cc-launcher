## Context

`internal/cli/cli.go:runProvider` 当前用手动 switch case 解析 args（行 105-145）：

```go
switch args[0] {
case "-h", "--help":
    fmt.Println(providerHelp)
    return nil
case "--list":
    return c.listProviders()
case "--template", "-T":
    return c.printTemplate(args[1:])
case "--new":
    // --new 可以与 --template 共存；如果同时给 --template，进入打印模式而非交互流
    var typeFlag string
    var positional string
    for i := 1; i < len(args); i++ {
        switch args[i] {
        case "--template", "-T":
            return c.printTemplate(args[i+1:])
        case "--type":
            ...
        default:
            ...
        }
    }
    return c.newProvider(positional, typeFlag)
case "--delete":
    if len(args) < 2 {
        return fmt.Errorf("用法: cc provider --delete <name>")
    }
    return c.deleteProvider(args[1])
default:
    if strings.HasPrefix(args[0], "-") {
        return fmt.Errorf("未知选项: %s", args[0])
    }
    return c.switchProvider(args[0])
}
```

**问题**：
1. 只接 `args[0]` 第一个 token dispatch——同时给 `--list --new` 走 `--list`，没互斥检查
2. 只接受 `--xxx` 双横杠——`ccl provider -list` 报"未知子命令: -list"
3. `-T` 是手动列在 case 中的别名机制——硬编码
4. `--type` 仅在 `--new` 上下文有意义，但需要手动循环 args 找

**目标**：跟 `ccl serve` / `ccl stats` 统一用 `flag.NewFlagSet`，让 `-x` / `--x` 两种形式都接受（Go 1.21+ stdlib 默认行为）。

约束：
- 不引入外部依赖（cobra / urfave-cli 等破坏零依赖原则）
- 不破坏现有 CLI 语义（`--xxx` 长形式照常 work；`--new --template <type>` 等价 `--template <type>` 的特殊语义保留）
- 复用 cli-shell-completion 创的 `registerProviderFlags` helper

## Goals / Non-Goals

**Goals**：
- `ccl provider -list` / `-new` / `-delete` / `-T` / `-template` / `-h` 都接受（runtime 行为）
- 4 个顶层 flag 互斥检查（`--list / --new / --delete / --template`）
- 位置参数 dispatch（`<name>` → switchProvider；`--delete <name>` / `--template <type>` / `--new <name>` 的位置参数）
- 复用 `registerProviderFlags` helper

**Non-Goals**：
- 不动 `providerTemplates` 内容（JSON 模板）—— `provider-templates` capability 不变
- 不动 `providerNewGuidedFlow` 行为（引导式流程）—— `provider-new-guided-flow` capability 不变
- 不改 `providerHelp` 文案（`--list / --new / --delete / --template, -T` 写法对短/长等价都适用）
- 不解决 completion 兼容短形式输入（`-list<TAB>` 仍无补全）—— 那是 cli-shell-completion 的 polish

## Decisions

### D1：flag 注册策略——`flag.NewFlagSet("provider", flag.ContinueOnError)` + 5 个 `Bool`

```go
fs := flag.NewFlagSet("provider", flag.ContinueOnError)
listFlag := fs.Bool("list", false, "列出所有提供商")
newFlag := fs.Bool("new", false, "新增提供商（可用 --type 选模板）")
deleteFlag := fs.Bool("delete", false, "删除提供商（需给 name）")
templateFlag := fs.Bool("template, T", false, "打印提供商配置模板")
helpFlag := fs.Bool("help, h", false, "显示本帮助并退出")
```

**原因**：
- Go 1.21+ stdlib 自动接受 `-x` 和 `--x` 两种形式（不再需要 Cobra）
- `Bool("template, T", false, ...)` 让 `-T` / `-template` / `--template` 都注册（std lib 语法：`name1, name2` 表示主名 + 短别名）
- `Bool("help, h", false, ...)` 让 `-h` / `--help` 都注册
- `ContinueOnError` 让 `fs.Parse` 返回 error 而不是 panic；`-h` 自动返回 `flag.ErrHelp`

**Trade-off**：Go 1.21 之前的 stdlib 不接受 `--x` 长形式（除非同时注册）—— 项目 go.mod 锁 1.22+，不是问题。

### D2：互斥检查——只查 3 个 flag（list / new / delete），template 不进 mutex

**关键约束**：`provider-new-guided-flow` spec 第 73-78 行锁住 `ccl provider --new --template` 等价 `--template`（不创建 provider 文件）—— mutex 必须放过这个组合。

```go
// mutex 只查 list / new / delete 三者互斥（语义对立：列 / 改 / 删）
flags := []*bool{listFlag, newFlag, deleteFlag}
set := 0
for _, f := range flags {
    if *f {
        set++
    }
}
if set > 1 {
    fmt.Fprintln(os.Stderr, "用法: ccl provider [--list | --new | --delete]（互斥）")
    fmt.Fprintln(os.Stderr, "详见 `ccl provider --help`")
    return fmt.Errorf("--list / --new / --delete 互斥；不可同时给")
}
```

**`--template` 不进 mutex 检查 + dispatch 里 `--template` 优先级最高**：
- 保留 `ccl provider --new --template deepseek` 等价 `--template deepseek`（旧约定）
- 保留 `ccl provider --new --template` 列全部模板（不创建 provider 文件）

**变化**：
- 原行为：`--list --new` 走 `--list`（args[0] 第一个匹配）
- 新行为：`--list --new` → 报错（mutex violation）

这是**用户面行为变化**—— spec 必须锁定（见 specs/provider-cli-parsing/spec.md）。

**Trade-off**：跟 `ccl serve` 的 4 flag 互斥（serve.go:55-67）风格略有不同（serve 不含 template 概念）——provider 的 template 是查询 / 显示 flag，**不进语义对立的 mutex 组**。

### D3：`--help` / `-h` 处理——`*helpFlag` 模式（mutex 检查前）

**关键事实**：Go stdlib `flag.ErrHelp` **仅在 `-h / --help` 没注册时返回**——一旦用 `Bool("help, h", false, ...)` 注册了这两个 flag，fs.Parse 见到 `-h` 会把 `*helpFlag` 设为 true，**不返回 ErrHelp**。所以判断必须靠返回值。

```go
fs.Parse(args)
if *helpFlag {
    fmt.Println(providerHelp)
    return nil
}
// mutex / dispatch 顺序
```

**优先级最高**：放在 mutex 检查**之前**——`ccl provider --list --new --help` 仍打 help（不报 mutex）。

**Trade-off**：provider 用 `*helpFlag` 模式（注册 + 判断），跟 serve 的 wantsHelpOnly / stats 的 fs.Args 后置检查都不同——provider **注册** flag（让 completion 补 `-h / --help`），再判断 flag 值。**为后续子命令立 flag**。

### D4：位置参数 dispatch——`fs.Args()` 取

`fs.Parse` 后：
- `fs.Args()` 收集未识别的非 flag token（位置参数）
- `fs.NArg()` 是位置参数数量

```go
positional := fs.Args()
var name string
if len(positional) > 0 {
    name = positional[0]
}

if *listFlag {
    return c.listProviders()  // 忽略位置参数
}
if *deleteFlag {
    if name == "" {
        return fmt.Errorf("用法: ccl provider --delete <name>")
    }
    return c.deleteProvider(name)
}
if *templateFlag {
    return c.printTemplate(positional)  // 0 或 1 个 type name
}
if *newFlag {
    // 进入 --new 内部 dispatch（见 D5）
}

// 无 flag → 走 switch provider 或 list
if name != "" {
    return c.switchProvider(name)
}
return c.listProviders()
```

**dispatch 顺序**：mutex → `--list` → `--delete` → `--template` → `--new` → 兜底（switch / list）。

### D5：`--type` 仅在 `--new` 上下文有意义——注册为顶层 flag 但 dispatch 时强制 `--new` 必须同时给

**关键问题**：`--type` 没注册到 FlagSet 时，fs.Parse 见到 `ccl provider --new --type deepseek` 会报 unknown flag → runProvider 退出，**破坏 `--new --type` 流程**。

**解决**：注册 `--type` 为顶层 flag（`Bool("type", false, "...")`），但 completion 时跳过（不让它出现在用户候选里），runProvider dispatch 时要求 `--new` 必须同时给：

```go
typeFlag := fs.Bool("type", false, "仅在 --new 上下文生效")
```

**runProvider dispatch**：
- `--type` 给 + `--new` 没给 → 报错"未知选项: --type"（保留 cli.go:185 旧行为）
- `--type` 给 + `--new` 给 → `newProvider(name, typeFlag)`

**completion 处理**：cli-shell-completion 的 `extractFlagNames` 拿到 flag 名后**过滤掉 `--type`**（不在用户候选里出现）—— 见 cli-shell-completion tasks.md §3.4。

**约束**：`registerProviderFlags` **必须包含** `--type` 注册（让 fs.Parse 通过），但 completion 时跳过。cli-shell-completion 不知道这个细节——需要 provider-flag-migration 在 dispatch 层处理。

**Trade-off**：把 `--type` 加到 registerProviderFlags 的返回里会让 helper 签名复杂。**当前决定**：`registerProviderFlags` 注册 `--type` 但**不返回** `*bool`（用户在 `runProvider` 里**用不上 `*bool` 值**——只用 `fs.Args()` 二次 parse 找 `--type` 后的 token）。具体实现见 D7。

### D6：`--new --template <type>` 等价 `--template <type>`——`--template` 优先级高于 `--new`

mutex 检查已保证不同时给 `--new --template`。但 dispatch 顺序里 `--template` 在 `--new` 之前（见 D4），即使 mutex 检查误判也走 `--template`：

```go
if *templateFlag {
    return c.printTemplate(positional)
}
if *newFlag {
    ...
}
```

**保留原行为**（cli.go:119 `case "--template", "-T": return c.printTemplate(args[i+1:])`）—— mutex 检查后 `--template` 优先。

### D7：`registerProviderFlags` 签名扩展——保持 4 个返回值不变，加 `help` 返回

cli-shell-completion 当前签名：

```go
func registerProviderFlags(fs *flag.FlagSet) (list, new, delete, template *bool)
```

**provider-flag-migration 扩展为**：

```go
func registerProviderFlags(fs *flag.FlagSet) (list, new, delete, template, help *bool)
```

**约束**（**关键**）：
- `list / new / delete / template` 4 个返回值的顺序与类型**不得修改 / 移除**——cli-shell-completion 的 `completeAtProvider` 依赖它们
- provider-flag-migration 可加新返回（如 `help`）但不删旧的
- `--type` 注册在 `registerProviderFlags` 内部但**不返回**——completion 不需要（见 D5）

```go
func registerProviderFlags(fs *flag.FlagSet) (list, new, delete, template, help *bool) {
    list = fs.Bool("list", false, "列出所有提供商")
    new = fs.Bool("new", false, "新增提供商")
    delete = fs.Bool("delete", false, "删除提供商")
    fs.Bool("template, T", false, "打印模板")         // 用短别名语法
    fs.Bool("type", false, "仅在 --new 上下文生效") // 见 D5
    fs.Bool("help, h", false, "显示本帮助")        // help 也注册
    template = ... // 拿 templateFlag 指针
    help = ...     // 拿 helpFlag 指针
    return
}
```

实际上 stdlib 不直接允许"注册但不返回"——`Bool` 总是返回 `*bool`。所以 `registerProviderFlags` 必须返回所有 5 个（或更多）指针。

**调整后**：

```go
func registerProviderFlags(fs *flag.FlagSet) (list, new, delete, template, typeFlag, help *bool) {
    list = fs.Bool("list", false, "...")
    new = fs.Bool("new", false, "...")
    delete = fs.Bool("delete", false, "...")
    template = fs.Bool("template, T", false, "...")
    typeFlag = fs.Bool("type", false, "...")
    help = fs.Bool("help, h", false, "...")
    return
}
```

返回 6 个：原 4 个 + 新 2 个（type + help）。

**cli-shell-completion 兼容性**：
- `completeAtProvider` 调 `registerProviderFlags(fs)` 时 Go 编译器**只校验参数类型不校验返回值个数**——调用方忽略额外返回值是合法的
- `_, _, _, _ := registerProviderFlags(fs)` 或 `_, _, _, _, _, _ := registerProviderFlags(fs)` 都合法

但 Go 函数返回值是位置敏感——加返回值属于**API 变更**（虽然调用方只取前 4 个时不破坏）。

**保险做法**：用命名返回让调用方明确：

```go
// completeAtProvider 调用
list, new, delete, template, _, _ := registerProviderFlags(fs)
```

`_, _` 显式忽略 type + help。OK 兼容。

## Risks / Trade-offs

**[R1] `--type` 注册为顶层 flag 但只对 `--new` 上下文有意义** → 用户按 `--type foo`（无 `--new`）走 dispatch 时报错（保留 cli.go:185 旧行为）。spec 锁定。

**[R2] mutex 行为变化** → 原"第一个 flag dispatch"变"mutex violation 报错"。spec 锁定。文档同步：providerHelp 顶部加"互斥"提示。

**[R3] `ccl provider --help` 走 stdlib `flag.ErrHelp`** → 不需要手动短路。`fs.PrintDefaults` 自动生成帮助（**注意**：当前 `providerHelp` const 是手写的，比 fs.PrintDefaults 更详细——保留手写 const，fs.PrintDefaults 仅给 completion 用）。

**[R4] `--new --type deepseek` 解析路径变化** → 原行为：cli.go:122 `case "--type"` 手动识别；新行为：fs.Parse 拿到 typeFlag=true，dispatch 时用。New + D5 锁定。

**[R5] registerProviderFlags 签名扩展** → cli-shell-completion 的 `completeAtProvider` 用前 4 个返回值，新加的 type + help 显式忽略。D7 锁定。**archive cli-shell-completion 时不锁定具体签名，只锁定"前 4 个返回值的顺序与类型不变"**。

## Migration Plan

- 用户：所有原 `--xxx` 形式照常 work；新行为（`-xxx` 短形式 + mutex 报错）是扩展
- 文档：providerHelp 顶部加"互斥"提示（4 个 flag 不可同时给）
- 兼容：`registerProviderFlags` 签名扩展时 cli-shell-completion 的 `completeAtProvider` 调用兼容

## Open Questions

- **`--new --template <type>` vs `--new --type <type>` 的语义差**：前者等价 `--template <type>`（直接打印模板）；后者用模板引导新建。spec 锁定两个 scenario。
- **providerHelp 顶部"互斥"提示**：提议加"4 个 flag 互斥，不可同时给（--list / --new / --delete / --template）"。
- **`fs.PrintDefaults` 自动生成帮助 vs providerHelp const**：保留手写 const（更详细）。`fs.PrintDefaults` 仅给 completion 用（不暴露给 user）。