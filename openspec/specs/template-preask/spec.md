# template-preask Specification

## Purpose
TBD - created by archiving change template-driven-preask. Update Purpose after archive.
## Requirements
### Requirement: Template MAY declare pre-ask fields

`templates.Template` MUST expose a `PreAsks []PreAsk` field. Each `PreAsk` declares one question to be asked BEFORE the template's main field-collection loop runs. `PreAsk.Key` is written into the collected-values map so subsequent field defaults can reference it via `{Key}` substitution.

The `PreAsk` type MUST contain:
- `Key string` — the slot in the collected-values map
- `Prompt string` — the text printed to the user
- `Default string` — value used when the user submits empty input (optional)
- `Validate func(string) error` — validation; returns nil on accept, non-nil to re-prompt

#### Scenario: minimax template declares region pre-ask
- **WHEN** user runs `ccl provider --new --type minimax`
- **THEN** the prompt starting with `地域` appears BEFORE any `TemplateField` prompt; the answer is recorded under key `region`; if the user submits empty input, the value recorded is `cn`; values other than `cn` or `io` trigger a re-prompt (no cancellation); the value `io` is recorded as `io`

### Requirement: Template MAY declare post-collection field overrides

`templates.Template` MUST expose a `PostOverrides []FieldOverride` field. Each `FieldOverride` declares one field to be replaced AFTER the main field-collection loop runs (and AFTER placeholder substitution). `FieldOverride.Apply` receives the collected-values map (containing PreAsk values and Field values) and returns the new value to write for `Field`.

The `FieldOverride` type MUST contain:
- `Field string` — the target field key (MUST match an existing `TemplateField.Key`)
- `Apply func(map[string]string) string` — returns the new value

`Apply` MUST run AFTER placeholder substitution and BEFORE the write-to-disk step. The value returned by `Apply` MUST overwrite any previous value for `Field` in the final provider config.

#### Scenario: minimax overrides ANTHROPIC_BASE_URL based on region cn
- **WHEN** user runs `ccl provider --new --type minimax` and the pre-ask `region` resolves to `cn` (either by user input or default)
- **THEN** the final `ANTHROPIC_BASE_URL` in the written provider config is `https://api.minimax.cn/anthropic`, regardless of any `TemplateField.Default` value for `ANTHROPIC_BASE_URL`

#### Scenario: minimax overrides ANTHROPIC_BASE_URL based on region io
- **WHEN** user runs `ccl provider --new --type minimax` and the pre-ask `region` resolves to `io`
- **THEN** the final `ANTHROPIC_BASE_URL` in the written provider config is `https://api.minimax.io/anthropic`

### Requirement: Template placeholder substitution is generic

The field-collection loop MUST substitute `{Key}` placeholders in any `TemplateField.Default` with the corresponding value from the collected-values map. Substitution order MUST be: PreAsk values first, then collected Field values. Substitution MUST NOT depend on template name — any field whose `Default` contains a `{Key}` placeholder where `Key` resolves to a known value is substituted; fields whose placeholder does not resolve are left unchanged.

#### Scenario: aliyun-pay ANTHROPIC_BASE_URL is built from WorkspaceId via placeholder substitution
- **WHEN** user runs `ccl provider --new --type aliyun-pay` and the collected `WorkspaceId` value is `ws-abc123`
- **THEN** the final `ANTHROPIC_BASE_URL` is `https://ws-abc123.cn-beijing.maas.aliyuncs.com/apps/anthropic` (the `{WorkspaceId}` placeholder in `ANTHROPIC_BASE_URL.Default` is substituted with `ws-abc123`); the `WorkspaceId` key itself MUST NOT appear in the final provider config (it is a temporary field, deleted before write-to-disk)

### Requirement: Template MAY declare skip-prompt fields

`templates.TemplateField` MUST expose a `SkipPrompt bool` field. When `SkipPrompt` is `true`, the field-collection loop MUST NOT prompt the user and MUST write the (placeholder-substituted) `Default` value directly to the collected-values map. The `SkipPrompt` decision MUST NOT depend on the template name — any field whose `SkipPrompt == true` is auto-written regardless of which template owns it.

#### Scenario: aliyun-pay ANTHROPIC_BASE_URL is auto-assembled without prompting
- **WHEN** user runs `ccl provider --new --type aliyun-pay` and enters `WorkspaceId = "ws-abc123"`
- **THEN** the cli MUST NOT prompt for `ANTHROPIC_BASE_URL`; the final `ANTHROPIC_BASE_URL` in the written provider config is `https://ws-abc123.cn-beijing.maas.aliyuncs.com/apps/anthropic` (placeholder-substituted from the `SkipPrompt` field's `Default`)

#### Scenario: minimax ANTHROPIC_BASE_URL is auto-selected without prompting
- **WHEN** user runs `ccl provider --new --type minimax` and the pre-ask `region` resolves to any value
- **THEN** the cli MUST NOT prompt for `ANTHROPIC_BASE_URL`; the final `ANTHROPIC_BASE_URL` is determined by the template's `PostOverride` for `ANTHROPIC_BASE_URL` (using the resolved `region` value)

