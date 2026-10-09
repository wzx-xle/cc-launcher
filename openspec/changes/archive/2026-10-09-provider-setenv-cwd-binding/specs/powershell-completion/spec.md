# powershell-completion Specification

## Purpose
TBD - created by archiving change provider-setenv-cwd-binding. Update Purpose after archive.
## ADDED Requirements

### Requirement: __init powershell outputs Register-ArgumentCompleter script
`ccl __init powershell` SHALL print a PowerShell script to stdout. The script SHALL use `Register-ArgumentCompleter` to register a tab-completion handler for the `ccl` command. The handler SHALL delegate to `ccl __complete <args>` (the existing completion protocol) for each completion request.

#### Scenario: __init powershell exit code
- **WHEN** the user runs `ccl __init powershell`
- **THEN** the command SHALL exit 0 and stdout SHALL contain a valid PowerShell script (starts with `Register-ArgumentCompleter` or `$ccl = ...`)

#### Scenario: __init powershell unknown shell error
- **WHEN** `ccl __init <other-shell>` is called with a shell other than `bash`, `zsh`, or `powershell`
- **THEN** the command SHALL exit non-zero and stderr SHALL contain an error message identifying the supported shells

#### Scenario: script uses -Native flag
- **WHEN** the script runs in `$PROFILE`
- **THEN** `Register-ArgumentCompleter -Native -CommandName 'ccl'` SHALL be used (ccl is a native executable, not a cmdlet)

#### Scenario: script invokes __complete for candidates
- **WHEN** the user presses Tab after `ccl prov<TAB>`
- **THEN** the registered ScriptBlock SHALL invoke `ccl __complete ccl prov` and parse the output (one candidate per line) as completion candidates

### Requirement: Script is self-contained
The PowerShell script SHALL NOT depend on external files. It SHALL work standalone when copied into `$PROFILE` via `Invoke-Expression`.

#### Scenario: script copied into $PROFILE
- **WHEN** the user adds `Invoke-Expression -Command (ccl __init powershell)` to `$PROFILE` and reopens the shell
- **THEN** Tab completion for `ccl <TAB>` SHALL produce candidates

#### Scenario: script piped into Invoke-Expression
- **WHEN** the user runs `ccl __init powershell | Out-String | Invoke-Expression` interactively
- **THEN** Tab completion SHALL work for the current session without modifying `$PROFILE`

### Requirement: Completion scope matches bash/zsh
The PowerShell completion handler SHALL offer the same candidates as the existing bash/zsh handlers (via the shared `__complete` protocol).

#### Scenario: top-level completion
- **WHEN** the user types `ccl <TAB>` in PowerShell
- **THEN** candidates SHALL be `serve`, `stats`, `provider`, `mode`, `-h`, `--help`, `help` (matching bash output)

#### Scenario: provider --template completion
- **WHEN** the user types `ccl provider --template <TAB>` in PowerShell
- **THEN** candidates SHALL be `anthropic`, `deepseek`, `minimax`, `glm`, `aliyun-coding`, `aliyun-pay`, `aliyun-token` (matching bash output)

#### Scenario: provider --new --type completion
- **WHEN** the user types `ccl provider --new --type <TAB>` in PowerShell
- **THEN** candidates SHALL match the 7 provider template names (same as `ccl provider --template <TAB>`)

#### Scenario: mode completion
- **WHEN** the user types `ccl mode <TAB>` in PowerShell
- **THEN** candidates SHALL be the 6 permission modes (`manual`, `acceptEdits`, `auto`, `bypassPermissions`, `dontAsk`, `plan`)

### Requirement: filter on wordToComplete prefix
The PowerShell completion handler SHALL only emit candidates whose text starts with the user's current word (`$wordToComplete`).

#### Scenario: partial word filter
- **WHEN** `$wordToComplete = "pr"` and the user types `ccl pr<TAB>`
- **THEN** only candidates starting with `pr` SHALL be offered (e.g., `provider`); other top-level commands SHALL be filtered out

### Requirement: documented install path in topHelp
The top-level `ccl --help` output SHALL mention PowerShell alongside bash/zsh for tab completion setup.

#### Scenario: topHelp includes powershell
- **WHEN** `ccl --help` is printed
- **THEN** the "Tab completion" section SHALL include a line like `powershell 用户：在 $PROFILE 加一行   Invoke-Expression -Command (ccl __init powershell)` alongside the existing bash/zsh lines

#### Scenario: wordToComplete contains wildcard metacharacters
- **WHEN** `$wordToComplete` contains PowerShell `-like` metacharacters (`*`, `?`, `[`, `]`, e.g. user types `ccl proj*<TAB>`)
- **THEN** the ScriptBlock SHALL treat `$wordToComplete` as a literal prefix when filtering candidates — use ordinal `StartsWith` (`$line.StartsWith($wordToComplete, [System.StringComparison]::OrdinalIgnoreCase)`) instead of `$line -like "$wordToComplete*"`. This prevents wildcard expansion from inflating or hiding the candidate list when the user's token contains glob metacharacters.