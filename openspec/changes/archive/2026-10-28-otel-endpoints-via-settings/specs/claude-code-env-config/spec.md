## ADDED Requirements

### Requirement: ccl delivers the OTel env to Claude Code through --settings
On every `ccl` invocation (before launching Claude Code), ccl MUST compute the OTel environment for the child process and deliver it through Claude Code's `--settings` parameter, in the same settings object that carries the launch context.

ccl MUST NOT write any OTel key into `~/.claude/settings.json`. The settings file applies to every Claude Code process on the machine; a takeover written there would also redirect the sessions that `ccl` did not launch, and would leave two generations of the user's configuration in the file (the live value and a `CC_ORIG_*` backup).

The `--settings` `env` block is merged key by key with the settings file's own `env` block, and the `--settings` block takes precedence. Therefore delivering a key overrides the settings file's value for that key, and NOT delivering a key leaves the settings file's value in effect.

The 6 keys and their actions:

| Key | The settings file has a user value | The settings file has no value |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | deliver `http://localhost:4318` | deliver `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | deliver `http://localhost:4318/v1/metrics` | deliver `http://localhost:4318/v1/metrics` |
| `OTEL_METRICS_EXPORTER` | do not deliver the key | deliver `otlp` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | do not deliver the key | deliver `http/protobuf` |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | do not deliver the key | deliver `1` |
| `OTEL_LOGS_EXPORTER` | do not deliver the key | do not deliver the key |

"Respecting the user's value" means NOT delivering that key: the settings file's value then applies unchanged, and the user's explicit opt-out (for example `OTEL_METRICS_EXPORTER=prometheus`) survives.

A "user value" is a value in the settings file's `env` block. The process environment does not count: ccl MUST deliver the default when the settings file has no value for the key, so that the process environment cannot silently change ccl's telemetry behavior.

ccl MUST NOT deliver `OTEL_LOGS_EXPORTER`. The daemon accepts logs without parsing or persisting them, and the local-privacy guarantee comes from the daemon's architecture, not from overriding the user's exporter choice.

The settings ccl delivers MUST contain only an `env` block; the settings file's own non-`env` settings (permissions, plugins, and the like) MUST remain in effect.

#### Scenario: the endpoint is taken over without touching the settings file
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and the user runs `ccl`
- **THEN** the settings ccl delivers contain `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`, the metrics are received by the local daemon, and `~/.claude/settings.json` still contains `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` with no `CC_ORIG_*` key

#### Scenario: ccl supplies the whole OTel configuration on its own
- **WHEN** `~/.claude/settings.json` contains no OTel key at all and the user runs `ccl`
- **THEN** the settings ccl delivers contain `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics`, `OTEL_METRICS_EXPORTER=otlp`, `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf` and `CLAUDE_CODE_ENABLE_TELEMETRY=1`, and the metrics reach the local daemon

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

### Requirement: ccl delivers the forwarding target to the daemon through its process environment
The daemon MUST forward the metrics it receives to the user's own remote collector. ccl MUST obtain the forwarding target from the user's own endpoint configuration in `~/.claude/settings.json` and MUST hand it to the daemon through the daemon's process environment, by setting these variables in ccl's own process environment before the daemon is forked:

- `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` — the user's own fallback endpoint
- `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` — the user's own per-signal metrics endpoint

ccl MUST NOT write these variables into `~/.claude/settings.json`. They describe the daemon's destination, not the child process's configuration.

When the settings file still contains a legacy `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` key (left by an earlier version of ccl, which had overwritten the live key), that value MUST be used as the user's fallback endpoint, in preference to the live key — the live key was already overwritten by that earlier version.

A missing endpoint MUST leave the corresponding variable unset. ccl MUST NOT substitute a placeholder value.

#### Scenario: the user's remote collector still receives the metrics
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and the user runs `ccl`
- **THEN** the daemon receives the metrics via `http://localhost:4318` and forwards them to `https://company-otel:4317`

#### Scenario: the per-signal endpoint wins over the fallback endpoint
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=https://fallback:4317` and `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=https://metrics:4318`
- **THEN** the daemon forwards the metrics to `https://metrics:4318`

#### Scenario: no user endpoint means no forwarding
- **WHEN** `~/.claude/settings.json` contains no OTel endpoint and the user runs `ccl`
- **THEN** neither forwarding variable is set, and the daemon does not forward

#### Scenario: a legacy backup key is preferred over the overwritten live key
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`
- **THEN** the daemon forwards the metrics to `https://company-otel:4317`

### Requirement: ccl cleans up legacy ccl artifacts from settings.json once
ccl MUST NOT write to `~/.claude/settings.json` in its normal launch flow. A version of ccl that did write to it may have left artifacts behind: `CC_ORIG_*` and `FORWORD_*` backup keys, and OTel keys holding ccl's own values.

On startup, ccl MUST run a one-time cleanup that removes those artifacts and restores the user's own values:

- For each `CC_ORIG_<KEY>` or `FORWORD_<KEY>`: write the backup value back to `<KEY>` and delete the backup key — unless the backup value equals ccl's own default for that key, in which case the backup is ccl's own earlier injection of its default and the live key MUST be deleted instead.
- For each managed OTel key that has no backup: delete it when its value equals ccl's own default for that key.
- Every other key MUST be left untouched.

The cleanup MUST only run when the settings file shows a ccl artifact: a `CC_ORIG_*` / `FORWORD_*` key, or a live `OTEL_EXPORTER_OTLP_ENDPOINT` equal to `http://localhost:4318`. Without such an artifact, ccl MUST NOT modify the settings file at all.

The cleanup MUST run at most once. ccl MUST record that it has run, and MUST NOT run it again on later startups — the rules above compare values against ccl's defaults, and re-running them would delete values the user wrote after the cleanup.

Before modifying the settings file, the cleanup MUST take a file-level snapshot through `config.Manager.Backup()`. A failure to take the snapshot MUST NOT block the launch: ccl MUST print a warning and continue.

The cleanup MUST be idempotent: running it again when there is nothing left to clean MUST leave the settings file unchanged.

#### Scenario: the user's endpoint is restored
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`
- **THEN** after the cleanup the settings file has `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and no `CC_ORIG_*` key

#### Scenario: a key ccl added without a backup is removed
- **WHEN** `~/.claude/settings.json` has `OTEL_METRICS_EXPORTER=otlp` and no `CC_ORIG_OTEL_METRICS_EXPORTER`
- **THEN** after the cleanup the key `OTEL_METRICS_EXPORTER` is removed

#### Scenario: a self-backup does not restore ccl's own default
- **WHEN** `~/.claude/settings.json` has `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` (the backup of ccl's own injection)
- **THEN** after the cleanup the key `OTEL_EXPORTER_OTLP_ENDPOINT` and the backup key are both removed

#### Scenario: a settings file without ccl artifacts is not touched
- **WHEN** `~/.claude/settings.json` has `OTEL_METRICS_EXPORTER=otlp` and no `CC_ORIG_*` / `FORWORD_*` key and no `http://localhost:4318` endpoint
- **THEN** the cleanup does not run and the settings file is unchanged

#### Scenario: the cleanup runs only once
- **WHEN** the user runs `ccl` twice
- **THEN** the cleanup modifies the settings file on the first run only, and the second run leaves it unchanged

#### Scenario: the user's own keys survive
- **WHEN** `~/.claude/settings.json` has `ANTHROPIC_BASE_URL=https://api.example.com` and `permissions.allow` alongside a ccl artifact
- **THEN** the cleanup leaves `ANTHROPIC_BASE_URL` and `permissions` unchanged

#### Scenario: a snapshot failure does not block the launch
- **WHEN** `config.Manager.Backup()` returns an error
- **THEN** ccl prints a warning and continues, and Claude Code still starts

### Requirement: ccl serve --restore removes legacy ccl artifacts from settings.json
`ccl serve --restore` MUST remove the artifacts an earlier version of ccl left in `~/.claude/settings.json`, using the same rules as the one-time cleanup:

- For each `CC_ORIG_<KEY>` or `FORWORD_<KEY>`: write the backup value back to `<KEY>` and delete the backup key — unless the backup value equals ccl's own default for that key, in which case the live key MUST be deleted instead.
- For each managed OTel key that has no backup: delete it when its value equals ccl's own default for that key.
- Every other key MUST be left untouched.

The command MUST be idempotent, and MUST report what it removed. When the settings file does not exist, it MUST report that there is nothing to restore and exit successfully.

The command MUST NOT be described as undoing a current takeover: ccl does not write its takeover into the settings file any more, so there is no live takeover to undo.

#### Scenario: the original user config is recovered
- **WHEN** the user runs `ccl serve --restore` and the settings file has `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`
- **THEN** the settings file ends up with `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and no `CC_ORIG_*` key

#### Scenario: keys ccl added are removed
- **WHEN** the user runs `ccl serve --restore` and the settings file has `OTEL_METRICS_EXPORTER=otlp` (ccl added it; the user never had a value)
- **THEN** the key is removed (since no `CC_ORIG_OTEL_METRICS_EXPORTER` exists)

#### Scenario: the command is idempotent
- **WHEN** the user runs `ccl serve --restore` twice
- **THEN** the second run is a no-op; the settings file is unchanged

#### Scenario: user-added non-ccl keys are kept
- **WHEN** the user has `ANTHROPIC_BASE_URL=https://...` (a key ccl does not touch)
- **THEN** `ccl serve --restore` does not modify it

#### Scenario: the settings file does not exist
- **WHEN** the user runs `ccl serve --restore` and `~/.claude/settings.json` is missing
- **THEN** ccl prints "no settings to restore" and exits 0

## MODIFIED Requirements

### Requirement: daemon auto-enables forwarder based on CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT
On `ccl serve` startup, the daemon MUST inspect its own env for `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` and for `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`. If the per-signal variable is set, the daemon MUST forward to it; otherwise, if `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` is set, the daemon MUST forward to that URL. If the selected URL is `http://localhost:4318` (or 127.0.0.1), the daemon MUST NOT enable a forwarder, so that the daemon does not forward to itself.

The forwarding target MUST be the user's own endpoint, delivered by ccl into the daemon's process environment before the daemon is forked. The daemon MUST NOT read the endpoint from `~/.claude/settings.json` itself, and MUST NOT read it from the payload of an export.

ccl MUST NOT set either variable to a placeholder value when the user has no endpoint configured; an unset variable means "no forwarding".

#### Scenario: user has remote OTLP configured
- **WHEN** the daemon starts with `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`
- **THEN** the daemon enables the forwarder; each metric received via /v1/metrics is: (1) written to local SQLite (sync), (2) POSTed to remote in a goroutine (async, fire-and-forget)

#### Scenario: the per-signal target takes priority
- **WHEN** the daemon starts with `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=https://metrics:4318` and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://fallback:4317`
- **THEN** the daemon forwards the metrics to `https://metrics:4318`

#### Scenario: forwarder detects localhost (loop prevention)
- **WHEN** the daemon starts and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` (or 127.0.0.1)
- **THEN** the daemon does NOT enable forwarder; logs at startup "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT points to localhost; forwarder disabled to prevent loop"

#### Scenario: no backup env present
- **WHEN** the daemon starts and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` is unset
- **THEN** the daemon does NOT enable forwarder; no log emitted

## REMOVED Requirements

### Requirement: ccl injects OTEL env into Claude Code settings with backup
Removed: the OTel environment is delivered through `--settings` now (see "ccl delivers the OTel env to Claude Code through --settings"), and ccl does not write the settings file any more, so there is nothing to back up.

### Requirement: ccl serve --restore removes all CC_ORIG_* and ccl-injected defaults
Removed: superseded by "ccl serve --restore removes legacy ccl artifacts from settings.json" — the command now only cleans up what an earlier version of ccl left behind, because there is no live takeover in the settings file to undo.

### Requirement: ccl 文件级备份仅在控制权交接时触发
Removed: the control-handover backup existed only because ccl rewrote the settings file. ccl does not write that file any more, so there is no handover to detect.
