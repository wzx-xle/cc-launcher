## Why

`ccl stats` 子命令的 stats 查询路径对时钟不可控：`QueryStats` / `QueryBucketSummary` / `DefaultSince` / `TodayStartMillis` 都硬编码调 `time.Now()`。这导致 smoke test `TestStatsTimeBuckets::a_day7_text_has_7_buckets_and_total` 硬编码 expected dates `09-24..09-30`（与 collector store `WithClock` 注入的 2026-09-30 mock today 对齐），但 stats 路径不接收同一 clock，输出基于真实 today（2026-10-16）生成 `10-10..10-16` daily label——子测试必然失败。该失败从 v0.12.0 起即存在（`2026-09-30 17:30` 还没过去时也只是擦边通过），本 change 在 v0.13.0 已被识别为 pre-existing bug。本次给 stats 查询路径加可注入 clock，与 `collector.Store.WithClock` 模式对齐，让所有 smoke 子测试 deterministic 锚定到同一 mock today。

## What Changes

- `internal/stats/query.go`：`Query` struct 新增 `Clock func() time.Time` 字段（nil = fallback `time.Now`）；`QueryStats` 内部所有 `time.Now()`（line 74 `DefaultSince`、line 107 桶 since 推导）走 `q.clock()` helper；`DefaultSince()` 保留但改为 helper 形式以维持向后兼容（caller 仍可调）。
- `internal/stats/bucket.go`：`QueryBucketSummary(dbPath, kind, sinceMs, nowFn)` 签名加 `nowFn func() time.Time` 参数（nil = `time.Now`）；内部用 `nowFn()` 替代 `time.Now()`；`bucketSkeleton` 已接受 now 参数不动。`TodayStartMillis()` 公开签名不变（向后兼容 `CountSessionsByWindow` / `runStatusInner`），私有加 `todayStartMillisAt(now)` helper。
- `internal/stats/bucket_test.go`：4 个 `QueryBucketSummary` 调用方传 `time.Now` 作为第 4 参数。
- `cmd/cc-launcher/smoke_test.go`：`TestStatsTimeBuckets` 顶层 `now` 变量上提（已在 `OpenStore.WithClock` 用），构造 `stats.Query{Clock: func() time.Time { return now }}` 传入 `runStats`（**改 runStats 让它接收 Query 而非字段散装**——见 design Decision 2）；`a_day7_text_has_7_buckets_and_total` 把 expected dates 改回 `09-24..09-30`（与 mock today 对齐）；`b_day1` / `h_json_day1_marks_current_hour` 移除 `time.Now().Hour()` 改用固定 now.Hour()=17。
- `openspec/specs/stats-query/spec.md`：新增 requirement "stats query accepts an optional clock"。
- `openspec/specs/time-buckets/spec.md`：MODIFIED requirement 4（"uses local timezone"）：明确"query clock 可注入"。
- 不动 `internal/stats/count.go::CountSessionsByWindow`（已经接收 sinceMs 注入）。
- 不动 `TodayStartMillis()` 公开签名（`runStatusInner` 调它走真实 today 是正确行为）。
- 不动 `runStats` 行为（用户可见 CLI flag 不变；--json 输出字段不变；`--day` / `--by-provider` / `--project` 行为不变）。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `stats-query`：新增 Requirement "stats query accepts an optional clock"，规定 `Query.Clock` 字段语义（nil = default time.Now）和 `QueryBucketSummary` 第 4 参数语义。
- `time-buckets`：Requirement 4（"uses hourly / daily bucket semantics derived from N"）改一段话，明确"query 的 clock 可注入，决定 today start 与 current hour"。

## Impact

- `internal/stats/query.go` — `Query` struct 加 1 字段；`QueryStats` 内部 2 处 `time.Now()` 改走 `q.clock()`
- `internal/stats/bucket.go` — `QueryBucketSummary` 签名加 1 参数（包内 1 caller + 4 test 调用方同步更新）
- `internal/stats/bucket_test.go` — 4 处调用方补 1 参数
- `cmd/cc-launcher/statscmd.go::runStats` — 接收 `Query` 改为传 `q.Clock`（如果 `runStats` 现有结构已经传 `q.Query`，此步可能 noop；具体见 design Decision 2）
- `cmd/cc-launcher/smoke_test.go` — 顶层 now 上提 + 3 个 sub-test 改写
- `openspec/specs/stats-query/spec.md` — 主体 spec 加 Requirement
- `openspec/specs/time-buckets/spec.md` — MODIFIED 1 段
- 无 schema 变化、无 CLI 标志变化、`--json` 字节级兼容
- **修复** smoke test pre-existing failure：`a_day7_text_has_7_buckets_and_total` 不再 brittle；`b_day1` / `h_json_day1_marks_current_hour` 也不再依赖 `time.Now()` 在 sub-test 入口的"今天小时"
