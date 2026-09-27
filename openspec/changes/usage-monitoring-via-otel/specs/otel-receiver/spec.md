## ADDED Requirements

### Requirement: ccl-launcher accepts OTLP HTTP metrics on :4318
A `ccl serve` mode MUST start an HTTP server on `localhost:4318` (standard OTLP HTTP port) that accepts the OTLP `/v1/metrics` protobuf endpoint. Logs (`/v1/logs`) and traces (`/v1/traces`) MUST return HTTP 405/404 — they are explicitly out of scope. The server MUST NOT block on individual slow writes; a slow consumer MUST NOT cause the HTTP server to stall.

#### Scenario: claude code pushes metrics successfully
- **WHEN** Claude Code (with `CLAUDE_CODE_ENABLE_TELEMETRY=1` and `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`) sends an OTLP metrics ExportRequest
- **THEN** the daemon receives the export, parses the protobuf, persists each metric to SQLite, and returns HTTP 200 to Claude Code

#### Scenario: server binds when port is free
- **WHEN** `ccl serve` starts and `:4318` is not in use
- **THEN** the server binds successfully; the daemon begins accepting OTLP traffic

#### Scenario: server fails fast when port is occupied
- **WHEN** `ccl serve` starts and `:4318` is already bound by another process
- **THEN** the daemon prints an error mentioning `OTEL_EXPORTER_OTLP_ENDPOINT` override and exits non-zero; Claude Code's metrics are NOT silently redirected to another service

#### Scenario: server continues running across multiple Claude Code sessions
- **WHEN** the first `ccl` invocation forks `ccl serve`, the user runs `ccl` again 30 minutes later
- **THEN** the second invocation sees the daemon already alive and skips fork; both sessions' metrics land in the same SQLite file

#### Scenario: server rejects logs endpoint
- **WHEN** Claude Code (or anyone) sends an OTLP logs ExportRequest to `:4318/v1/logs`
- **THEN** the daemon returns HTTP 404 (or 405) without persisting anything; this prevents prompt content from accidentally landing in local SQLite

#### Scenario: server rejects traces endpoint
- **WHEN** Claude Code (or anyone) sends an OTLP traces ExportRequest to `:4318/v1/traces`
- **THEN** the daemon returns HTTP 404 (or 405) without persisting anything

### Requirement: parser maps OTLP protobuf metrics to domain objects
The OTLP parser MUST extract for each metric: name, value, labels (resource + scope + data-point attributes), timestamp. Log and trace payloads are not parsed (see scenario above).

#### Scenario: claude_code.session.count metric is extracted
- **WHEN** an OTLP export includes `claude_code.session.count` with label `provider=deepseek`
- **THEN** the parser produces one `metrics` row with `name="claude_code.session.count"`, `value=1`, labels `{"provider":"deepseek"}`, captured_at=timestamp

#### Scenario: input_tokens metric accumulates value
- **WHEN** multiple OTLP data points arrive with the same metric name + labels
- **THEN** each is stored as a separate row (no aggregation at parse time; aggregation happens at query time)

#### Scenario: parser extracts resource attribute as session metadata
- **WHEN** an OTLP export includes resource attributes (`service.name`, `service.version`) and session metadata (`session.id`, `cwd`)
- **THEN** the parser records these in the `sessions` row for that session_id; later metrics with the same session_id link to that row

### Requirement: parser handles malformed exports without crashing
A malformed OTLP metrics export MUST be logged with the error and the connection closed; the daemon MUST continue accepting subsequent exports.

#### Scenario: bad protobuf bytes do not kill the daemon
- **WHEN** a client sends invalid bytes to `:4318/v1/metrics`
- **THEN** the daemon logs the error and continues accepting new requests

### Requirement: sub-process env-isolation is preserved
The daemon MUST NOT pass `OTEL_*` env vars to its child processes (e.g., for backup, log rotation, or any subprocess). This mirrors Claude Code's own behavior per the OTel docs.

#### Scenario: child processes do not inherit OTEL env
- **WHEN** daemon forks a child (e.g., for SQLite VACUUM, log rotation)
- **THEN** `os.Environ()` of the child does NOT contain `OTEL_EXPORTER_OTLP_ENDPOINT` or other `OTEL_*` variables