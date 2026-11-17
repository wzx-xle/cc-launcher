## ADDED Requirements

### Requirement: ccl reads a codex2api quota interface

ccl MUST send the provider's API key as a bearer token to the codex2api quota endpoint.

ccl MUST derive the endpoint from the provider's base URL by appending the path `/api/key-usage/summary` to the base URL with its trailing slashes removed, so that a deployment served under a path prefix keeps that prefix. ccl MUST report a failure when the base URL is empty.

ccl MUST NOT identify a codex2api deployment from its host. A codex2api deployment can be served from any host, and a wrong identification would send the user's API key to an address that is not a codex2api deployment. The adapter MUST therefore only be used when the provider's quota configuration names it explicitly.

ccl MUST read the used amount from the response's `key.quota_used` and the ceiling from the response's `key.quota_limit`, and MUST report the ceiling as the dimension's total so that a used percentage is derived.

ccl MUST report the dimension as unlimited when the interface reports `quota_limit` as zero, in which case no used percentage is derived and no total is reported. A zero ceiling means the key has no amount limit, so there is no denominator to derive a percentage from.

ccl MUST NOT report the lifetime total from the response's `key.total_used` as the used amount of the dimension, because the lifetime total and the used amount are counted over different periods and the ratio between them and the ceiling would be wrong. The lifetime total MUST be preserved in the dimension's raw fields instead.

ccl MUST NOT report a reset time for the dimension, because the period of the used amount ends when an administrator resets the key's quota, and that moment cannot be known in advance.

ccl MUST treat an HTTP 404 from the endpoint as a failure, because the interface answers that way when the deployment has the public usage page turned off.

ccl MUST treat a response whose body cannot be parsed as a failure.

A failure MUST leave the previously recorded quota of that provider in place, so that a disabled usage page or a changed response shape does not discard the quota that was already known.

#### Scenario: the used amount and the ceiling become the dimension
- **WHEN** the quota endpoint returns `quota_used: 0.8024772` and `quota_limit: 100`
- **THEN** ccl reports a dimension whose used amount is 0.8024772 and whose total is 100

#### Scenario: a zero ceiling means the key has no limit
- **WHEN** the quota endpoint returns `quota_used: 0.8024772` and `quota_limit: 0`
- **THEN** ccl reports the dimension as unlimited and derives no used percentage

#### Scenario: the lifetime total is not the used amount
- **WHEN** the quota endpoint returns `quota_used: 0.8`, `total_used: 12.5` and `quota_limit: 100`
- **THEN** ccl reports the used amount as 0.8 and keeps 12.5 in the dimension's raw fields

#### Scenario: the endpoint is derived from the base URL
- **WHEN** a provider's base URL is `http://codex2api.internal:8317/`
- **THEN** ccl queries `http://codex2api.internal:8317/api/key-usage/summary`

#### Scenario: a path prefix of the base URL is kept
- **WHEN** a provider's base URL is `https://gw.example.com/codex2api`
- **THEN** ccl queries `https://gw.example.com/codex2api/api/key-usage/summary`

#### Scenario: the deployment is not recognized from its host
- **WHEN** a provider's base URL is `http://codex2api.internal:8317` and its quota configuration does not name the adapter
- **THEN** ccl reports that the provider has no quota interface

#### Scenario: the usage page is turned off
- **WHEN** the quota endpoint answers HTTP 404
- **THEN** ccl reports the failure and keeps the previously recorded quota

#### Scenario: the response shape changed
- **WHEN** the quota endpoint answers with a body that cannot be parsed into the expected fields
- **THEN** ccl reports the failure and keeps the previously recorded quota
