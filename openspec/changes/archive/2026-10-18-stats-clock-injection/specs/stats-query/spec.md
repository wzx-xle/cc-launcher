## ADDED Requirements

### Requirement: stats query accepts an optional clock for deterministic time-sensitive results
The `stats.Query` struct MUST accept an optional `Clock func() time.Time` field. When `Clock == nil`, the package MUST fall back to `time.Now` (preserving current behavior); when `Clock != nil`, every internal `time.Now()` call in the stats query path (default 7-day window derivation, bucket since derivation, `QueryBucketSummary` current-hour resolution, today-start calculation) MUST route through the provided function instead. The `QueryBucketSummary(dbPath, kind, sinceMs, nowFn)` public function MUST accept `nowFn func() time.Time` as a 4th parameter; `nil` MUST mean "use `time.Now`". The public `TodayStartMillis()` function MUST keep its 0-arg signature (it remains a real-time helper for `ccl serve --status`); a private `todayStartMillisAt(now)` MUST back it.

The contract purpose is deterministic testing: tests that inject a mock clock via `collector.Store.WithClock` MUST be able to make stats queries (via `stats.Query{Clock: ...}` or the `nowFn` parameter) read the same mock time, eliminating the `smoke_test.go::a_day7_text_has_7_buckets_and_total` brittleness caused by stats paths silently diverging from the mocked collector clock.

The `--json` output and CLI behavior MUST be byte-identical whether the caller supplies a clock or not.

#### Scenario: Query.Clock = nil preserves the pre-change time.Now() behavior
- **WHEN** a caller constructs `stats.Query{Buckets: &n}` without setting `Clock` and invokes `QueryStats`
- **THEN** the package reads `time.Now()` for window derivation, bucket since, and `QueryBucketSummary` current-hour resolution; output and behavior are byte-identical to v0.13.0 (no `Clock` field in `Query`)

#### Scenario: Query.Clock routes all internal time reads through the function
- **WHEN** a caller constructs `stats.Query{Clock: func() time.Time { return fixedNow }}` and invokes `QueryStats` while real `time.Now()` advances
- **THEN** every internal time read in the query path (default 7-day window, bucket since, `QueryBucketSummary`, `todayStartMillisAt`) returns `fixedNow`; the output reflects `fixedNow`'s date / hour, NOT real `time.Now()`

#### Scenario: QueryBucketSummary nowFn parameter replaces the time.Now() inside
- **WHEN** a caller invokes `stats.QueryBucketSummary(dbPath, "day", sinceMs, func() time.Time { return fixedNow })`
- **THEN** the returned bucket skeleton uses `fixedNow`'s local calendar day for "today"; bucket label `09-30` (or whatever `fixedNow`'s date) appears in the result; no real `time.Now()` is consulted for current-day or current-hour resolution

#### Scenario: QueryBucketSummary with nil nowFn still uses time.Now()
- **WHEN** a caller invokes `stats.QueryBucketSummary(dbPath, "day", sinceMs, nil)`
- **THEN** behavior is identical to the pre-change `QueryBucketSummary(dbPath, "day", sinceMs)` (i.e., real `time.Now()` is used); no behavior regression

#### Scenario: --json output is byte-identical with or without Clock injection
- **WHEN** the same db is queried via `stats.Query{}` (no clock) and via `stats.Query{Clock: realTimeFn}` where `realTimeFn` returns `time.Now()`
- **THEN** the two `--json` outputs are byte-identical (same `since`, same `bucket_rows`, same `bucket_totals`, same field ordering); `Query.Clock` is not serialized into JSON output

#### Scenario: smoke test a_day7_text_has_7_buckets_and_total no longer depends on real today
- **WHEN** `TestStatsTimeBuckets` injects a fixed clock `now = 2026-09-30 17:30 local` into BOTH `collector.OpenStore(WithClock)` AND `stats.Query{Clock: ...}`, then runs `ccl stats --day 7`
- **THEN** the bucket table contains daily labels `09-24, 09-25, 09-26, 09-27, 09-28, 09-29, 09-30` regardless of real-world today's date; the test passes on any day of the year
