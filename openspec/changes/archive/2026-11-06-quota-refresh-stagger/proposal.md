## Why

额度刷新会撞厂商的限流。

**实测场景（2026-11-06）**：一台机器上配了 **8 个 iflytek provider**（各自不同的
API key → 8 个额度池），`ccl serve` 起来后第一轮刷新就在几秒内打出 8 个请求 →
**厂商返回 429**。

现状的问题不是"请求太多"（8 个 provider 各配 60s → 每分钟 8 个请求本身不算
多），而是**这 8 个请求挤在一起打**：

```
现在:  |████ 8 个请求挤在 2 秒内 ████|          |████ 8 个请求 ████|
相位后:  |·A·|···B···|····C····|·····D····|····E····|··F··|····G····|
```

**根因**：`refreshInterval` 是 per-provider 的、"各自生效"在**到期判定**上已经
做到了（`internal/quota/refresh.go` 逐个 provider 用自己的 interval 判定），
但**触发时刻仍聚集在 ticker 的触发点**——8 个 provider 都配 60s 时，它们必然
在同一个 tick 上同时到期。

**429 的后果比"少显示额度"严重**：刷新失败只进日志、旧条目保留 → `fetched_at`
越来越旧 → 8 个 provider 的额度**全部变 stale**，额度功能实际瘫掉；而 serve
下一轮照打 → 持续 429，永远起不来。

## What Changes

- **相位错开（phase stagger）**：每个额度池在自己的刷新周期内占一个**固定
  相位**，相位由"同周期的一批 provider"均分（8 个 → 周期内每 7.5 秒一个）。
  **刷新周期仍是精确的 `refreshInterval`**（不是 1-2 倍），只是触发时刻错开。
  相位分配一次后**永久保持**（持久化在数据文件里）。
- **轮内最小间隔**：一轮里相邻两个请求之间至少隔一小段（缺省 500ms，可按
  provider 配）——首轮（还没有相位）时兜底。
- **429 指数退避（持久化）**：命中 429 → 自动把该 provider 的刷新间隔拉长
  （1min → 2min → 4min … 封顶 30min），**写进数据文件** → serve 与
  `ccl quota --refresh` 两个写者都遵守、跨 serve 重启也生效；连续成功一次后
  恢复。退避期间条目保留 → stale 自动生效（用户看得出"数据不新鲜"而不是
  "额度没了"）。
- **`ccl quota --refresh` 也遵守退避**（手动刷不能冲掉退避——它打的是同一个
  接口），但会明确提示还剩多少秒可以再试。

## Capabilities

### Modified Capabilities

- `provider-quota`: 刷新不再"同一时刻打一批"——新增相位错开、轮内间隔、
  429 退避三条要求；数据文件新增 `next_at` / `retry_after` 字段。

## Impact

- **改动** `internal/quota/refresh.go`：相位分配、`next_at` 递推、轮内间隔、
  退避判定与写回
- **改动** `internal/quota/store.go`：`Entry` 增加 `next_at` / `retry_after` /
  `backoff_seconds`（可选字段，老文件零值 → 退化成旧行为）
- **改动** `internal/quota/adapter*.go`：HTTP 429 → `*RateLimitError`（带
  `Retry-After`）
- **改动** `internal/quota/model.go`：`Config` 增加 `MinRequestGap`（毫秒）
- **改动** `cmd/cc-launcher/quotacmd.go`：`--refresh` 遵守退避 + 提示剩余秒数
- **改动** `docs/provider-quota.md`：补"刷新错开与退避"一节
- 风险：相位分配依赖"同周期的一批 provider 数量"——provider 增减时相位不重排
  （老的不动，新的拿一个可能重复的相位 → 轮内间隔兜底）；退避状态写进数据
  文件 → 老版本 ccl 不认识这些字段会忽略（无害）
