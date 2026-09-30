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
- `auto [--yes]` — auto-detect bump type from commit messages (conventional commits)
- `check` — verify working tree is clean
- `help` — show usage

If omitted, default to `auto --yes` (since "bump based on what I did" is the common request).

**Auto-detection rules** (from `bin/bump-version.sh do_auto`):

Conventional commit prefix → bump type (major > minor > patch, take highest):
- Any `type!:` (e.g. `feat!:`, `fix!:`) → **major**
- Contains `BREAKING CHANGE:` footer → **major**
- `feat:` or `feat(scope):` → **minor**
- Everything else (fix, refactor, chore, docs, style, test, build, ci, perf, revert) → **patch**

The script scans commits since the latest tag with `git log latest..HEAD --pretty=format:'%s'`, prints the list to the user, then invokes `do_bump` with the inferred type.

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
   - Conventional commit inference (in `auto` mode)
   - Interactive confirmation (skipped when `--yes`)
   - `git tag` + `git push origin` atomically

3. **Report result**

   Show the script's stdout/stderr directly to the user. If exit code is non-zero, propagate the error.

   For `auto`: the script prints each scanned commit + the inferred bump type before asking for confirmation. Make sure the user sees this — it's the safety net for misclassified commits.

**Important guardrails**

- **Never use `git tag` directly** — always go through `bin/bump-version.sh bump` so the script's safety checks (working tree, confirmation, conventional commit inference) apply.
- **Don't bump without an OpenSpec archive commit** — bumping makes a "release", and release implies a completed change. If the user asks to bump mid-feature, ask whether they've completed + archived the corresponding OpenSpec change.
- **`auto` only reads commit subjects** — the script does NOT scan commit bodies. If a commit has `BREAKING CHANGE:` in its footer but the subject is `chore(...)`, it will be classified as **patch**, not major. Always use `feat!:` in the subject for breaking changes (or explicitly invoke `bump major`).
- **`bin/bump-version.ps1` is for manual PowerShell only** — do not invoke it from this slash command (Claude's Bash tool doesn't run PowerShell).
- **Permitted permission in `.claude/settings.local.json` may need expansion** — if the script's `git push` is blocked, tell the user to add `Bash(git push *)` and `Bash(git tag *)` to `.claude/settings.local.json` allow array.