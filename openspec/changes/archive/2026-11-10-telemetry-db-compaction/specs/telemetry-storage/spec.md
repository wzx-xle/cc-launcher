## ADDED Requirements

### Requirement: metric rows keep only a whitelist of label keys

Each row in the `metrics` table MUST store its `labels` as a JSON object holding only these keys: `type`, `model`, `ccl.provider`, `provider`. Every other key present on the incoming metric — including host-level constants such as `service.name`, `service.version`, `host.arch`, `os.type`, `os.version`, `terminal.type`, `query_source`, `effort`, the host fingerprint `user.id`, and the working directory `ccl.cwd` — MUST NOT be stored.

The keys that carry the session's own identity (`model`, `ccl.cwd`, `ccl.provider`) MUST still be extracted into the `sessions` table, so the session-scoped queries keep working.

A stored `labels` object MUST be a valid JSON object even when no whitelisted key is present.

#### Scenario: a metric with many attributes is stored with few labels

- **WHEN** the daemon receives a metric whose attributes carry `service.name`, `os.type`, `user.id`, `type` and `model`
- **THEN** the stored row's `labels` holds only `type` and `model`

#### Scenario: the session-scoped columns are unaffected

- **WHEN** the daemon receives a metric carrying `ccl.cwd` and `ccl.provider`
- **THEN** the `sessions` row for that session holds the `cwd` and `provider`, and the metric row's `labels` holds neither

#### Scenario: a metric with no whitelisted attribute is still stored

- **WHEN** the daemon receives a metric whose attributes carry no whitelisted key
- **THEN** the row is stored with `labels` equal to an empty JSON object

### Requirement: data older than the retention period is pruned

The daemon MUST delete every metric row whose `captured_at` is older than the retention period, and MUST delete every session row that started before the retention period and has no metric row left. The retention period MUST be 90 days.

Pruning MUST be based on when the data was captured, not only on when the session started: a session that started before the retention period but is still producing data MUST keep its recent metric rows and MUST keep its session row.

Pruning MUST also remove metric rows that belong to no session, so that rows which can never be attributed to a session do not accumulate forever.

The pruning MUST NOT VACUUM the database. The free pages it leaves behind MUST be reusable by later writes; reclaiming them for the file system is a separate, explicit action.

#### Scenario: old metric rows are removed

- **WHEN** a metric row was captured 100 days ago
- **THEN** the row is gone after pruning

#### Scenario: a long-running session keeps its recent data

- **WHEN** a session started 100 days ago still has metric rows captured yesterday
- **THEN** those recent metric rows and the session's row are still present after pruning

#### Scenario: a session without remaining data is removed

- **WHEN** a session started 100 days ago and all of its metric rows are older than the retention period
- **THEN** both its metric rows and its session row are gone

#### Scenario: metric rows without a session are removed

- **WHEN** a metric row has no matching session row
- **THEN** the row is removed once it is older than the retention period

#### Scenario: pruning does not shrink the file on its own

- **WHEN** pruning removes rows
- **THEN** the database file keeps its size and the freed pages are left available for later writes

#### Scenario: pruning runs repeatedly without side effects

- **WHEN** pruning runs twice in a row with no new data in between
- **THEN** the second run removes nothing and leaves the database unchanged
