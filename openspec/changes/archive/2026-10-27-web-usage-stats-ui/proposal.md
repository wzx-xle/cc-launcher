## Why

用量统计目前只有终端一条路：`ccl stats` 打印文本表格，用户必须切到终端才能看到 token / cost 消耗。桌面端、远程开发、投屏汇报这些场景都不适用，需要一个能在浏览器里直接看的看板。

同时探查现有实现时发现一个更底层的空洞：Claude Code 的 OTel payload 里**根本没有 `cwd` 字段**（`internal/collector/parser.go` 明确注释 "provider / cwd Claude Code 推送中不存在"），`telemetry.db` 里 46 条 session 的 `cwd` 全是空串。这让 `ccl stats` 的 `PROJECT` 列恒空、`--by-project` 聚成一行、`--project <子串>` 恒返回空——README 与 `spec/stats-query` 承诺的三个能力在真实数据上全部是空壳。做看板之前必须先把这个维度补上，否则"按项目看占比 / 按项目筛选"做不出来。

## What Changes

**A. 补齐 session 的 cwd 与 provider 维度（前置修复）**

- **主路径**：`ccl` 启动 `cc` 时，把当次 provider 名、工作目录、provider 端点 host 写进子进程环境的 `OTEL_RESOURCE_ATTRIBUTES`；Claude Code 会把它们作为 OTLP payload 的 resource attributes 推送；daemon 从 resource attributes 读取，写入 `sessions.provider` / `sessions.cwd` / `sessions.provider_endpoint`。**逐次 launch 的真值**，不依赖反查命中率。
- **兜底路径**：payload 里没有 `ccl.cwd` 时（直接 `claude` 启动的会话、上下文注入之前的历史行），用 `session.id` 反查 Claude Code 的会话文件 `~/.claude/projects/<编码cwd>/<session-id>.jsonl`，读取其中记录的 `cwd` 字段。
- 提供一次性历史回填，把既有 session 行的 `cwd` 补上（实测当前库覆盖率 29/46 = 63%，其余是 Claude Code 已清理的旧会话，按"未知项目"处理）。
- 连带修复 `ccl stats` 的 `PROJECT` 列、`--by-project` 聚合、`--project <子串>` 过滤三条既有能力的空壳问题；`PROVIDER` 列与 `--by-provider` 聚合同样从空壳变可用，并新增 `--provider <子串>` 过滤。

**B. daemon 上新增看板路由**

- 在 daemon 已监听的 `:4318` 上新增 `/ui/*`（页面与静态资源）与 `/api/stats`（JSON）路由，不新开端口、不新增进程。
- 访问控制：token 鉴权。token 首次启动自动生成（32 字节随机 hex），存 `~/.cc-launcher/web-token`（0600）。访问 `/ui?token=<tok>` 校验通过后种一个 HttpOnly session cookie（HMAC 签名，带签发时间与有效期），随后重定向到不带 token 的 `/ui`，避免 token 留在浏览器历史 / Referer 里。

**C. 新增 `ccl web` 入口**

- `ccl web`：确保 daemon 在跑（不在则 fork），打印带 token 的看板 URL。
- `ccl web --rotate`：轮换 token（旧 token 与其派生出的所有 session cookie 立即失效）。
- `ccl serve --status` 追加一行看板 URL。

**D. 看板页面内容**

- 顶部 KPI 卡片：总 input / output / cache token、总 cost、session 数。
- 趋势图：按时间桶的 token 消耗（堆叠柱）与 cost 走势，粒度随时间窗口在 hourly / daily 之间自动切换。
- 占比：按项目、按模型的 token 与 cost 占比。
- 明细表：与 `ccl stats` 终端表格一一对应。
- 交互：时间窗口切换（24h / 7d / 30d / 90d）、项目与模型筛选、手动刷新按钮、自动轮询刷新（可关）。

**E. 查询层扩展**

- `stats.Query` 新增按 model 聚合与 model 过滤能力（供看板的"按模型占比"与模型筛选使用）。
- **引入 provider 维度**：provider 真值来自每次 `ccl` 启动时随 export 一起送达的 launch 上下文（见 A），因此 provider 是**逐次 launch 的快照**而非"当前激活的 provider"。新增 `--provider <子串>` 过滤与看板的 provider 筛选 / 占比。
- **不使用**"当前激活的 provider"去标注历史 session——那是假数据。直接 `claude` 启动的会话 provider 显示"未知"。

**F. 前端资源**

- `go:embed` 内嵌 HTML / CSS / JS，零新增依赖（无前端框架、无 CDN 图表库），图表用手写 SVG。保持单文件二进制与离线可用。

**明确的非目标（本次不做）**

- 不给 OTel ingest 端点（`/v1/metrics`）加鉴权——`:4318` 目前绑 `0.0.0.0`，局域网上可被灌入伪造 metrics，是**既存**风险；本次保持范围干净，该风险单独记档。
- 不做多用户 / 权限体系、不做历史数据导入、不做 PWA / 移动端原生应用、不做图表导出。

## Capabilities

### New Capabilities

- `web-usage-stats`: 看板的 HTTP 面——`/ui` 与 `/api/stats` 路由、token 鉴权与 session cookie、`ccl web` 入口、页面内容（KPI 卡片 / 趋势图 / 占比 / 明细表）与前端资源内嵌。
- `session-context-attrs`: launch 上下文的搬运机制——`ccl` 通过 `OTEL_RESOURCE_ATTRIBUTES` 把 provider / cwd / 端点 host 编进 OTLP payload 的 resource attributes，daemon 从 payload 读取并落到 session 行；含 percent-encoding 规则、settings.json 覆盖的降级约定、"空值不覆盖非空"的持久化规则。
- `session-cwd-enrichment`: session cwd 的兜底补采集机制——用 session id 反查 Claude Code 会话文件取 cwd，写入 `sessions.cwd`，以及历史 session 行的一次性回填。

### Modified Capabilities

- `telemetry-storage`: `sessions.cwd` / `sessions.provider` 由"恒为空串"改为"尽力补齐真实值"；新增 `sessions.provider_endpoint` 列及其幂等迁移；新增回填要求与"查不到即留空"的降级约定。
- `stats-query`: 新增按 model 聚合与 model 过滤、新增 `--provider` 过滤；`--by-project` / `--project` / `--by-provider` 的既有 scenario 从"文档承诺"变为"真实可用"（依赖 cwd / provider 补齐）。
- `cli-help`: 顶层帮助新增 `web` 子命令条目。
- `cli-shell-completion`: 新增 `ccl web` 的补全候选。

## Impact

- **新增** `internal/web/`：`server.go`（路由与 API）、`auth.go`（token 生成 / 校验 / session cookie）、`assets/`（内嵌前端资源）。
- **新增** `internal/sessiondir/`（或等价工具包）：session id → cwd 的反查与索引。
- **新增** launch 上下文的编码 / 组装工具（provider / cwd / 端点 host → percent-encoded `OTEL_RESOURCE_ATTRIBUTES` 值）。
- **改动** `internal/collector/parser.go` / `store.go`：从 resource attributes 读取 provider / cwd / 端点 host；写 session 行时空值不覆盖非空；`sessions` 表新增 `provider_endpoint` 列（幂等迁移）。
- **改动** `internal/collector/server.go`：`ServerOptions` 增加路由挂载入口（由 `cmd/cc-launcher` 注入 `internal/web`，collector 不反向依赖 web）。
- **改动** `internal/stats/query.go`：model 聚合 / 过滤、provider 过滤。
- **改动** `cmd/cc-launcher/`：`serve.go`（挂载路由、status 打印 URL）、新增 `web.go`（`ccl web` 子命令）、`main.go`（子命令分发、顶层帮助、启动前注入 launch 上下文）、`completion.go`（补全）。
- **依赖**：无新增 Go 依赖（`go:embed` + 标准库 `net/http` / `crypto/hmac`）。
- **数据**：`telemetry.db` 新增一列 `sessions.provider_endpoint`（幂等迁移，不动其它列）；新增 `~/.cc-launcher/web-token`。
- **行为变更**：`ccl stats` 的 `PROJECT` / `PROVIDER` 列与 `--by-project` / `--project` / `--by-provider` 从"恒空"变为"有数据"，属修复而非破坏。
