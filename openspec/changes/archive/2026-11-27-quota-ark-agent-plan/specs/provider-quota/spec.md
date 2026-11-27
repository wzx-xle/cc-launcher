## ADDED Requirements

### Requirement: ccl reads a Volcengine Ark Agent Plan quota interface

ccl MUST query the quota interface of a Volcengine Ark Agent Plan deployment at `https://ark.<region>.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01`, and MUST derive `<region>` from the region in the provider's base URL host.

ccl MUST report one dimension per window that constrains text model inference: a five hour window, a weekly window and a monthly window. Each dimension MUST carry the window's duration, so that the five hour window carries a duration of 5 hours, the weekly window 7 days and the monthly window 30 days. ccl MUST report these dimensions as windows that reset.

ccl MUST report the quota as the quota of the account that owns the access key, and MUST NOT present it as the quota of a single API key. The interface of that vendor takes no API key as an input, so the quota it reports belongs to the account and is shared by every key and every tool that spends from the same subscription. ccl MUST NOT derive a per-key quota from a usage report instead, because such a report carries no ceiling and therefore yields no percentage.

ccl MUST NOT report the daily window of the interface, because that window counts the usage of the image, speech and harness capabilities rather than the usage of text model inference, and its used amount therefore stays at zero for a provider that runs text models.

ccl MUST read the total of a window from the response's `Quota` field and the used amount from its `Used` field. Either field MAY carry a number or a string in the interface's response, and ccl MUST parse either form as a decimal number.

ccl MUST report a dimension as unlimited when the interface reports a total of zero, in which case ccl MUST derive no used percentage and MUST report no total, because a total of zero means there is no ceiling to derive a percentage from.

ccl MUST report the remaining amount as the difference between the total and the used amount, and MUST NOT report a negative remaining amount.

ccl MUST report the reset time of a dimension from the window's `ResetTime` field, which carries a Unix millisecond timestamp.

ccl MUST NOT report a dimension for a window whose total cannot be parsed as a number, and MUST NOT treat an unparsable amount as zero, because zero is a conclusion about the amount while an unparsable amount means the amount is unknown. A window whose amount cannot be parsed MUST NOT discard the other windows of the same response.

ccl MUST treat a response that carries no `Result` object as a failure, because that means the interface answered with a shape ccl does not know.

ccl MUST treat an HTTP 429 as a rate limit rejection, so that the recorded wait applies to it.

ccl MUST report the interface's own failure reason in the error message, so that a rejected credential can be told apart from a rejected signature and from an unreachable interface.

A failure MUST leave the previously recorded quota of that provider in place, so that a changed response shape does not discard the quota that was already known.

#### Scenario: the windows become dimensions
- **WHEN** the quota interface returns a five hour window of quota `10000` and used `328.5542`, a weekly window of quota `35000` and used `328.5542`, and a monthly window of quota `100000` and used `328.5542`
- **THEN** ccl reports three dimensions whose durations are 5 hours, 7 days and 30 days, whose totals are 10000, 35000 and 100000, and whose used amounts are all 328.5542

#### Scenario: the daily window is not reported
- **WHEN** the quota interface returns a daily window whose used amount is `0`
- **THEN** ccl reports no dimension for the daily window

#### Scenario: the quota belongs to the account rather than to the key
- **WHEN** two providers of the same account spend from the same subscription
- **THEN** ccl reports the same quota for both of them, because the interface reports the quota of the account and takes no API key as an input

#### Scenario: the amounts are read from numbers
- **WHEN** the quota interface returns a weekly window whose quota is `35000` and whose used amount is `328.5542`
- **THEN** ccl reports that window's total as 35000 and its used amount as 328.5542

#### Scenario: the amounts are read from strings
- **WHEN** the quota interface returns a weekly window whose quota is the string `"35000"` and whose used amount is the string `"328.5542"`
- **THEN** ccl reports that window's total as 35000 and its used amount as 328.5542

#### Scenario: the remaining amount is derived
- **WHEN** the quota interface returns a weekly window whose quota is `500.0` and whose used amount is `150.0`
- **THEN** ccl reports that window's remaining amount as 350

#### Scenario: an exceeded window has no negative remaining amount
- **WHEN** the quota interface returns a weekly window whose quota is `500.0` and whose used amount is `600.0`
- **THEN** ccl reports that window's remaining amount as 0

#### Scenario: a zero total means the window is unlimited
- **WHEN** the quota interface returns a five hour window whose quota is `0`
- **THEN** ccl reports that window as unlimited and derives no used percentage for it

#### Scenario: the reset time comes from the window
- **WHEN** the quota interface returns a five hour window whose reset time is `1778806800000`
- **THEN** ccl reports that window's reset time as the moment of that Unix millisecond timestamp

#### Scenario: the endpoint follows the region of the base URL
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://ark.cn-beijing.volces.com/api/plan`
- **THEN** ccl queries `https://ark.cn-beijing.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01`

#### Scenario: an unparsable amount does not become zero
- **WHEN** the quota interface returns a weekly window whose quota is `""` and a five hour window whose quota is `10000`
- **THEN** ccl reports no dimension for the weekly window and reports the five hour window with a total of 10000

#### Scenario: the response shape changed
- **WHEN** the quota interface answers with a body that carries no `Result` object
- **THEN** ccl reports the failure and keeps the previously recorded quota

#### Scenario: the interface rate limits the caller
- **WHEN** the quota interface answers HTTP 429
- **THEN** ccl records a wait for that interface and does not query it again until the wait has elapsed

### Requirement: ccl authenticates a Volcengine Ark quota request with an access key

ccl MUST authenticate the quota request with a signed access key, because the interface rejects a bearer token: a request carrying the provider's API key as a bearer token is answered with a rejection of the authorization header.

ccl MUST sign the request with the version 4 signature scheme of the vendor, over the request method, the request path, the request query, the signed request headers and the request body, and MUST derive the signing key by the chain of keyed hashes over the date, the region, the service and the literal `request`.

ccl MUST include the request's date, the hash of its body and the host among the signed headers, and MUST determine the signed headers by the vendor's rule rather than by a fixed list, so that a request carrying an additional vendor-prefixed header does not leave that header unsigned.

When the access key is a temporary one, ccl MUST send the session token with the request and MUST include it among the signed headers.

ccl MUST take the access key from the provider's quota configuration, and MUST fall back to the environment variables `VOLCENGINE_ACCESS_KEY_ID` and `VOLCENGINE_SECRET_ACCESS_KEY` when that configuration carries neither.

ccl MUST NOT take the access key from the provider's `env` block, because that block is delivered to Claude Code, and an account-level access key must not reach the model, its hooks or its subprocesses.

ccl MUST NOT include the access key or its secret in an error message.

ccl MUST report a missing access key as a failure that names the missing configuration, so that the user knows which credential to provide.

A failure to authenticate MUST leave the previously recorded quota of that provider in place.

#### Scenario: the configured access key signs the request
- **WHEN** the provider's quota configuration carries an access key and a secret key
- **THEN** ccl sends the quota request with a signature computed from that access key and secret key

#### Scenario: the environment provides the access key
- **WHEN** the provider's quota configuration carries no access key and the environment carries `VOLCENGINE_ACCESS_KEY_ID` and `VOLCENGINE_SECRET_ACCESS_KEY`
- **THEN** ccl sends the quota request with a signature computed from that access key and secret key

#### Scenario: the access key is not taken from the environment block
- **WHEN** the provider's `env` block carries an access key
- **THEN** ccl does not use that value to sign the quota request

#### Scenario: the signature matches the vendor's
- **WHEN** the request is signed for access key `AKTESTKEY`, secret key `SKTESTSECRET`, date `20261127T032146Z`, body `{}` and host `ark.cn-beijing.volcengineapi.com`
- **THEN** the authorization header is `HMAC-SHA256 Credential=AKTESTKEY/20261127/cn-beijing/ark/request, SignedHeaders=content-type;host;x-content-sha256;x-date, Signature=17647640ac5545aa9c6d4990d0059db3ff19d4a098411b9381157953d62f78a5`

#### Scenario: no access key is available
- **WHEN** neither the provider's quota configuration nor the environment carries an access key
- **THEN** ccl reports that the access key is missing and keeps the previously recorded quota

#### Scenario: a temporary access key carries its session token
- **WHEN** the access key comes with a session token
- **THEN** ccl sends the session token with the request and includes it among the signed headers

#### Scenario: a rejected signature is reported as such
- **WHEN** the quota interface rejects the signature of the request
- **THEN** ccl reports the failure with the interface's own reason, and reports no used percentage for the provider in that cycle

## MODIFIED Requirements

### Requirement: ccl resolves the quota adapter and endpoint of a provider

ccl MUST determine which quota adapter serves the selected provider, and which quota endpoint it queries, before querying any quota.

When the provider configuration declares a top-level `quota` object, ccl MUST take the adapter from its `source` field and MUST NOT apply host-based adapter detection. When the `quota` object declares an `endpoint`, ccl MUST use that URL as the quota endpoint and MUST NOT derive one.

Otherwise ccl MUST detect the adapter from the host of the provider's `ANTHROPIC_BASE_URL`: after removing a leading `api.` or `www.`, the host MUST equal `minimax.cn` or `minimax.io` for the MiniMax adapter to be selected. A host that merely contains the substring `minimax` MUST NOT be recognized, because a self-hosted relay under such a name would be given a derived endpoint that does not exist.

ccl MUST detect the Volcengine Ark adapter from a base URL whose host has the shape `ark.<region>.volces.com` and whose path contains `/api/plan`. The path MUST be part of the detection, because the pay-as-you-go deployment of the same host serves the path `/api/v3` and carries no Agent Plan, so that a host-only rule would select a plan interface for a deployment that has no plan.

Endpoint derivation MUST be performed by the selected adapter, not by a single global rule: the MiniMax adapter derives `https://www.<host>/v1/token_plan/remains`, while the DeepSeek adapter derives `https://<host>/user/balance` and the New API adapter derives `<base url>/api/usage/token/`. ccl MUST NOT hardcode a single host for any adapter: the Chinese and the international MiniMax deployments use different API keys, so the endpoint MUST follow the provider's own base URL. The Volcengine Ark adapter derives its endpoint from the region in the base URL's host, because the interface of that vendor is served from a different host than the deployment itself.

ccl MUST follow HTTP redirects when it queries a quota endpoint, and MUST keep sending the provider's API key on a redirect that stays within the same host. ccl MUST NOT rewrite the scheme of a configured or derived quota endpoint: an endpoint that serves plain HTTP only would stop working if it were upgraded to HTTPS.

When ccl derives an endpoint from the provider's base URL, it MUST remove the base URL's trailing slashes before appending the path, so that a base URL written with a trailing slash does not produce an endpoint with a doubled slash.

The New API endpoint MUST end with a slash. The interface's own route carries it — a request without it is answered with a redirect — and a gateway that mounts the New API under a path prefix forwards that redirect with the prefix stripped, so that following it reaches the gateway's root instead of the interface. Requesting the endpoint with the slash in the first place reaches the interface directly.

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

#### Scenario: the Volcengine Ark endpoint follows the region of the base URL
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://ark.cn-beijing.volces.com/api/plan`
- **THEN** the quota endpoint is `https://ark.cn-beijing.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01`

#### Scenario: the pay-as-you-go deployment of the same host is not recognized
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://ark.cn-beijing.volces.com/api/v3` and the configuration declares no `quota` object
- **THEN** ccl reports that the provider has no quota interface and makes no request

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

### Requirement: ccl refreshes the quota periodically in the daemon

ccl MUST refresh the quota of the providers that declare a quota interface, on a periodic schedule, and MUST do that work in the daemon rather than in the process that reports the quota.

The refresh interval MUST default to 60 seconds, and a provider MUST be able to override it through its `quota` configuration.

The daemon MUST refresh every quota pool that has a provider which declares a quota interface **and that has reported activity recently**, and MUST NOT refresh a quota pool that has none. A provider counts as recently active when it has reported activity within the recent window. When any provider of a quota pool is recently active, the daemon MUST refresh that pool and MUST record the result for every provider of that pool, because the quota of a pool is a single fact and MUST NOT be recorded with different values for different providers. The daemon MUST NOT query a pool whose providers are all outside that window, because the quota of a provider that nobody is using is not worth the request. Two providers that resolve to the same quota endpoint and the same pool key MUST be queried once for that cycle, and the result MUST be recorded for both.

The pool key MUST default to the provider's API key, and an adapter MAY declare a different pool key for its provider, because the quota of some vendors is counted per account rather than per API key. A pool key that names an access key MUST NOT be recorded in clear text in the local data file.

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

#### Scenario: providers sharing an account are queried once
- **WHEN** two providers resolve to the same quota endpoint and the same access key, but carry different API keys
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
