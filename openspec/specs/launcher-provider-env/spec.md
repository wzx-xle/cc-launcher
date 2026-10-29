# launcher-provider-env Specification

## Purpose
TBD - created by archiving change provider-setenv-cwd-binding. Update Purpose after archive.
## Requirements
### Requirement: Launch accepts caller-provided environment
The launcher package SHALL accept a `Env []string` field on `Options` that callers use to pass the environment passed to the Claude Code subprocess. The launcher package SHALL NOT mutate, interpret, or otherwise inspect the contents of `Env`.

The provider's own configuration is NOT carried by this field. It is delivered through the `--settings` parameter, which takes precedence over both the settings file and the process environment. `Env` carries the parent process environment and nothing that is meant to override the user's configuration.

#### Scenario: Caller passes the parent environment
- **WHEN** the TUI runner constructs `launcher.Options{Env: os.Environ()}` with the launch context's resource attributes upserted into it
- **THEN** `Launch(opts)` SHALL pass the exact slice to the child process without inspection or modification

#### Scenario: Caller omits Env field
- **WHEN** `launcher.Options{Env: nil}` is passed (legacy or non-TUI callers)
- **THEN** `Launch` SHALL default to `os.Environ()` on Unix and inherit parent environment on Windows so that existing behavior is preserved

### Requirement: Unix Launch forwards Env to syscall.Exec
On Unix (`//go:build !windows`), `Launch` SHALL pass `opts.Env` as the third argument to `syscall.Exec(claudePath, argv, opts.Env)`.

#### Scenario: opts.Env propagated verbatim
- **WHEN** `opts.Env` contains `["PATH=/usr/bin", "ANTHROPIC_AUTH_TOKEN=sk-xxx", ...]`
- **THEN** `syscall.Exec` receives this slice as `env` parameter and the Claude Code subprocess inherits these variables

#### Scenario: Process is replaced
- **WHEN** `syscall.Exec` succeeds
- **THEN** the ccl process image is replaced by Claude Code with `opts.Env` as the new environment (no extra shell between ccl and Claude Code)

### Requirement: Windows Launch sets cmd.Env from Env
On Windows (`//go:build windows`), `Launch` SHALL set `cmd.Env = opts.Env` so the child process inherits the caller-provided environment.

#### Scenario: opts.Env assigned to cmd.Env
- **WHEN** `opts.Env` contains `["PATH=C:\\Windows", "ANTHROPIC_AUTH_TOKEN=sk-xxx", ...]`
- **THEN** `cmd.Env` is set to this slice and the child process Claude Code runs with these variables

#### Scenario: stdio is inherited on Windows
- **WHEN** `Launch` sets up the child process
- **THEN** `cmd.Stdin` / `cmd.Stdout` / `cmd.Stderr` SHALL be `os.Stdin` / `os.Stdout` / `os.Stderr` regardless of `opts.Env` (env and stdio are orthogonal)

#### Scenario: Batch-file entry uses cmd.exe /c
- **WHEN** `resolveClaude` returns a path ending in `.cmd` or `.bat`
- **THEN** `cmd.Env = opts.Env` is set before invoking via `cmd.exe /c <path> <args...>` so the batch file runs in the caller-provided environment

### Requirement: Caller is responsible for parent env inclusion
The launcher package SHALL NOT prepend `os.Environ()` to `opts.Env` automatically. Callers that want the child to inherit parent environment MUST include parent env entries in `opts.Env` themselves.

#### Scenario: Caller without os.Environ() in Env
- **WHEN** the caller passes `opts.Env = []string{"ANTHROPIC_AUTH_TOKEN=sk-xxx"}` (no parent env entries)
- **THEN** the child process SHALL see only `ANTHROPIC_AUTH_TOKEN=sk-xxx` and nothing else — no inherited `PATH`, `HOME`, etc.

#### Scenario: Caller with os.Environ() + provider keys
- **WHEN** the caller passes `opts.Env = append(os.Environ(), "ANTHROPIC_AUTH_TOKEN=sk-xxx", ...)`
- **THEN** the child process SHALL see all parent variables and the ANTHROPIC_* overrides (parent variables other than the overridden keys preserved)

### Requirement: upsertEnv helper is exported from a shared package
The launcher package SHALL NOT define env-construction helpers. A shared helper `upsertEnv(env []string, key, value string) []string` SHALL be exported from a separate package (`internal/envutil` or `internal/cli/envutil`) for use by callers.

#### Scenario: existing key is replaced
- **WHEN** `env = ["PATH=/usr/bin", "FOO=1"]` and `upsertEnv(env, "PATH", "/bin")` is called
- **THEN** the result is `["PATH=/bin", "FOO=1"]` (length unchanged, value updated)

#### Scenario: missing key is appended
- **WHEN** `env = ["FOO=1"]` and `upsertEnv(env, "BAR", "2")` is called
- **THEN** the result is `["FOO=1", "BAR=2"]` (new key appended at the end)

