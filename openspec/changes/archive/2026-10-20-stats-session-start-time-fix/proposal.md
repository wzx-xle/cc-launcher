## Why

`ccl stats` 的 token / cost 统计与 Anthropic 侧的用量汇总对不上。2026-10-18 实测逐日对账（`ccl stats --json` vs 官方 OTel 侧日汇总）：

| 日期 | cc-launcher 总量 | OTel 侧总量 | 倍数 | cc-l sessions | OTel sessions |
|---|---|---|---|---|---|
| 2026-10-13 | 1,989,321 | 27,248,149 | **0.07** | 6 | 5 |
| 2026-10-14 | 43,463,486 | 15,551,677 | 2.79 | 15 | 10 |
| 2026-10-15 | 111,259,785 | 49,919,695 | 2.23 | 5 | 3 |
| 2026-10-16 | 27,221,343 | 36,604,317 | 0.74 | 2 | 4 |
| 2026-10-18 | 79,209,748 | 16,644,014 | **4.76** | 5 | 1 |
| 合计 | 263,143,683 | 145,967,852 | 1.80 | 33 | 23 |

偏差**双向且幅度巨大**（同一天低 14 倍 / 高 4.8 倍），不是系统性倍数——这是"归集时间错位"的特征，不是"口径不同"或"重复计入"的特征。

**根因**：`sessions.started_at` 记录的不是 session 起始时刻，而是**该 session 最后一次 OTLP export 到达 daemon 的时刻**，且每次 export 都会覆盖它。

```
Claude Code SDK 每 60s 推一次 OTLP export（batch 内含该 session 所有活跃 metric）
   │
   ▼ collector/server.go:137    ParseMetricsRequest(body)         ← 每个 HTTP request 都调一次
   │ collector/parser.go:76-89  对 batch 内每个 session.id 建 Session{ID, StartedAt: time.Now()}
   │                                                    ↑ 错：batch 处理时刻，非 session 起始时刻
   ▼ collector/server.go:151    store.WriteSession(sess)           ← 每个 batch 都写一次
   ▼ collector/store.go:262     INSERT OR REPLACE INTO sessions (id, started_at, ...)
   │                                     ↑ REPLACE：每次 export 覆盖 started_at
   ▼
最终 sessions.started_at = 该 session「最后一次 export」的时刻
```

**本机真实 telemetry.db 实测证据**（43 sessions / 7107 metrics）：

- **43/43** session 的 `MAX(metrics.captured_at) == sessions.started_at`（误差 < 1s）
- 以 `claude_code.session.count` metric 的时间戳为真值（它就是 session 创建时刻）：**25/41（61%）** session 的 `started_at` 偏晚，最大偏差 **64,479 秒 ≈ 18 小时**
- 逐日偏差方向与"token 全被推到 session 最后活跃那天"完全吻合

**后果**：

1. **归集日错位**：`--day N` 的 daily 桶按 `s.started_at` 归桶 → 跨天 session 的全部 token 堆在最后一天（10-18 高 4.76x），之前的日期被掏空（10-13 低 14x）
2. **窗口边界错**：`WHERE s.started_at >= since` 按"最后 export 时刻"划窗 → 数天前开始、今天仍在跑的 session 被计入窗口，且其**全生命周期** token 一次性计入 → 总量虚高
3. **session 数偏移**：同样被推到错误的日期（10-18：OTel 侧 1 个，cc-launcher 侧 5 个）

**曾经假设但已被推翻**：本 change 的前一版把根因归为"OTel SDK 进程退出时推 close-flush 携带累计值，SUM 重复计入"，并计划引入 `StartTimeUnixNano` 落库 + Datadog OTEL-2649 的 `shouldConsumeInitialValue` heuristic。实测证明该假设错误：按 OTel 真实 series 身份（`session_id` + `name` + 完整 labels）分组，Claude Code 推的是 **Delta** 语义（296 个 series 非单调 / 120 个单调，值序列明显波动而非递增），Delta 求和本身正确，不存在 close-flush 重复计入；且该 heuristic 在"SDK 晚于 daemon 启动"的正常场景下**根本不会命中**（SDK close-flush 发生在 SDK 退出时，其 series 起始时间晚于 daemon 启动）。据此重写本 change。

## What Changes

- `internal/collector/parser.go`：`Session.StartedAt` 改为**真实 session 起始时刻**——优先取该 session 的 `claude_code.session.count` metric 的 `captured_at`；该 batch 内无 `session.count` 时回退到 batch 内该 session 的最小 `captured_at`。不再使用 `time.Now()`
- `internal/collector/store.go::insertSession`：`INSERT OR REPLACE` 改为 `INSERT ... ON CONFLICT(id) DO UPDATE`，其中 `started_at = MIN(sessions.started_at, excluded.started_at)`，保证该列**永不被后续 batch 抬高**
- `internal/stats/bucket.go::queryBucketTokensSum`：daily 桶的归桶键由 `s.started_at` 改为 `m.captured_at`（与 hourly 统一，按**消费时刻**归集）；token 查询的窗口过滤由 `s.started_at >= since` 改为 `m.captured_at >= since`
- `internal/stats/query.go::buildSessionStatsSQL`：token / cost 聚合的窗口过滤改为 `m.captured_at >= since`（按消费时刻划窗），session 元信息（`MIN(s.started_at)` / model / project）不变
- `internal/stats/query.go::QueryStats`：`--day N` 时 detail 的 `since` 对齐到桶窗口起点（最旧桶那天的本地 00:00），使 detail 与桶视图覆盖同一时间窗
- 历史数据回填：daemon 启动时（`PRAGMA user_version` 驱动，幂等）执行一次 `UPDATE sessions SET started_at = COALESCE(session.count 的 MIN(captured_at), metrics 的 MIN(captured_at))`，修正已被污染的历史 `started_at`
- `openspec/specs/telemetry-storage/spec.md`：ADDED Requirement "session start time reflects the real session start, not the last export time"
- `openspec/specs/stats-query/spec.md`：MODIFIED Requirement "ccl stats prints a table of session usage"（窗口语义）+ "queries aggregate by metric name"（归桶语义）
- 不改 db 列结构、不改 CLI flag、不改 JSON 字段名、不改 `ccl stats` 表格格式

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `telemetry-storage`：新增 Requirement "session start time reflects the real session start, not the last export time"（`sessions.started_at` 的语义、写入幂等性、历史回填）
- `stats-query`：MODIFIED "ccl stats prints a table of session usage"（窗口按消费时刻划定）+ MODIFIED "queries aggregate by metric name"（桶按消费时刻归集）

## Impact

- `internal/collector/parser.go` — `ParseMetricsRequest` 收集每 session 的起始时刻；`Session.StartedAt` 赋值改源
- `internal/collector/store.go` — `insertSession` 改 `ON CONFLICT DO UPDATE`；新增一次性回填（`PRAGMA user_version` 驱动）
- `internal/stats/bucket.go` — daily 归桶键 + token 窗口过滤
- `internal/stats/query.go` — detail 窗口过滤 + `--day N` 时 since 对齐
- `openspec/specs/telemetry-storage/spec.md` — ADDED 1 个 Requirement
- `openspec/specs/stats-query/spec.md` — MODIFIED 2 个 Requirement
- **用户可见**：逐日数字与 OTel 侧对齐；`--day N` 的桶按消费时刻分布（跨天 session 的 token 不再全堆在一天）
- **历史数据**：桶视图（按 `m.captured_at` 归集）**立即对全部历史生效**（`captured_at` 一直是准的）；detail 视图的 `STARTED` 列经回填后修正
- **行为变更**：`ccl stats`（无 `--day`）的 session 行语义从"session 全生命周期用量"改为"**窗口内**的用量"；`--day N` 时 JSON 的 `since` 变为桶窗口起点（更早）。两者都是"统计窗口"语义向 OTel 侧口径对齐，CHANGELOG 需明确标注
- 无 db 列变化、无 CLI flag 变化、无 JSON 字段名变化、无表格格式变化
