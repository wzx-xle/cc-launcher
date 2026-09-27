## Context

Claude Code v2.1.x 已经原生推送 OTel（`CLAUDE_CODE_ENABLE_TELEMETRY=1` + OTLP endpoint）。`~/.claude/settings.json` 里本机已开。Claude Code 推送的 metrics 已包含 token 计数、cost、session 数、API 请求、工具决策——这些正是 ccl 用户想看的"花在哪了"。

ccl-launcher 需要变成一个**轻量本地 OTel 接收端**：daemon 模式监听 OTLP metrics、解析、落 SQLite；CLI 直接读 SQLite 聚合查询。

## Goals / Non-Goals

**Goals:**
- 用户跑 `ccl` 一次后就能在 `ccl stats` 看到当次 session 的 token / cost
- 全自动启动 daemon（用户感知不到）
- 数据 100% 本地（无云依赖）
- 端口冲突时报错退出（不污染他人）
- 只接 metrics，**不接 logs / traces**（避免 prompt 内容落本地 SQLite）

**Non-Goals:**
- 不接 OTel logs / traces（prompt 隐私 + 体积考虑）
- 不做远程 collector / vendor 推送
- 不做实时 dashboard（TUI 滚动面板留给后续）
- 不做 jsonl fallback（OTel 是更权威数据源）
- 不做 HTTP API 给 `ccl stats` —— daemon 只写 SQLite，CLI 直读

## Decisions

### 1. OTLP 协议用 HTTP/protobuf :4318（仅 metrics）

Claude Code 配置 `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf` + `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` 后推 metrics。

**理由**：
- **HTTP 比 gRPC 简单**：标准 `net/http` 服务端即可，无 gRPC 包依赖，体积省 ~2MB
- **protobuf 比 JSON 更紧凑**：流量极小（个人工具），但 schema 自描述
- **:4318 是 OTel HTTP 端口标准**：与官方约定一致

**只接 metrics，不接 logs / traces**：
- metrics 含 token / cost / session 全信息 → **满足所有需求**
- logs 含 user prompt → **本地 SQLite 明文存储是隐私风险**（spec 强制禁）
- traces 是调试工具 → 个人用户不需要

**备选 gRPC :4317**（原方案）：多 ~2MB 体积，protobuf 解析已可工作，gRPC overhead 不必要。**否决**。

### 2. SQLite 用 modernc.org/sqlite（**WAL 模式**）

纯 Go 实现，无需 CGO；编译链简单。

**必须开 WAL**：
- daemon 单写、CLI 读 → WAL 让读不阻塞写
- `ccl stats` 与 daemon 并发时不锁表
- 创建表后立即 `PRAGMA journal_mode=WAL`

**备选 mattn/go-sqlite3**：需要 CGO，跨平台编译复杂。**否决**。

### 3. Daemon 由 ccl 按需拉起，不上 systemd

每次 `ccl` 启动时：
1. 探活 `localhost:4318`（TCP connect，超时 200ms）
2. 失败 → fork `ccl serve`（`os/exec.Cmd`，**`SysProcAttr.Setpgid: true`** 让 daemon 在新进程组）
3. daemon 启动后阻塞等待端口就绪（最多 3s）
4. 超时 → 提示"daemon 没起来"**但仍 exec Claude Code**（降级；Claude Code 推 OTel 失败但不报错）

**为什么 Setpgid**：ccl `syscall.Exec` Claude Code 后替换自身进程。如果 daemon 与 ccl 同进程组，ccl 替换 Claude Code 后新进程 PGID 不变，daemon 仍能存活但和 Claude Code 同 PGID，未来 ccl `ccl stop` 发 SIGTERM 可能误杀 Claude Code。新进程组隔离干净。

**理由**：用户感知零；不需要单独管 systemd 单元；个人工具。

**备选 systemd / launchd**：多一份配置、多一个维护负担。**否决**。

### 4. settings.json env 注入而非 shell env 变量

ccl 启动时若 `~/.claude/settings.json` 的 `env` 字段缺以下键，自动写入（已存在不动）：
- `CLAUDE_CODE_ENABLE_TELEMETRY=1`
- `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`
- `OTEL_METRICS_EXPORTER=otlp`
- `OTEL_LOGS_EXPORTER=none`（**显式禁用 logs，避免误启 prompt logging**）
- `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`

**理由**：Claude Code 优先读 settings.json env；持久；显式禁用 logs 是隐私默认。

**备选 shell env 变量**：临时，shell 重启即失效。**否决**。

### 5. 只接 metrics，logs/traces 不入 daemon

daemon **只实现 OTLP `/v1/metrics` HTTP endpoint**。Logs/Traces 即使被推也丢弃（HTTP 404）。

**理由**：
- 体积小：HTTP server 路由只一条
- 隐私强：本地 SQLite 永远不会有 prompt 内容
- 简单：测试只需要构造 metric ExportRequest

### 6. 两张 SQLite 表 + WAL

- `sessions(id TEXT PK, started_at INTEGER, ended_at INTEGER NULL, provider TEXT, cwd TEXT, model TEXT)`：每个 session 一行
- `metrics(id INTEGER PK, session_id TEXT, name TEXT, value REAL, labels TEXT JSON, captured_at INTEGER)`：每个 metric 数据点一行

索引：
- `metrics(session_id, captured_at)` —— 按 session 时间排序
- `sessions(cwd, started_at)` —— project 维度查询

**为什么删了 events 表**：原 spec 包含 `events` 表存 logs/traces。**只接 metrics 后不需要 events 表**。

**理由**：sessions 是聚合维度；metrics 是事实数据；分开存便于 `ccl stats` 直读 SQLite。

**备选单表宽列**：JSON 解析开销、schema 演化麻烦。**否决**。

### 7. `ccl stats` 直接读 SQLite（**不通过 daemon HTTP**）

daemon 是**唯一写者**，`ccl stats` 是**只读读者**。两者不需要 IPC。

**理由**：
- **少一个端口**（`:4319` 整个砍掉）
- **少一个 HTTP API**（少写一半测试 + handler）
- **少一个失败模式**（daemon 死了 stats 也能查历史数据）

WAL 模式保证读不阻塞写：`ccl stats` 与 daemon 并发安全。

**风险**：跨机器？**不解决**——ccl-launcher 是单机工具。

**未来扩展**：TUI / web dashboard 可直接打开同一 SQLite 文件，无需 daemon。

### 8. claude-code 的 prompt / response 内容**永不**入库

`OTEL_LOGS_EXPORTER=none` 写死在 settings.json（决策 4）。即便 Claude Code 默认行为变了，ccl 用户也不会本地持久化 prompt 文本。

**理由**：本地 SQLite 是非加密数据库；万一被其它进程读取，prompt 是隐私敏感。**默认禁用 prompt logging 是不可绕过的硬约束**。

### 9. 端口冲突报错退出（不共享）

daemon 启动若 `:4318` 被占，立即报错退出，**不重用现有服务**。

**理由**：
- 如果别人也在跑 OTel collector，重用会让数据混在一起
- 显式报错让用户决定（kill 旧服务 / 改端口）

**备选 auto-reuse**：可能误投。**否决**。

### 10. daemon fork 失败降级（**exec Claude Code 仍执行**）

fork / 端口就绪超时 → ccl 仍 exec Claude Code。Claude Code 会**静默推 OTel 失败**（无 collector 接收），但**用户能正常使用 ccl**。下次 ccl 启动会再尝试拉 daemon。

**理由**：daemon 是**可选增强**，不是核心依赖。daemon 挂了不能让用户失去 ccl 主功能。

**spec 明确这一行为**，避免未来维护者误以为 daemon 是 hard dep。

## Risks / Trade-offs

- [体积 +2-3 MB] HTTP + protobuf + sqlite pure-go → 接受；个人工具可接受
- [WAL 文件] `~/.cc-launcher/telemetry.db-wal` / `-shm` 与 db 并存 → sqlite 自动管理
- [daemon 启动开销] 首次 `ccl` 启动多 ~500ms（fork + 端口就绪），后续 `ccl` < 50ms（探活命中）
- [端口被占误报] 用户若有其它 OTel 服务占 :4318，ccl 会拒绝启动 → 缓解：help 明确说明可用 `OTEL_EXPORTER_OTLP_ENDPOINT` 覆盖
- [SQLite 文件增长无界] 缓解：task 加定期 VACUUM / 保留 N 天
- [daemon fork 失败] 数据丢本次 session，下次 ccl 重试 → 缓解：spec 写明降级语义
- [OTel 推送失败静默] 用户不知情 → 缓解：`ccl stats` 报错"daemon 未运行"

## Migration Plan

无状态迁移。`ccl` 行为变化：
- 之前：直接 exec Claude Code
- 现在：先探活 daemon、可能 fork、注入 settings.json env、再 exec Claude Code
- 老用户透明（功能行为不变，只是多了用量统计）

## Open Questions

- TUI 实时面板（`ccl stats --tui`）做不做？决定：留后续
- 多 Claude Code 实例并发时（罕见）daemon 怎么处理？决定：daemon 单实例；第二个 ccl 会因为 daemon 已起而跳过 fork
- 是否暴露 `ccl stats --export csv`？决定：留后续