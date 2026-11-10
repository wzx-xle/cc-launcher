## ADDED Requirements

### Requirement: the daemon migrates stored metric labels to the whitelist form on startup

When the daemon opens the telemetry database, it MUST detect that the stored metric rows still carry the pre-compaction label shape and MUST rewrite them so that each row's `labels` holds only the whitelisted keys.

Before rewriting, the daemon MUST take a consistent snapshot of the database into the backup directory — a snapshot that includes the data still held in the write-ahead log, so that the pre-migration state can be restored.

The migration MUST be idempotent: a database already in the whitelisted form MUST NOT be rewritten again, and a repeated attempt MUST NOT produce a second backup.

If the migration fails, the daemon MUST log the failure and MUST still start — the database MUST remain usable in its pre-migration form, and the migration MUST be retried on the next startup.

#### Scenario: a legacy database is migrated on startup

- **WHEN** the daemon starts against a database whose metric rows still carry host-level attributes
- **THEN** the rows' `labels` hold only the whitelisted keys, and a snapshot of the pre-migration database exists in the backup directory

#### Scenario: the snapshot contains the data still held in the write-ahead log

- **WHEN** the daemon takes the pre-migration snapshot while recent transactions are still in the write-ahead log
- **THEN** the snapshot holds the same row counts as the live database

#### Scenario: a migrated database is not migrated again

- **WHEN** the daemon starts against a database whose metric rows already hold only whitelisted keys
- **THEN** the rows are left untouched and no additional snapshot is written

#### Scenario: a failed migration does not stop the daemon

- **WHEN** the migration fails while the daemon is starting
- **THEN** the daemon logs the failure, starts anyway, and the database remains usable

### Requirement: ccl serve --compact compacts the telemetry database

`ccl serve --compact` MUST reclaim the free space held by the telemetry database by rewriting it, so that the file on disk shrinks to the size the retained data actually needs.

The compaction MUST run while no daemon holds the database, because rewriting it requires exclusive access. When a daemon is running, the command MUST stop it first, and MUST start a daemon again afterwards. When no daemon was running, the command MUST NOT start one.

The compaction MUST NOT change the stored data: the session rows, the metric rows and their values MUST be identical before and after.

The command MUST report the size before and after the compaction.

#### Scenario: a running daemon is stopped, compacted and started again

- **WHEN** the user runs `ccl serve --compact` while a daemon is running
- **THEN** the daemon is stopped, the database is rewritten, a daemon is running again, and the command reports the size before and after

#### Scenario: compacting without a running daemon does not start one

- **WHEN** the user runs `ccl serve --compact` while no daemon is running
- **THEN** the database is rewritten and no daemon is started

#### Scenario: compaction preserves the stored data

- **WHEN** the user runs `ccl serve --compact`
- **THEN** the session count, the metric count and the sum of the metric values are identical before and after

#### Scenario: a failed compaction leaves the database usable

- **WHEN** the rewrite fails
- **THEN** the command exits non-zero, the database is still readable, and the daemon is running again if it was running before
