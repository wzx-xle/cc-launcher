## MODIFIED Requirements

### Requirement: parser maps OTLP protobuf metrics to domain objects
The OTLP parser MUST extract for each metric: name, value, labels (resource + scope + data-point attributes), timestamp. Log and trace payloads are not parsed.

Before a metric is written to the database, the store MUST trim its labels to a fixed whitelist of keys, so that the attributes which repeat identically across every row of a session — the host-level and session-level constants — are not stored once per row.

#### Scenario: claude_code.session.count metric is extracted
- **WHEN** an OTLP export includes `claude_code.session.count` with label `provider=deepseek`
- **THEN** the parser produces one `metrics` row with `name="claude_code.session.count"`, `value=1`, labels `{"provider":"deepseek"}`, captured_at=timestamp

#### Scenario: input_tokens metric accumulates value
- **WHEN** multiple OTLP data points arrive with the same metric name + labels
- **THEN** each is stored as a separate row (no aggregation at parse time; aggregation happens at query time)

#### Scenario: parser extracts resource attribute as session metadata
- **WHEN** an OTLP export includes resource attributes (`service.name`, `service.version`) and session metadata (`session.id`, `cwd`)
- **THEN** the parser records these in the `sessions` row for that session_id; later metrics with the same session_id link to that row
