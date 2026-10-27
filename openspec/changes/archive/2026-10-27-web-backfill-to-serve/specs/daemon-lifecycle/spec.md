## ADDED Requirements

### Requirement: ccl serve --backfill runs the historical cwd backfill
`ccl serve --backfill` MUST run the historical session `cwd` backfill against `~/.cc-launcher/telemetry.db`: every session row whose `cwd` is empty gets resolved from Claude Code's own session records and written back.

The backfill MUST be **unbounded** (the per-start bound of 500 rows applies only to the daemon's automatic startup backfill). It MUST be **idempotent**: it only ever targets rows whose `cwd` is empty, so running it twice yields the same result, and a row created afterwards with an empty `cwd` remains eligible for a later run. It MUST NOT change the schema.

The command MUST report how many rows were resolved and how many could not be resolved, and MUST exit 0 when the backfill completes (a row that cannot be resolved is a normal outcome, not an error — the daemon may have been started before the launch context existed, and Claude Code prunes old session records).

`--backfill` MUST be mutually exclusive with the other `ccl serve` actions (`--stop / --restore / --status / --restart`), like they are with each other.

#### Scenario: backfill fills the empty cwd rows
- **WHEN** user runs `ccl serve --backfill` against a database whose session rows have an empty `cwd`, and the resolver can resolve some of them
- **THEN** those rows' `cwd` is updated to the resolved values, and stdout reports the resolved and failed counts

#### Scenario: backfill is idempotent
- **WHEN** the user runs `ccl serve --backfill` twice against the same database
- **THEN** the resulting `cwd` values are identical to running it once, and the second run reports nothing newly resolved

#### Scenario: backfill preserves an already-known cwd
- **WHEN** the user runs `ccl serve --backfill` against a database where some rows already have a `cwd`
- **THEN** those rows are left unchanged

#### Scenario: backfill does not require the schema to change
- **WHEN** the user runs `ccl serve --backfill` against a database created by a version that predates cwd enrichment
- **THEN** no table is created, dropped, or altered, and the `sessions` table's column list is unchanged

#### Scenario: backfill is mutually exclusive with the other serve actions
- **WHEN** user runs `ccl serve --backfill --stop` (or any combination with --restore / --status / --restart)
- **THEN** exit code is non-zero and stderr explains the conflict
