## MODIFIED Requirements

### Requirement: schema has two tables with stable contract
The schema MUST define `sessions` and `metrics` with the columns and indices specified in `design.md` §Decision 6. Schema changes MUST require an explicit migration (no silent column drops).

#### Scenario: tables and indices exist after first run
- **WHEN** the daemon starts on a fresh machine
- **THEN** the schema contains `sessions(id, started_at, ended_at, provider, provider_endpoint, cwd, model)`, `metrics(id, session_id, name, value, labels, captured_at)`, plus indices `metrics(session_id, captured_at)` and `sessions(cwd, started_at)`

#### Scenario: metric labels are stored as JSON
- **WHEN** a metric with multiple labels (`provider=deepseek`, `model=deepseek-chat`) is persisted
- **THEN** the `labels` column contains a JSON object with both keys; `ccl stats --json` round-trips without losing fields

#### Scenario: cwd is the project's working directory at session start
- **WHEN** a session runs with working directory `/home/zxwang/workspace/golang/cc-launcher` and the working directory can be resolved for that session
- **THEN** the `sessions.cwd` column stores the absolute path; `ccl stats` can group/filter by project (using `filepath.Base(cwd)` as the project name)

#### Scenario: cwd is empty when the working directory cannot be resolved
- **WHEN** a session's working directory cannot be resolved (no Claude Code session record found for it, and the launch carried no working directory)
- **THEN** the `sessions.cwd` column is left empty, the session row is still persisted, and `ccl stats` shows that session under an empty project name

#### Scenario: the launch context is stored on the session row
- **WHEN** a session is launched through `ccl` with provider `minimax` and endpoint host `api.minimax.cn`
- **THEN** the `sessions.provider` column stores `minimax` and the `sessions.provider_endpoint` column stores `api.minimax.cn`

#### Scenario: events table does NOT exist
- **WHEN** the daemon runs and any session produces events
- **THEN** `sqlite3 ~/.cc-launcher/telemetry.db ".tables"` does NOT list `events`; no log/trace data is persisted (logs/traces are explicitly dropped at HTTP layer)

## ADDED Requirements

### Requirement: the provider endpoint column is added by an explicit migration
The `sessions.provider_endpoint` column MUST be introduced through an explicit, idempotent schema migration driven by the schema version. The daemon MUST NOT drop or recreate existing tables or columns when adding it, and MUST NOT lose existing rows.

The migration MUST run automatically when the daemon opens a database written by an older version.

#### Scenario: an older database gains the column
- **WHEN** the daemon opens a database whose `sessions` table has no `provider_endpoint` column
- **THEN** the column is added, the existing session rows are preserved, and the schema version is advanced

#### Scenario: the migration is idempotent
- **WHEN** the daemon opens a database that already has the `provider_endpoint` column
- **THEN** the column is not added a second time and no error is raised

#### Scenario: existing rows keep their values
- **WHEN** a database with populated `provider` and `cwd` values is migrated
- **THEN** those values are unchanged after the migration

### Requirement: session rows are repaired in place without a schema change
Enriching the `sessions.cwd` column MUST NOT require a schema change and MUST NOT consume a one-shot repair marker. The repair MUST be driven by the data itself: it targets only rows whose `cwd` is empty.

This means a session row created after the repair has run is still eligible for a later repair, and running the repair repeatedly converges instead of repeating.

#### Scenario: the repair targets only empty cwd rows
- **WHEN** the repair runs against a database where some session rows already have a `cwd` and others have an empty `cwd`
- **THEN** only the rows with an empty `cwd` are candidates for repair; rows with a `cwd` are not touched

#### Scenario: the repair requires no schema change
- **WHEN** the repair runs against a database created by a version that predates cwd enrichment
- **THEN** no table is created, dropped, or altered, and the `sessions` table's column list is unchanged

#### Scenario: the repair does not consume a one-shot marker
- **WHEN** the repair runs, and afterwards a new session row with an empty `cwd` is written
- **THEN** that new row is still eligible for repair on a later run

#### Scenario: the repair preserves an already-known cwd
- **WHEN** the repair encounters a session row whose `cwd` is non-empty
- **THEN** the stored value is left unchanged
