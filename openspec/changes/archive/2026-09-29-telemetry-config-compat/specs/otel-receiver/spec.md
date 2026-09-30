## MODIFIED Requirements

### Requirement: ccl-launcher accepts OTLP HTTP on :4318; metrics persist + forward, logs/traces return 200 only
A `ccl serve` mode MUST start an HTTP server on `localhost:4318` (standard OTLP HTTP port) that accepts three OTel endpoints:
- `POST /v1/metrics` — MUST parse protobuf and persist to local SQLite; MUST also forward to remote collector (if forwarder enabled)
- `POST /v1/logs` — MUST return HTTP 200; MUST NOT parse, persist, or forward. Logs collection is the user's own collector's responsibility — when user configures `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`, Claude Code SDK sends logs directly to that remote endpoint, bypassing the daemon entirely.
- `POST /v1/traces` — same as logs: HTTP 200 only, no persistence, no forwarding.

The forwarder (if enabled) MUST only target `/v1/metrics` — logs and traces handlers never invoke the forwarder. Local SQLite NEVER contains log or trace content — privacy is preserved by the daemon's architecture (don't parse/persist logs/traces), not by overriding Claude Code's exporter config.

The server MUST NOT block on individual slow writes or forward calls.

#### Scenario: claude code pushes metrics successfully
- **WHEN** Claude Code (with `CLAUDE_CODE_ENABLE_TELEMETRY=1` and `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics` or fallback `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`) sends an OTLP metrics ExportRequest
- **THEN** the daemon receives the export, parses the protobuf, persists each metric to SQLite, and returns HTTP 200 to Claude Code

#### Scenario: metric is forwarded to remote collector
- **WHEN** daemon has `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=https://company-otel:4318/v1/metrics` and `CC_FORWARD_AUTH_HEADER="Authorization=Bearer t"`
- **THEN** for each parsed metric, daemon POSTs the original protobuf bytes (or re-encoded metric payload) to the remote URL with the auth header; failures are logged and dropped

#### Scenario: claude code pushes logs; daemon returns 200 only
- **WHEN** Claude Code sends an OTLP logs ExportRequest to `:4318/v1/logs` (because fallback `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` and no per-signal logs endpoint is configured, OR because user has per-signal logs endpoint that has been redirected to localhost by ccl injection — which ccl does NOT do, so this only happens via fallback)
- **THEN** daemon accepts the request (HTTP 200) with NO persistence and NO forwarding; SQLite has NO new log entries; forwarder is NOT invoked

#### Scenario: claude code pushes traces; daemon returns 200 only
- **WHEN** Claude Code sends an OTLP traces ExportRequest to `:4318/v1/traces`
- **THEN** daemon accepts the request (HTTP 200) with NO persistence and NO forwarding; SQLite has NO new trace entries; forwarder is NOT invoked

#### Scenario: server binds when port is free
- **WHEN** `ccl serve` starts and `:4318` is not in use
- **THEN** the server binds successfully; the daemon begins accepting OTLP traffic

#### Scenario: server fails fast when port is occupied
- **WHEN** `ccl serve` starts and `:4318` is already bound by another process
- **THEN** the daemon prints an error mentioning `OTEL_EXPORTER_OTLP_ENDPOINT` override and exits non-zero; Claude Code's metrics are NOT silently redirected to another service

#### Scenario: server continues running across multiple Claude Code sessions
- **WHEN** the first `ccl` invocation forks `ccl serve`, the user runs `ccl` again 30 minutes later
- **THEN** the second invocation sees the daemon already alive and skips fork; both sessions' metrics land in the same SQLite file

#### Scenario: user has no forwarder configured
- **WHEN** daemon has neither `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` nor `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` env set
- **THEN** metrics are persisted to SQLite (no forwarding); logs/traces POSTs get HTTP 200 but no further action

## ADDED Requirements

### Requirement: forwarder priority — FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT takes precedence over CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT
When deciding whether to enable the metrics forwarder, the daemon MUST evaluate env vars in this priority order:
1. If `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` is set AND is not `localhost` / `127.0.0.1`, enable forwarder targeting that URL (suffixed with `/v1/metrics` if not already).
2. Else if `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` is set AND is not `localhost` / `127.0.0.1`, enable forwarder targeting that URL (suffixed with `/v1/metrics`).
3. Else forwarder is disabled.

This priority ensures users with per-signal metrics endpoint have their explicit backup used; users with only fallback endpoint still get metrics forwarded (legacy behavior).

#### Scenario: user has per-signal metrics — FORWORD_ takes priority
- **WHEN** daemon has both `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=https://primary:4318/v1/metrics` and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://fallback:4318`
- **THEN** forwarder targets `https://primary:4318/v1/metrics` (the per-signal backup wins)

#### Scenario: user has only fallback — CC_ORIG_ still works
- **WHEN** daemon has only `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://legacy:4318` (no per-signal metrics backup)
- **THEN** forwarder targets `https://legacy:4318/v1/metrics` (fallback trigger maintained)

#### Scenario: FORWORD_ points to localhost — skip and use CC_ORIG_
- **WHEN** daemon has `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics` and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://remote:4318`
- **THEN** primary is skipped (loop prevention); forwarder targets `https://remote:4318/v1/metrics` via fallback trigger