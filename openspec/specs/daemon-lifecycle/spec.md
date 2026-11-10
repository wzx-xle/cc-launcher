# daemon-lifecycle Specification

## Purpose
TBD - created by archiving change usage-monitoring-via-otel. Update Purpose after archive.
## Requirements
### Requirement: ccl auto-starts the daemon when port is free
On every `ccl` invocation, before exec'ing Claude Code, ccl MUST probe `localhost:4318` with a 200ms TCP connect timeout. If unreachable AND no daemon record exists, ccl MUST fork `ccl serve` as a detached child process and wait up to 3s for the port to become reachable.

#### Scenario: first ccl invocation in a session
- **WHEN** user runs `ccl` and `:4318` is unreachable
- **THEN** ccl forks `ccl serve` (detached, in a new process group), polls `:4318` every 100ms, exits the wait when port opens, then proceeds to exec Claude Code; total pre-exec latency ≤ 3s

#### Scenario: subsequent ccl invocation (daemon alive)
- **WHEN** user runs `ccl` and `:4318` is reachable
- **THEN** ccl skips fork, proceeds to exec Claude Code; pre-exec latency < 50ms

#### Scenario: daemon fails to bind (port stolen between probe and fork)
- **WHEN** ccl forks `ccl serve` but the new daemon exits within 3s with "port already in use"
- **THEN** ccl prints "OTel daemon failed to start, falling back" and proceeds to exec Claude Code without OTEL env (Claude Code won't push metrics this session but still runs)

### Requirement: daemon is forked into a new process group
`ccl serve` MUST be launched with `SysProcAttr.Setpgid: true` so that the daemon has its own PGID, independent of ccl's PID and Claude Code's forked children. This prevents `ccl serve --stop` from accidentally signaling Claude Code or its child processes.

#### Scenario: daemon survives ccl's syscall.Exec
- **WHEN** ccl forks `ccl serve` (new PGID) and then `syscall.Exec`s Claude Code (inherits ccl's original PGID)
- **THEN** the daemon's PID and PGID are different from Claude Code's; killing one does not affect the other

#### Scenario: ccl serve --stop SIGTERM does not affect Claude Code
- **WHEN** Claude Code is running (started by previous `ccl`) and user runs `ccl serve --stop`
- **THEN** the SIGTERM goes only to the daemon PGID (not Claude Code's); Claude Code keeps running

### Requirement: ccl serve --stop sends SIGTERM to daemon
`ccl serve --stop` MUST find the running daemon (via pidfile at `~/.cc-launcher/daemon.pid`) and send SIGTERM. **On Unix**, the signal MUST be sent to the daemon's PGID (negative PID) so it does not accidentally signal Claude Code or its child processes. After SIGTERM, ccl MUST wait up to 5s for the process to exit; if still alive, send SIGKILL.

#### Scenario: stop kills daemon gracefully (Unix)
- **WHEN** user runs `ccl serve --stop` on Unix and daemon PID is in `daemon.pid`
- **THEN** SIGTERM is sent to the daemon PGID (`kill -PID`); daemon flushes SQLite within 5s and exits; ccl prints "daemon stopped"

#### Scenario: stop kills daemon gracefully (Windows)
- **WHEN** user runs `ccl serve --stop` on Windows and daemon PID is in `daemon.pid`
- **THEN** SIGTERM-equivalent (Windows process termination) is sent directly to the daemon PID (Windows has no PGID concept); daemon exits

#### Scenario: stop SIGKILLs hung daemon
- **WHEN** daemon does not exit within 5s after SIGTERM (or Windows equivalent) and user invoked `ccl serve --stop`
- **THEN** ccl sends SIGKILL; daemon dies; pidfile removed

#### Scenario: stop when no daemon
- **WHEN** user runs `ccl serve --stop` and `daemon.pid` does not exist
- **THEN** ccl prints "daemon not running" and exits 0

### Requirement: daemon writes a pidfile at startup
On startup `ccl serve` MUST write its PID to `~/.cc-launcher/daemon.pid` atomically (write to .tmp, rename). On shutdown, MUST remove the file.

#### Scenario: pidfile is created
- **WHEN** `ccl serve` starts successfully
- **THEN** `~/.cc-launcher/daemon.pid` contains the daemon's PID

#### Scenario: pidfile is removed on clean shutdown
- **WHEN** daemon exits via SIGTERM
- **THEN** the pidfile is removed before exit

#### Scenario: stale pidfile does not block new daemon
- **WHEN** user starts `ccl serve` and `daemon.pid` exists from a previous crash
- **THEN** `ccl serve` overwrites the pidfile (no error); ccl serve --stop finds the new PID

### Requirement: daemon refuses to start if port is occupied
`ccl serve` MUST attempt to bind `:4318` on startup. If bind fails with `EADDRINUSE` (or equivalent), it MUST log an actionable error and exit non-zero. It MUST NOT auto-pick a different port.

#### Scenario: port occupied by another OTel service
- **WHEN** `ccl serve` starts and `:4318` is held by, say, an OpenTelemetry Collector
- **THEN** daemon prints: `port 4318 already in use; either stop the conflicting service or set OTEL_EXPORTER_OTLP_ENDPOINT to a different port and re-run claude with that env var`; exit non-zero

#### Scenario: port free
- **WHEN** `ccl serve` starts and `:4318` is free
- **THEN** bind succeeds and the daemon accepts OTLP traffic

### Requirement: daemon handles SIGTERM with graceful shutdown
On SIGTERM (or platform equivalent), the daemon MUST run a graceful shutdown sequence: stop accepting new OTLP requests, drain the async write queue, close the SQLite handle, remove the pidfile, then exit 0. Total wall time MUST be ≤ 5s; if exceeded, `ccl serve --stop` sends SIGKILL.

#### Scenario: SIGTERM stops accepting then drains
- **WHEN** daemon receives SIGTERM with 100 events in the async write queue
- **THEN** within 5s the daemon:
  - HTTP handler returns 503 Service Unavailable for new requests (signaling "going away")
  - All 100 queued events are written to SQLite
  - SQLite handle is closed (WAL checkpointed)
  - pidfile is removed
  - process exits 0

#### Scenario: SIGTERM with empty queue is fast
- **WHEN** daemon receives SIGTERM with empty write queue
- **THEN** within 200ms the daemon exits 0 cleanly (no waiting on queue)

#### Scenario: in-flight HTTP request is not interrupted
- **WHEN** daemon receives SIGTERM while a POST to /v1/metrics is mid-write
- **THEN** the in-flight request completes normally; new requests get 503; shutdown proceeds after the in-flight request finishes

#### Scenario: SIGKILL during graceful shutdown loses only the queue tail
- **WHEN** `ccl serve --stop` escalates to SIGKILL after 5s
- **THEN** any events still in the in-memory queue are lost; events already committed to SQLite survive (SQLite WAL); pidfile may or may not be removed (best-effort cleanup)

### Requirement: fork failure does NOT block ccl's core function
The daemon is an OPTIONAL enhancement for usage statistics. If the daemon fails to start (port taken, fork fails, bind error, etc.), ccl MUST still proceed to exec Claude Code. The user loses usage stats for that session but the core flow continues.

#### Scenario: daemon refuses to start, ccl still works
- **WHEN** user runs `ccl` and `ccl serve` exits within 3s with port conflict
- **THEN** ccl prints "OTel daemon failed to start; usage stats for this session will be lost" and proceeds to exec Claude Code; user can still use ccl

#### Scenario: fork timeout, ccl still works
- **WHEN** user runs `ccl` and the forked `ccl serve` does NOT bind `:4318` within 3s
- **THEN** ccl prints "OTel daemon did not start in time; falling back" and proceeds to exec Claude Code

#### Scenario: subsequent ccl retries daemon
- **WHEN** user runs `ccl` again (after a previous session's daemon failed to start)
- **THEN** ccl re-attempts the fork; user can recover from transient failures

### Requirement: ccl serve --status reports daemon runtime status
`ccl serve --status` MUST print a human-readable status summary and exit 0 (status reporting is informational; missing connections are NOT errors). The status MUST include:
- Whether the daemon is currently running (judged by port `:4318` reachability AND the pidfile at `~/.cc-launcher/daemon.pid` — see matrix below)
- The daemon's PID when running
- The SQLite db file path and size at `~/.cc-launcher/telemetry.db`; if the db file is missing, MUST print "missing" rather than erroring

#### Scenario: status when daemon is running
- **WHEN** user runs `ccl serve --status` and `daemon.pid` exists with PID N and `:4318` is reachable
- **THEN** stdout shows a right-aligned 10-char `daemon:` label followed by `pid=N` and `port :4318 reachable` indented 11 spaces under `   daemon: running`; plus a right-aligned `       db:` label with the db path, size, and `<total> sessions (<today> today)` on three indented sub-lines

#### Scenario: status when no daemon but port is held by foreign service
- **WHEN** user runs `ccl serve --status` and `daemon.pid` does NOT exist but `:4318` is reachable
- **THEN** stdout shows `   daemon: not running` with `port :4318 in use by foreign service (no pidfile)` indented under it; plus a `db` row

#### Scenario: status when stale pidfile (crashed daemon)
- **WHEN** user runs `ccl serve --status` and `daemon.pid` exists but `:4318` is unreachable
- **THEN** stdout shows `   daemon: not running` with `stale pidfile pid=N` and `port :4318 unreachable` indented under it; plus a `db` row

#### Scenario: status when nothing is running
- **WHEN** user runs `ccl serve --status` and `daemon.pid` does NOT exist and `:4318` is unreachable
- **THEN** stdout shows `   daemon: not running` with `port :4318 unreachable` indented under it; plus a `db` row

#### Scenario: status when db file is missing
- **WHEN** user runs `ccl serve --status` and `~/.cc-launcher/telemetry.db` does NOT exist
- **THEN** stdout shows the daemon portion as above and the db row as a single `       db: missing (no telemetry yet)` line; exit code is 0

#### Scenario: status is mutually exclusive with stop and restore
- **WHEN** user runs `ccl serve --status --stop` (or any combination with --restore)
- **THEN** exit code is non-zero and stderr explains the conflict

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

### Requirement: ccl serve refreshes the quotas of the configured providers

`ccl serve` MUST refresh the quota of every provider that declares a quota interface **and that has reported activity recently**, and MUST do so on a periodic schedule while it is running. A provider that has not reported activity recently MUST NOT be queried, because its quota is not being used.

The refresh interval MUST default to 60 seconds. A provider MUST be able to override it through its own configuration.

The refresh MUST happen in the daemon process rather than in the process that reports a quota, so that a consumer that reports the quota never waits for the network.

The daemon MUST NOT query a provider whose recorded quota is younger than that provider's refresh interval, and MUST derive that age from the recorded quota rather than from a time kept only in the daemon's memory.

When the daemon starts, it MUST refresh the quotas once immediately instead of waiting for the first interval to elapse. That first refresh MUST NOT be left with nothing to query because the daemon has not seen any activity yet, and the daemon MUST fall back to the provider recorded as active.

A refresh that fails MUST NOT discard the quota that was recorded for that provider previously, and MUST NOT stop the daemon's refresh schedule.

Two providers that resolve to the same quota endpoint and the same API key MUST be queried once per cycle, and the result MUST be recorded for both.

The refresh MUST NOT delay or fail the daemon's other duties: the metrics endpoint MUST keep serving, and the graceful shutdown on `SIGTERM` MUST still complete within its budget.

#### Scenario: the quotas are refreshed on the default interval
- **WHEN** the daemon is running and a provider declares a quota interface
- **THEN** the daemon refreshes that provider's quota every 60 seconds

#### Scenario: the refresh interval is configurable per provider
- **WHEN** a provider's quota configuration declares a refresh interval of 300 seconds
- **THEN** the daemon refreshes that provider's quota every 300 seconds and does not refresh it more often

#### Scenario: the daemon refreshes without waiting
- **WHEN** the daemon starts
- **THEN** it refreshes the quotas immediately rather than waiting for the first interval to elapse

#### Scenario: the first refresh after starting is not left empty
- **WHEN** the daemon starts and has not received any activity yet
- **THEN** it queries the quota pool of the provider recorded as active in that first refresh

#### Scenario: providers sharing a key are queried once
- **WHEN** two providers resolve to the same quota endpoint and the same API key
- **THEN** the daemon queries the quota interface once per cycle and records the result for both providers

#### Scenario: a recently recorded quota is not queried again
- **WHEN** the recorded quota of a provider was fetched less than that provider's refresh interval ago
- **THEN** the daemon does not query the quota interface for that provider in that cycle

#### Scenario: a failed refresh keeps the previous quota
- **WHEN** a refresh of a provider fails
- **THEN** the previously recorded quota of that provider is kept and the daemon keeps refreshing on schedule

#### Scenario: the refresh does not disturb the metrics endpoint
- **WHEN** the daemon is refreshing a quota
- **THEN** the metrics endpoint keeps serving and the daemon's graceful shutdown still completes within its budget

### Requirement: ccl serve records its log to a local file

`ccl serve` MUST write its log to a local file, because it is started without a terminal and a log that is discarded cannot be inspected when something goes wrong.

The log file MUST be written next to the other state of the launcher, MUST be readable only by the user, and MUST be kept from growing without bound.

Lines that report the quota refresh MUST carry a marker that consists only of ASCII characters, so that they can be selected even when the surrounding text cannot be displayed correctly.

#### Scenario: the log is written where it can be found
- **WHEN** the launcher starts the daemon on its own
- **THEN** the daemon writes its log to a file in the launcher's state directory

#### Scenario: quota lines can be selected without reading the text
- **WHEN** the daemon reports the outcome of a quota refresh
- **THEN** that line carries an ASCII marker that identifies it as a quota line

### Requirement: ccl serve accepts a provider activation request

`ccl serve` MUST accept a request that names a provider as in use for the running session, so that the quota of that provider can be refreshed without waiting for the session's first report.

The request MUST name the provider in the query of a `POST` to a dedicated path on the daemon's own HTTP port, and MUST be answered without disturbing the metrics endpoint.

The daemon MUST accept the request only when the name matches a provider that is configured, and MUST reject any other name without querying a quota interface.

The daemon MUST refresh the quota pool of an accepted provider in the same cycle, without waiting for the refresh interval to elapse.

The request MUST NOT be able to make the daemon query an endpoint that belongs to no configured provider.

#### Scenario: an activation request is accepted
- **WHEN** the daemon receives a request that names a configured provider
- **THEN** it accepts the request and treats that provider as in use

#### Scenario: an activation request refreshes the quota at once
- **WHEN** the daemon receives an activation request for a provider whose recorded quota is older than its refresh interval
- **THEN** the daemon queries that provider's quota pool in that cycle

#### Scenario: an unknown provider is not accepted
- **WHEN** the daemon receives a request that names no configured provider
- **THEN** it rejects the request and queries no quota interface

#### Scenario: the activation path does not disturb the metrics endpoint
- **WHEN** the daemon receives an activation request
- **THEN** the metrics endpoint keeps serving and the daemon's graceful shutdown still completes within its budget

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

