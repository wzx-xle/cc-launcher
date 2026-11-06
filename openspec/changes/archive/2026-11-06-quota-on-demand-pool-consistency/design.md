## Context

额度池 = `sha256(端点 + "\0" + API key)`。同一把 key 的多个 provider 引用同一个池。

刷新有两条路径：

| 路径 | 入口 | 写给谁 |
|---|---|---|
| serve 周期刷新 | `RefreshAll` | **所有引用该池的 provider** ✓ |
| 手动刷新 | `RefreshOne` | 只有被点名的那一个 ✗ |

## Decisions

### 决策 1：手动刷新也写给同池所有 provider

数据同源是硬要求：额度池的额度是**一个事实**，两个 provider 显示不同时刻的值
等于"同一个东西有两个答案"。

实现：`Store.UpdatePoolEntries(name, pool, source, dims, nextAt)` —— 读文件 →
找出所有 `pool` 相同的条目 → 一起更新（source/pool/dimensions/next_at/fetched_at
统一）→ 原子写回。

`next_at` 也统一：相位是**按池**分配的（`assignPhases` 按 pool 分组），同池的
provider 本来就共享同一个 next_at。

### 决策 2：写回走数据文件里的既有条目，不枚举 providers 目录

同池的 provider 里，可能有还没条目的（从没被查过）。从数据文件里找同池条目能
覆盖"已经有数据因而会出现不一致"的那些；没条量的下次 serve 刷新会补上。

代价：极端情况下（一个同池 provider 从没被查过）它要等下一轮才拿到数据 —— 可
接受（它本来就没有数据）。

## Risks / Trade-offs

- 无实质风险：只是让同源数据一致
