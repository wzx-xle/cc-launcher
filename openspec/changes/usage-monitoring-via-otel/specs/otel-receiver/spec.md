## ADDED Requirements

### Requirement: ccl-launcher accepts OTLP gRPC on :4317
A `ccl serve` mode MUST start a gRPC server on `localhost:4317` (standard OTLP port) that accepts metrics, logs, and traces from Claude Code. The server MUST NOT block on individual slow writes; a slow consumer MUST NOT cause the gRPC server to stall.

#### Scenario: claude code pushes metrics successfully
- **WHEN** Claude Code (with `CLAUDE_CODE_ENABLE_TELEMETRY=1` and `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317`) sends a metrics export
- **THEN** the daemon receives the export, parses it, persists each metric to SQLite, and returns success to Claude Code

#### Scenario: server binds when port is free
- **WHEN** `ccl serve` starts and `:4317` is not in use
- **THEN** the server binds successfully; `ccl stats` becomes queryable via HTTP :4319

#### Scenario: server fails fast when port is occupied
- **WHEN** `ccl serve` starts and `:4317` is already bound by another process
- **THEN** the daemon prints an error mentioning `OTEL_EXPORTER_OTLP_ENDPOINT` override and exits non-zero; Claude Code's metrics are NOT silently redirected to another service

#### Scenario: server continues running across multiple Claude Code sessions
- **WHEN** the first `ccl` invocation forks `ccl serve`, the user runs `ccl` again 30 minutes later
- **THEN** the second invocation sees the daemon already alive and skips fork; both sessions' metrics land in the same SQLite file

### Requirement: parser maps OTLP proto to domain objects
The OTLP parser MUST extract for each metric: name, value, labels (resource + scope + data-point attributes), timestamp. For each log: timestamp, severity, body, attributes. For each trace: trace-id, span-id, parent-span-id, name, attributes.

#### Scenario: claude_code.session.count metric is extracted
- **WHEN** an OTLP export includes `claude_code.session.count` with label `provider=deepseek`
- **THEN** the parser produces one `metrics` row with `name="claude_code.session.count"`, `value=1`, labels `{"provider":"deepseek"}`, captured_at=timestamp

#### Scenario: input_tokens metric accumulates value
- **WHEN** multiple OTLP data points arrive with the same metric name + labels
- **THEN** each is stored as a separate row (no aggregation at parse time; aggregation happens at query time)

### Requirement: parser handles malformed exports without crashing
A malformed OTLP export MUST be logged with the error and the connection reset; the daemon MUST continue accepting subsequent exports.

#### Scenario: bad protobuf bytes do not kill the daemon
- **WHEN** a client sends invalid bytes to `:4317`
- **THEN** the daemon logs the error and continues accepting new connections

### Requirement: sub-process env-isolation is preserved
The daemon MUST NOT pass `OTEL_*` env vars to its child processes (Claude Code via ccl, or any subprocess). This mirrors Claude Code's own behavior per the OTel docs.

#### Scenario: child processes do not inherit OTEL env
- **WHEN** daemon forks a child (e.g., for backup, log rotation, or external command execution)
- **THEN** `os.Environ()` of the child does NOT contain `OTEL_EXPORTER_OTLP_ENDPOINT` or other OTEL_* variables