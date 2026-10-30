## REMOVED Requirements

### Requirement: the delivered settings contain only an env block
**Reason**: The provider configuration now carries keys beyond its `env` block, and ccl delivers them to Claude Code through the same `--settings` parameter. A requirement that pins the delivered object to exactly one field contradicts that delivery.

**Migration**: None. The new requirement delivers a superset of the old content: the `env` block is still delivered, and the settings file's own settings for keys ccl does not deliver still remain in effect.

## ADDED Requirements

### Requirement: The delivered settings contain the env block and the provider's passthrough keys

The settings object ccl delivers MUST contain the `env` block, and the top-level keys the selected provider configures, except for ccl's own `note` and `order` metadata. The settings file's own settings for the keys ccl does not deliver (plugins, and the settings for keys the provider does not configure) MUST remain in effect, because ccl's object is merged into the settings file rather than replacing it.

The delivered object MUST list its keys in a stable order, so that the same provider configuration and the same environment always produce the same settings content and therefore the same content-hash-named cache file.

#### Scenario: the env block is delivered
- **WHEN** ccl launches Claude Code with a selected provider
- **THEN** the delivered settings object contains an `env` field carrying the provider's env keys, the OTel keys and the launch context

#### Scenario: the provider's keys are delivered alongside it
- **WHEN** the selected provider's configuration contains `"model": "some-model"` and the user runs `ccl`
- **THEN** the delivered settings object contains both the `env` field and the top-level key `model`

#### Scenario: the settings file's other settings survive
- **WHEN** `~/.claude/settings.json` defines `permissions` and the selected provider configures no `permissions`, and ccl delivers its own settings object
- **THEN** the permissions remain in effect for the launched session

#### Scenario: the same configuration yields the same content
- **WHEN** the user runs `ccl` twice with the same provider and the same environment
- **THEN** both launches deliver settings content whose keys appear in the same order
