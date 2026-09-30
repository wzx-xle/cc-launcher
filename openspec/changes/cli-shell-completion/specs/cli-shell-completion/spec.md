# cli-shell-completion Specification

## Purpose
TBD - created by archiving change cli-shell-completion. Update Purpose after archive.

## Protocol note (read before scenarios)

`ccl __complete` 收到的 `<args...>` **不包含** `ccl` 自身——这是 wrapper script 的约定
（跟 `kubectl completion` / `gh completion` 风格一致）：shell wrapper 在 spawn 二进制前
已经去掉 `ccl`。下面的 scenario 都按这一约定写。

User 视角的命令是 `ccl <subcmd> <TAB>`，binary 实际收到的是 `ccl __complete <subcmd> <arg>`。

## ADDED Requirements

### Requirement: ccl __complete outputs position-aware completion candidates
`ccl __complete <args...>` MUST print shell completion candidates for the position represented by the last argument. Each candidate MUST be on its own line, with no description or formatting metadata — a pure token stream consumable by bash `compgen` and zsh `compadd`. Errors MUST NOT surface to stdout; the command MUST exit 0 even when no candidates are available.

#### Scenario: completing the top-level subcommand
- **WHEN** user invokes `ccl __complete ""` (empty trailing argument = position under completion)
- **THEN** stdout shows the top-level subcommands (`serve / stats / provider / mode`) plus the help flags (`-h / --help / help`), one per line

#### Scenario: completing ccl serve flags
- **WHEN** user invokes `ccl __complete serve ""` (completing inside `ccl serve`)
- **THEN** stdout shows `ccl serve`'s flags (`--stop / --restore / --status / --restart / -h / --help`), one per line

#### Scenario: completing ccl provider template name after --template
- **WHEN** user invokes `ccl __complete provider --template ""`
- **THEN** stdout shows the names from `internal/templates.All` (anthropic / deepseek / minimax / glm / aliyun-coding / aliyun-pay / aliyun-token), one per line

#### Scenario: completing ccl provider --new --type template name
- **WHEN** user invokes `ccl __complete provider --new --type ""`
- **THEN** stdout shows the same template names as the `--template` position, one per line

#### Scenario: completing ccl stats flags
- **WHEN** user invokes `ccl __complete stats ""`
- **THEN** stdout shows the stats flags (`--since / --by-provider / --by-project / --project / --json / -h / --help`), one per line

#### Scenario: position arguments for ccl provider <name> are NOT completed by ccl
- **WHEN** user invokes `ccl __complete provider ""` (completing the position after `ccl provider`)
- **THEN** stdout is empty (no candidates); the user's shell will fall back to filename completion for `~/.cc-launcher/providers/`

#### Scenario: position after ccl provider --delete is NOT completed
- **WHEN** user invokes `ccl __complete provider --delete ""` (completing the provider name to delete)
- **THEN** stdout is empty (provider name is user's file; shell filename fallback applies)

#### Scenario: unknown subcommand after ccl produces empty output
- **WHEN** user invokes `ccl __complete help ""` (no `completeAtHelp` handler exists)
- **THEN** stdout is empty; the shell falls back to default behavior

#### Scenario: errors do not propagate
- **WHEN** user invokes `ccl __complete` (no args) or `ccl __complete bogus`
- **THEN** stdout is empty; exit code is nonzero ONLY for truly malformed invocations (e.g., unknown internal sub-flag); empty candidates are exit 0

### Requirement: ccl __init <shell> prints a wrapper script that wires up completion
`ccl __init <shell>` MUST print a shell wrapper script to stdout that registers `ccl __complete` as the completion engine. Supported shells: `bash`, `zsh`. Unknown shells MUST cause exit 1 and an actionable stderr message. The script MUST be self-contained (no external file references) so the user can pipe it directly to `source`.

#### Scenario: bash wrapper enables completion via complete -F
- **WHEN** user runs `source <(ccl __init bash)`
- **THEN** bash registers a `_ccl_complete` function calling `ccl __complete "${COMP_WORDS[@]}"` and uses `complete -F _ccl_complete -o default ccl`; subsequent `<Tab>` after `ccl serve --` shows the serve flag list

#### Scenario: zsh wrapper enables completion via compdef
- **WHEN** user runs `source <(ccl __init zsh)`
- **THEN** zsh registers a `_ccl` function that splits `ccl __complete` output by newline (`${(@f)...}`) and feeds candidates to `compadd`; subsequent `<Tab>` after `ccl serve --` shows the serve flag list

#### Scenario: unknown shell returns error
- **WHEN** user runs `ccl __init fish`
- **THEN** stderr shows `unknown shell: fish（支持: bash / zsh）` and exit code is nonzero; stdout is empty

### Requirement: __complete and __init are hidden subcommands
`ccl __complete` and `ccl __init` MUST NOT appear in `ccl --help`'s top-level subcommand list. They are internal shell-completion protocol endpoints.

#### Scenario: top-level help does not list internal subcommands
- **WHEN** user runs `ccl --help`
- **THEN** the subcommand list shows `serve / stats / provider / mode` only; `__complete` / `__init` are absent

### Requirement: completion stays in sync with runtime flag set
When `ccl serve` / `ccl stats` / `ccl provider` adds, removes, or renames a flag, the next `ccl __complete ccl <subcmd> ""` call MUST reflect the change WITHOUT regenerating any external script. This is automatic because completion uses `flag.NewFlagSet` reflection at runtime.

#### Scenario: adding a new flag to ccl serve
- **WHEN** developer adds a new bool flag (e.g., `--rotate-logs`) to `runServe` via `fs.Bool(...)`
- **THEN** `ccl __complete ccl serve ""` automatically includes `--rotate-logs` in its output on the next invocation; no separate completion config file to update

### Requirement: completion respects the mutex of serve flags
`ccl serve` has 4 mutually exclusive flags (`--stop / --restore / --status / --restart`). When the user has already typed one of them, `ccl __complete ccl serve <existing-flag> ""` MUST NOT suggest any of the other 3 (defensive — same mutex as runtime enforces).

#### Scenario: completing serve flags after --stop is already given
- **WHEN** user invokes `ccl __complete serve --stop ""`
- **THEN** stdout is empty (the 4 flags are mutually exclusive; no further flag completion is meaningful)

### Requirement: tab completion is opt-in, not first-time
The user MUST run `source <(ccl __init bash)` (or zsh equivalent) explicitly to enable completion. `ccl --help` / `ccl serve --help` SHOULD mention the option but MUST NOT enable it automatically.

#### Scenario: completion is not enabled by default
- **WHEN** user runs `ccl serve --<Tab>` without prior `__init` source
- **THEN** shell's default completion behavior runs (typically no candidates); ccl does not auto-register any completion

#### Scenario: topHelp mentions the enable command
- **WHEN** user runs `ccl --help`
- **THEN** the help output mentions `source <(ccl __init bash)` (or zsh) under a "Tab completion" paragraph