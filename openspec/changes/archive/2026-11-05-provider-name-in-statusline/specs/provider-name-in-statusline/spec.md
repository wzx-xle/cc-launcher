## ADDED Requirements

### Requirement: ccl exposes the selected provider's name as CCL_PROVIDER

Every Claude Code session that ccl launches MUST carry the name of the selected provider in the environment, under the variable name `CCL_PROVIDER`.

The value MUST be the provider's name as ccl identifies it: the provider configuration file's name without its `.json` suffix (for example the configuration file `minimax-m3-stliu14.json` yields the value `minimax-m3-stliu14`).

ccl MUST deliver the variable through the `env` block of the settings object it delivers through the `--settings` parameter. This is the channel that decides the value: it outranks both the user's own settings file and the process environment, and it is the channel the project uses for every other variable ccl hands to Claude Code.

ccl MUST ALSO place the variable in its own process environment, as a redundant second channel, so that the value survives a Claude Code version that stops applying the delivered `env` block to the process environment.

The two copies MUST carry the same value.

The name `CCL_PROVIDER` is a contract with consumers outside this repository. ccl MUST NOT rename it, and MUST NOT change the meaning of its value, without treating the change as a breaking change to this capability.

The name MUST NOT carry credential characteristics. Claude Code can remove credential-like variables from the environments of the subprocesses it starts, and a name that looks like a credential would be removed with them.

#### Scenario: the selected provider's name is exposed
- **WHEN** the user selects the provider `minimax-m3-stliu14` and runs `ccl`
- **THEN** the environment of the launched Claude Code process carries `CCL_PROVIDER=minimax-m3-stliu14`

#### Scenario: the value is delivered through the settings env block
- **WHEN** ccl launches Claude Code with a selected provider
- **THEN** the delivered settings object's `env` block contains the key `CCL_PROVIDER` with the selected provider's name as its value

#### Scenario: the process environment carries a redundant copy
- **WHEN** ccl launches Claude Code with a selected provider
- **THEN** ccl's own process environment carries `CCL_PROVIDER` with the same value as the delivered settings object's `env` block

#### Scenario: the value identifies the provider rather than its endpoint
- **WHEN** the user selects the provider `minimax-m3-stliu14`, whose configuration points at the same endpoint as another provider
- **THEN** the value is `minimax-m3-stliu14`, not the endpoint and not the provider's `note`

### Requirement: a consumer reads the provider name regardless of whether Claude Code scrubs subprocess environments

Claude Code can be configured to remove credential-like variables from the environments of the subprocesses it starts. A consumer MUST still read the provider name in that configuration.

ccl MUST deliver the variable under a name that carries no credential characteristics, so that no consumer configuration is needed to read it.

#### Scenario: the variable is readable while credential variables are removed
- **WHEN** Claude Code runs with the subprocess environment scrub enabled and starts a consumer subprocess
- **THEN** the consumer subprocess's environment carries `CCL_PROVIDER` with the selected provider's name

#### Scenario: the variable is readable while the scrub is disabled
- **WHEN** Claude Code runs with the subprocess environment scrub disabled and starts a consumer subprocess
- **THEN** the consumer subprocess's environment carries `CCL_PROVIDER` with the selected provider's name

### Requirement: ccl's provider name wins over a provider-configured CCL_PROVIDER

A provider configuration is free to contain any key in its `env` block, including `CCL_PROVIDER`. The value ccl delivers MUST be the selected provider's own name, regardless of what the provider configuration says.

#### Scenario: a provider-configured value is replaced
- **WHEN** the selected provider's configuration contains `"env": {"CCL_PROVIDER": "something-else"}` and the provider's name is `claude-official`
- **THEN** the delivered `CCL_PROVIDER` is `claude-official`

#### Scenario: the user's own settings file does not replace it either
- **WHEN** `~/.claude/settings.json` has an `env` block with `CCL_PROVIDER=stale` and the selected provider's name is `claude-official`
- **THEN** the launched session's `CCL_PROVIDER` is `claude-official`

### Requirement: the value is the provider's name verbatim, and sanitizing it before display belongs to the consumer

ccl MUST deliver the provider's name verbatim. It MUST NOT percent-encode the value, MUST NOT strip characters from it, and MUST NOT format it into a composite of several fields.

A provider's name originates in a file name, so it may in principle contain control characters or terminal escape sequences. ccl MUST NOT alter the value to defend against that; a consumer that renders the value into a terminal MUST strip escape sequences and control characters, and MUST bound the length it displays.

The provider's `note` and the provider's endpoint MUST NOT appear in the value. The `note` is the user's personal remark rather than an identifier, and the endpoint cannot distinguish providers that share it while using different accounts.

#### Scenario: the value carries no encoding
- **WHEN** the selected provider's name contains a character that ccl's telemetry channel would percent-encode
- **THEN** the delivered `CCL_PROVIDER` carries that character as it appears in the name

#### Scenario: the value carries exactly one field
- **WHEN** the selected provider's configuration has a `note` and an `ANTHROPIC_BASE_URL`
- **THEN** the delivered `CCL_PROVIDER` is the provider's name alone, with neither the note nor the endpoint appended

### Requirement: the absence of the variable identifies a session that ccl did not launch

ccl MUST NOT deliver `CCL_PROVIDER` with an empty value. A consumer MUST be able to tell "no provider" apart from "a provider whose name is empty" by the variable being absent rather than present and empty.

A session that ccl did not launch — a Claude Code started directly by the user — inherits no `CCL_PROVIDER`, and a consumer MUST treat that absence as "this session has no ccl provider".

#### Scenario: the variable is never delivered empty
- **WHEN** ccl launches Claude Code
- **THEN** the delivered `CCL_PROVIDER` is a non-empty name, and ccl delivers no `CCL_PROVIDER` key at all rather than an empty value

#### Scenario: a directly launched session has no variable
- **WHEN** the user runs `claude` directly instead of through `ccl`
- **THEN** the launched process's environment carries no `CCL_PROVIDER`

#### Scenario: the delivery stays deterministic
- **WHEN** the user runs `ccl` twice with the same provider
- **THEN** both launches deliver the same `CCL_PROVIDER` value, and the delivered settings content is identical
