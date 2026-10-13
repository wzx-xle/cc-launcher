## MODIFIED Requirements

### Requirement: Each interactive step prints its purpose
Before reading input for a step, `ccl provider --new` MUST print a one-line explanation of what the step is for and what value to enter. The explanation MUST come from `templates.Hint` for the active template (no hardcoded strings in `cli.go`). The number of **template field prompts** MUST equal `len(template.Fields)` plus 1 (the leading "provider name" step). After the file is written, `ccl provider --new` MAY add additional optional metadata steps (see ADDED Requirement "ccl provider --new optionally prompts for note and displays suggested order after write"); these metadata steps are not counted in the "template field prompts" total and are not subject to this Requirement's hint-from-template rule.

#### Scenario: prompt includes the step explanation
- **WHEN** user runs `ccl provider --new --type deepseek` and the flow reaches the `ANTHROPIC_BASE_URL` step
- **THEN** the prompt printed before reading input contains the BASE_URL hint from `templates.ByName("deepseek").Hints`

#### Scenario: type-less interactive flow shows the template selection hint
- **WHEN** user runs `ccl provider --new` (no `--type`)
- **THEN** the flow first lists the 7 available templates with one-line descriptions, then prompts for the type

#### Scenario: deepseek flow has 8 template field prompts (1 name + 7 Fields)
- **WHEN** user runs `ccl provider --new --type deepseek`
- **THEN** the flow iterates 8 template field prompts in order: provider name, ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, ANTHROPIC_MODEL, ANTHROPIC_DEFAULT_OPUS_MODEL, ANTHROPIC_DEFAULT_SONNET_MODEL, ANTHROPIC_DEFAULT_HAIKU_MODEL, CLAUDE_CODE_SUBAGENT_MODEL. After the file is written, the flow may continue with the optional metadata steps.

#### Scenario: glm flow has 9 template field prompts (1 name + 8 Fields)
- **WHEN** user runs `ccl provider --new --type glm`
- **THEN** the flow iterates 9 template field prompts in order: provider name, ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, ANTHROPIC_DEFAULT_HAIKU_MODEL, ANTHROPIC_DEFAULT_SONNET_MODEL, ANTHROPIC_DEFAULT_OPUS_MODEL, CLAUDE_CODE_AUTO_COMPACT_WINDOW, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC, API_TIMEOUT_MS. After the file is written, the flow may continue with the optional metadata steps.

#### Scenario: anthropic flow has 3 template field prompts (1 name + 2 Fields)
- **WHEN** user runs `ccl provider --new --type anthropic`
- **THEN** the flow iterates 3 template field prompts: provider name, ANTHROPIC_AUTH_TOKEN (plus an optional auto-confirmation that no BASE_URL is set). After the file is written, the flow may continue with the optional metadata steps.

## ADDED Requirements

### Requirement: ccl provider --new optionally prompts for note and displays suggested order after write
After `ccl provider --new` successfully writes the provider JSON file, the flow MUST continue with two **optional** metadata steps before exiting:

1. **note prompt**: print `note (可选备注，回车跳过): ` and read one line of input. If the input is empty (or contains only whitespace), the file is left with no `note` field. If non-empty, the file MUST be updated so its top-level `note` field equals the trimmed input string.
2. **order hint**: print `排序值: <N> (现有 max=<M>, 建议 max+10；手改 JSON order 字段覆盖)` where `<M>` is the maximum `Order` value across all **pre-existing** provider config files (the new file MUST NOT be considered, since `List()` is not re-read between write and hint). `<N>` is `<M>+10`. The flow MUST NOT read input for this step.

The two metadata steps MUST be skipped if the file write itself failed. The "note prompt" MUST be cancellable by the same EOF-on-stdin mechanism as the rest of the flow (EOF on this step cancels the whole flow per the "Cancellation is preserved" Requirement).

#### Scenario: empty note input leaves file unchanged
- **WHEN** user runs `ccl provider --new --type deepseek` and reaches the post-write `note` prompt
- **AND** the user presses Enter without typing anything (empty input)
- **THEN** the written file at `~/.cc-launcher/providers/<name>.json` MUST NOT contain a top-level `note` field; the order hint line is printed and the flow exits 0

#### Scenario: non-empty note input updates the file
- **WHEN** user runs `ccl provider --new --type deepseek` and reaches the post-write `note` prompt
- **AND** the user types `国内 / 按量计费` followed by Enter
- **THEN** the written file at `~/.cc-launcher/providers/<name>.json` MUST contain a top-level `note` field whose value is `"国内 / 按量计费"` (the trimmed input); the `env` block and other top-level fields are preserved unchanged
- **AND** `provider.Manager.Get(name)` parses the file without error and returns a map where `note == "国内 / 按量计费"`

#### Scenario: order hint shows max-plus-ten
- **WHEN** user runs `ccl provider --new --type deepseek` from a providers dir where the pre-existing `bravo.json` has `order = 5` and `alpha.json` has `order = 20`
- **THEN** the post-write `排序值:` line prints `排序值: 30 (现有 max=20, 建议 max+10；手改 JSON order 字段覆盖)` and the flow does NOT read further input

#### Scenario: order hint uses zero baseline when no pre-existing providers
- **WHEN** user runs `ccl provider --new --type deepseek` from an empty providers dir
- **THEN** the post-write `排序值:` line prints `排序值: 10 (现有 max=0, 建议 max+10；手改 JSON order 字段覆盖)` (or any equivalent phrasing that signals `max=0` baseline → suggested `10`)

#### Scenario: order hint excludes the just-written file
- **WHEN** user runs `ccl provider --new --type deepseek` twice in a row (first creates a provider without an explicit `order` field, so it parses as `Order=0`)
- **THEN** the second run's `排序值:` hint still uses `max` computed from providers **before** the second write, NOT from the just-written first provider (the hint is not re-read from disk between write and print)

#### Scenario: EOF on note prompt skips the note (does not roll back the file)
- **WHEN** the post-write `note` prompt receives EOF (Ctrl-D or piped stdin exhaustion) before the user supplies a line
- **THEN** the `note` field is treated as empty (skipped) and the flow proceeds to the `order` hint step. The file at `~/.cc-launcher/providers/<name>.json` (already written before the note prompt) is preserved unchanged. This is intentionally different from the "Cancellation is preserved" Requirement — that Requirement governs EOF during **template field prompts** (where no file has been written yet), whereas the note prompt runs **after** the file is on disk and cannot be rolled back without introducing a separate delete-on-EOF path.

#### Scenario: file write failure skips metadata steps
- **WHEN** `ccl provider --new` fails to write the provider JSON file (e.g., disk full, permission denied)
- **THEN** the `note` prompt and `order` hint MUST NOT be printed; the flow returns the underlying write error
