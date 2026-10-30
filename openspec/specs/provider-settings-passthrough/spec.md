# provider-settings-passthrough Specification

## Purpose
TBD - created by archiving change provider-settings-passthrough. Update Purpose after archive.
## Requirements
### Requirement: ccl delivers the provider's non-env top-level keys through the --settings parameter

When `ccl` launches Claude Code with a selected provider, every top-level key of that provider configuration other than `env` MUST be delivered through the `--settings` parameter, with the key name and the value preserved exactly as the provider wrote them. ccl MUST NOT rename the key, coerce the value, or wrap the value in another structure.

The keys `note` and `order` MUST NOT be delivered: they are ccl's own metadata for the provider list (the remark line and the sort order), not Claude Code settings.

The delivered keys MUST be merged into the same settings object that carries the provider's `env` block, the OTel environment and the launch context.

The delivered settings object MUST be deterministic: the same provider configuration MUST always produce the same settings content, so that the content-hash naming of the cache file reuses one file instead of accumulating a new one per launch.

When a delivered key collides with one of ccl's own keys in the same settings object, ccl's own key MUST win.

#### Scenario: the provider's model key is delivered
- **WHEN** the selected provider's configuration contains `"model": "some-model"` and the user runs `ccl`
- **THEN** the delivered settings contain the top-level key `model` with the value `some-model`

#### Scenario: the provider's permissions key is delivered
- **WHEN** the selected provider's configuration contains `"permissions": {"allow": ["Bash(ls:*)"]}` and the user runs `ccl`
- **THEN** the delivered settings contain the top-level key `permissions` with that object as its value

#### Scenario: ccl's provider metadata is not delivered
- **WHEN** the selected provider's configuration contains `"note": "a remark"` and `"order": 3` and the user runs `ccl`
- **THEN** the delivered settings contain neither `note` nor `order`

#### Scenario: a provider with only an env block delivers the same as before
- **WHEN** the selected provider's configuration contains only an `env` block and the user runs `ccl`
- **THEN** the delivered settings contain the `env` block and no other provider key

#### Scenario: the delivered content is stable across launches
- **WHEN** the user runs `ccl` twice with the same provider and the same environment
- **THEN** both launches deliver settings content with the same keys in the same order

#### Scenario: ccl's own key wins a collision
- **WHEN** the selected provider's configuration contains a top-level key that ccl also sets in the delivered settings object
- **THEN** the delivered settings carry ccl's value for that key

### Requirement: the provider's model key takes effect over the ANTHROPIC_MODEL environment variable

Claude Code MUST read the `ANTHROPIC_MODEL` environment variable before it reads the `model` key of any settings file, so a `model` key delivered through `--settings` alone loses to an `ANTHROPIC_MODEL` the user has somewhere. The user's `~/.claude/settings.json` `env` block in turn takes precedence over the process environment, so setting the variable in ccl's own process is not enough either.

When the selected provider's configuration contains a top-level `model` key, ccl MUST deliver that value as `ANTHROPIC_MODEL` in the `env` block of the delivered settings, which is the only channel that outranks both the user's settings file and the process environment.

When the provider's own `env` block already contains `ANTHROPIC_MODEL`, ccl MUST keep that value, because both come from the provider and the `env` block already outranks the `model` key.

When the selected provider's configuration contains no top-level `model` key, ccl MUST NOT deliver `ANTHROPIC_MODEL`, and the value the user has MUST stay in effect.

#### Scenario: the provider's model wins over the user's own setting
- **WHEN** the selected provider's configuration contains `"model": "provider-model"` and `~/.claude/settings.json` has an `env` block with `ANTHROPIC_MODEL=stale-model`
- **THEN** the launched session uses `provider-model`

#### Scenario: the provider's model wins over an exported variable
- **WHEN** the selected provider's configuration contains `"model": "provider-model"` and the process environment has `ANTHROPIC_MODEL=shell-model`
- **THEN** the launched session uses `provider-model`

#### Scenario: a provider without a model key changes nothing
- **WHEN** the selected provider's configuration contains no top-level `model` key and the process environment has `ANTHROPIC_MODEL=shell-model`
- **THEN** the launched session still uses `shell-model`

#### Scenario: the provider's own env block keeps priority over the model key
- **WHEN** the selected provider's configuration contains both a top-level `model` key and an `env` block with `ANTHROPIC_MODEL`
- **THEN** the launched session uses the value from the provider's `env` block

