## MODIFIED Requirements

### Requirement: Migration preserves non-provider env keys
The provider migration SHALL NOT touch any key outside the snapshot. Specifically, OTel-related keys (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`, `OTEL_METRICS_EXPORTER`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `CLAUDE_CODE_ENABLE_TELEMETRY`) and backup keys (`CC_ORIG_*`, `FORWORD_*`) SHALL be preserved unchanged by this migration, regardless of whether they appear in the snapshot.

This preservation is scoped to the provider migration. A separate one-time cleanup of the artifacts that an earlier version of ccl left in the settings file MAY remove ccl's own OTel artifacts and the `CC_ORIG_*` / `FORWORD_*` keys. That cleanup is a different migration with its own rules: it MUST NOT remove any key outside the OTel keys it manages, and it MUST NOT remove a key whose value does not belong to ccl.

#### Scenario: OTel keys not in snapshot are preserved
- **WHEN** `settings.json.env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://localhost:4318"` (not in snapshot)
- **THEN** after the provider migration, `settings.json.env["OTEL_EXPORTER_OTLP_ENDPOINT"]` SHALL still equal `"http://localhost:4318"` unchanged

#### Scenario: CC_ORIG_* keys not in snapshot are preserved
- **WHEN** `settings.json.env["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://remote:4318"` (not in snapshot)
- **THEN** after the provider migration, this key SHALL remain unchanged

#### Scenario: the provider migration and the OTel cleanup do not overlap
- **WHEN** the settings file holds both a provider key in the snapshot and an OTel artifact of an earlier ccl version
- **THEN** the provider migration removes only the provider key, and the OTel cleanup removes only the OTel artifact; neither removes a key that belongs to the other
