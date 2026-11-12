## MODIFIED Requirements

### Requirement: ccl reports the quota through a command that reads the local data file

ccl MUST provide a command that reports the quota of a provider. The command MUST read the recorded quota from the local data file — either by reading that file itself, or by asking the daemon, which reads that same file — and MUST NOT make a request to the quota interface itself.

When the daemon is reachable, the command MUST obtain the quota through the daemon and MUST report the document the daemon serves, so that the command and a consumer that reads the quota over the daemon's endpoint report the same document for the same provider. When the daemon cannot be reached, the command MUST read the local data file itself and MUST report the same document it would have reported through the daemon.

Its JSON output MUST carry the dimensions of the quota as a list, each with a kind, a duration where one is known, a used percentage where one can be derived, and a reset time where one is known. A dimension that could not be determined MUST be absent from the list rather than reported as an empty value.

The command MUST accept the provider to report on as an explicit option. When no option is given, it MUST fall back to the provider named by the `CCL_PROVIDER` environment variable, and then to the provider recorded as active. This lets a consumer that runs inside a session started by ccl name the provider without knowing ccl's own state.

The command MUST report whether the quota it returns is stale, by comparing the time at which the quota was fetched against the refresh interval of that provider. When the refresh interval of that provider cannot be read, the command MUST use 60 seconds for that comparison. The comparison MUST allow for the fact that the time of the fetch is recorded with a resolution of one second, so that a quota that is refreshed on schedule is not reported as stale between two refreshes.

The command MUST report the time at which the quota it returns was fetched, so that a consumer can tell how old the data is without consulting the data file.

When the provider has no quota interface, the command MUST exit with status 2 and MUST NOT print a quota document. When no quota has been recorded for the provider, the command MUST exit with status 1 and MUST NOT print a quota document. Only a successful report MUST exit with status 0.

A missing quota MUST NOT prevent the launch of Claude Code, and MUST NOT affect the provider's own configuration.

#### Scenario: the JSON output carries a list of dimensions
- **WHEN** the command runs for a provider with a recorded quota
- **THEN** its JSON output carries a `dimensions` list whose entries carry a `kind` and, where derivable, a `used_percentage`

#### Scenario: the command makes no request to a quota interface
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

#### Scenario: the command reads the quota through the daemon when the daemon is reachable
- **WHEN** the command runs for a provider with a recorded quota while the daemon is running
- **THEN** the command obtains the quota from the daemon and reports the document the daemon serves

#### Scenario: the command reads the quota itself when the daemon cannot be reached
- **WHEN** the command runs for a provider with a recorded quota while the daemon is not running, or the daemon cannot be reached
- **THEN** the command reads the local data file itself and reports the same document it would have reported through the daemon

#### Scenario: the command does not report an absent quota because the daemon is down
- **WHEN** the command runs for a provider with a recorded quota while the daemon cannot be reached
- **THEN** the command exits with status 0 and prints the quota document

#### Scenario: both paths agree on the document
- **WHEN** the daemon serves a provider's quota and the quota command runs for that same provider
- **THEN** the document the command prints carries the same fields and the same stale verdict as the document the daemon serves

### Requirement: ccl declares the provider it selected to the daemon

When ccl starts Claude Code with a selected provider, it MUST tell the daemon which provider that session uses, so that the quota of that provider is refreshed right away instead of waiting for the session to report activity.

The declaration MUST NOT delay the launch of Claude Code, and a daemon that cannot be reached MUST NOT stop the launch.

The daemon MUST accept the declaration only when it arrives over a loopback connection, and MUST refuse a declaration that arrives from a source address that is not a loopback address. The decision MUST be based on the address of the peer the daemon actually accepted the connection from, and MUST NOT be based on any header the request carries, because the daemon serves the endpoint directly and has no trusted forwarding chain.

The daemon MUST treat a declared provider as recently active, and MUST refresh the quota pool of that provider in that cycle. A provider that becomes active this way MUST be queried without waiting for its refresh interval to elapse, when its recorded quota is older than that interval.

The daemon MUST accept a provider name only when it names a provider that is configured, and MUST reject a name that names no configured provider.

#### Scenario: the selected provider is declared to the daemon
- **WHEN** ccl starts Claude Code with a selected provider
- **THEN** ccl tells the daemon which provider that session uses

#### Scenario: the declaration does not delay the launch
- **WHEN** the daemon cannot be reached while ccl declares the selected provider
- **THEN** ccl starts Claude Code unaffected

#### Scenario: the declared provider is refreshed in that cycle
- **WHEN** the daemon receives a declaration for a provider whose recorded quota is older than its refresh interval
- **THEN** the daemon queries that provider's quota pool in that cycle

#### Scenario: an unknown provider name is rejected
- **WHEN** the daemon receives a declaration for a name that matches no configured provider
- **THEN** the daemon rejects it and queries no quota interface

#### Scenario: a declaration from outside the loopback is refused
- **WHEN** the daemon receives a declaration from a source address that is not a loopback address
- **THEN** the daemon refuses the declaration and queries no quota interface

#### Scenario: ccl's own declaration is still accepted
- **WHEN** ccl declares the selected provider to the daemon it started on the same machine
- **THEN** the daemon accepts the declaration and treats that provider as recently active

#### Scenario: a forwarded address does not grant access
- **WHEN** the daemon receives a declaration over a loopback connection but the request carries a header naming a non-loopback address as its origin
- **THEN** the daemon accepts the declaration, because the header does not decide the answer
