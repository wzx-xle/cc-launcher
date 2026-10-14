## MODIFIED Requirements

### Requirement: Windows Launch spawns a child with inherited stdio
On Windows, `launcher.Launch` SHALL:
- Use `exec.Cmd` to start Claude Code as a child process
- Set `cmd.Env = opts.Env` if non-nil; otherwise leave `cmd.Env` unset so the child inherits the parent process environment
- Inherit `os.Stdin`, `os.Stdout`, `os.Stderr` so the user experience matches Unix
- For `.cmd` / `.bat` entry points (npm global install), invoke via `cmd.exe /c` (Windows CreateProcess cannot execute batch files directly)
- On child exit, propagate the exit code via `os.Exit(exitErr.ExitCode())` to mirror Unix `syscall.Exec` semantics

#### Scenario: successful Launch on Windows does not return
- **WHEN** the child process exits with code 0
- **THEN** `Launch` calls `os.Exit(0)` and does not return

#### Scenario: child exits non-zero on Windows
- **WHEN** the child process exits with a non-zero code
- **THEN** `Launch` calls `os.Exit(child.ExitCode())` and does not return

#### Scenario: spawn itself fails on Windows
- **WHEN** `cmd.Run()` returns an error that is not an `*exec.ExitError`
- **THEN** `Launch` returns the error to the caller (no `os.Exit`)

#### Scenario: claude entry is a batch file
- **WHEN** `resolveClaude` returns a path ending in `.cmd` or `.bat` (case-insensitive)
- **THEN** Windows `Launch` invokes `cmd.exe /c <path> <args...>`, not the path directly

#### Scenario: opts.Env is passed to cmd.Env on Windows
- **WHEN** `opts.Env = ["PATH=C:\\Windows", "ANTHROPIC_AUTH_TOKEN=sk-xxx"]` is passed to `Launch`
- **THEN** `cmd.Env = opts.Env` is set on `*exec.Cmd` before `cmd.Run()` is called

#### Scenario: opts.Env is nil leaves cmd.Env unset on Windows
- **WHEN** `opts.Env = nil` is passed to `Launch`
- **THEN** `cmd.Env` SHALL NOT be set (left as the zero value `nil`), causing the child to inherit the parent process environment