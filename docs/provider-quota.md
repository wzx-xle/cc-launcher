# provider 额度查询（`ccl quota`）

> 状态：v0.20.0 起可用。本文档描述的是**对外契约**——`ccl quota --json` 的输出
> 形状是稳定的，仓库外的消费方（如 Claude Code 状态栏脚本）可以依赖它。

## 为什么需要

Claude Code 的状态栏有两个额度槽位（`5h` / `7d`），数据来自 Claude Code 自己
维护的 `rate_limits`——那是 **Anthropic 官方**的限流。走第三方模型时它拿不到
数据，槽位就一直是 `--`：

```
ctx: ░░░░░░░░░░ -- | 5h: ░░░░░░░░░░ -- | 7d: ░░░░░░░░░░ --
```

用户看不到自己的配额还剩多少，只能等 429 报错才发现。而各家"额度"的形态
完全不同：

| provider | 额度形态 | 百分比可算？ | 会重置？ |
|---|---|---|---|
| Anthropic 官方 | 5h + 7d 窗口 | ✅ | ✅ |
| MiniMax 套餐 | 5h + 7d 窗口 | ✅ | ✅ |
| DeepSeek | 预付费余额（$） | ❌ **没有分母** | ❌ |
| 讯飞（New API 私有部署） | 令牌总额度（额度点） | ✅（分母是用户自设上限） | ❌ |

**固定成"5 小时 + 7 天"两个槽位表达不了这个多样性**——只有月额度的厂商塞不
进 `five_hour`，标签写 `5h` 实际是 30 天更是说谎。所以额度输出是一个**维度
列表**，不是一个固定形状。

## 用法

```bash
# 看当前 provider 的额度（人看的表格）
ccl quota

# 指定 provider + JSON 输出（pipeable to jq）
ccl quota --provider minimax-m3 --json

# 手动联网刷一次（不需要 serve 在跑）
ccl quota --refresh --json
```

**默认只读本地数据文件，一次网络都不发。** 数据由 `ccl serve` 周期刷新
（默认 60 秒）写入 `~/.cc-launcher/quota.json`。

### provider 怎么定

三级回落：

1. 显式 `--provider <名字>`
2. 环境变量 `CCL_PROVIDER`（ccl 启动 Claude Code 时注入；状态栏子进程继承得到）
3. `state.json` 的 active provider（上次在 TUI 里选的）

所以状态栏脚本只要 `execSync('ccl quota --provider "$CCL_PROVIDER" --json')`
就行——不传 `--provider` 也能靠环境变量兜住。

### 退出码

| 码 | 含义 | 消费方该怎么做 |
|---|---|---|
| `0` | 读到额度 | 用 `dimensions` |
| `1` | **读不到数据**：serve 没跑 / 首次刷新还没完成 / 该 provider 尚无条目 | 回落 Claude Code 的 `rate_limits` |
| `2` | 该 provider **没有额度接口**（正常状态，不是错误）——**纯离线可判** | 回落 Claude Code 的 `rate_limits` |

分级的意义：消费方能区分"这厂商没额度"（2）和"数据还没准备好"（1）。
**失败时不打印额度文档**（stdout 只有成功时才有内容）。

## 输出契约

```json
{
  "provider": "minimax-m3",
  "source": "minimax",
  "stale": false,
  "fetched_at": 1793943914,
  "dimensions": [
    {
      "id": "interval",
      "label": "5h",
      "kind": "window",
      "duration_seconds": 18000,
      "used_percentage": 4,
      "used": 4,
      "limit": 100,
      "unit": "%",
      "resets_at": 1793961914
    },
    {
      "id": "weekly",
      "label": "7d",
      "kind": "window",
      "duration_seconds": 604800,
      "used_percentage": 33,
      "used": 33,
      "limit": 100,
      "unit": "%",
      "resets_at": 1794323556
    }
  ]
}
```

### 顶层字段

| 字段 | 说明 |
|---|---|
| `provider` | provider 名 |
| `source` | 产出这份数据的 adapter 名（`minimax` / `deepseek` / `newapi`） |
| `stale` | 数据是否已过期（见下） |
| `fetched_at` | 抓取时间（Unix 秒）——消费方不必读数据文件就知道数据多旧 |
| `dimensions` | 额度维度列表（**有几个显示几个**） |

### 维度字段

| 字段 | 说明 |
|---|---|
| `id` | 稳定标识（`interval` / `weekly` / `balance` / `quota`） |
| `label` | 展示名，**纯 ASCII**（见下） |
| `kind` | `window` / `quota` / `balance`（见下） |
| `duration_seconds` | 窗口时长（秒）；**没有的字段不写**（不写 null） |
| `used_percentage` | **已用**百分比（ccl 派生，消费方不用自己算）；算不出时缺席 |
| `used` / `limit` / `remaining` | 绝对量；`limit` 缺席 = 不知道分母 |
| `unit` | 单位（`%` / `USD` / `CNY` / `tokens`），**纯 ASCII** |
| `resets_at` / `expires_at` | Unix 秒 |
| `unlimited` | 无限额度（此时无 `used_percentage`） |
| `raw` | 厂商原始字段（排查用） |

### 三种 kind

| kind | 语义 | 典型 | 状态栏画法 |
|---|---|---|---|
| `window` | 有周期 + 有分母 | MiniMax 5h / 7d | 条形图 + 百分比 + 重置时间 |
| `quota` | 无周期 + 有分母 | New API 令牌额度 | 条形图 + 绝对值 |
| `balance` | **无分母** | DeepSeek 预付费余额 | **只显示数字**（画不出条形图，且"已用"与"剩余"方向相反） |

### label 与 unit 的规则

- **`label` 与 `unit` 永远是纯 ASCII**——它们要渲染到终端的一行文本里，
  中文字形缺失时无法读取。
- **`label` 永远不取自厂商响应的原始字符串**（厂商的字段可能是中文、可能含
  控制字符）：
  - 窗口型 → 从 `duration_seconds` 派生：`18000`→`5h`、`604800`→`7d`、
    `2592000`→`30d`
  - 非窗口型 → **`unit` 的值**（`CNY` / `USD` / `tokens`）
- **ccl 给的 `label` 优先**，但消费方仍应自己过一遍 `sanitizeLabel`（防御性）。

### stale 语义

`stale` 由 ccl 判定：条目的 `fetched_at` 距今超过该 provider 的
`refreshInterval`（**兜底 60 秒**）→ `stale: true`。

**stale 是连续的状态**，不是"这次请求失败"：serve 刷新失败时**保留旧条目**
（`fetched_at` 不变 → 越来越旧 → stale 自动变 true）。所以：

- 额度不会因为一次网络抖动就变没——状态栏从数字跳回 `--` 是很糟的体验
  （用户会以为额度用光了）
- 用户看得出"数据不新鲜"，而不是"额度没了"

## 数据文件

`~/.cc-launcher/quota.json`，权限 **0600**，**原子写**（临时文件 + rename）→
并发读不会读到半个文件。

```jsonc
{
  "version": 1,
  "updated_at": 1793943914,
  "entries": {
    "minimax-m3": {
      "source": "minimax",
      "pool": "3f2a9c…",        // sha256(端点 + "\0" + API key)，**不含明文 key**
      "fetched_at": 1793943914,
      "dimensions": [ … ]
    }
  }
}
```

**按 provider 名索引**（读取侧要按名字直接找）；**冗余存储维度**（同一额度池
被多个 provider 引用时内容重复——冗余换来读取侧零查找成本，且每个条目自带
`fetched_at`）。

**额度池按 `(端点, key)` 去重**：额度是**按 key 分**的，不是按 provider 名分
的。现有 3 个 minimax provider 里 `minimax-m3` 与 `minimax-m3.1-flash` 共用
同一把 key → 同一个额度池 → **一轮刷新只发一次网络请求**，结果写进所有引用它
的条目。

**写入是「读-改-写」**，不是全量重建：serve 与 `ccl quota --refresh` 会并发
写同一个文件，读-改-写让冲突只剩几毫秒，且冲突时写的是同一个额度池的数据
（内容等价）→ **不需要文件锁**。最坏后果是某个条目短暂缺失一个刷新周期，
serve 下轮自愈。

## provider 配置

### `quota` 块（可选）

`quota` 是 **ccl 自己的元数据键**（与 `note` / `order` 同等），**不交付**给
Claude Code。

```jsonc
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://one.iflytek.com/api/llm/console/chat/",
    "ANTHROPIC_AUTH_TOKEN": "sk-xxx"
  },
  "quota": {
    "source": "newapi",          // 选内置 adapter
    "endpoint": "https://…",     // 可选：覆盖自动推导
    "quotaPerUnit": 500000,      // 可选：额度点换算比例
    "displayUnit": "USD",        // 可选：显示单位
    "refreshInterval": 60        // 可选：刷新周期（秒），缺省 60
  }
}
```

| 字段 | 必填 | 含义 |
|---|---|---|
| `source` | ❌ | 选内置 adapter（`minimax` / `deepseek` / `newapi`）。**不配就按 base_url 的 host 自动识别**（认得出 MiniMax / DeepSeek） |
| `endpoint` | ❌ | 覆盖自动推导（私有网关、二开路径） |
| `quotaPerUnit` | ❌ | 额度点换算比例（默认 500000 = New API 出厂默认） |
| `displayUnit` | ❌ | 显示单位（默认由 adapter 决定） |
| `refreshInterval` | ❌ | 刷新周期，**秒**（缺省 60） |

> ⚠️ **`quota.endpoint` 会把你的 API key 发给那个地址**。它是你自己的选择
> （provider 配置本来就带端点与凭证），但配置前想清楚：这个地址是你信任的吗？

### 支持的 adapter

| source | 识别方式 | 额度端点 | 判据 |
|---|---|---|---|
| `minimax` | host 剥掉 `api.`/`www.` 后**恰好等于** `minimax.cn` / `minimax.io` | `https://www.<host>/v1/token_plan/remains` | body 的 `base_resp.status_code == 0`（**错误也返回 HTTP 200**） |
| `deepseek` | host 剥掉 `api.`/`www.` 后**恰好等于** `deepseek.com` | `https://<host>/user/balance` | HTTP 状态码（401 = key 不对） |
| `newapi` | **只能显式配 `source`**（任何 host 都可能是它） | `<base_url>/api/usage/token/` | body 的 `code == true` |

**识别规则为什么这么严**：用"包含 minimax"会把 `minimax-proxy.corp.example`
这类自建域名误判成 MiniMax，推出一个不存在的端点——**识别错比识别不到更
糟**（识别错 = 拿着你的 API key 去打一个不存在的地址）。

**端点推导是 per-adapter 的**，不是全局规则：三家的规律完全不同（MiniMax 换
子域、DeepSeek 同主机换路径、New API 同前缀加后缀）。任何全局规则都会推出
错端点。

### 代理

额度查询走 **provider 自己的网络路径**：`HTTP_PROXY` / `HTTPS_PROXY` /
`NO_PROXY` 从**该 provider 的 `env` 块**取（不是 ccl 进程的环境——provider
的 env 是经 `--settings` 交付给 Claude Code 的，daemon 从 ccl fork 继承的是
用户 shell 的环境）。

**一个"必须走代理才能访问"的 provider，Claude Code 能用但额度查询会超时**
→ 所以额度查询必须复用 provider 的代理配置。

`NO_PROXY` 按 host 后缀匹配（`.example.com` 排除 `api.example.com`），
`no_proxy` 小写形式也认。

### 明文 base_url

provider 的 `ANTHROPIC_BASE_URL` 是 `http://` 时，ccl 会给一条**非阻断**警告
（key 会明文传输）。**ccl 不自动改写你的 base_url**——那是静默改你的配置
语义，ccl 只提示不替你做主。

## 刷新由谁负责

**`ccl serve`**（常驻，ccl 自动拉起，永不自动 idle 退出）：

- **启动即刷一轮**（不等一个周期）→ 跑完一次 `ccl` 就能读到数据
- ticker 周期 = 所有待刷 provider 的 `refreshInterval` 的**最小值**；每轮遍历
  provider，**距上次刷新不足其自身周期的跳过这一轮**
- **刷新失败只进 serve 的日志，不写数据文件** → 旧条目保留 → stale 自动生效
- 独立 goroutine + ctx 取消，不干扰 metrics 服务与 graceful shutdown

**`ccl quota --refresh`** 是 serve 被 `ccl serve --stop` 关掉时的**逃生舱**：
联网查一次 → 写回数据文件 → 照常输出（**不需要 serve 在跑**）。

它与 serve **天然互斥**：serve 的周期判定从**数据文件里的 `fetched_at`** 读
（不是内存 map），所以 `--refresh` 刚写过的 provider，serve 下一轮会自动跳过。

## 状态栏怎么用

状态栏脚本**只调命令，不直接读数据文件**（文件位置与格式是 ccl 的内部细节）。

**两条分支不合并**：

| 来源 | 缺一个维度 | 含义 | 该怎么办 |
|---|---|---|---|
| ccl quota 的 `dimensions` | 厂商就没这个限制 | 确定没有 | **不显示**（不占槽位） |
| Claude Code 的 `rate_limits` | 官方没填这个槽位 | 可能是"没这个限制"，也可能是"这次没拿到" | **保留 `--`**（保持今天行为，不猜） |

合并会把这两个语义抹平。分支 B 保留 `--` → **Anthropic 官方 provider 的输出
一个字节都不变**。

```js
// 分支 A：ccl quota 退出码 0 → 完全用 quota 的维度清单
// 分支 B：ccl quota 退出码非 0 → 保持今天 Claude Code 的 rate_limits 两个固定槽位
```

**超时保护**：`execSync` 的 timeout 设 **250ms**（Claude Code 状态栏有 300ms
去抖窗口且每次执行前 abort 上一次——超时 10 分钟不是瓶颈，300ms 窗口才是）。
正常路径 ~15ms。任何失败**静默回落**，状态栏不能因为额度查询卡住或崩掉。

**渲染约定**：**label 已经是 unit 时不要重复显示单位**（非窗口型维度的 label
就是 unit）——`"CNY: 5.17 CNY"` 应渲染成 `"CNY: 5.17"`。

**颜色阈值**：额度条沿用 Claude Code `rate_limits` 的既有阈值（≥90 红闪 /
≥75 红 / ≥50 黄 / 其余绿），与上下文条（`ctx`，≥60 转黄）**各用各的**——
上下文接近上限才是真问题，额度是消耗类指标宁可早一点预警。

**调用极其频繁是常态**：状态栏命令在生成过程中约每 300ms 被调一次（每秒 3.3
次）→ 设计必须假设这一点。由于命令纯本地读，耗时恒定。

## 外部行为实测结论

额度接口是**外部行为**——厂商改了字段或端点，ccl 就读不出来。

**2026-11-06 实测基线**（改动实现前先看这里）：

| 厂商 | 端点 | 成败判据 | 错误体形状 |
|---|---|---|---|
| MiniMax | `www.<host>/v1/token_plan/remains` | `base_resp.status_code == 0` | **HTTP 200 也可能是错误**（`1004` 登录失败 / `2049` key 无效） |
| DeepSeek | `<host>/user/balance` | HTTP 状态码 | **可能是纯文本**（无 Authorization 头时 `Authentication Fails (governor)`） |
| New API 系 | `<base_url>/api/usage/token/` | body 的 `code == true` | 认证层 `{"error":{…,"type":"new_api_error"}}` + 401；API 层 `{"code":false,"message":…}` |

各家的响应形状（实测样本见 design.md）：

- **MiniMax**：`model_remains[]` 里取 `model_name == "general"` 的条目；窗口
  时长从 `end_time - start_time` **算**（实测恒为 18000000ms = 5h，周窗口
  604800000ms = 7d）——**不许写死 "5h"/"7d"**；剩余百分比要转成已用百分比
  （`100 - remaining`，clamp 到 [0,100]）；`remains_time` 是**距重置的毫秒数**
- **DeepSeek**：`balance_infos[0].total_balance` 是**剩余**金额（不是已用），
  **字符串**要转 float；**没有总额度** → 算不出百分比 → kind = `balance`
- **New API**：`total_granted` 是**建令牌时设的上限**（分母），`total_used` 是
  已用，`total_available` 是剩余；三者**代数自洽**（granted - used =
  available）；单位换算除以 `quotaPerUnit`（默认 500000）

### 厂商改了字段怎么办

**降级而不是崩**：

1. 解析失败发生在 **serve 侧** → 旧条目保留（`fetched_at` 变旧 → 自动
   `stale: true`）+ 错误进 serve 日志
2. 读取侧完全不受影响——`ccl quota` 仍返回旧数据（标 stale）
3. **绝不允许**额度查询失败影响 Claude Code 启动（额度是锦上添花，provider
   能不能生效才是生死线）
4. 状态栏拿不到数据就保持 `--`，不崩

要修的话：改 `internal/quota/adapter_<厂商>.go` 的解析，重新验证，并把新的实测
结论更新到本节。**升级 Claude Code 后也要重验一遍**（Claude Code 升级不影响
额度接口，但会影响 `--settings` 的交付语义）。

### 已知偏差

- **`quotaPerUnit` 默认 500000 是"假设默认值"**：实测某实例的 `quota_per_unit`
  恰好等于 New API 出厂默认值，但通用 adapter **不应假定能访问 `/api/status`**
  （该路径在网关部署下被 403）→ 已知偏差时显式配置 `quotaPerUnit`
- **`model_limits` 启用后的语义未实测**：实测 `model_limits_enabled: false`；
  启用后 `total_*` 仍是**令牌级总量**，不随 model 分池
- **`current_interval_status` / `current_weekly_status` 的取值语义未知**
  （只见 `1`）：当前**不解释**，原值进 `dimension.raw`
- **New API 的 API 层失败体形状未实测**：需同时容忍 `code:false` 与
  `error` 对象两种
