## ADDED Requirements

### Requirement: ccl injects OTEL env into Claude Code settings
On every `ccl` invocation (before fork-if-needed and exec Claude Code), ccl MUST inspect `~/.claude/settings.json` and ensure the `env` map contains:

- `CLAUDE_CODE_ENABLE_TELEMETRY=1`
- `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`
- `OTEL_METRICS_EXPORTER=otlp`
- `OTEL_LOGS_EXPORTER=none` (**explicit disable** to prevent accidental prompt logging)
- `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`

If any key is missing, ccl MUST write the missing keys (without disturbing existing keys). If `~/.claude/settings.json` does not exist, ccl MUST create it with the required keys.

#### Scenario: settings.json exists with some env keys
- **WHEN** user runs `ccl` and `~/.claude/settings.json` has `env` with `ANTHROPIC_BASE_URL` but no `CLAUDE_CODE_ENABLE_TELEMETRY`
- **THEN** ccl adds `CLAUDE_CODE_ENABLE_TELEMETRY=1` and the OTEL_* keys to the existing `env` map; `ANTHROPIC_BASE_URL` is preserved

#### Scenario: settings.json exists with all required keys
- **WHEN** user runs `ccl` and `~/.claude/settings.json` already has all required env keys
- **THEN** ccl does NOT modify the file

#### Scenario: settings.json does not exist
- **WHEN** user runs `ccl` and `~/.claude/settings.json` is missing
- **THEN** ccl creates the file with `{"env": {...required keys...}}` and the daemon config section; the existing settings.json schema (env + permissions + ...) is respected

#### Scenario: env value points to a non-default port
- **WHEN** user has manually set `OTEL_EXPORTER_OTLP_ENDPOINT=http://other:9999` and runs `ccl`
- **THEN** ccl does NOT overwrite that key (respects manual override); the docstring/help tells users to remove the override to use the local daemon

#### Scenario: OTEL_LOGS_EXPORTER is always set to none
- **WHEN** user runs `ccl` and `OTEL_LOGS_EXPORTER` is missing from settings.json
- **THEN** ccl sets `OTEL_LOGS_EXPORTER=none` regardless of any other config; this is the privacy default (decision 8)

### Requirement: existing settings.json field order is preserved
ccl MUST use the ordered JSON read/write from `internal/config/ordered.go` so the existing key ordering is not disrupted when ccl adds new keys.

#### Scenario: order preserved after injection
- **WHEN** ccl writes `CLAUDE_CODE_ENABLE_TELEMETRY` to an existing settings.json with keys in order `[env, permissions, enabledPlugins, ...]`
- **THEN** after write, the file still has the same key order with `CLAUDE_CODE_ENABLE_TELEMETRY` added in the env section, not appended to the end of the file

### Requirement: ccl auto-start respects OTEL env override
If `CLAUDE_CODE_ENABLE_TELEMETRY=1` is missing but the user explicitly set `OTEL_EXPORTER_OTLP_ENDPOINT` to a non-localhost value, ccl MUST NOT auto-start the daemon (the user wants remote push, no local sink needed).

#### Scenario: user points to a remote OTel collector
- **WHEN** user runs `ccl` with `~/.claude/settings.json` having `OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example.com:4318`
- **THEN** ccl adds `CLAUDE_CODE_ENABLE_TELEMETRY=1` and the OTEL_* keys but does NOT fork a daemon (Claude Code will push to the user's remote collector, not localhost)