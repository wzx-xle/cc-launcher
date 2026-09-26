## ADDED Requirements

### Requirement: Permission mode list is the single source of truth in internal/modes
The system SHALL expose mode metadata (name + Chinese description) through `internal/modes`. Both `internal/cli` and `internal/tui` MUST consume from this package — they MUST NOT hold their own hardcoded list.

#### Scenario: cli uses modes.Names() for validation
- **WHEN** `cc mode <mode>` is invoked with an arbitrary value
- **THEN** validation iterates `modes.Names()` and accepts only modes listed there

#### Scenario: tui renders from modes.All()
- **WHEN** the TUI shows the permission-mode selector
- **THEN** it iterates `modes.All()` and displays each mode's `Name` and `Description`

### Requirement: modes package self-consistency
The system SHALL guarantee that `modes.Names()` is a permutation of the mode list, each entry has a non-empty Chinese description, names are unique, and `ByName` returns the matching entry or `ok=false` for misses.

#### Scenario: Names() is in lockstep with All()
- **WHEN** `go test ./internal/modes/...` runs
- **THEN** `TestNamesMatchAll` passes: `Names()[i] == all[i].Name` for every index

#### Scenario: every mode has a non-empty Chinese description
- **WHEN** `go test ./internal/modes/...` runs
- **THEN** `TestModesHaveDescriptions` passes: every entry in `all` has a non-whitespace `Description`

#### Scenario: mode names are unique
- **WHEN** `go test ./internal/modes/...` runs
- **THEN** `TestNamesAreUnique` passes: no duplicate `Name` in `all`

#### Scenario: ByName hit and miss
- **WHEN** `go test ./internal/modes/...` runs
- **THEN** `TestByName` passes: `ByName("plan")` returns `ok=true`; `ByName("definitely-not-A-mode")` returns `ok=false`

### Requirement: Contract test pins valid modes to Claude Code CLI via behavior probe
The system SHALL provide a build-tag-isolated test (`//go:build contract`) that probes the locally installed `claude` with an invalid `--permission-mode` value and parses the `Allowed choices are ...` line, comparing it to `modes.Names()`. Drift SHALL be caught with a message listing the modes to add/remove.

#### Scenario: claude CLI has the same choices as modes.Names()
- **WHEN** `go test -tags contract ./...` runs on a host with `claude` installed
- **THEN** `TestContract_PermissionModesMatchClaudeCLI` passes (no missing, no extra modes)

#### Scenario: claude CLI is missing
- **WHEN** `go test -tags contract ./...` runs on a host without `claude` in PATH
- **THEN** the test is SKIPPED, not failed

#### Scenario: probe output cannot be parsed
- **WHEN** `claude --permission-mode <probe>` does not produce a line matching `Allowed choices are ...`
- **THEN** the test fails with a descriptive error including the full `claude` output for diagnosis

#### Scenario: claude unexpectedly accepts the probe value
- **WHEN** `claude --permission-mode __cc_launcher_probe__` exits 0 (probe value becomes valid)
- **THEN** the test fails with a clear "cannot probe choices" message — a contract violation

### Requirement: Mode descriptions accurately reflect Claude Code semantics
The system SHALL describe each permission mode in `modes.All()` using semantics that match Claude Code 2.1.x. Specifically, `dontAsk` MUST be described as the strictest non-interactive mode (refusing operations not in the allow list), NOT as "auto-approve".

#### Scenario: dontAsk description says "refuse", not "auto-approve"
- **WHEN** a user reads the TUI mode list or README permission mode table
- **THEN** the `dontAsk` row's description mentions refusal/non-interactive, not unconditional approval