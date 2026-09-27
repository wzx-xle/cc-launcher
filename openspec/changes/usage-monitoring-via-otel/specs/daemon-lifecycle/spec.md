## ADDED Requirements

### Requirement: ccl auto-starts the daemon when port is free
On every `ccl` invocation, before exec'ing Claude Code, ccl MUST probe `localhost:4317` with a 200ms TCP connect timeout. If unreachable AND no daemon record exists, ccl MUST fork `ccl serve` as a detached child process and wait up to 3s for the port to become reachable.

#### Scenario: first ccl invocation in a session
- **WHEN** user runs `ccl` and `:4317` is unreachable
- **THEN** ccl forks `ccl serve` (detached), polls `:4317` every 100ms, exits the wait when port opens, then proceeds to exec Claude Code; total pre-exec latency ≤ 3s

#### Scenario: subsequent ccl invocation (daemon alive)
- **WHEN** user runs `ccl` and `:4317` is reachable
- **THEN** ccl skips fork, proceeds to exec Claude Code; pre-exec latency < 50ms

#### Scenario: daemon fails to bind (port stolen between probe and fork)
- **WHEN** ccl forks `ccl serve` but the new daemon exits within 3s with "port already in use"
- **THEN** ccl prints "OTel daemon failed to start, falling back" and proceeds to exec Claude Code without OTEL env (Claude Code won't push metrics this session but still runs)

### Requirement: ccl stop sends SIGTERM to daemon
`ccl stop` MUST find the running daemon (via pidfile at `~/.cc-launcher/daemon.pid`) and send SIGTERM. After SIGTERM, ccl MUST wait up to 5s for the process to exit; if still alive, send SIGKILL.

#### Scenario: stop kills daemon gracefully
- **WHEN** user runs `ccl stop` and daemon PID is in `daemon.pid`
- **THEN** SIGTERM is sent; daemon flushes SQLite within 5s and exits; ccl prints "daemon stopped"

#### Scenario: stop SIGKILLs hung daemon
- **WHEN** daemon does not exit within 5s after SIGTERM
- **THEN** ccl sends SIGKILL; daemon dies; pidfile removed

#### Scenario: stop when no daemon
- **WHEN** user runs `ccl stop` and `daemon.pid` does not exist
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
- **THEN** `ccl serve` overwrites the pidfile (no error); ccl stop finds the new PID

### Requirement: daemon refuses to start if port is occupied
`ccl serve` MUST attempt to bind `:4317` on startup. If bind fails with `EADDRINUSE` (or equivalent), it MUST log an actionable error and exit non-zero. It MUST NOT auto-pick a different port.

#### Scenario: port occupied by another OTel service
- **WHEN** `ccl serve` starts and `:4317` is held by, say, an OpenTelemetry Collector
- **THEN** daemon prints: `port 4317 already in use; either stop the conflicting service or set OTEL_EXPORTER_OTLP_ENDPOINT to a different port and re-run claude with that env var`; exit non-zero

#### Scenario: port free
- **WHEN** `ccl serve` starts and `:4317` is free
- **THEN** bind succeeds and the daemon accepts OTLP traffic