## 1. OTel receiver (基础)

- [ ] 1.1 新增 `internal/collector/server.go`：gRPC server 监听 `:4317`，实现 OTLP MetricsService / LogsService / TraceService 的 Export RPC
- [ ] 1.2 新增 `internal/collector/parser.go`：protobuf → domain 对象（Metric{Name, Value, Labels, Timestamp} / Log / Span）
- [ ] 1.3 `internal/collector` 加单元测试：构造测试用 OTLP ExportRequest，验证解析后的 domain 对象字段正确

## 2. SQLite 存储

- [ ] 2.1 新增 `internal/collector/store.go`：用 `modernc.org/sqlite` 驱动；建表 SQL 幂等（`CREATE TABLE IF NOT EXISTS`）；索引 `(session_id, captured_at)`
- [ ] 2.2 store.go 实现 `WriteMetric` / `WriteEvent` / `WriteSession` 异步队列（chan + worker）
- [ ] 2.3 store.go 实现背压：队列 >10k 时丢最旧 + 计数器
- [ ] 2.4 store.go 实现 `Close()`：flush + 优雅关
- [ ] 2.5 store 单元测试：写入 / 查询 / 异步关闭

## 3. Stats 查询 API

- [ ] 3.1 新增 `internal/stats/query.go`：从 SQLite 拉 sessions + metrics，按窗口 / provider 聚合
- [ ] 3.2 新增 `internal/stats/format.go`：表格 / JSON 输出
- [ ] 3.3 daemon 暴露 HTTP `:4319` 端点：`/stats` / `/stats/by-provider` / `/health`
- [ ] 3.4 stats 单元测试：聚合正确性

## 4. Daemon 生命周期

- [ ] 4.1 新增 `cmd/cc-launcher/serve.go`：`ccl serve` 入口，组装 collector server + stats API + SQLite
- [ ] 4.2 `ccl serve` 启动时写 `~/.cc-launcher/daemon.pid`，SIGTERM 时删
- [ ] 4.3 `ccl serve` 启动时若 `:4317` 被占，报错退出（明确提示 `OTEL_EXPORTER_OTLP_ENDPOINT` 覆盖）
- [ ] 4.4 `cmd/cc-launcher/main.go`：每次 `ccl` 启动前探活 `:4317`（200ms timeout）
- [ ] 4.5 main.go：探活失败时 fork `ccl serve` 后 disown，等端口就绪（≤ 3s）
- [ ] 4.6 main.go：fork 超时则降级（提示但不阻塞 exec Claude Code）
- [ ] 4.7 新增 `ccl stop` 子命令：读 pidfile → SIGTERM → 等 ≤ 5s → SIGKILL

## 5. Claude Code env 配置

- [ ] 5.1 `cmd/cc-launcher/main.go`：在 exec Claude Code 前检查 `~/.claude/settings.json`
- [ ] 5.2 缺 `CLAUDE_CODE_ENABLE_TELEMETRY=1` / `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_METRICS_EXPORTER` / `OTEL_LOGS_EXPORTER` / `OTEL_EXPORTER_OTLP_PROTOCOL` → 自动写
- [ ] 5.3 已有键不动；用 `internal/config/ordered.go` 保持 JSON 字段顺序
- [ ] 5.4 若 `OTEL_EXPORTER_OTLP_ENDPOINT` 是非 localhost（如远程 collector），不拉起本地 daemon

## 6. CLI 命令

- [ ] 6.1 `ccl stats` 子命令：HTTP GET `:4319/stats`，表格输出
- [ ] 6.2 `ccl stats --since <date>` / `--by-provider` / `--json`
- [ ] 6.3 `ccl stop` 子命令（task 4.7 已列）
- [ ] 6.4 daemon 离线时 `ccl stats` 报错并提示"run ccl to start one"
- [ ] 6.5 `ccl stats` 单元测试：mock HTTP server 验证请求与输出格式

## 7. 集成与依赖

- [ ] 7.1 `go.mod` 加 `google.golang.org/grpc` + `go.opentelemetry.io/proto/otlp` + `modernc.org/sqlite`
- [ ] 7.2 验证 `GOOS=windows go build ./...` 通过（gRPC + sqlite 跨平台）
- [ ] 7.3 README 加 "用量监控" 小节：如何启 daemon / 如何查 stats / 如何关

## 8. 验证

- [ ] 8.1 `go test ./...` 全绿
- [ ] 8.2 `GOOS=windows go build ./...` 通过
- [ ] 8.3 手动：`ccl` → daemon 起来 → `ccl stats` 看到当次 session
- [ ] 8.4 手动：故意占 `:4317` → `ccl serve` 报错退出
- [ ] 8.5 手动：`ccl stop` → daemon 优雅关
- [ ] 8.6 跑 24h 累积测试：sessions 数 / metrics 数 / db 文件大小

## 9. 提交

- [ ] 9.1 commit: feat(collector): OTel gRPC server + parser + SQLite store
- [ ] 9.2 commit: feat(daemon): ccl serve subcommand + HTTP :4319 query API
- [ ] 9.3 commit: feat(cli): ccl auto-starts daemon; ccl stats / ccl stop
- [ ] 9.4 commit: feat(env): inject OTEL_* into ~/.claude/settings.json
- [ ] 9.5 commit: docs: README 用量监控小节