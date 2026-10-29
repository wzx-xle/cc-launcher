## MODIFIED Requirements

### Requirement: ccl delivers the OTel env to Claude Code through --settings
On every `ccl` invocation (before launching Claude Code), ccl MUST compute the OTel environment for the child process and deliver it through Claude Code's `--settings` parameter, in the same settings object that carries the launch context.

ccl MUST NOT write any OTel key into `~/.claude/settings.json`. The settings file applies to every Claude Code process on the machine; a takeover written there would also redirect the sessions that `ccl` did not launch, and would leave two generations of the user's configuration in the file (the live value and a `CC_ORIG_*` backup).

The `--settings` `env` block is merged key by key with the settings file's own `env` block, and the `--settings` block takes precedence. Therefore delivering a key overrides the settings file's value for that key, and NOT delivering a key leaves the settings file's value in effect.

ccl's usage statistics need **metrics only**. It MUST therefore take over **only the metrics signal**, and MUST leave the fallback endpoint — the "catch-all" configuration that logs and traces also travel through when no per-signal endpoint is set — alone.

The 6 keys and their actions:

| Key | The settings file has a user value | The settings file has no value |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | do not deliver the key | do not deliver the key |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | deliver `http://localhost:4318/v1/metrics` | deliver `http://localhost:4318/v1/metrics` |
| `OTEL_METRICS_EXPORTER` | do not deliver the key | deliver `otlp` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | do not deliver the key | deliver `http/protobuf` |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | do not deliver the key | deliver `1` |
| `OTEL_LOGS_EXPORTER` | do not deliver the key | do not deliver the key |

"Respecting the user's value" means NOT delivering that key: the settings file's value then applies unchanged, and the user's explicit opt-out (for example `OTEL_METRICS_EXPORTER=prometheus`) survives.

A "user value" is a value in the settings file's `env` block. The process environment does not count: ccl MUST deliver the default when the settings file has no value for the key, so that the process environment cannot silently change ccl's telemetry behavior.

ccl MUST NOT deliver `OTEL_LOGS_EXPORTER`. The daemon accepts logs without parsing or persisting them, and the local-privacy guarantee comes from the daemon's architecture, not from overriding the user's exporter choice.

ccl MUST NOT deliver the fallback endpoint `OTEL_EXPORTER_OTLP_ENDPOINT`, whether or not the user configured one. It is the catch-all endpoint: logs and traces travel through it when no per-signal endpoint is set, and the daemon answers `/v1/logs` and `/v1/traces` with HTTP 200 without parsing or forwarding them. Taking it over would therefore divert the user's logs and traces into the daemon and silently drop them, while ccl's statistics need none of them.

The metrics interception MUST NOT depend on taking over the fallback endpoint: the per-signal metrics endpoint is always delivered, and the OTel specification gives a per-signal endpoint precedence over the fallback endpoint. The metrics therefore reach the local daemon in every configuration, including one where the user configured a remote fallback endpoint.

The settings ccl delivers MUST contain only an `env` block; the settings file's own non-`env` settings (permissions, plugins, and the like) MUST remain in effect.

#### Scenario: the metrics endpoint is taken over without touching the settings file
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and the user runs `ccl`
- **THEN** the settings ccl delivers contain `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics`, the metrics are received by the local daemon, and `~/.claude/settings.json` still contains `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` with no `CC_ORIG_*` key

#### Scenario: ccl supplies the whole metrics configuration on its own
- **WHEN** `~/.claude/settings.json` contains no OTel key at all and the user runs `ccl`
- **THEN** the settings ccl delivers contain `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics`, `OTEL_METRICS_EXPORTER=otlp`, `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf` and `CLAUDE_CODE_ENABLE_TELEMETRY=1`, and the metrics reach the local daemon

#### Scenario: the fallback endpoint is left to the user
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and `OTEL_LOGS_EXPORTER=otlp` and the user runs `ccl`
- **THEN** the settings ccl delivers contain neither `OTEL_EXPORTER_OTLP_ENDPOINT` nor `OTEL_LOGS_EXPORTER`, and the logs reach `https://company-otel:4317` directly, without passing through the daemon

#### Scenario: the metrics reach the daemon although the fallback endpoint is not taken over
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and the user runs `ccl`
- **THEN** the metrics are received by the local daemon, because the per-signal metrics endpoint takes precedence over the fallback endpoint

#### Scenario: the user's exporter choice is respected
- **WHEN** `~/.claude/settings.json` has `OTEL_METRICS_EXPORTER=prometheus` and the user runs `ccl`
- **THEN** the settings ccl delivers do NOT contain `OTEL_METRICS_EXPORTER`, the settings file's value `prometheus` stays in effect, and no metrics are exported to the daemon (the user opted out)

#### Scenario: the user's own settings keys keep working
- **WHEN** `~/.claude/settings.json` has `OTEL_LOGS_EXPORTER=otlp` and the user runs `ccl`
- **THEN** the settings ccl delivers do NOT contain `OTEL_LOGS_EXPORTER`, and the logs still reach the endpoint the user configured

#### Scenario: the settings file's other settings are left alone
- **WHEN** `ccl` launches Claude Code
- **THEN** the settings ccl delivers contain only an `env` block, and the settings file's own non-`env` settings (such as permissions) remain in effect

#### Scenario: the settings file is not modified
- **WHEN** `ccl` launches Claude Code
- **THEN** the `env` block of `~/.claude/settings.json` contains no key that ccl added, and its file content is byte-identical to its content before the launch
