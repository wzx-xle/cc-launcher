## ADDED Requirements

### Requirement: ccl recognizes the web subcommand and its --help
The `ccl` binary MUST recognize `web` as a top-level subcommand and MUST list it in the top-level usage summary. `ccl web --help` MUST print usage for the dashboard entry point, including its flags (`--rotate`, `--backfill`, `--open`) with a one-line description of each, and MUST state that the subcommand prints the dashboard access URL.

#### Scenario: ccl web --help prints web-subcommand usage
- **WHEN** user runs `ccl web --help`
- **THEN** stdout explains the subcommand, lists `--rotate / --backfill / --open` with one-line descriptions each, and exits 0

#### Scenario: the top-level help lists the web subcommand
- **WHEN** user runs `ccl --help`
- **THEN** the subcommand list includes `web` with a one-line description

#### Scenario: ccl web --help works without a running daemon
- **WHEN** user runs `ccl web --help` on a machine where the daemon is not running
- **THEN** the same usage text is printed; the help path MUST NOT depend on daemon state or on a token file
