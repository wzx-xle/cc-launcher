## Context

状态栏是 ccl 额度的唯一消费者，它的调用节奏极其苛刻：Claude Code 的状态栏约每 300ms 刷新一次，每次都是**新起的进程**，Claude Code 在下一次执行前会 abort 上一次。

现状是消费方跑 `ccl quota --json` —— 一个**纯本地读**的命令（读 `~/.cc-launcher/quota.json`），但 Windows 上实测 p50 166~190ms / max 252ms，耗时全在进程启动：`cmd.exe(scoop shim)` → `cmd.exe(app\ccl.cmd)` → `cc-launcher.exe`。

消费方自己设了 `timeout: 250`，余量 60~80ms。系统繁忙时（Claude Code 冷启动、用户配的三个 hook 各起一个 node 进程、Defender 扫 18MB 的 exe 与上百 MB 的 node.exe、daemon 正在写额度数据文件）必然超时 → 静默回落 Claude Code 自己的 `rate_limits` → 用户看到"默认用量"。

要点：**这是"叫不动"，不是"算错"** —— 额度数据实测完全正常（`exit=0`、`stale=false`、维度齐全）。

ccl 侧已有的条件：
- daemon 常驻、监听 `:4318`（**所有网卡**），已有无鉴权的 `POST /ccl/activate` 与带鉴权的 `/api/*` 两类路由
- `cmd/cc-launcher/quotacmd.go` 里已有 `quotaOutput` 结构与 stale 判定，但**住在 cmd 层** —— `internal/collector` / `internal/web` 不能反向依赖它
- `CCL_PROVIDER` 已经建立了"ccl 注入给消费方的只读环境变量"的模式：`--settings` 的 `env` 块（主通道）+ 进程环境（冗余副本），ccl 自己的键赢过 provider 配置与用户 settings.json

## Goals / Non-Goals

**Goals:**

- 消费方能在**不启动任何进程**的前提下读到额度（目标：几 ms）。
- 端点的输出与 `ccl quota --json` **逐字段同构** —— 消费方换通道时解析逻辑不用改。
- stale 判定、维度组装只有**一份实现**，命令与端点不会给出互相矛盾的结论。
- daemon 没跑时 `ccl quota` 依然可用（`--refresh` 逃生舱场景不退化）。
- 端点不构成新的暴露面。

**Non-Goals:**

- 不改 `ccl quota --json` 的字段、退出码语义（0/1/2）。
- 不接管用户的 `statusLine` 配置、不分发状态栏脚本。
- 不把额度数据文件的位置与内部格式暴露给消费方。
- 不改额度刷新的节奏、活跃判定、相位错开、退避。
- 不解决 Windows 上"起进程慢"本身（Defender、`.cmd` shim）—— 与本 change 无关。

## Decisions

### D1：额度输出的组装逻辑搬到 `internal/quota`，命令与端点共用

`quotaOutput` 结构 + stale 判定现在住在 `cmd/cc-launcher/quotacmd.go`。端点要输出**同构**的 JSON，就必须共用同一份实现，否则两份 stale 判定迟早分叉（阈值、容忍量、refreshInterval 的兜底各写一遍）。

⇒ 新增 `internal/quota/output.go`：

```go
type Output struct {
    Provider   string      `json:"provider"`
    Source     string      `json:"source"`
    Stale      bool        `json:"stale"`
    FetchedAt  int64       `json:"fetched_at"`
    Dimensions []Dimension `json:"dimensions"`
}

// BuildOutput 组装一份对外的额度输出。entry 为零值（没有记录）时返回 ok=false。
func BuildOutput(ref ProviderRef, entry Entry, now time.Time) (Output, bool)
```

cmd 层的 `quotaOutput` 删除，改用 `quota.Output`（字段名与 json tag 完全一致 → 输出逐字节相同）。

`internal/quota` 是**叶子包**（不 import 任何 internal 包）→ 谁都能依赖它，无循环风险。

**替代方案**：在 collector 与 cmd 各写一份组装。否决——两份 stale 判定必然分叉，且现有命令的 stale 语义（refreshInterval 兜底 60 秒 + 1 秒容忍）是有意设计的，不能简化。

### D2：端点挂在 collector 层，业务用回调注入（与 `OnActivate` 同构）

`internal/collector` 已经有一条成熟的路子：`ServerOptions.OnActivate` —— HTTP 层只管路由与校验，"这个名字是不是已配置的 provider"交给 cmd 层判（cmd 层知道 `providers/` 目录）。

额度端点需要 provider 的 `refreshInterval`（stale 判定用），那也只有 cmd 层拿得到（`quota.LoadProviderRef` 要 providers 目录）。

⇒ `ServerOptions` 新增：

```go
// OnQuota 返回某个 provider 的额度输出。第二个值是 HTTP 状态码：
// 200 = 有数据；404 = 没有记录或 provider 未配置；422 = provider 没有额度接口。
OnQuota func(provider string) (any, int)
```

回调返回 `any` → collector **不 import `internal/quota`**（保持采集层与展示层解耦的既有纪律，跟 `Mount` 的理由一样），只负责 JSON 编码与状态码透传。端点未配置时**不注册路由**（与 `OnActivate` 一致：不暴露一个"什么都不做"的端点）。

**为什么要三态而不是"有/没有"两个**：消费方要能区分"这厂商没额度"（422）与"数据还没准备好"（404）——`ccl quota` 的退出码 2 与 1 就是这个区分，端点自己也要能表达它，否则消费方只能靠"命令的退出码"才知道，而端点就是为了替代命令的。

**替代方案**：端点整个写在 cmd 层、用 `ServerOptions.Mount` 挂载。否决——`Mount` 的语义是"挂 web 的展示层路由"，把额度这种数据端点混进去会让那层职责变浑；且 `/ccl/*` 前缀已有的 `activate` 在 collector 层，分裂成两个前缀不好。

### D3：回环限制是必需的，不是加固

daemon 监听 `:4318` = **所有网卡**，不是只回环。端点若无限制，局域网内任意机器都能读到本机额度余额。

**为什么只限回环就够**：本机任何进程本来就能直接读 `~/.cc-launcher/quota.json`（0600 只挡别的用户，挡不住同用户的进程）。所以"回环来源"这个边界**恰好等于本机进程能直接读文件的边界** —— 不引入新的本机暴露面，同时挡掉网络侧。

判定：`net.SplitHostPort(r.RemoteAddr)` → `net.ParseIP` → `ip.IsLoopback()`。**不看 `X-Forwarded-For`** —— daemon 是裸 HTTP server，没有可信的转发链，看那个头等于把鉴权交给请求方。

非回环 → **403**。

**同一条限制也套用到既有的 `POST /ccl/activate`**（ccl 声明本次用哪个 provider）。它此前无鉴权，局域网内任意机器都能让 daemon 带着用户的 API key 去查其**已配置** provider 的额度端点（名字有白名单校验，所以放大面有限，但仍是白送的打接口机会）。

**收紧 activate 是零风险的**，论证很硬：**daemon 是 ccl fork 出来的子进程**（`forkDaemon` 用 `os.Executable()` 起自己），或是用户在本机手动 `ccl serve` 起的 → **声明的调用方必然与 daemon 同机** → 永远是回环来源。

### D3b：收紧只落在 `/ccl/*`，`/v1/*` 与 `/ui` 不动

daemon 上有三类无鉴权端点，语义不同，不能一刀切：

| 端点 | 合法调用方 | 收紧会打断谁 | 处置 |
|---|---|---|---|
| `/ccl/activate`、`/ccl/quota` | 只有本机 ccl 与本机消费方 | 无人 | **限回环** |
| `/v1/metrics`、`/v1/logs`、`/v1/traces` | 任意 OTLP 客户端 | **WSL2 / 容器里的 Claude Code**：WSL2 连 Windows 的 `localhost:4318` 走 NAT，Windows 侧看到的源地址是虚拟网段（`172.x`），**不是回环** → 一收紧遥测就断，且报错难懂 | 不动 |
| `/ui`、`/api/*` | 用户浏览器 | 从别的机器看看板（已有 token 保护） | 不动 |

**daemon 的监听地址保持 `:4318` 不变**——把它绑到 `127.0.0.1` 会同时打断上面两类，所以收紧只发生在**端点级**。

`/v1/*` 的收紧若将来要做，前置条件是**先确认没有 WSL2 / 容器 / 跨机 Claude Code 推遥测的场景**，否则要配套一个"可信来源白名单"配置项——那是另一个 change 的事，本 change 不预埋。

### D4：端点的契约

```
GET /ccl/quota?provider=<名字>
```

| 情况 | 状态码 | body |
|---|---|---|
| 有记录 | 200 | `quota.Output` 的 JSON |
| `provider` 缺失或空 | 400 | 纯文本 |
| provider 未配置 | 404 | 纯文本 |
| provider 没有额度接口 | 422 | 纯文本 |
| provider 有额度接口但无记录 | 404 | 纯文本 |

**为什么把"没有额度接口"（422）与"没有记录"（404）分开**：消费方要能区分"这厂商没额度"与"数据还没准备好"（`ccl quota` 的退出码 2 与 1 就是这个区分）。端点自己也要能表达它，否则消费方只能靠"命令的退出码"才知道——而端点就是为了替代命令的。

**端点用响应头标记自己：`X-Ccl-Endpoint: quota`（每个响应都带）**。

这不是装饰，是一个**实施期发现的真实场景**：daemon 是常驻进程，**ccl 升级了但 daemon 还是旧进程**时，旧 daemon 没有 `/ccl/quota` 这个路径 → mux 回 `404 page not found`。若客户端把它当成"没有额度记录"（404），用户明明有数据却看到状态栏空着 —— 而这正是本 change 要消灭的体验。

有了标记头，客户端的规则就干净了：

| 响应 | 客户端怎么处理 |
|---|---|
| 带 `X-Ccl-Endpoint: quota` + 200 | 用端点给的文档 |
| 带标记头 + 404 | 没有记录 → 退出码 1 |
| 带标记头 + 422 | 没有额度接口 → 退出码 2 |
| **不带标记头**（端点不存在 / 不是端点的应答） | **回落直读本地数据文件** |
| 连不上 / 超时 | **回落直读本地数据文件** |

实测（2026-11-12，本机）：旧 daemon 在跑时，新版 `ccl quota --json` 依然正常输出（走回落路径，退出码 0）。

### D5：`ccl quota` 优先走 daemon，不可达时回落本地文件

```
ccl quota --json
   ├─ daemon 可达  → GET /ccl/quota?provider=X → 200 → 打印
   │                  404 → exit 1
   │                  422 → exit 2
   └─ daemon 不可达 → 直读 quota.json → 同一份 BuildOutput → 打印
```

**为什么不是"只走 daemon"**：`ccl quota --refresh` 是"不跑 serve 时的逃生舱"——它联网查一次、写进 `quota.json`。如果之后不带 `--refresh` 的 `ccl quota` 改成只走 daemon，而用户压根没跑 serve（daemon 没起），就**读不到自己刚写的数据**。这是功能倒退。

**为什么优先 daemon 而不是反过来**：daemon 在跑时，命令与状态栏拿到的是**同一份数据、同一时刻的 stale 判定**。反过来（命令永远直读文件、状态栏走端点）也能保证一致（同文件同逻辑），但优先 daemon 能让"daemon 在跑"这个状态被利用起来，且顺带让命令的输出路径与消费方完全同构。

两条路径读同一个文件、用同一个 `BuildOutput` → **输出逐字节一致**。

回落的判定：连接失败、超时、响应**不是端点给的**（缺标记头，见 D4）→ 直读文件。超时给 **500ms**（回环请求是毫秒级，500ms 足够宽松，又不至于让命令在 daemon 假死时卡住）。端点明确回答的 404 / 422 **不回落**——它们是**答案**（没有记录 / 没有额度接口），分别对应退出码 1 / 2。

`--refresh` 路径**不变**：它自己联网查 → 写文件 → 直接 `BuildOutput` 组装（刚查到的数据，不绕 daemon）。

### D6：`CCL_QUOTA_URL` 的交付与 `CCL_PROVIDER` 完全同构

值 = `http://127.0.0.1:4318/ccl/quota`（daemon 端口是常量 `daemonAddr`）。

通道：
1. `--settings` 的 `env` 块（主通道，压过用户 settings.json 与进程环境）
2. ccl 进程环境（冗余副本）

纪律：
- provider 配置里若写了同名键 → **ccl 的值赢**（与 `CCL_PROVIDER` 同一条纪律）
- **无条件交付**（不因 daemon 没起来就不给）—— 避免把 `buildLaunchOptions` 与 `ensureDaemonRunning` 的先后顺序耦合进来；消费方拿到 URL 连不上就自己回落
- **缺席 ≠ 空串**：直接跑 `claude`（不经 ccl）的会话拿不到 `CCL_QUOTA_URL` → 消费方知道"这个会话不是 ccl 起的"

**为什么 URL 是常量而不是动态拼**：daemon 端口固定 4318；URL 形态一旦发布就是对外契约（与 `CCL_PROVIDER` 同等：改名/改形态 = breaking change）。

### D7：状态栏脚本侧走 HTTP 优先、命令兜底（双通道）

推荐写法（写进 `docs/provider-quota.md`，供用户粘贴）：

```js
function loadQuota() {
  if (!process.env.CCL_PROVIDER) return null;
  const provider = process.env.CCL_PROVIDER;
  const url = process.env.CCL_QUOTA_URL;
  if (url) {
    try {
      // Node 内置 http，不起进程
      const body = httpGetSync(`${url}?provider=${encodeURIComponent(provider)}`);
      const q = JSON.parse(body);
      if (q && Array.isArray(q.dimensions)) return q;
    } catch {}
  }
  try {   // 兜底：daemon 没跑 / 端点不可用
    return JSON.parse(execSync('ccl quota --json', { /* timeout: 250 */ }));
  } catch { return null; }
}
```

**双通道的理由**：HTTP 快但依赖 daemon 在跑；命令永远可用但慢（Windows 上 170ms）。两个通道的输出同构 → 消费方的解析逻辑（`q.dimensions` / `q.stale`）**一行都不用改**。

**脚本的改动不属于本 change 的强制部分**（它在仓库外，是用户的文件）—— 但 **docs 必须给出可直接粘贴的写法**，否则这个 change 对用户没有实际收益。

### D8：端点读数据文件用 `quota.Store.Load()`

daemon 端点读 `quota.json` 时，与 daemon 自己的写并发 —— Windows 上有替换窗口（v0.22.1 已修的那个）。端点必须走 `quota.Store.Load()`（含 5ms 重试），**不能裸 `os.ReadFile`**。

## Risks / Trade-offs

- **[daemon 监听所有网卡，回环限制写漏了就是局域网泄露]** → 回环判定放在 collector 的 HTTP 层（有独立的单元测试覆盖"非回环来源被 403"），且实现上不看任何请求头。
- **[`CCL_QUOTA_URL` 是新的对外契约]** → 写进 spec 与 docs，形态固定；改名/改形态按 breaking change 处理（与 `CCL_PROVIDER` 同等对待）。
- **[消费方不改脚本就看不到改善]** → docs 给出可直接粘贴的推荐写法；端点与 env 交付本身是**向后兼容的增量**（老脚本读不到 `CCL_QUOTA_URL` 就继续走命令，行为不变）。
- **[`ccl quota` 优先走 daemon 多一次 HTTP 往返]** → 回环请求是毫秒级；换来的是与状态栏同源同构。
- **[daemon 变成额度读取的新单点]** → 端点不可用时命令回落文件、状态栏回落命令，**没有退化路径**。
- **[`quota.Output` 搬家会动到 cmd 层的类型]** → 只有 `quotacmd.go` 用它，编译期即可发现；输出字段与 json tag 不变 → 逐字节兼容。
- **[端点给本机任意进程提供了额度数据]** → 与"本机进程本来就能直读数据文件"等价，不引入新的本机暴露面；额度数据不含任何凭证。

## Migration Plan

- **无数据迁移**，无格式变更。
- **回滚**：端点与 `CCL_QUOTA_URL` 交付可以独立回滚；消费方在变量缺席时回落命令。
- **兼容性矩阵**：
  - 新 ccl + 新脚本 → HTTP 通道（快）
  - 新 ccl + 老脚本 → 脚本不认识 `CCL_QUOTA_URL` → 走命令（慢但正确）
  - 老 ccl + 新脚本 → 脚本读不到 `CCL_QUOTA_URL` → 走命令（慢但正确）
- **验证**：Linux 上真机（真 ccl + 真 claude）验证端点返回与 `ccl quota --json` 逐字段一致；Windows 上复测 `ccl quota --json` 的耗时分布与状态栏的显示。

## Open Questions

无。鉴权边界（只限回环）与 `ccl quota` 的读取策略（优先 daemon、回落文件）已定。
