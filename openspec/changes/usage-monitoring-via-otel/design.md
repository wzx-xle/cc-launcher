## Context

Claude Code v2.1.x 已经原生推送 OTel（`CLAUDE_CODE_ENABLE_TELEMETRY=1` + OTLP endpoint）。`~/.claude/settings.json` 里本机已开。Claude Code 推送的 metrics / logs / traces 已包含 token 计数、cost、session 数、API 请求、工具决策——这些正是 ccl 用户想看的"花在哪了"。

ccl-launcher 需要变成一个**轻量本地 OTel 接收端**：daemon 模式监听 OTLP、解析、落 SQLite；CLI 查询聚合。

## Goals / Non-Goals

**Goals:**
- 用户跑 `ccl` 一次后就能在 `ccl stats` 看到当次 session 的 token / cost
- 全自动启动 daemon（用户感知不到）
- 数据 100% 本地（无云依赖）
- 端口冲突时报错退出（不污染他人）

**Non-Goals:**
- 不做远程 collector / vendor 推送
- 不做实时 dashboard（TUI 滚动面板留给后续）
- 不做 quota check（独立 change `provider-quota-check`）
- 不做 jsonl fallback（OTel 是更权威数据源）

## Decisions

### 1. OTLP 协议用 gRPC :4317

Claude Code 默认 endpoint 是 `:4317` gRPC（参见 [Claude Code Monitoring Docs](https://code.claude.com/docs/en/monitoring-usage)）。用户开 `CLAUDE_CODE_ENABLE_TELEMETRY=1` 后零配置即推到这里。

**备选 HTTP/protobuf :4318**：用户得手动设 `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`，不友好。**否决**。

### 2. SQLite 用 modernc.org/sqlite

纯 Go 实现，无需 CGO；编译链简单；Go 跨平台编译不会因 CGO 失败。

**备选 mattn/go-sqlite3**：需要 CGO，跨平台编译复杂。**否决**。

### 3. Daemon 由 ccl 按需拉起，不上 systemd

每次 `ccl` 启动时：
1. 探活 `localhost:4317`（TCP connect，超时 200ms）
2. 失败 → fork `ccl serve`（用 `syscall.Exec` 或 `os/exec` detach）
3. fork 后阻塞等待端口就绪（最多 3s）
4. 超时 → 提示"daemon 没起来"但不阻塞 exec Claude Code

**理由**：用户感知零；不需要单独管 systemd 单元；个人工具。

**备选 systemd / launchd**：多一份配置、多一个维护负担。**否决**。

### 4. settings.json env 注入而非 shell env 变量

ccl 启动时若 `~/.claude/settings.json` 的 `env` 字段缺 `CLAUDE_CODE_ENABLE_TELEMETRY=1` 或 `OTEL_EXPORTER_OTLP_ENDPOINT`，自动写入（已存在则不动）。

**理由**：Claude Code 优先读 settings.json env；用户改 shell env 是临时、不能持久；settings.json 是真理之源。

**备选 shell env 变量**：临时有效，ccl fork 子进程后 Claude Code 能继承，但重启 shell 即失效。**否决**。

### 5. 不做 quota check

本次 change 只做用量监控。quota check（DeepSeek / MiniMax 余额）是独立 change `provider-quota-check`，刻意分开。

### 6. 三张 SQLite 表

- `sessions(id TEXT, started_at INTEGER, ended_at INTEGER NULL, provider TEXT, cwd TEXT, model TEXT)`：每个 OTel `session.start` 事件一行
- `metrics(id INTEGER PK, session_id TEXT, name TEXT, value REAL, labels TEXT JSON, captured_at INTEGER)`：每个 metric 数据点一行
- `events(id INTEGER PK, session_id TEXT, type TEXT, payload TEXT JSON, captured_at INTEGER)`：log / trace 事件原始存储

索引：`(session_id, captured_at)` 联合主键保证顺序。

**理由**：sessions 是聚合维度；metrics / events 是事实数据；分开存便于 stats 查询只读 sessions + 必要时聚合 metrics。

**备选单表宽列**：每行一事件，宽列涵盖所有字段。**否决**：JSON 解析开销、schema 演化麻烦。

### 7. daemon 暴露独立 HTTP :4319 给 CLI 查询

CLI `ccl stats` 通过 HTTP 调 daemon 而不是直读 SQLite。理由：
- CLI 与 daemon 是独立进程，直读 SQLite 需要同一文件路径 + 并发锁
- HTTP API 让未来 TUI / web dashboard 也能复用

端口 `:4319` 不会与 OTLP `:4317` 冲突。

### 8. claude-code 的 prompt / response 内容不入库

Claude Code OTel logs 可包含 user prompt 与 assistant response 文本（`OTEL_LOG_USER_PROMPTS=1`）。ccl daemon **不存这些文本字段**，只存元数据（事件类型、时间、关联 session）。

**理由**：本地 SQLite 是非加密数据库；万一被其它进程读取，prompt 内容是隐私敏感。**默认禁用 prompt logging**（不主动开启 `OTEL_LOG_USER_PROMPTS`）。

### 9. 端口冲突报错退出（不共享）

daemon 启动若 `:4317` 被占，立即报错退出，**不重用现有服务**。理由：
- 如果别人也在跑 OTel collector，重用会让数据混在一起
- 显式报错让用户决定（kill 旧服务 / 改端口）

**备选 auto-reuse**：可能误投。**否决**。

## Risks / Trade-offs

- [体积 +5-8 MB] gRPC + protobuf + sqlite pure-go → 接受；个人工具可接受
- [SQLite 写入并发] ccl daemon 是唯一写者（Claude Code 单进程推），无并发风险
- [daemon 启动开销] 首次 `ccl` 启动多 ~500ms（fork + 端口就绪），后续 `ccl` < 50ms（探活命中）
- [端口被占误报] 用户若有其它 OTel 服务占 :4317，ccl 会拒绝启动 → 缓解：help 明确说明可用 `OTEL_EXPORTER_OTLP_ENDPOINT` 覆盖
- [SQLite 文件增长无界] 缓解：task 8.7 加定期 VACUUM / 保留 N 天

## Migration Plan

无状态迁移。`ccl` 行为变化：
- 之前：直接 exec Claude Code
- 现在：先探活 daemon、可能 fork、注入 settings.json env、再 exec Claude Code

老用户透明（功能行为不变，只是多了用量统计）。

## Open Questions

- TUI 实时面板（`ccl stats --tui`）做不做？决定：留后续
- 多 Claude Code 实例并发时（罕见）daemon 怎么处理？决定：daemon 单实例；第二个 ccl 会因为 daemon 已起而跳过 fork
- 是否暴露 `ccl stats --export csv`？决定：留后续