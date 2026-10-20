## MODIFIED Requirements

### Requirement: ccl stats prints a table of session usage
`ccl stats` MUST open `~/.cc-launcher/telemetry.db` directly (no daemon HTTP) and print a human-readable table with one row per session. The table MUST include a `PROJECT` column derived from `filepath.Base(session.cwd)`.

The table's time window MUST be defined by **when the usage occurred**, not by when the session began: a session row MUST appear only if the session recorded at least one metric whose `captured_at` falls within the window, and the token / cost columns for that row MUST sum only the metrics captured within the window. A session that started before the window but kept consuming tokens inside it MUST appear, with the tokens it consumed inside the window. The `STARTED` column MUST still report the session's real start time (which MAY precede the window).

Default window is the last 7 days. The table MUST include a `PROJECT` column derived from `filepath.Base(session.cwd)`.

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
- **THEN** the table has one row for each (provider, project) combination

#### Scenario: stats --json outputs structured data
- **WHEN** user runs `ccl stats --json`
- **THEN** stdout is a valid JSON object with `sessions: [...]` (each session has `project` field derived from cwd basename) and `totals: {...}`; pipeable to `jq`

#### Scenario: usage outside the window is not counted
- **WHEN** a session recorded 100,000 input tokens on day 1 and 5,000 input tokens on day 3, and the window covers only day 3
- **THEN** the session's `IN` column shows `5,000`, not `105,000`; the row still appears because it had activity in the window

#### Scenario: a session started before the window still appears
- **WHEN** a session started 10 days ago and consumed tokens yesterday, and the window covers yesterday
- **THEN** the session appears in the table with only yesterday's tokens; its `STARTED` column shows the real start time 10 days ago

#### Scenario: sessions without in-window activity are omitted
- **WHEN** a session recorded metrics only before the window
- **THEN** it does not appear in the table

### Requirement: queries aggregate by metric name
`ccl stats` MUST aggregate the per-event `input_tokens` / `output_tokens` / `cache_creation_input_tokens` / `cache_read_input_tokens` into session totals.

All token and cost aggregation MUST be scoped by the metric's own `captured_at` timestamp (when the usage occurred), both for the session-detail table and for the time-bucket table. Time-bucket rows MUST be keyed by the metric's `captured_at` as well — for hourly buckets AND for daily buckets alike — so that a session spanning multiple days has its tokens distributed across the days the usage actually occurred, rather than being attributed entirely to the day the session began. The bucket table's `SESSIONS` column remains keyed by the session's start time (it counts sessions that *began* in that bucket); the token / cost columns are keyed by consumption time. The two are independent measures and MUST NOT be conflated.

When `--day N` is in effect, the session-detail aggregation and the bucket aggregation MUST cover exactly the same time window: the window MUST start at the local midnight of the oldest bucket day and end at the query time. The `since` field reported in `--json` output MUST reflect that window start.

#### Scenario: per-message tokens roll up to session totals
- **WHEN** a session has 10 assistant-message metrics each with `input_tokens=100`
- **THEN** the session row shows `IN=1000` (sum)

#### Scenario: cache tokens are shown separately
- **WHEN** a session has metrics with both `input_tokens` and `cache_creation_input_tokens`
- **THEN** the row shows distinct columns for both; no double-counting

#### Scenario: daily buckets are keyed by consumption time
- **WHEN** a session started on `2026-10-14` and its metrics were captured on `2026-10-14`, `2026-10-15`, and `2026-10-16`
- **THEN** in `ccl stats --day 7`, the `10-14`, `10-15`, and `10-16` bucket rows each carry the tokens captured on that date; the `10-14` row does NOT contain the `10-15` and `10-16` tokens

#### Scenario: detail totals and bucket totals agree
- **WHEN** user runs `ccl stats --day 7 --json` and compares the detail `totals` token fields with the sum of the `bucket_rows` token fields
- **THEN** the two are equal for every token field and for `cost_usd`

#### Scenario: --day N aligns the window to the bucket boundaries
- **WHEN** user runs `ccl stats --day 7 --json` at local time `2026-10-18 17:04`
- **THEN** the reported `since` is the local midnight of `2026-10-12` (`2026-10-12T00:00:00` local), and the bucket table contains exactly 7 daily buckets `10-12` … `2026-10-18`

#### Scenario: without --day the window is unchanged
- **WHEN** user runs `ccl stats --json` without `--day`
- **THEN** the reported `since` is 7 days before the query time, not midnight-aligned
