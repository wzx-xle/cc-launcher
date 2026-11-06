## Context

### 缺口

Claude Code 的状态栏有两个额度槽位，数据来自它自己维护的 `rate_limits`（Anthropic 官方限流）。走第三方模型时它拿不到这两个数，槽位就一直显示 `--`。

实测（2026-11-05，Claude Code 2.1.311 + MiniMax provider）：

```
ctx: ░░░░░░░░░░ -- | 5h: ░░░░░░░░░░ -- | 7d: ░░░░░░░░░░ --
```

### 各厂商的额度接口长什么样（实测，2026-11-06）

三个真实端点，三种响应形状，三种错误协议。**这一节是整个设计的依据**——下面的每个决策都从这张表推出来。

| | MiniMax | DeepSeek | 讯飞（New API 私有部署） |
|---|---|---|---|
| 额度端点 | `https://www.minimax.cn/v1/token_plan/remains` | `https://api.deepseek.com/user/balance` | `<baseURL>/api/usage/token` |
| provider 的 base_url | `https://api.minimax.cn/anthropic` | `https://api.deepseek.com/anthropic` | `https://one.iflytek.com/api/llm/console/chat` |
| 成功判据 | `base_resp.status_code == 0` | HTTP 200 | `code == true` |
| 失败判据 | **HTTP 200** + `status_code != 0` | HTTP 401 | HTTP 401 |
| 错误体形状 | `base_resp` | **JSON 或纯文本** | `{"error":{code,message,request_id,type}}` |
| 分母（总量） | 有（隐含 100%） | **无** | 有（`total_granted`） |
| 周期 | 5h + 7d | 无 | 无（`expires_at: 0`） |
| 绝对量 | 无 | `total_balance` | `total_used` / `total_available` / `total_granted` |
| 单位 | % | CNY / USD | 额度点（÷ `quota_per_unit`） |

**MiniMax 的实测响应**（正常返回，HTTP 200；2026-11-06 用本机真 key 复测）：

```json
{
  "model_remains": [
    {
      "start_time": 1793943914255,
      "end_time": 1793961914255,
      "remains_time": 16542413,
      "current_interval_total_count": 0,
      "current_interval_usage_count": 0,
      "model_name": "general",
      "current_weekly_total_count": 0,
      "current_weekly_usage_count": 0,
      "weekly_start_time": 1793718756984,
      "weekly_end_time": 1794323556984,
      "weekly_remains_time": 378185142,
      "current_interval_status": 1,
      "current_interval_remaining_percent": 96,
      "current_weekly_status": 1,
      "current_weekly_remaining_percent": 67
    }
  ],
  "base_resp": { "status_code": 0, "status_msg": "success" }
}
```

MiniMax 实测得到的关键事实（每一条都影响设计）：

| 事实 | 依据 |
|---|---|
| `model_remains` 是**数组**，当前只有一个元素，`model_name` 为 `"general"` | 两个不同账号的实测响应都是 1 项 |
| `current_interval_remaining_percent` = **剩余**百分比（不是已用） | 字段名 + 与 `used_percentage` 的语义对照 |
| `end_time - start_time` = 18000000ms = **恰好 5 小时** | 实测数值（两次实测都是 18000000） |
| `weekly_end_time - weekly_start_time` = 604800000ms = **恰好 7 天** | 实测数值 |
| `remains_time` / `weekly_remains_time` 是**距重置的毫秒数**（不是绝对时间戳） | 实测数值（约 4.6 小时 / 4.4 天，与 5h/7d 窗口吻合） |
| 还有 4 个**按次计数**字段（`current_interval_total_count` / `current_interval_usage_count` / `current_weekly_total_count` / `current_weekly_usage_count`），实测均为 **0** | 2026-11-06 真实响应。语义未确认（"按次计费的套餐"？），当前只透传进 `Raw`，百分比取自 `*_remaining_percent` |
| **错误也返回 HTTP 200** | 错误 key → HTTP 200 + `base_resp.status_code: 1004` |
| `base_resp.status_code` 才是成败判据 | `0` = 成功；`1004` = "login fail…"；`2049` = "invalid api key" |
| 响应耗时约 **150ms** | 实测 |
| 国际站**同一路径存在**，但 key 不通用 | `www.minimax.io` 用 `.cn` 的 key → `2049 invalid api key` |

**DeepSeek 的实测响应**（官方文档 `GET /user/balance` 确认；2026-11-06 用本机真 key 复测）：

```json
{
  "is_available": true,
  "balance_infos": [
    { "currency": "CNY", "total_balance": "5.17", "granted_balance": "0.16", "topped_up_balance": "5.01" }
  ]
}
```

| 事实 | 依据 |
|---|---|
| `total_balance` 是**剩余**金额，**不是"已用/总量"** | 官方文档："总的可用余额，包括赠金和充值余额" |
| **没有总额度** → **百分比无解** | 响应里只有剩余侧字段 |
| 数字是**字符串**（`"110.00"`），不是数字 | 官方文档示例 |
| `granted_balance` 是**未过期的赠金余额**（剩余的赠金部分），不是总额 | 官方文档字段说明 |
| 错误体有两种形状 | 无 Authorization 头 → **纯文本** `Authentication Fails (governor)`；无效 Bearer → JSON `{"error":{message,type,param,code}}`。两者都是 HTTP 401 |

**讯飞 / New API 的实测响应**（2026-11-06 用本机真 key 复测）：

```json
{
  "code": true,
  "data": {
    "expires_at": 0,
    "model_limits": {},
    "model_limits_enabled": false,
    "name": "jlcao3",
    "object": "token_usage",
    "total_available": 199949277,
    "total_granted": 200000000,
    "total_used": 50723,
    "unlimited_quota": false
  },
  "message": "ok"
}
```

| 事实 | 依据 |
|---|---|
| 后端是 **New API**（开源 LLM 网关面板） | 错误体 `"type": "new_api_error"` |
| 这是 New API 的**标准响应**（官方端点 `GET /api/usage/token`），只是网关重写了路径 | New API 官方文档的字段与本样本逐一对应 |
| `total_granted - total_used == total_available`（代数自洽） | 200000000 - 50723 = 199949277 ✅ |
| `total_granted` 是**用户给这把 key 设的令牌额度上限**，不是厂商配额 | New API 的令牌模型：`RemainQuota` 初始值 = 建令牌时设的上限 |
| 令牌额度**不按周期重置**（总额度制，只减不增） | 同上；`expires_at: 0` = 永不过期 |
| `quota_per_unit = 500000`（1 USD = 500000 额度点），`quota_display_type: "USD"`，`display_in_currency: true` | `GET /api/status` 实测（该实例 `quota_per_unit` 恰好等于 New API 出厂默认值） |
| 标准 New API 路径被**网关前缀白名单**挡住 | `/api/status`、`/api/user/self` → 403 HTML；加前缀 `/api/llm/console/chat` 后 → 200 |

**网关前缀剥离的证据**：404 的 message 回显了被剥掉前缀后的真实路径——
`GET /api/llm/console/chat/api/nonexistent` → `{"error":{"message":"Invalid URL (GET /api/nonexistent)"}}`。
即网关结构是 `https://one.iflytek.com` + 挂载前缀 `/api/llm/console/chat` → 后面全部直通 New API。

**端点拼接要规范化尾斜杠**：本机 provider 配的 base_url 是 `https://one.iflytek.com/api/llm/console/chat/`（**带尾斜杠**），直接拼 `/api/usage/token` 会得到 `.../chat//api/usage/token`。实测该网关**双斜杠也能通**（返回与单斜杠完全一致的结果），但这是网关的宽容行为、不是契约——别的网关未必。→ 拼接前统一去掉 base_url 的尾斜杠。

**（2026-11-06 真机补充）New API 的端点自身要带尾斜杠**：实测 New API 的路由是 `/api/usage/token/`——请求不带尾斜杠的 `/api/usage/token` 会收到 **301**（Location: `/api/usage/token/`）。标准部署上跟一跳就回到 200；但**带挂载前缀的网关**（本机讯飞）会把 New API 的 301 **剥掉前缀**后透传（`/api/llm/console/chat/api/usage/token` → Location `/api/usage/token/`）——跟过去落到网关根路径 → **SSO 重定向** → 额度永远查不到。

→ **端点统一带尾斜杠**（`.../api/usage/token/`），一步到位。实测：带尾斜杠 → 200 + 正确数据；不带 → 301 → SSO。（标准 New API 部署上带尾斜杠同样直接 200——它的路由本来就带尾斜杠。）

**三家端点的推导规律各不相同**：

| provider | 额度端点相对 base_url |
|---|---|
| MiniMax | **换子域**（`api.` → `www.`）+ 换路径 |
| DeepSeek | **同主机** + 换路径 |
| New API 系 | **同前缀** + `/api/usage/token` |

### 既有约束

- 状态栏脚本在 `~/.claude/statusline.js`（**不在本仓库**），已经用 `execSync('git branch --show-current')` 在每次渲染时起子进程——再加一个 `execSync` 是同一种既有模式。
- `ccl` 装在 `/usr/local/bin/ccl`，**在 PATH 里**（已实测 statusline 子进程继承得到）→ 状态栏可以 `execSync('ccl quota --json')`。
- provider 配置的顶层键会**原样透传**给 Claude Code（`provider-settings-passthrough`），所以任何 ccl 自己的元数据键都必须进 `provider.ReservedKeys`。
- 项目已有 `internal/stats`（SQLite 查询）、`internal/collector`（OTel HTTP 服务）、daemon（:4318）——但 daemon 的职责是 OTel 数据链路。
- **serve 是可依赖的常驻进程**：`daemon-lifecycle` spec —— **每次 `ccl` 启动 Claude Code 前**都探 `localhost:4318`（200ms TCP 超时），不可达就 fork `ccl serve`（detached、新 PGID）并等 ≤3s；`ccl serve --help` 明写**永不自动 idle 退出**。→ 额度刷新搭在它上面，不需要新建轮询体系。
- **本机现有 provider 的 base_url 全部已是 https**（deepseek ×2、minimax ×3、iflytek；`claude-official` 无 base_url）。`claude-official.json` 里的 `http://` 是 `HTTP_PROXY`/`HTTPS_PROXY` **代理地址**，不是 base_url——代理走 http:// + CONNECT 隧道是标准做法，不在改造范围内。

### 额度查询的网络路径必须跟 provider 一致（本机实测发现）

provider 的 env 块里**可能有代理设置**——本机实测：

```json
// iflytek-jlcao3.json
"HTTPS_PROXY": "http://t14.local:7890",
"HTTP_PROXY":  "http://t14.local:7890",
"NO_PROXY":    "127.0.0.1,localhost,192.168.1.,xunfei.cn,.iflytek.com",
"ANTHROPIC_BASE_URL": "https://one.iflytek.com/api/llm/console/chat/"
```

**关键事实：provider 的 env 块是通过 `--settings` 交付给 Claude Code 的，不是 ccl 进程的环境。** `ccl quota` 是**另一个进程**，它的环境里没有这些代理变量。

→ **后果**：一个"必须走代理才能访问"的 provider，Claude Code 能用（代理变量随 settings 交付给它了），但 `ccl quota` 直连会超时 → 额度查不到。**额度查询必须复用该 provider 的代理配置**，否则两者看到的网络世界不一样。

→ **实现约束**：Go 的 `http.ProxyFromEnvironment` 读的是**进程环境**，不能直接用；quota 的 HTTP 客户端要显式构造——从 provider 的 env 块里取 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`，按 `NO_PROXY` 决定某个 host 走不走代理（Go 的 `NO_PROXY` 语义是**按 host 后缀匹配**，`no_proxy` 小写形式也要认）。

→ 本机两个 provider 的代理情形各不相同，正好覆盖两种分支：
- `iflytek`：`NO_PROXY` 含 `.iflytek.com` → **不走代理**（实测直连可达）
- `claude-official`：`HTTP_PROXY` 指向 `42.192.60.90:31093`，无 `NO_PROXY` → **要走代理**

## Goals / Non-Goals

**Goals:**

- 状态栏在第三方 provider 下显示真实的额度/余额，而不是两个 `--`。
- **额度模型能表达不同厂商的周期差异**：有 5h/7d 的、有只有月的、有 5h+周+月并存的、有根本没有窗口只有余额的。
- 额度查询逻辑**在仓库里**（可测、可版本化），状态栏只负责展示。
- **刷新频率与调用频率解耦**：额度由 serve 按周期刷新（默认 60 秒），状态栏与 `ccl quota` 都只读本地数据——状态栏路径恒定 ~15ms、一次网络都不发。
- 查询失败**绝不影响 Claude Code 启动**——额度是锦上添花，provider 能不能生效才是生死线。
- **接一个新厂商的成本可控**：形状常见的（New API 系 / DeepSeek 系）**只加配置不发版**；形状特殊的才写 adapter。

**Non-Goals:**

- 不做插件系统、不做声明式 JSON 路径映射、不做通用额度 DSL。
- 不做 RPM/TPM 速率限制的建模（速率 ≠ 额度）。
- 不做额度告警、自动切换 provider、预算控制。
- 不把额度数据放进 `--settings` 的 env 块（额度随时间滚动，启动快照在会话内会过时）。
- 不改 Claude Code 的 `rate_limits` 语义，不接管 `statusLine` 配置。

## Decisions

### 决策 1：额度刷新由 **`ccl serve`** 负责；`ccl quota` 与状态栏**只读本地数据**

```
ccl serve（常驻，已自动启动）
  └─ 额度刷新 ticker（默认 60s，per-provider 可配）
       └─ 写 ~/.cc-launcher/quota.json（原子写）

ccl quota --json（一次性，纯本地）
  └─ 读 ~/.cc-launcher/quota.json → 打印

statusline.js
  └─ execSync('ccl quota --provider "$CCL_PROVIDER" --json')
```

**核心抽象：刷新频率与调用频率解耦。** 状态栏的调用频率由 Claude Code 决定（生成中约每 300ms 一次），额度的刷新频率由用户决定（默认 60s）。两者本来就不该是同一件事。

**为什么是 serve 而不是状态栏自己查：**

| 备选 | 否决理由 |
|---|---|
| 状态栏脚本自己 curl 厂商接口 | 解析逻辑落在**仓库外的个人脚本**里——不可测、不可版本化、换 provider 要重写一遍。刚做完的 `CCL_PROVIDER` 把"ccl 拥有契约、消费方只负责展示"这条分工立起来了，这个需求应当沿用同一条线 |
| 状态栏触发查询（`ccl quota` 命中本地数据则不联网，否则同步查） | 状态栏路径**慢的时候会被 Claude Code 打断重试**（见决策 13）；且"每 300ms 触发一次网络"是把数据新鲜度和 UI 刷新频率绑死 |
| **serve 定期刷新 + 命令读文件** ✅ | **采纳** |
| 在 `--settings` 的 env 块里交付启动时的快照 | 额度是**滚动窗口**的量（5 小时窗口随时在走）。90 分钟的会话里，启动快照的 5h 百分比能差出十几个点——显示一个明显过时的数字比不显示更糟 |

**"serve 不是必然在跑"这条顾虑不成立**（原设计的否决理由，已过时）：

- `daemon-lifecycle` spec：**每次 `ccl` 启动 Claude Code 前**都探 `localhost:4318`（200ms TCP 超时），不可达就 fork `ccl serve`（detached、新 PGID）并等 ≤3s → **serve 几乎必然在跑**
- `ccl serve --help` 明写：**永不自动 idle 退出**；要关就 `ccl serve --stop`
- serve 自带 pidfile、SIGTERM graceful shutdown（≤5s）、`--status`、`--restart`

→ 额度刷新搭在一个**已经解决生命周期问题**的常驻进程上，不需要新建轮询体系。

**唯一退步**：serve 没跑时额度功能整体不可用（退化成状态栏回落 Claude Code 的 `rate_limits`，**不崩**）。这是可接受的：serve 由 ccl 自动拉起，首次刷新在 daemon 起来后立即发生（不等一个周期）。

**`--provider` 的来源**（读取侧的三级回落）：显式 `--provider` 优先；否则读**环境变量 `CCL_PROVIDER`**（状态栏子进程继承得到，正是上一个 change 注入的）；再否则用 `state.json` 的 active provider。三级回落让脚本侧只要传 `--provider "$CCL_PROVIDER"` 就行，别的场景也能直接用。

### 决策 2：额度输出 = **维度列表**，不是两个固定槽位

```json
{
  "provider": "minimax-m3",
  "source": "minimax",
  "stale": false,
  "fetched_at": 1793943914,
  "dimensions": [
    {
      "id": "interval", "label": "5h", "kind": "window",
      "duration_seconds": 18000,
      "used_percentage": 4, "resets_at": 1793961914
    },
    {
      "id": "weekly", "label": "7d", "kind": "window",
      "duration_seconds": 604800,
      "used_percentage": 33, "resets_at": 1794323556
    }
  ]
}
```

**为什么不用 Claude Code `rate_limits` 的 `five_hour` / `seven_day` 形状：**

| 破绽 | 后果 |
|---|---|
| 窗口时长被焊进键名 | 只有月额度的厂商塞进 `five_hour` → 标签写 5h 实际 30 天 = **说谎** |
| 缺失的维度只能缺席 | 只有月额度的厂商 → 两个槽位全空 → 状态栏显示 `-- \| --`，用户明明还有额度却看着像坏了 |
| 没有第 N 个窗口的位置 | 百炼是 5h + 周 + 月**三个并存**，`{five_hour, seven_day}` 只能塞两个 |

**而且现在改的成本最低**：这个 change 尚未实施，`ccl quota --json` 的契约**还没发过版**，仓库外**还不存在**它的消费方（只有用户自己的 `~/.claude/statusline.js`）。发版后再改形状 = 所有外部消费方都得跟着改。

**`kind` 决定状态栏怎么画**（三种画法，见决策 12）：

| kind | 语义 | 画法 |
|---|---|---|
| `window` | 有周期 + 有分母 | 条形图 + 百分比 + 重置时间 |
| `quota` | 无周期 + 有分母 | 条形图 + 绝对值（`$399.99 / $400`） |
| `balance` | 无分母 | **只显示数字**（不画条形图） |

**`duration_seconds` 是维度的属性，不是分类依据**——这是整个模型能收敛的关键。见决策 3。

### 决策 3：`Dimension` 模型——周期降级为属性，百分比是派生的

```go
// internal/quota/model.go

// Kind 是额度维度的类别。
type Kind string

const (
    // KindWindow 有周期 + 有分母：会重置的额度窗口（5h / 7d / 月）。
    KindWindow Kind = "window"
    // KindQuota 无周期 + 有分母：总额度制，不按周期重置（令牌额度、包月总额）。
    KindQuota Kind = "quota"
    // KindBalance 无分母：只有剩余量，算不出百分比（预付费余额）。
    KindBalance Kind = "balance"
)

// Dimension 是一个额度维度。
//
// 三个正交属性：
//   - 有没有分母（Limit != nil）→ 决定能不能算百分比、能不能画条形图
//   - 会不会重置（Duration > 0 / ResetsAt != nil）→ 决定是不是"窗口"
//   - 是不是无限（Unlimited）→ 无上限时百分比无意义
type Dimension struct {
    ID        string        // 稳定标识（"interval" / "weekly" / "balance"）
    Label     string        // 展示名，**必须全是 ASCII**（见下）
    Kind      Kind
    Duration  time.Duration // 0 = 不重置
    Used      *float64      // 已用绝对量
    Limit     *float64      // 总量；nil = 不知道分母
    Remaining *float64      // 剩余绝对量
    Unit      string        // "%" / "USD" / "CNY" / "tokens"，**必须全是 ASCII**
    ResetsAt  *time.Time    // 下次重置（KindWindow）
    ExpiresAt *time.Time    // 到期时间（令牌型，可选）
    Unlimited bool          // 无限额度
    Raw       map[string]any // 厂商原始字段，便于排查
}

// UsedPct 是**派生**的，不是接口给的，也不是存出来的。
func (d Dimension) UsedPct() *float64 {
    if d.Unlimited || d.Limit == nil || *d.Limit == 0 || d.Used == nil {
        return nil
    }
    v := *d.Used / *d.Limit * 100
    return &v
}
```

**为什么这样切分类**——各家厂商的差异其实是**两个正交属性**的组合：

```
             无周期                有周期
有分母      额度型(讯飞令牌)        窗口型(MiniMax 5h/7d、百炼)
无分母      余额型(DeepSeek)           —
```

于是"有没有 5 小时"**根本不是一个分类问题**，而只是 `Duration` 的取值。厂商有几个窗口就输出几个维度，一个窗口都没有也不影响别的厂商——"只有月额度"天然被支持，不需要任何特例代码。

**`Remaining` 与 `Used` 必须分成两个字段**，不能合并成一个 `Amount`：余额是"剩余"、额度是"已用"，**方向相反**。合并成一个就要靠约定猜方向，而约定就是 bug 的来源。

**百分比是派生的**，好处是三家厂商一套模型、不需要 if-else：

| 厂商 | Limit | Used | `UsedPct()` | kind |
|---|---|---|---|---|
| MiniMax | 隐含 100 | 隐含 4 | 4 | window |
| DeepSeek | **nil** | nil | **nil**（无分母） | balance |
| 讯飞 New API | 200000000 | 50723 | 0.0253615 | quota |

**"已用百分比"的语义统一为"已用"**（MiniMax 给的是剩余百分比，adapter 负责 `100 - remaining` 转换）→ 同一行里不会一个朝左一个朝右。

**`label` 与 `unit` 必须是纯 ASCII，且有确定的生成规则**——状态栏是直接渲染到终端的一行文本，**中文/符号字体缺失时无法渲染**，而且 `label` 可能间接来自厂商响应（不可信）。

| kind | `label` | `unit` | 例 |
|---|---|---|---|
| `window` | **从 `duration_seconds` 派生**：`18000`→`5h`、`604800`→`7d`、`2592000`→`30d` | `%` | `5h` / `7d` |
| `quota` | **`unit` 的值** | 货币代码或 `tokens` | `USD` / `tokens` |
| `balance` | **`unit` 的值** | 货币代码 | `CNY` / `USD` |

**非窗口型为什么用 `unit` 当 label**：余额/总额度没有天然的短标签（不是 "5h"），而它的 `unit` 恰好就是货币代码——`CNY` / `USD` 既是准确语义又是纯 ASCII。

**规则只有一条**：**`label` 永远不取自厂商响应的原始字符串**（厂商的字段名可能是中文、可能含控制字符）。脚本侧仍然要 `sanitizeLabel` 兜底（防御性），但那是最后一道防线，不是唯一一道。

### 决策 4：adapter 层——厂商差异全部关进盒子

```go
// internal/quota/adapter.go

// Adapter 是某厂商的额度适配器。
type Adapter interface {
    // Name 是适配器名（写入输出的 source 字段）。
    Name() string
    // Match 判断该适配器是否负责这个 provider（看 base_url）。
    Match(baseURL string) bool
    // Endpoint 推导额度端点。第二个返回值 false = 推导不出来。
    Endpoint(baseURL string) (string, bool)
    // Fetch 发起请求并解析，返回统一的维度列表。
    // 成败由适配器自己判定（各厂商的失败协议不同，见决策 9）。
    Fetch(ctx context.Context, p ProviderInfo) ([]Dimension, error)
}

// ProviderInfo 是查询一个 provider 额度所需的输入。
type ProviderInfo struct {
    BaseURL string
    APIKey  string
    Model   string
}

// 注册表：加一个厂商 = 一个新文件 + 这里加一行。
var adapters = []Adapter{
    minmaxAdapter{},
    deepseekAdapter{},
    newapiAdapter{},
}
```

**`Fetch` 拥有整个请求**是有意的：不同厂商的认证头、签名方式、超时要求都可能不同（New API 用 Bearer，某些云厂商要 AK/SK 签名）。放在 adapter 里，ccl 的 store / refresh 层就不用管这些差异。

**ccl 统一管的**：超时（ctx）、额度池去重、刷新周期、数据文件写入、新鲜度判定、输出形状、退出码分级。

**加一个厂商的成本**：
- 形状常见的（New API 系 / DeepSeek 系）→ **只加一行配置，不发版**（见决策 6）
- 形状特殊的 → 写一个 adapter + 注册表加一行 + 发版（代码问题，发版可接受）

### 决策 5：端点推导是 **per-adapter**，不是全局规则

三家的推导规律各不相同（实测）：

| provider | base_url | 额度端点 | 规律 |
|---|---|---|---|
| MiniMax | `api.minimax.cn/anthropic` | `www.minimax.cn/v1/token_plan/remains` | **换子域** + 换路径 |
| DeepSeek | `api.deepseek.com/anthropic` | `api.deepseek.com/user/balance` | **同主机** + 换路径 |
| New API 系 | `https://host/<prefix>` | `<baseURL>/api/usage/token/` | **同前缀** + 固定后缀（**带尾斜杠**，见下） |

**任何全局规则都会推导出错端点**。原设计里"识别为 MiniMax 时 → `https://www.<host>/v1/token_plan/remains`"这句是 MiniMax 专属规则，被写成了通用规则——接 DeepSeek 会推出 `www.deepseek.com/user/balance`（错的）。

**MiniMax 的识别规则**（保留原设计，理由不变）：去掉前导 `api.` / `www.` 之后，主机名**恰好等于** `minimax.cn` 或 `minimax.io`。用"包含 minimax"会把 `minimax-proxy.corp.example` 这类自建域名误判成 MiniMax，推出 `www.minimax-proxy.corp.example` 这种垃圾端点。宁可识别不出来，也不要识别错。

**兜底必须"恰好等于"的另一个理由**：`.cn` 与 `.io` 的 key **不通用**（实测 `.cn` 的 key 打 `.io` 返回 `2049 invalid api key`）。端点必须跟着 base_url 的域名走，不能硬编码。

### 决策 6：provider 配置键 `quota`（对象）

```json
{
  "env": { "ANTHROPIC_BASE_URL": "...", "ANTHROPIC_AUTH_TOKEN": "..." },
  "quota": {
    "source": "newapi",
    "endpoint": "https://one.iflytek.com/api/llm/console/chat/api/usage/token/",
    "quotaPerUnit": 500000,
    "displayUnit": "USD",
    "refreshInterval": 60
  }
}
```

| 字段 | 必填 | 含义 |
|---|---|---|
| `source` | ✅ | 选内置 adapter（`minimax` / `deepseek` / `newapi`） |
| `endpoint` | ❌ | 覆盖自动推导（私有网关、二开路径） |
| `quotaPerUnit` | ❌ | 额度点换算比例（默认 500000 = New API 出厂默认） |
| `displayUnit` | ❌ | 显示单位（默认由 adapter 决定） |
| `refreshInterval` | ❌ | 刷新周期，**秒**（默认 60）——见决策 7 |

**为什么用对象键而不是 `quotaEndpoint` 字符串键**：
1. 原设计的 `quotaEndpoint` **单独存在是废的**——三个厂商的响应形状零交集，配了端点但没有"用哪个解析器"照样用不了。`source` 与 `endpoint` 必须成对出现。
2. 对象键让两个字段**同生共死**，杜绝"配了 endpoint 忘了 source"的半截状态。
3. 它只占 `ReservedKeys` **一个**键，以后加 `quotaHeaders` / `quotaAuth` 有地方放。

**`source` 让接厂商零发版**：本机每个 provider 显式声明用哪个 adapter，不依赖 host 匹配的启发式规则（启发式会误判，也覆盖不了私有部署）：

```json
{ "env": {...}, "quota": { "source": "newapi" } }      // 讯飞
{ "env": {...}, "quota": { "source": "deepseek" } }    // deepseek ×2
{ "env": {...}, "quota": { "source": "minimax" } }     // minimax ×3
{ "env": {...} }                                       // claude-official：没有额度接口
```

**`quota` 是 ccl 自己的元数据键，必须进 `provider.ReservedKeys`**（与 `note` / `order` 同等对待）。这构成对 `provider-settings-passthrough` 的**修改**（排除清单扩了一个键）。

### 决策 7：数据文件 `~/.cc-launcher/quota.json` + **刷新周期默认 60 秒**

**文件**：`~/.cc-launcher/quota.json`，权限 **0600**，**原子写**（临时文件 + rename，与 `settingsfile.go` 的做法一致）→ 读取侧并发读不会读到半个文件。

```json
{
  "version": 1,
  "updated_at": 1793943914,
  "entries": {
    "minimax-m3": {
      "source": "minimax",
      "pool": "3f2a9c...",
      "fetched_at": 1793943914,
      "dimensions": [ { "id":"interval", "label":"5h", "kind":"window", "duration_seconds":18000, "used_percentage":4, "resets_at":1793961914 } ]
    },
    "minimax-m3.1-flash": {
      "source": "minimax",
      "pool": "3f2a9c...",
      "fetched_at": 1793943914,
      "dimensions": [ ... 同上 ... ]
    }
  }
}
```

- **按 provider 名索引**：读取侧（`ccl quota --provider X`）要按名字直接找。
- **冗余存储维度**：同一个额度池被多个 provider 引用时内容重复——冗余换来读取侧零查找成本，且**每个 provider 条目自带 `fetched_at`**，新鲜度判定不需要二次查询。
- **额度池按 (端点, key) 去重**：刷新时同一个额度池**只查一次网络请求**，结果写进所有引用它的 provider 条目。`pool` 字段 = `sha256(端点 + "\x00" + API key)`：
  - **含 API key**：额度是**按 key 分**的，不是按 provider 名分的。现有 3 个 minimax provider 里 `minimax-m3` 与 `minimax-m3.1-flash` 共用同一把 key → 同一个额度池 → **只查一次**。
  - **只存 key 的哈希，不存 key 本身**：文件里不该躺明文凭证。

**刷新周期**：

- **默认 60 秒**，per-provider 可配：provider 配置的 `quota.refreshInterval`（秒）。
- serve 的 ticker 周期 = 所有待刷 provider 的 `refreshInterval` 的**最小值**；每 tick 遍历 provider，**距上次刷新不足其自身 `refreshInterval` 的跳过这一轮** → 周期不同的 provider 互不干扰（余额型可以配 300 秒，窗口型保持 60 秒）。
- **serve 启动后立即刷一轮**（不等一个周期）→ 用户跑完一次 `ccl`（它会拉起 serve）就能读到数据。

**`pool` 不含 model**：三家已实测的接口返回的都是 **key 级别**的总量（讯飞的 `total_*` 是令牌级，New API 的 `model_limits` 只在启用按模型限额时才影响分池），所以同 key 不同模型共享一个额度池是对的。（若将来 adapter 要取"按模型的已用量"，那时才把 model 加进去——见 Open Questions。）

### 决策 8：新鲜度由 `fetched_at` 判定，stale 是**连续**的状态

不再是"这次请求失败才 stale"——serve 是定时任务，读取侧看到的是"这份数据是什么时候取的"。

- `ccl quota` 读出条目后，用 `fetched_at` 算 age。
- age 超过该 provider 的 `refreshInterval`（**兜底 60 秒** = 默认刷新周期；provider 配置读不到时用它）→ 输出标 `stale: true`。
- **serve 刷新失败时保留旧条目**（不覆盖，`fetched_at` 不变）→ `fetched_at` 越来越旧 → stale 自动变 true → 用户看得出"数据不新鲜"而不是"额度没了"。错误详情只进 serve 的日志，不写进数据文件。
- **额度不会因为一次网络抖动就变没**——状态栏从数字跳回 `--` 是很糟的体验（用户会以为额度用光了）。
- serve 每轮**读-改-写**（`store.Update` + `store.Prune`，见决策 15）：当前不存在的 provider 由 `Prune` 清掉 → 残留条目自动清理；查询失败的 provider **不调 `Update`** → 沿用旧条目。

### 决策 9：成败判据由 adapter 自己定——**三套协议**

| 厂商 | 成败判据 | 错误体 |
|---|---|---|
| MiniMax | `base_resp.status_code == 0` | HTTP **200** 也可能是错误（`1004` / `2049`） |
| DeepSeek | HTTP 状态码（401） | **可能是纯文本**（无 Bearer 头时：`Authentication Fails (governor)`），也可能是 JSON |
| New API 系 | `code == true` | `{"error":{code,message,request_id,type}}` + 401 |

**推论**：
1. **HTTP 状态码不能作为通用判据**（MiniMax 错误也返 200），**body 里的判据也不能作为通用判据**（三家各不相同）→ 判据下沉到 adapter。
2. **错误体不能无脑 `json.Unmarshal`**——DeepSeek 无 Authorization 头时返回的是纯文本。adapter 必须能处理"错误体不是 JSON"。
3. 认证层的失败与业务层的失败**要分开归类**：DeepSeek 401 是"key 不对"，MiniMax `2049` 是"key 不对"，而 HTTP 200 + `status_code: 1004` 是"登录失败"——都是失败，但对用户有用的措辞不同。

### 决策 10：额度端点**跟随重定向**，不改写 scheme；但对明文 base_url 提示

实测：讯飞的 `http://one.iflytek.com/...` **302 重定向**到 `https://one.iflytek.com:443/...`。

**实测结论（Go 实测，非推断）**：Go 默认的 `http.Client` 跟随重定向，且**跨 http→https 重定向保留 `Authorization`**——`shouldCopyHeaderOnRedirect` 按 `url.Hostname()`（**剥掉端口**）比对，`one.iflytek.com` vs `one.iflytek.com:443` 判定为同主机 → 敏感头不剥。

```
最终 scheme   : https
Authorization : Bearer sk-secret      ← 保留
```

**→ ccl 不主动把 `http://` 升级成 `https://`**，依赖默认的重定向行为。两条理由：

1. **主动升级会弄坏本来能用的配置**：一个只提供 http 的内网额度服务（`http://quota.corp/usage`），升级成 https → TLS 握手失败 → **额度永远查不到**。
2. **用户显式配的 `quota.endpoint` 尤其不该被 ccl 改写**——那是配置者自己的选择。

**但明文 base_url 要提示**：provider 的 `ANTHROPIC_BASE_URL` 是 `http://` 时给 warning（`providerConfig.Warnings` 已有现成位置）。两个理由：① **API key 明文过网**；② Claude Code 未必跟随 http→https 重定向 → provider 可能直接失效。

**ccl 不自动改写用户的 base_url**——那是静默改用户配置语义。ccl 的规矩是提示而不是替用户做主。真正的修复点是让用户把配置改成 https。

**本机现有 provider 无需改动**：deepseek ×2 / minimax ×3 / iflytek 的 base_url 已是 https；`claude-official.json` 里的 `http://` 是 `HTTP_PROXY` / `HTTPS_PROXY` **代理地址**，不是 base_url——代理走 http:// + CONNECT 隧道是标准做法。

### 决策 11：额度查询走 **provider 自己的网络路径**（代理）

额度查询的 HTTP 客户端 MUST 使用**该 provider 的 env 块**里的 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`，而不是 ccl 进程的环境。

**理由**（本机实测）：provider 的 env 块是**通过 `--settings` 交付给 Claude Code 的**，不是通过进程环境。所以：

- **Claude Code** 拿得到 provider 的 `HTTP_PROXY` / `NO_PROXY`（settings 的 env 块）→ 走 provider 指定的网络路径
- **serve**（额度刷新在这里发生）是**从 ccl fork 出来的**，继承的是**用户 shell 的环境**，**不是** provider 的 env 块 → 默认走 shell 的代理设置

一个"必须走代理才能访问"的 provider，Claude Code 能用，但 serve 用 shell 的代理去连就会超时。**两者看到的网络世界必须一样** → serve 刷新额度时**自己读 provider 的配置文件**，取它 env 块里的代理设置。

（注意：serve 读的是 provider 配置文件，不是 Claude Code 的 stdin/env —— `--settings` 的 env 块只在 Claude Code 进程里。）

**实现要点**：Go 的 `http.ProxyFromEnvironment` 读的是进程环境，不能直接用——客户端要显式构造：按 `NO_PROXY`（含小写 `no_proxy`）决定某个 host 走不走代理，Go 的 `NO_PROXY` 语义是**按 host 后缀匹配**。

**不引入新依赖**：手写一个几十行的 `NO_PROXY` 匹配就够，不为了这个引入 `golang.org/x/net/http/httpproxy`。

**验证**：本机两个 provider 覆盖两种分支——`iflytek` 的 `NO_PROXY` 含 `.iflytek.com` → 不走代理（实测直连可达）；`claude-official` 只有 `HTTP_PROXY` → 走代理。

**`ccl quota --refresh` 同理**——它也是一个独立进程，读的是**同一个 provider 的 env 块**，所以走的网络路径与 serve 一致。

### 决策 12：状态栏侧——**两条分支不合并**，按 `kind` 三种画法

```js
// 分支 A：ccl quota 退出码 0 → 完全用 quota 的维度清单（有几个显示几个）
// 分支 B：ccl quota 退出码非 0 → 保持今天 Claude Code 的两个固定槽位（缺就显示 --）
```

**为什么两条分支不合并**——"缺一个维度"的含义在两个来源里**不同**：

| 来源 | 缺一个维度 | 含义 | 该怎么办 |
|---|---|---|---|
| ccl quota 的 `dimensions` | 厂商就没这个限制 | 确定没有 | **不显示**（不占槽位） |
| Claude Code 的 `rate_limits` | 官方没填这个槽位 | 可能是"没这个限制"，也可能是"这次没拿到" | **保留 `--`**（保持今天行为，不猜） |

合并会把这两个语义抹平。分支 B 保留 `--` → **Anthropic 官方 provider 的输出一个字节都不变**。

**三种画法**：

```js
// window：现有 rateLimitStr() 一行不改（dimension 是 rate_limits 项的严格超集）
// quota：条形图 + 绝对值
// balance：只显示数字——Limit 未知画不出分母，且"已用"与"剩余"方向相反
```

**标签与日期的取值规则**（与决策 3 一致）：
- **窗口型**：`label` = 从 `duration_seconds` 派生（`18000`→`5h`、`604800`→`7d`、`2592000`→`30d`）；是否总带日期 = `duration_seconds > 86400`（"7 天后重置"只写 `03:14` 会让人以为是今天）
- **非窗口型**：`label` = **ccl 给的 `unit`**（`CNY` / `USD` / `tokens`）→ 显示成 `CNY: 5.17`
- ccl 给的 `label` 优先，但**脚本侧仍要 `sanitizeLabel`**（防御性；ccl 已保证它是 ASCII 且不取自厂商原始字符串）

**`dimension` 是 Claude Code `rate_limits` 项的严格超集**：`used_percentage` 名字一样（都是"已用百分比"，决策 3 统一了语义），`resets_at` 单位一样（Unix 秒）→ **现有 `rateLimitStr(limit, label, includeDate)` 一行都不用改**，只是喂进去的东西换了。

**优先级**：quota 优先，回落 Claude Code 自己的 `rate_limits`。MiniMax provider：Claude Code 的 `rate_limits` 本来就是空的 → quota 填上。Anthropic 官方 provider：`ccl quota` 退出码 2（没有额度接口）→ 回落，行为完全不变。

**状态栏只调 `ccl quota --json`，不直接读数据文件**——文件位置与格式是 ccl 的内部细节，屏蔽在命令后面（决策 1）。

**维度数量上限**：终端宽度是硬约束。状态栏侧可按 `duration_seconds` 升序截断（稳定的"最紧的窗口在前"），**但这是显示策略不是契约**——ccl 只管把完整清单交出来。

### 决策 13：状态栏路径的**时延预算**——纯本地读，恒定 ~15ms

**实测数据**（Claude Code 2.1.311 二进制，状态栏类 `n$r` 逐行读出）：

| 事实 | 出处 | 含义 |
|---|---|---|
| 命令超时 **600000ms = 10 分钟** | `var xf=600000`（由 `chunk-93rmhj5a.js` 导出、状态栏 chunk import）+ 判定 `Date.now()-w>=xf?"timeout":...` | **超时不是瓶颈** |
| statusLine 设置**没有 `timeout` 字段** | schema `{type, command, padding, refreshInterval}` | 走默认 10 分钟，用户无法调 |
| **300ms 去抖窗口** | `#_(){...setTimeout(()=>{this.#b()}, hRr)}`，`hRr=300` | 命令要在 ~200ms 内返回 |
| **每次执行前 abort 上一次** | `#b(){this.#s?.abort(); let e=new AbortController(); ...}` | 命令慢 → 被无限 abort + 重启 → **永远出不来** |
| 默认**无定时循环** | `#f()` 里 `mtt(statusLine)` 为 null 直接 return | 只有显式设 `refreshInterval` 才有周期刷新 |
| 事件驱动触发，含 `tokenUsage` | `SRr=["tokenUsage","permissionMode","vimMode",...]` | **生成过程中约每 300ms 一次** |
| 额度重置点唤醒只对 Anthropic 官方有效 | `#w(wRr(G))`，`wRr` 取 `rate_limits.*.resets_at` | 第三方 provider 的 rate_limits 为空 → `wRr` 返回 null → **不调度** |

**→ 设计要求：状态栏路径必须是纯本地读取，一次网络都不发。**

本设计已经把它做成了：**额度刷新由 serve 负责（决策 1），`ccl quota` 只读本地数据文件（决策 7）**。于是状态栏路径是：

```
statusline.js → execSync('ccl quota --json') → 读 ~/.cc-launcher/quota.json → 打印
                                        ↑ 纯本地，~15ms（含进程启动），恒定
```

**推论**：

1. **超时完全够**（10 分钟 vs ~15ms）→ 这条顾虑彻底放下。
2. **300ms 去抖窗口 + abort 风暴的风险消失**——命令恒定 ~15ms 返回，不会被反复打断重试。
3. **真实负载是每 300ms 一个进程**（生成中）→ 每秒 3.3 个 `ccl` 进程，但每个都只读文件、不联网。

**⚠️ 命名陷阱**：Claude Code 的 `statusLine.refreshInterval`（状态栏自己的刷新频率，秒）与 ccl 的 `quota.refreshInterval`（**额度刷新周期**，秒）**同名但完全无关**。文档里出现 `refreshInterval` 时要看清楚是谁的。

**状态栏侧的 `execSync` timeout**：设 **250ms**（小于 Claude Code 的 300ms 去抖窗口）。正常路径 ~15ms，留 15 倍余量；万一读文件卡住（网络盘？），脚本自己能超时退出 → 静默回落 Claude Code 的 `rate_limits`，**状态栏不会卡住**。

**明确不选**：
- 状态栏直接读 `~/.cc-launcher/quota.json` → 0 进程，但**文件位置与格式变成仓库外脚本的契约**；用户已定：**调命令**，把位置细节屏蔽在 ccl 内部。
- 状态栏同步等网络 → 慢时落进 abort 风暴。

### 决策 14：退出码分级

| 码 | 含义 | 消费方该怎么做 |
|---|---|---|
| `0` | 读到额度 | 用 `dimensions` |
| `2` | 该 provider **没有额度接口**（正常状态，不是错误）——**纯离线可判**（读 provider 配置就知道） | 回落 Claude Code 的 `rate_limits` |
| `1` | **读不到数据**：serve 没跑 / 首次刷新还没完成 / 该 provider 尚无条目 | 回落 Claude Code 的 `rate_limits` |

原设计只说"非 0"，消费方分不出"这厂商没额度"和"数据还没准备好"。分级后状态栏能区分处理。

**读取侧不再联网**，所以不再有"查询失败"这一类退出码——网络失败发生在 serve 侧，表现为**旧条目 + `stale: true`**（决策 8），而不是读取侧失败。

### 决策 15：`--refresh` 手动刷新一次——**读-改-写**，不需要文件锁

`ccl quota --refresh` = **联网查一次 → 写回数据文件 → 照常输出**（输出的是刚查到的值，不是文件里的旧值）。

**不拆成两个 flag**：`--refresh` 同时做"刷新"和"输出"。用户 `ccl quota --refresh --json | jq` 一步到位；脚本场景也够用（只想要副作用就把 stdout 重定向掉）。

**它解决的是"serve 没跑"的兜底**：serve 被 `ccl serve --stop` 关掉时，用户手动刷一次，状态栏就能看到数据（虽然之后不再自动更新）。**`--refresh` 不需要 serve 在跑。**

**并发写文件：不需要锁。** 前提是把 serve 的"每轮全量重建"改成"**读-改-写**"（`store.Update` + `store.Prune`）：

| 冲突情形 | 后果 | 严重吗 |
|---|---|---|
| 两个写者写**同一个额度池** | 内容等价，`fetched_at` 谁新谁旧 | **无害** |
| `--refresh` 覆盖了 serve 刚写的**别的** provider 条目 | 那个条目缺失 | serve 下轮重建 → **≤ 一个刷新周期自愈** |
| serve 的 `Prune` 删掉了 `--refresh` 刚写的条目 | 那个条目缺失 | 同上 |

→ **不需要文件锁**。最坏后果是"某个条目短暂缺失一个周期"，而原子写保证**不会读到半个文件**。

**顺带白捡一个协同：`fetched_at` 当"上次刷新时间"。**

实现要求：serve 判断"这个 provider 该不该刷"时，**必须从数据文件里读 `fetched_at`**，不能只记在 serve 的内存 map 里。于是：

```
用户跑 ccl quota --refresh  →  写 fetched_at = now
serve 下一轮看到 age=0 < refreshInterval(60s)  →  跳过这一轮
```

→ **`--refresh` 与 serve 天然互斥，不会重复查询同一个额度池**。这是"文件即状态"换来的，不需要任何锁。

**不选的方案**：让 `--refresh` 通过本机 HTTP 请求 daemon 触发刷新（daemon 加 `POST /quota/refresh`）——daemon 是唯一写者，零并发问题，但要引入 HTTP 端点 + 认证，且 daemon 没跑时还得回落到本地写。复杂度更高，收益只有"避免一个周期内的条目缺失"，不值。

## Risks / Trade-offs

- **[厂商改了接口字段或端点]** → 解析失败必须**降级而不是崩**：失败发生在 **serve 侧** → 旧条目保留（`fetched_at` 变旧 → 自动 stale）+ 错误进 serve 日志；读取侧完全不受影响。**绝不允许**额度查询失败影响 Claude Code 启动（那是生死线，额度只是锦上添花）。外部行为的实测结论记进 `docs/provider-quota.md`，升级/改接口后重验。
- **[状态栏每次渲染多起一个进程]** → ~15ms，可接受（脚本本来每次渲染就起 `git`）。命令**纯本地读文件、不联网**，耗时恒定。
- **["`quota` 被当成普通 provider 顶层键透传]** → 加进 `provider.ReservedKeys`（决策 6），并加单测守住。
- **[数据文件里的 provider 名与百分比泄露]** → 文件 0600；**不存 API key 明文**，只存哈希。
- **[厂商侧限流额度查询接口]** → 实测 150ms；HTTP 客户端超时 5 秒；**刷新周期默认 60 秒 × provider 数量**（去重后本机约 5 次/分钟）——周期可配，余额型建议调大。
- **[serve 没跑 → 额度功能整体不可用]** → 唯一的退步。缓解：ccl 每次启动 Claude Code 都会自动拉起 serve（`daemon-lifecycle` spec），且 serve 永不自动 idle 退出；读取侧失败时状态栏回落 Claude Code 的 `rate_limits`，**不崩**。
- **[数据文件被并发读到半个]** → 原子写（临时文件 + rename）。
- **[错误体不是 JSON]** → adapter 的错误解析必须容忍纯文本（DeepSeek 无 Bearer 头时实测如此）。
- **[纯靠 `source` 配置接厂商，配置写错就静默失效]** → `source` 找不到对应 adapter 时 serve 刷不了 → 文件里没有该 provider 的条目 → `ccl quota` **退出码 1 + stderr 说明**，不静默返回空。（退出码 2 只留给"确实没有额度接口"的 provider。）
- **[`quotaPerUnit` 硬编码 500000，遇到非默认部署就显示错钱数]** → 可配置覆盖；且实测该实例的 `quota_per_unit` 恰好等于默认值。已知偏差时应显式配置。
- **[5 小时窗口的百分比在会话中会变旧]** → 刷新周期默认 60 秒，误差上限约 1 分钟；且 `fetched_at` 让陈旧程度**可见**（`stale: true`）。
- **[`quota.endpoint` 指向第三方地址会带上 API key]** → 这是配置者自己的选择（provider 配置本来就带端点与凭证）；但要在文档里写明"这个地址会收到你的 API key"。
- **[只内置三个 adapter，其余厂商接不了]** → 这是刻意的范围限制。形状常见的（New API 系 / DeepSeek 系）**已经能用 `source` 接上**；形状特殊的（如按次计费、按 token 计费的复杂套餐）需要写 adapter。

## Migration Plan

无迁移：纯增量。不改 provider 文件格式的**必填项**（`quota` 块整体可选）；不改 `~/.claude/settings.json`；不改 Claude Code 行为。

回滚：把 provider 配置里的 `quota` 块删掉即可回到"没有额度查询"；彻底回滚则是回退 ccl 二进制（状态栏脚本侧的 `execSync` 会因为 `ccl quota` 不存在而失败 → 状态栏保持原样，不会崩）。

## Open Questions

- `current_interval_status` / `current_weekly_status` 的完整取值语义未知（实测只见 `1`）。当前实现不解释这两个字段，只透传原始值到 `Dimension.Raw`。若将来发现 `0` = 已耗尽，可在 MiniMax adapter 里补上"耗尽时置 0%"的规则——但那是**基于观察的推断**，需要更多样本佐证。
- MiniMax 是否会为不同模型给出多条 `model_remains` 条目（当前恒为 1 条 `"general"`）。若将来出现，`general` 优先的规则仍然成立（它代表整体配额池）。
- **New API 的 API 层失败体形状未实测**：认证层已确认是 `{"error":{...,"type":"new_api_error"}}` + 401；API 层失败按 New API 惯例应是 `{"code": false, "message": "..."}` + HTTP 200，但**未用真 key 验证**。New API adapter 需同时容忍这两种。
- **讯飞这个部署的 `quota_per_unit` 是否恒为 500000**：实测 `/api/status` 返回 500000（= New API 出厂默认），但该实例 `/api/status` 需带网关前缀才能访问，且前缀是部署相关的——**通用 New API adapter 不应假定能访问 `/api/status`**（标准路径在讯飞被 403）。因此 `quotaPerUnit` 的默认 500000 是"假设默认值"，配置可覆盖。
- **`model_limits` 启用后的语义**：实测 `model_limits_enabled: false`。启用后 `model_limits` 形如 `{"gpt-4": 1000000}`，但该接口返回的 `total_*` 仍是**令牌级总量**，不随 model 分池。若将来要显示"按模型的已用量"，需要另一个接口（如 `/api/log/stat`），届时额度池标识要把 model 加进去。
- **`is_available: false`（DeepSeek）的语义**：推测是"余额为 0 / 不可用"。当前映射为 `Remaining = 0` → 状态栏可按 `remaining <= 0` 染红。这个推断同样需要样本佐证。
