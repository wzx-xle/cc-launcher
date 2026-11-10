## MODIFIED Requirements

### Requirement: ccl recognizes the web subcommand and its --help
The `ccl` binary MUST recognize `web` as a top-level subcommand and MUST list it in the top-level usage summary. `ccl web --help` MUST print usage for the dashboard entry point, including its flags (`--rotate`, `--open`) with a one-line description of each, and MUST state that the subcommand prints the dashboard access URL.

The web subcommand's flags MUST all be about the dashboard itself: printing the access URL, rotating the access token, and opening the browser. Actions that maintain the daemon's stored data — currently the historical `cwd` backfill — MUST NOT appear under the web subcommand.

`ccl serve --help` MUST list `--backfill` with a one-line description, alongside the daemon maintenance actions it already documents.

`ccl serve --help` MUST list `--compact` with a one-line description of the database compaction, alongside the other daemon maintenance actions.

`ccl serve` MUST treat `--compact` as one of its mutually exclusive maintenance actions, together with `--stop`, `--restore`, `--status`, `--restart` and `--backfill`. Giving two of them MUST fail with a usage message naming the mutually exclusive flags.

#### Scenario: ccl web --help prints web-subcommand usage
- **WHEN** user runs `ccl web --help`
- **THEN** stdout explains the subcommand, lists `--rotate / --open` with one-line descriptions each, and exits 0

#### Scenario: the top-level help lists the web subcommand
- **WHEN** user runs `ccl --help`
- **THEN** the subcommand list includes `web` with a one-line description

#### Scenario: ccl web --help works without a running daemon
- **WHEN** user runs `ccl web --help` on a machine where the daemon is not running
- **THEN** the same usage text is printed; the help path MUST NOT depend on daemon state or on a token file

#### Scenario: ccl serve --help lists the backfill action
- **WHEN** user runs `ccl serve --help`
- **THEN** stdout lists `--backfill` with a one-line description of the historical `cwd` backfill

#### Scenario: ccl serve --help lists the compaction action
- **WHEN** user runs `ccl serve --help`
- **THEN** stdout lists `--compact` with a one-line description of the database compaction

#### Scenario: the web help does not mention the backfill
- **WHEN** user runs `ccl web --help`
- **THEN** stdout does not list `--backfill`; the flag belongs to `ccl serve`

#### Scenario: two mutually exclusive serve actions are rejected
- **WHEN** user runs `ccl serve --compact --backfill`
- **THEN** the exit code is non-zero and stderr names the mutually exclusive flags
