# provider-templates Specification

## Purpose
TBD - created by archiving change improve-help-and-provider-templates. Update Purpose after archive.
## Requirements
### Requirement: --template prints a single template
`ccl provider --template <type>` (short alias `-T`) MUST print the JSON template for the requested type to stdout. Exit code MUST be 0. The output MUST be valid JSON (parseable by `jq` / `json.Unmarshal`).

#### Scenario: --template anthropic prints Anthropic template
- **WHEN** user runs `ccl provider --template anthropic` (or `ccl provider -T anthropic`)
- **THEN** stdout is a valid JSON object with `env.ANTHROPIC_AUTH_TOKEN` only (no `ANTHROPIC_BASE_URL`; official endpoint used); exit code 0

#### Scenario: --template deepseek prints DeepSeek template
- **WHEN** user runs `ccl provider --template deepseek`
- **THEN** stdout is a valid JSON object containing `env.ANTHROPIC_BASE_URL: "https://api.deepseek.com/anthropic"`, `env.ANTHROPIC_AUTH_TOKEN`, `env.ANTHROPIC_MODEL: "deepseek-flash[1m]"`, and the three `env.ANTHROPIC_DEFAULT_*_MODEL` fields plus `env.CLAUDE_CODE_SUBAGENT_MODEL`; exit code 0

#### Scenario: --template minimax prints MiniMax template (CN)
- **WHEN** user runs `ccl provider --template minimax` and the user has not selected a region
- **THEN** stdout defaults to the 国内 variant: `env.ANTHROPIC_BASE_URL: "https://api.minimax.cn/anthropic"`, `env.ANTHROPIC_AUTH_TOKEN`, `env.ANTHROPIC_MODEL: "MiniMax-M3[1m]"`, and the three `env.ANTHROPIC_DEFAULT_*_MODEL` fields; exit code 0

#### Scenario: --template glm prints Zhipu BigModel Coding Plan template
- **WHEN** user runs `ccl provider --template glm`
- **THEN** stdout is a valid JSON object containing `env.ANTHROPIC_BASE_URL: "https://open.bigmodel.cn/api/anthropic"`, `env.ANTHROPIC_AUTH_TOKEN`, the three `env.ANTHROPIC_DEFAULT_*_MODEL` fields (HAIKU=`glm-5.3-flash[1m]`, SONNET/OPUS=`glm-5.3[1m]`), and `env.CLAUDE_CODE_AUTO_COMPACT_WINDOW: "1000000"`; exit code 0

#### Scenario: --template aliyun-coding prints Bailian Coding Plan template
- **WHEN** user runs `ccl provider --template aliyun-coding`
- **THEN** stdout is a valid JSON object containing `env.ANTHROPIC_BASE_URL: "https://coding.dashscope.aliyuncs.com/apps/anthropic"`, `env.ANTHROPIC_AUTH_TOKEN`, and the four `env.ANTHROPIC_MODEL + ANTHROPIC_DEFAULT_*_MODEL` fields all set to `qwen3.7-plus`; exit code 0

#### Scenario: --template aliyun-pay prints Bailian 按量计费 template
- **WHEN** user runs `ccl provider --template aliyun-pay`
- **THEN** stdout is a valid JSON object containing `env.ANTHROPIC_BASE_URL: "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/apps/anthropic"` (literal `{WorkspaceId}` placeholder, NOT replaced), `env.ANTHROPIC_AUTH_TOKEN`, and the model fields set to `qwen3.7-max` / `qwen3.6-flash` per the official doc; exit code 0

#### Scenario: --template aliyun-token prints Bailian Token Plan template
- **WHEN** user runs `ccl provider --template aliyun-token`
- **THEN** stdout is a valid JSON object containing `env.ANTHROPIC_BASE_URL: "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic"`, `env.ANTHROPIC_AUTH_TOKEN`, `env.ANTHROPIC_MODEL: "auto"`, and the three `env.ANTHROPIC_DEFAULT_*_MODEL` fields set to `qwen3.6-flash` / `qwen3.8-flash` / `qwen3.8-max`; exit code 0

### Requirement: --template without type lists all supported types
`ccl provider --template` (no type argument) MUST print the list of supported template names with one-line descriptions, and exit 0.

#### Scenario: --template lists 7 supported types
- **WHEN** user runs `ccl provider --template` (or `ccl provider -T`) with no type
- **THEN** stdout lists `anthropic`, `deepseek`, `minimax`, `glm`, `aliyun-coding`, `aliyun-pay`, `aliyun-token` each with a one-line description; exit code 0

### Requirement: --template rejects unknown types
`ccl provider --template <bogus>` MUST exit non-zero with an error naming the available types. Exit code MUST NOT be 0.

#### Scenario: --template bogus fails clearly
- **WHEN** user runs `ccl provider --template bogus` (or `ccl provider -T bogus`)
- **THEN** exit code is non-zero and stderr explains the supported types

### Requirement: Template bodies are valid JSON
Every template body in `internal/templates` MUST be valid JSON (parses without error). This MUST be enforced by a unit test that iterates `templates.All` and `json.Unmarshal`s each body.

#### Scenario: templates_test.go verifies all 7 bodies parse
- **WHEN** `go test ./internal/templates/...` runs
- **THEN** every entry in `templates.All` unmarshals into `map[string]any` without error

### Requirement: Non-anthropic templates set the required Anthropic env contract
Every non-`anthropic` template MUST set exactly these env keys when they appear in the template's `Fields`:
- `ANTHROPIC_BASE_URL` (literal value, NOT computed)
- `ANTHROPIC_AUTH_TOKEN`
- `ANTHROPIC_DEFAULT_OPUS_MODEL`
- `ANTHROPIC_DEFAULT_SONNET_MODEL`
- `ANTHROPIC_DEFAULT_HAIKU_MODEL`
- (Optionally) `ANTHROPIC_MODEL` — explicit main model

If a template's `Fields` includes `ANTHROPIC_BASE_URL`, that field MUST be non-empty and MUST end in `/anthropic` or `/apps/anthropic`. Drift MUST be caught by a unit test.

#### Scenario: every non-anthropic template has all required model fields
- **WHEN** `go test ./internal/templates/...` runs the field-presence check
- **THEN** for each non-anthropic template, the parsed env map contains `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL`

#### Scenario: every BASE_URL ends with the Anthropic-compatible path
- **WHEN** `go test ./internal/templates/...` runs the URL-shape check
- **THEN** every non-anthropic `ANTHROPIC_BASE_URL` ends with `/anthropic` or `/apps/anthropic`

### Requirement: Template fields are stable identifiers
Every entry in `templates.All` MUST have a non-empty `Name` matching `[a-z-]+` and a non-empty `Description`.

#### Scenario: template names are valid kebab-case
- **WHEN** `templates.All` is iterated
- **THEN** every entry has a non-empty `Name` matching `[a-z-]+` and a non-empty `Description`

### Requirement: Source URL and verification date are documented in code
For each non-`anthropic` template, the `internal/templates/templates.go` source MUST contain a comment block immediately above the template variable documenting (a) the source URL for the base URL value, (b) the source URL for the default model names, (c) the date the values were last verified (YYYY-MM-DD).

#### Scenario: each template has a verification comment
- **WHEN** `internal/templates/templates.go` is inspected
- **THEN** every non-anthropic template variable has a `// verified YYYY-MM-DD:` comment listing the URL source for its base_url and default model

### Requirement: aliyun-pay template preserves the WorkspaceId placeholder
`ccl provider --template aliyun-pay` MUST print the literal `{WorkspaceId}` placeholder in the `ANTHROPIC_BASE_URL` value (not a substituted example WorkspaceId). The interactive flow MUST replace this placeholder with the user-supplied value before writing the JSON file.

#### Scenario: --template aliyun-pay prints literal placeholder
- **WHEN** user runs `ccl provider --template aliyun-pay`
- **THEN** the printed JSON has `ANTHROPIC_BASE_URL` containing the literal substring `{WorkspaceId}` (not replaced)

#### Scenario: guided flow replaces the placeholder before writing
- **WHEN** user runs `ccl provider --new --type aliyun-pay` and supplies a non-empty `WorkspaceId`
- **THEN** the written JSON file has `ANTHROPIC_BASE_URL` containing the user's WorkspaceId (literal `{WorkspaceId}` substring MUST NOT be present)

