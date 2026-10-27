# session-context-attrs Specification

## Purpose
TBD - created by archiving change web-usage-stats-ui. Update Purpose after archive.
## Requirements
### Requirement: ccl carries the launch context to the daemon through OTel resource attributes
When `ccl` launches Claude Code, it MUST make the current launch context — the selected provider's name, the working directory it was invoked from, and the provider's endpoint host — available to the daemon that receives Claude Code's OTel exports.

The context MUST travel **inside the OTLP payload**, as resource attributes, by setting `OTEL_RESOURCE_ATTRIBUTES` in the **child process environment** with these keys:

- `ccl.provider` — the name of the selected provider (the provider configuration's file name)
- `ccl.cwd` — the absolute path of the working directory `ccl` was invoked from
- `ccl.provider_endpoint` — the host of the provider's `ANTHROPIC_BASE_URL`

The context MUST NOT be carried by the daemon reading its own environment. The daemon is a separate long-running process; its environment describes when the daemon started, not which session a given export belongs to.

`ccl` MUST NOT write these attributes into `~/.claude/settings.json`. The settings file's `env` block applies to every Claude Code process on the machine, so a value written there would be inherited by sessions that `ccl` did not launch, and would attribute them to the previously launched project.

`ccl` MUST place the attributes into the environment that is actually handed to the child process. On platforms where the launcher replaces the process with an explicit environment list, setting a process-wide environment variable alone is not sufficient.

#### Scenario: the attributes reach the payload
- **WHEN** the user runs `ccl` in `/home/u/proj`, selects the provider `minimax`, and Claude Code exports metrics
- **THEN** the exported payload's resource attributes contain `ccl.provider` = `minimax` and `ccl.cwd` = `/home/u/proj`

#### Scenario: the endpoint host is carried alongside the provider name
- **WHEN** the selected provider's `ANTHROPIC_BASE_URL` is `https://api.minimax.cn/anthropic`
- **THEN** the exported payload's resource attributes contain `ccl.provider_endpoint` = `api.minimax.cn`

#### Scenario: the attributes are not written to the settings file
- **WHEN** `ccl` launches Claude Code
- **THEN** the `env` block of `~/.claude/settings.json` contains no `ccl.provider`, `ccl.cwd` or `ccl.provider_endpoint` key, and no `OTEL_RESOURCE_ATTRIBUTES` key that `ccl` added

#### Scenario: a session started without ccl is not misattributed
- **WHEN** a user runs `claude` directly, without `ccl`
- **THEN** the exported payload carries no `ccl.*` resource attribute, and the session is not attributed to the previously launched project or provider

### Requirement: attribute values are percent-encoded
Every value `ccl` writes into `OTEL_RESOURCE_ATTRIBUTES` MUST be percent-encoded so that it contains only unreserved characters (letters, digits, `-`, `.`, `_`, `~`).

This encoding is mandatory, not cosmetic: Claude Code splits the attribute string on `,` and `=`, and rejects a value that contains a space, a control character or a non-ASCII byte. A rejected value causes the **entire** attribute map to be discarded — including attributes whose values were valid — so an unencoded path or provider name silently removes the whole context.

An attribute whose encoded value would exceed 255 characters MUST be omitted entirely rather than emitted in truncated or invalid form, for the same reason.

Percent-encoding is reversible: the receiver MUST decode the value, so the stored value is the original path, host or provider name.

#### Scenario: a path containing a space is carried intact
- **WHEN** `ccl` is invoked in `/home/u/my project` and the provider is `minimax`
- **THEN** the payload's resource attributes contain `ccl.cwd` = `/home/u/my project` and `ccl.provider` = `minimax`

#### Scenario: a path containing non-ASCII characters is carried intact
- **WHEN** `ccl` is invoked in `/home/u/测试项目`
- **THEN** the payload's resource attributes contain `ccl.cwd` = `/home/u/测试项目`

#### Scenario: an over-long value is omitted without harming the others
- **WHEN** the working directory's encoded form exceeds 255 characters
- **THEN** the payload carries no `ccl.cwd` attribute, but still carries `ccl.provider` and `ccl.provider_endpoint`

### Requirement: ccl merges with a pre-existing attribute value
If the child process environment already contains an `OTEL_RESOURCE_ATTRIBUTES` value, `ccl` MUST preserve it and append its own attributes to it. Its own keys take precedence over a pre-existing value of the same key.

If `~/.claude/settings.json` defines `OTEL_RESOURCE_ATTRIBUTES`, that value takes precedence over the child process environment when Claude Code starts, and the injected context is therefore lost. `ccl` MUST detect this case, MUST warn the user that the provider and project dimensions will be empty, and MUST NOT silently modify the settings file.

#### Scenario: an existing attribute value is preserved
- **WHEN** the environment already contains `OTEL_RESOURCE_ATTRIBUTES=team=platform`
- **THEN** the exported payload's resource attributes contain `team` = `platform` in addition to `ccl.provider` and `ccl.cwd`

#### Scenario: ccl's own key wins over a pre-existing one
- **WHEN** the environment already contains `OTEL_RESOURCE_ATTRIBUTES=ccl.provider=stale`
- **THEN** the exported payload's resource attributes contain `ccl.provider` = the actually selected provider

#### Scenario: a settings-file attribute value is reported
- **WHEN** `~/.claude/settings.json` defines `OTEL_RESOURCE_ATTRIBUTES`
- **THEN** `ccl` prints a warning naming the settings file and stating that the provider and project dimensions will be empty

### Requirement: the daemon reads the launch context from the payload's resource attributes
The daemon MUST read `ccl.provider`, `ccl.cwd` and `ccl.provider_endpoint` from the resource attributes of the OTLP metrics payload it receives, and MUST attach them to the sessions recorded in that payload.

The values MUST be taken from the payload of the export in which the session's metrics appear. The daemon MUST NOT derive them from a process-wide "currently active" value, because such a value describes the present, not the session.

When an export carries no `ccl.*` attribute, the daemon MUST NOT invent one and MUST NOT fail the export.

#### Scenario: the context is attached to the session
- **WHEN** the daemon receives an export whose resource attributes carry `ccl.provider` = `minimax` and `ccl.cwd` = `/home/u/proj`, and the export contains metrics for session `S`
- **THEN** the session row for `S` records the provider `minimax` and the working directory `/home/u/proj`

#### Scenario: an export without context attributes is accepted
- **WHEN** the daemon receives an export whose resource attributes carry no `ccl.*` attribute
- **THEN** the export is parsed and persisted as before, with the context fields left empty, and no error is raised

#### Scenario: two exports with different contexts do not overwrite each other
- **WHEN** the daemon receives one export carrying `ccl.provider` = `a` for session `S1`, and later another export carrying `ccl.provider` = `b` for session `S2`
- **THEN** the provider of `S1` remains `a` and the provider of `S2` is `b`

### Requirement: session rows persist the launch context
The daemon MUST persist the launch context on the session row: the provider name in `sessions.provider`, the working directory in `sessions.cwd`, and the endpoint host in `sessions.provider_endpoint`.

A non-empty stored value MUST NOT be overwritten by an empty one. A later non-empty value MUST fill a previously empty field.

Adding the endpoint host column MUST be performed through an explicit, idempotent schema migration.

#### Scenario: a new session row carries the context
- **WHEN** the daemon writes a session row for a session whose export carried `ccl.provider` = `minimax`, `ccl.cwd` = `/home/u/proj` and `ccl.provider_endpoint` = `api.minimax.cn`
- **THEN** the session row stores all three values

#### Scenario: an empty value does not overwrite a known one
- **WHEN** a session row already stores `ccl.provider` = `minimax` and a later export for that session carries no `ccl.provider`
- **THEN** the stored provider remains `minimax`

#### Scenario: a later value fills a previously empty field
- **WHEN** a session row stores an empty `ccl.cwd` and a later export for that session carries `ccl.cwd` = `/home/u/proj`
- **THEN** the stored working directory becomes `/home/u/proj`

#### Scenario: the schema is migrated exactly once
- **WHEN** the daemon starts against a database created by a version without the endpoint host column
- **THEN** the column is added, and starting the daemon again does not add it a second time

### Requirement: the launch context is absent for sessions started without ccl
A session whose export carries no context attributes MUST be recorded with empty context fields, and the CLI and the dashboard MUST show it as unknown rather than as an empty-string dimension of its own.

The daemon MUST NOT fall back to a currently configured provider when reading an export, because that would attribute past sessions to the provider that happens to be selected now.

#### Scenario: a session without context is grouped as unknown
- **WHEN** the daemon records a session whose export carried no `ccl.*` attribute
- **THEN** grouping that session by provider or by project yields an "unknown" bucket, and the session is not attributed to any configured provider

