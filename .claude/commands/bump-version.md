---
name: "bump-version"
description: 推进 git semver tag（v0.x.y）——OpenSpec 归档完成后跑
category: Workflow
tags: [versioning, release, semver]
---

Bump the project's git semver tag. **Claude (you) reads commits and infers the bump type** — no string-pattern matching in the script.

**Input**: `/bump-version [subcommand] [args]`:
- *(no subcommand)* — Claude infers bump type from commits since latest tag, presents reasoning, asks for confirmation, then runs `bin/bump-version.sh bump <type>`
- `current` — show latest tag
- `list` — list all tags
- `bump <major|minor|patch> [--yes]` — force a specific bump type (skip inference)
- `check` — verify working tree is clean
- `help` — show usage

**Steps**

1. **Parse the subcommand**

   First positional arg (if any) is the subcommand. Default = infer from current date (no subcommand).

2. **For `current` / `list` / `check` / `help`**: pass through to `bin/bump-version.sh` directly, no inference needed.

3. **For `bump <type> [--yes]`**: pass through to `bin/bump-version.sh bump <type> [--yes]` — the user explicitly chose the type, no inference.

4. **For the default (inference) flow**:

   a. **Get latest tag**:
      ```bash
      git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0"
      ```
      Empty result (no tags yet) → treat as `v0.0.0` and start from repo's first commit.

   b. **Read commits since latest tag**, including body so `BREAKING CHANGE:` footers are visible:
      ```bash
      git log <tag>..HEAD --pretty=format:'COMMIT %h%nSubject: %s%nBody:%n%b%n---END---'
      ```
      Cap at ~50 commits (`git log -50`) to avoid token bloat. If there are more, note that older commits are truncated.

   c. **Analyze each commit for semver impact**. Use semver 2.0.0 semantics — NOT string pattern matching:

      - **major** = breaking change. Signals:
        - `BREAKING CHANGE:` footer (per Conventional Commits spec, anywhere in body)
        - `feat!:` / `fix!:` / `refactor!:` etc. in subject (the `!` marks breaking)
        - Subject or body describes removing APIs, changing CLI flags incompatibly, removing fields, etc.
        - Multiple subjects mentioning removal / rename / incompatible
      - **minor** = new feature (backward-compatible). Signals:
        - `feat:` / `feat(scope):` subject
        - Subject or body describes new subcommand / new option / new capability / new provider template / etc.
      - **patch** = bug fix / refactor / docs / chore (backward-compatible, no new user-facing capability). Signals:
        - `fix:`, `refactor:`, `perf:`, `chore:`, `docs:`, `test:`, `build:`, `ci:`, `style:`
        - Anything that improves correctness/quality without adding features

      **Take the highest level** (major > minor > patch). When commits are mixed, the highest-impact commit wins.

   e. **Present reasoning** to the user:
      ```
      自 v0.6.0 至今的 commits（N 个）：
        <short list>
      
      推断 bump 类型: <major|minor|patch>
      
      理由:
        - <commit>: <why this signal>
        - <commit>: <why this signal>
        ...
      
      综合: <one-sentence summary of why this level>
      ```

   f. **Confirm with user** using AskUserQuestion:
      - Options: `major` / `minor` / `patch` / `cancel`
      - The inferred option is recommended (first in list).
      - User can override if they disagree with the inference.

   g. **Run the bump**:
      ```bash
      ./bin/bump-version.sh bump <chosen-type>
      ```
      The script handles its own working-tree check, confirmation, `git tag`, `git push`. Do NOT pass `--yes` here — the user already confirmed via AskUserQuestion, but the script's own confirmation gives an extra chance to abort.

   h. **Report** the script's output verbatim (or any error).

**Important guardrails**

- **Never use `git tag` or `git push` directly** — always go through `bin/bump-version.sh bump` so the script's working-tree check + confirmation apply.
- **Don't infer without an OpenSpec archive commit** — bumping makes a "release", and release implies a completed change. If the user asks to bump mid-feature, ask whether they've completed + archived the corresponding OpenSpec change.
- **Inference is non-deterministic** — same commits may yield different answers at different times or on different models. That's why step (e) presents reasoning and step (f) confirms with user — never blind-bump.
- **Token cap at ~50 commits** — if `git log` returns more, only feed the most recent 50 to Claude. Mention this in the reasoning so user knows the inference may miss older commits.
- **`bin/bump-version.ps1` is for manual PowerShell only** — do not invoke it from this slash command (Claude's Bash tool doesn't run PowerShell).
- **Permission may need expansion** — `Bash(git log *)`, `Bash(git push *)`, `Bash(git tag *)` may need to be in `.claude/settings.local.json` allow array.

**Design rationale** (why inference lives in Claude, not the script)

The user explicitly rejected conventional-commit string-pattern matching — they want semantic analysis of commit history, not prefix parsing. This puts the inference in Claude (the slash command runner) instead of in POSIX shell (where text-pattern matching is awkward) or PowerShell (where it's verbose). Trade-off: inference is non-deterministic but reviewable; pattern matching was deterministic but missed `BREAKING CHANGE:` footers and required explicit prefixes.