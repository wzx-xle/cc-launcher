## MODIFIED Requirements

### Requirement: ccl stats --day N uses hourly / daily bucket semantics derived from N
The bucket granularity MUST be derived from `N`: `--day 1` MUST use hourly buckets (24 rows for a full local day, including the current hour); `--day N` for `N >= 2` MUST use daily buckets (N rows for N local calendar days, ending today). Bucket boundaries MUST be computed in local timezone using `TodayStartMillis()` semantics (same source as `ccl serve --status`). Daily bucket boundaries MUST be inclusive of the start (a session started at `2026-09-30 00:00:00` local time on the last day of `--day 7` is included in the most-recent bucket). Hourly bucket boundaries MUST also be inclusive.

The "today" / "current hour" references above MUST come from the same clock as the rest of the query: when the caller injects a clock via `stats.Query.Clock` (or `QueryBucketSummary`'s `nowFn` parameter), the bucket skeleton uses that clock's "today" / "current hour" — NOT the real `time.Now()`. This is what makes `ccl stats` deterministic against a `collector.Store.WithClock` mock today. When no clock is injected, "today" / "current hour" fall back to real `time.Now()` (pre-change behavior).

#### Scenario: --day 7 boundary aligns with local calendar day
- **WHEN** a session started at `2026-09-24 00:00:00` local time and user runs `ccl stats --day 7` at any time on `2026-09-30`
- **THEN** the session is included in the `09-24` bucket (oldest of the 7 buckets)

#### Scenario: --day 30 boundary ends today (inclusive)
- **WHEN** a session started at `2026-09-30 23:59:00` local time and user runs `ccl stats --day 30` at `2026-09-30 23:59:30` local time
- **THEN** the session is included in the `09-30` bucket (most-recent of the 30 buckets)

#### Scenario: --day 7 bucket label format is MM-DD
- **WHEN** user runs `ccl stats --day 7` on `2026-09-30`
- **THEN** bucket labels are `09-24`, `09-25`, `09-26`, `09-27`, `09-28`, `09-29`, `09-30` (in chronological order)

#### Scenario: --day 1 bucket label format is HH:00
- **WHEN** user runs `ccl stats --day 1`
- **THEN** bucket labels are `00:00`, `01:00`, ..., `23:00` (24 rows; current hour's label has `*` suffix per Requirement 1)

#### Scenario: injected clock determines "today" for daily bucket skeleton
- **WHEN** a caller invokes `QueryBucketSummary(dbPath, "day", sinceMs, func() time.Time { return time.Date(2026, 9, 30, 17, 30, 0, 0, time.Local) })`
- **THEN** the most recent daily bucket is labeled `09-30` and there are `(sinceMs_to_today_inclusive)` rows ending at `09-30`; no real `time.Now()` is consulted

#### Scenario: injected clock determines "current hour" for hourly bucket skeleton
- **WHEN** a caller invokes `QueryBucketSummary(dbPath, "hour", sinceMs, func() time.Time { return time.Date(2026, 9, 30, 17, 30, 0, 0, time.Local) })`
- **THEN** the bucket skeleton has exactly 18 hourly rows (00:00 .. 17:00, the last one labeled `17:00*`); no real `time.Now()` is consulted, so running the same call at any real-world time produces the same output
