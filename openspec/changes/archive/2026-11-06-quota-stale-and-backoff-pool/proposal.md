## Why

v0.20.1 上线后实测发现两个缺陷，都会直接影响用户可见的行为。

**缺陷 1：stale 在稳态下误报（每分钟闪一次）**

stale 的判据是「数据年龄 > 该 provider 的 refreshInterval」（缺省 60 秒），而
`fetched_at` 只存到秒（向下取整）。数据年龄的峰值真实值 ≈ 周期 − 刷新耗时，
**取整误差最多 +1 秒** → 阈值正好等于周期时，每次刷新前约 1 秒会越过阈值。

实测（2026-11-06，本机 6 个 provider，42 秒亚秒精度采样）：

```
provider            max_age   >60s?
  deepseek-flash      60.81s   是 (5次)
  iflytek-jlcao3      61.48s   是 (8次)
  minimax-m3-stliu14  60.75s   是 (4次)
```

用户看到的是状态栏上的 `(stale)` 偶尔闪一下——而数据其实是新鲜的。

**缺陷 2：退避按 provider 名记录，不按额度池**

额度是**按 key 分**的：限流是厂商针对**那把 key** 的。但退避状态记在 provider
名对应的条目上 → 退避期间**换了 key**（= 换了额度池）时，新 key 也会被老退避
跳过，直到退避结束才恢复。

## What Changes

- **stale 判定给 1 秒容忍**：`fetched_at` 只有秒精度，取整误差最多 +1 秒 →
  阈值 = refreshInterval + 1 秒，正好覆盖误差，稳态下不再误报
- **退避状态改按额度池记录**：数据文件新增顶层 `backoffs` 段（key = pool 哈希
  = `sha256(端点 + "\0" + API key)`）→ 换 key = 换池 = 不受老退避影响
- **老数据文件的退避状态惰性迁移**：v0.20.1 写在条目里的 `retry_after` /
  `backoff_seconds` 读出时并入对应的 pool（不丢退避）

## Capabilities

### Modified Capabilities

- `provider-quota`: stale 的判定（增加取整误差容忍）+ 退避状态的记录粒度
  （按额度池而不是 provider 名）。

## Impact

- **改动** `internal/quota/store.go`：`File` 增加 `Backoffs` 段；`Entry` 去掉
  两个退避字段；读出时做惰性迁移；新增 `SetBackoff`
- **改动** `internal/quota/refresh.go`：退避的判定与写回改按 pool
- **改动** `cmd/cc-launcher/quotacmd.go`：stale 判定加 1 秒容忍；`--refresh`
  的退避检查改查 pool
- **改动** `openspec/specs/provider-quota/spec.md`：stale 与退避两条要求的措辞
- 兼容性：数据文件新增可选字段 `backoffs`；老版本 ccl 不认识会忽略（json 忽略
  未知字段）→ 退避状态失效（无害：会被重新限流并重新记录）
