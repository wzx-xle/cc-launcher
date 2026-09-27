# provider-new-guided-flow Specification

## Purpose
TBD - created by archiving change improve-help-and-provider-templates. Update Purpose after archive.
## Requirements
### Requirement: ccl provider --new accepts --type to preselect template
`ccl provider --new --type <type>` MUST preselect the template and skip the "select template" interactive step. `--type` MUST be one of the names returned by `ccl provider --template` (`anthropic`, `deepseek`, `minimax`, `glm`, `aliyun-coding`, `aliyun-pay`, `aliyun-token`).

#### Scenario: --type deepseek skips type selection
- **WHEN** user runs `ccl provider --new --type deepseek`
- **THEN** the flow goes straight to prompting the provider name (no "select template" prompt); exit code 0 on success

#### Scenario: --type aliyun-pay skips type selection
- **WHEN** user runs `ccl provider --new --type aliyun-pay`
- **THEN** the flow goes straight to prompting the provider name; the WorkspaceId prompt appears BEFORE the BASE_URL prompt; exit code 0 on success

#### Scenario: --type bogus fails before any prompt
- **WHEN** user runs `ccl provider --new --type bogus`
- **THEN** exit code is non-zero, stderr names the 7 valid types, and NO provider config file is written

### Requirement: Each interactive step prints its purpose
Before reading input for a step, `ccl provider --new` MUST print a one-line explanation of what the step is for and what value to enter. The explanation MUST come from `templates.Hint` for the active template (no hardcoded strings in `cli.go`). The number of steps MUST equal `len(template.Fields)` plus 1 (the leading "provider name" step).

#### Scenario: prompt includes the step explanation
- **WHEN** user runs `ccl provider --new --type deepseek` and the flow reaches the `ANTHROPIC_BASE_URL` step
- **THEN** the prompt printed before reading input contains the BASE_URL hint from `templates.ByName("deepseek").Hints`

#### Scenario: type-less interactive flow shows the template selection hint
- **WHEN** user runs `ccl provider --new` (no `--type`)
- **THEN** the flow first lists the 7 available templates with one-line descriptions, then prompts for the type

#### Scenario: deepseek flow has 8 steps (1 name + 7 Fields)
- **WHEN** user runs `ccl provider --new --type deepseek`
- **THEN** the flow iterates 8 prompts in order: provider name, ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, ANTHROPIC_MODEL, ANTHROPIC_DEFAULT_OPUS_MODEL, ANTHROPIC_DEFAULT_SONNET_MODEL, ANTHROPIC_DEFAULT_HAIKU_MODEL, CLAUDE_CODE_SUBAGENT_MODEL

#### Scenario: glm flow has 9 steps (1 name + 8 Fields)
- **WHEN** user runs `ccl provider --new --type glm`
- **THEN** the flow iterates 9 prompts in order: provider name, ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, ANTHROPIC_DEFAULT_HAIKU_MODEL, ANTHROPIC_DEFAULT_SONNET_MODEL, ANTHROPIC_DEFAULT_OPUS_MODEL, CLAUDE_CODE_AUTO_COMPACT_WINDOW, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC, API_TIMEOUT_MS

#### Scenario: anthropic flow has 3 steps (1 name + 2 Fields)
- **WHEN** user runs `ccl provider --new --type anthropic`
- **THEN** the flow iterates 3 prompts: provider name, ANTHROPIC_AUTH_TOKEN (plus an optional auto-confirmation that no BASE_URL is set)

### Requirement: aliyun-pay flow asks for WorkspaceId before BASE_URL
The `aliyun-pay` template MUST have `ANTHROPIC_BASE_URL` containing the literal `{WorkspaceId}` placeholder. The interactive flow MUST prompt for the WorkspaceId BEFORE showing the BASE_URL step. The placeholder MUST be replaced with the user-supplied value before writing the JSON file.

#### Scenario: aliyun-pay WorkspaceId is required
- **WHEN** user runs `ccl provider --new --type aliyun-pay` and submits an empty WorkspaceId
- **THEN** the flow re-prompts until a non-empty value is entered (the field is `Required: true` in `templates.ByName("aliyun-pay").Fields`)

#### Scenario: aliyun-pay written file has no placeholder
- **WHEN** user runs `ccl provider --new --type aliyun-pay` and supplies `WorkspaceId=my-ws-12345`
- **THEN** the written JSON file's `ANTHROPIC_BASE_URL` is `https://my-ws-12345.cn-beijing.maas.aliyuncs.com/apps/anthropic` (literal `{WorkspaceId}` MUST NOT appear)

### Requirement: minimax flow asks for region (国内/国际) before BASE_URL
The `minimax` template MUST offer the user a choice between 国内 (`api.minimax.cn/anthropic`) and 国际 (`api.minimax.io/anthropic`). The chosen region's URL is used as the BASE_URL. The region choice MUST be prompted BEFORE the BASE_URL step.

#### Scenario: minimax 国内 is the default
- **WHEN** user runs `ccl provider --new --type minimax` and accepts the default region
- **THEN** the BASE_URL in the written JSON file is `https://api.minimax.cn/anthropic`

#### Scenario: minimax 国际 requires explicit choice
- **WHEN** user runs `ccl provider --new --type minimax` and explicitly types 国际
- **THEN** the BASE_URL in the written JSON file is `https://api.minimax.io/anthropic`

### Requirement: Secret fields do not echo input
When the interactive flow reads a `Secret: true` field (currently `ANTHROPIC_AUTH_TOKEN`), the readline driver MUST disable terminal echo, similar to a password prompt. If echo cannot be disabled (e.g., piped stdin), the field MUST still be read; this is documented as a known limitation.

#### Scenario: secret field prompt mentions "不回显"
- **WHEN** the flow reaches an `ANTHROPIC_AUTH_TOKEN` prompt
- **THEN** the prompt text includes the marker "不回显" so the user knows input is hidden

### Requirement: provider --new --template is independent of interactive flow
`ccl provider --new --template` MUST print the same content as `ccl provider --template` (no `--new` required). This exists for users who discover `--template` via `--help` and may type both flags out of habit.

#### Scenario: --new --template still just prints templates
- **WHEN** user runs `ccl provider --new --template` (or `ccl provider --new --template deepseek`)
- **THEN** stdout matches what `ccl provider --template` prints; no provider file is created; exit code 0

### Requirement: Cancellation is preserved
The existing EOF-on-stdin cancellation behavior MUST continue to work. Pressing Ctrl-D / piping an empty stdin to `ccl provider --new` MUST exit 0 without writing any file.

#### Scenario: EOF cancels without writing
- **WHEN** user runs `echo "" | ccl provider --new --type deepseek` (or any equivalent EOF)
- **THEN** exit code is 0 (or matches the existing "已取消" semantics) and `~/.cc-launcher/providers/<name>.json` is NOT created

### Requirement: Written file is a complete, valid provider config
After a successful guided flow, the file at `~/.cc-launcher/providers/<name>.json` MUST be valid JSON parseable by the existing `provider.Manager.Get`. A round-trip test MUST verify this for each of the 7 template types.

#### Scenario: written deepseek file parses via provider.Manager.Get
- **WHEN** the deepseek guided flow finishes successfully
- **THEN** `provider.Manager.Get(name)` returns the parsed `map[string]any` without error; the file's `env` block contains all the keys from `templates.ByName("deepseek").Body`

#### Scenario: written aliyun-pay file has substituted WorkspaceId
- **WHEN** the aliyun-pay guided flow finishes successfully with `WorkspaceId=ws-abc`
- **THEN** the file's `ANTHROPIC_BASE_URL` ends with `.cn-beijing.maas.aliyuncs.com/apps/anthropic` and contains the substring `ws-abc` (not `{WorkspaceId}`)

#### Scenario: round-trip test covers all 7 templates
- **WHEN** `go test ./internal/cli/...` runs the round-trip suite
- **THEN** for each of the 7 template types, supplying valid inputs produces a JSON file that re-parses cleanly via `provider.Manager.Get`

