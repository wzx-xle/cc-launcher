# cross-platform-launcher Specification

## Purpose
TBD - created by archiving change cross-platform-launcher-and-mode-contract. Update Purpose after archive.
## Requirements
### Requirement: Launch abstraction supports cross-platform composition
The launcher package SHALL split process-launch concerns into:
- A platform-agnostic `resolveClaude(opts) (path, args, err)` that finds the `claude` executable and builds argv (excluding argv[0]).
- A platform-specific `Launch(opts) error` provided by `launcher_unix.go` (`//go:build !windows`) and `launcher_windows.go` (`//go:build windows`).

#### Scenario: launch package compiles on both Unix and Windows
- **WHEN** `GOOS=linux go build ./...` and `GOOS=windows go build ./...` both run
- **THEN** both succeed; the Unix build uses `launcher.go` + `launcher_unix.go`; the Windows build uses `launcher.go` + `launcher_windows.go`

#### Scenario: resolveClaude produces correct argv
- **WHEN** `resolveClaude` is called with `opts = {PermissionMode: "plan", Resume: true}`
- **THEN** it returns `args = ["--permission-mode", "plan", "--resume"]` and the resolved `claude` path (or an error if `claude` is not in PATH)

### Requirement: Unix Launch replaces the current process
On Unix, `launcher.Launch` SHALL replace the current process image via `syscall.Exec(claudePath, argv, env)` so the user sees no extra shell between cc-launcher and Claude Code.

#### Scenario: successful Launch does not return
- **WHEN** `syscall.Exec` succeeds
- **THEN** the function does not return; the calling process image is replaced by Claude Code

#### Scenario: failed Launch returns the error
- **WHEN** `syscall.Exec` fails (e.g., permission denied)
- **THEN** the function returns the underlying error to the caller

### Requirement: Windows Launch spawns a child with inherited stdio
On Windows, `launcher.Launch` SHALL:
- Use `exec.Cmd` to start Claude Code as a child process
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

### Requirement: Build script supports cross-compilation and contract runs
`build.sh` SHALL provide:
- `contract`: runs `go test -tags contract ./... -run ^TestContract_`
- `windows`: cross-compiles Windows amd64 + arm64 binaries to `dist/`
- `release`: cross-compiles all supported OS/arch pairs to `dist/`
- `clean`: removes `dist/` along with the local binary

#### Scenario: ./build.sh contract runs the contract tests
- **WHEN** `./build.sh contract` is invoked
- **THEN** the script executes `go test -tags contract ./... -v -run '^TestContract_'`

#### Scenario: ./build.sh windows produces dist/*.exe
- **WHEN** `./build.sh windows` is invoked
- **THEN** `dist/cc-launcher-windows-amd64.exe` and `dist/cc-launcher-windows-arm64.exe` exist after the command completes

#### Scenario: ./build.sh clean removes dist/
- **WHEN** `./build.sh clean` is invoked with `dist/` present
- **THEN** the `dist/` directory no longer exists

