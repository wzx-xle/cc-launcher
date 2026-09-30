## MODIFIED Requirements

### Requirement: ccl injects OTEL env into Claude Code settings with backup
On every `ccl` invocation (before fork-if-needed and exec Claude Code), ccl MUST inspect `~/.claude/settings.json` and ensure the `env` map contains the OTel keys. For each key that already has a user value, ccl MUST first save a backup (to a backup key), then apply the action below. For missing keys, ccl MUST add the default without a backup.

The 6 keys and actions:

| Key | Backup key (when user has value) | If user has value | If missing |
|---|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` | backup; override to `http://localhost:4318` | add `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | backup; override to `http://localhost:4318/v1/metrics` | **do not inject** (Claude Code falls back to `OTEL_EXPORTER_OTLP_ENDPOINT`) |
| `OTEL_LOGS_EXPORTER` | `CC_ORIG_OTEL_LOGS_EXPORTER` | backup; **respect user's value** | add `otlp` (default; user can edit) |
| `OTEL_METRICS_EXPORTER` | `CC_ORIG_OTEL_METRICS_EXPORTER` | backup; respect user's value | add `otlp` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `CC_ORIG_OTEL_EXPORTER_OTLP_PROTOCOL` | backup; respect user's value | add `http/protobuf` |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | `CC_ORIG_CLAUDE_CODE_ENABLE_TELEMETRY` | backup; respect user's value | add `1` |

Per-signal logs and traces endpoints (`OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) are NOT touched by ccl — the user owns those values; Claude Code SDK sends logs/traces straight to those endpoints when configured.

**Note**: ccl does NOT force-override `OTEL_LOGS_EXPORTER`. The local-privacy guarantee (no prompts in SQLite) comes from the daemon's architecture (it accepts /v1/logs but only returns 200, never persists or forwards). User's original `OTEL_LOGS_EXPORTER=otlp` continues to work — logs reach the remote collector directly via per-signal endpoint when configured, or via daemon's 200-only path otherwise.

#### Scenario: settings.json has only OTel endpoint (typical user)
- **WHEN** `~/.claude/settings.json` has only `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`
- **THEN** after ccl:
  - `OTEL_EXPORTER_OTLP_ENDPOINT` = `http://localhost:4318`
  - `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` = `https://company-otel:4317`
  - `OTEL_LOGS_EXPORTER` = `none` (newly added, no backup)
  - `OTEL_METRICS_EXPORTER` = `otlp` (newly added)
  - `OTEL_EXPORTER_OTLP_PROTOCOL` = `http/protobuf` (newly added)
  - `CLAUDE_CODE_ENABLE_TELEMETRY` = `1` (newly added)

#### Scenario: typical company remote collector with per-signal metrics
- **WHEN** `~/.claude/settings.json` has:
  - `OTEL_EXPORTER_OTLP_ENDPOINT=http://remote:4318`
  - `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://remote:4318/v1/metrics`
  - `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://remote:4318/v1/logs`
  - `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://remote:4318/v1/traces`
- **THEN** after ccl:
  - `OTEL_EXPORTER_OTLP_ENDPOINT` = `http://localhost:4318` (override)
  - `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` = `http://remote:4318` (backup)
  - `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` = `http://localhost:4318/v1/metrics` (override)
  - `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` = `http://remote:4318/v1/metrics` (backup)
  - `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` = `http://remote:4318/v1/logs` (UNTOUCHED — user owns)
  - `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` = `http://remote:4318/v1/traces` (UNTOUCHED — user owns)
  - remaining 4 fallback keys follow the existing logic (backup + respect or inject default)

#### Scenario: user has only per-signal metrics (no fallback)
- **WHEN** `~/.claude/settings.json` has only `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://remote:4318/v1/metrics` (no fallback endpoint key)
- **THEN** after ccl:
  - `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` = `http://localhost:4318/v1/metrics` (override)
  - `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` = `http://remote:4318/v1/metrics` (backup)
  - `OTEL_EXPORTER_OTLP_ENDPOINT` is injected as `http://localhost:4318` (default; ccl ensures fallback exists)

#### Scenario: settings.json already has all 6 keys
- **WHEN** user has manually configured every OTel key (including per-signal metrics)
- **THEN** ccl backs up all 6 to their respective backup keys (only first time; if backups already present, don't overwrite); ccl force-overrides `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`; other 4 are preserved

#### Scenario: settings.json already has CC_ORIG_* or FORWORD_* from prior ccl run
- **WHEN** user runs `ccl` again and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` already exists (or `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` already exists)
- **THEN** ccl does NOT overwrite the backup (preserves the original saved value); only adds missing keys

#### Scenario: user has OTEL_LOGS_EXPORTER=otlp (logs enabled) — ccl respects
- **WHEN** user has `OTEL_LOGS_EXPORTER=otlp`
- **THEN** ccl saves `CC_ORIG_OTEL_LOGS_EXPORTER=otlp` and **respects the value** (does NOT force to `none`); Claude Code pushes logs to wherever its `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` points (per-signal, untouched); daemon `/v1/logs` is only hit via fallback path and returns 200 only

#### Scenario: user sets OTEL_METRICS_EXPORTER to a non-otlp value (e.g. prometheus)
- **WHEN** user has `OTEL_METRICS_EXPORTER=prometheus`
- **THEN** ccl backs up as `CC_ORIG_OTEL_METRICS_EXPORTER=prometheus` and respects the value; Claude Code will use prometheus, ccl stats will be empty (user opted out)

#### Scenario: env value points to a remote collector
- **WHEN** user has `OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example.com:4318`
- **THEN** ccl backs it up to `CC_ORIG_*`; daemon sees the backup at startup and **enables forwarder** (fallback trigger path) to that URL; metrics flow: Claude Code → daemon → remote collector (and locally to SQLite)

#### Scenario: existing settings.json field order is preserved
- **WHEN** ccl writes new keys to an existing settings.json with keys in order `[env, permissions, enabledPlugins, ...]`
- **THEN** after write, the file still has the same key order with new keys appended in the env section, not inserted at the top of the file (ccl uses `internal/config/ordered.go` to preserve order)

### Requirement: ccl restore removes CC_ORIG_*, FORWORD_, and ccl-injected defaults
`ccl restore` MUST read `~/.claude/settings.json` env field and:
- For each `CC_ORIG_<KEY>` found: write `<KEY>` = `CC_ORIG_<KEY>` value; delete the `CC_ORIG_*` key
- For each `FORWORD_<KEY>` found: write `<KEY>` = `FORWORD_<KEY>` value; delete the `FORWORD_*` key
- For each of the 6 OTel keys ccl would have injected as default: delete them if no backup exists (i.e., user never had them — they're purely ccl artifacts)
- The settings.json file structure MUST be preserved otherwise

`FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` MUST be restored to `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` (per-signal metrics endpoint), not to `OTEL_EXPORTER_OTLP_ENDPOINT` (the keys are different — the per-signal backup belongs to the per-signal key).

#### Scenario: restore recovers original user config (CC_ORIG_)
- **WHEN** user runs `ccl restore` and settings.json has `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` (along with ccl's override to localhost:4318)
- **THEN** settings.json ends up with `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and no `CC_ORIG_*` keys

#### Scenario: restore recovers per-signal metrics config (FORWORD_)
- **WHEN** user runs `ccl restore` and settings.json has `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://remote:4318/v1/metrics` (along with ccl's override to localhost:4318/v1/metrics)
- **THEN** settings.json ends up with `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://remote:4318/v1/metrics` and no `FORWORD_*` keys

#### Scenario: restore removes keys ccl added (no original)
- **WHEN** user runs `ccl restore` and settings.json has `OTEL_METRICS_EXPORTER=otlp` (ccl added it; user never had a value)
- **THEN** the key is removed (since no `CC_ORIG_OTEL_METRICS_EXPORTER` exists)

#### Scenario: restore removes per-signal metrics override ccl added (no original)
- **WHEN** user runs `ccl restore` and settings.json has `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics` (ccl added it; user never had a per-signal metrics key)
- **THEN** the key is removed (since no `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` exists)

#### Scenario: restore is idempotent
- **WHEN** user runs `ccl restore` twice
- **THEN** the second run is a no-op; settings.json is unchanged

#### Scenario: restore keeps user-added non-ccl keys
- **WHEN** user has `ANTHROPIC_BASE_URL=https://...` (a ccl-doesn't-touch key)
- **THEN** `ccl restore` does not modify it

#### Scenario: restore keeps user-added OTEL_LOGS_EXPORTER override
- **WHEN** user has `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://remote:4318/v1/logs` (a ccl-doesn't-touch key)
- **THEN** `ccl restore` does not modify it (no `FORWORD_*` backup exists for it because ccl doesn't touch it)

#### Scenario: settings.json does not exist
- **WHEN** user runs `ccl restore` and `~/.claude/settings.json` is missing
- **THEN** ccl prints "no settings to restore" and exits 0

### Requirement: daemon auto-enables metrics forwarder based on FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (with CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT fallback)
On `ccl serve` startup, the daemon MUST inspect its own env to determine the metrics forwarder target. Priority:
1. `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` (if set and not localhost) → target this URL (suffixed with `/v1/metrics` if not already)
2. Else `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` (if set and not localhost) → target this URL (suffixed with `/v1/metrics`)
3. Else forwarder disabled

When enabled, the daemon MUST forward each parsed metric to the chosen URL after persisting locally.

#### Scenario: user has per-signal metrics remote configured
- **WHEN** daemon starts with `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=https://company-otel:4318/v1/metrics`
- **THEN** forwarder is enabled targeting that URL; each metric received via /v1/metrics is: (1) written to local SQLite (sync), (2) POSTed to remote in a goroutine (async, fire-and-forget)

#### Scenario: user has fallback remote configured (legacy path)
- **WHEN** daemon starts with only `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` (no per-signal metrics backup)
- **THEN** forwarder is enabled targeting `https://company-otel:4317/v1/metrics`; metrics flow: Claude Code → daemon → remote collector (and locally to SQLite)

#### Scenario: forwarder detects localhost (loop prevention)
- **WHEN** daemon starts and both `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` are unset, OR both point to localhost
- **THEN** daemon does NOT enable forwarder; logs at startup "no remote endpoint configured; forwarder disabled"

#### Scenario: no backup env present
- **WHEN** daemon starts and both `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` are unset
- **THEN** daemon does NOT enable forwarder; no log emitted (silent default)

## ADDED Requirements

### Requirement: injectOTelEnv propagates FORWORD_ env var to ccl serve subprocess
When `injectOTelEnv` runs in the parent ccl process (before fork-if-needed), after applying the env map to `~/.claude/settings.json`, ccl MUST also `os.Setenv` the new backup key `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` (if user originally had a per-signal metrics endpoint) so that the forked `ccl serve` process inherits it via the standard `os.Environ()` mechanism. Without this step, the forwarder would not see the backup env var and metrics would not be forwarded.

#### Scenario: ccl forks ccl serve — env propagation works
- **WHEN** parent ccl has injected `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://remote:4318/v1/metrics` into settings.json env map
- **THEN** parent ccl also `os.Setenv`s the same key in its own process env; forked `ccl serve` inherits via `os.Environ()`; daemon sees the backup env at startup and enables forwarder

#### Scenario: ccl does not fork ccl serve — env still in settings.json
- **WHEN** daemon is already running from a prior ccl invocation; current `ccl` call does not fork
- **THEN** `injectOTelEnv` still `os.Setenv`s the backup key (no harm; daemon ignores env changes after startup); the next fork (if any) would still see it

#### Scenario: user has no per-signal metrics — no FORWORD_ key set
- **WHEN** user has no `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` in original settings.json
- **THEN** ccl does NOT `os.Setenv` any `FORWORD_*` key; daemon has no backup to forward to