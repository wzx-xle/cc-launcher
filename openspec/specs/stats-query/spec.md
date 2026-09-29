## ADDED Requirements

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