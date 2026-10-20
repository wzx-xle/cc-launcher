## ADDED Requirements

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
