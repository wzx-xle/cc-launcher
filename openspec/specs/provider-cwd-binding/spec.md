# provider-cwd-binding Specification

## Purpose
TBD - created by archiving change provider-setenv-cwd-binding. Update Purpose after archive.
## Requirements
### Requirement: state.json maintains dir_bindings map
The state manager SHALL persist a `dir_bindings map[string]string` field in `~/.cc-launcher/state.json`. The map key SHALL be the absolute working directory (`os.Getwd()` result) and the value SHALL be the provider name. The field SHALL be absent (or empty) when no bindings are configured.

#### Scenario: reading dir_bindings when none exist
- **WHEN** `state.json` exists but has no `dir_bindings` field
- **THEN** `GetProviderForDir(wd)` SHALL return `""` (empty string) and the TUI runner SHALL fall back to `GetActiveProvider()`

#### Scenario: writing a new binding
- **WHEN** `SetProviderForDir(wd, "deepseek")` is called for a working directory not yet in the map
- **THEN** `state.json` is updated with `dir_bindings[wd] = "deepseek"` and the change is persisted atomically

#### Scenario: updating an existing binding
- **WHEN** `SetProviderForDir(wd, "anthropic")` is called for a working directory already mapped to `"deepseek"`
- **THEN** `state.json` is updated with `dir_bindings[wd] = "anthropic"` (the previous binding is overwritten)

#### Scenario: dir_bindings field is preserved on other state writes
- **WHEN** `SetActiveProvider("anthropic")` or `SetPermissionProposal(wd, mode)` is called
- **THEN** `dir_bindings` content SHALL be preserved unchanged

### Requirement: TUI prefers cwd binding over active_provider
When the TUI launches, it SHALL resolve the default provider in this order:
1. `dir_bindings[wd]` if present (cwd binding wins)
2. `active_provider` from state.json (global fallback)

The resolved value SHALL be passed to the TUI as the pre-selected provider.

#### Scenario: cwd is in dir_bindings
- **WHEN** `os.Getwd()` returns `/home/u/work/proj-a` and `state.json` has `dir_bindings["/home/u/work/proj-a"] = "deepseek"`
- **THEN** the TUI pre-selects `deepseek` as the default (cursor position / highlighted item)

#### Scenario: cwd is not in dir_bindings
- **WHEN** `os.Getwd()` returns `/home/u/work/new-dir` and `state.json` has no entry for it but `active_provider = "anthropic"`
- **THEN** the TUI pre-selects `anthropic` as the default

#### Scenario: TUI user picks a different provider
- **WHEN** the TUI pre-selects `deepseek` from cwd binding but the user picks `anthropic`
- **THEN** the TUI result contains `result.Provider = "anthropic"`, the runner delivers `anthropic`'s env through the `--settings` parameter, and `SetActiveProvider` records the selection

### Requirement: TUI offers to remember cwd binding
After the user confirms provider and permission mode selections in the TUI, the TUI SHALL prompt `记住当前目录 (<wd>) 默认用 <provider> 吗？[Y/n]`. The prompt SHALL NOT appear if the user picked the same provider already bound to this cwd (no change to record).

#### Scenario: User accepts prompt
- **WHEN** the TUI result provider differs from the current cwd binding (or no binding exists) and the user enters `Y` (or hits Enter)
- **THEN** `SetProviderForDir(wd, result.Provider)` SHALL be called and `dir_bindings[wd]` SHALL equal `result.Provider` on next read

#### Scenario: User declines prompt
- **WHEN** the TUI result provider differs from the current cwd binding and the user enters `n`
- **THEN** `dir_bindings` SHALL remain as it was before (no write to `state.json` for this cwd)

#### Scenario: TUI cancelled before selection
- **WHEN** the user presses Esc / Ctrl+C during TUI selection
- **THEN** the "remember" prompt SHALL NOT appear (Esc cancels the whole flow)

#### Scenario: Selection matches existing binding
- **WHEN** `dir_bindings[wd]` already equals the selected provider
- **THEN** the prompt SHALL be skipped (no need to ask "remember this?" when nothing changes)

### Requirement: CLI provider command updates cwd binding
The `ccl provider <name>` CLI command SHALL call `SetProviderForDir(wd, name)` where `wd = os.Getwd()` at command entry, in addition to calling `SetActiveProvider(name)`. This aligns CLI behavior with the TUI flow.

#### Scenario: CLI provider switches and binds cwd
- **WHEN** the user runs `ccl provider deepseek` from `/home/u/work/proj-a`
- **THEN** `state.json` SHALL have `active_provider = "deepseek"` AND `dir_bindings["/home/u/work/proj-a"] = "deepseek"` after the command exits

#### Scenario: CLI provider from outside any tracked cwd
- **WHEN** the user runs `ccl provider deepseek` from `/tmp/one-off`
- **THEN** `dir_bindings["/tmp/one-off"] = "deepseek"` SHALL be written (one-off directories are still tracked)

### Requirement: ccl provider --list marks cwd binding with *
When listing providers, `ccl provider --list` SHALL mark the provider bound to the current cwd with `*` prefix, in addition to marking `active_provider` with `▶` prefix. The `*` marker SHALL take precedence in display (cwd binding is the most specific).

#### Scenario: cwd binding exists and differs from active_provider
- **WHEN** `dir_bindings[wd] = "deepseek"` and `active_provider = "anthropic"`
- **THEN** the list output shows:
```
* deepseek      ← cwd 绑定
▶ anthropic     ← 全局 active
  aliyun-coding
  ...
```

#### Scenario: cwd binding equals active_provider
- **WHEN** both `dir_bindings[wd]` and `active_provider` equal `"anthropic"`
- **THEN** the `anthropic` row SHALL display `*▶` (both markers stacked) since cwd binding and global default coincide

#### Scenario: no cwd binding
- **WHEN** `dir_bindings` is empty OR has no entry for `wd`
- **THEN** only `▶` marks `active_provider`; no `*` marker anywhere

