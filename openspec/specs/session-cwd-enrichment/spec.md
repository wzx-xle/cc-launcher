# session-cwd-enrichment Specification

## Purpose
TBD - created by archiving change web-usage-stats-ui. Update Purpose after archive.
## Requirements
### Requirement: session cwd falls back to Claude Code session records
Claude Code does not push `cwd` in its OTel payload, so when the payload carries no `ccl.cwd` attribute, `ccl` MUST resolve the working directory of a session from Claude Code's own session records.

This record lookup is a **fallback**: it covers sessions that were not launched through `ccl`, and historical rows written before the launch context existed. It MUST NOT override a working directory that the launch context already provided.

The resolver MUST locate the session record file at `~/.claude/projects/<encoded-project-dir>/<session-id>.jsonl` (the file name is the session id) and MUST read the first record in that file that carries a top-level `cwd` field. The resolver MUST stop reading at that record. It MUST NOT read, retain, log, or persist any other field of those records — in particular no prompt or message content.

The encoded project directory name MUST NOT be used as the cwd source, because the encoding is ambiguous (a `-` in a directory name is indistinguishable from an encoded path separator).

Resolution MUST be best-effort: when the record file is missing, unreadable, or contains no `cwd` field, the resolution MUST return an empty string and MUST NOT raise an error.

#### Scenario: cwd is resolved from the session record
- **WHEN** the daemon processes a session whose id is `S`, and `~/.claude/projects/<dir>/S.jsonl` exists and contains a record with `"cwd":"/home/u/proj"`
- **THEN** the resolver returns `/home/u/proj`

#### Scenario: a record file without a cwd field yields an empty result
- **WHEN** `~/.claude/projects/<dir>/S.jsonl` exists but none of its records carry a top-level `cwd` field
- **THEN** the resolver returns an empty string and reports no error

#### Scenario: a missing record file yields an empty result
- **WHEN** no file named `S.jsonl` exists under any directory of `~/.claude/projects/`
- **THEN** the resolver returns an empty string and reports no error

#### Scenario: no prompt content is read or retained
- **WHEN** the resolver reads a session record file
- **THEN** it stops at the first record carrying a `cwd` field and no other field of that file is copied into memory, logs, or the database

### Requirement: session rows persist the resolved cwd
The resolved cwd MUST be written to the `sessions.cwd` column on the same write path that persists the session row. A session row's `cwd` MUST NOT be left empty when a resolution succeeded.

An existing non-empty `cwd` MUST NOT be overwritten with an empty value. When a later resolution yields a non-empty value for a session whose stored `cwd` is empty, the stored value MUST be updated.

The `cwd` written for a session MUST be the `ccl.cwd` launch attribute when the payload carried one; the record lookup applies only to payloads without it.

#### Scenario: a new session row carries the resolved cwd
- **WHEN** the daemon writes a session row for session `S` and the resolver returns `/home/u/proj`
- **THEN** the `sessions` row for `S` has `cwd = "/home/u/proj"`

#### Scenario: an empty resolution does not overwrite a known cwd
- **WHEN** the daemon writes a session row for session `S` whose stored `cwd` is `/home/u/proj`, and the resolver returns an empty string
- **THEN** the stored `cwd` remains `/home/u/proj`

#### Scenario: a later resolution fills a previously empty cwd
- **WHEN** the daemon writes a session row for session `S` whose stored `cwd` is empty, and the resolver returns `/home/u/proj`
- **THEN** the stored `cwd` becomes `/home/u/proj`

#### Scenario: a failed resolution does not fail the write
- **WHEN** the resolver returns an empty string for a session
- **THEN** the session row is still persisted, with `cwd` left as an empty string, and the daemon logs no error

#### Scenario: the launch attribute wins over the record lookup
- **WHEN** the payload for session `S` carries `ccl.cwd` = `/home/u/now`, and the record lookup for `S` returns `/home/u/then`
- **THEN** the stored `cwd` is `/home/u/now`

### Requirement: historical session rows are backfilled
Session rows written before cwd enrichment existed MUST be repairable in place. The daemon MUST, on startup, resolve and persist the cwd for session rows whose `cwd` is empty.

The repair MUST be idempotent by construction: it MUST only ever target rows whose `cwd` is empty, so running it repeatedly converges rather than repeating. It MUST NOT require a schema change and MUST NOT use a one-shot marker (such as `PRAGMA user_version`) that would prevent rows created later from ever being filled.

The startup repair MUST be bounded per daemon start (at most 500 rows per start) and MUST run in the background, so that the daemon's listener becomes ready without waiting for it. An explicit, unbounded backfill MUST also be available to the user on demand.

#### Scenario: startup repair fills empty cwd rows
- **WHEN** the daemon starts against a database whose session rows have empty `cwd`, and the resolver can resolve some of them
- **THEN** those rows' `cwd` is updated to the resolved values

#### Scenario: the repair is idempotent
- **WHEN** the repair runs twice against the same database
- **THEN** the resulting `cwd` values are identical to running it once

#### Scenario: the startup repair is bounded
- **WHEN** the daemon starts against a database with more than 500 session rows having an empty `cwd`
- **THEN** at most 500 rows are repaired during that startup; the remainder are left for a later start

#### Scenario: the startup repair does not delay the listener
- **WHEN** the daemon starts and a backfill is pending
- **THEN** the HTTP listener accepts requests while the backfill is still running

#### Scenario: an explicit backfill processes every eligible row
- **WHEN** the user runs the explicit backfill command against a database with 700 session rows having an empty `cwd`
- **THEN** all 700 rows are processed in that run

### Requirement: cwd resolution outcomes are observable
The daemon MUST log the number of sessions whose cwd was resolved and the number whose resolution failed, so that a silent degradation of the project dimension is visible to the user.

#### Scenario: resolution counts are logged
- **WHEN** the daemon completes a startup backfill
- **THEN** it logs how many rows were resolved and how many could not be resolved

