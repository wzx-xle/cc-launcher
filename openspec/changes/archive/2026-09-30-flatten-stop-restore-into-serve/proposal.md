## Why

`ccl` 现在有三个"管理 daemon / settings"的顶层子命令（`serve` / `stop` / `restore`），但 `stop` 和 `restore` 只接受 `-h/--help`，没有任何 flag——它们本质上是 `serve` 的**两个动作**（启动 vs. 停 vs. 还原），却和 `serve` 平级作为三个独立顶层子命令。这跟现有 `ccl provider --list/--new/--delete` 把"动作"做成 flag 的风格不一致，也违背"同一对象的多个操作用同一命令组织"的原则。

把这三个动作整合到 `ccl serve` 下：
- `ccl serve`           启动 daemon（默认行为）
- `ccl serve --stop`    停 daemon（原 `ccl stop`）
- `ccl serve --restore` 还原 settings.json（原 `ccl restore`）

## What Changes

- **新增** `ccl serve --stop` flag（bool）：原 `ccl stop` 子命令的逻辑直接搬过来，复用 `runStop` 内部实现和错误文案（"daemon not running"）。
- **新增** `ccl serve --restore` flag（bool）：原 `ccl restore` 子命令的逻辑直接搬过来，复用 `runRestore` 内部实现和错误文案（"no settings to restore"）。
- **删除** 顶层 `ccl stop` 子命令。main.go 路由表去掉 `case "stop"` 分支；runStop/statscmd.go 对应代码删除（`stop_unix.go` / `stop_windows.go` 里的 `stopDaemon` 函数**保留**——是 `runStop` 的下层实现，被 `runServe --stop` 调用）。
- **删除** 顶层 `ccl restore` 子命令。main.go 路由表去掉 `case "restore"` 分支；`runRestore` 函数移动到 `serve.go` 内部（或保留在 `statscmd.go` 但改为 unexported），由 `runServe --restore` 调用。
- **新增** `ccl serve --status` flag（bool）：报告 daemon 运行状态，human-readable 输出。复用 `probeDaemonAddr()` 探端口 + `readPidfile()` 读 PID + 新增一条 `COUNT(*)` 查询 SQLite 拿 session 数 + 文件大小。**四态判断**：(端口通 & 有 pidfile) → running；(端口通 & 无 pidfile) → foreign service；(端口不通 & 有 pidfile) → stale pidfile；(端口不通 & 无 pidfile) → not running。
- **修改** `ccl --help` 顶层帮助（`topHelp`）：去掉 `stop` 和 `restore` 两个子命令条目，`serve` 一行改写为说明三个动作。
- **修改** `ccl serve --help`：增加 `--stop` / `--restore` / `--status` 三个 flag 的说明。
- **修改** README 用量监控小节（第 318-325 行）：`ccl stop` / `ccl restore` 改为 `ccl serve --stop` / `ccl serve --restore`。
- **BREAKING**：移除 `ccl stop` 和 `ccl restore` 顶层子命令。仓库内无脚本/外部 cron 依赖（grep 0 命中 .sh/.go/.yaml 外部引用）；唯一对外暴露是 README 同步更新。Archive 目录下的历史 references 不动。

stats 子命令完全不动（已参数化）。

## Capabilities

### New Capabilities
（无新增 capability。本次改动只迁移现有接口，不引入新的契约能力。）

（--status 是 daemon-lifecycle capability 下的一个新 requirement，不是新 capability；在下方的 "Modified Capabilities" 里以 ADDED Requirements 形式落地。）

### Modified Capabilities
- `daemon-lifecycle`：Requirement "ccl stop sends SIGTERM to daemon" 改写为 `ccl serve --stop` 接口；该 requirement 下的所有 scenario 的 WHEN 子句从 `ccl stop` 改为 `ccl serve --stop`。
- `claude-code-env-config`：Requirement "ccl restore removes all CC_ORIG_* and ccl-injected defaults" 改写为 `ccl serve --restore` 接口；该 requirement 下的所有 scenario 的 WHEN 子句从 `ccl restore` 改为 `ccl serve --restore`。

## Impact

- 代码：
  - `cmd/cc-launcher/main.go`：路由表删除 `stop` 和 `restore` 两行 case
  - `cmd/cc-launcher/statscmd.go`：删除 `runStop` + `runRestore` + `stopHelp` + `restoreHelp` + `groupByLabel`（`groupByLabel` 是 stats 自己的辅助函数，跟 stop/restore 同文件但不依赖 stop/restore，可保留也可移——按就近代移原则留在 statscmd.go 不动）
  - `cmd/cc-launcher/serve.go`：runServe 顶部加 `flag.NewFlagSet`，注册 `--stop` / `--restore` 两个 bool flag；命中任一则分发到对应逻辑并 `return`（不走监听端口分支）；未命中走原启动 daemon 流程
  - `cmd/cc-launcher/stop_unix.go` / `stop_windows.go`：`stopDaemon` 函数签名和实现**不动**（它是 `runStop` 现在的下层调用，被 `runServe --stop` 复用）
  - `cmd/cc-launcher/statscmd.go` / 内部 restore 相关代码：`restoreOTelEnv` 函数不动，被 `runServe --restore` 复用
- 顶层帮助：`topHelp`（`statscmd.go:18-41`）去掉 stop / restore 条目
- `serveHelp`（`serve.go:145-156`）：增加 `--stop` / `--restore` 说明
- README 第 318-325 行
- 活动 spec（`daemon-lifecycle`、`claude-code-env-config`）：见 Modified Capabilities
- archive 目录：**不动**
- 依赖、API、跨平台：均无影响