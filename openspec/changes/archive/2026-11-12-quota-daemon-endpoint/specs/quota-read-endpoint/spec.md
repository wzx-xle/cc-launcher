## ADDED Requirements

### Requirement: the daemon serves the quota of a provider over a read-only endpoint that only accepts loopback callers

The daemon MUST serve the recorded quota of a provider at the path `/ccl/quota`, taking the provider's name from a `provider` query parameter.

The endpoint MUST accept requests whose source address is a loopback address, and MUST refuse requests whose source address is not a loopback address. The decision MUST be based on the address of the peer the daemon actually accepted the connection from, and MUST NOT be based on any header the request carries, because the daemon serves the endpoint directly and has no trusted forwarding chain.

The daemon MUST refuse a request that carries no provider name.

The endpoint MUST NOT be registered at all when the daemon cannot serve quota data, so that a request to the path yields a not-found answer rather than an answer that looks successful but carries nothing.

The daemon MUST mark every answer of the endpoint with a header that identifies the answer as coming from this endpoint, so that a consumer can tell an answer of the endpoint apart from an answer of a path the daemon does not know.

#### Scenario: a loopback caller receives the quota
- **WHEN** a request for `/ccl/quota` arrives over a loopback connection with the query parameter `provider` naming a provider
- **THEN** the daemon answers with that provider's quota document

#### Scenario: a caller from outside the loopback is refused
- **WHEN** a request for `/ccl/quota` arrives from a source address that is not a loopback address
- **THEN** the daemon refuses the request and does not report any quota

#### Scenario: a forwarded address does not grant access
- **WHEN** a request for `/ccl/quota` arrives over a loopback connection but carries a header naming a non-loopback address as its origin
- **THEN** the daemon serves the request, because the header does not decide the answer

#### Scenario: a request without a provider name is refused
- **WHEN** a request for `/ccl/quota` arrives with no provider name
- **THEN** the daemon refuses the request and does not report any quota

#### Scenario: an answer that is not from the endpoint is not taken for an answer of the endpoint
- **WHEN** a request for `/ccl/quota` receives an answer that does not carry the endpoint's marker header, because the daemon is an older version that does not know that path
- **THEN** the consumer treats the endpoint as unavailable and obtains the quota by another means, rather than reporting that no quota is recorded

### Requirement: the endpoint reports the quota in the same document as the quota command

The document the endpoint returns MUST carry the same fields as the document the quota command prints: the provider's name, the source that produced the quota, whether the quota is stale, the time at which the quota was fetched, and the list of dimensions.

The endpoint MUST reach the same verdict on staleness as the quota command reaches for the same provider, because both MUST derive it from the same recorded fetch time and the same refresh interval of that provider, including the fallback to 60 seconds when that interval cannot be read.

The endpoint MUST report a provider that declares no quota interface as distinct from a provider that declares one but has no quota recorded yet, so that a consumer can tell "this provider has no quota" from "the data is not ready".

The endpoint MUST read the quota from the local data file, through the same reader that tolerates that file being replaced while it is read.

#### Scenario: the document carries the same fields as the command's
- **WHEN** the endpoint serves a provider that has a recorded quota
- **THEN** the document it returns carries the provider's name, the source, the stale flag, the fetch time and the list of dimensions

#### Scenario: the stale verdict agrees with the command's
- **WHEN** the endpoint serves a provider whose recorded quota was fetched longer ago than that provider's refresh interval
- **THEN** the endpoint marks the quota stale, exactly as the quota command marks it stale

#### Scenario: a provider without a quota interface is reported apart from one without a record
- **WHEN** the endpoint is asked for a provider that declares no quota interface
- **THEN** the endpoint answers in a way that distinguishes that provider from one that declares a quota interface but has no quota recorded yet

#### Scenario: a provider without a recorded quota is reported as absent
- **WHEN** the endpoint is asked for a provider that has no recorded quota
- **THEN** the endpoint reports that no quota is recorded for it

### Requirement: ccl delivers the location of the quota endpoint to consumers as CCL_QUOTA_URL

When ccl launches Claude Code, it MUST place the location of the quota endpoint in the environment, under the variable name `CCL_QUOTA_URL`.

The value MUST be the address of the endpoint on the loopback interface of the machine, naming the daemon's fixed port and the endpoint's path.

ccl MUST deliver the variable through the `env` block of the settings object it delivers through the `--settings` parameter. This is the channel that decides the value: it outranks both the user's own settings file and the process environment.

ccl MUST ALSO place the variable in its own process environment, as a redundant second channel, so that the value survives a Claude Code version that stops applying the delivered `env` block to the process environment.

The two copies MUST carry the same value.

ccl MUST deliver the variable regardless of whether the daemon is running, so that a consumer does not have to know the state of the daemon to decide whether to try the endpoint. A consumer that cannot reach the endpoint MUST be able to fall back to reading the quota another way.

A provider configuration is free to contain any key in its `env` block, including `CCL_QUOTA_URL`. The value ccl delivers MUST be ccl's own, regardless of what the provider configuration says, and regardless of what the user's own settings file says.

ccl MUST NOT deliver `CCL_QUOTA_URL` with an empty value. A session that ccl did not launch inherits no `CCL_QUOTA_URL`, and a consumer MUST treat that absence as "this session has no ccl quota endpoint".

The name `CCL_QUOTA_URL` and the shape of the value are a contract with consumers outside this repository. ccl MUST NOT rename it, and MUST NOT change the meaning of its value, without treating the change as a breaking change to this capability.

#### Scenario: the endpoint's location reaches the session
- **WHEN** the user selects a provider and runs `ccl`
- **THEN** the environment of the launched Claude Code process carries `CCL_QUOTA_URL` naming the daemon's quota endpoint

#### Scenario: the value is delivered through the settings env block
- **WHEN** ccl launches Claude Code with a selected provider
- **THEN** the delivered settings object's `env` block contains the key `CCL_QUOTA_URL`

#### Scenario: the process environment carries a redundant copy
- **WHEN** ccl launches Claude Code with a selected provider
- **THEN** ccl's own process environment carries `CCL_QUOTA_URL` with the same value as the delivered settings object's `env` block

#### Scenario: the variable is delivered even when the daemon is down
- **WHEN** ccl launches Claude Code while the daemon is not running
- **THEN** the delivered settings object's `env` block still contains the key `CCL_QUOTA_URL`

#### Scenario: a provider-configured value is replaced
- **WHEN** the selected provider's configuration contains `"env": {"CCL_QUOTA_URL": "http://elsewhere/"}` and the selected provider's name is `claude-official`
- **THEN** the delivered `CCL_QUOTA_URL` is ccl's own value

#### Scenario: the user's own settings file does not replace it either
- **WHEN** `~/.claude/settings.json` has an `env` block with `CCL_QUOTA_URL=http://stale/` and the selected provider's name is `claude-official`
- **THEN** the launched session's `CCL_QUOTA_URL` is ccl's own value

#### Scenario: the variable is never delivered empty
- **WHEN** ccl launches Claude Code
- **THEN** the delivered settings object's `env` block carries a non-empty `CCL_QUOTA_URL`, and ccl delivers no `CCL_QUOTA_URL` key at all rather than an empty value

#### Scenario: a directly launched session has no variable
- **WHEN** the user runs `claude` directly instead of through `ccl`
- **THEN** the launched process's environment carries no `CCL_QUOTA_URL`

### Requirement: the endpoint is an optional channel, and the quota command does not depend on it

A consumer that cannot reach the endpoint MUST be able to obtain the same quota document by another means, and the quota document MUST NOT differ between the two means for the same provider.

The quota command MUST report the quota whether or not the daemon is running. The command MUST NOT fail, and MUST NOT report the absence of a quota, merely because the daemon cannot be reached.

#### Scenario: the quota command works without the daemon
- **WHEN** the quota command runs for a provider with a recorded quota while the daemon is not running
- **THEN** the command reports that provider's quota

#### Scenario: both means report the same document
- **WHEN** the daemon is running and the quota command runs for a provider with a recorded quota
- **THEN** the document the command prints carries the same fields and the same stale verdict as the document the endpoint serves for that provider

#### Scenario: a consumer that cannot reach the endpoint still gets the quota
- **WHEN** a consumer tries the endpoint and the request fails
- **THEN** the consumer can obtain the same quota document through the quota command

### Requirement: the daemon's internal exchange endpoints apply the same loopback restriction

The daemon's endpoints that exist for ccl to exchange information with itself — the endpoint that declares the provider a session uses, and the endpoint that serves the quota — MUST accept requests over loopback connections only, and MUST refuse requests that arrive from a source address that is not a loopback address.

The endpoints that carry the OpenTelemetry protocol, and the endpoints that serve the usage dashboard, MUST NOT be restricted this way: the protocol endpoints have callers outside the machine in some setups, and the dashboard is meant to be opened from another machine.

#### Scenario: the internal endpoints share one restriction
- **WHEN** a request for either the provider declaration or the quota arrives from a source address that is not a loopback address
- **THEN** the daemon refuses it

#### Scenario: the telemetry endpoints stay reachable
- **WHEN** a request for the OpenTelemetry protocol endpoints arrives from a source address that is not a loopback address
- **THEN** the daemon serves it, because that restriction would break a caller outside the machine

#### Scenario: the dashboard stays reachable
- **WHEN** a request for the usage dashboard arrives from a source address that is not a loopback address
- **THEN** the daemon serves it, because the dashboard is meant to be opened from another machine
