## 0. 代码/文档盘点（2026-09-30 实施前核对）

实施前对仓库现状做了实地核对（对比 `internal/cli/cli.go::registerProviderFlags` 与 `cmd/cc-launcher/{serve,statscmd}.go`）：

- **已实现**（不需重做）：`internal/cli/cli.go::registerProviderFlags`（§2.0.c）；变更说明见已归档 `2026-09-30-provider-flag-migration`
- **未实现**（本 change 要做）：`completion.go` 主文件、`registerServeFlags` / `registerStatsFlags` helpers（serve.go:46-50 / statscmd.go:33-38 仍 inline）、main.go 路由 `__complete` / `__init`（main.go:48-63 switch 无此 case）、`topHelp` 加 Tab completion 段落（main.go:19-39）、README Tab completion 小节（README 当前无引用）、`runCompletion` / `runShellInit` 单元测试 + smoke test
- **明确不动**（保留 §6.2 / §7.2 / §9.2 描述）：serveHelp 不加 Tab 段；README 其他章节不动；commit message 用简体中文

子任务盘点：37 项里 **37 项已勾选（全部完成）**——`go build ./...` + `go test ./...` + `go test -tags smoke ./cmd/cc-launcher/...` 全绿。

## 1. completion.go 核心：__complete 与 __init

> **前置确认**：`internal/cli/cli.go::registerProviderFlags` 已实现于 `cli.go:105-141` 且被 `runProvider` 复用（共享同一份 flag 注册逻辑，design §D2 的"自动同步"已成立）。本 change 不重写也不重构该 helper，仅在 `completeAtProvider` 中消费其返回值。

- [x] 1.1 新建 `cmd/cc-launcher/completion.go`：包内 `runCompletion(args []string) error` + `runShellInit(shell string) error`
- [x] 1.2 `runCompletion` 顶层：根据 `args` 长度判断位置（如 `ccl __complete serve ""` 表示在 `ccl serve` 后待补全；wrapper 已去掉 `ccl`，跟 kubectl / gh 风格一致），分发到 `completeAtTopLevel` / `completeAtServe` / `completeAtStats` / `completeAtProvider` / `completeAtMode`
- [x] 1.3 新增 helper `extractFlagNames(fs *flag.FlagSet) []string`：复用 `flag.PrintDefaults()` 输出格式提取 flag 名（design §D2）

## 2. flag 注册 helper 抽取（D2 实施前置）

design §D2 的"flag 改动自动同步"前提是 `runX` 与 `completeAtX` 共享同一份 flag 注册代码。stdlib `flag` 不导出已注册 flag，唯一办法是抽 helper。

- [x] 2.0.a 抽 `registerServeFlags(fs *flag.FlagSet) (stop, restore, status, restart *bool)`：`runServe` 与 `completeAtServe` 共用；返回的指针供 `runServe` 做 mutex 检查与 dispatch
- [x] 2.0.b 抽 `registerStatsFlags(fs *flag.FlagSet)`：`runStats` 与 `completeAtStats` 共用
- [x] 2.0.c `internal/cli/cli.go::registerProviderFlags`：**已实现**（cli.go:105-141）。签名为 `(list, new, delete, template *bool, typeFlag *string, help *bool)`——含 `--template / -T` 共用 `template` 指针、`--type` 二级 flag（`typeFlag *string`）、`--help / -h` 共用 `help` 指针。`runProvider` 与未来的 `completeAtProvider` 共享此 helper。

> **实施说明**（本 change 不重写，但实施时需做的包边界处理）：
> 1. `registerProviderFlags` 当前为包内 unexported（`internal/cli` 包），`cmd/cc-launcher/completion.go`（`package main`）跨包调用不到。**实施时**第一步把它改名为 `RegisterProviderFlags`（exported）——改动最小，注释里"cli-shell-completion 兼容性"的承诺自然延伸
> 2. §3.4 `completeAtProvider` 直接 `cli.RegisterProviderFlags(fs)` 拿 flag 集合，再走 `extractFlagNames(fs)`（§1.3）取候选
> 3. `runProvider` 迁到 `flag.NewFlagSet` 已由 `2026-09-30-provider-flag-migration` 完成；本 change 不再触碰 provider runtime 解析

## 3. 各子命令候选集

- [x] 3.1 `completeAtTopLevel`：`serve / stats / provider / mode / -h / --help / help`
- [x] 3.2 `completeAtServe`：调 `registerServeFlags(fs)` 后跑 `extractFlagNames(fs)`（`--stop / --restore / --status / --restart / -h / --help`）
- [x] 3.3 `completeAtStats`：调 `registerStatsFlags(fs)` 后跑 `extractFlagNames(fs)`
- [x] 3.4 `completeAtProvider`：调 `registerProviderFlags(fs)` 后跑 `extractFlagNames(fs)`；解析 `--template / -T` 位置补 7 个模板名
- [x] 3.5 `completeAtProvider` 位置：`--template ""` 或 `--new --type ""` 时从 `templates.All` 提取 `Name`
- [x] 3.6 `completeAtMode`：从 `modes.Names()` 提取 6 个权限模式名（无 flag）
- [x] 3.7 mutex 防御：`completeAtServe` 看到前一个 token 已是 4 个 flag 之一时，**输出空**（不补，避免误导）
- [x] 3.8 `completeAtProvider --delete ""` → 输出空（位置参数是用户文件）
- [x] 3.9 `completeAtProvider --new ""` → 补 `--type` / `-T` / `--template` / `-h` / `--help`
- [x] 3.10 unknown subcommand（如 `help`）→ 输出空

## 4. 隐藏子命令路由

- [x] 4.1 `main.go:run()` 顶部 switch 加 `case "__complete"` → `runCompletion(args[1:])`
- [x] 4.2 `main.go:run()` 顶部 switch 加 `case "__init"` → `runShellInit(args[1])`（args[1] 是 shell 名）
- [x] 4.3 确认 `topHelp` const 子命令列表**不**包含 `__complete` / `__init`

## 5. shell wrapper script（__init）

- [x] 5.1 `runShellInit("bash")`：输出 `complete -F _ccl_complete -o default ccl` wrapper
- [x] 5.2 `runShellInit("zsh")`：输出 `compdef _ccl ccl` wrapper（`${(@f)$(...)}` 按行 split）
- [x] 5.3 未知 shell：stderr 打 `unknown shell: <name>（支持: bash / zsh）` + exit 1，stdout 空

## 6. help 文案同步

- [x] 6.1 `topHelp` 加 "Tab completion" 段落：`source <(ccl __init bash)` / zsh 对应
- [x] 6.2 `serveHelp` 不动（细节太碎，不在 help 里教 completion 启用）

## 7. README 同步

- [x] 7.1 README 加 "Tab completion" 小节：教用户加到 `~/.bashrc` / `~/.zshrc`
- [x] 7.2 README 不动其他章节

## 8. 测试

completion 是纯函数（输入 args → 输出 token 流），主战场在 `main_test.go`（无 build tag，
`go test ./...` 覆盖）。smoke 仅留端到端断言（hidden-from-help）。

- [x] 8.1 unit (main_test.go)：调 `runCompletion` 各位置，验证候选集（顶层 / serve / stats / provider / --template / --new --type / mutex / --delete 空 / --new 空 / help 空 / bogus 空）
- [x] 8.2 unit (main_test.go)：`runShellInit("bash")` 输出非空 + 包含 `complete -F` 关键字
- [x] 8.3 unit (main_test.go)：`runShellInit("zsh")` 输出非空 + 包含 `compdef` 关键字
- [x] 8.4 unit (main_test.go)：`runShellInit("fish")` exit 非 0 + stderr 含 "unknown shell"
- [x] 8.5 smoke (smoke_test.go)：端到端 `run([]string{"--help"})` 不含 `__complete` / `__init` 字样
- [x] 8.6 smoke (smoke_test.go)：端到端 `run([]string{"__complete", ""})` 顶层候选正确
- [x] 8.7 `go build ./...` 通过
- [x] 8.8 `go test ./...` 通过
- [x] 8.9 `go test -tags smoke ./cmd/cc-launcher/...` 通过

## 9. 收尾

- [x] 9.1 grep 全仓非 archive 路径：除 prose（README 帮助文本 / spec 文字）外应无 `__complete` / `__init` 误引用
- [x] 9.2 commit message 用简体中文：`feat(cli): 加 shell tab completion（ccl __complete + ccl __init）`