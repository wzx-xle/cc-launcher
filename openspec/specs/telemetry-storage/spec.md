## ADDED Requirements

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
On `SIGTERM` (from `ccl stop` or system), the daemon MUST finish the current batch of writes within 5s, then close the SQLite handle and exit 0.

#### Scenario: graceful shutdown via ccl stop
- **WHEN** user runs `ccl stop`
- **THEN** daemon receives SIGTERM, finishes pending writes (≤ 5s), closes DB, exits 0; subsequent `ccl stats` reports the daemon as stopped

#### Scenario: SIGKILL loses in-flight writes
- **WHEN** daemon is killed with SIGKILL during a write
- **THEN** SQLite's WAL recovers on next start; no full corruption, but the in-flight row may be lost (documented limitation, not a bug)