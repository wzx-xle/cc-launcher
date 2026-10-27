## MODIFIED Requirements

### Requirement: ccl __complete outputs position-aware completion candidates
`ccl __complete <args...>` MUST print shell completion candidates for the position represented by the last argument. Each candidate MUST be on its own line, with no description or formatting metadata — a pure token stream consumable by bash `compgen` and zsh `compadd`. Errors MUST NOT surface to stdout; the command MUST exit 0 even when no candidates are available.

#### Scenario: completing the top-level subcommand
- **WHEN** user invokes `ccl __complete ""` (empty trailing argument = position under completion)
- **THEN** stdout shows the top-level subcommands (`serve / stats / web / provider / mode`) plus the help flags (`-h / --help / help`), one per line

#### Scenario: completing ccl serve flags
- **WHEN** user invokes `ccl __complete serve ""` (completing inside `ccl serve`)
- **THEN** stdout shows `ccl serve`'s flags (`--stop / --restore / --status / --restart / --backfill / -h / --help`), one per line

#### Scenario: completing ccl provider template name after --template
- **WHEN** user invokes `ccl __complete provider --template ""`
- **THEN** stdout shows the names from `internal/templates.All` (anthropic / deepseek / minimax / glm / aliyun-coding / aliyun-pay / aliyun-token), one per line

#### Scenario: completing ccl provider --new --type template name
- **WHEN** user invokes `ccl __complete provider --new --type ""`
- **THEN** stdout shows the same template names as the `--template` position, one per line

#### Scenario: completing ccl stats flags
- **WHEN** user invokes `ccl __complete stats ""`
- **THEN** stdout shows the stats flags (`--day / --by-provider / --by-project / --by-model / --project / --model / --provider / --json / -h / --help`), one per line

#### Scenario: position arguments for ccl provider <name> are NOT completed by ccl
- **WHEN** user invokes `ccl __complete provider ""` (completing the position after `ccl provider`)
- **THEN** stdout is empty (no candidates); the user's shell will fall back to filename completion for `~/.cc-launcher/providers/`

#### Scenario: position arguments for ccl provider --delete are NOT completed by ccl
- **WHEN** user invokes `ccl __complete provider --delete ""` (completing the provider name to delete)
- **THEN** stdout is empty (provider name is user's file; shell filename fallback applies)

#### Scenario: unknown subcommand after ccl produces empty output
- **WHEN** user invokes `ccl __complete help ""` (no `completeAtHelp` handler exists)
- **THEN** stdout is empty; the shell falls back to default behavior

#### Scenario: errors do not propagate
- **WHEN** user invokes `ccl __complete` (no args) or `ccl __complete bogus`
- **THEN** stdout is empty; exit code is nonzero ONLY for truly malformed invocations (e.g., unknown internal sub-flag); empty candidates are exit 0

### Requirement: completion respects the mutex of serve flags
`ccl serve` has 5 mutually exclusive flags (`--stop / --restore / --status / --restart / --backfill`). When the user has already typed one of them, `ccl __complete ccl serve <existing-flag> ""` MUST NOT suggest any of the others (defensive — same mutex as runtime enforces).

#### Scenario: completing serve flags after --stop is already given
- **WHEN** user invokes `ccl __complete serve --stop ""`
- **THEN** stdout is empty

### Requirement: completion covers the ccl web flags
`ccl __complete web ""` MUST print the flags of the `ccl web` subcommand (`--rotate / --open / -h / --help`), one per line, and MUST exit 0.

#### Scenario: completing ccl web flags
- **WHEN** user invokes `ccl __complete web ""`
- **THEN** stdout shows `ccl web`'s flags (`--rotate / --open / -h / --help`), one per line

#### Scenario: the web flag list stays in sync with the runtime flag set
- **WHEN** developer adds a new flag to the `ccl web` flag set via `fs.Bool(...)` / `fs.String(...)`
- **THEN** `ccl __complete web ""` automatically includes it on the next invocation, with no separate completion configuration to update
