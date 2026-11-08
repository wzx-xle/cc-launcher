## Why

**额度数据既不及时，也不自洽。** 两件事都是实测确认的：

**一、同额度池的多个 provider 显示不同时刻的数据。** `internal/quota/refresh.go:572`
的"只刷新活跃 provider"过滤做在 **provider 级** —— 不活跃的 provider 在登记归属池
**之前**就 `continue` 了；刷新结果写回时按池匹配，匹配不上它 → 同池成员永远拿不到
新数据。实测（2026-11-06）：`minimax-m3` 与 `minimax-m3.1-flash` 同 key 同 endpoint
→ 同一个额度池，`fetched_at` 却相差 38 分钟。违反 `provider-quota` spec 的"同池同源"
要求（手动刷新路径已修，serve 的周期刷新路径漏了）。

**二、额度迟迟不刷新。** daemon 的 provider 活跃度是**纯内存**状态，而"活跃"的唯一
来源是 Claude Code 推来的 OTel metrics。于是 daemon 刚重启就整整 60 秒一个 provider
都没查过（实测 serve.log 连续 4 轮"查了 0 / 不活跃跳过 6"，第 5 轮才恢复；60 秒 =
Claude Code 的 OTel 周期）—— `daemon-lifecycle` spec 写的"启动即刷一轮"从未成立。
daemon 常驻时新跑一次 `ccl` 也要等首条 OTel + 一个 15 秒 ticker（≈18 秒）。

根子在于**因果倒置**：判定"该查谁"用"已经在用"当信号，而"已经在用"要等 Claude Code
跑起来推 OTel 才知道。**但 ccl 启动 Claude Code 时本就确知这次要用哪个 provider**
—— 这个最权威的信号一直没被用上。

## What Changes

- **额度刷新的粒度从 provider 级提升到额度池级**：池内任一 provider 活跃 → 整池
  刷新；结果写给池内**所有** provider。同一把 key 的额度是一个事实，不该因为
  过滤粒度而裂成两份。
- **新增兜底保鲜**：某个额度池的记录超过 **10 分钟**未更新时，即便当前没有活跃
  provider 也刷新一次 → 用户切到任何 provider 都不会看到几十分钟前的数字。
  （本机规模：4 个池 × 每 10 分钟 ≈ 24 请求/小时，可忽略。）
- **新增 ccl → daemon 的激活通道**：daemon 暴露 `POST /ccl/activate?provider=X`，
  收到即标记该 provider 活跃并**立即触发一轮刷新**（不等 ticker）；ccl 在拉起
  daemon 之后 fire-and-forget 调一次，**不阻塞启动**。ccl 是唯一确知"用户这次要
  用哪个 provider"的角色，这个信号此前完全没被利用。
- **daemon 启动时读 `state.json` 的 `active_provider` 作为初始活跃集合**兜底 →
  "启动即刷一轮"第一次真正成立。
- **刷新轮次日志的计数单位从 provider 改成额度池**（请求本来就是按池发的）。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `provider-quota`: 刷新的对象粒度从 provider 改为额度池（池内任一 provider 活跃
  则整池刷新）；新增兜底保鲜规则（超过 10 分钟未更新的池即使无活跃 provider 也
  刷新）；新增"ccl 启动时声明本次使用的 provider，daemon 立即刷新"的激活要求
- `daemon-lifecycle`: "daemon 启动即刷新一轮"必须真正生效（不得因活跃度为空而
  空转）；新增 provider 激活端点

## Impact

- **改动** `internal/quota/refresh.go`：`RefreshAll` 的活跃过滤提到池级；新增
  保鲜阈值参数；不活跃池的到期判定改用 `max(next_at, fetched_at+保鲜阈值)`
- **改动** `internal/collector/server.go`：新增 `POST /ccl/activate` 端点
- **改动** `cmd/cc-launcher/serve.go`：daemon 启动时读 `state.json` 兜底；接上
  激活通道（收到激活信号 → 立即触发一轮刷新）；刷新轮次日志改用池计数
- **改动** `cmd/cc-launcher/main.go`：`ensureDaemonRunning()` 之后向 daemon 声明
  本次使用的 provider（fire-and-forget）
- **改动** `docs/provider-quota.md`：刷新粒度、保鲜规则、激活通道
- **数据文件格式不变**（`quota.json` / `state.json` 均不改）；`ccl quota` 输出
  契约不变；`CCL_PROVIDER` 契约不变
- 风险：
  - 请求量小幅上升（保鲜规则：约 24 请求/小时/4 池；激活通道：每次 `ccl` 启动
    最多多 1 个池）→ 仍在 429 退避机制的覆盖范围内
  - `POST /ccl/activate` 是 daemon 新暴露的 HTTP 面 → provider 名必须做白名单
    校验（只接受 `providers/` 目录里真实存在的名字），防注入
