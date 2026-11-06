## MODIFIED Requirements

### Requirement: ccl refreshes the quota periodically in the daemon

ccl MUST refresh the quota of the providers that declare a quota interface, on a periodic schedule, and MUST do that work in the daemon rather than in the process that reports the quota.

The refresh interval MUST default to 60 seconds, and a provider MUST be able to override it through its `quota` configuration.

The daemon MUST refresh every provider that declares a quota interface, and MUST NOT refresh a provider that declares none. Two providers that resolve to the same quota endpoint and the same API key MUST be queried once for that cycle, and the result MUST be recorded for both.

The daemon MUST NOT query a provider whose recorded quota is younger than that provider's refresh interval. The age of a recorded quota MUST be derived from the time recorded in the local data file rather than from a time kept only in the daemon's memory, so that a quota recorded by an on-demand refresh counts as a recent refresh.

The daemon MUST refresh a provider immediately when it starts, rather than waiting for the first interval to elapse, so that a quota is available as soon as the daemon is up.

A refresh that fails MUST NOT discard the previously recorded quota of that provider.

Providers that share a refresh interval MUST NOT be queried at the same moment, because an interface that serves several providers at once can reject the burst. Each quota pool MUST occupy a fixed position within its refresh interval, and that position MUST be kept across refreshes so that the interval of a pool stays exactly as long as the provider configured. The position MUST be derived from the quota pools that share the interval, so that their requests are spread over the interval instead of issued together.

Within a single refresh cycle, the daemon MUST leave a minimum gap between two consecutive requests, and the gap MUST be configurable per provider.

When an interface reports that it is rate limiting the caller, ccl MUST stop querying that provider for a while instead of retrying on every cycle. The wait MUST grow with each consecutive rejection and MUST be capped, and it MUST be recorded in the local data file so that it also applies to an on-demand refresh and survives a restart of the daemon. A provider that is not being queried MUST keep its previously recorded quota.

The wait MUST be recorded against the quota pool rather than against the provider, because the interface limits the API key and not the provider's name. A provider whose API key is replaced MUST therefore not be held back by a wait that was recorded for the previous key.

#### Scenario: the quota is refreshed on the default interval
- **WHEN** a provider declares a quota interface and no refresh interval
- **THEN** ccl refreshes its quota every 60 seconds

#### Scenario: the refresh interval can be overridden
- **WHEN** a provider's quota configuration declares a refresh interval of 300 seconds
- **THEN** ccl refreshes its quota every 300 seconds

#### Scenario: providers sharing a key are queried once
- **WHEN** two providers resolve to the same quota endpoint and the same API key
- **THEN** ccl queries the quota interface once for that cycle and records the result for both providers

#### Scenario: a recently recorded quota is not queried again
- **WHEN** the recorded quota of a provider was fetched less than that provider's refresh interval ago
- **THEN** ccl does not query the quota interface for that provider in that cycle

#### Scenario: an on-demand refresh counts as a recent refresh
- **WHEN** the quota of a provider was recorded by an on-demand refresh and the daemon refreshes afterwards
- **THEN** ccl does not query the quota interface for that provider until its refresh interval has elapsed

#### Scenario: the daemon refreshes without waiting
- **WHEN** the daemon starts
- **THEN** ccl refreshes the quotas immediately instead of waiting for the first interval to elapse

#### Scenario: a failed refresh keeps the previous quota
- **WHEN** a refresh of a provider fails
- **THEN** ccl keeps the previously recorded quota of that provider

#### Scenario: providers of the same interval are not queried together
- **WHEN** eight providers share a refresh interval of 60 seconds
- **THEN** ccl does not issue their eight requests at the same moment but spreads them over the interval

#### Scenario: the position within the interval is kept
- **WHEN** a provider's quota pool has been assigned a position within its refresh interval
- **THEN** ccl refreshes that provider at that position again in every following interval, so that the interval stays exactly as long as the provider configured

#### Scenario: the requests of one cycle are separated
- **WHEN** ccl refreshes several providers in one cycle
- **THEN** ccl leaves the configured minimum gap between two consecutive requests

#### Scenario: a rate-limited provider is not queried again right away
- **WHEN** an interface rejects a request because the caller is rate limited
- **THEN** ccl does not query that provider again until the recorded wait has elapsed, and the wait grows with each consecutive rejection

#### Scenario: the wait applies to an on-demand refresh as well
- **WHEN** a provider is recorded as waiting after a rate limit rejection and the quota command is asked to refresh it
- **THEN** the command reports that the provider is waiting instead of issuing the request

#### Scenario: the wait survives a restart
- **WHEN** a provider was recorded as waiting after a rate limit rejection and the daemon restarts
- **THEN** ccl does not query that provider before the recorded wait has elapsed

#### Scenario: a provider that is waiting keeps its quota
- **WHEN** a provider is waiting after a rate limit rejection
- **THEN** ccl keeps the previously recorded quota of that provider

#### Scenario: a replaced key is not held back
- **WHEN** a quota pool was recorded as waiting after a rate limit rejection and the provider's API key is replaced
- **THEN** ccl queries the provider with the new key instead of holding it back for the recorded wait

### Requirement: ccl reports the quota through a command that reads the local data file

ccl MUST provide a command that reports the quota of a provider. The command MUST read the recorded quota from the local data file and MUST NOT make a request to the quota interface itself.

Its JSON output MUST carry the dimensions of the quota as a list, each with a kind, a duration where one is known, a used percentage where one can be derived, and a reset time where one is known. A dimension that could not be determined MUST be absent from the list rather than reported as an empty value.

The command MUST accept the provider to report on as an explicit option. When no option is given, it MUST fall back to the provider named by the `CCL_PROVIDER` environment variable, and then to the provider recorded as active. This lets a consumer that runs inside a session started by ccl name the provider without knowing ccl's own state.

The command MUST report whether the quota it returns is stale, by comparing the time at which the quota was fetched against the refresh interval of that provider. When the refresh interval of that provider cannot be read, the command MUST use 60 seconds for that comparison. The comparison MUST allow for the fact that the time of the fetch is recorded with a resolution of one second, so that a quota that is refreshed on schedule is not reported as stale between two refreshes.

The command MUST report the time at which the quota it returns was fetched, so that a consumer can tell how old the data is without consulting the data file.

When the provider has no quota interface, the command MUST exit with status 2 and MUST NOT print a quota document. When no quota has been recorded for the provider, the command MUST exit with status 1 and MUST NOT print a quota document. Only a successful report MUST exit with status 0.

A missing quota MUST NOT prevent the launch of Claude Code, and MUST NOT affect the provider's own configuration.

#### Scenario: the JSON output carries a list of dimensions
- **WHEN** the command runs for a provider with a recorded quota
- **THEN** its JSON output carries a `dimensions` list whose entries carry a `kind` and, where derivable, a `used_percentage`

#### Scenario: the command makes no request
- **WHEN** the command runs
- **THEN** it makes no request to any quota interface

#### Scenario: the provider comes from the environment
- **WHEN** the command runs with the environment variable `CCL_PROVIDER` set to `minimax-m3` and no explicit option
- **THEN** it reports the quota of `minimax-m3`

#### Scenario: an explicit provider wins over the environment
- **WHEN** the command runs with the environment variable `CCL_PROVIDER` set to `minimax-m3` and the option naming `minimax-m3-stliu14`
- **THEN** it reports the quota of `minimax-m3-stliu14`

#### Scenario: a provider without a quota interface exits with status 2
- **WHEN** the command runs for a provider that has no quota interface
- **THEN** it exits with status 2 and prints no quota document

#### Scenario: a provider without a recorded quota exits with status 1
- **WHEN** the command runs for a provider that has no recorded quota
- **THEN** it exits with status 1 and prints no quota document

#### Scenario: an old quota is reported as stale
- **WHEN** the recorded quota of a provider was fetched longer ago than that provider's refresh interval
- **THEN** the command reports the quota and marks it stale

#### Scenario: a quota refreshed on schedule is not reported as stale
- **WHEN** the quota of a provider was refreshed on schedule and the recorded fetch time is up to one second older than the refresh interval because of its resolution
- **THEN** the command reports the quota without marking it stale

#### Scenario: a missing quota does not block the launch
- **WHEN** no quota has been recorded for the selected provider
- **THEN** ccl reports the failure and the launch of Claude Code proceeds unaffected
