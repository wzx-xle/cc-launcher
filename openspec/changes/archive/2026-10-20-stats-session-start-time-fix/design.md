## Context

### 现象

`ccl stats` 与 Anthropic 侧用量汇总对不上，逐日偏差**双向且幅度巨大**（10-13 低 14 倍，10-18 高 4.8 倍）。详见 proposal.md 的对账表。

### 已确证的根因

`sessions.started_at` 被**每次 OTLP export 覆盖**，最终值 = session 最后一次 export 的时刻。

```
collector/server.go:137   ParseMetricsRequest(body)            每个 HTTP request 调一次
collector/parser.go:76-89  Session{ID, StartedAt: time.Now()}   ← batch 处理时刻，非 session 起始
collector/server.go:151   store.WriteSession(sess)              每个 batch 写一次
collector/store.go:262    INSERT OR REPLACE INTO sessions       ← REPLACE 覆盖 started_at
```

实测（本机真实 db，43 sessions / 7107 metrics）：
- **43/43** session 的 `MAX(metrics.captured_at) == sessions.started_at`
- 以 `claude_code.session.count` metric 的时间戳为真值：**25/41（61%）** 偏晚，最大 **64,479 秒 ≈ 18 小时**

### 被推翻的前一版假设（保留记录，避免走回头路）

前一版把根因归为"OTel SDK 退出时推 close-flush 携带累计值 → SUM 重复计入"，方案是落库 `StartTimeUnixNano` + Datadog OTEL-2649 的 `shouldConsumeInitialValue` heuristic（drop「series 起始早于 daemon 启动」的首个 datapoint）。**实测证伪**：

1. **源是 Delta 不是 Cumulative**。按 OTel 真实 series 身份（`session_id` + `name` + 完整 labels JSON）分组统计：296 个 series 值序列非单调、120 个单调，值明显波动（`[39961, 11377, 9681, 5209, 73344, 2205, ...]`）→ Delta 语义，**求和本身正确**，不存在重复计入
2. **heuristic 方向与自己的故事矛盾**。SDK close-flush 发生在 SDK **退出**时，其 series 起始时间 = SDK **启动**时刻。正常场景（daemon 先起、Claude Code 后起）下 `start_time > daemon_start` → heuristic **不会命中**；它唯一命中的场景（SDK 早于 daemon 存在）恰恰是「SDK 第一个 export 覆盖了启动以来的真实累计增量」——**那是真实用量，不是 close-flush**，drop 掉即丢数据
3. **历史数据无法修复**。close-flush 的原始信息（哪个 datapoint 是 flush）从未落库，事后不可重建 → 老 db 永远修不好
4. 附带问题：drop 子查询只覆盖 `claude_code.token.usage`，漏掉偏差最大的 `claude_code.cost.usage`

**新方案的关键优势**：根因数据（`metrics.captured_at`）**一直是准的**，所以桶视图修复**对全部历史立即生效**，且历史 `sessions.started_at` 可从 metrics 表回填。

### 现有代码事实

- `internal/collector/parser.go:83` — `StartedAt: time.Now()`
- `internal/collector/parser.go:76-89` — `sessionModels` map 收集 session→model，但**不收集时间**
- `internal/collector/store.go:262` — `INSERT OR REPLACE INTO sessions`
- `internal/stats/query.go:243-247` — detail 表聚合，`WHERE s.started_at >= ?`
- `internal/stats/bucket.go:181-182` — 桶归桶键：hourly 用 `m.captured_at`，daily 用 `s.started_at`（**双 key 策略**）
- `internal/stats/bucket.go:197` — tokens 查询 `WHERE s.started_at >= ?`
- `internal/stats/bucket.go:272` — `bucketSkeleton` 的天数 = `(todayStartMs - sinceMs) / 24h + 1`，`sinceMs` **未对齐午夜**

## Goals / Non-Goals

**Goals:**
- `sessions.started_at` = 真实 session 起始时刻，且**写入幂等**（永不被抬高）
- token / cost 的**窗口边界**与**归集日**都按**消费时刻**（`metrics.captured_at`）——与 OTel 侧"按 API 调用发生时刻归集"口径一致
- detail 视图与桶视图覆盖**同一时间窗**、token 总和**完全一致**
- 历史数据可修正：桶视图立即生效；`sessions.started_at` 一次性回填
- 与 OTel 侧逐日对账可验证

**Non-Goals:**
- 不改 db 列结构（`start_time_unix_nano` / `daemon_state` 全部不引入——前一版的方案整体废弃）
- 不做 cumulative→delta 转换（实测源已是 Delta）
- 不改 CLI flag / JSON 字段名 / 表格格式
- 不追溯修正 `metrics` 表本身（`captured_at` 一直是准的，无需修）
- 不改 `claude_code.cost.usage` 的口径（cost 由 Claude Code SDK 计算，与 token 同源同窗即可对齐）

## Decisions

### Decision 1：`Session.StartedAt` 取真实 session 起始时刻

**取值优先级**：

1. **`claude_code.session.count` metric 的 `captured_at`** —— 这是 session 创建时刻的权威观测。实测证据：它比 `sessions.started_at` 早最多 18 小时，说明它记录的是真实创建时刻（该 metric 在 session 创建时被 observe，随第一个 export 批次发出）
2. **回退：同一 OTLP batch 内该 session 的最小 `captured_at`** —— 无 `session.count` 时（极少见）用 batch 内最早的活动时刻近似
3. **不再使用 `time.Now()`**

**实现**：`ParseMetricsRequest` 内新增 `sessionStarts map[string]int64`（session.id → 起始毫秒），在 `extractFromMetric` 遍历 datapoint 时更新：
- 若 `name == "claude_code.session.count"` → 记 `min(existing, dp.captured_at)`
- 否则 → 记 `min(existing, dp.captured_at)`（作为回退基线）

由于 `session.count` 的观测时刻必然 ≤ 同 batch 内其它 metric 的时刻，同一个 map 里 `min` 天然让 `session.count` 优先。**不需要两套 map**。

**理由**：让"session 起始"有一个数据源，而不是从 export 到达时刻反推。

### Decision 2：`insertSession` 改 `ON CONFLICT DO UPDATE`，`started_at` 取 `MIN`

```sql
INSERT INTO sessions (id, started_at, ended_at, provider, cwd, model)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    started_at = MIN(sessions.started_at, excluded.started_at),
    ended_at   = excluded.ended_at,
    provider   = excluded.provider,
    cwd        = excluded.cwd,
    model      = excluded.model
```

**为什么必须**：`INSERT OR REPLACE` 会整行替换。即使 parser 改成取 `min(captured_at)`，**后续 batch 的 min 会更大**（该 batch 里的 metric 时间更晚）→ REPLACE 会把 `started_at` 抬高。`MIN(sessions.started_at, excluded.started_at)` 保证只降不升，与 REPLACE 的效果在正确输入下等价，但多一道防御。

**model 的处理**：保持"后续 batch 覆盖"语义（等价于原 REPLACE），不在本次改动范围内。

**为什么不用 `INSERT OR IGNORE`**：那样 model / ended_at 就永远不更新了，与现状行为不一致。

### Decision 3：历史 `sessions.started_at` 一次性回填，用 `PRAGMA user_version` 驱动

```sql
UPDATE sessions SET started_at = COALESCE(
    (SELECT MIN(m.captured_at) FROM metrics m
      WHERE m.session_id = sessions.id AND m.name = 'claude_code.session.count'),
    (SELECT MIN(m.captured_at) FROM metrics m
      WHERE m.session_id = sessions.id)
)
WHERE started_at <> COALESCE(
    (SELECT MIN(m.captured_at) FROM metrics m
      WHERE m.session_id = sessions.id AND m.name = 'claude_code.session.count'),
    (SELECT MIN(m.captured_at) FROM metrics m
      WHERE m.session_id = sessions.id)
);
```

**幂等性**：用 `PRAGMA user_version` 做 schema 版本号。daemon 启动时读 `user_version`，`< 1` 则执行回填并 `PRAGMA user_version = 1`。执行一次后不再重复。

**为什么用 `user_version` 而不是自建表**：SQLite 内置、零 schema 变更、不与 `sessions` / `metrics` 的契约冲突。

**回填的边界**：回填只修正 `started_at`。若某 session 的 `metrics` 表里**没有任何行**（只有 session row 无 metric），`COALESCE` 两项都 NULL → `started_at` 保持原值（WHERE 条件也不匹配）→ 无害。

**为什么不提供 `ccl stats --recompute`**：回填在 daemon 侧自动完成，`ccl stats` 是只读进程，不该写 db。

### Decision 4：token / cost 的窗口边界改用 `m.captured_at`

**detail 表**（`buildSessionStatsSQL`）：

```sql
-- 原
WHERE s.started_at >= ?
-- 新
WHERE m.captured_at >= ?
```

**桶表的 tokens 查询**（`queryBucketTokensSum`）：同上。

**理由**：
- 窗口语义从"session 在窗口内开始"变为"**用量在窗口内发生**"——与 OTel 侧按 API 调用时刻归集一致
- 原语义下，一个数天前开始、今天仍在跑的 session 会把**全生命周期** token 一次性计入 → 总量虚高
- `LEFT JOIN` + `m.captured_at` 过滤退化为 INNER JOIN 语义——窗口外无活动的 session 不再出现（原语义下它们也可能出现并贡献 0）

**副作用（已知且接受）**：`ccl stats`（无 `--day`）的 session 行数字从"session 全生命周期用量"变为"**窗口内**用量"。这是语义变更，需在 CHANGELOG 明确标注。理由：统计工具的默认视图应当是"这段时间用了多少"，而不是"这些 session 一辈子用了多少"；后者可以通过不带时间窗的独立查询提供（本次不做）。

**session count 桶不变**：仍按 `s.started_at` 归桶（"该时段开始了多少个 session"），与 token 桶（"该时段消耗了多少 token"）是两个不同的量，各自语义正确。

### Decision 5：桶表的归桶键统一为 `m.captured_at`

```go
// 原（双 key 策略）
tsCol := "s.started_at"
if kind == "hour" { tsCol = "m.captured_at" }
// 新（统一）
tsCol := "m.captured_at"
```

**理由**：
- hourly 已经用 `m.captured_at`；daily 用 `s.started_at` 是为了"与 sessions 计数对齐"（原 design §D6 的理由），但代价是**跨天 session 的全部 token 堆在开始那天**——正是逐日偏差的主因
- 归集日应当是"用量发生的那天"，与 OTel 侧一致
- 统一后 hourly / daily 语义一致，用户切换 `--day 1` / `--day 7` 得到同源数据

**与 Decision 4 的关系**：Decision 4 改"划窗"，Decision 5 改"归桶"。两者都指向 `m.captured_at`，方向一致。

**sessions 计数查询**（`queryBucketSessionsCount`）保持 `s.started_at` 不变。

### Decision 6：`--day N` 时 detail 的 `since` 对齐桶窗口起点

`QueryStats` 现在 `since = q.now().AddDate(0, 0, -N)`（非午夜对齐），而 `bucketSkeleton` 的天数按 `(todayStartMs - sinceMs) / 24h + 1` 计算 → 桶覆盖 `[todayStart-(N-1)d, todayStart]`，detail 覆盖 `[now-N d, now]`。

**两者不重合**：`[now-N d, todayStart-(N-1)d)` 这段（约 1 天的尾部）在 detail 里但不在任何桶里 → detail totals ≠ Σbucket rows。这是**现存**的不一致（不修也一直在）。

**修法**：`--day N` 时 `since` 取**最旧桶那天的本地 00:00**：

```go
if q.Buckets != nil {
    n := *q.Buckets
    now := q.now()
    oldest := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
        AddDate(0, 0, -(n - 1))
    since = oldest
}
```

这样 detail 与桶覆盖 `[oldest, now]`，**完全重合**。

**副作用**：JSON 的 `since` 字段值会变（更早，最多早 1 天）。属窗口语义修正，CHANGELOG 标注。

**无 `--day` 时**：`since = q.now().AddDate(0, 0, -7)` 不变（无桶视图，不存在对齐问题）。

### Decision 7：`bucketSkeleton` 的天数计算保持不变

`days = (todayStartMs - sinceMs) / 24h + 1`。Decision 6 让 `sinceMs` 午夜对齐后，`todayStartMs - sinceMs` 正好是 `(N-1) * 24h` → `days = N`，与 `--day N` 的语义精确吻合。

**不需要改 `bucketSkeleton`**，只需保证传入的 `sinceMs` 午夜对齐。**这也修复了原代码注释里那句"sinceMs 必须对齐到本地时区午夜(由 runStats 在调 QueryBucketSummary 前保证)"——那个保证此前并不存在**（runStats 传的是 `now - N days`）。Decision 6 补上了它。

## Risks / Trade-offs

- [**detail 视图语义变更**（全生命周期 → 窗口内）] → 这是"统计窗口"语义向正确方向对齐，但用户可能觉得"session 用了 500K 怎么只显示 20K"。缓解：CHANGELOG 明确标注；`STARTED` 列仍显示真实 session 起始（可能早于窗口），可据此判断
- [**回填把 `started_at` 改成 metrics 的最早时间，与真实 session 起始可能差几秒到几分钟**] → `session.count` metric 的观测时刻就是 session 创建时刻，误差在 export interval（60s）内。实测中它比 `sessions.started_at` 早最多 18 小时，说明它确实更准
- [**回填对没有 `session.count` 的 session 用 `MIN(captured_at)`**] → 那是该 session 最早的活动时刻，**不早于**真实起始 → 保守（略晚），不会把 session 归到错误的、更早的日子
- [**回填 UPDATE 全表，大 db 上慢**] → 只在 `user_version < 1` 时执行一次；`metrics` 上有 `idx_metrics_session_time(session_id, captured_at)` 索引，子查询可用；单次执行，可接受
- **[窗口过滤改成 `m.captured_at` 后，detail 视图可能少列 session**] → 原语义下列出的"0 token session"（有 session row 无 metric）会消失——它们本就是噪音
- [**跨 daemon 版本边界的历史数据**] → 回填对**全部**历史生效（不限于新数据），因为 `metrics.captured_at` 一直在。**这是本方案相对前一版的核心优势**
- **[两个视图的 session 集合含义不同**] → detail 列出"窗口内有活动的 session"；桶表的 SESSIONS 列是"该时段开始的 session 数"。两者不同但各自语义正确，spec 中分别写明

## Migration Plan

1. **collector 侧**（写 db）：
   - `parser.go`：`Session.StartedAt` 改取真实起始（Decision 1）
   - `store.go::insertSession`：`ON CONFLICT DO UPDATE` + `MIN`（Decision 2）
   - `store.go`：daemon 启动时按 `PRAGMA user_version` 执行一次性回填（Decision 3）
2. **stats 侧（只读）**：
   - `query.go`：detail 窗口过滤改 `m.captured_at`；`--day N` 时 `since` 午夜对齐（Decision 4 + 6）
   - `bucket.go`：daily 归桶键改 `m.captured_at`（Decision 5）
3. **无需 db schema 变更**（无新列、无新表）
4. **回滚**：老版本 collector 用 `time.Now()` + REPLACE → 恢复旧行为（但会再次污染 started_at）；老版本 stats 用旧窗口语义 → 数字回到旧口径。**双向可回滚**
5. **灰度**：collector + stats 在同一 PR 内改，`go test ./...` + `go test -tags smoke ./...` 全绿即上线；上线后跑一次逐日对账验证

## Open Questions

1. **detail 视图是否应保留"session 全生命周期用量"作为独立视图**（例如未来的 `ccl stats --all-time`）？本次不做，等用户实际使用反馈。
2. **`sessions.ended_at` 是否也值得修正**？当前 parser 从不设置它（恒 NULL），本次不动。若未来要修，思路与 `started_at` 同源（最后一个 metric 的时间）。
3. **回填后是否需要给 `sessions` 加索引**？现有 `idx_sessions_cwd_time(cwd, started_at)` 够用；回填的子查询走 `idx_metrics_session_time`。
