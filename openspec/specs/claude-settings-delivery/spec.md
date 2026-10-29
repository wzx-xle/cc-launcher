# claude-settings-delivery Specification

## Purpose
TBD - created by archiving change provider-env-via-settings. Update Purpose after archive.
## Requirements
### Requirement: ccl delivers the settings to Claude Code through a reusable cache file
On every `ccl` invocation, ccl MUST deliver the settings object it composes to Claude Code through the `--settings` parameter, referring to a file rather than to an inline JSON string, on every platform.

The file MUST live in `~/.cc-launcher/cache/`, and its name MUST be derived from the SHA-256 hash of the settings content: `settings-<first 16 hex digits of the hash>.json`.

The content-addressed name MUST serve two purposes:
- the same settings content always maps to the same file, so repeated launches reuse the file instead of accumulating one file per launch;
- different settings content maps to a different file, so two concurrent launches never read each other's settings.

The cache directory MUST be created with mode `0700` and the file with mode `0600`, because the content may contain the provider's credentials.

ccl MUST NOT delete the file after the launch. The file is a reusable cache entry, not a per-launch temporary file.

Writing MUST be idempotent: when the target file already exists and its content is identical to the content being delivered, ccl MUST reuse it and MUST NOT rewrite it.

When the target file does not exist or its content differs, ccl MUST write it atomically — into a temporary file in the same directory, followed by a rename — so that a concurrent reader never observes a partially written file.

The settings content MUST NOT be passed as an inline command-line argument, because on Unix the command line is world-readable (`/proc/<pid>/cmdline` is mode `0444`) and the content may contain credentials.

#### Scenario: the file name follows the content
- **WHEN** ccl delivers a settings object whose SHA-256 hash is `0123456789abcdef…`
- **THEN** the file is `~/.cc-launcher/cache/settings-0123456789abcdef.json`

#### Scenario: the same content reuses the same file
- **WHEN** ccl launches twice with identical settings content
- **THEN** both launches refer to the same file, and the second launch does not rewrite it

#### Scenario: concurrent launches do not read each other's settings
- **WHEN** two `ccl` launches run at the same time with different settings content
- **THEN** each launch refers to its own file, and neither observes the other's content

#### Scenario: the file is private
- **WHEN** ccl writes the settings file
- **THEN** the cache directory has mode `0700` and the file has mode `0600`

#### Scenario: no credentials on the command line
- **WHEN** ccl launches Claude Code
- **THEN** the `--settings` argument is a file path, and the settings content does not appear in the process's command line

#### Scenario: the file survives the launch
- **WHEN** Claude Code exits
- **THEN** the settings file still exists in the cache directory

### Requirement: ccl removes stale cache files on startup
On startup, ccl MUST remove the cache files it owns whose modification time is older than one year.

Only the files whose names begin with `settings-` and end with `.json` are eligible for removal; ccl MUST NOT remove other files from the cache directory.

A failure to remove a stale file MUST NOT block the launch: ccl MUST print a warning and continue.

#### Scenario: an old file is removed
- **WHEN** the cache directory contains `settings-aaaaaaaaaaaaaaaa.json` whose modification time is more than one year old
- **THEN** ccl removes that file

#### Scenario: a recent file is kept
- **WHEN** the cache directory contains `settings-bbbbbbbbbbbbbbbb.json` whose modification time is within one year
- **THEN** ccl keeps that file

#### Scenario: unrelated files are not touched
- **WHEN** the cache directory contains a file that is neither `settings-*.json` nor older than a year
- **THEN** ccl leaves it in place

### Requirement: the delivered settings contain only an env block
The settings object ccl delivers MUST contain exactly one top-level field, `env`. The settings file's own non-`env` settings (permissions, plugins, and the like) MUST remain in effect, because ccl's object is merged into the settings file rather than replacing it.

#### Scenario: only an env block is delivered
- **WHEN** ccl launches Claude Code
- **THEN** the delivered settings object contains only an `env` field

#### Scenario: the settings file's other settings survive
- **WHEN** `~/.claude/settings.json` defines `permissions` and ccl delivers its own settings object
- **THEN** the permissions remain in effect for the launched session

