# telemetry-storage Specification

## Purpose
TBD - created by archiving change usage-monitoring-via-otel. Update Purpose after archive.
## Requirements
### Requirement: telemetry lives in a single SQLite file with WAL mode
All persisted metrics / session rows MUST go to a single SQLite file at `~/.cc-launcher/telemetry.db`. The database MUST be created (with schema) lazily on first write. **WAL mode MUST be enabled** (`PRAGMA journal_mode=WAL`) on every connection, so that `ccl stats` reads do not block the daemon's writes.

The file MUST be readable by `sqlite3` CLI for manual debugging.

#### Scenario: db is created on first event
- **WHEN** the daemon receives its first OTLP export and `~/.cc-launcher/telemetry.db` does not exist
- **THEN** the daemon creates the file, runs `CREATE TABLE IF NOT EXISTS` for all tables, runs `PRAGMA journal_mode=WAL`, and persists the event

#### Scenario: WAL is enabled on every open
- **WHEN** the daemon opens the SQLite file
- **THEN** it issues `PRAGMA journal_mode=WAL`; a subsequent `sqlite3 ~/.cc-launcher/telemetry.db "PRAGMA journal_mode"` returns `wal`

#### Scenario: db is reusable across daemon restarts
- **WHEN** the daemon is killed and restarted, and the SQLite file exists
- **THEN** the daemon opens the file (no re-create), re-enables WAL, continues persisting to existing tables

#### Scenario: db file is plain SQLite
- **WHEN** `sqlite3 ~/.cc-launcher/telemetry.db ".tables"` is run from the shell
- **THEN** it lists `events`, `metrics`, `sessions` — wait, no, it MUST list only `metrics`, `sessions` (logs/traces not stored)

#### Scenario: ccl stats reads while daemon writes
- **WHEN** `ccl stats` opens the SQLite file while the daemon is actively writing
- **THEN** `ccl stats` does NOT block; SQLite WAL allows concurrent reads + single writer

### Requirement: schema has two tables with stable contract
The schema MUST define `sessions` and `metrics` with the columns and indices specified in `design.md` §Decision 6. Schema changes MUST require an explicit migration (no silent column drops).

#### Scenario: tables and indices exist after first run
- **WHEN** the daemon starts on a fresh machine
- **THEN** the schema contains `sessions(id, started_at, ended_at, provider, cwd, model)`, `metrics(id, session_id, name, value, labels, captured_at)`, plus indices `metrics(session_id, captured_at)` and `sessions(cwd, started_at)`

#### Scenario: metric labels are stored as JSON
- **WHEN** a metric with multiple labels (`provider=deepseek`, `model=deepseek-chat`) is persisted
- **THEN** the `labels` column contains a JSON object with both keys; `ccl stats --json` round-trips without losing fields

#### Scenario: cwd is the project's working directory at session start
- **WHEN** Claude Code starts a session with `cwd=/home/zxwang/workspace/golang/cc-launcher`
- **THEN** the `sessions.cwd` column stores the absolute path; `ccl stats` can group/filter by project (using `filepath.Base(cwd)` as the project name)

#### Scenario: events table does NOT exist
- **WHEN** the daemon runs and any session produces events
- **THEN** `sqlite3 ~/.cc-launcher/telemetry.db ".tables"` does NOT list `events`; no log/trace data is persisted (logs/traces are explicitly dropped at HTTP layer)

### Requirement: writes are non-blocking on slow disk
A slow SQLite write MUST NOT cause the OTLP HTTP server to time out. Writes are queued and drained asynchronously; if the queue exceeds 10k events, oldest events are dropped (and a counter is logged) rather than blocking the receiver.

#### Scenario: disk stall does not reject incoming OTLP exports
- **WHEN** the SQLite write path is paused (e.g., disk full, fsync slow)
- **THEN** the HTTP server continues to accept and acknowledge OTLP exports; the queue depth is logged

#### Scenario: queue overflow drops oldest events
- **WHEN** the in-memory write queue exceeds 10k entries while disk is paused
- **THEN** the oldest entries are dropped; a `ccl stats --internal` shows the drop count; subsequent exports are still accepted

### Requirement: daemon flushes SQLite on shutdown
On `SIGTERM` (from `ccl serve --stop` or system), the daemon MUST finish the current batch of writes within 5s, then close the SQLite handle and exit 0.

#### Scenario: graceful shutdown via ccl serve --stop
- **WHEN** user runs `ccl serve --stop`
- **THEN** daemon receives SIGTERM, finishes pending writes (≤ 5s), closes DB, exits 0; subsequent `ccl stats` reports the daemon as stopped

#### Scenario: SIGKILL loses in-flight writes
- **WHEN** daemon is killed with SIGKILL during a write
- **THEN** SQLite's WAL recovers on next start; no full corruption, but the in-flight row may be lost (documented limitation, not a bug)

### Requirement: session start time reflects the real session start, not the last export time

The `sessions.started_at` column MUST hold the moment the session actually began, NOT the moment the daemon last received an OTLP export for it.

The daemon derives the start time from the telemetry payload itself:

- The `captured_at` of the session's `claude_code.session.count` metric is the authoritative observation of the session's creation, and MUST be used when present
- Otherwise, the earliest `captured_at` among the session's metrics in the same OTLP export batch is used as an approximation
- The daemon MUST NOT use the wall-clock time at which the export batch was received

Writing a session row MUST be idempotent with respect to `started_at`: when a session row already exists, the stored `started_at` MUST be replaced by the **minimum** of the stored value and the newly derived value, so that a later export batch can never move the start time forward. The write MUST use `INSERT ... ON CONFLICT(id) DO UPDATE` semantics (not `INSERT OR REPLACE`) so that the `started_at` clamp is applied while the remaining columns (`ended_at`, `provider`, `cwd`, `model`) continue to be refreshed as they are today.

Because the metric timestamps that precede the fix were already stored correctly, the daemon MUST repair existing rows on startup: it MUST set `sessions.started_at` to the minimum `captured_at` of that session's `claude_code.session.count` metric, falling back to the minimum `captured_at` of all of that session's metrics. The repair MUST run at most once per database, tracked via SQLite's `PRAGMA user_version`, and MUST be idempotent if executed again.

#### Scenario: session start time comes from the session.count metric
- **WHEN** the daemon receives an OTLP export containing a `claude_code.session.count` metric for session `S` whose datapoint timestamp is `1790664811109`
- **THEN** the `sessions` row for `S` has `started_at = 1790664811109`, regardless of when the export was received

#### Scenario: a later export batch does not move the start time forward
- **WHEN** session `S` was first seen in an export batch whose metrics are timestamped `1790664811109`, and a later export batch for the same session contains metrics timestamped `1790665211109`
- **THEN** the `sessions` row for `S` still has `started_at = 1790664811109` (the earlier of the two), NOT the later timestamp

#### Scenario: the start time survives a daemon restart
- **WHEN** the daemon is restarted and receives a new export batch for an existing session `S`
- **THEN** `sessions.started_at` for `S` is unchanged (still the original session start), and is not reset to the restart time or the new batch's timestamp

#### Scenario: existing rows are repaired on startup
- **WHEN** the daemon starts against a database whose `sessions.started_at` values were written by a version that recorded the last export time
- **THEN** the daemon updates each affected row to the session's earliest recorded metric timestamp, exactly once; a subsequent daemon start does not re-run the repair

#### Scenario: repair is idempotent
- **WHEN** the repair statement is executed twice against the same database
- **THEN** the resulting `sessions.started_at` values are identical to executing it once

#### Scenario: sessions without any metrics are left untouched
- **WHEN** the daemon starts and a session row exists whose `session_id` has no rows in the `metrics` table
- **THEN** that row's `started_at` is left unchanged (no NULL is written, and the row is not deleted)

