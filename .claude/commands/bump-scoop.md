---
name: "bump-scoop"
description: 计算 cc-launcher Windows amd64 zip 的 SHA256 并更新 wzx-xle/scoop-bucket 仓的 bucket/cc-launcher.json
category: Workflow
tags: [release, scoop, sha256, manifest, github-api]
---

Bump the wzx-xle/scoop-bucket manifest for a new cc-launcher release.

**Input**: `/bump-scoop <version>` — version is the cc-launcher release tag (e.g. `v0.12.0`).

**Pair**: this command produces the manifest JSON via `./build.sh bump-scoop <version>`. Run after `bump-version` has pushed the v* tag and GitHub Actions release workflow has finished.

**Prerequisites**:
- `v*` tag pushed to `wzx-xle/cc-launcher`
- GitHub Actions release workflow finished (5 build jobs: 1 windows zip + 2 linux tar.gz + 2 darwin tar.gz). The windows-amd64 job's "append windows zip SHA256 to release body" step MUST have completed — it writes a `<!-- sha256:auto-hash:HEX -->` marker into the release body. **Without this marker the new flow cannot fetch the hash.**
- `wzx-xle/scoop-bucket` repo cloned to `/tmp/scoop-bucket` (first time only)

**Why hash comes from GitHub API (not local build)**

Go's build action hash is a content hash of all inputs (source + GOMODCACHE state + toolchain + env vars). Fresh CI release workflow's GOMODCACHE vs local incremental cache **always differ**, producing ~58K bytes of diff across ~22K regions in the resulting binary (Go runtime, pclntab function hash table, embedded build ID). The local zip SHA256 **will never byte-level match** the GitHub release asset SHA256, no matter what `-trimpath`/`touch -d @0`/`TZ=UTC zip -X` does.

Therefore the new flow fetches the release zip SHA256 from **GitHub release body** (which CI writes via `gh release edit`), avoiding both the 18 MB zip download AND the failed local-hash-matching-release assumption.

**Steps**

1. **Compute local zip + fetch release hash from GitHub API**:

   ```bash
   rm -rf dist && ./build.sh bump-scoop v0.X.Y
   ```

   The script:
   - cross-compiles `cc-launcher-windows-amd64.zip` locally (kept for human verification)
   - fetches the release body via `curl https://api.github.com/repos/wzx-xle/cc-launcher/releases/tags/v0.X.Y`
   - parses `<!-- sha256:auto-hash:HEX -->` from the body (3 retries × 5s backoff for race with release workflow's `gh release edit` step)
   - emits the JSON with the **release asset's** hash (NOT the local one)

   Output: full manifest JSON.

   **If the script fails** with "fetch_release_zip_hash_gh_api 失败" (rate limit, network, or release workflow hasn't appended the marker yet):
   - Wait 30s and run `bash build.sh bump-scoop v0.X.Y` again (marker may not yet be in body — wait longer)
   - **Manual fallback** (download 18 MB zip, costs bandwidth — operator decides):

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

   **Do NOT use the local zip's SHA256** — local build hash ≠ GitHub release asset hash (see top of doc). The `hash` field MUST come from step 1's fetched value.

3. **Commit and push to wzx-xle/scoop-bucket**:

   ```bash
   cd /tmp/scoop-bucket
   git config --global --unset-all url.https://github.com/.insteadof
   git add bucket/cc-launcher.json
   git -c user.name=wxyz -c user.email=wxyz@users.noreply.github.com commit \
       -m "chore(scoop): bump cc-launcher to v0.X.Y

   v0.X.Y release asset hash: <hash> (windows-amd64 zip, fetched from GitHub
   release body via bump-scoop-hash-trace flow)"
   git push origin main
   git config --global --add url.https://github.com/.insteadof "git@github.com:"
   git config --global --add url.https://github.com/.insteadof "ssh://git@github.com/"
   ```

   The `insteadOf` unset/add cycle is required because the global `url.https://github.com/.insteadof` rule otherwise rewrites `git@github.com:` to `https://github.com:`, which fails without a GitHub PAT. Always re-add both rules with `--add` (not set), to avoid dropping a rule.

   **Commit message is English** (NOT Chinese): the `wzx-xle/scoop-bucket` repo is cloned by Windows users via `scoop update`; git GUI / GitHub web on Windows mis-renders CJK characters without proper LANG/LC_ALL. cc-launcher's own commit message convention (Chinese) does NOT apply to the bucket repo — bucket commits stay ASCII to avoid encoding hazards for downstream consumers.

4. **Verify**:

   ```bash
   git ls-remote https://github.com/wzx-xle/scoop-bucket main
   ```

   Confirm the remote main has the new commit hash. Users can then `scoop update cc-launcher` to receive v0.X.Y.

**Common mistakes**

- **Including an `arm64` block** in the manifest — breaks `scoop install` because the release no longer ships a windows/arm64 zip. Removed in commit `299a2a4`.
- **Forgetting to restore `url.https://github.com/.insteadof`** after the push — leaves the local environment inconsistent. Always re-add both rules with `--add`.
- **Pushing bucket to a non-GitHub remote** — bucket repo lives on GitHub only (`wzx-xle/scoop-bucket`); `git.wxyz.ren` mirror is for the cc-launcher source only and does not host the bucket. Verify the bucket repo's remote URL before `git push`.
- **Using the local zip's SHA256 for the manifest hash** — local build's SHA256 **never matches** GitHub release asset's SHA256 (Go build action hash includes GOMODCACHE state; fresh CI vs incremental local cache always differ). Always use the value fetched from GitHub release body by `build.sh bump-scoop` (or the manual download fallback).
- **Running bump-scoop before the release workflow's "append hash to release body" step completes** — the marker won't be in the release body yet, fetch will retry 3×5s then fail. Wait 30s and re-run, or check the release workflow status before running.
- **Using the wrong username in the manifest URL** — use `wzx-xle` (GitHub) for this repo, not `wxyz` (Gitea). See memory `github-vs-gitea-usernames`.

**Related**

- `build.sh bump-scoop` subcommand — produces local zip + fetches release hash via GitHub API
- `.github/workflows/release.yml` — produces the release assets + writes the marker into release body
- memory `scoop-bucket-zip-hash-stability` — historical context (the "four conventions" no longer suffice for byte-level match)
- memory `github-push-via-ssh-with-insteadof` — the SSH push workflow
- memory `github-vs-gitea-usernames` — `wzx-xle` vs `wxyz` distinction
- `.claude/commands/bump-version.md` — companion slash command
- `openspec/changes/archive/2026-10-14-bump-scoop-hash-trace/` — full change rationale