## MODIFIED Requirements

### Requirement: Launch accepts caller-provided environment
The launcher package SHALL accept a `Env []string` field on `Options` that callers use to pass the environment passed to the Claude Code subprocess. The launcher package SHALL NOT mutate, interpret, or otherwise inspect the contents of `Env`.

The provider's own configuration is NOT carried by this field. It is delivered through the `--settings` parameter, which takes precedence over both the settings file and the process environment. `Env` carries the parent process environment and nothing that is meant to override the user's configuration.

#### Scenario: Caller passes the parent environment
- **WHEN** the TUI runner constructs `launcher.Options{Env: os.Environ()}` with the launch context's resource attributes upserted into it
- **THEN** `Launch(opts)` SHALL pass the exact slice to the child process without inspection or modification

#### Scenario: Caller omits Env field
- **WHEN** `launcher.Options{Env: nil}` is passed (legacy or non-TUI callers)
- **THEN** `Launch` SHALL default to `os.Environ()` on Unix and inherit parent environment on Windows so that existing behavior is preserved
