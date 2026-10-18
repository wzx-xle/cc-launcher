# stats-query Specification

## Purpose
TBD - created by archiving change usage-monitoring-via-otel. Update Purpose after archive.
## Requirements
### Requirement: ccl stats prints a table of session usage
`ccl stats` MUST open `~/.cc-launcher/telemetry.db` directly (no daemon HTTP) and print a human-readable table with one row per session. Default window is the last 7 days. The table MUST include a `PROJECT` column derived from `filepath.Base(session.cwd)`.

#### Scenario: stats table is readable
- **WHEN** user runs `ccl stats` after one Claude Code session
- **THEN** stdout contains a table with columns: `STARTED`, `PROJECT`, `PROVIDER`, `MODEL`, `IN`, `OUT`, `CACHE_CRE`, `CACHE_READ`, `COST`, with at least one row for that session

#### Scenario: stats supports --since filter
- **WHEN** user runs `ccl stats --since 2026-09-20`
- **THEN** only sessions started on or after that date appear

#### Scenario: stats --by-provider aggregates
- **WHEN** user runs `ccl stats --by-provider`
- **THEN** the table has one row per provider with sum of input/output tokens and total cost across all sessions

#### Scenario: stats --by-project aggregates by project name
- **WHEN** user runs `ccl stats --by-project` and sessions exist in 3 distinct projects
- **THEN** the table has 3 rows (one per project) with aggregated tokens / cost; `PROJECT` column shows the basename of each session's cwd

#### Scenario: stats --project <path> filters by substring
- **WHEN** user runs `ccl stats --project cc-launcher`
- **THEN** only sessions whose `cwd` contains the substring `cc-launcher` (full path or basename) appear

#### Scenario: stats --by-provider and --by-project are combinable
- **WHEN** user runs `ccl stats --by-provider --by-project`
- **THEN** the table has one row per (provider, project) combination

#### Scenario: stats --json outputs structured data
- **WHEN** user runs `ccl stats --json`
- **THEN** stdout is a valid JSON object with `sessions: [...]` (each session has `project` field derived from cwd basename) and `totals: {...}`; pipeable to `jq`

### Requirement: ccl stats handles the missing-db case
If `~/.cc-launcher/telemetry.db` does not exist (e.g., daemon never started), `ccl stats` MUST print a clear error and exit non-zero — NOT silently print empty results.

#### Scenario: db file does not exist
- **WHEN** user runs `ccl stats` and `~/.cc-launcher/telemetry.db` is missing
- **THEN** stdout says "no telemetry data yet; run `ccl` to start a session"; exit code is non-zero

#### Scenario: db exists but has no sessions
- **WHEN** user runs `ccl stats` and SQLite has no rows in `sessions`
- **THEN** stdout shows an empty table (no rows) with a "no data" hint; exit code 0

### Requirement: queries aggregate by metric name
`ccl stats` MUST aggregate the per-event `input_tokens` / `output_tokens` / `cache_creation_input_tokens` / `cache_read_input_tokens` into session totals.

#### Scenario: per-message tokens roll up to session totals
- **WHEN** a session has 10 assistant-message metrics each with `input_tokens=100`
- **THEN** the session row shows `IN=1000` (sum)

#### Scenario: cache tokens are shown separately
- **WHEN** a session has metrics with both `input_tokens` and `cache_creation_input_tokens`
- **THEN** the row shows distinct columns for both; no double-counting

### Requirement: ccl stats uses WAL-safe concurrent reads
`ccl stats` MUST NOT block waiting for the daemon's writes; SQLite WAL mode MUST allow concurrent reads while the daemon is the single writer.

#### Scenario: ccl stats runs while daemon is writing
- **WHEN** user runs `ccl stats` and the daemon is actively persisting metrics to the same SQLite file
- **THEN** `ccl stats` returns results in <500ms with no lock contention errors

#### Scenario: no daemon HTTP is involved
- **WHEN** user inspects `ccl stats` source code
- **THEN** there is NO HTTP client / no :4319 reference; ccl stats opens the SQLite file directly

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

