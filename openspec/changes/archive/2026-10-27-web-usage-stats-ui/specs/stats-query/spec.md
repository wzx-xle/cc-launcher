## MODIFIED Requirements

### Requirement: ccl stats prints a table of session usage
`ccl stats` MUST open `~/.cc-launcher/telemetry.db` directly (no daemon HTTP) and print a human-readable table with one row per session. The table MUST include a `PROJECT` column derived from `filepath.Base(session.cwd)`.

The table's time window MUST be defined by **when the usage occurred**, not by when the session began: a session row MUST appear only if the session recorded at least one metric whose `captured_at` falls within the window, and the token / cost columns for that row MUST sum only the metrics captured within the window. A session that started before the window but kept consuming tokens inside it MUST appear, with the tokens it consumed inside the window. The `STARTED` column MUST still report the session's real start time (which MAY precede the window).

Default window is the last 7 days. The table MUST include a `PROJECT` column derived from `filepath.Base(session.cwd)`. The `PROJECT` column MUST be empty for sessions whose working directory could not be resolved.

The window MUST be derived, not settable by an explicit start-date flag: without `--day` the window starts 7 days before the query time, and with `--day N` it starts at the local midnight of the oldest bucket day. There MUST NOT be a `--since` flag.

#### Scenario: stats table is readable
- **WHEN** user runs `ccl stats` after one Claude Code session
- **THEN** stdout contains a table with columns: `STARTED`, `PROJECT`, `PROVIDER`, `MODEL`, `IN`, `OUT`, `CACHE_CRE`, `CACHE_READ`, `COST`, with at least one row for that session

#### Scenario: the window is derived rather than settable by a start date
- **WHEN** user runs `ccl stats` without `--day`
- **THEN** the window starts 7 days before the query time; there is no `--since` flag, and passing `--since` is rejected as an unknown flag

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

## ADDED Requirements

### Requirement: ccl stats groups and filters by model
`ccl stats` MUST support aggregating by model and filtering by model, alongside the existing provider and project dimensions.

`--by-model` MUST produce one row per distinct model, with the summed input / output / cache-creation / cache-read tokens and cost across the sessions of that model. `--model <substring>` MUST restrict the result to sessions whose model contains the substring, and MUST apply the restriction to the totals as well as to the rows.

The filter MUST be applied at the query level (not by post-filtering already-aggregated rows), so that the reported totals equal the sum of the reported rows.

#### Scenario: --by-model aggregates by model
- **WHEN** user runs `ccl stats --by-model` and sessions exist with 2 distinct models
- **THEN** the table has 2 rows (one per model) with aggregated tokens / cost for that model, and the `MODEL` column shows the model name

#### Scenario: --model filters by substring
- **WHEN** user runs `ccl stats --model MiniMax`
- **THEN** only sessions whose model contains `MiniMax` appear

#### Scenario: the model filter applies to the totals
- **WHEN** user runs `ccl stats --model MiniMax --json`
- **THEN** the `totals` object's token and cost fields equal the sum of the `sessions` array's corresponding fields

#### Scenario: --by-model and --model are combinable
- **WHEN** user runs `ccl stats --by-model --model MiniMax`
- **THEN** the table has one row per distinct model matching the filter, and no row for a model outside the filter

### Requirement: ccl stats reports and filters by the session's provider
`ccl stats` MUST report, for each session, the provider that session ran on, and MUST support filtering by provider.

The provider of a session MUST be the provider recorded when the session was launched. It MUST NOT be the provider that is currently configured, because that would attribute past sessions to a provider they did not use.

`--provider <substring>` MUST restrict the result to sessions whose provider contains the substring, and MUST apply the restriction to the totals as well as to the rows, at the query level.

A session whose provider is unknown MUST be reported with an empty provider, and MUST NOT be attributed to any configured provider.

#### Scenario: the provider column carries the launching provider
- **WHEN** a session was launched through `ccl` with the provider `minimax`, and `ccl stats` is run
- **THEN** the session's `PROVIDER` column shows `minimax`

#### Scenario: --provider filters by substring
- **WHEN** user runs `ccl stats --provider mini`
- **THEN** only sessions whose provider contains `mini` appear, and the totals reflect only those sessions

#### Scenario: the provider filter applies to the totals
- **WHEN** user runs `ccl stats --provider minimax --json`
- **THEN** the `totals` object's token and cost fields equal the sum of the `sessions` array's corresponding fields

#### Scenario: a session without a provider stays unattributed
- **WHEN** a session was not launched through `ccl` and no provider was recorded for it
- **THEN** its `PROVIDER` column is empty, and `--provider` filters never match it

#### Scenario: --provider and --project are combinable
- **WHEN** user runs `ccl stats --provider minimax --project cc-launcher`
- **THEN** only sessions matching both conditions appear, and the totals reflect only those sessions
