---
name: "bump-scoop"
description: 计算 cc-launcher Windows amd64 zip 的 SHA256 并更新 wzx-xle/scoop-bucket 仓的 bucket/cc-launcher.json
category: Workflow
tags: [release, scoop, sha256, manifest]
---

Bump the wzx-xle/scoop-bucket manifest for a new cc-launcher release.

**Input**: `/bump-scoop <version>` — version is the cc-launcher release tag (e.g. `v0.11.2`).

**Pair**: this command produces the SHA256 via `./build.sh bump-scoop <version>` (renamed from `scoop-bump` in v0.11.2). Run after `bump-version` has pushed the v* tag and GitHub Actions release workflow has finished.

**Prerequisites**:
- `v*` tag pushed to `wzx-xle/cc-launcher`
- GitHub Actions release workflow finished (5 build jobs: 1 windows zip + 2 linux tar.gz + 2 darwin tar.gz)
- `wzx-xle/scoop-bucket` repo cloned to `/tmp/scoop-bucket` (first time only)

**Steps**

1. **Compute SHA256 + manifest JSON** (local, no download):

   ```bash
   rm -rf dist && ./build.sh bump-scoop v0.X.Y
   ```

   Output is the full manifest JSON. Local hash byte-level matches release assets when the "four conventions" are in place (`-trimpath`, no `BuildTime` ldflag, `touch -d @0`, `TZ=UTC zip -X`).

   **Fallback** (if hash mismatches the actual release asset): download the zip and hash it directly:

   ```bash
   curl -sSL https://github.com/wzx-xle/cc-launcher/releases/download/v0.X.Y/cc-launcher-windows-amd64.zip \
     -o /tmp/x.zip && sha256sum /tmp/x.zip
   ```

2. **Edit `/tmp/scoop-bucket/bucket/cc-launcher.json`** — current expected format (v0.11.2+, no windows/arm64):

   ```json
   {
       "version": "0.X.Y",
       "description": "Terminal launcher for Claude Code with provider switching and permission mode selection",
       "homepage": "https://github.com/wzx-xle/cc-launcher",
       "license": "MIT",
       "architecture": {
           "64bit": {
               "url": "https://github.com/wzx-xle/cc-launcher/releases/download/v0.X.Y/cc-launcher-windows-amd64.zip",
               "hash": "<sha256 from step 1>",
               "bin": ["ccl.cmd"]
           }
       }
   }
   ```

   **Do NOT include an `arm64` block** — the release workflow matrix dropped windows/arm64 as of v0.11.2; including it would point to a non-existent asset and break `scoop install`.

3. **Commit and push to wzx-xle/scoop-bucket**:

   ```bash
   cd /tmp/scoop-bucket
   git config --global --unset-all url.https://github.com/.insteadof
   git add bucket/cc-launcher.json
   git -c user.name=wxyz -c user.email=wxyz@users.noreply.github.com commit \
       -m "chore(scoop): bump cc-launcher 到 v0.X.Y

   v0.X.Y release 资产 hash: <hash> (windows-amd64 zip, '四约定' 下
   byte-level 稳定) ..."
   git push origin main
   git config --global --add url.https://github.com/.insteadof "git@github.com:"
   git config --global --add url.https://github.com/.insteadof "ssh://git@github.com/"
   ```

   The `insteadOf` unset/add cycle is required because the global `url.https://github.com/.insteadof` rule otherwise rewrites `git@github.com:` to `https://github.com:`, which fails without a GitHub PAT. Always re-add both rules with `--add` (not set), to avoid dropping a rule.

4. **Verify**:

   ```bash
   git ls-remote https://github.com/wzx-xle/scoop-bucket main
   ```

   Confirm the remote main has the new commit hash. Users can then `scoop update cc-launcher` to receive v0.X.Y.

**Common mistakes**

- **Including an `arm64` block** in the manifest — breaks `scoop install` because the release no longer ships a windows/arm64 zip. Removed in commit `299a2a4`.
- **Forgetting to restore `url.https://github.com/.insteadof`** after the push — leaves the local environment inconsistent. Always re-add both rules with `--add`.
- **Pushing bucket to a non-GitHub remote** — bucket repo lives on GitHub only (`wzx-xle/scoop-bucket`); `git.wxyz.ren` mirror is for the cc-launcher source only and does not host the bucket. Verify the bucket repo's remote URL before `git push`.
- **Trusting the local hash without verifying the release workflow succeeded** — if the workflow failed or the build matrix differed, the local binary won't match the (missing) release asset. Always confirm the release page has `cc-launcher-windows-amd64.zip` before relying on the local hash.
- **Using the wrong username in the manifest URL** — use `wzx-xle` (GitHub) for this repo, not `wxyz` (Gitea). See memory `github-vs-gitea-usernames`.

**Related**

- `build.sh bump-scoop` subcommand — source of the local hash
- `.github/workflows/release.yml` — produces the release assets
- memory `scoop-bucket-zip-hash-stability` — the four conventions that make local hash match release hash
- memory `github-push-via-ssh-with-insteadof` — the SSH push workflow
- memory `github-vs-gitea-usernames` — `wzx-xle` vs `wxyz` distinction
- `.claude/commands/bump-version.md` — companion slash command
