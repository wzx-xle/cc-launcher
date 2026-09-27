## 1. OTel HTTP receiver（只 metrics）

- [ ] 1.1 新增 `internal/collector/server.go`：net/http server 监听 `:4318`，路由：
  - `POST /v1/metrics` → 解析 protobuf，写 SQLite
  - 其它路径（`/v1/logs`、`/v1/traces`、其它）→ HTTP 404
- [ ] 1.2 新增 `internal/collector/parser.go`：protobuf → domain 对象（Metric{Name, Value, Labels, Timestamp}）；只处理 `ExportMetricsServiceRequest`
- [ ] 1.3 `internal/collector` 加单元测试：用构造的 `ExportMetricsServiceRequest` 验证解析正确

## 2. SQLite 存储（WAL 模式）

- [ ] 2.1 新增 `internal/collector/store.go`：用 `modernc.org/sqlite` 驱动；建表 SQL 幂等（`CREATE TABLE IF NOT EXISTS`）；每次 open 立即 `PRAGMA journal_mode=WAL`
- [ ] 2.2 store.go 实现 `WriteSession` / `WriteMetric` 异步队列（chan + worker）
- [ ] 2.3 store.go 实现背压：队列 >10k 时丢最旧 + 计数器
- [ ] 2.4 store.go 实现 `Close()`：flush + 优雅关
- [ ] 2.5 store 单元测试：WAL 启用 / 写入 / 异步关闭
- [ ] 2.6 **schema 两张表**：sessions + metrics（无 events 表，因不接 logs/traces）

## 3. Stats 查询（直读 SQLite）

- [ ] 3.1 新增 `internal/stats/query.go`：从 SQLite 拉 sessions + metrics，按窗口 / provider / project 聚合
- [ ] 3.2 新增 `internal/stats/format.go`：表格 / JSON 输出
- [ ] 3.3 stats 单元测试：聚合正确性 + WAL 并发读（mock 同时写）

## 4. Daemon 生命周期

- [ ] 4.1 新增 `cmd/cc-launcher/serve.go`：`ccl serve` 入口，组装 HTTP server + SQLite
- [ ] 4.2 `ccl serve` 启动时写 `~/.cc-launcher/daemon.pid`，SIGTERM 时删
- [ ] 4.3 `ccl serve` 启动时若 `:4318` 被占，报错退出（明确提示 `OTEL_EXPORTER_OTLP_ENDPOINT` 覆盖）
- [ ] 4.4 `cmd/cc-launcher/main.go`：每次 `ccl` 启动前探活 `:4318`（200ms timeout）
- [ ] 4.5 main.go：探活失败时 fork `ccl serve`，**`SysProcAttr.Setpgid: true`** 让 daemon 在新进程组，stdin/stdout/stderr = nil，disown；等端口就绪（≤ 3s）
- [ ] 4.6 main.go：fork 超时 / daemon 退出 → **降级（仍 exec Claude Code，OTel 静默失败）**
- [ ] 4.7 新增 `ccl stop` 子命令：读 pidfile → SIGTERM → 等 ≤ 5s → SIGKILL
- [ ] 4.8 `ccl serve` 收到 SIGTERM 时按顺序执行：(1) HTTP `/v1/metrics` 返回 503；(2) 等 async queue 清空；(3) SQLite WAL checkpoint + Close；(4) 删 pidfile；(5) exit 0；全程 ≤ 5s
- [ ] 4.9 跨平台 stop：新建 `cmd/cc-launcher/stop_unix.go` 用 `syscall.Kill(-pid, SIGTERM)`（PGID）；新建 `cmd/cc-launcher/stop_windows.go` 用 `syscall.Kill(pid, SIGTERM)`（直接 PID）
- [ ] 4.10 daemon 单元测试：SIGTERM 期间 in-flight 请求完成、新请求 503、queue 清空、pidfile 删、exit 0

## 5. Claude Code env 配置（含备份与转发）

- [ ] 5.1 `cmd/cc-launcher/main.go`：在 exec Claude Code 前检查 `~/.claude/settings.json`
- [ ] 5.2 新增 `cmd/cc-launcher/envconfig.go`：`backupAndInjectOTel(env map[string]string) (newEnv map[string]string)` 函数——按 spec 表对 5 个 key 做"备份 + 修改"
- [ ] 5.3 `OTEL_LOGS_EXPORTER=none` 强制覆盖；其它 4 个 key 尊重用户原值（保留或仅在缺失时填默认）
- [ ] 5.4 用 `internal/config/ordered.go` 保持 JSON 字段顺序
- [ ] 5.5 单元测试 envconfig.go：每种 key 组合（含未设 / 已设 / CC_ORIG 已存在）正确产出
- [ ] 5.6 若 `OTEL_EXPORTER_OTLP_ENDPOINT` 是非 localhost（如远程 collector），不拉起本地 daemon

## 6. CLI 命令

- [ ] 6.1 `ccl stats` 子命令：**直接打开 SQLite**（无 HTTP），按 last 7 days 表格输出
- [ ] 6.2 `ccl stats --since <date>` / `--by-provider` / `--by-project` / `--project <substr>` / `--json`
- [ ] 6.3 `ccl stop` 子命令（task 4.7 已列）
- [ ] 6.4 SQLite 文件不存在时 `ccl stats` 报错并提示"run ccl to start one"
- [ ] 6.5 `ccl stats` 单元测试：用 t.TempDir() 准备 db，验证表格 / JSON 输出格式
- [ ] 6.6 **新增 `ccl restore` 子命令**：扫 `CC_ORIG_*` 还原 + 删 ccl 注入的默认 key；不可逆（spec 写明）

## 7. Forwarder（OTel client 角色）

- [ ] 7.1 `internal/collector/forwarder.go`：从 daemon env 读 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 和 `CC_FORWARD_AUTH_HEADER`；决定是否启用
- [ ] 7.2 拒 localhost 循环：endpoint 含 `localhost` 或 `127.0.0.1` → 不启用 + log warning
- [ ] 7.3 在 HTTP handler 中：metric 写完 SQLite 后 `go func()` 异步 POST 原始 protobuf bytes（不重编码）
- [ ] 7.4 失败策略：5s timeout / 非 2xx → log stderr 单行（不打印 auth）+ drop
- [ ] 7.5 单元测试 forwarder.go：用 `httptest.Server` mock 200 / 5xx / timeout；验证 log 行为

## 8. 集成与依赖

- [ ] 8.1 `go.mod` 加 `go.opentelemetry.io/proto/otlp/collector/metrics/v1` + `modernc.org/sqlite`（**无 gRPC 依赖**）
- [ ] 8.2 验证 `GOOS=windows go build ./...` 通过（HTTP / sqlite 跨平台）
- [ ] 8.3 README 加 "用量监控" 小节：如何启 daemon / 如何查 stats / 如何关 / 隐私默认 / `ccl restore` 用法

## 9. 验证

- [ ] 9.1 `go test ./...` 全绿
- [ ] 9.2 `GOOS=windows go build ./...` 通过
- [ ] 9.3 手动：`ccl` → daemon 起来 → 跑 Claude Code session → `ccl stats` 看到当次 session
- [ ] 9.4 手动：故意占 `:4318` → `ccl serve` 报错退出，**但 `ccl` 仍 exec Claude Code**
- [ ] 9.5 手动：`ccl stop` → daemon 优雅关
- [ ] 9.6 手动：调 `POST :4318/v1/logs` → 404，不写盘
- [ ] 9.7 手动：用户 settings.json 有 `OTEL_EXPORTER_OTLP_ENDPOINT=remote.com` → ccl 自动备份 + daemon 自动转发；`ccl stats` 本地有数据
- [ ] 9.8 手动：`ccl restore` → settings.json 恢复原状（`CC_ORIG_*` 删、原值写回）
- [ ] 9.9 跑 24h 累积测试：sessions 数 / metrics 数 / db 文件大小

## 10. 提交

- [ ] 10.1 commit: feat(collector): HTTP :4318 OTel metrics receiver + parser + WAL store
- [ ] 10.2 commit: feat(stats): ccl stats 直接读 SQLite + 表格 / JSON 输出
- [ ] 10.3 commit: feat(daemon): ccl serve + Setpgid fork + ccl stop + graceful SIGTERM
- [ ] 10.4 commit: feat(cli): ccl auto-starts daemon; ccl stats / ccl stop / ccl restore + 降级语义
- [ ] 10.5 commit: feat(env): 备份 + 注入 5 个 OTEL env 键到 settings.json（logs=none）
- [ ] 10.6 commit: feat(forwarder): 自动检测 + 转发到 CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT
- [ ] 10.7 commit: docs: README 用量监控小节（隐私默认 + 端口覆盖 + ccl restore + 转发）