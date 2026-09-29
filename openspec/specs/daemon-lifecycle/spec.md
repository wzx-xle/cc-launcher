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
- **THEN** stdout shows a `daemon` row with `pid=N` and `port :4318 reachable` indented under `daemon  running`; plus a `db` row with the db path, size, and session count on three indented sub-lines

#### Scenario: status when no daemon but port is held by foreign service
- **WHEN** user runs `ccl serve --status` and `daemon.pid` does NOT exist but `:4318` is reachable
- **THEN** stdout shows `daemon  not running` with `port :4318 in use by foreign service (no pidfile)` indented under it; plus a `db` row

#### Scenario: status when stale pidfile (crashed daemon)
- **WHEN** user runs `ccl serve --status` and `daemon.pid` exists but `:4318` is unreachable
- **THEN** stdout shows `daemon  not running` with `stale pidfile pid=N` and `port :4318 unreachable` indented under it; plus a `db` row

#### Scenario: status when nothing is running
- **WHEN** user runs `ccl serve --status` and `daemon.pid` does NOT exist and `:4318` is unreachable
- **THEN** stdout shows `daemon  not running` with `port :4318 unreachable` indented under it; plus a `db` row

#### Scenario: status when db file is missing
- **WHEN** user runs `ccl serve --status` and `~/.cc-launcher/telemetry.db` does NOT exist
- **THEN** stdout shows the daemon portion as above and the db row as a single `db      missing (no telemetry yet)` line; exit code is 0

#### Scenario: status is mutually exclusive with stop and restore
- **WHEN** user runs `ccl serve --status --stop` (or any combination with --restore)
- **THEN** exit code is non-zero and stderr explains the conflict