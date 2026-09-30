## Why

当前 `ccl provider` 子命令用手动 switch case 解析 args（`internal/cli/cli.go:runProvider`），**只接受双横杠**形式（`--list` / `--new` / `--delete` / `--template` / `-T`）——这跟 `ccl serve` / `ccl stats`（用 `flag.NewFlagSet` 注册 flag，Go 1.21+ stdlib 自动接受 `-x` 和 `--x` 两种）不一致：

- `ccl stats -since 2024-01-01` ✅ runtime 接受
- `ccl serve -stop` ✅ runtime 接受
- `ccl provider -list` ❌ runtime 报"未知子命令: -list"

三个子命令的 flag 接受形式**不一致**——用户用 `-<TAB>` 在 serve / stats 下能跑，在 provider 下不能。

## What Changes

- **重构** `internal/cli/cli.go:runProvider`：从手动 switch → `flag.NewFlagSet` 注册 flag。注册 4 个顶层 flag + 1 个 help flag：
  - `Bool("list", false, ...)` → `ccl provider -list` / `--list` 都接受
  - `Bool("new", false, ...)` → `ccl provider -new` / `--new` 都接受
  - `Bool("delete", false, ...)` → `ccl provider -delete <name>` / `--delete <name>` 都接受
  - `Bool("template, T", false, ...)` → `-T` / `-template` / `--template` 都接受
  - `Bool("help, h", false, ...)` → `-h` / `--help` 都接受
- **新增** 互斥检查：4 个顶层 flag（`--list / --new / --delete / --template`）**最多给一个**；同时给 2 个及以上 → stderr 打用法提示 + exit 非 0。
  - 这是**用户面行为变化**——原来 `ccl provider --list --new` 会走 `--list` 分支（args[0] 第一个匹配 dispatch）；新行为会报错。需要 spec 锁定
- **保留** 位置参数语义：`ccl provider <name>` 仍走 `switchProvider(name)`（用 `fs.Args()` 取）；`--delete <name>` / `--template <type>` / `--new <name>` 的位置参数用 `fs.Args()` 取
- **保留** `--type` 二级 flag 语义：只在 `--new` 内部 parse，不进 provider 顶层 flag 集合
- **保留** `--new --template <type>` 等价 `--template <type>` 的特殊语义（在 `runProvider` 内识别 `--new` 后再分发）
- **复用** `internal/cli/cli.go:registerProviderFlags(fs *flag.FlagSet) (list, new, delete, template *bool)` helper（cli-shell-completion 已建）——让 `runProvider` 和 `completeAtProvider` 共用同一份 flag 注册，避免 drift
- **可扩展** `registerProviderFlags` 签名（如加 `help` 返回）——`runProvider` 用 `help` 指针处理 `flag.ErrHelp`。**cli-shell-completion 已用的 4 个返回指针不得修改 / 移除**（向上兼容约束）
- **BREAKING**：无。所有 `--xxx` 长形式照常 work；新行为（`-xxx` 短形式 + 互斥报错）是**扩展**而非破坏

## Capabilities

### New Capabilities
- `provider-cli-parsing`：provider 子命令 CLI flag 解析契约（覆盖短/长形式接受 + 4 flag 互斥 + 位置参数 dispatch + `--type` 二级 flag 不进顶层 + `--new --template` 等价 `--template`）。

### Modified Capabilities
（无修改。`provider-templates` 锁的是 JSON 模板内容、`provider-new-guided-flow` 锁的是引导式流程——本 change 只动 CLI 解析层，不动模板内容或引导流程的行为。）

## Impact

- 代码：
  - `internal/cli/cli.go` 重构 `runProvider`（约 60-80 行改动）：从手动 switch case 改成 `flag.NewFlagSet` + `fs.Parse` + 互斥检查 + dispatch
  - `internal/cli/cli.go` 复用 `registerProviderFlags` helper（cli-shell-completion 已建）；可能扩展签名加 `help` 返回，**保留 4 个原返回值**不变
  - `internal/cli/cli.go` 的 `providerHelp` 文案不动（`--list` / `--new` / `--delete` / `--template, -T` 的写法对短/长等价都适用）
- 测试：
  - unit test 锁 flag 接受形式：`-list` / `--list` runtime 等价；`-T deepseek` / `--template deepseek` 等价
  - unit test 锁互斥：`--list --new` 同时给 → 报错
  - unit test 锁位置参数：`ccl provider foo` → `switchProvider("foo")`（无 flag 时）
- 文档：README 不动（CLI 表面没有功能变化，只是输入更宽容）
- 依赖：零
- 跨平台：Linux / macOS / Windows 通用
- 关联 change：`cli-shell-completion` 创的 `registerProviderFlags` helper 是本 change 的复用基础；archive cli-shell-completion 时该 helper 最小契约固定（4 个返回值 `(list, new, delete, template *bool)` 不变）

## Open Questions

- **互斥检查的报错文案**：提议 `用法: ccl provider [--list | --new | --delete | --template]（互斥）`，跟 `ccl serve` 的 mutex 报错格式一致（见 `serve.go` 第 64 行）。
- **`-h` 处理**：`Bool("help, h", false, ...)` 注册后 fs.Parse 遇到 `-h` 会返回 `flag.ErrHelp`（stdlib 自动）。`runProvider` 调 `fs.Parse(args)` 时检查 `errors.Is(err, flag.ErrHelp)` → 打 `providerHelp` + exit 0。**变更**：原来 `ccl provider --help` 走 cli.go:106 case `-h, --help`；新行为走 stdlib 自动 help。
- **`ccl provider -h<TAB>` completion**：cli-shell-completion 的 `extractFlagNames` 拿到 `--help`（PrintDefaults 输出长名）。用户按 `-h<TAB>` 时 bash 不一定响应（参考 kubectl 惯例——只补长名）。**不在本 change 解决**：completion 兼容短形式是 cli-shell-completion 的额外 polish。