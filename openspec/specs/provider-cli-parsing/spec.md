# provider-cli-parsing Specification

## Purpose
TBD - created by archiving change provider-flag-migration. Update Purpose after archive.
## Requirements
### Requirement: ccl provider accepts both short and long flag forms

`ccl provider` MUST accept both `-x` and `--x` forms for every flag. The runtime behavior MUST be equivalent: parsing succeeds, the same dispatch happens, and the same exit code is produced. This is achieved by registering flags via `flag.NewFlagSet` (Go 1.21+ stdlib behavior).

#### Scenario: short --list form
- **WHEN** user runs `ccl provider -list`
- **THEN** runtime accepts it; stdout / behavior / exit code matches `ccl provider --list`

#### Scenario: short --new form
- **WHEN** user runs `ccl provider -new` (with empty stdin to complete the guided flow)
- **THEN** runtime enters the guided flow; matches `ccl provider --new`

#### Scenario: short --delete form
- **WHEN** user runs `ccl provider -delete foo` (with `foo` existing as a provider)
- **THEN** runtime deletes `foo`; matches `ccl provider --delete foo`

#### Scenario: short -T form for --template
- **WHEN** user runs `ccl provider -T deepseek`
- **THEN** runtime prints the `deepseek` template JSON; matches `ccl provider --template deepseek`

#### Scenario: short --template form
- **WHEN** user runs `ccl provider -template deepseek`
- **THEN** runtime prints the `deepseek` template JSON; matches `ccl provider --template deepseek`

#### Scenario: short -h form for --help
- **WHEN** user runs `ccl provider -h`
- **THEN** runtime prints `providerHelp`; matches `ccl provider --help`

### Requirement: ccl provider enforces mutex on list / new / delete

`ccl provider` MUST reject any invocation that combines `--list` with `--new`, `--list` with `--delete`, or `--new` with `--delete`. Stderr MUST contain the literal "互斥"; exit code MUST be nonzero.

**NOTE**：`--template` does NOT participate in this mutex — see the next requirement.

#### Scenario: --list --new is a mutex violation
- **WHEN** user runs `ccl provider --list --new`
- **THEN** stderr contains "互斥"; exit code is nonzero; no provider list is printed; no file is written

#### Scenario: --list --delete is a mutex violation
- **WHEN** user runs `ccl provider --list --delete foo`
- **THEN** stderr contains "互斥"; exit code is nonzero; `foo` is NOT deleted

#### Scenario: --new --delete is a mutex violation
- **WHEN** user runs `ccl provider --new --delete foo`
- **THEN** stderr contains "互斥"; exit code is nonzero; no flow starts; `foo` is NOT deleted

#### Scenario: --list --new --template is a mutex violation (3-flag combo)
- **WHEN** user runs `ccl provider --list --new --template deepseek`
- **THEN** stderr contains "互斥"; exit code is nonzero; no provider list is printed; no template is printed

(The list / new / delete mutex fires first; `--template` is not consulted because mutex fails before dispatch.)

### Requirement: ccl provider --template is independent of --new (back-compat)

`ccl provider --new --template` MUST continue to print the same content as `ccl provider --template` (no `--new` required, no provider file created). This is a locked behavior from `provider-new-guided-flow` §"provider --new --template is independent of interactive flow" — preserving it requires:

- `--template` MUST NOT participate in the list / new / delete mutex
- Dispatch MUST check `--template` BEFORE `--new` so that `--new --template deepseek` reaches `printTemplate`

#### Scenario: --new --template still just prints templates
- **WHEN** user runs `ccl provider --new --template deepseek`
- **THEN** stdout matches `ccl provider --template deepseek`; no provider file is created; exit code 0

#### Scenario: --new --template (no type) still lists all templates
- **WHEN** user runs `ccl provider --new --template`
- **THEN** stdout matches `ccl provider --template` (lists all 7 types); no provider file is created; exit code 0

#### Scenario: --list --template combined (template wins, --list ignored)
- **WHEN** user runs `ccl provider --list --template deepseek`
- **THEN** stdout matches `ccl provider --template deepseek` (no provider list is printed); exit code 0

(The list / new / delete mutex does NOT include template, so this combination is legal. Dispatch checks `--template` first, so `--list` is ignored.)

### Requirement: ccl provider --type is only meaningful inside --new

`ccl provider --type <name>` outside of a `--new` invocation MUST be rejected. This preserves the old "未知选项: --type" semantics for `ccl provider --type foo` (no `--new`).

`--type` MUST be registered on the FlagSet so that `fs.Parse` does not error when `--new --type <name>` is given — but its string value is only consulted when `--new` is also set.

#### Scenario: --type alone is rejected
- **WHEN** user runs `ccl provider --type deepseek` (without `--new`)
- **THEN** exit code is nonzero; stderr explains `--type` only works with `--new`

#### Scenario: --type with --new still preselects the template
- **WHEN** user runs `ccl provider --new --type deepseek` (with empty stdin)
- **THEN** the flow skips the "select template" prompt and proceeds to name / fields (matches `provider-new-guided-flow` §"--type deepseek skips type selection")

#### Scenario: --type silently ignored when other flag is given
- **WHEN** user runs `ccl provider --delete foo --type deepseek`
- **THEN** runtime calls `deleteProvider("foo")`; `foo` is deleted; `--type` is silently ignored (no error, but the type value is not consulted)

(Three cases: `--type` alone → error per "Scenario: --type alone is rejected"; `--type + --new` → preselect per "Scenario: --type with --new still preselects the template"; `--type + other flag` → silently ignored per this scenario.)

### Requirement: ccl provider dispatches position arguments correctly

After `fs.Parse`, `ccl provider` MUST dispatch based on the set of flags and the position arguments:

| Flags given | Position args | Dispatch |
|---|---|---|
| none | `<name>` | `switchProvider(name)` |
| none | (none) | `listProviders()` |
| `--list` | any | `listProviders()` (position ignored) |
| `--new` | `<name>` | `newProvider(name, typeFromFlag)` |
| `--new` | (none) | `newProvider("", typeFromFlag)` |
| `--new --type <type>` | `<name>` | `newProvider(name, type)` |
| `--new --type <type>` | (none) | `newProvider("", type)` |
| `--delete` | `<name>` | `deleteProvider(name)` |
| `--delete` | (none) | error: usage hint |
| `--template` / `-T` | `<type>` | `printTemplate([type])` |
| `--template` / `-T` | (none) | `printTemplate([])` (list all) |

#### Scenario: bare ccl provider lists all
- **WHEN** user runs `ccl provider` (no args)
- **THEN** runtime calls `listProviders()`; stdout matches `ccl provider --list`

#### Scenario: ccl provider <name> switches active
- **WHEN** user runs `ccl provider foo` (with `foo` existing)
- **THEN** runtime calls `switchProvider("foo")`; the active provider is set to `foo`

#### Scenario: ccl provider --delete without name errors
- **WHEN** user runs `ccl provider --delete` (no name)
- **THEN** exit code is nonzero; stderr explains usage (`ccl provider --delete <name>`)

#### Scenario: ccl provider --new <name> enters guided flow with name preset
- **WHEN** user runs `ccl provider --new my-deepseek --type deepseek` (with valid field inputs)
- **THEN** runtime calls `newProvider("my-deepseek", "deepseek")`; the guided flow skips the name prompt

#### Scenario: short -new with --type and position arg
- **WHEN** user runs `ccl provider -new --type deepseek my-deepseek` (with valid field inputs)
- **THEN** runtime calls `newProvider("my-deepseek", "deepseek")`; matches the long-form scenario above

### Requirement: ccl provider --help uses *helpFlag and takes priority over mutex

`ccl provider --help` / `-h` MUST be handled via `*helpFlag` (the value of `Bool("help, h", false, ...)`), NOT via `flag.ErrHelp` (which is only returned when `--help` is **unregistered** in the FlagSet). Help MUST take priority over the list / new / delete mutex — even `ccl provider --list --new --help` shows help (does NOT report a mutex violation).

The `--help` flag is registered on the FlagSet (not left unregistered) so that completion can include `-h / --help` as candidates.

#### Scenario: --help triggers providerHelp output
- **WHEN** user runs `ccl provider --help`
- **THEN** stdout contains the contents of `providerHelp` const; exit code 0

#### Scenario: -h triggers providerHelp output
- **WHEN** user runs `ccl provider -h`
- **THEN** stdout matches the `--help` scenario

#### Scenario: --help takes priority over mutex violations
- **WHEN** user runs `ccl provider --list --new --help`
- **THEN** stdout contains `providerHelp`; exit code is 0; stderr does NOT contain "互斥"

