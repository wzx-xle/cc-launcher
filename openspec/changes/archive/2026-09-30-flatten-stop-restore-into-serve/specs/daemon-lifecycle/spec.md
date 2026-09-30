## MODIFIED Requirements

### Requirement: ccl serve --stop sends SIGTERM to daemon
`ccl serve --stop` MUST find the running daemon (via pidfile at `~/.cc-launcher/daemon.pid`) and send SIGTERM. **On Unix**, the signal MUST be sent to the daemon's PGID (negative PID) so it does not accidentally signal Claude Code or its child processes. After SIGTERM, ccl MUST wait up to 5s for the process to exit; if still alive, send SIGKILL.

#### Scenario: serve --stop kills daemon gracefully (Unix)
- **WHEN** user runs `ccl serve --stop` on Unix and daemon PID is in `daemon.pid`
- **THEN** SIGTERM is sent to the daemon PGID (`kill -PID`); daemon flushes SQLite within 5s and exits; ccl prints "daemon stopped"

#### Scenario: serve --stop kills daemon gracefully (Windows)
- **WHEN** user runs `ccl serve --stop` on Windows and daemon PID is in `daemon.pid`
- **THEN** SIGTERM-equivalent (Windows process termination) is sent directly to the daemon PID (Windows has no PGID concept); daemon exits

#### Scenario: serve --stop SIGKILLs hung daemon
- **WHEN** daemon does not exit within 5s after SIGTERM (or Windows equivalent) and user invoked `ccl serve --stop`
- **THEN** ccl sends SIGKILL; daemon dies; pidfile removed

#### Scenario: serve --stop when no daemon
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
- **THEN** `ccl serve` overwrites the pidfile (no error); `ccl serve --stop` finds the new PID

## ADDED Requirements

### Requirement: ccl serve --status reports daemon runtime status
`ccl serve --status` MUST print a human-readable status summary and exit 0 (status reporting is informational; missing connections are NOT errors). The status MUST include:
- Whether the daemon is currently running (judged by port `:4318` reachability AND the pidfile at `~/.cc-launcher/daemon.pid` — see matrix below)
- The daemon's PID when running
- The SQLite db file path and size at `~/.cc-launcher/telemetry.db`; if the db file is missing, MUST print "missing" rather than erroring

#### Scenario: status when daemon is running
- **WHEN** user runs `ccl serve --status` and `daemon.pid` exists with PID N and `:4318` is reachable
- **THEN** stdout shows `daemon: running (pid=N, port :4318 reachable)` plus a `db:` line with the db path and size

#### Scenario: status when no daemon but port is held by foreign service
- **WHEN** user runs `ccl serve --status` and `daemon.pid` does NOT exist but `:4318` is reachable
- **THEN** stdout shows `daemon: not running` and `port :4318: in use by foreign service (no pidfile)` plus a `db:` line

#### Scenario: status when stale pidfile (crashed daemon)
- **WHEN** user runs `ccl serve --status` and `daemon.pid` exists but `:4318` is unreachable
- **THEN** stdout shows `daemon: not running` and `stale pidfile: pid=N (port unreachable)` plus a `db:` line

#### Scenario: status when nothing is running
- **WHEN** user runs `ccl serve --status` and `daemon.pid` does NOT exist and `:4318` is unreachable
- **THEN** stdout shows `daemon: not running` and `port :4318: unreachable` plus a `db:` line

#### Scenario: status when db file is missing
- **WHEN** user runs `ccl serve --status` and `~/.cc-launcher/telemetry.db` does NOT exist
- **THEN** stdout shows the daemon portion as above and the db line as `db: missing (no telemetry yet)`; exit code is 0

#### Scenario: status is mutually exclusive with stop and restore
- **WHEN** user runs `ccl serve --status --stop` (or any combination with --restore)
- **THEN** exit code is non-zero and stderr explains the conflict