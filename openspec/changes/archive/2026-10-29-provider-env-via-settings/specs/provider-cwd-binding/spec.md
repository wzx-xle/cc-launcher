## MODIFIED Requirements

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
