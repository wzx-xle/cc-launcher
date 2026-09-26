## ADDED Requirements

### Requirement: Permission mode list is exported and stable
The system SHALL expose `cli.ValidModes` (exported) as the single source of truth for the project's hardcoded list of Claude Code `--permission-mode` choices.

#### Scenario: cli.ValidModes is importable from other packages
- **WHEN** `internal/tui/modes_test.go` imports `cc-launcher/internal/cli`
- **THEN** it can reference `cli.ValidModes` without panic

### Requirement: TUI mode list mirrors CLI valid modes
The system SHALL keep the TUI's `modeItems` mode set in lockstep with `cli.ValidModes`. Drift SHALL be caught by the test suite before merge.

#### Scenario: TUI modeItems is a permutation of cli.ValidModes
- **WHEN** `go test ./internal/tui/...` runs
- **THEN** `TestModeItemsMatchCLIValidModes` passes: the sorted mode set of `modeItems` equals the sorted `cli.ValidModes`

#### Scenario: every TUI mode has a non-empty Chinese description
- **WHEN** `go test ./internal/tui/...` runs
- **THEN** `TestModeItemsHaveDescriptions` passes: every entry in `modeItems` has a non-whitespace `desc`

### Requirement: Contract test pins valid modes to Claude Code CLI
The system SHALL provide a build-tag-isolated test (`//go:build contract`) that parses the actual `--permission-mode` choices from the locally installed `claude --help` and compares them to `cli.ValidModes`. Drift SHALL be caught with a message listing the modes to add/remove.

#### Scenario: claude CLI has the same choices as cli.ValidModes
- **WHEN** `go test -tags contract ./...` runs on a host with `claude` installed
- **THEN** `TestContract_PermissionModesMatchClaudeCLI` passes (no missing, no extra modes)

#### Scenario: claude CLI is missing
- **WHEN** `go test -tags contract ./...` runs on a host without `claude` in PATH
- **THEN** the test is SKIPPED, not failed

#### Scenario: help text cannot be parsed
- **WHEN** `claude --help` output does not contain a `(choices: ...)` segment for `--permission-mode`
- **THEN** the test fails with a descriptive error including the raw `--permission-mode` block for diagnosis

### Requirement: Mode descriptions accurately reflect Claude Code semantics
The system SHALL describe each permission mode in TUI and README using semantics that match Claude Code 2.1.x. Specifically, `dontAsk` MUST be described as the strictest non-interactive mode (refusing operations not in the allow list), NOT as "auto-approve".

#### Scenario: dontAsk description says "refuse", not "auto-approve"
- **WHEN** a user reads the TUI mode list or README permission mode table
- **THEN** the `dontAsk` row's description mentions refusal/non-interactive, not unconditional approval