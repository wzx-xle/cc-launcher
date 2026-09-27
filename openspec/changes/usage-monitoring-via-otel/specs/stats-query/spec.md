## ADDED Requirements

### Requirement: ccl stats prints a table of session usage
`ccl stats` MUST call the daemon's HTTP `:4319/stats` endpoint and print a human-readable table with one row per session. Default window is the last 7 days. The table MUST include a `PROJECT` column derived from `filepath.Base(session.cwd)`.

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

### Requirement: ccl stats handles the offline case
If the daemon is not running, `ccl stats` MUST print a clear error and exit non-zero — NOT silently print empty results.

#### Scenario: daemon not running
- **WHEN** user runs `ccl stats` and `:4319` is unreachable
- **THEN** stdout says "daemon not running, run ccl to start one" and exit code is non-zero

#### Scenario: daemon alive but db empty
- **WHEN** user runs `ccl stats` and SQLite has no sessions
- **THEN** stdout shows an empty table (no rows) with a "no data" hint; exit code 0

### Requirement: HTTP API on :4319 returns the same data
The daemon MUST expose HTTP endpoints for stats so non-CLI consumers (future TUI / web dashboard) can use the same API.

#### Scenario: GET /stats returns sessions + totals
- **WHEN** a HTTP client sends `GET http://localhost:4319/stats?since=2026-09-01&by_provider=true`
- **THEN** the response is JSON with `sessions` array and `totals` object

#### Scenario: GET /stats accepts project filter
- **WHEN** a HTTP client sends `GET http://localhost:4319/stats?project=cc-launcher`
- **THEN** only sessions whose cwd contains `cc-launcher` appear in the response

#### Scenario: GET /health returns 200
- **WHEN** a HTTP client sends `GET http://localhost:4319/health`
- **THEN** the response is `200 OK` with `{"status":"ok"}`

### Requirement: queries aggregate by metric name
`ccl stats` MUST aggregate the per-event `input_tokens` / `output_tokens` / `cache_creation_input_tokens` / `cache_read_input_tokens` into session totals.

#### Scenario: per-message tokens roll up to session totals
- **WHEN** a session has 10 assistant-message metrics each with `input_tokens=100`
- **THEN** the session row shows `IN=1000` (sum)

#### Scenario: cache tokens are shown separately
- **WHEN** a session has metrics with both `input_tokens` and `cache_creation_input_tokens`
- **THEN** the row shows distinct columns for both; no double-counting