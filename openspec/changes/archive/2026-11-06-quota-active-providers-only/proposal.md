## Why

**额度数据的唯一消费者是状态栏，而没人看的额度不值得花一次网络请求去查。**

一台机器上配了 8 个 iflytek provider（各自的 key → 8 个额度池）时，用户每次只用
其中一个 —— 其余 7 个的额度数据**当前根本没人看**。但 serve 是**全量刷新**的：
每一轮都把 8 个 provider 查一遍。

结果就是：**为了没人看的 7 份数据，把厂商的额度查询接口打到 429**（实测，Windows
机器上）。

v0.20.1 加的相位错开 + 429 退避能缓解症状（请求散开、限流后自动退避），但**没
解决根因**：不该查的 provider 本来就不该查。

## What Changes

- **serve 只刷新"最近有活动"的 provider**，活动信号取自 **OTel**：
  - serve 本来就在收 Claude Code 推来的 OTel metrics，而 ccl 启动时把 provider 名
    注入了 `OTEL_RESOURCE_ATTRIBUTES` 的 `ccl.provider` → **collector 解析每条
    metrics 时就知道是哪个 provider**
  - collector 记录各 provider 最近的 OTel 活动时刻
  - serve 刷新额度时，**只查最近 5 分钟内有 OTel 活动的 provider**（5 分钟 = 4 个
    OTel 周期；实测 Claude Code 每 60 秒推一次 metrics）
  - 其余 provider **一次请求都不发**（不是"退避"，是"根本不查"）
- **额度命令不受影响**：`ccl quota --provider X --refresh` 是用户显式点名，
  无论 X 最近有没有活动都照查（`--refresh` 是逃生舱）

## Capabilities

### Modified Capabilities

- `provider-quota`: 刷新的对象从"所有声明了额度接口的 provider"改成"最近有活动
  的 provider"
- `daemon-lifecycle`: 同样改（`ccl serve` 不再全量刷新）

## Impact

- **新增** `internal/collector/activity.go`：provider 活跃度跟踪（内存 map + mutex）
- **改动** `internal/collector/server.go`：收到 metrics 时标记 provider 活跃；
  暴露 `Server.Activity()`
- **改动** `cmd/cc-launcher/serve.go`：刷新前按活跃度过滤 provider 列表
- **改动** `docs/provider-quota.md`：说明"只刷新最近有活动的 provider"
- 风险：
  - **会话刚启动的前 60 秒**状态栏可能还没额度数据（要等第一条 OTel + 一个
    ticker 周期）→ 回落到 Claude Code 的 `rate_limits`（显示 `--`），**不影响
    Claude Code 启动**
  - **用户离开超过 5 分钟** → 该 provider 停止被刷新 → 额度数据变旧 → 状态栏标
    stale（**可见**，不是静默失效）。这正是想要的："没人用就别查"
  - **serve 重启后活跃度清空** → 要等下一次 OTel（≤60 秒）才恢复 → 期间不查任何
    provider（保守，无害）
- 兼容性：不改任何数据文件格式；不改 `state.json`；`ccl quota` 输出契约不变
