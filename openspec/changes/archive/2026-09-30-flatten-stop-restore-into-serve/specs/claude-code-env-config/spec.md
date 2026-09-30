## MODIFIED Requirements

### Requirement: ccl serve --restore removes all CC_ORIG_* and ccl-injected defaults
`ccl serve --restore` MUST read `~/.claude/settings.json` env field and:
- For each `CC_ORIG_<KEY>` found: write `<KEY>` = `CC_ORIG_<KEY>` value; delete the `CC_ORIG_*` key
- For each of the 5 OTel keys ccl would have injected as default: delete them if no backup exists (i.e., user never had them — they're purely ccl artifacts)
- The settings.json file structure MUST be preserved otherwise

#### Scenario: serve --restore recovers original user config
- **WHEN** user runs `ccl serve --restore` and settings.json has `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` (along with ccl's override to localhost:4318)
- **THEN** settings.json ends up with `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and no `CC_ORIG_*` keys

#### Scenario: serve --restore removes keys ccl added (no original)
- **WHEN** user runs `ccl serve --restore` and settings.json has `OTEL_METRICS_EXPORTER=otlp` (ccl added it; user never had a value)
- **THEN** the key is removed (since no `CC_ORIG_OTEL_METRICS_EXPORTER` exists)

#### Scenario: serve --restore is idempotent
- **WHEN** user runs `ccl serve --restore` twice
- **THEN** the second run is a no-op; settings.json is unchanged

#### Scenario: serve --restore keeps user-added non-ccl keys
- **WHEN** user has `ANTHROPIC_BASE_URL=https://...` (a ccl-doesn't-touch key)
- **THEN** `ccl serve --restore` does not modify it

#### Scenario: settings.json does not exist
- **WHEN** user runs `ccl serve --restore` and `~/.claude/settings.json` is missing
- **THEN** ccl prints "no settings to restore" and exits 0