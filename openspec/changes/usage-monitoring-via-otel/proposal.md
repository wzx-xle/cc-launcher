## Why

ccl-launcher 现在能启动 Claude Code，但对"用了多少"完全无感知。用户希望看到每个 provider 的 token / 成本 / session 数。Claude Code v2.1.x 已经原生支持 OTel 推送（`CLAUDE_CODE_ENABLE_TELEMETRY=1`），本机 `~/.claude/settings.json` 已开启——所以 ccl 不必再从 stdout 抓数据，只需要做 **OTel 接收端**。

本次实现"用量监控"路径，与"provider 余额查询"是独立 feature（见 sibling change `provider-quota-check`）。两者数据源不同、用途互补，刻意分开。

## What Changes

- **新增 `internal/collector` 包**：OTLP gRPC server 监听 `:4317`，解析 metrics / logs / traces，落 SQLite
- **新增 `internal/stats` 包**：从 SQLite 读聚合，提供查询 API
- **新增 `cmd/cc-launcher/serve.go`**：`ccl serve` 子命令入口，daemon 模式
- **`cmd/cc-launcher/main.go` 扩展**：每次 `ccl` 启动时探活 `:4317`，失败则 fork `ccl serve` 后台进程；fork 后阻塞 ≤ 3s 等端口就绪；超时则降级（提示但不阻塞 exec Claude Code）
- **`ccl` 启动时自动写 settings.json env**：`CLAUDE_CODE_ENABLE_TELEMETRY=1` + `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317`（持久，用户改不到）；如已存在不动
- **新增 `ccl stats` 子命令**：从 daemon HTTP API 拉聚合，默认 last 7 days，支持 `--by-provider` / `--since <date>` / `--json`
- **新增 `ccl stop` 子命令**：SIGTERM daemon，优雅关 SQLite
- **不动 jsonl 扫描**：OTel 是更权威的数据源，jsonl 解析全砍

## Capabilities

### New Capabilities
- `otel-receiver`: gRPC server 监听 OTLP `:4317`，解析 metrics / logs / traces 三类信号；启动时端口被占报错退出（不让 Claude Code 推给别的服务）
- `telemetry-storage`: SQLite 单文件 `~/.cc-launcher/telemetry.db`，三张表（sessions / metrics / events）；启动时幂等建表；关闭时 flush
- `stats-query`: daemon HTTP `:4319` 暴露 `/stats`、`/stats/by-provider`；CLI `ccl stats` 经 HTTP 调 daemon；空库返回零；离线返回错误
- `daemon-lifecycle`: 探活 + fork + 阻塞启动 ≤ 3s；端口冲突报错；`ccl stop` SIGTERM 优雅关
- `claude-code-env-config`: ccl 启动时若 settings.json 缺 `CLAUDE_CODE_ENABLE_TELEMETRY=1` / `OTEL_EXPORTER_OTLP_ENDPOINT` 自动写入

### Modified Capabilities
<!-- 无现有 capability 受影响 -->

## Impact

- 代码：新增 `internal/collector` / `internal/stats` / `cmd/cc-launcher/serve.go`；扩展 `main.go`；扩展 `internal/config` 写 settings.json
- 依赖：新增 `google.golang.org/grpc` + `go.opentelemetry.io/proto/otlp` + `modernc.org/sqlite`（纯 Go 避免 CGO）
- 用户行为：`ccl` 首次使用会 fork 一个后台 daemon，监听 `:4317`；需要时 `ccl stop` 手动关
- 体积影响：+约 5-8 MB（gRPC + protobuf + sqlite pure-go）
- 平台：Unix + Windows 都跑（gRPC / sqlite 跨平台）
- 兼容性：OTel 路径与现有功能（jsonl 扫描等）平行；现有调用方不变