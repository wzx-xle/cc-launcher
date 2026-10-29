# provider-settings-injection Specification

## Purpose
TBD - created by archiving change provider-env-via-settings. Update Purpose after archive.
## Requirements
### Requirement: ccl delivers the provider env through the --settings parameter
When `ccl` launches Claude Code with a selected provider, ccl MUST deliver every key of that provider configuration's `env` block through the `--settings` parameter, in the same settings object that carries the OTel environment and the launch context.

ccl MUST ALSO place the provider's env in its own process environment, from which the child process and the daemon it forks inherit it. That copy is the lower-precedence channel: in Claude Code the `--settings` delivery still decides the values, and the process environment is a backup for the case where `--settings` is not honored. The copy exists because the daemon's forwarder reaches the remote collector over the network with the proxy variables taken from the process environment — a provider that configures a proxy must keep that proxy working for the forwarded metrics.

The provider's env MUST NOT be delivered through the process environment alone. The process environment is the lowest-precedence channel in Claude Code — the settings file's `env` block overrides it — so a provider delivered that way loses to any `ANTHROPIC_*` value the user happens to have in `~/.claude/settings.json`, and the selected provider silently does not apply.

The provider configuration's structure MUST remain identical to Claude Code's own settings structure, so that the same `env` block serves both purposes.

#### Scenario: the provider's keys are delivered
- **WHEN** the selected provider's configuration contains `env.ANTHROPIC_BASE_URL = "https://api.example.com"` and the user runs `ccl`
- **THEN** the delivered settings contain `env.ANTHROPIC_BASE_URL = "https://api.example.com"`, and the child process environment carries the same value as the lower-precedence copy

#### Scenario: the daemon inherits the provider's network configuration
- **WHEN** the selected provider's configuration contains `env.HTTPS_PROXY = "http://proxy:3128"` and the user runs `ccl`
- **THEN** the daemon that ccl forks inherits `HTTPS_PROXY` from ccl's process environment, so its forwarder reaches the remote collector through that proxy

#### Scenario: the provider shares the settings object with the OTel environment
- **WHEN** ccl launches Claude Code with a selected provider
- **THEN** one settings object carries the provider's env keys, the OTel keys and the launch context

#### Scenario: a provider without env delivers nothing
- **WHEN** the selected provider's configuration has no `env` block
- **THEN** the delivered settings contain no provider keys, and the launch proceeds as before

### Requirement: the provider configuration takes precedence over the settings file
A key that the selected provider configures MUST take effect for the launched session, even when `~/.claude/settings.json` defines the same key with a different value. This is the purpose of configuring a separate provider: the provider the user picked is the one that applies.

A key that the selected provider does NOT configure MUST be left alone: the settings file's value for it, if any, stays in effect, and the process environment's value stays in effect if the settings file defines none.

ccl MUST NOT deliver a key it has no value for, and MUST NOT attempt to remove a key from the settings file in order to let another channel win — the delivered settings object can only add keys, never subtract them.

#### Scenario: the provider overrides a settings file value
- **WHEN** `~/.claude/settings.json` has `ANTHROPIC_BASE_URL = "https://stale.example.com"` and the selected provider configures `ANTHROPIC_BASE_URL = "https://fresh.example.com"`
- **THEN** the launched session uses `https://fresh.example.com`

#### Scenario: a key the provider does not configure is left alone
- **WHEN** `~/.claude/settings.json` has `ANTHROPIC_MODEL = "some-model"` and the selected provider configures no `ANTHROPIC_MODEL`
- **THEN** the launched session uses `some-model`

#### Scenario: a key nobody configures falls back to the process environment
- **WHEN** neither the settings file nor the selected provider defines `ANTHROPIC_MODEL`, and the process environment does
- **THEN** the launched session uses the process environment's value

#### Scenario: the settings file is not modified to make the provider win
- **WHEN** ccl launches Claude Code with a selected provider
- **THEN** `~/.claude/settings.json` is not modified to make room for the provider's values

