## MODIFIED Requirements

### Requirement: ccl can refresh a provider's quota on demand

ccl MUST provide an option that makes the quota command query the quota interface of the selected provider, record the result in the local data file, and report it. When that option is given, the command MUST make a request to the quota interface; without it, the command MUST make none. Providers that share the quota pool MUST receive the same recorded result, because the quota of a pool is a single fact and MUST NOT be recorded with different values for different providers.

The on-demand refresh MUST NOT require the daemon to be running.

When the on-demand refresh fails, the command MUST exit with status 1 and MUST leave the previously recorded quota of that provider untouched.

A provider that has no quota interface MUST be reported with status 2 without any request being made, whether the on-demand refresh was asked for or not.

#### Scenario: the on-demand refresh records the quota
- **WHEN** the command runs with the on-demand refresh option for a provider with a quota interface
- **THEN** it queries the quota interface, records the result in the local data file, and reports it

#### Scenario: providers of the same pool record the same result
- **WHEN** the command runs with the on-demand refresh option for a provider whose quota pool is also referenced by another provider
- **THEN** it records the result for the other provider as well, so that both report the same quota

#### Scenario: the on-demand refresh needs no daemon
- **WHEN** the command runs with the on-demand refresh option while the daemon is not running
- **THEN** it still queries the quota interface and records the result

#### Scenario: a failed on-demand refresh keeps the recorded quota
- **WHEN** the on-demand refresh fails
- **THEN** the command exits with status 1 and the previously recorded quota of that provider is left untouched

#### Scenario: a provider without a quota interface is not queried
- **WHEN** the command runs with the on-demand refresh option for a provider that has no quota interface
- **THEN** it exits with status 2 and makes no request
