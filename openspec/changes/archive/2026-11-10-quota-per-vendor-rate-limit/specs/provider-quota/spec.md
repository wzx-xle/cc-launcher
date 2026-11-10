## MODIFIED Requirements

### Requirement: ccl refreshes the quota periodically in the daemon

ccl MUST refresh the quota of the providers that declare a quota interface, on a periodic schedule, and MUST do that work in the daemon rather than in the process that reports the quota.

The refresh interval MUST default to 60 seconds, and a provider MUST be able to override it through its `quota` configuration.

The daemon MUST refresh every quota pool that has a provider which declares a quota interface **and that has reported activity recently**, and MUST NOT refresh a quota pool that has none. A provider counts as recently active when it has reported activity within the recent window. When any provider of a quota pool is recently active, the daemon MUST refresh that pool and MUST record the result for every provider of that pool, because the quota of a pool is a single fact and MUST NOT be recorded with different values for different providers. The daemon MUST NOT query a pool whose providers are all outside that window, because the quota of a provider that nobody is using is not worth the request. Two providers that resolve to the same quota endpoint and the same API key MUST be queried once for that cycle, and the result MUST be recorded for both.

A quota pool that has no recently active provider MUST still be refreshed when its recorded quota is older than a freshness threshold of 10 minutes, so that a user who switches to such a provider does not see a quota that is far out of date. The freshness threshold MUST bound how often such a pool is queried, so that a pool without active providers is not queried on every refresh cycle.

The daemon MUST NOT query a provider whose recorded quota is younger than that provider's refresh interval. The age of a recorded quota MUST be derived from the time recorded in the local data file rather than from a time kept only in the daemon's memory, so that a quota recorded by an on-demand refresh counts as a recent refresh.

The daemon MUST refresh a provider immediately when it starts, rather than waiting for the first interval to elapse, so that a quota is available as soon as the daemon is up. Starting the daemon MUST NOT leave that first refresh with no provider to query, and the daemon MUST fall back to the recorded active provider when it has no activity of its own yet.

A refresh that fails MUST NOT discard the previously recorded quota of that provider.

Providers that share a refresh interval MUST NOT be queried at the same moment, because an interface that serves several providers at once can reject the burst. Each quota pool MUST occupy a fixed position within its refresh interval, and that position MUST be kept across refreshes so that the interval of a pool stays exactly as long as the provider configured. The position MUST be derived from the quota pools that share the interval, so that their requests are spread over the interval instead of issued together.

Within a single refresh cycle, the daemon MUST leave a minimum gap between two consecutive requests, and the gap MUST be configurable per provider.

The daemon MUST also leave a minimum gap between two requests to the same quota interface, even when the two requests belong to different quota pools and fall in different refresh cycles. That gap MUST default to 10 seconds and MUST be configurable per provider. The daemon MUST remember, across refresh cycles, when it last queried each interface, so that the gap also applies to requests separated by a cycle boundary.

When an interface reports that it is rate limiting the caller, ccl MUST stop querying that provider for a while instead of retrying on every cycle. The wait MUST grow with each consecutive rejection and MUST be capped, and it MUST be recorded in the local data file so that it also applies to an on-demand refresh and survives a restart of the daemon. A provider that is not being queried MUST keep its previously recorded quota.

The wait MUST be recorded against the quota interface rather than against a single provider, because an interface may limit the whole account or the caller's address rather than the API key alone. The quota pools that resolve to the same interface MUST therefore be held back together: when one of them is recorded as waiting, the others of that interface are not queried either.

A quota pool whose API key has been replaced MUST still be held back by the wait recorded for its interface, because the interface may be limiting the account rather than the key.

A wait MUST NOT be discarded entirely because a single refresh of that interface succeeded. A successful refresh MUST reduce the recorded wait by one step instead, so that a wait which grew under repeated rejections is not reset by one lucky success.

#### Scenario: the quota is refreshed on the default interval
- **WHEN** a provider declares a quota interface and no refresh interval
- **THEN** ccl refreshes its quota every 60 seconds

#### Scenario: the refresh interval can be overridden
- **WHEN** a provider's quota configuration declares a refresh interval of 300 seconds
- **THEN** ccl refreshes its quota every 300 seconds

#### Scenario: providers sharing a key are queried once
- **WHEN** two providers resolve to the same quota endpoint and the same API key
- **THEN** ccl queries the quota interface once for that cycle and records the result for both providers

#### Scenario: a provider that was not active recently is not queried
- **WHEN** every provider of a quota pool declares a quota interface but none of them has reported activity within the recent window, and the pool's recorded quota is younger than the freshness threshold
- **THEN** ccl does not query the quota interface for that pool

#### Scenario: a provider that is active is queried
- **WHEN** a provider has reported activity within the recent window
- **THEN** ccl queries the quota interface for that provider

#### Scenario: an active provider refreshes the whole pool
- **WHEN** one provider of a quota pool has reported activity within the recent window and another provider of the same pool has not
- **THEN** ccl queries that pool once and records the result for both providers, so that both report the same quota

#### Scenario: a pool without active providers is refreshed once it goes out of date
- **WHEN** no provider of a quota pool has reported activity within the recent window and the pool's recorded quota is older than the freshness threshold
- **THEN** ccl queries that pool and records the result for every provider of that pool

#### Scenario: a pool without active providers is not queried on every cycle
- **WHEN** no provider of a quota pool has reported activity within the recent window and the pool's recorded quota is younger than the freshness threshold
- **THEN** ccl does not query that pool again in that cycle, so that it is not queried more often than the freshness threshold allows

#### Scenario: a recently recorded quota is not queried again
- **WHEN** the recorded quota of a provider was fetched less than that provider's refresh interval ago
- **THEN** ccl does not query the quota interface for that provider in that cycle

#### Scenario: an on-demand refresh counts as a recent refresh
- **WHEN** the quota of a provider was recorded by an on-demand refresh and the daemon refreshes afterwards
- **THEN** ccl does not query the quota interface for that provider until its refresh interval has elapsed

#### Scenario: the daemon refreshes without waiting
- **WHEN** the daemon starts
- **THEN** ccl refreshes the quotas immediately instead of waiting for the first interval to elapse

#### Scenario: the first refresh after starting queries the recorded active provider
- **WHEN** the daemon starts and has not seen any activity of its own yet
- **THEN** ccl queries the quota pool of the recorded active provider in that first refresh

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

#### Scenario: a replaced key is held back by the interface wait
- **WHEN** a quota pool was recorded as waiting after a rate limit rejection and the provider's API key is replaced
- **THEN** ccl holds the provider back for the wait recorded for that interface, because the interface may be limiting the account rather than the key
#### Scenario: the quota pools of one interface are held back together
- **WHEN** one quota pool of an interface is recorded as waiting after a rate limit rejection
- **THEN** ccl does not query the other quota pools of that interface until the recorded wait has elapsed

#### Scenario: the requests to one interface are separated
- **WHEN** two quota pools of the same interface are queried
- **THEN** the second request is issued no earlier than the configured interface gap after the first one

#### Scenario: a successful refresh reduces the wait instead of clearing it
- **WHEN** a refresh of an interface succeeds while that interface is recorded as waiting
- **THEN** the recorded wait is reduced by one step rather than removed
