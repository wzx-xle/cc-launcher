## ADDED Requirements

### Requirement: ccl recognizes top-level --help
The `ccl` binary MUST recognize `-h` and `--help` (and bare `help`) as a top-level flag and print a usage summary to stdout. Exit code MUST be 0.

#### Scenario: ccl --help prints usage and exits 0
- **WHEN** user runs `ccl --help`
- **THEN** stdout contains a usage summary listing the top-level subcommands (TUI entry, `provider`, `mode`) and exits with code 0

#### Scenario: ccl -h also prints usage
- **WHEN** user runs `ccl -h`
- **THEN** same usage text as `--help` is printed and exit code is 0

#### Scenario: ccl help (no flag) prints usage
- **WHEN** user runs `ccl help` (bare word, no dashes)
- **THEN** same usage text is printed and exit code is 0

### Requirement: ccl provider recognizes --help
`ccl provider --help` MUST print provider-subcommand usage, including the available flags (`--list`, `--new`, `--delete`, `--template`) and a hint pointing at `ccl provider --template <type>` for the per-type JSON template.

#### Scenario: ccl provider --help prints subcommand usage
- **WHEN** user runs `ccl provider --help`
- **THEN** stdout lists `--list`, `--new`, `--delete`, `--template` flags with one-line descriptions and mentions `ccl provider --template` for template discovery; exit code 0

### Requirement: ccl provider --template accepts a short alias -T
The `ccl provider --template` flag MUST also be reachable by the short alias `-T` (capital T). `-T` MUST NOT collide with any other flag. The short alias MUST be listed in `ccl provider --help` output.

#### Scenario: ccl provider -T <type> behaves like --template
- **WHEN** user runs `ccl provider -T aliyun-coding`
- **THEN** stdout matches what `ccl provider --template aliyun-coding` prints; exit code 0

#### Scenario: ccl provider -T (no type) lists supported types
- **WHEN** user runs `ccl provider -T` with no type
- **THEN** stdout lists the 7 supported template types with one-line descriptions; exit code 0

#### Scenario: ccl provider -T bogus fails clearly
- **WHEN** user runs `ccl provider -T bogus`
- **THEN** exit code is non-zero and stderr explains the supported types

#### Scenario: ccl provider --help works without any providers configured
- **WHEN** user runs `ccl provider --help` with an empty providers directory
- **THEN** the same usage text is printed; the help path MUST NOT depend on `~/.cc-launcher/providers/` state

### Requirement: ccl mode recognizes --help
`ccl mode --help` MUST print mode-subcommand usage, listing that `ccl mode` shows current modes and `ccl mode <name>` sets a per-directory default.

#### Scenario: ccl mode --help prints subcommand usage
- **WHEN** user runs `ccl mode --help`
- **THEN** stdout explains the subcommand with one-line description per flag and exits 0

### Requirement: --help does not swallow real unknown-flag errors
The `--help` short-circuit MUST NOT interfere with error reporting for genuinely unknown flags. If a user types `--bogus` (not a recognized flag), the system MUST continue to return a non-zero exit with a "未知选项" style error.

#### Scenario: --bogus still returns error
- **WHEN** user runs `ccl provider --bogus`
- **THEN** exit code is non-zero and stderr contains a "未知选项" style error pointing at `--bogus`

#### Scenario: ccl --unknown-sub still returns error
- **WHEN** user runs `ccl --unknown-sub`
- **THEN** exit code is non-zero and stderr contains a "未知选项" style error pointing at `--unknown-sub`