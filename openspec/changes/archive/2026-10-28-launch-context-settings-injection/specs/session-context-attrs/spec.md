## MODIFIED Requirements

### Requirement: ccl carries the launch context to the daemon through OTel resource attributes
When `ccl` launches Claude Code, it MUST make the current launch context — the selected provider's name, the working directory it was invoked from, and the provider's endpoint host — available to the daemon that receives Claude Code's OTel exports.

The context MUST travel **inside the OTLP payload**, as resource attributes, with these keys:

- `ccl.provider` — the name of the selected provider (the provider configuration's file name)
- `ccl.cwd` — the absolute path of the working directory `ccl` was invoked from
- `ccl.provider_endpoint` — the host of the provider's `ANTHROPIC_BASE_URL`

The context MUST NOT be carried by the daemon reading its own environment. The daemon is a separate long-running process; its environment describes when the daemon started, not which session a given export belongs to.

`ccl` MUST NOT write these attributes into `~/.claude/settings.json`. The settings file's `env` block applies to every Claude Code process on the machine, so a value written there would be inherited by sessions that `ccl` did not launch, and would attribute them to the previously launched project.

`ccl` MUST place the attributes into the environment that is actually handed to the child process. On platforms where the launcher replaces the process with an explicit environment list, setting a process-wide environment variable alone is not sufficient.

The context MUST be delivered through Claude Code's **`--settings` parameter**, carrying an additional settings object whose `env` block sets `OTEL_RESOURCE_ATTRIBUTES`. This MUST be the primary delivery channel, because the settings file's `env` block takes precedence over the child process environment: a pre-existing `OTEL_RESOURCE_ATTRIBUTES` in `~/.claude/settings.json` would otherwise displace the injected context entirely. The `--settings` argument MUST take precedence over both the settings file and the process environment, so the injected context is delivered regardless of what the settings file contains.

The injection MUST NOT displace the user's own resource attributes: the value `ccl` delivers MUST contain the user's pre-existing attribute value in addition to `ccl`'s own keys.

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

#### Scenario: the context survives a settings file that defines the same key
- **WHEN** `~/.claude/settings.json` defines `OTEL_RESOURCE_ATTRIBUTES` with the value `team=platform`, and the user runs `ccl`
- **THEN** the exported payload's resource attributes contain `ccl.provider` and `ccl.cwd` for that session, in addition to `team` = `platform`

#### Scenario: the settings file's other settings are left alone
- **WHEN** `ccl` launches Claude Code
- **THEN** the settings `ccl` delivers contain only an `env` block, and the settings file's own non-`env` settings (such as permissions) remain in effect

### Requirement: attribute values are percent-encoded
Every value `ccl` writes into `OTEL_RESOURCE_ATTRIBUTES` MUST be percent-encoded so that it contains only unreserved characters (letters, digits, `-`, `.`, `_`, `~`).

This encoding is mandatory, not cosmetic: Claude Code splits the attribute string on `,` and `=`, and rejects a value that contains a space, a control character or a non-ASCII byte. A rejected value causes the **entire** attribute map to be discarded — including attributes whose values were valid — so an unencoded path or provider name silently removes the whole context.

An attribute whose encoded value would exceed 255 characters MUST be omitted entirely rather than emitted in truncated or invalid form, for the same reason.

Percent-encoding is reversible: the receiver MUST decode the value, so the stored value is the original path, host or provider name.

The encoding applies to the attributes `ccl` appends. A pre-existing attribute value that the user wrote MUST be carried through unchanged — it is the user's own text, and re-encoding it would change what the user's collector receives.

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
If the settings file's `env` block already contains an `OTEL_RESOURCE_ATTRIBUTES` value, `ccl` MUST preserve it and append its own attributes to it. Its own keys take precedence over a pre-existing value of the same key.

`ccl` MUST NOT warn about, degrade on, or refuse to deliver the context because the settings file defines `OTEL_RESOURCE_ATTRIBUTES`: the `--settings` channel takes precedence over the settings file, so the context is delivered regardless.

If the child process environment already contains an `OTEL_RESOURCE_ATTRIBUTES` value, `ccl` MUST preserve it and append its own attributes to it as well, with its own keys taking precedence.

#### Scenario: an existing attribute value is preserved
- **WHEN** `~/.claude/settings.json` defines `OTEL_RESOURCE_ATTRIBUTES=team=platform`
- **THEN** the exported payload's resource attributes contain `team` = `platform` in addition to `ccl.provider` and `ccl.cwd`

#### Scenario: ccl's own key wins over a pre-existing one
- **WHEN** `~/.claude/settings.json` defines `OTEL_RESOURCE_ATTRIBUTES=ccl.provider=stale`
- **THEN** the exported payload's resource attributes contain `ccl.provider` = the actually selected provider

#### Scenario: a pre-existing attribute value does not cause a degradation
- **WHEN** `~/.claude/settings.json` defines `OTEL_RESOURCE_ATTRIBUTES`
- **THEN** `ccl` delivers the launch context anyway, and prints no warning about the settings file

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

## ADDED Requirements

### Requirement: the daemon strips ccl's attributes before forwarding
When the daemon forwards an export to a remote collector, it MUST remove the attributes `ccl` added (`ccl.provider`, `ccl.cwd`, `ccl.provider_endpoint`) from the forwarded payload.

The remote collector MUST therefore receive the resource attributes the user would have received without `ccl` installed: the user's own attributes plus the attributes Claude Code sets itself. `ccl`'s launch context is local bookkeeping and MUST NOT leak to a collector the user did not intend to receive it.

This MUST NOT affect the copy the daemon stores locally: the local record MUST keep `ccl.*`, because the local query layer falls back to the `ccl.provider` attribute when a session row carries no provider of its own.

#### Scenario: forwarded exports carry no ccl attributes
- **WHEN** the daemon receives an export carrying `ccl.provider` = `minimax` and `ccl.cwd` = `/home/u/proj`, and forwards it to a remote collector
- **THEN** the forwarded payload's resource attributes contain neither `ccl.provider` nor `ccl.cwd` nor `ccl.provider_endpoint`, while the user's own attributes are still present

#### Scenario: the local record keeps ccl attributes
- **WHEN** the daemon stores an export carrying `ccl.provider` = `minimax`
- **THEN** the stored metric labels still contain `ccl.provider` = `minimax`

#### Scenario: forwarding without a remote collector is unaffected
- **WHEN** no remote collector is configured
- **THEN** the daemon forwards nothing and stores the export unchanged
