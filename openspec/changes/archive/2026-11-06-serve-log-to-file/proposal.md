## Why

**serve 的日志无处可查**：daemon 是被 `ccl` 自动拉起的（detached、无终端），
`cmd.Stdout/Stderr = nil` → 日志**全部丢弃**。

而 serve 恰恰是出问题时最需要日志的地方：额度接口 429、厂商改了字段、刷新失败
……用户遇到这些只能猜——"serve 有日志吗"的答案是"自动拉起的没有"。

真机场景（2026-11-06，Windows）：8 个 provider 的机器上额度接口返回 429，用户
完全看不到原因（日志被丢弃 + Windows 控制台把 UTF-8 按 GBK 显示成乱码）。

## What Changes

- serve 的 stdout/stderr 落到 `~/.cc-launcher/serve.log`（UTF-8、0600、>2MB 截断）
- 额度刷新相关的日志行加 **`[quota]` ASCII 标记** → 即使中文显示成乱码，也能
  用 `findstr quota` / `Select-String` 精确筛出所有额度相关的行
- 额度刷新每轮多打一行**汇总**（查了 N / 不活跃跳过 N / 未到期或退避跳过 N /
  失败 N）→ 第一眼就能看出"是不是全查了""是不是被限流了"

## Capabilities

### Modified Capabilities

- `daemon-lifecycle`: serve 的日志必须落盘；额度相关的行必须带 ASCII 标记

## Impact

- **改动** `cmd/cc-launcher/probe.go`：`forkDaemon` 把 stdout/stderr 接到日志文件
- **改动** `cmd/cc-launcher/serve.go`：额度日志加 `[quota]` 前缀
- **改动** `internal/quota/refresh.go`：每轮打一行汇总（区分"不活跃跳过"与
  "未到期/退避跳过"）
- 风险：日志文件可能含本机路径 → 0600；文件会增长 → 2MB 截断
