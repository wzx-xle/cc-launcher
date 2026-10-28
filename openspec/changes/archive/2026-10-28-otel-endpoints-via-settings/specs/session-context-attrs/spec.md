## MODIFIED Requirements

### Requirement: the launch context is absent for sessions started without ccl
ccl MUST NOT install a global OTel endpoint into `~/.claude/settings.json`. Therefore a session that the user starts with `claude` directly, without `ccl`, does not export to the ccl daemon at all and does not appear in the statistics.

When an export that carries no context attributes does reach the daemon — for example because the user's own configuration points at the daemon's address, or because an earlier version of ccl left its endpoint in the settings file — the daemon MUST record the session with empty context fields, and the CLI and the dashboard MUST show it as unknown rather than as an empty-string dimension of its own.

The daemon MUST NOT fall back to a currently configured provider when reading an export, because that would attribute past sessions to the provider that happens to be selected now.

#### Scenario: a session without context is grouped as unknown
- **WHEN** the daemon records a session whose export carried no `ccl.*` attribute
- **THEN** grouping that session by provider or by project yields an "unknown" bucket, and the session is not attributed to any configured provider

#### Scenario: a session started without ccl is not recorded at all
- **WHEN** the user runs `claude` directly, without `ccl`, and the settings file contains no OTel endpoint
- **THEN** no export reaches the ccl daemon, and the session does not appear in the statistics

#### Scenario: a leftover endpoint still yields an unknown session
- **WHEN** an export reaches the daemon without any `ccl.*` attribute, because a leftover endpoint from an earlier ccl version is still configured
- **THEN** the session is recorded with empty context fields and shown as unknown, and it is not attributed to the currently selected provider
