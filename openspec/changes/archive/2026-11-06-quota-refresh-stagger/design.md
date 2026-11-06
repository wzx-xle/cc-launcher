## Context

### 问题现场

2026-11-06：一台机器配了 8 个 iflytek provider（各自的 API key → 8 个额度
池）。`ccl serve` 起来后第一轮刷新打出 8 个请求（挤在 2 秒内）→ **429**。

现状实现（`internal/quota/refresh.go` 的 `RefreshAll`）：

```
serve ticker（周期 = 所有 provider 的最小 refreshInterval，缺省 60s）
  └─ 遍历所有 provider：
       ├─ age < 该 provider 自己的 refreshInterval → 跳过（各自生效 ✓）
       └─ 否则 → 加入待刷队列
  └─ 待刷队列按额度池去重 → 串行逐个 Fetch
```

**"各自生效"做到了周期，没做到时刻**：8 个 provider 都配 60s → 8 个都该每
60 秒刷一次 → 这 60 秒里必然有 8 个请求 → 它们同时到期 → 挤在 ticker 触发点
那一瞬间打完。

### 三个可选方向与否决理由

| 备选 | 否决理由 |
|---|---|
| **让用户给 8 个 provider 配不同的 refreshInterval** | 零代码，但数据新鲜度参差不齐（60s 的刷得勤、109s 的刷得稀），且用户得自己算——这不是"配置"，是"手工错开" |
| **把刷新周期拉长**（都配 300s） | 同样零代码，但额度数据最旧 5 分钟；用户要的就是"实时看到额度" |
| **把每个 provider 的实际周期拉长到 1-2 倍来错开** | 周期不再是配置的 `refreshInterval`（"配 60 实际 120"违反直觉），且 stale 阈值要跟着放宽 |
| **相位错开（采纳）** | 周期**精确**等于 `refreshInterval`，只是每个池在周期内占一个固定相位 → 8 个请求均匀分布在 60 秒里 |

## Decisions

### 决策 1：相位错开 = 每个池在自己的周期内占一个固定相位

**不变式**：

1. 每个额度池的刷新**周期长度** = 该 provider 的 `refreshInterval`（精确）
2. 每个额度池的刷新**时刻** = 某个固定相位 + k × 周期（相位分配一次后永久
   保持）
3. 同一周期内，不同池的相位**不重叠**

**持久化**：数据文件的 entry 增加 `next_at`（下次该刷的时刻，Unix 秒）。

```
判定到期： now >= next_at
刷新之后： next_at = old_next_at + interval          （递推 → 相位保持）
          若递推后 <= now（错过了，比如 serve 停了很久）：
              重复 +interval 直到 > now               （相位仍然保持）
```

**关键**：用 `next_at` **递推**而不是 `now + interval` 重算——后者会把每次刷新
的实际耗时累积进周期（刷新慢 → 周期越拖越长），且相位会漂。

**首次分配**：一批 provider 同时需要刷新且都还没有 `next_at` 时（首轮，或老
数据文件），按 `(interval, pool)` 分组，组内按 pool 名排序，第 k 个（k 从 0
到 n-1）拿

```
phase_k = round(k × interval / n)
```

8 个 provider 都配 60s → phase = 0, 8, 15, 23, 30, 38, 45, 53 秒
→ 每 7.5 秒一个，均匀铺满 60 秒 ✓

不同 interval 的 provider 各自成组，组内独立分配 ✓

**分配时机**：在 `RefreshAll` 里，判定到期之后、实际请求之前做一次分配（本轮
待刷的池按组算相位）。

**相位不重排**：provider 增减时老的不动，新的拿一个可能重复的相位 → 轮内间隔
兜底。理由：重排会让所有 provider 的刷新时刻突变（用户会看到一批数据同时
变旧）。

### 决策 2：轮内最小间隔（首轮的兜底）

首轮（还没有相位）时，8 个请求仍会挤在一起 → 相邻两个请求之间强制间隔。

- 缺省 **500ms**，可按 provider 配（`quota.minRequestGapMs`）——不同厂商的限流
  阈值不同，硬编码 500ms 对某些厂商不合适
- 间隔要**尊重 ctx 取消**（serve 停的时候不能还在 sleep）
- 间隔只加在"同一轮内的相邻请求"之间；跨轮不额外加（相位已经错开了）

### 决策 3：429 指数退避（持久化，跨进程）

**为什么必须持久化**：退避状态如果只记在 daemon 内存里，那么
`ccl quota --refresh`（另一个进程）不知道要退避 → 它一刷就打 → 限流持续。
而且 serve 重启后记忆丢失 → 又开始打。

**退避规则**：

```
命中 429：
    backoff = min(max(backoff × 2, 1min), 30min)   首次 = 1min
    retry_after = now + backoff
成功刷新一次：
    backoff = 0（复位，下次 429 从 1min 重新开始）
    retry_after = 0
```

**判定**：`now < retry_after` → 跳过这个 provider（不发请求）。

**退避期间**：条目**保留**（不删）→ `fetched_at` 变旧 → stale 自动变 true ✓
（用户看得出"数据不新鲜"，而不是"额度没了"——额度不会因为一次网络抖动就
变没）。

**数据文件字段**（都是可选，老文件零值 → 退化成旧行为）：

| 字段 | 含义 |
|---|---|
| `next_at` | 下次该刷的时刻（相位错开） |
| `retry_after` | 退避到什么时候才允许再查 |
| `backoff_seconds` | 当前退避时长（下次 429 时翻倍的基数） |

### 决策 4：adapter 必须把 429 识别出来

三家的失败协议已经各不相同（MiniMax 看 body、DeepSeek 看 HTTP 状态码、New API
看 body 的 `code`）——**429 是 HTTP 层的共性**，所以在"拿到 HTTP 状态码"的
地方统一识别：

```go
// RateLimitError 表示厂商限流（HTTP 429）。
type RateLimitError struct {
    Status     int           // HTTP 状态码（429）
    RetryAfter time.Duration // 厂商给的 Retry-After（没有则 0）
    Msg        string        // 响应体摘要
}
```

`fetchJSON` 在 status == 429 时返回这个类型 → `RefreshAll` 用 `errors.As`
识别 → 退避。

**Retry-After**：标准 HTTP 头，厂商给了就尊重（比指数退避更准）；没给就按指数
退避。

### 决策 5：`--refresh` 也遵守退避

`ccl quota --refresh` 打的是**同一个接口**——手动刷冲掉退避等于让限流持续。

但必须给出**可行动的提示**（不是干巴巴地失败）：

```
提供商 "iflytek-x" 正在退避中（厂商限流），还有 47 秒可以再试（试试 ccl quota --refresh）
```

### 决策 6：stale 阈值不变

相位错开后，刷新**按点**发生 → 数据年龄始终 < `refreshInterval` → stale 判定
（age > refreshInterval）语义不变 ✓

不需要放宽阈值——用 `next_at` 递推正是为了保证这一点。

## Risks / Trade-offs

- **相位分配依赖同批 provider 数量** → provider 增减时相位不重排（决策 1 已
  说明理由）
- **退避状态写进数据文件** → 老版本 ccl 不认识 `next_at`/`retry_after` 字段会
  忽略（json 忽略未知字段）→ 退回"每次都打"的旧行为，无害
- **轮内间隔让一轮变慢**（8 个 × 500ms = 4 秒）→ serve 的刷新在独立 goroutine
  里，不影响 metrics 服务与 graceful shutdown
- **退避最长 30 分钟** → 退避中的 provider 额度数据会变 stale（最旧 30 分钟）
  → 状态栏标 stale，用户看得见
