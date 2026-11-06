# provider-quota Specification

## Purpose
TBD - created by archiving change provider-quota. Update Purpose after archive.
## Requirements
### Requirement: ccl resolves the quota adapter and endpoint of a provider

ccl MUST determine which quota adapter serves the selected provider, and which quota endpoint it queries, before querying any quota.

When the provider configuration declares a top-level `quota` object, ccl MUST take the adapter from its `source` field and MUST NOT apply host-based adapter detection. When the `quota` object declares an `endpoint`, ccl MUST use that URL as the quota endpoint and MUST NOT derive one.

Otherwise ccl MUST detect the adapter from the host of the provider's `ANTHROPIC_BASE_URL`: after removing a leading `api.` or `www.`, the host MUST equal `minimax.cn` or `minimax.io` for the MiniMax adapter to be selected. A host that merely contains the substring `minimax` MUST NOT be recognized, because a self-hosted relay under such a name would be given a derived endpoint that does not exist.

Endpoint derivation MUST be performed by the selected adapter, not by a single global rule: the MiniMax adapter derives `https://www.<host>/v1/token_plan/remains`, while the DeepSeek adapter derives `https://<host>/user/balance` and the New API adapter derives `<base url>/api/usage/token/`. ccl MUST NOT hardcode a single host for any adapter: the Chinese and the international MiniMax deployments use different API keys, so the endpoint MUST follow the provider's own base URL.

ccl MUST follow HTTP redirects when it queries a quota endpoint, and MUST keep sending the provider's API key on a redirect that stays within the same host. ccl MUST NOT rewrite the scheme of a configured or derived quota endpoint: an endpoint that serves plain HTTP only would stop working if it were upgraded to HTTPS.

When ccl derives an endpoint from the provider's base URL, it MUST remove the base URL's trailing slashes before appending the path, so that a base URL written with a trailing slash does not produce an endpoint with a doubled slash.

The New API endpoint MUST end with a slash. The interface's own route carries it — a request without it is answered with a redirect — and a gateway that mounts the New API under a path prefix forwards that redirect with the prefix stripped, so following it reaches the gateway's root instead of the interface. Requesting the endpoint with the slash in the first place reaches the interface directly.

A provider that has neither a configured `quota` object nor a recognized base URL MUST be reported as having no quota interface, and ccl MUST NOT attempt a request against a derived endpoint.

#### Scenario: the configured source selects the adapter
- **WHEN** the selected provider's configuration contains `"quota": {"source": "newapi"}`
- **THEN** ccl queries the quota with the New API adapter and applies no host-based adapter detection

#### Scenario: the MiniMax endpoint is derived from the base URL
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://api.minimax.cn/anthropic`
- **THEN** the quota endpoint is `https://www.minimax.cn/v1/token_plan/remains`

#### Scenario: the international endpoint follows the base URL
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://api.minimax.io/anthropic`
- **THEN** the quota endpoint is `https://www.minimax.io/v1/token_plan/remains`

#### Scenario: the DeepSeek endpoint keeps the host of the base URL
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://api.deepseek.com/anthropic`
- **THEN** the quota endpoint is `https://api.deepseek.com/user/balance`

#### Scenario: the New API endpoint follows the base URL prefix
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://one.example.com/api/llm/console/chat`
- **THEN** the quota endpoint is `https://one.example.com/api/llm/console/chat/api/usage/token/`

#### Scenario: a declared endpoint overrides the derivation
- **WHEN** the selected provider's configuration contains `"quota": {"source": "newapi", "endpoint": "https://quota.example.com/api/usage/token"}`
- **THEN** ccl queries `https://quota.example.com/api/usage/token` and applies no derivation

#### Scenario: a lookalike host is not recognized
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://minimax-proxy.corp.example/anthropic` and the configuration declares no `quota` object
- **THEN** ccl reports that the provider has no quota interface and makes no request

#### Scenario: a redirect is followed
- **WHEN** the quota endpoint answers with a redirect to another address on the same host
- **THEN** ccl follows the redirect and keeps sending the provider's API key

#### Scenario: a plain HTTP endpoint is not rewritten
- **WHEN** the quota endpoint is `http://quota.internal.example/usage`
- **THEN** ccl queries it over plain HTTP rather than upgrading it to HTTPS

#### Scenario: a base URL with a trailing slash yields a single slash
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://one.example.com/api/llm/console/chat/`
- **THEN** the quota endpoint is `https://one.example.com/api/llm/console/chat/api/usage/token/`

### Requirement: ccl models the quota as a list of dimensions

ccl MUST report the quota of a provider as a list of dimensions rather than as a fixed set of named windows, because providers differ in which windows they have.

Each dimension MUST carry an identifier, a label, a kind, the duration of its window, the amount used, the total amount, the amount remaining, a unit, a reset time and an expiry time. A value that the interface did not report MUST be absent rather than reported as zero.

The kind MUST distinguish a dimension that has both a period and a total (`window`), a dimension that has a total but no period (`quota`), and a dimension that has no total (`balance`).

The used percentage MUST be derived by ccl rather than taken from the interface: it MUST be the used amount divided by the total amount, as a percentage, and it MUST be absent when the total amount is unknown, when the total is zero, or when the dimension is unlimited.

The used amount and the remaining amount MUST be reported as separate values. They MUST NOT be merged into a single amount, because the used amount and the remaining amount have opposite meanings.

The label and the unit of a dimension MUST consist only of ASCII characters, because a consumer renders them into a terminal line and a missing glyph would make them unreadable. The label of a dimension that has a window MUST be derived from the dimension's duration rather than taken from the interface. The label of a dimension without a window MUST be the dimension's unit. ccl MUST NOT take a label from a raw string reported by the interface.

#### Scenario: a window label comes from the duration
- **WHEN** a dimension has a duration of 18000 seconds
- **THEN** its label is `5h`

#### Scenario: a balance label comes from the unit
- **WHEN** a dimension has no window and its unit is `CNY`
- **THEN** its label is `CNY`

#### Scenario: labels carry no non-ASCII characters
- **WHEN** ccl reports the quota of any supported provider
- **THEN** every reported label and unit consists only of ASCII characters

#### Scenario: a window dimension carries its duration and reset time
- **WHEN** the interface reports a window that lasts 18000 seconds and resets at a known time
- **THEN** the dimension's kind is `window`, its duration is 18000 seconds and it carries that reset time

#### Scenario: a total without a period is a quota dimension
- **WHEN** the interface reports a granted total of 200000000 and a used amount of 2576 and no period
- **THEN** the dimension's kind is `quota` and its duration is absent

#### Scenario: a remaining amount without a total is a balance dimension
- **WHEN** the interface reports a remaining amount of 110 and no total
- **THEN** the dimension's kind is `balance` and the used percentage is absent

#### Scenario: the used percentage is derived
- **WHEN** a dimension reports a used amount of 2576 and a total of 200000000
- **THEN** the reported used percentage is 0.001288

#### Scenario: a missing total leaves the percentage absent
- **WHEN** a dimension reports a used amount but no total
- **THEN** the reported used percentage is absent rather than zero

#### Scenario: used and remaining stay separate
- **WHEN** a dimension reports a used amount of 2576 and a remaining amount of 199997424
- **THEN** the dimension carries the used amount and the remaining amount as distinct values

### Requirement: ccl reads a MiniMax quota interface

ccl MUST send the provider's API key as a bearer token to the MiniMax quota endpoint.

ccl MUST determine the outcome from the response body's `base_resp.status_code`, not from the HTTP status code: the quota interface answers with HTTP 200 even when the request failed, and reports the failure in `base_resp.status_code` and `base_resp.status_msg`. A response whose `base_resp.status_code` is not `0` MUST be treated as a failure.

ccl MUST read the remaining percentage of the five-hour window from the entry whose `model_name` is `general`, falling back to the first entry when no such entry exists. The remaining percentage of the weekly window MUST be read from the same entry. An empty `model_remains` array MUST leave the corresponding dimension absent rather than reported as zero.

ccl MUST convert the remaining percentage into a used percentage by subtracting it from 100, so that the value means the same thing as the used percentage reported for every other provider. ccl MUST clamp the result to the range 0 to 100.

ccl MUST derive the duration of each window from the interval the interface reports for it, and MUST NOT assume that the first window lasts five hours or that the second lasts seven days. ccl MUST derive the reset time of a window from the milliseconds remaining until that window resets, added to the current time, and MUST report it as a Unix timestamp in seconds.

ccl MUST NOT interpret the interface's interval status fields, whose values are not documented; it MUST carry their raw values in the dimension's raw data instead.

#### Scenario: the quota is read from the response
- **WHEN** the quota endpoint returns `current_interval_remaining_percent: 41` and `current_weekly_remaining_percent: 70`
- **THEN** the reported five-hour used percentage is 59 and the reported weekly used percentage is 30

#### Scenario: an error body with HTTP 200 is a failure
- **WHEN** the quota endpoint answers HTTP 200 with `base_resp.status_code: 1004`
- **THEN** ccl reports the failure with the message from `base_resp.status_msg`

#### Scenario: the reset time comes from the remaining milliseconds
- **WHEN** the quota endpoint returns `remains_time: 3600000` and `weekly_remains_time: 86400000`
- **THEN** the reported five-hour reset time is about one hour ahead and the weekly reset time about one day ahead

#### Scenario: the window duration comes from the reported interval
- **WHEN** the quota endpoint returns `start_time` and `end_time` that are 18000000 milliseconds apart
- **THEN** the reported duration of that window is 18000 seconds

#### Scenario: a general entry is preferred
- **WHEN** the quota endpoint returns a `model_remains` array whose first entry has `model_name: "other"` and whose second entry has `model_name: "general"`
- **THEN** ccl reads the percentages from the `general` entry

#### Scenario: an empty array leaves the dimension absent
- **WHEN** the quota endpoint returns `model_remains: []`
- **THEN** the reported quota carries no dimension for that window

### Requirement: ccl reads a DeepSeek quota interface

ccl MUST send the provider's API key as a bearer token to the DeepSeek quota endpoint.

ccl MUST determine the outcome from the HTTP status code, because the balance interface reports authentication failures with HTTP 401. ccl MUST tolerate a failure body that is not JSON, because the interface answers with a plain-text message when the request carries no authorization header at all.

ccl MUST read the remaining amount from the `balance_infos` entry and MUST report its currency as the dimension's unit. The amounts are reported as strings and MUST be converted to numbers.

ccl MUST report the balance as a dimension without a total, because the interface reports only the remaining amount and no granted total, so that no used percentage is derived from it.

ccl MUST report the interface's availability flag as a remaining amount of zero when it reports the account as unavailable.

#### Scenario: the balance is read from the response
- **WHEN** the balance endpoint returns `balance_infos` with `currency: "CNY"` and `total_balance: "110.00"`
- **THEN** ccl reports a dimension whose remaining amount is 110 and whose unit is `CNY`

#### Scenario: a plain-text failure is understood
- **WHEN** the balance endpoint answers HTTP 401 with a body that is not JSON
- **THEN** ccl reports the failure and does not treat the body as a quota

#### Scenario: an unavailable account reads as no balance
- **WHEN** the balance endpoint returns `is_available: false`
- **THEN** ccl reports a dimension whose remaining amount is zero

### Requirement: ccl reads a New API quota interface

ccl MUST send the provider's API key as a bearer token to the New API quota endpoint.

ccl MUST determine the outcome from the response body's `code` field, which is `true` on success, and MUST treat any other value as a failure. ccl MUST tolerate a failure body that carries an `error` object instead, because a rejected key is answered with HTTP 401 before the interface itself answers.

ccl MUST read the granted total, the used amount and the remaining amount from the response's `data` object, and MUST report the used amount as the dimension's used amount and the granted total as its total, so that a used percentage is derived.

ccl MUST report the granted total as the total rather than as the remaining amount, because the granted total is the ceiling the token was created with and the remaining amount is what is left of it.

ccl MUST convert the amounts into the unit named by the configuration, dividing them by the configured `quotaPerUnit` when one is configured, and MUST otherwise assume the default of 500000. ccl MUST report a dimension as unlimited when the interface reports `unlimited_quota` as true, in which case no used percentage MUST be derived.

ccl MUST report the expiry time when the interface reports one that is not zero.

#### Scenario: the granted total becomes the total of the dimension
- **WHEN** the quota endpoint returns `total_granted: 200000000`, `total_used: 2576` and `total_available: 199997424`
- **THEN** ccl reports a dimension whose total is 200000000, whose used amount is 2576 and whose remaining amount is 199997424

#### Scenario: the amounts are converted into the configured unit
- **WHEN** the quota endpoint returns `total_granted: 200000000` and the configuration declares `quotaPerUnit: 500000` and `displayUnit: "USD"`
- **THEN** ccl reports the dimension in the unit `USD` with a total of 400 and a used amount of 0.005152

#### Scenario: an error code is a failure
- **WHEN** the quota endpoint answers with `code: false` and a message
- **THEN** ccl reports the failure with that message

#### Scenario: a rejected key is understood
- **WHEN** the quota endpoint answers HTTP 401 with an `error` object
- **THEN** ccl reports the failure and does not treat the body as a quota

#### Scenario: an unlimited token has no percentage
- **WHEN** the quota endpoint returns `unlimited_quota: true`
- **THEN** ccl reports the dimension as unlimited and derives no used percentage

#### Scenario: a zero expiry means no expiry
- **WHEN** the quota endpoint returns `expires_at: 0`
- **THEN** the reported dimension carries no expiry time

### Requirement: ccl queries the quota over the provider's own network path

When ccl queries a quota interface, it MUST reach it over the network path that the provider itself uses. It MUST take the proxy settings from that provider's `env` block — `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` — and MUST NOT use the proxy settings of the daemon's own environment.

ccl MUST NOT send the quota request through a proxy when the quota endpoint's host is excluded by the provider's `NO_PROXY` list, and MUST honor both the upper-case and the lower-case spelling of that list. A host excluded by `NO_PROXY` MUST be matched by suffix, so that a list entry of `.example.com` excludes `api.example.com`.

The reason for this requirement is that a provider's `env` block is delivered to Claude Code through the `--settings` parameter rather than through the environment of the daemon: a provider reachable only through a proxy would work for Claude Code while a quota query issued without that proxy would fail.

#### Scenario: a proxied provider is queried through its proxy
- **WHEN** the selected provider's `env` block contains `HTTP_PROXY` and no `NO_PROXY`
- **THEN** ccl sends the quota request through that proxy

#### Scenario: an excluded host is queried directly
- **WHEN** the selected provider's `env` block contains `NO_PROXY` with the entry `.example.com` and the quota endpoint's host is `api.example.com`
- **THEN** ccl sends the quota request directly, without a proxy

#### Scenario: the lower-case no-proxy list is honored
- **WHEN** the selected provider's `env` block contains `no_proxy` with the entry `.example.com` and the quota endpoint's host is `api.example.com`
- **THEN** ccl sends the quota request directly, without a proxy

### Requirement: ccl refreshes the quota periodically in the daemon

ccl MUST refresh the quota of the providers that declare a quota interface, on a periodic schedule, and MUST do that work in the daemon rather than in the process that reports the quota.

The refresh interval MUST default to 60 seconds, and a provider MUST be able to override it through its `quota` configuration.

The daemon MUST refresh every provider that declares a quota interface **and that has reported activity recently**, and MUST NOT refresh a provider that declares none. A provider counts as recently active when it has reported activity within the recent window, and the daemon MUST NOT query a provider that falls outside that window, because the quota of a provider that nobody is using is not worth the request. Two providers that resolve to the same quota endpoint and the same API key MUST be queried once for that cycle, and the result MUST be recorded for both.

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

#### Scenario: a provider that was not active recently is not queried
- **WHEN** a provider declares a quota interface but has not reported activity within the recent window
- **THEN** ccl does not query the quota interface for that provider

#### Scenario: a provider that is active is queried
- **WHEN** a provider has reported activity within the recent window
- **THEN** ccl queries the quota interface for that provider

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

### Requirement: ccl stores the quota in a local data file

ccl MUST store the quota of every provider in a single local data file, and MUST record for each provider the time at which its quota was fetched.

The data file MUST be written atomically, so that a concurrent reader never observes a partially written file, and MUST be readable only by the user.

The data file MUST NOT contain the API key in plain text. When it identifies which quota pool a provider's quota belongs to, it MUST use a hash of that pool's endpoint and API key.

The data file MUST NOT keep an entry for a provider that no longer exists, and MUST keep the entry of a provider whose latest refresh failed.

#### Scenario: the data file records when the quota was fetched
- **WHEN** ccl records the quota of a provider
- **THEN** the recorded entry carries the time at which that quota was fetched

#### Scenario: the data file holds no plaintext key
- **WHEN** ccl writes the data file
- **THEN** the data file contains no plaintext API key

#### Scenario: the data file is written atomically
- **WHEN** ccl writes the data file
- **THEN** a reader either sees the previous content or the new content, never a partially written file

#### Scenario: an entry of a removed provider is dropped
- **WHEN** a provider that was recorded in the data file no longer exists
- **THEN** ccl stops recording an entry for it

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

### Requirement: ccl can refresh a provider's quota on demand

ccl MUST provide an option that makes the quota command query the quota interface of the selected provider, record the result in the local data file, and report it. When that option is given, the command MUST make a request to the quota interface; without it, the command MUST make none.

The on-demand refresh MUST NOT require the daemon to be running.

When the on-demand refresh fails, the command MUST exit with status 1 and MUST leave the previously recorded quota of that provider untouched.

A provider that has no quota interface MUST be reported with status 2 without any request being made, whether the on-demand refresh was asked for or not.

#### Scenario: the on-demand refresh records the quota
- **WHEN** the command runs with the on-demand refresh option for a provider with a quota interface
- **THEN** it queries the quota interface, records the result in the local data file, and reports it

#### Scenario: the on-demand refresh needs no daemon
- **WHEN** the command runs with the on-demand refresh option while the daemon is not running
- **THEN** it still queries the quota interface and records the result

#### Scenario: a failed on-demand refresh keeps the recorded quota
- **WHEN** the on-demand refresh fails
- **THEN** the command exits with status 1 and the previously recorded quota of that provider is left untouched

#### Scenario: a provider without a quota interface is not queried
- **WHEN** the command runs with the on-demand refresh option for a provider that has no quota interface
- **THEN** it exits with status 2 and makes no request

### Requirement: ccl warns about a provider whose base URL is not encrypted

ccl MUST warn when the selected provider's `ANTHROPIC_BASE_URL` uses the `http` scheme, because the provider's API key would then travel over the network unencrypted on the first request. ccl MUST NOT rewrite the provider's base URL on the user's behalf.

The warning MUST NOT prevent the launch.

#### Scenario: a plaintext base URL is reported
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` starts with `http://`
- **THEN** ccl reports a warning naming the provider and the plain-text base URL, and the launch proceeds

