## MODIFIED Requirements

### Requirement: ccl stats --day N prints a time-bucket aggregate table
`ccl stats --day N` MUST open `~/.cc-launcher/telemetry.db` directly and print a human-readable time-bucket aggregate table. The bucket table MUST have columns `BUCKET`, `SESSIONS`, `IN`, `OUT`, `CACHE_CRE`, `CACHE_READ`, `COST_USD`, `TOTAL_TOKENS`. **No preceding session detail table** is printed: when `--day` is enabled, the output contains ONLY the bucket table (and a `(no data in window since ...)` hint when there are no sessions in the window). The `--day` flag's semantic is "switch to the bucket view" — users who want per-session detail run `ccl stats` without `--day` (see Requirement 2). Each row MUST aggregate per-session token / cost totals using the same metric-name rules as the base `ccl stats` (input / output / cacheCreation / cacheRead / cost.usage). The `TOTAL_TOKENS` column MUST display the humanized sum `IN + OUT + CACHE_CRE + CACHE_READ` rendered by the same `humanizeTokens` rule as the other token columns (`0` for zero, `1.23K` / `1.23M` / `1.23B` for values ≥ 1K, with at most 2 significant digits after the leading portion). The `--day N` flag MUST accept integer `N` in range `[1, 90]`; values outside MUST cause exit non-zero with a clear error. When `--day N` is used together with `--json`, the JSON output MUST include a `bucket_rows` array and a `bucket_totals` object (see Requirement 4).

#### Scenario: --day 7 produces 7 daily buckets in chronological order
- **WHEN** user runs `ccl stats --day 7` and telemetry.db has at least one session within the last 7 days
- **THEN** stdout contains a single table with headers `BUCKET SESSIONS IN OUT CACHE_CRE CACHE_READ COST_USD TOTAL_TOKENS` and exactly 7 data rows followed by 1 `TOTAL` row; data rows are in chronological order (oldest first → most recent last); each data row's `BUCKET` is a date string in `MM-DD` format (e.g., `09-24`); stdout does NOT contain the words `STARTED`, `Window since:`, or `Group by:`

#### Scenario: --day 1 produces 24 hourly buckets
- **WHEN** user runs `ccl stats --day 1` and telemetry.db has at least one session today
- **THEN** the bucket table has exactly 24 data rows plus 1 `TOTAL` row; each data row's `BUCKET` is an hour string in `HH:00` format (e.g., `00:00`, `17:00`); rows are in chronological order; stdout does NOT contain the words `STARTED`, `Window since:`, or `Group by:`

#### Scenario: --day 1 includes the current hour with a star suffix
- **WHEN** user runs `ccl stats --day 1` at local time 17:30 and a session started at 17:15
- **THEN** the row for `17:00` is present in the table and its `BUCKET` label is `17:00*` (asterisk suffix); a header note reads `(* = 当前小时,数据仍在累计)` or equivalent; the `TOTAL_TOKENS` cell for that row contains the humanized numeric sum (no Unicode bar, no hourglass symbol)

#### Scenario: token / cost values follow the same metric-name rules as base stats
- **WHEN** a session in the `--day 7` window has 3 metrics with `claude_code.token.usage` label `type=input` value=100 and 1 metric with `claude_code.cost.usage` value=0.01
- **THEN** the bucket containing that session shows `IN=300` (or humanized equivalent) and `COST_USD=0.0100` (or humanized equivalent)

#### Scenario: TOTAL_TOKENS column shows the humanized per-bucket sum
- **WHEN** the bucket with `input_tokens=1000`, `output_tokens=200`, `cache_creation_tokens=150`, `cache_read_tokens=650` (sum = 2000 tokens)
- **THEN** the row's `TOTAL_TOKENS` cell renders `2.00K` (the same humanized form `humanizeTokens` would produce for 2000)
- **AND WHEN** another bucket has `input_tokens=0`, `output_tokens=0`, `cache_creation_tokens=0`, `cache_read_tokens=0`
- **THEN** its `TOTAL_TOKENS` cell renders `0` (no Unicode characters, no placeholder glyph)

#### Scenario: bucket table renders without a fixed 80-column cap
- **WHEN** the bucket table (without a Unicode bar column) is rendered into a writer with at least 80 columns
- **THEN** every row fits within the available columns; the cells in each column align vertically across rows within the writer's `tabwriter` padding tolerance; no row introduces Unicode half-block, full-block, box-drawing, hourglass, middle-dot, or asterisk-in-bar-context characters anywhere in the table (the asterisk suffix on `BUCKET` labels for the current hour is the only such character and lives in the `BUCKET` column, not `TOTAL_TOKENS`)

#### Scenario: --day N validates N is in [1, 90]
- **WHEN** user runs `ccl stats --day 0` or `ccl stats --day 91` or `ccl stats --day -1`
- **THEN** exit code is non-zero; stderr / stdout contains an error message of the form `--day 必须在 1 到 90 之间, got: <N>`

#### Scenario: --day with empty db prints no-data hint and bucket skeleton, no detail table
- **WHEN** user runs `ccl stats --day 7` and `telemetry.db` has zero rows in `sessions`
- **THEN** stdout contains the `(no data in window since YYYY-MM-DD)` hint followed by a 7-row daily bucket table (all zero values); stdout does NOT contain `STARTED`, `Window since:`, or `Group by:`; exit code 0
