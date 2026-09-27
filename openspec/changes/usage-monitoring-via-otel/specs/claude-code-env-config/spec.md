## ADDED Requirements

### Requirement: ccl injects OTEL env into Claude Code settings with backup
On every `ccl` invocation (before fork-if-needed and exec Claude Code), ccl MUST inspect `~/.claude/settings.json` and ensure the `env` map contains the 5 OTel keys. For each key that already has a user value, ccl MUST first save a backup to a `CC_ORIG_<KEY>` key, then apply the action below. For missing keys, ccl MUST add the default without a backup.

The 5 keys and actions:

| Key | If user has value | If missing |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | backup to `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`; override to `http://localhost:4318` | add `http://localhost:4318` |
| `OTEL_LOGS_EXPORTER` | backup to `CC_ORIG_OTEL_LOGS_EXPORTER`; **respect user's value** | add `otlp` (default; user can edit) |
| `OTEL_METRICS_EXPORTER` | backup to `CC_ORIG_OTEL_METRICS_EXPORTER`; respect user's value | add `otlp` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | backup to `CC_ORIG_OTEL_EXPORTER_OTLP_PROTOCOL`; respect user's value | add `http/protobuf` |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | backup to `CC_ORIG_CLAUDE_CODE_ENABLE_TELEMETRY`; respect user's value | add `1` |

**Note**: ccl does NOT force-override `OTEL_LOGS_EXPORTER`. The local-privacy guarantee (no prompts in SQLite) comes from the daemon's architecture (it accepts /v1/logs but only forwards, never persists). User's original `OTEL_LOGS_EXPORTER=otlp` continues to work — logs reach the remote collector via forwarding.

#### Scenario: settings.json has only OTel endpoint (typical user)
- **WHEN** `~/.claude/settings.json` has only `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`
- **THEN** after ccl:
  - `OTEL_EXPORTER_OTLP_ENDPOINT` = `http://localhost:4318`
  - `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` = `https://company-otel:4317`
  - `OTEL_LOGS_EXPORTER` = `none` (newly added, no backup)
  - `OTEL_METRICS_EXPORTER` = `otlp` (newly added)
  - `OTEL_EXPORTER_OTLP_PROTOCOL` = `http/protobuf` (newly added)
  - `CLAUDE_CODE_ENABLE_TELEMETRY` = `1` (newly added)

#### Scenario: settings.json already has all 5 keys
- **WHEN** user has manually configured every OTel key
- **THEN** ccl backs up all 5 to `CC_ORIG_*` (only first time; if backups already present, don't overwrite); ccl force-overrides only `OTEL_LOGS_EXPORTER` to `none`; other 4 are preserved

#### Scenario: settings.json already has CC_ORIG_* from prior ccl run
- **WHEN** user runs `ccl` again and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` already exists
- **THEN** ccl does NOT overwrite the backup (preserves the original saved value); only adds missing keys

#### Scenario: user has OTEL_LOGS_EXPORTER=otlp (logs enabled) — ccl respects
- **WHEN** user has `OTEL_LOGS_EXPORTER=otlp`
- **THEN** ccl saves `CC_ORIG_OTEL_LOGS_EXPORTER=otlp` and **respects the value** (does NOT force to `none`); Claude Code pushes logs to wherever its OTEL_EXPORTER_OTLP_ENDPOINT points; if forwarding is configured, daemon forwards logs to backup endpoint

#### Scenario: user sets OTEL_METRICS_EXPORTER to a non-otlp value (e.g. prometheus)
- **WHEN** user has `OTEL_METRICS_EXPORTER=prometheus`
- **THEN** ccl backs up as `CC_ORIG_OTEL_METRICS_EXPORTER=prometheus` and respects the value; Claude Code will use prometheus, ccl stats will be empty (user opted out)

#### Scenario: env value points to a remote collector
- **WHEN** user has `OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example.com:4318`
- **THEN** ccl backs it up to `CC_ORIG_*`; daemon sees the backup at startup and **enables forwarder** to that URL; metrics flow: Claude Code → daemon → remote collector (and locally to SQLite)

#### Scenario: existing settings.json field order is preserved
- **WHEN** ccl writes new keys to an existing settings.json with keys in order `[env, permissions, enabledPlugins, ...]`
- **THEN** after write, the file still has the same key order with new keys appended in the env section, not inserted at the top of the file (ccl uses `internal/config/ordered.go` to preserve order)

### Requirement: ccl restore removes all CC_ORIG_* and ccl-injected defaults
`ccl restore` MUST read `~/.claude/settings.json` env field and:
- For each `CC_ORIG_<KEY>` found: write `<KEY>` = `CC_ORIG_<KEY>` value; delete the `CC_ORIG_*` key
- For each of the 5 OTel keys ccl would have injected as default: delete them if no backup exists (i.e., user never had them — they're purely ccl artifacts)
- The settings.json file structure MUST be preserved otherwise

#### Scenario: restore recovers original user config
- **WHEN** user runs `ccl restore` and settings.json has `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` (along with ccl's override to localhost:4318)
- **THEN** settings.json ends up with `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and no `CC_ORIG_*` keys

#### Scenario: restore removes keys ccl added (no original)
- **WHEN** user runs `ccl restore` and settings.json has `OTEL_METRICS_EXPORTER=otlp` (ccl added it; user never had a value)
- **THEN** the key is removed (since no `CC_ORIG_OTEL_METRICS_EXPORTER` exists)

#### Scenario: restore is idempotent
- **WHEN** user runs `ccl restore` twice
- **THEN** the second run is a no-op; settings.json is unchanged

#### Scenario: restore keeps user-added non-ccl keys
- **WHEN** user has `ANTHROPIC_BASE_URL=https://...` (a ccl-doesn't-touch key)
- **THEN** `ccl restore` does not modify it

#### Scenario: settings.json does not exist
- **WHEN** user runs `ccl restore` and `~/.claude/settings.json` is missing
- **THEN** ccl prints "no settings to restore" and exits 0

### Requirement: daemon auto-enables forwarder based on CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT
On `ccl serve` startup, the daemon MUST inspect its own env for `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`. If the value is set AND is not `http://localhost:4318` (or 127.0.0.1), the daemon MUST enable a forwarder that POSTs each parsed metric to that URL after persisting locally.

#### Scenario: user has remote OTLP configured
- **WHEN** daemon starts and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`
- **THEN** daemon enables forwarder; each metric received via /v1/metrics is: (1) written to local SQLite (sync), (2) POSTed to remote in a goroutine (async, fire-and-forget)

#### Scenario: forwarder detects localhost (loop prevention)
- **WHEN** daemon starts and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` (or 127.0.0.1)
- **THEN** daemon does NOT enable forwarder; logs at startup "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT points to localhost; forwarder disabled to prevent loop"

#### Scenario: no backup env present
- **WHEN** daemon starts and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` is unset
- **THEN** daemon does NOT enable forwarder; no log emitted

### Requirement: forwarder auth comes from CC_FORWARD_AUTH_HEADER env
The forwarder MUST check env `CC_FORWARD_AUTH_HEADER`. If set, every POST request MUST include it as a header. If unset, no auth header is sent. The daemon MUST NOT reuse `ANTHROPIC_AUTH_TOKEN` for forwarder auth.

#### Scenario: user sets CC_FORWARD_AUTH_HEADER
- **WHEN** env has `CC_FORWARD_AUTH_HEADER="Authorization=Bearer my-token"`
- **THEN** forwarder POSTs include `Authorization: Bearer my-token`

#### Scenario: user does not set auth
- **WHEN** env lacks `CC_FORWARD_AUTH_HEADER`
- **THEN** forwarder POSTs have no auth header

#### Scenario: forwarder error log does not echo auth
- **WHEN** forwarder POST returns non-2xx
- **THEN** daemon logs stderr `forwarder: post to <url> failed: status=401` — auth header value is NOT included

### Requirement: forwarder failures are logged and dropped
Forwarder failures (non-2xx response, timeout > 5s, connection error) MUST be logged to stderr in one line per failure and the metric MUST be dropped from the forwarder pipeline. The local SQLite write MUST have already succeeded before the failure.

#### Scenario: forwarder times out
- **WHEN** remote collector does not respond within 5s
- **THEN** daemon logs `forwarder: post to <url> timeout after 5s`; metric is already in local SQLite

#### Scenario: forwarder returns 5xx
- **WHEN** remote returns 503
- **THEN** daemon logs `forwarder: post to <url> status=503`; metric is already in local SQLite

#### Scenario: forwarder succeeds
- **WHEN** remote returns 200
- **THEN** daemon logs nothing for this metric (success); local SQLite has it

#### Scenario: forwarder does not block daemon
- **WHEN** forwarder is slow (but not timed out)
- **THEN** daemon accepts new OTLP requests at full speed; local SQLite writes proceed normally