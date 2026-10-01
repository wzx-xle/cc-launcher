# time-buckets Specification

## Purpose
在 `ccl stats` 子命令下提供"按时间桶聚合"的次级视图(hourly / daily),与现有 session 明细列表并存,帮助用户快速对照"今天每小时"或"最近 N 天每天"的 token / cost 趋势,无需手算或调 `--since`。

## Requirements

### Requirement: ccl stats --day N prints a time-bucket aggregate table
`ccl stats --day N` MUST open `~/.cc-launcher/telemetry.db` directly and, after the existing session detail table, print a second human-readable table. The bucket table MUST have columns `BUCKET`, `SESSIONS`, `IN`, `OUT`, `CACHE_CRE`, `CACHE_READ`, `COST_USD`, `TOTAL_TOKENS`(柱). Each row MUST aggregate per-session token / cost totals using the same metric-name rules as the base `ccl stats` (input / output / cacheCreation / cacheRead / cost.usage). The `TOTAL_TOKENS` column MUST display a Unicode bar whose length is proportional to that row's IN + CACHE_CRE + CACHE_READ + OUT (total tokens) normalized across all rows in the table (longest bar = 20 Unicode cells, 8-block-half precision using the full character set `▏▎▍▌▋▊▉█`). For buckets where total tokens = 0 (or where the table-wide max = 0), the bar MUST render as a single middle dot `·` (U+00B7) followed by 19 spaces, distinguishing "no data" from "missing column". The `--day 1` partial bucket (current hour) MUST additionally append `⏳` (U+23F3) after the bar to flag that the bucket is still accumulating. A `TOTAL` row MUST appear at the end of the table with pure numeric aggregates; its `TOTAL_TOKENS` column MUST display `──────────────────── (total)` (20 U+2500 characters followed by ` (total)`) instead of a bar, serving as a horizontal-rule separator. The `--day N` flag MUST accept integer `N` in range `[1, 90]`; values outside MUST cause exit non-zero with a clear error. When `--day N` is used together with `--json`, the JSON output MUST include a `bucket_rows` array and a `bucket_totals` object (see Requirement 4).

#### Scenario: --day 7 produces 7 daily buckets in chronological order
- **WHEN** user runs `ccl stats --day 7` and telemetry.db has at least one session within the last 7 days
- **THEN** stdout contains a second table with headers `BUCKET SESSIONS IN OUT CACHE_CRE CACHE_READ COST_USD TOTAL_TOKENS` and exactly 7 data rows followed by 1 `TOTAL` row; data rows are in chronological order (oldest first → most recent last); each data row's `BUCKET` is a date string in `MM-DD` format (e.g., `09-24`)

#### Scenario: --day 1 produces 24 hourly buckets
- **WHEN** user runs `ccl stats --day 1` and telemetry.db has at least one session today
- **THEN** the bucket table has exactly 24 data rows plus 1 `TOTAL` row; each data row's `BUCKET` is an hour string in `HH:00` format (e.g., `00:00`, `17:00`); rows are in chronological order

#### Scenario: --day 1 includes the current hour with partial marker
- **WHEN** user runs `ccl stats --day 1` at local time 17:30 and a session started at 17:15
- **THEN** the row for `17:00` is present in the table and its `BUCKET` label is `17:00*` (asterisk suffix); the `TOTAL_TOKENS` bar for that row ends with `⏳` (U+23F3 HOURGLASS WITH FLOWING SAND); a header note reads `(* = 当前小时,数据仍在累计)` or equivalent

#### Scenario: token / cost values follow the same metric-name rules as base stats
- **WHEN** a session in the `--day 7` window has 3 metrics with `claude_code.token.usage` label `type=input` value=100 and 1 metric with `claude_code.cost.usage` value=0.01
- **THEN** the bucket containing that session shows `IN=300` (or humanized equivalent) and `COST_USD=0.0100` (or humanized equivalent)

#### Scenario: TOTAL_TOKENS bar length is proportional to total tokens
- **WHEN** the bucket with the largest total-token sum in the table has 1000 tokens and another bucket has 100 tokens
- **THEN** the largest bucket's bar is 20 Unicode cells long; the smaller bucket's bar is 2 cells long (10% proportion, 8-block-half precision)

#### Scenario: bucket table renders within 80-column terminal width without column overflow
- **WHEN** the bucket table (with bar column enabled) is rendered into a writer configured for 80-column output (e.g., `COLUMNS=80` or `tabwriter` with explicit padding width) and contains at least one partial bucket (with `⏳` suffix) and one zero-token bucket (with `·` placeholder)
- **THEN** every row in the table is no wider than 80 columns; the right edge of each `TOTAL_TOKENS` bar (including `⏳` / `·` / half-block / full-block / horizontal-rule suffixes) aligns vertically across rows within ±1 column tolerance; no row visually overflows or causes column misalignment with the surrounding `BUCKET` / `SESSIONS` / `IN` / `OUT` / `CACHE_CRE` / `CACHE_READ` / `COST_USD` columns

#### Scenario: --day N validates N is in [1, 90]
- **WHEN** user runs `ccl stats --day 0` or `ccl stats --day 91` or `ccl stats --day -1`
- **THEN** exit code is non-zero; stderr / stdout contains an error message of the form `--day 必须在 1 到 90 之间, got: <N>`

### Requirement: ccl stats preserves base behavior when --day is not specified
When `--day` is NOT specified, `ccl stats` MUST produce output byte-identical to the pre-change behavior: same session detail table, same JSON shape (no `bucket_rows` / `bucket_totals` keys), same empty-data message, same exit codes for missing db, same default 7-day window. The `--since` flag MUST NOT exist in this state (it was removed by this change; passing `--since` produces a "flag provided but not defined" error).

#### Scenario: omitting --day keeps existing detail-only output
- **WHEN** user runs `ccl stats` (without `--day`)
- **THEN** stdout does NOT contain the words `TOTAL_TOKENS` or `BUCKET`; the existing session detail table renders unchanged with the default 7-day window

#### Scenario: omitting --day keeps existing JSON shape
- **WHEN** user runs `ccl stats --json` (without `--day`)
- **THEN** the JSON object's top-level keys are exactly `since`, `group_by`, `sessions`, `totals` — no `bucket_rows` or `bucket_totals` keys are present at any nesting level

#### Scenario: --since is removed
- **WHEN** user runs `ccl stats --since 2026-09-20`
- **THEN** Go's flag package prints `flag provided but not defined: -since` to stderr; exit code is non-zero (Go's default flag parse error behavior)

### Requirement: ccl stats --day N uses hourly / daily bucket semantics derived from N
The bucket granularity MUST be derived from `N`: `--day 1` MUST use hourly buckets (24 rows for a full local day, including the current hour); `--day N` for `N >= 2` MUST use daily buckets (N rows for N local calendar days, ending today). Bucket boundaries MUST be computed in local timezone using `TodayStartMillis()` semantics (same source as `ccl serve --status`). Daily bucket boundaries MUST be inclusive of the start (a session started at `2026-09-30 00:00:00` local time on the last day of `--day 7` is included in the most-recent bucket). Hourly bucket boundaries MUST also be inclusive.

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

### Requirement: ccl stats --day N --json emits bucket_rows and bucket_totals
When `--day` is enabled together with `--json`, `ccl stats` MUST include `bucket_rows` and `bucket_totals` keys in the JSON output. `bucket_rows` MUST be an array of objects (one per bucket, in chronological order); each object MUST have fields `bucket` (label string), `bucket_iso` (ISO local-date `YYYY-MM-DD` for daily or `YYYY-MM-DDTHH:00` for hourly), `bucket_unix_ms` (UTC millisecond), `sessions` (int), `input_tokens` (int), `output_tokens` (int), `cache_creation_tokens` (int), `cache_read_tokens` (int), `cost_usd` (float). For `--day 1`, the bucket for the current hour MUST additionally have `is_current: true`. `bucket_totals` MUST be an object with the same numeric fields (no `bucket`/`bucket_iso`/`bucket_unix_ms`/`is_current`) representing the sum across all buckets. When `--day` is NOT enabled, the `bucket_rows` and `bucket_totals` keys MUST be absent from JSON output.

#### Scenario: --json --day 7 emits bucket_rows array and bucket_totals object
- **WHEN** user runs `ccl stats --json --day 7`
- **THEN** stdout contains a JSON object with top-level keys `bucket_rows` and `bucket_totals`; `bucket_rows` is an array of 7 objects each with the 9 numeric / string fields listed above; `bucket_totals` is a single object with the 6 numeric fields

#### Scenario: --json --day 1 marks current hour bucket
- **WHEN** user runs `ccl stats --json --day 1` at local time 17:30
- **THEN** the bucket_rows array has exactly 24 entries; the entry whose `bucket` is `17:00` has `is_current: true`; all other entries have `is_current` absent (omitempty)

#### Scenario: --json without --day omits bucket_rows and bucket_totals
- **WHEN** user runs `ccl stats --json` only
- **THEN** the JSON object has no `bucket_rows` or `bucket_totals` key at any nesting level

#### Scenario: bucket_rows numeric fields match the human-readable table
- **WHEN** user runs both `ccl stats --day 7` and `ccl stats --json --day 7`
- **THEN** for every bucket, the `sessions` / `input_tokens` / `output_tokens` / `cache_creation_tokens` / `cache_read_tokens` / `cost_usd` numeric fields in `bucket_rows[i]` equal the corresponding numeric values printed in the human-readable table at row `i` (before humanize), and `bucket_totals` equals the `TOTAL` row

### Requirement: ccl stats --day N uses humanize for token / cost display in text mode
In text (non-JSON) output, all numeric fields in the bucket table (`IN`, `OUT`, `CACHE_CRE`, `CACHE_READ`, `COST_USD`) and the `TOTAL` row MUST be rendered using the `humanizeTokens` rule: values `< 1000` render as the integer itself (`0`, `350`, `999`); values `>= 1000` render with 2 significant digits using K/M/B suffix (`12345 → 12.3K`, `1234567 → 1.23M`, `1234567890 → 1.23B`). The `TOTAL_TOKENS` bar visualization MUST use the un-humanized total (sum of raw int64 values) for bar length normalization, then render the bar — no numeric suffix on the bar. The `TOTAL` row MUST NOT render a bar.

#### Scenario: token values under 1K render as integers
- **WHEN** a bucket has `input_tokens = 500` and `output_tokens = 50`
- **THEN** the bucket row shows `IN=500` and `OUT=50` (no K suffix, no decimal)

#### Scenario: token values >= 1K render with 2-digit K/M/B suffix
- **WHEN** a bucket has `input_tokens = 12345`, `output_tokens = 800`, `cache_creation_tokens = 1200`, `cache_read_tokens = 5432`
- **THEN** the bucket row shows `IN=12.3K`, `OUT=800`, `CACHE_CRE=1.20K`, `CACHE_READ=5.43K`

#### Scenario: cost values always render with 4 decimal places
- **WHEN** a bucket has `cost_usd = 0.0123` and another has `cost_usd = 5.0`
- **THEN** the rows show `COST_USD=0.0123` and `COST_USD=5.0000` (4-decimal formatting, not humanized)

#### Scenario: TOTAL row shows humanized aggregates with a horizontal-rule separator
- **WHEN** the table has 7 daily buckets and the sum of all `input_tokens` across buckets is 216000
- **THEN** the `TOTAL` row shows `IN=216K` (humanized), all other numeric fields similarly humanized; the `TOTAL_TOKENS` column for the `TOTAL` row displays `──────────────────── (total)` (20 U+2500 BOX DRAWINGS LIGHT HORIZONTAL characters followed by ` (total)`)

#### Scenario: --day N tolerates empty db gracefully
- **WHEN** user runs `ccl stats --day 7` and `telemetry.db` has zero rows in `sessions`
- **THEN** the detail table is replaced with the existing `(no data in window since ...)` hint; the bucket table renders with 7 daily rows all showing `SESSIONS=0`, all token / cost fields = `0` / `0.0000`, and all 7 `TOTAL_TOKENS` bars showing a single `·` (U+00B7 MIDDLE DOT) followed by 19 spaces (placeholder, not blank); exit code 0