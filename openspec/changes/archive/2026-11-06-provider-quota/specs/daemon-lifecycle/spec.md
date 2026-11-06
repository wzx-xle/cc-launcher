## ADDED Requirements

### Requirement: ccl serve refreshes the quotas of the configured providers

`ccl serve` MUST refresh the quota of every provider that declares a quota interface, and MUST do so on a periodic schedule while it is running.

The refresh interval MUST default to 60 seconds. A provider MUST be able to override it through its own configuration.

The refresh MUST happen in the daemon process rather than in the process that reports a quota, so that a consumer that reports the quota never waits for the network.

The daemon MUST NOT query a provider whose recorded quota is younger than that provider's refresh interval, and MUST derive that age from the recorded quota rather than from a time kept only in the daemon's memory.

When the daemon starts, it MUST refresh the quotas once immediately instead of waiting for the first interval to elapse.

A refresh that fails MUST NOT discard the quota that was recorded for that provider previously, and MUST NOT stop the daemon's refresh schedule.

Two providers that resolve to the same quota endpoint and the same API key MUST be queried once per cycle, and the result MUST be recorded for both.

The refresh MUST NOT delay or fail the daemon's other duties: the metrics endpoint MUST keep serving, and the graceful shutdown on `SIGTERM` MUST still complete within its budget.

#### Scenario: the quotas are refreshed on the default interval
- **WHEN** the daemon is running and a provider declares a quota interface
- **THEN** the daemon refreshes that provider's quota every 60 seconds

#### Scenario: the refresh interval is configurable per provider
- **WHEN** a provider's quota configuration declares a refresh interval of 300 seconds
- **THEN** the daemon refreshes that provider's quota every 300 seconds and does not refresh it more often

#### Scenario: the daemon refreshes without waiting
- **WHEN** the daemon starts
- **THEN** it refreshes the quotas immediately rather than waiting for the first interval to elapse

#### Scenario: providers sharing a key are queried once
- **WHEN** two providers resolve to the same quota endpoint and the same API key
- **THEN** the daemon queries the quota interface once per cycle and records the result for both providers

#### Scenario: a recently recorded quota is not queried again
- **WHEN** the recorded quota of a provider was fetched less than that provider's refresh interval ago
- **THEN** the daemon does not query the quota interface for that provider in that cycle

#### Scenario: a failed refresh keeps the previous quota
- **WHEN** a refresh of a provider fails
- **THEN** the previously recorded quota of that provider is kept and the daemon keeps refreshing on schedule

#### Scenario: the refresh does not disturb the metrics endpoint
- **WHEN** the daemon is refreshing a quota
- **THEN** the metrics endpoint keeps serving and the daemon's graceful shutdown still completes within its budget
