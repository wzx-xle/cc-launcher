---
name: "bump-version"
description: 推进 git semver tag（v0.x.y）——OpenSpec 归档完成后跑
category: Workflow
tags: [versioning, release, semver]
---

Bump the project's git semver tag using `bin/bump-version.sh`.

**Input**: `/bump-version <subcommand> [args]` where subcommand is one of:
- `current` — show latest tag (default `v0.0.0` if none)
- `list` — list all existing tags (sorted)
- `bump <major|minor|patch> [--yes]` — bump and push to origin
- `check` — verify working tree is clean
- `help` — show usage

If omitted, default to `bump minor --yes`.

**Steps**

1. **Detect script**

   Always invoke `bin/bump-version.sh` from the repository root. This is POSIX sh-compatible — runs identically on Linux, macOS, and Windows (Git Bash). The Bash tool Claude uses already invokes bash, so no OS detection needed.

   The PowerShell variant `bin/bump-version.ps1` exists for users running manually in PowerShell native — it is **not** called from this slash command.

2. **Run the script**

   ```bash
   ./bin/bump-version.sh <subcommand> [args]
   ```

   Pass through args verbatim. The script handles its own:
   - Working tree clean check (rejects bump on dirty tree)
   - Tag existence check (assumes `v0.0.0` if no tags)
   - Interactive confirmation (skipped when `--yes`)
   - `git tag` + `git push origin` atomically

3. **Report result**

   Show the script's stdout/stderr directly to the user. If exit code is non-zero, propagate the error.

**Important guardrails**

- **Never use `git tag` directly** — always go through `bin/bump-version.sh bump` so the script's safety checks (working tree, confirmation) apply.
- **Don't bump without an OpenSpec archive commit** — bumping makes a "release", and release implies a completed change. If the user asks to bump mid-feature, ask whether they've completed + archived the corresponding OpenSpec change.
- **`bin/bump-version.ps1` is for manual PowerShell only** — do not invoke it from this slash command (Claude's Bash tool doesn't run PowerShell).
- **Permitted permission in `.claude/settings.local.json` may need expansion** — if the script's `git push` is blocked, tell the user to add `Bash(git push *)` and `Bash(git tag *)` to `.claude/settings.local.json` allow array.