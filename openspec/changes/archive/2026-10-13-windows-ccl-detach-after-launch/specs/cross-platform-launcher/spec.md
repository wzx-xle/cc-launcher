## MODIFIED Requirements

### Requirement: Windows Launch spawns a child with inherited stdio
On Windows, `launcher.Launch` SHALL:
- Use `exec.Cmd` to start Claude Code as a child process
- Set `cmd.Env = opts.Env` if non-nil; otherwise leave `cmd.Env` unset so the child inherits the parent process environment
- Inherit `os.Stdin`, `os.Stdout`, `os.Stderr` so the user experience matches Unix
- For `.cmd` / `.bat` entry points (npm global install), invoke via `cmd.exe /c` (Windows CreateProcess cannot execute batch files directly)
- After `cmd.Start()` succeeds, call `os.Exit(0)` immediately to terminate the ccl process. The ccl process SHALL NOT wait for the child to exit. This mirrors the Unix `syscall.Exec` user-perceived behavior (ccl process disappears, claude takes over stdio).

The ccl process's exit code SHALL always be 0 on successful child spawn. Claude Code's own exit code is NOT propagated to the ccl caller; it is reported to the ccl caller's parent (PowerShell) via the cmd.exe → node.exe → claude.exe process chain.

#### Scenario: launch package compiles on both Unix and Windows
- **WHEN** `GOOS=linux go build ./...` and `GOOS=windows go build ./...` both run
- **THEN** both succeed; the Unix build uses `launcher.go` + `launcher_unix.go`; the Windows build uses `launcher.go` + `launcher_windows.go`

#### Scenario: resolveClaude produces correct argv
- **WHEN** `resolveClaude` is called with `opts = {PermissionMode: "plan", Resume: true}`
- **THEN** it returns `args = ["--permission-mode", "plan", "--resume"]` and the resolved `claude` path (or an error if `claude` is not in PATH)

#### Scenario: caller passes provider env
- **WHEN** `opts = {Env: ["ANTHROPIC_AUTH_TOKEN=sk-xxx", ...]}` and `Launch` is called
- **THEN** the Claude Code subprocess SHALL inherit `Env` as its environment (no interpretation by the launcher package itself)

#### Scenario: caller omits Env
- **WHEN** `opts = {Env: nil}` and `Launch` is called
- **THEN** on Unix the subprocess inherits `os.Environ()`; on Windows the subprocess inherits the parent process environment (legacy behavior preserved)

#### Scenario: successful Launch on Windows exits ccl immediately
- **WHEN** `cmd.Start()` succeeds (child process is spawned)
- **THEN** `Launch` calls `os.Exit(0)` without waiting for the child to exit. The ccl process SHALL NOT be visible in task manager after `ccl` returns control to its caller; the child process (and its cmd.exe / node.exe ancestors) continue running independently.

#### Scenario: spawn itself fails on Windows
- **WHEN** `cmd.Start()` returns an error (e.g., claude binary not found, permission denied)
- **THEN** `Launch` returns the error to the caller (no `os.Exit`). The ccl process exits with a non-zero code via the normal `main` return path.

#### Scenario: claude entry is a batch file
- **WHEN** `resolveClaude` returns a path ending in `.cmd` or `.bat` (case-insensitive)
- **THEN** Windows `Launch` invokes `cmd.exe /c <path> <args...>`, not the path directly

#### Scenario: opts.Env is passed to cmd.Env on Windows
- **WHEN** `opts.Env = ["PATH=C:\\Windows", "ANTHROPIC_AUTH_TOKEN=sk-xxx"]` is passed to `Launch`
- **THEN** `cmd.Env = opts.Env` is set on `*exec.Cmd` before `cmd.Start()` is called

#### Scenario: opts.Env is nil leaves cmd.Env unset on Windows
- **WHEN** `opts.Env = nil` is passed to `Launch`
- **THEN** `cmd.Env` is left as the zero value (nil) so the child inherits the parent process environment
