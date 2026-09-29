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
- `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`

**注意**：**不强制写** `OTEL_LOGS_EXPORTER`。如果用户已配 logs 相关设置，ccl 保留原值——daemon 的架构（不解析 logs）已经保护本地隐私，不需要通过配置禁 logs。

**理由**：Claude Code 优先读 settings.json env；持久；用户原配置功能完整。

**备选 shell env 变量**：临时，shell 重启即失效。**否决**。

### 5. metrics 入 SQLite；logs/traces 仅转发不入库

daemon 实现三个 HTTP endpoint：
- `POST /v1/metrics` → 解析 protobuf → 写 SQLite → 转发（如果 forwarder 启用）
- `POST /v1/logs` → **不解析**，仅转发（如果 forwarder 启用）
- `POST /v1/traces` → **不解析**，仅转发（如果 forwarder 启用）

**为何接受 logs/traces 而不丢弃**：
- 用户的原配置 `OTEL_LOGS_EXPORTER=otlp` / `OTEL_EXPORTER_OTLP_TRACES` 是有意义的——他们想看
- 如果 daemon 对 `/v1/logs` 返回 404，Claude Code 会重试几次后丢弃 → 用户**失去 logs 功能**
- 接受并转发保留了用户原始功能；本地隐私靠"不解析"维持（不写盘）

**本地隐私的真正保证**：daemon **永远不解析 logs/traces、不写 SQLite**——只把原始 bytes 转发出去。

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

### 8. claude-code 的 prompt / response 内容**永不**入库（**仅 metrics 入库**）

daemon **不解析 logs/traces 的内容**——只把原始 bytes 转发出去。SQLite 永远没有 prompt 文本。

**理由**：本地 SQLite 是非加密数据库；万一被其它进程读取，prompt 是隐私敏感。**本地不存 logs/traces 是不可绕过的硬约束**。

**用户原配置不受影响**：用户配 `OTEL_LOGS_EXPORTER=otlp` 仍能让 logs 推到他的远程 collector——只是不经过 ccl 的本地 SQLite。

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

### 11. daemon 永不自动退出（**永远跑直到显式 `ccl stop`**）

daemon 没有 idle timeout / 自动退出机制。它跑起来后**永远在**：

- 直到 `ccl stop` SIGTERM 关闭
- 直到 SIGKILL / 系统关机
- 直到 panic / crash（用户需 `ccl stop` 后手动 fork 恢复）

**为什么不加自动退出**：
- 个人用户真实模式：跑 session → 读输出（可能 30 分钟）→ 继续交互 → 走开 → 回来
- 任何 "空闲 X 分钟退出" 都会**在某次 pause 期间误杀 daemon**
- 误杀后：Claude Code 仍跑 → OTel 推送失败 → **session stats 永久丢失**（直到下次 `ccl`）
- 永不断 daemon：占内存 + 端口，但 stats 永远完整

**接受 trade-off**：daemon 长期占用 localhost:4318 + ~10MB 内存 → 个人 CLI 工具的合理成本。

**用户主动管理**：`ccl stop` 随时可关。

### 12. SIGTERM 优雅退出序列（**5 秒硬上限**）

daemon 收到 SIGTERM 时按顺序执行：

```
1. HTTP handler: `/v1/metrics` 立即返回 503
   （让 in-flight 请求完成，新请求被拒）
2. 等 async write queue 清空
   （极端情况下 10k events × flush time）
3. SQLite WAL checkpoint + Close
4. 删 pidfile
5. exit 0
```

**5 秒硬上限**：超时则 ccl stop 发 SIGKILL，剩余 queue 数据丢失。

**理由**：5 秒够 10k events 入库（WAL fsync 也够），超时就该用 SIGKILL。

### 13. Windows 没有 PGID 概念

Windows 上 `syscall.Kill(-pid, SIGTERM)` 不可用（没有 PGID）。`ccl stop` 在 Windows 上**直接按 PID kill**，不传负数。

**实现方式**：
- `cmd/cc-launcher/stop_unix.go`：`syscall.Kill(-pid, syscall.SIGTERM)`（PGID kill）
- `cmd/cc-launcher/stop_windows.go`：`syscall.Kill(pid, syscall.SIGTERM)`（PID kill；Windows 上 syscall.Kill 实际走 TerminateProcess）
- build tag 区分

**避免**跨平台 hard dep：`golang.org/x/term` 之类仅 Unix 的包不进 Windows 构建。

### 14. 自动检测 + 转发（**OTel collector 模式**）

daemon **同时是 OTel server（收 Claude Code）和 OTel client（推远程）**。本地 + 远程两不误。

**触发条件**：daemon 启动时检查自己的 env：
- 若 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 非空且非 localhost → 启用 forwarder
- 否则 → 不启用

**forwarder 行为**：
- 收 metrics：**写本地 SQLite** + **异步转发**（如果启用）
- 收 logs：**不写本地** + **异步转发**（如果启用）——保留用户 logs 功能
- 收 traces：**不写本地** + **异步转发**（如果启用）
- 三个 endpoint 都复用 daemon 的 HTTP client 连接池
- **失败策略**：log stderr 一行 + drop（**不重试、不排队**——决策 18）

**转发路径**：
```
Claude Code → POST localhost:4318 (ccl 改写的 endpoint)
  ├─→ /v1/metrics → 解析 → 写 SQLite → POST to backup (async)
  ├─→ /v1/logs → (no parse) → POST to backup (async)
  └─→ /v1/traces → (no parse) → POST to backup (async)
```

### 15. 备份所有 OTel 相关 key（**`CC_ORIG_<KEY>`**）

ccl 启动时**对所有 5 个 OTel key** 做"备份 + 修改"两步：

| 原 key | ccl 处理 |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 备份到 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`，**改写为 `http://localhost:4318`** |
| `OTEL_LOGS_EXPORTER` | 备份到 `CC_ORIG_OTEL_LOGS_EXPORTER`，**保留原值**（用户配 `otlp` 就转发 logs 到 backup） |
| `OTEL_METRICS_EXPORTER` | 备份到 `CC_ORIG_OTEL_METRICS_EXPORTER`，**保留原值** |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | 备份到 `CC_ORIG_OTEL_EXPORTER_OTLP_PROTOCOL`，**保留原值** |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | 备份到 `CC_ORIG_CLAUDE_CODE_ENABLE_TELEMETRY`，**保留原值** |

**新增 key**（用户原本没有）→ 不备份，直接写入默认值。

**`CC_ORIG_*` 是 ccl 的命名空间**——用户不会主动写这些 key。如果用户已经写过（手贱），ccl 不动它（避免覆盖用户备份）。

**实现位置**：`cmd/cc-launcher/envconfig.go`（独立函数 `backupAndInjectOTel(env map[string]string)`）。

### 16. `ccl restore` 子命令（**默认存在**）

读 `~/.claude/settings.json` 的 env 字段：
- 对每个 `CC_ORIG_*` 键：把值写回对应原 key；删除 `CC_ORIG_*` 键
- 对 `CLAUDE_CODE_ENABLE_TELEMETRY=1`（ccl 加的）：若用户没原值，**也删**
- 对 `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`：若用户没原值，**也删**

**默认行为是"全清"**。要保留部分 ccl 注入时，加 `--keep-injected` flag（后续）。

**理由**：用户的"恢复"语义是"回到 ccl 之前"，所以默认最干净。

### 17. Forwarder auth 走 env（**`CC_FORWARD_AUTH_HEADER`**）

daemon 启动时检查 `CC_FORWARD_AUTH_HEADER`：
- 非空 → 加到 forwarder 的每个 POST 请求头
- 空 → 不加 auth header

用户自己设 auth，例：
```
CC_FORWARD_AUTH_HEADER="Authorization=Bearer my-company-token"
```

daemon **不复用 `ANTHROPIC_AUTH_TOKEN`**——那是 provider 的 key（用于 Anthropic Messages API），不是 collector 的 key（用于 OTel 推送）。

### 18. Forwarder 失败 = log + drop（**不重试不排队**）

失败路径：
1. net/http POST 返回非 2xx OR 超时（5s）
2. daemon 在 stderr 打印一行（带 target URL 和 status code，但**不打印 auth**）
3. metric 已落本地 SQLite，**本地 stats 完整**

**理由**：
- 个人工具，重试逻辑不值
- 本地已持久化，remote 偶发丢失可接受
- 简单 → 易测

### 19. Forwarder 不递归（**避免 loop**）

若 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 是 localhost 或 127.0.0.1 → daemon **拒绝启用 forwarder**（避免 daemon 自己 POST 自己）。

**理由**：哪怕用户写错，写错的结果应该是"明显坏"（本地 stats 仍然完整），不是"静默丢数据"。

## Risks / Trade-offs

- [体积 +2-3 MB] HTTP + protobuf + sqlite pure-go → 接受；个人工具可接受
- [WAL 文件] `~/.cc-launcher/telemetry.db-wal` / `-shm` 与 db 并存 → sqlite 自动管理
- [daemon 启动开销] 首次 `ccl` 启动多 ~500ms（fork + 端口就绪），后续 `ccl` < 50ms（探活命中）
- [端口被占误报] 用户若有其它 OTel 服务占 :4318，ccl 会拒绝启动 → 缓解：help 明确说明可用 `OTEL_EXPORTER_OTLP_ENDPOINT` 覆盖
- [SQLite 文件增长无界] 缓解：task 加定期 VACUUM / 保留 N 天
- [daemon fork 失败] 数据丢本次 session，下次 ccl 重试 → 缓解：spec 写明降级语义
- [OTel 推送失败静默] 用户不知情 → 缓解：`ccl stats` 报错"daemon 未运行"
- [daemon 永驻内存] ~10MB 占用 + 占 localhost:4318 → 个人 CLI 工具可接受；想关掉 `ccl stop`
- [Forwarder 失败丢数据] remote collector 挂了 → 转发失败的数据丢；本地 SQLite 仍完整 → 用户**只看本地 stats 就够**
- [Forwarder 阻塞 daemon] 决策 18 异步 + drop，daemon 不阻塞
- [Forwarder loop] 决策 19 防 localhost 循环

## Migration Plan

无状态迁移。`ccl` 行为变化：
- 之前：直接 exec Claude Code
- 现在：先探活 daemon、可能 fork、注入 settings.json env、再 exec Claude Code
- 老用户透明（功能行为不变，只是多了用量统计）

## Open Questions

- TUI 实时面板（`ccl stats --tui`）做不做？决定：留后续
- 多 Claude Code 实例并发时（罕见）daemon 怎么处理？决定：daemon 单实例；第二个 ccl 会因为 daemon 已起而跳过 fork
- 是否暴露 `ccl stats --export csv`？决定：留后续
- 是否加用户可配的 idle-timeout？决定：**不做**（决策 11；用户想 stats 完整就别动）