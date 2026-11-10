## MODIFIED Requirements

### Requirement: the daemon strips ccl's attributes before forwarding
When the daemon forwards an export to a remote collector, it MUST remove the attributes `ccl` added (`ccl.provider`, `ccl.cwd`, `ccl.provider_endpoint`) from the forwarded payload.

The remote collector MUST therefore receive the resource attributes the user would have received without `ccl` installed: the user's own attributes plus the attributes Claude Code sets itself. `ccl`'s launch context is local bookkeeping and MUST NOT leak to a collector the user did not intend to receive it.

This MUST NOT affect the copy the daemon stores locally: the local session record MUST keep the `ccl.*` context — `ccl.provider` and `ccl.cwd` reach the session row's own `provider` and `cwd` columns, and `ccl.provider` is additionally kept in the stored metric labels, because the local query layer falls back to the `ccl.provider` attribute when a session row carries no provider of its own. The stored metric labels are trimmed to a fixed whitelist of keys before they are written, so the working directory itself is not repeated in every metric row.

#### Scenario: forwarded exports carry no ccl attributes
- **WHEN** the daemon receives an export carrying `ccl.provider` = `minimax` and `ccl.cwd` = `/home/u/proj`, and forwards it to a remote collector
- **THEN** the forwarded payload's resource attributes contain neither `ccl.provider` nor `ccl.cwd` nor `ccl.provider_endpoint`, while the user's own attributes are still present

#### Scenario: the local record keeps ccl attributes
- **WHEN** the daemon stores an export carrying `ccl.provider` = `minimax`
- **THEN** the stored metric labels still contain `ccl.provider` = `minimax`

#### Scenario: the local session record keeps the launch context
- **WHEN** the daemon stores an export carrying `ccl.provider` = `minimax` and `ccl.cwd` = `/home/u/proj`
- **THEN** the session row holds `provider` = `minimax` and `cwd` = `/home/u/proj`

#### Scenario: forwarding without a remote collector is unaffected
- **WHEN** no remote collector is configured
- **THEN** the daemon forwards nothing and stores the export unchanged
