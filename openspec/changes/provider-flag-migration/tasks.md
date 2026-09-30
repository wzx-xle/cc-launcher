## 1. runProvider 主体重构

- [x] 1.1 在 `internal/cli/cli.go:runProvider` 顶部建 `fs := flag.NewFlagSet("provider", flag.ContinueOnError)`，删原 `switch args[0]` 的手动 case 分支
- [x] 1.2 注册 6 个 flag（含 1 个 string）：
  - `Bool("list", false, "列出所有提供商")` → `-list / --list`
  - `Bool("new", false, "新增提供商")` → `-new / --new`
  - `Bool("delete", false, "删除提供商")` → `-delete / --delete`
  - `Bool("template, T", false, "打印提供商配置模板")` → `-T / -template / --template`
  - `String("type", "", "仅在 --new 上下文生效")` → `-type / --type`（拿 string 值供 newProvider 用）
  - `Bool("help, h", false, "显示本帮助并退出")` → `-h / --help`

## 2. registerProviderFlags 签名扩展

- [x] 2.1 把 `registerProviderFlags(fs *flag.FlagSet) (list, new, delete, template *bool)` 扩展为 `(list, new, delete, template *bool, typeFlag *string, help *bool)`
- [x] 2.2 **约束**：cli-shell-completion 已用的 4 个 `*bool` 返回值**位置 / 类型 / 顺序**不变；新增 `typeFlag` / `help` 追加在末尾。`completeAtProvider` 仍可 `_, _, _, _, _, _ := registerProviderFlags(fs)` 显式忽略新返回值
- [x] 2.3 `templateFlag` 用 `Bool("template, T", false, ...)` 注册（短别名语法）

> **注**：Go 1.21+ stdlib 实际不接受 `Bool("template, T", ...)` 这种逗号语法——会把字面 `"template, T"` 注册成单个 flag。改用 `BoolVar(template, "template", ...)` + `BoolVar(template, "T", ...)` 共享同一 bool 指针实现短别名（`-T` 和 `--template` 反映到同一 template 指针）。

- [x] 2.4 `helpFlag` 用 `Bool("help, h", false, ...)` 注册

> **同上注**：`--help` 与 `-h` 共享同一 help 指针。

- [x] 2.5 `typeFlag` 用 `String("type", "", ...)` 注册（不返回 *bool——string flag 不一样）

## 3. mutex 检查

- [x] 3.1 fs.Parse 后立即检查：`flags := []*bool{listFlag, newFlag, deleteFlag}`，set 计数
- [x] 3.2 set > 1 → stderr 打 `用法: ccl provider [--list | --new | --delete]（互斥）` + `详见 ` + "`ccl provider --help`"，error 字符串 `"--list / --new / --delete 互斥；不可同时给"`
- [x] 3.3 **不**把 `--template` / `--type` 加进 mutex（保留 `--new --template` 约定 + `--type` 单独约束）

## 4. --help / -h 处理（优先级最高，mutex 之前）

**修订**：Go 1.21+ stdlib `parseOne` 见到 `-h` / `--help` **即便注册了也会硬返回 `flag.ErrHelp`**（代码里 hardcoded 检查 `name == "help" || name == "h"`），所以判断靠 `errors.Is(err, flag.ErrHelp)` 而不是 `*helpFlag`。`*helpFlag` 仅作未来 stdlib 行为变化的兜底。

- [x] 4.1 fs.Parse 后**第一件事**（mutex 检查之前）：`if errors.Is(err, flag.ErrHelp) { fmt.Println(providerHelp); return nil }`——exit 0。优先级最高：用户显式求 help 时**不**报 mutex 错（即使同时给了 `--list --new --help`，仍打 help）
- [x] 4.2 删原手动 `case "-h", "--help":` 分支（已被 `flag.ErrHelp` 接管）

## 5. dispatch 顺序（mutex 通过后）

- [x] 5.1 `fs.Args()` 取位置参数列表；`name := positional[0]` 取第一个（若有）
- [x] 5.2 **优先级 1：`--template` / `-T`** → `c.printTemplate(positional)`（0 或 1 个 type name）
- [x] 5.3 **优先级 2：`--list`** → `c.listProviders()`（忽略 positional）
- [x] 5.4 **优先级 3：`--delete`** → `name == ""` 报错用法；`name != ""` 调 `c.deleteProvider(name)`
- [x] 5.5 **优先级 4：`--new`** → 调 `c.newProvider(name, *typeFlag)`；`name == ""` 时引导式问 name；`*typeFlag != ""` 时跳过模板选择
- [x] 5.6 **兜底：无 flag + `name != ""`** → `c.switchProvider(name)`
- [x] 5.7 **兜底：无 flag + 无 name** → `c.listProviders()`

## 6. --type 上下文约束

**精确条件**：仅当 `--type` 是**唯一**给定 flag 时报错；其它 4 个核心 flag（list / new / delete / template）任一给定时 `--type` silently ignored（dispatch 不读 `--type`）。

- [x] 6.1 dispatch 顶部（mutex 检查后，dispatch 之前）：`*typeFlag != "" && !*listFlag && !*newFlag && !*deleteFlag && !*templateFlag` → 报错 `fmt.Errorf("--type 只能与 --new 一起使用")`
- [x] 6.2 `--new` 分支用 `*typeFlag` 传给 `newProvider` 作为 `typeName` 参数

## 7. 保留 `--new --template` 约定

- [x] 7.1 dispatch 顺序里 `--template` 在 `--new` 之前判断（保证 mutex 通过后 `--new --template deepseek` 走 printTemplate 而非 newProvider）
- [x] 7.2 单元测试锁：`ccl provider --new --template deepseek` 的输出 == `ccl provider --template deepseek` 的输出（不创建 provider 文件）—— 见 §10.7 `TestProvider_NewTemplateDeepseek_EqualsTemplateDeepseek`

## 8. providerHelp 文案同步

- [x] 8.1 在 `providerHelp` const 顶部加"互斥"提示：`--list / --new / --delete 互斥，不可同时给（--template 允许与 --new 共存）`
- [x] 8.2 其余文案不动（`--list / --new / --delete / --template, -T` 写法对短 / 长形式都对）

## 10. 测试

**调用约定**：所有 provider 测试通过 `c.Run([]string{"provider", ...})` 走完整 path（跟现有 `TestProvider_BogusFlagStillErrors` / `TestTemplate_ShortAliasT` 一致），不直接调 `runProvider`。stdout / stderr 用现有 `captureStdout` / 新增 `captureStderr` 工具。

- [x] 10.1 unit (cli_test.go)：`c.Run({"provider", "-list"})` 输出 == `c.Run({"provider", "--list"})` 输出（list 短形式）—— `TestProvider_ShortList_EquivLongList`
- [x] 10.2 unit：`c.Run({"provider", "-new"})`（mock readLine → EOF）调 newProvider，等价 `c.Run({"provider", "--new"})`（new 短形式）—— `TestProvider_ShortNew_EquivLongNew`
- [x] 10.3 unit：`c.Run({"provider", "-delete", "temp"})`（temp 存在）调 deleteProvider，等价 `--delete temp`（delete 短形式）—— `TestProvider_ShortDelete_EquivLongDelete`
- [x] 10.4 unit：`c.Run({"provider", "-T", "deepseek"})` 输出 deepseek JSON 模板，等价 `--template deepseek`（template 短形式 + 短别名）—— `TestProvider_ShortT_EquivLongTemplate`
- [x] 10.5 unit：`c.Run({"provider", "-h"})` 输出 providerHelp，等价 `--help`（help 短形式）—— `TestProvider_ShortH_EquivLongHelp`
- [x] 10.6 unit：mutex 报错（4 个 case 都在 `TestProvider_Mutex_AllCases`）：
  - `c.Run({"provider", "--list", "--new"})` → 返回 error + stderr 含 "互斥"
  - `c.Run({"provider", "--list", "--delete", "foo"})` → 同上
  - `c.Run({"provider", "--new", "--delete", "foo"})` → 同上
  - `c.Run({"provider", "--list", "--new", "--template", "deepseek"})` → 同上（4 个核心 flag 中 3 个触发 mutex）
- [x] 10.7 unit：`c.Run({"provider", "--new", "--template", "deepseek"})` 输出 deepseek JSON（**不**进引导式，**不**创建文件）——约定保留 —— `TestProvider_NewTemplateDeepseek_EqualsTemplateDeepseek`
- [x] 10.8 unit：`c.Run({"provider", "--new", "--template"})` 列全部 7 模板（约定保留）—— `TestProvider_NewTemplateNoArg_ListsAll`
- [x] 10.9 unit：`c.Run({"provider", "--type", "deepseek"})`（无 `--new` 且无其它 flag）报错 —— `TestProvider_TypeAlone_Errors`
- [x] 10.10 unit：`c.Run({"provider", "--delete"})`（无 name）报错用法 —— `TestProvider_DeleteNoName_Errors`
- [x] 10.11 unit：`c.Run({"provider"})`（无 args）调 listProviders；`c.Run({"provider", "foo"})` 调 switchProvider("foo") —— `TestProvider_FallbackDispatch`
- [x] 10.12 unit：`c.Run({"provider", "-new", "--type", "deepseek", "my-deepseek"})`（mock）调 newProvider("my-deepseek", "deepseek")（短形式 + 位置参数组合）—— `TestProvider_ShortNewTypePositional_PassesThrough`
- [x] 10.13 unit：`c.Run({"provider", "--list", "--help"})` → 打 providerHelp，**不**报 mutex（help 优先级最高）—— `TestProvider_ListHelp_HelpWinsOverMutex`
- [x] 10.14 unit：`c.Run({"provider", "--list", "--template", "deepseek"})` → mutex 通过 + template 优先级 → printTemplate（`--list` 被忽略）—— `TestProvider_ListTemplate_TemplateWins`
- [x] 10.15 unit：`c.Run({"provider", "--delete", "foo", "--type", "deepseek"})` → mutex 通过 → deleteProvider("foo")，`--type` silently ignored —— `TestProvider_DeleteWithType_TypeIgnored`
- [x] 10.16 `go build ./...` 通过
- [x] 10.17 `go test ./...` 通过（重点 cli_test.go 不破坏——`TestProvider_BogusFlagStillErrors` 等老测试在新实现下继续）
- [x] 10.18 `go test ./... -tags contract` 通过（跟 claude CLI 契约测试不破坏）

## 11. 收尾

- [x] 11.1.a grep 全仓非 archive 路径：现有 `ccl provider --list / --new / --delete / --template` 引用**没误改**（CLI 表面没功能变化，只是输入更宽容）—— README.md / cmd/cc-launcher/main.go 等仍用长形式，runtime 现在两种都接受
- [x] 11.1.b grep 全仓：mutex 报错文案 `互斥` 在 mutex 实现里出现（spec 锁定）—— internal/cli/cli.go:182, 184
- [x] 11.1.c grep 全仓：至少一个 smoke test 触发新接受的短形式（`-list / -new / -delete / -T / -h`）—— `TestSmoke_ProviderShortForms_Accepted`（跑 `-list` / `-T` / `-h` 端到端）
- [ ] 11.2 commit message 用简体中文：`refactor(provider): 把 ccl provider 迁到 flag.NewFlagSet（接受 -x / --x 短形式 + mutex 收紧 + 保留 --new --template 约定）`