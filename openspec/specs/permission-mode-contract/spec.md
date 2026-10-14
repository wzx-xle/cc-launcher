# permission-mode-contract Specification

## Purpose
TBD - created by archiving change cross-platform-launcher-and-mode-contract. Update Purpose after archive.
## Requirements
### Requirement: Permission mode list is the single source of truth in internal/modes
The system SHALL expose mode metadata (name + Chinese description) through `internal/modes`. Both `internal/cli` and `internal/tui` MUST consume from this package — they MUST NOT hold their own hardcoded list.

#### Scenario: cli uses modes.Names() for validation
- **WHEN** `ccl mode <mode>` is invoked with an arbitrary value
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

### Requirement: TUI default mode is marked with a leading dim asterisk, mirroring the provider list pattern

In the TUI permission-mode list, the entry whose `Name` equals the resolved default mode (`modes.All()` entry matching the workDir-based `defaultMode` argument passed to `tui.Run`) MUST be rendered with a dim-styled `[*]` marker in the leading two-character slot. Non-default entries MUST be rendered with `[ ]`. The default marker uses `dimStyle` (grey); the cursor row continues to be rendered with `cursorStyle` (highlight) and the `[*]` / `[ ]` distinction remains visible (cursor highlights the entire line; the default `[*]` is visible in the leading slot when the default entry is not the cursor).

The trailing "默认" text tag SHALL NOT be rendered in the mode list (it is replaced by the leading dim `[*]` marker, mirroring `spec/provider-list-rendering` §"TUI pre-selected provider is marked with a dim asterisk").

The marker three-state logic SHALL be identical to `renderProviderView`: the cursor branch (`if i == m.cursor { marker = "[*]" }`) overrides the default branch, so when the cursor sits on the default row the marker is bright `[*]` (still recognizable as `[*]`; the dim color is consumed by the cursor row's `cursorStyle`).

#### Scenario: Default mode not at cursor shows dim [*]
- **WHEN** the TUI resolves `defaultMode = "plan"` and the cursor starts on `plan` (top of `modes.All()`), and the user presses `↓` once to move the cursor to `acceptEdits`
- **THEN** the `plan` row's leading slot shows `[*]` rendered in `dimStyle`, the `acceptEdits` row's leading slot shows `[ ]`, and the cursor row (`acceptEdits`) is wrapped in `cursorStyle`; the `plan` row MUST NOT contain a trailing `默认` text tag

#### Scenario: Default mode is the cursor
- **WHEN** the TUI resolves `defaultMode = "auto"` and the cursor is positioned on `auto`
- **THEN** the `auto` row's leading slot shows `[*]` (its color is overridden by the `cursorStyle` row-highlight; the marker shape `[*]` is still recognizable), and no other row shows `[*]`; the `auto` row MUST NOT contain a trailing `默认` text tag

#### Scenario: No default mode available
- **WHEN** `defaultMode` is empty or does not match any entry in `modes.All()` (e.g. after `sm.GetPermissionMode(wd)` returns `""` and `tui.Run` falls back to `"manual"`, then a future mode is removed from `modes.All()`)
- **THEN** no row's leading slot shows `[*]`; all rows show `[ ]` until the user moves the cursor; no row carries a trailing `默认` text tag

#### Scenario: Mode row carries description in dim style
- **WHEN** the TUI renders a mode whose `Description` is non-empty and is NOT the default mode
- **THEN** the rendered line contains the `[*]` / `[ ]` leading marker, the mode name, then two spaces, then the description rendered in `dimStyle` prefixed by an em dash (`—`); no `默认` text tag is appended

#### Scenario: Mode row without default marker and without description shows minimal layout
- **WHEN** the TUI renders a mode whose `Description` is non-empty and IS the default mode (cursor on a different row)
- **THEN** the rendered line contains the dim `[*]` leading marker, the mode name, then two spaces, then the description rendered in `dimStyle`; no `默认` text tag is appended (the leading `[*]` already conveys the default semantics)

