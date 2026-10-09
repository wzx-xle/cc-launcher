# provider-setenv-migration Specification

## Purpose
TBD - created by archiving change provider-setenv-cwd-binding. Update Purpose after archive.
## Requirements
### Requirement: One-time migration removes ccl-injected ANTHROPIC_* from settings.json
On every TUI startup, before applying provider env, ccl SHALL run a one-time migration that detects `env.ANTHROPIC_*` keys in `~/.claude/settings.json` whose value equals the value in `state.json.injected_snapshot.env[key]` and removes them from `settings.json`. The migration SHALL be a no-op if `injected_snapshot` is empty/nil.

#### Scenario: snapshot has ANTHROPIC_AUTH_TOKEN, settings.json matches
- **WHEN** `state.json.injected_snapshot.env.ANTHROPIC_AUTH_TOKEN = "sk-ant-old"` and `settings.json.env["ANTHROPIC_AUTH_TOKEN"] = "sk-ant-old"`
- **THEN** `settings.json.env["ANTHROPIC_AUTH_TOKEN"]` SHALL be removed (key deleted) and the rest of `settings.json` preserved unchanged

#### Scenario: snapshot is nil or empty
- **WHEN** `GetInjectedSnapshot()` returns `nil` or `{env: {}}`
- **THEN** the migration SHALL return early without reading or writing any file

#### Scenario: migration runs every startup
- **WHEN** `ccl` is invoked and `injected_snapshot` is non-empty
- **THEN** the migration function SHALL run on each startup (no "already-migrated" flag — idempotency is achieved by clearing `injected_snapshot` after migration)

### Requirement: Migration preserves user-modified keys
If a key in `settings.json.env` is also in `injected_snapshot.env` but the value DIFFERS from the snapshot, the migration SHALL NOT delete that key (the user manually edited it; CLAUDE.md requires Claude Code's own additions to be preserved).

#### Scenario: user manually edited ANTHROPIC_AUTH_TOKEN
- **WHEN** `state.json.injected_snapshot.env.ANTHROPIC_AUTH_TOKEN = "sk-ccl-wrote"` and `settings.json.env["ANTHROPIC_AUTH_TOKEN"] = "sk-user-wrote"` (different value)
- **THEN** `settings.json.env["ANTHROPIC_AUTH_TOKEN"]` SHALL remain as `"sk-user-wrote"` (NOT removed)

#### Scenario: snapshot has key but settings.json does not
- **WHEN** `state.json.injected_snapshot.env.ANTHROPIC_AUTH_TOKEN = "sk-old"` but `settings.json.env` has no `ANTHROPIC_AUTH_TOKEN` key
- **THEN** the migration SHALL be a no-op for that key (nothing to remove)

### Requirement: Migration preserves non-provider env keys
The migration SHALL NOT touch any key outside the snapshot. Specifically, OTel-related keys (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`, `OTEL_METRICS_EXPORTER`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `CLAUDE_CODE_ENABLE_TELEMETRY`) and backup keys (`CC_ORIG_*`, `FORWORD_*`) SHALL be preserved unchanged regardless of whether they appear in the snapshot.

#### Scenario: OTel keys not in snapshot are preserved
- **WHEN** `settings.json.env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://localhost:4318"` (not in snapshot)
- **THEN** after migration, `settings.json.env["OTEL_EXPORTER_OTLP_ENDPOINT"]` SHALL still equal `"http://localhost:4318"` unchanged

#### Scenario: CC_ORIG_* keys not in snapshot are preserved
- **WHEN** `settings.json.env["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://remote:4318"` (not in snapshot)
- **THEN** after migration, this key SHALL remain unchanged

### Requirement: Migration is idempotent
After successful migration, `state.json.injected_snapshot` SHALL be set to `nil` (or empty map). On the next startup, the migration SHALL be a no-op (no snapshot → early return).

#### Scenario: migration clears injected_snapshot
- **WHEN** the migration successfully removes keys from `settings.json`
- **THEN** `state.json.injected_snapshot` SHALL be cleared (persisted as `nil`) on the same write

#### Scenario: second startup after migration
- **WHEN** `ccl` is invoked a second time after a successful migration
- **THEN** the migration function SHALL return early without touching `settings.json` (because `injected_snapshot` is `nil`)

#### Scenario: migration aborted mid-write
- **WHEN** the migration function fails mid-write (e.g., disk full) before clearing the snapshot
- **THEN** on next startup the migration SHALL re-run (idempotent — re-removes already-removed keys, no harm)

### Requirement: Migration backs up settings.json before write
The migration SHALL call `config.Manager.Backup()` BEFORE writing to `settings.json`, so the migration is reversible via `ccl serve --restore`.

#### Scenario: backup created on first run with non-empty snapshot
- **WHEN** the migration function is about to modify `settings.json` and the snapshot has at least one match
- **THEN** `~/.cc-launcher/backups/settings-<timestamp>.json` SHALL contain the pre-migration `settings.json` content

#### Scenario: no backup when migration has nothing to remove
- **WHEN** the migration function would remove zero keys (snapshot values don't match settings.json, or no overlap)
- **THEN** NO backup file SHALL be created (only write when there's actual change)

### Requirement: Migration reports to user
The migration SHALL print a one-line stderr message before modifying settings.json, indicating what was cleaned. The message SHALL include the count of cleaned keys and the backup file path.

#### Scenario: migration prints message before write
- **WHEN** the migration function is about to modify `settings.json`
- **THEN** stderr SHALL receive a message in the form `⚙ 检测到 ccl 之前注入的 provider env，正在清理 settings.json（备份到 ~/.cc-launcher/backups/settings-<timestamp>.json）... ✓` with `<N>` keys cleaned

#### Scenario: migration with no work
- **WHEN** the migration function would remove zero keys
- **THEN** no message SHALL be printed (silent no-op for already-clean settings.json)

