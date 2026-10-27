## Context

`ccl` 目前的用量链路是完整的：Claude Code 把 OTel metrics 推到 `:4318`，daemon 落盘到 `~/.cc-launcher/telemetry.db`，`ccl stats` 直读该文件打印文本表格。查询层（`internal/stats`）质量不错——窗口语义清晰、桶聚合与明细同窗口对齐、`--json` 已经是结构化输出。

缺的是两件事：

**1. 没有浏览器入口。** 用量数据只能切终端看。

**2. 项目维度是空的。** Claude Code 的 OTel payload 只有 `host.arch / os.type / os.version / service.name / service.version / terminal.type / user.id`（resource attrs）和 `session.id / model / type / start_type / query_source`（datapoint attrs）——**没有 cwd，也没有 provider**。`internal/collector/parser.go` 写 session 行时硬编码 `Cwd: ""`，实测本机 `telemetry.db` 46 条 session 的 `cwd` 全为空串。后果：

| 能力 | 文档承诺 | 真实行为 |
|---|---|---|
| `ccl stats` 的 `PROJECT` 列 | `filepath.Base(session.cwd)` | 恒空 |
| `ccl stats --by-project` | 按 cwd 分组 | 46 条聚成 1 组（空串） |
| `ccl stats --project <子串>` | cwd 子串过滤 | 恒返回空结果 |

`spec/stats-query` 里对应的 scenario 全部处于"文档通过、现实失效"的状态。做看板前必须先补这个维度——用户要的"按项目占比 / 按项目筛选"直接依赖它。

**cwd 的补救来源已经找到**：Claude Code 把每个会话的完整记录写进 `~/.claude/projects/<编码cwd>/<session-id>.jsonl`，文件名就是 session id（与 `telemetry.db` 的 `sessions.id` 同一套 uuid），每条记录里带真实 `cwd` 字段。实测本机 46 个 session 中 29 个能命中（63%），其余是 Claude Code 已清理的旧会话。

**更好的来源在探查中浮现**：Claude Code 的 OTel SDK 支持 `OTEL_RESOURCE_ATTRIBUTES`——用户自定义的键值会作为 **resource attributes** 随 metrics 一起推送。`ccl` 启动 `cc` 时本来就知道"这次用的哪个 provider、在哪个目录"，把它写进去，daemon 就能拿到**逐次 launch 的真值**：provider 与 cwd 都从空壳变可用，且不依赖 Claude Code 的私有目录结构、不依赖反查命中率。本机端到端实测通过（见 D15）。

这同时推翻了"provider 无真值可得"的旧结论——真值一直都在，只是之前没找到搬运它的通道。

**约束**：
- 单文件 Go 二进制，`modernc.org/sqlite`（无 cgo），Linux / macOS / Windows 三平台
- 无 Web 框架依赖，前端资源需内嵌（`go:embed`）
- `:4318` 已被 daemon 占用，daemon 绑 `0.0.0.0`（用户要求局域网可访问）
- 项目规范驱动：OpenSpec `spec-driven` schema

## Goals / Non-Goals

**Goals:**

- 浏览器里看到用量看板：KPI 卡片 + 时间趋势 + 项目/模型占比 + 会话明细
- 看板挂在 daemon 已监听的 `:4318` 上，不新增端口、不新增进程
- token 鉴权：URL 一次性换 session cookie，token 不留在地址栏
- 补齐 `sessions.cwd`，让 `ccl stats` 的项目维度从空壳变可用（CLI 与 web 同时受益）
- 补齐 `sessions.provider` / `sessions.provider_endpoint`，让 provider 维度同样可用（CLI 与 web 同时受益）
- 前端零新依赖：内嵌 HTML/CSS/JS，图表用手写 SVG
- `ccl web` 作为看板入口：打印/打开 URL、轮换 token、触发回填

**Non-Goals:**

- **不给 OTel ingest 端点加鉴权**。`:4318` 绑 `0.0.0.0`，局域网上任何设备都能 POST `/v1/metrics` 灌入伪造 metrics。这是**既存**风险，不是本次引入；本次保持范围干净，另行记档。
- 不做多用户 / 权限体系；不做历史数据导入迁移；不做 PWA / 移动端原生应用；不做图表导出
- 不引入前端框架、不引入 CDN 图表库、不新增任何 Go 依赖

## Decisions

### D1. 看板挂在 daemon 的 `:4318`，不新开端口

**选择**：`collector.Server` 的 mux 上新增 `/ui/*` 与 `/api/stats`。

**理由**：daemon 已是常驻进程（`serveHelp` 明说"永不自动 idle 退出"），数据同源（同一个 `telemetry.db`），不新增端口冲突面，不需要第二个进程去并发读同一个 WAL 库。

**否决的替代方案**：
- *独立 `ccl web` 进程 + 独立端口（:8080）*：多一个要管理的进程、多一个端口占用源，且要处理"看板进程与 daemon 同时写/读 db"的并发语义。合并后这些全部消失。
- *生成静态 HTML 文件*：零运维，但数据是死快照，不能交互筛选与实时刷新，与"看板"预期不符。

**代价**：页面随 daemon 生死。用 `ccl web` 主动拉起 daemon 兜底（见 D9）。

### D2. 路由通过 `ServerOptions` 注入，collector 不反向依赖 web

`collector.ServerOptions` 增加一个挂载入口：

```go
type ServerOptions struct {
    Addr     string
    Store    *Store
    Forward  *Forwarder
    Mount    func(mux *http.ServeMux) // 新增：cmd 层注入 /ui + /api 路由
}
```

`internal/collector` 不 import `internal/web`——采集层与展示层解耦；组装发生在 `cmd/cc-launcher/serve.go`。**否决**：让 collector 直接 import web，会让 OTel 采集层背上 UI 包袱，且未来若要第二个 UI 入口就得改 collector。

### D3. token 存独立文件 `~/.cc-launcher/web-token`

32 字节随机 hex（64 字符），文件权限 `0600`，原子写（tmp + rename）。daemon 启动时若文件不存在则生成。

**为什么不放 `state.json`**：`state.json` 是既有结构化状态契约，塞一个自由格式的 secret 进去会与之耦合；独立文件权限可控、便于轮换、便于 `0600` 而不用改 `state` 的写入逻辑。

**为什么落盘而不是只在内存**：daemon 会因 `ccl serve --restart` 重启，token 必须跨进程存活；且 `ccl web` 需要在**没有 daemon 进程**的情况下也能打印 URL。

**轮换**：`ccl web --rotate` 重新生成并覆盖 → 旧 token 及其派生出的所有 session cookie 立即失效（D4）。

### D4. session cookie：HMAC 签名 + 签发时间

cookie 名 `ccl_web`，值 `<issuedAtUnix>.<hex(HMAC-SHA256(key=tokenBytes, msg="ccl-web-session-v1|"+issuedAt))>`。

- 校验：`hmac.Equal` 常量时间比较 + `issuedAt` 距今 ≤ 7 天
- cookie 属性：`HttpOnly; SameSite=Lax; Path=/; Max-Age=7d`（不设 `Secure`——多数局域网场景走 http，设了会导致 http 下 cookie 被丢弃）
- **为什么用 HMAC 而不是把 token 直接塞进 cookie**：避免 token 明文落进浏览器磁盘、出现在中间件日志里。HMAC 的 key 就是 token，服务端只持有 token，不持有任何服务端 session 状态。
- **为什么无状态**：单进程、无多实例需求；无状态让 daemon 重启后 cookie 依然有效（只要 token 未轮换）。
- **轮换即吊销**：token 变了 → HMAC key 变 → 旧 cookie 全部失效，符合直觉。

### D5. 鉴权边界与 token 的"一次性"使用

`/ui`（HTML）、`/ui/app.js`、`/ui/style.css`、`/api/stats` 全部需要鉴权。

访问流程：
1. 请求 `/ui?token=<tok>` → 服务端常量时间比对 token
2. 命中 → 种 session cookie → **302 重定向到 `/ui`（去掉 query）**
3. 后续请求只带 cookie；`/api/stats` 的 fetch 不带 token

**为什么要重定向**：token 留在地址栏会进浏览器历史、可能被 Referer 带给第三方资源。重定向后地址栏干净，cookie 承担后续鉴权。

未鉴权时的响应：页面返回 401 + 极简提示 HTML（"需要 token，用 `ccl web` 获取访问 URL"）；API 返回 401 + `{"error":"unauthorized"}`。

### D6. cwd 来源：launch 上下文优先，jsonl 反查兜底

cwd 有两个来源，**优先级明确**：

1. **launch 上下文**（主）：`ccl` 启动 `cc` 时把当次工作目录写进 OTEL payload 的 resource attributes（见 D15），daemon 直接取。这是"这次启动是在哪个目录跑的"——真值、零 IO、100% 覆盖 `ccl` 启动的会话。
2. **jsonl 反查**（兜底）：`~/.claude/projects/*/<session-id>.jsonl`（文件名 = session id）。逐行解析，**找到第一个含顶层 `cwd` 字段的行即停**。只覆盖**没走 `ccl` 启动**的会话，以及 launch 上下文出现之前的历史行。

**为什么不反推目录名**：目录名是编码后的 cwd，编码规则不唯一——本机 `cc-launcher` 的 `-` 与路径分隔符转换存在歧义，实测不可靠。jsonl 里有真值。

**读取纪律**：只取 `cwd` 字段，取到即停止解析；**prompt 内容绝不落盘、落库或进内存缓存**。这是读用户自己的本地会话文件，只取一个路径字段。

**降级**：两处都没有（文件已清理 / 目录结构变化 / 非 `ccl` 启动且无记录）→ `cwd` 留空，web 与 CLI 显示"未知项目"。这是正常状态，不是错误。

**语义**：launch 上下文里的 cwd 是**本次启动时的工作目录**。`--resume` 恢复在别处创建的会话时，它给的是当前目录，不是会话原始目录；jsonl 反查给的才是原始目录。两者不一致时以 launch 上下文为准（当前行为更符合"这次用在哪"），差异在 spec 里写明。

### D7. provider 维度：随 launch 上下文一起携带

**早期结论"不做 provider 维度"已被推翻。** 推翻的理由是当时只有一个候选来源：`~/.cc-launcher/state.json` 的 `active_provider` / `dir_bindings`——那是**当前值**，用它标注历史 session 会在用户中途切换 provider 后产生错误归因，那是在展示假数据。

现在有了**逐次 launch 的真值来源**（D15）：`ccl` 启动 `cc` 时，provider 已经选定、且 provider 的 env 已经由 `ccl` 用 `setenv` 注入子进程（provider-setenv-cwd-binding 改造后的机制），所以"这次启动用的是哪个 provider"对 `ccl` 而言是**确定的事实**，不是推断。

因此 provider 维度做，且语义是**启动时的快照**：

- `sessions.provider` = 启动时选中的 provider 名（provider 配置文件名）
- `sessions.provider_endpoint` = 该 provider 的 `ANTHROPIC_BASE_URL` 的 host（仅 host，不含路径与凭据）
- provider 文件改名后，历史 session 仍显示旧名——这是标签语义，不是 bug
- 直接 `cc` 启动的会话 provider 恒空（`ccl` 已不再往 settings.json 写 `ANTHROPIC_*`，没有真值来源），显示"未知"，**绝不回退到当前配置的 provider**

`--by-provider` / `PROVIDER` 列因此从空壳变成可用；另加 `--provider <子串>` 过滤（与 `--project` 对称，SQL 层过滤）。web 看板加 provider 筛选 + provider 占比，与 project / model 对称。


### D8. 查询层扩展：model 分组 + model 过滤

`stats.Query` 新增两个字段：

- `ByModel bool` → `buildSessionStatsSQL` 增加 `case "model"` 分组（`GROUP BY s.model`；`sessions.model` 实测有真值）
- `Model string` → **SQL 层**过滤（`AND s.model LIKE ?`）。选 SQL 层而非事后过滤：model 是 `sessions` 表的真值列，SQL 层过滤更省，且能正确参与 totals 计算。

同时给 CLI 加 `--by-model` / `--model <子串>`，保持 CLI 与 web 同源（`cli-shell-completion` 要求 flag 改动自动同步到补全）。

### D9. `ccl web` 子命令

新增顶层子命令 `ccl web`：

| 用法 | 行为 |
|---|---|
| `ccl web` | 确保 daemon 在跑（不在则 fork 并等就绪），打印带 token 的看板 URL（本机 LAN IP + 127.0.0.1 两个） |
| `ccl web --rotate` | 轮换 token，提示旧 URL 失效 |
| `ccl web --backfill` | 显式触发历史 session 的 cwd 回填 |
| `ccl web --open` | 用系统浏览器打开看板 URL（`xdg-open` / `open` / `cmd start`） |

**为什么是独立子命令而不是 `ccl serve --url`**：`serve` 现有 4 个互斥 flag（`--stop / --restore / --status / --restart`），再加会继续膨胀；而"打开看板"是用户视角的动作，语义上与 daemon 生命周期管理不同。

**兜底价值**：D1 的代价（页面随 daemon 生死）由 `ccl web` 的"确保 daemon 在跑"消化——用户想看看板时执行一条命令即可，不必先 `ccl serve`。

`ccl serve --status` 追加一行看板 URL（不新增 flag）。

### D10. 回填策略

- **daemon 启动时自动回填**：`sessions.cwd = ''` 的行逐个反查并 UPDATE。限流：每次启动最多处理 500 行，超出部分留到下次；回填在 daemon 启动的后台进行，不阻塞端口监听（端口先绑上，页面立即可用，回填逐步生效）。
- **`ccl web --backfill` 显式回填**：不限量，供用户主动触发。
- 两者都幂等（只处理 `cwd = ''` 的行）。

**为什么需要两条路径**：daemon 常驻不重启时自动回填不会触发，用户等不到历史数据补齐；显式命令给一条确定的路径。

### D11. API 契约

`GET /api/stats?window=24h|7d|30d|90d&project=<子串>&model=<子串>&group=session|project|model`

- `window` → `stats.Query.Buckets`：`24h` → 1（hourly，今天 24 桶）、`7d` → 7、`30d` → 30、`90d` → 90。与既有 `--day N` 语义完全一致，**不发明新的窗口模型**。默认 `7d`。
- `project` → `Query.Project`（SQL 层，cwd 子串）
- `model` → `Query.Model`（SQL 层）
- `group` → `Query.ByProject` / `Query.ByModel` / 默认 session 级

**响应体不复用 `stats.Result`**，而是定义 web 专用 DTO。原因：`SessionStats.ExtraLabels` 会把 `user.id`（主机指纹哈希）、`host.arch`、`os.version` 原样带出——直接 marshal 到局域网可访问的 API 等于泄露主机指纹。DTO 采用**字段白名单**，不透传 `extra_labels`。

响应结构：

```
{ window, since, group_by, generated_at,
  totals:   { input, output, cache_creation, cache_read, cost_usd, sessions },
  sessions: [ { started_at, project, model, input, output, cache_creation, cache_read, cost_usd } ],
  buckets:  [ { label, bucket_iso, is_current, sessions, input, output, cache_creation, cache_read, cost_usd } ],
  projects: [ { name, tokens, cost_usd, token_share, cost_share } ],
  models:   [ { name, tokens, cost_usd, token_share, cost_share } ] }
```

**占比怎么算**：内部跑三次查询——主查询（用户选的 group，带桶聚合，用于明细与趋势）、按 project 分组（无桶）、按 model 分组（无桶）。占比由后两者的结果在内存里算份额。本地 db 每次查询 <50ms，三次可接受；换来的是"占比数字与表格数字同源"的一致性。

**桶粒度随窗口切换**：`24h` → 24 个 hourly 桶；`7d / 30d / 90d` → 对应天数的 daily 桶。沿用 `QueryStats` 现有推导（N==1 → hour，else → day）。

### D12. 前端：内嵌资源 + 手写 SVG

- `internal/web/assets/{index.html,app.js,style.css}`，用 `go:embed` 打包进二进制
- 图表全部手写 SVG（堆叠柱状图 = token 构成；折线 = cost 走势；横向条形 = 占比）。数据量 ≤ 90 个桶，不值得引入图表库
- 配色与图形规范在实现阶段按 `dataviz` skill 的方法走（浅色 / 深色主题都要可读）
- 静态资源带版本化 query（`app.js?v=<buildHash>`），避免浏览器缓存旧资源
- 轮询刷新：默认开启，间隔 30s，可关。切换筛选时用 `AbortController` 取消在途请求，避免旧响应覆盖新结果
- 响应式：窄屏下图表容器横向滚动，表格容器独立 `overflow-x: auto`

**否决**：CDN 图表库（离线环境挂图表 + 外部依赖供应链风险）；前端框架（引入 node 工具链，与当前零构建的 Go 项目风格不符）。

### D13. 端口与访问地址

daemon 维持 `:4318`（绑 `0.0.0.0`），满足局域网访问。`ccl web` 打印 URL 时探测本机第一个非 loopback 的 IPv4 地址，给出 `http://<lan-ip>:4318/ui?token=<tok>`，同时给出 `http://127.0.0.1:4318/ui?token=<tok>`。

### D14. 读取路径与并发

web API 每次请求独立开 `mode=ro` 连接查 db（复用 `internal/stats` 现有模式）。daemon 写用 WAL，读不阻塞写、也不被写阻塞。**不引入服务端缓存**——本地 db 查询足够快，且避免"页面显示的是旧数据却看不出是旧的"这类困惑。

### D15. 上下文靠 OTLP resource attributes 随请求走，不靠 daemon 读自己的 env

**选择**：`ccl` 启动 `cc` 时，在**子进程环境**里设 `OTEL_RESOURCE_ATTRIBUTES`，带三个 key：

| key | 值 | 来源 |
|---|---|---|
| `ccl.provider` | provider 名 | `result.Provider`（provider 配置文件名） |
| `ccl.cwd` | 绝对工作目录 | `os.Getwd()`（`ccl` 被调用的目录） |
| `ccl.provider_endpoint` | 端点 host | provider env 里 `ANTHROPIC_BASE_URL` 的 host |

Claude Code 的 OTel SDK 会把这些键写进 metrics 的 **resource attributes**；`internal/collector/parser.go` 已经把 resource attrs 读进 `resAttrs`，daemon 直接取即可，不需要新协议、不需要改 HTTP 契约。

**为什么不靠 daemon 读自己的 env**：daemon 是独立常驻进程，它的环境描述的是"daemon 何时启动"，不是"某个 export 属于哪个会话"。哪怕让 `ccl` fork daemon，那也只是首次启动那一刻的快照；之后换 provider / 换目录再开会话，daemon 侧依然是旧值 → 给历史 session 贴错 provider。D7 早先拒绝 provider 维度正是这个理由。改成"上下文随 export 走"后，逐次 launch 精确。

**为什么不用 OTLP HTTP headers**（备选，已评估）：headers 同样能逐请求携带，但要改 daemon handler 读 header，且用户若已设 `OTEL_EXPORTER_OTLP_HEADERS` 还得合并；resource attributes 已经在 payload 里、parser 已经在读，改动面最小。

**为什么不能写 `~/.claude/settings.json`**：settings.json 的 `env` 是全局的，会被所有 `claude` 进程继承——直接 `cc` 启动的会话会继承"上一次 `ccl` 的 cwd"，反而造出假数据。

**实测得到的编码规则（硬约束）**：

- Claude Code 把 `OTEL_RESOURCE_ATTRIBUTES` 按 `,` 拆条目、按 `=` 拆键值；值里出现空格 / 控制字符 / 非 ASCII 会被判为非法
- **一个非法值会让整份 map 被丢弃**（不是只丢那个属性）→ ccl 必须对所有值做 percent-encode（unreserved 之外全编）
- 编码后 > 255 字符的属性直接**不注入**（同样会毒掉整份 map）
- Claude Code 会把值 decode 回原样 → payload 里是明文路径，daemon 侧不需要再解码
- 实测结论：空格 → 整份丢失；逗号 → 该值被截断；原生中文 → 正常保留；`%20%2C%3B%5C` → 正确还原为 `,;\`

**合并规则**：环境里已有 `OTEL_RESOURCE_ATTRIBUTES` 时保留用户原值并追加 ccl 的三个 key（同 key 以 ccl 的为准）。

**settings.json 覆盖陷阱（实测）**：Claude Code 的 settings.json `env` 优先级**高于**进程环境。若用户的 settings.json 已设 `OTEL_RESOURCE_ATTRIBUTES`，我们的注入会被整体顶掉 → ccl 检出后**警告用户并降级**（provider / cwd 显示未知），**不擅自改**用户的 settings.json。

**注入位置**：`runTUI` 里 provider 选定后、`launcher.Launch` 之前。Unix 上 provider 选中时 `opts.Env` 非 nil（是启动时的 `os.Environ()` 快照，比 `injectOTelEnv` 还早），因此必须显式 `envutil.Upsert` 进 `optsEnv`；同时补一次 `os.Setenv` 覆盖 `opts.Env == nil` 的分支（Windows `cmd.Env=nil` 走进程环境，也吃 `os.Setenv`）。

**转发与隐私**：forwarder 原样转发 body → `ccl.cwd` 的绝对路径与 provider 名会一起送到用户的远端 collector。收端是用户自己配的，且远端能按 provider / project 聚合算是额外收益；已确认接受，不做剥离。

**语义边界**：`ccl.cwd` = 本次启动时的工作目录（`--resume` 时是当前目录）；provider = 启动时的 provider 名快照；直接 `cc` 启动的会话三项皆空，显示"未知"。

## Risks / Trade-offs

- **[R1] daemon 停了页面 404** → `ccl web` 确保 daemon 在跑；`ccl serve --status` 明确显示 daemon 状态
- **[R2] 依赖 Claude Code 私有目录结构（`~/.claude/projects`）** → 只作为 cwd 的**兜底**来源（launch 上下文才是主来源），查不到即留空，不影响主流程；Claude Code 升级改了路径/格式时表现为"项目名变未知"而非功能崩溃
- **[R3] token 明文落盘** → 文件权限 `0600`；`ccl web --rotate` 可随时吊销
- **[R4] 回填拖慢 daemon 启动** → 放后台执行 + 每次启动限 500 行；页面不等待回填完成
- **[R5] 看板 + ingest 共用 `:4318` 且绑 `0.0.0.0`** → 本次只给 UI 路由加 token 保护；ingest 端点仍无鉴权（既存风险）。建议后续单独一个 change：用 `OTEL_EXPORTER_OTLP_HEADERS` 注入共享 secret，Claude Code 的 OTel SDK 原生支持
- **[R6] API 泄露主机指纹** → 响应 DTO 字段白名单，不透传 `extra_labels`（含 `user.id` 哈希）
- **[R7] 手写 SVG 图表在小屏/高分屏下的表现** → SVG 用 viewBox 自适应 + 表格容器独立横向滚动
- **[R8] `ccl stats` 的 PROJECT 列从空变有值** → 这是修复，但依赖 Claude Code 目录结构的用户会看到输出变化；变更写进 CHANGELOG
- **[R9] 三次查询的延迟叠加** → 本地 db 单次 <50ms；若实测超过 200ms，改为单次查询后在内存里派生占比
- **[R10] launch 上下文因编码不当整体丢失** → Claude Code 对 `OTEL_RESOURCE_ATTRIBUTES` 的值校验很严（空格 / 控制字符 / 非 ASCII 即非法），且**一个非法值会毒掉整份 map**。对策：ccl 侧对所有值强制 percent-encode、超过 255 字符的属性直接不注入；编码逻辑有专门单测覆盖空格 / 逗号 / 中文 / 超长四种情形
- **[R11] 用户的 settings.json 覆盖了我们的注入** → settings.json 的 `env` 优先级高于进程环境，若用户已设 `OTEL_RESOURCE_ATTRIBUTES`，上下文会整体失效。对策：ccl 检出后警告用户并降级（显示"未知"），不擅自改用户文件
- **[R12] 上下文只在 `ccl` 启动的会话上有** → 直接 `claude` 启动的会话 provider 恒空、cwd 靠 jsonl 兜底。对策：CLI 与看板对未知维度显式标注"未知"，不用当前配置的 provider 去填补（那是假数据）
- **[R13] cwd 绝对路径随 body 转发到远端 collector** → 已与用户确认接受：收端是用户自己配的，且远端按 provider / project 聚合是额外收益

## Migration Plan

1. **schema 迁移（唯一一处）**：`sessions` 表新增 `provider_endpoint TEXT NOT NULL DEFAULT ''` 列。走 `PRAGMA user_version` 版本号（1 → 2）的既有迁移通道，`ALTER TABLE ADD COLUMN` 幂等执行，不动其它列、不丢数据
2. `sessions.cwd` / `sessions.provider` 列已存在，**不需 schema 变更**，只回填不改表
3. 部署：出新版 binary → `ccl serve --restart`（热重启，Claude Code session 不受影响）→ daemon 首次启动自动生成 `web-token`、执行 schema 迁移、开始后台回填
4. 回滚：回退 binary 即可；`~/.cc-launcher/web-token` 留着无害（旧 binary 不认识它）；多出来的 `provider_endpoint` 列留着也无害（旧 binary 不读它）
5. 历史数据回填失败（Claude Code 改了目录结构）：`sessions.cwd` 留空，看板与 CLI 显示"未知项目"，其余功能不受影响
6. **上下文只对新会话生效**：升级前跑的会话没有 `ccl.*` 属性，provider 恒空、cwd 靠 jsonl 兜底回填——这是预期行为，不做数据伪造

## Open Questions

- Claude Code 未来若在 OTel payload 中推送 cwd，应优先用 payload 值（真值）而非文件反查——`ResolveCwd` 接口已留口子，届时在 resolver 实现里调换优先级即可
- 是否需要给 `ccl web` 之外的入口（如 `ccl stats --web`）也提供 URL 打印？当前认为 `ccl web` 单独一个子命令已够，不做
