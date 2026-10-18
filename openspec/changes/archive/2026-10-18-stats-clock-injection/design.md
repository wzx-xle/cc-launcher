## Context

`ccl stats` 是 token / cost 聚合的查询入口。`internal/stats` 包当前对时钟不可控：3 处 `time.Now()` 调用（`query.go:74 DefaultSince`、`query.go:107 桶 since 推导`、`bucket.go:59 QueryBucketSummary`），`TodayStartMillis()` 也是 `time.Now()`。collector store 已经用 `WithClock` 注入模式（`internal/collector/store.go:77`），但 stats 路径没跟进。

后果是 smoke test `TestStatsTimeBuckets::a_day7_text_has_7_buckets_and_total` 硬编码 expected dates `09-24..09-30`（基于 collector store mock today 2026-09-30 17:30），但 stats 路径走 `time.Now()` 真实 today 2026-10-16，输出 daily label 变成 `10-10..10-16`，子测试必然 fail。该 fail 在 v0.12.0 即存在（v0.13.0 commit message 已标记为 pre-existing bug）。

`bucket.go` 内部已经有一半基础设施就绪：
- `queryBucketSummaryAt(dbPath, kind, sinceMs, now)` 私有函数已经接受 `now time.Time` 参数（bucket.go:65）
- `bucketSkeleton(kind, now, todayStartMs, sinceMs)` 接收 now（bucket.go:248）
- `startOfDayMillis(now)` 私有 helper 接收 now（bucket.go:123）

只差 public API 不暴露 now 注入。最小改动路径：把 `queryBucketSummaryAt` 的 now 暴露给 caller。

## Goals / Non-Goals

**Goals:**
- `internal/stats` 包对外暴露 clock 注入能力，与 `collector.Store.WithClock` 模式对齐
- `QueryStats` / `QueryBucketSummary` 内部 `time.Now()` 全部走注入 clock（fallback `time.Now`）
- smoke test `a_day7_text_has_7_buckets_and_total` 重新 deterministic，锚定 collector store mock today
- 不动 `TodayStartMillis()` 公开签名（`runStatusInner` 用法合理：serve --status 显示真实 today 数据，不该 mock）
- 不动 `CountSessionsByWindow`（已通过 sinceMs 注入）
- 不动任何用户可见 CLI / `--json` 输出 / 行为

**Non-Goals:**
- 不引入 functional options 模式（与现有 `Query` struct 风格不一致，scope 大）
- 不把 `ccl serve --status` 的 today 显示也 mock（那是真实状态展示，不该注入）
- 不给所有 stats 内部函数加 clock 参数（只动 public API + 必要的 2-3 处 `time.Now()`）
- 不改 spec 主体语义（bucket 范围、hourly/daily 推导、local timezone 都保留）

## Decisions

### Decision 1：`Query.Clock` 字段而非 functional options

`Query` struct 已经是配置的载体（`Buckets` / `ByProvider` / `ByProject` / `Project` 都在 struct 上）。加 `Clock func() time.Time` 字段是自然扩展，不引入新模式。

```go
type Query struct {
    Buckets    *int
    ByProvider bool
    ByProject  bool
    Project    string
    Clock      func() time.Time // nil = default time.Now
}

// unexported helper, package 内部用
func (q Query) now() time.Time {
    if q.Clock != nil {
        return q.Clock()
    }
    return time.Now()
}
```

`QueryStats` 内 `time.Now()` 改 `q.now()`。`DefaultSince()` 仍公开（向后兼容，但 implementation 改为不调 `time.Now`——它没有 query context，留作 legacy）：

```go
// 保留 DefaultSince 为 0-arg 形式,但内部不再调 time.Now — caller 必须自己提供 clock
// Decision: 实际把 DefaultSince 内部调 time.Now 保留(它没 Query context),caller 改
// 用 query.now() 替代
```

**理由**：`DefaultSince()` 是顶层 helper 不知道 `Query`，保留它的 `time.Now()` 内部实现 + 加 `DefaultSinceAt(now time.Time)` 重载是低风险选项。`QueryStats` 不调 `DefaultSince()`，直接 `q.now().AddDate(0, 0, -7)`——更显式。

### Decision 2：`QueryBucketSummary` 改签名 vs 加新函数

5 处调用方全在本仓：
- `query.go:180 QueryStats 内部` —— 1 处
- `bucket_test.go:259 / 其它` —— 4 处 test

无第三方依赖。改签名为 `(dbPath, kind, sinceMs, nowFn func() time.Time)` 全部同步更新，5 处改动可控。**不**走"保留旧函数 + 加新函数"——避免 dead code 残留。

### Decision 3：`smoke_test.go::runStats` 入口 vs `runStats` 接收 `Query`

sub-test `a` / `b` / `h` 都需要让 stats 走 mock clock。当前 `runStats(args []string) error` 内部构造 `q := stats.Query{...}` 不接 caller 提供的 Query。

两条路：
- **R1**：扩展 `runStats` 接收 `*stats.Query`（或加 `runStatsWithQuery(q stats.Query, args []string)` 内部函数）
- **R2**：在 `TestStatsTimeBuckets` 顶层把 `now` 改成全局变量，`runStats` 内部 query 构造时检测（污染 main 包内部状态）

**选 R1**：加 `runStatsWithQuery(q stats.Query, args []string) error`，原 `runStats` 改为 thin wrapper `runStats(args) = runStatsWithQuery(stats.Query{Clock: time.Now}, args)`。更显式、无全局状态、可测。

实际上更简单：把 `runStats` 直接改签名（单 caller = main.go）。但 main.go 也调 `runStats`！所以**必须**保留 `runStats(args)` 签名。

最终：抽 `runStatsWithQuery(q stats.Query, args []string) error`，`runStats(args) = runStatsWithQuery(stats.Query{}, args)`（zero value Query 走 `time.Now` fallback）。

### Decision 4：`TodayStartMillis()` 不改

`runStatusInner` 调 `stats.TodayStartMillis()` 传进 `CountSessionsByWindow(dbPath, sinceMs)`。`serve --status` 显示"今日 sessions 数"是真实状态，不该 mock。

`TodayStartMillis()` 内部仍 `time.Now()`，与本 change 解耦。`CountSessionsByWindow` 已接 sinceMs 注入，无需改。

### Decision 5：smoke test 顶层 now 复用

`TestStatsTimeBuckets` 顶层已注入：
```go
now := time.Date(2026, 9, 30, 17, 30, 0, 0, time.Local)
store, err := collector.OpenStore(dbPath, collector.WithClock(func() time.Time { return now }))
```

stats 路径**也**用同一 now：
- `sub-test a`：expected dates 改回 `09-24..09-30`（与 now 对齐）
- `sub-test b`：移除 `currentHour := time.Now().Hour()`，改 `currentHour := now.Hour()` = 17
- `sub-test h`：同样改 `now.Hour()`
- `runStats` 调用改 `runStatsWithQuery(stats.Query{Clock: func() time.Time { return now }}, args)` 或抽到 `runStatsForTest(t, args)` helper

## Risks / Trade-offs

- [`runStatsWithQuery` 抽出来后 main.go 的 `runStats` 不变,新增的 wrapper 仅 smoke test 用 → 1 行 dead code?] → 不算 dead code,是 `runStats` 的"无注入"版本,语义自洽(等同于 `runStatsWithQuery(stats.Query{}, args)`)
- [QueryBucketSummary 改签名如果未来有外部脚本直接调 ccl stats —— ] → ccl stats 是 CLI 子命令,签名变化对 user 不可见;`stats` 是 `internal/` 包,本就不允许外部依赖
- [clock 注入让 smoke test 与真实 today 解耦,反而错过"跨日期边界真实 bug"——] → 接受该 trade-off,跨日期边界用 explicit unit test 覆盖(`stats_query_test.go` 等可加 today=month_end / today=year_end case)
- [Query.Clock 字段加在 struct 末尾,JSON marshal 时若 Clock 字段没加 `json:"-"` tag 会泄漏] → 查现有 `Query` struct (query.go:32-37),它**没**导出为 JSON(被 `Result` 引用但 `Result` 用 `Sessions` / `Totals` / `BucketRows` 等);确认 `Query` 仅是 in-memory config,**不**走 `Result.MarshalJSON`。安全。

## Migration Plan

无需迁移：
- 无 state.json / settings.json 变化
- 无 CLI 标志变化
- 无 `--json` 字段变化
- `internal/stats` 是 internal 包,无外部 caller 担忧
- 灰度：渲染层 + 查询层同 PR 内一起改,`go test ./...` + `go test -tags smoke ./...` 全绿即上线

## Open Questions

无。
