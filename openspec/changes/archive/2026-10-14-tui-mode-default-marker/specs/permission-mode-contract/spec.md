## ADDED Requirements

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
