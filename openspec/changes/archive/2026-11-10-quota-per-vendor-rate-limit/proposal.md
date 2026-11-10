## Why

Windows 主环境的 `serve.log` 出现持续 429，且**只发生在讯飞的两个 provider**（`iflytek-jlcao3` / `iflytek-xjxiang`），其余 5 个池 3 小时零 429。日志给出的三个事实：

1. **两个讯飞 provider 用不同的 key，却被同一个厂商同时限流** —— 说明厂商限的是**账号/出口 IP**，不是 API key。而现在的退避**只按额度池记**（池 = endpoint + key 哈希）→ 一个池退避时，同厂商的另一个池照打照限。
2. **轮内最小间隔只有 500ms**（`DefaultMinRequestGap`）→ 同一轮多个池到期时是串行连打（`查了 3` 那一轮 = 1.5 秒内 3 个请求）。相位错开只保证"不同池不在同一时刻"，**不保证"同一厂商的多个池不撞在短时间内"** —— 两个讯飞池的 429 时刻经常只隔 8-20 秒。
3. **退避"成功一次就清零"**（`ClearBackoff`）→ 退避永远爬不到长退避：日志里 13:59 已经退避 4m，14:24 又从 1m 重新开始（中间它成功刷了一次 → 退避被清零 → 再撞 429）。

三条叠加 = 讯飞侧持续看到突发请求 → 反复 429 → 用户状态栏的额度一直显示旧值。

## What Changes

- **按厂商（endpoint）的跨轮最小请求间隔**：同一 endpoint 的两个额度池，无论是否在同一轮、相位相隔多远，相邻请求至少隔 N 秒（默认 **10 秒**，可按 provider 用 `quota.vendor_gap` 覆盖）。节流状态由 daemon **跨轮持有**（内存），因此跨轮的相邻请求同样受约束。
- **退避改按厂商记**：429 记在 endpoint 上，同厂商的所有额度池一起暂停。池级退避仍保留（同 key 的池共用一把 key，按 key 限流时也该一起停）。
- **换 key 不被旧退避挡住**：退避记录里带一份 key 指纹；provider 换 key 后，它自己不受旧退避约束（厂商级暂停由别的池承担），但同厂商其它池仍按厂商退避暂停。
- **退避不再因一次成功就完全清零**：同厂商在退避期内成功过 → 退避时长回落一级（而不是直接归零），让退避能真正爬到封顶。

非破坏性：命令行接口、配置格式（新增一个可选键）、数据文件格式（退避记录的键变了）都不影响既有用法；旧的按池退避记录读不出来即视为"无退避"。

## Capabilities

### New Capabilities

（无 —— 不引入新能力，只修改既有能力的对外行为。）

### Modified Capabilities

- `provider-quota`: "ccl refreshes the quota periodically in the daemon" —— ① 退避的粒度从"额度池"扩展到"厂商 endpoint"（并保留池级）；② 新增"同一厂商的相邻请求必须间隔至少 `vendor_gap`"；③ 退避不再因一次成功就完全清零。

## Impact

- **改动**：`internal/quota/refresh.go`（厂商节流器、退避键、到期与退避判定）、`internal/quota/store.go`（退避记录的读写与 key 指纹）、`cmd/cc-launcher/serve.go`（daemon 持有节流器并传给 `RefreshAll`）、`internal/quota/refresh.go` 的 `Config`（新增 `VendorGap`）。
- **数据**：`quota.json` 的 `backoffs` 键从"池哈希"变为"厂商哈希 + 池哈希 + key 指纹"；旧记录读不出即忽略（等价于清零）。
- **无依赖变更**，无 schema 变更。
