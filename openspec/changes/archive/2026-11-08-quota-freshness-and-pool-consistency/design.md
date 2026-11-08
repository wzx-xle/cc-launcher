## Context

### 现状

额度刷新跑在 `ccl serve`（常驻 daemon）里，链路是：

```
Claude Code ──OTel metrics(:4318)──> collector ──> Activity(内存 map)
                                                     │
                                          quotaRefreshLoop 每 15s 一轮
                                                     │
                                          RefreshAll(active=最近活跃的 provider)
                                                     │
                                          quota.json ──> ccl quota --json ──> 状态栏
```

额度数据的唯一消费者是状态栏（约每 300ms 调一次 `ccl quota`，只读本地文件）。
请求按**额度池**发（`PoolID = sha256(endpoint + key)`），同 key 的多个 provider
共用一个池。

### 实测确认的三个问题

1. **同池不同源**：`internal/quota/refresh.go:572` 的活跃过滤在 provider 级，
   不活跃的 provider 在赋 `poolOf` 之前 `continue` → 写回时按 `poolOf` 匹配不上
   → 同池的不活跃 provider 永远拿不到新数据。实测 `minimax-m3`(8 秒前) 与
   `minimax-m3.1-flash`(38 分钟前) 同池却是两个时刻。

2. **daemon 重启后 60 秒全盲**：`Activity` 是纯内存空 map，"活跃"的唯一来源是
   Claude Code 的 OTel（实测每 60 秒推一次）。实测 serve.log 连续 4 轮
   "查了 0 / 不活跃跳过 6"，第 5 轮才恢复。

3. **切换到不活跃的 provider 时看到几十分钟前的旧数字**。

### 约束

- 额度刷新**只能**由 `ccl serve` 发起（`ccl quota` 读取侧一次网络都不能发）
- 额度接口是外部行为，厂商改字段/端点必须降级而不是崩
- 429 必须退避（指数增长，封顶 30 分钟，持久化，按池记录）
- 同刷新周期的池必须**相位错开**（厂商侧会限流）
- `quota.json` / `state.json` 格式不变；`ccl quota` 输出契约不变

## Goals / Non-Goals

**Goals:**

- 同一额度池的所有 provider **永远显示同一份数据**（结构上保证，不靠刷新路径碰巧写对）
- 用户跑完 `ccl` 后，**本次使用的 provider 的额度立刻是新的**（不等 60 秒 OTel
  周期，不等 15 秒 ticker）
- 用户切到任何 provider，**看到的数字最多旧 10 分钟**
- 请求量增幅可忽略（4 池规模：≈24 请求/小时）

**Non-Goals:**

- 不改额度请求的协议、端点、解析（`docs/provider-quota.md` 的外部行为不动）
- 不改 `quota.json` / `state.json` 的文件格式，不做数据迁移
- 不给 `:4318` 加鉴权（OTel 端点本来就没有，保持一致）
- 不做"所有 provider 全量刷新"（那是 v0.20.1 之前的做法，429 的根因）
- 不改 `ccl quota` 的输出契约、不改 `CCL_PROVIDER` 契约

## Decisions

### D1：活跃过滤提升到**额度池级**，且参数语义不变

`RefreshAll(ctx, refs, st, logf, active)` 的 `active` **仍然收 provider 名集合**，
池的换算在 `internal/quota` 包**内部**完成。

- **为什么不把 active 改成 pool id 集合**：池是 quota 包的内部分类（`PoolID`），
  让 cmd 层去算等于把内部概念外泄；而 `RefreshAll` 本来就在内部算 pool，多算
  一次是零成本。保持参数语义不变 → `serve.go` 的 `activeProviders()` 一个字都不用
  改，兼容面最小。
- **实现**：把 provider 循环拆成两段 —— 先对**所有** ref 做 `Resolve` + 算 pool
  （不管活跃与否）并建 `providerPool` 映射，再由「池内任一 provider 在 active
  名单里」推出「活跃池集合」。`live` 集合（Prune 的依据）仍然是**全部** provider
  ——"这轮没刷它" ≠ "provider 被删了"。

### D2：不活跃池的到期判定必须用**保鲜阈值**，不能沿用 refreshInterval

这是本设计**最容易踩的坑**。

当前到期判定是 `dueAt = entry.NextAt`（或 `fetched_at + interval`）。`next_at` 是
按 interval（默认 60 秒）递推的 → 若不活跃池仍按它判到期，**每 60 秒就会刷一次**
→ 保鲜规则完全失效，退化成"全刷"（429 的根因）。

所以判定分三档：

| 池状态 | 到期判定 | 效果 |
|---|---|---|
| 正在退避 | 跳过 | 限流保护优先于一切 |
| **活跃** | `min(next_at, fetched_at + interval)` | 正常 60 秒节奏；数据旧时立即刷 |
| **不活跃** | `fetched_at + 保鲜阈值`（**不看 next_at**） | 每 10 分钟最多刷一次 |

「活跃」用 `min` 而不是单取 `next_at`：用户切到一个 38 分钟没刷过的 provider 时，
`fetched_at + interval` 早已过期 → `min` 取到过期值 → **立即刷**，不必等
`next_at`。而数据是 3 秒前刚刷的时，`min(next_at, +60s)` 取 `next_at` → 不重复刷。

这个 `min` 也让「激活即刷」**不需要额外的 forced 机制**：激活一个陈旧的池，
它的到期判定自然已经过期。

「不活跃」档**不能**写成 `max(next_at, fetched_at + 保鲜阈值)`：`next_at` 是按
`refreshInterval` 递推的，用户若把它配得比保鲜阈值还长（如 900 秒 > 600 秒），
`max` 会让实际保鲜期变成 15 分钟 → 违反「数据最多旧 10 分钟」的承诺。
`next_at` 服务的是活跃时的相位错开，对不活跃池没有意义。

### D3：保鲜阈值 = 10 分钟，做成包级常量

`quota.DefaultFreshnessThreshold = 10 * time.Minute`，`RefreshAll` 新增参数
`freshness time.Duration`（传 0 = 关闭保鲜）。

- **为什么不进 provider 的 `quota` 配置块**：保鲜是**全局策略**（用请求量换数据
  新鲜度），不是某个 provider 的属性；放进配置块会让人误以为能单独调。
- **为什么不用 `max(10min, interval × N)`**：固定值可预测；用户配 300 秒周期的
  provider 也只保 10 分钟新鲜度，够用。

### D4：激活通道用 daemon 的 HTTP 端点（`POST /ccl/activate`）

daemon 已经在 `:4318` 监听（`internal/collector/server.go` 的 mux），加一个端点
是几行代码；ccl 侧 fire-and-forget 一发，不阻塞启动。

- **为什么不在 collector 里直接刷额度**：collector 是采集层，不该知道 quota 的
  存在（现在它已经通过 `Activity` 泄漏了一部分）。用 `ServerOptions.OnActivate
  func(provider string)` 回调把"激活"这件事交给 serve 层，**分层不破**。
- **触发刷新的方式**：serve 侧持有一个 `chan string`，`OnActivate` 往里写，
  `quotaRefreshLoop` 在 `select` 里监听 → 收到就**立即**跑一轮（不等 ticker）。
- **provider 名必须白名单校验**：名字来自 HTTP 查询串 → daemon 只接受
  `providers/` 目录里真实存在的 `.json` 名，否则 400。防注入、防触发对任意 URL
  的请求。
- **调用位置**：`ensureDaemonRunning()` 之后、`launcher.Launch()` 之前。
  `ensureDaemonRunning` 内部有 `waitForDaemonReady()`（最多 3s 等端口就绪）→ 调用
  时 daemon 一定已 ready。超时给 500ms（本地 loopback），失败**静默忽略** ——
  daemon 没起来也无所谓，Claude Code 的 OTel 是兜底。

### D5：daemon 启动时读 `state.json` 的 `active_provider` 兜底

daemon 启动时把 `state.json` 里的 `active_provider` 标记为活跃 → 启动那轮就能刷。

- **时序成立**：`main.go` 里 `sm.SetActiveProvider(result.Provider)` 写在
  `ensureDaemonRunning()` **之前** → ccl 拉起 daemon 时 `state.json` 已是本次
  选中的 provider。
- **为什么还要 D4**：D5 有个漏洞 —— `main.go` 里
  `if result.Provider != defaultProvider` 才写 `state.json` → **选中的正好是
  default provider 时不写** → D5 读到的还是上次的值。D4 由 ccl 直接把实际选中的
  provider 告诉 daemon，不依赖 `state.json` → **D4 是主路径，D5 只是 daemon 独立
  启动（不经 ccl）时的兜底**。
- **为什么用全局 `active_provider` 而不是 cwd binding**：daemon 启动时不知道
  Claude Code 的 cwd，全局值是更好的近似。

### D6：刷新轮次日志的计数单位改成**额度池**

请求本来就是按池发的，之前用 provider 计数会让人误以为"6 个请求"（实际 4 个）。
日志改为：

```
[quota] 额度刷新一轮（池）：查了 1 / 不活跃跳过 3 / 未到期或退避跳过 1 / 失败 0
```

`docs/provider-quota.md` 同步更新。

## Risks / Trade-offs

- **[请求量上升]** 保鲜规则让"没人用的池"也持续发请求。相对**当前**行为
  （active-only，只有活跃池被查）是**净增加**：本机 1 活跃池 + 3 不活跃池，
  60 → 78 请求/小时（+30%）；相对 **v0.20.1 之前**的全刷（240 请求/小时）仍是
  净减少（-67%）。8 个 iflytek 池的机器：全刷 480 → 纯保鲜 48 请求/小时
  → Mitigation：量级远低于厂商限流阈值；仍在 429 退避机制的覆盖范围内，且退避
  按池记录

- **[`:4318` 新暴露一个能触发对外请求的端点]** daemon 绑 0.0.0.0，局域网内能
  访问该端口的人可以 POST 触发 ccl 用用户的 key 去查厂商额度（响应是 204 空，
  不外泄数据）。Mitigation：白名单校验（只能激活已配置的 provider）+
  能访问 :4318 的人本来就能看到 OTel 数据，风险级别未提升。**接受这个取舍**，
  不为该端点单独加鉴权（与 OTel 端点保持一致）

- **[保鲜刷新的是没人看的池]** 与 v0.20.1 "只查活跃的" 的初衷有张力
  → Mitigation：10 分钟阈值下代价极小（每天多约 144 请求/4 池），换来"切过去不会
  看到几十分钟前的数字"

- **[池级过滤让一个活跃 provider 带着整池发请求]** 同池本来就只发一个请求
  （`PoolID` 去重），池级过滤**不增加**请求数，只让结果能写给同池的所有 provider
  → 无额外风险

- **[激活端点与 OTel 端点共用 :4318]** 若用户的 OTel exporter 指向了 ccl 的
  daemon，这个端点不受影响（不同路径）→ 无冲突

- **[D5 的 `state.json` 兜底可能刷到"上次用的" provider]** 轻微浪费（1 个池）
  → Mitigation：D4 为主路径，D5 只在 daemon 独立启动时生效

## Migration Plan

- 无数据格式变更 → **无需迁移**
- 回滚：换回旧 binary 即可（`quota.json` / `state.json` 格式不变，旧代码读得懂）
- 升级后建议 `ccl serve --restart` 让新 daemon 上位（旧的内存活跃度是空的）

## Open Questions

- 保鲜阈值 10 分钟将来是否需要可配（全局配置 / 环境变量）？本次先做包级常量
- 激活端点将来是否需要更强的鉴权？本次与 OTel 端点保持一致
