---
name: cc-launcher-scoop-bump
description: Use when publishing a new cc-launcher release tag (v*) on wzx-xle/cc-launcher and needing to update the wzx-xle/scoop-bucket manifest with the new Windows amd64 zip SHA256. Project-specific to cc-launcher. Triggers after `bump-version` skill creates a v* tag and GitHub Actions release workflow produces the assets.
---

# cc-launcher Scoop Bucket Bump

Compute the Windows amd64 zip SHA256 for a cc-launcher release and update `wzx-xle/scoop-bucket/bucket/cc-launcher.json` so `scoop update cc-launcher` users receive the new version.

**Project-specific to cc-launcher.** Do not invoke for other projects.

## When to Use

Use when:
- A new `v*` tag has been pushed to `wzx-xle/cc-launcher` and `openspec archive` + `bump-version` skill have completed
- GitHub Actions release workflow for the new tag has finished (5 build jobs complete: 1 windows zip + 2 linux tar.gz + 2 darwin tar.gz)
- The user asks to "update bucket", "bump scoop", or "calc hash for v0.X.Y"

Do NOT use when:
- The release workflow has not finished yet (hash from local build is **not** the same as the release asset hash in practice — use this skill's download+hash flow instead, see "Hash source" below)
- Editing a non-Windows-amd64-only manifest (this skill assumes the project has dropped windows/arm64 as of v0.11.2)

## Hash Source: Local vs Download

Two valid approaches; pick the one that matches the current state:

| Approach | When | Command |
|----------|------|---------|
| **Local build** (preferred, no download) | Source is byte-level reproducible via the "four conventions" (see `openspec/changes/archive/2026-10-13-.../specs/` + `memory/scoop-bucket-zip-hash-stability`): `-trimpath` flag, no `BuildTime` ldflag, `touch -d @0` source mtime, `TZ=UTC zip -X` | `./build.sh scoop-bump v0.X.Y` |
| **Download from release** (fallback) | Conventions not yet applied, or hash mismatch with local build | `curl -sSL https://github.com/wzx-xle/cc-launcher/releases/download/v0.X.Y/cc-launcher-windows-amd64.zip -o /tmp/x.zip && sha256sum /tmp/x.zip` |

The local approach is preferred when conventions are in place — it avoids the download dependency and is fully reproducible.

## Workflow

### 1. Compute hash and produce manifest JSON

```bash
# Local (preferred, v0.11.2+)
rm -rf dist && ./build.sh scoop-bump v0.X.Y
```

Output is the full manifest JSON to stdout. Capture it (or copy-paste the hash field).

### 2. Clone or update the bucket clone

```bash
# First time only:
git clone git@github.com:wzx-xle/scoop-bucket.git /tmp/scoop-bucket
cd /tmp/scoop-bucket
git remote set-url origin git@github.com:wzx-xle/scoop-bucket.git  # ensure SSH

# Subsequent runs:
cd /tmp/scoop-bucket
git pull origin main
```

The SSH push path requires the local `url.https://github.com/.insteadof` workaround to be temporarily unset (see memory `github-push-via-ssh-with-insteadof`). Use the standard sequence:

```bash
git config --global --unset-all url.https://github.com/.insteadof
# ... edit, commit, push ...
git config --global --add url.https://github.com/.insteadof "git@github.com:"
git config --global --add url.https://github.com/.insteadof "ssh://git@github.com/"
```

### 3. Write `bucket/cc-launcher.json`

Current expected format (v0.11.2+ — no windows/arm64):

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

**Do NOT include an `arm64` block** — `wzx-xle/cc-launcher` release workflow matrix dropped windows/arm64 as of v0.11.2. Including it makes the manifest reference a release asset that does not exist, breaking `scoop install`.

### 4. Commit and push to GitHub

```bash
cd /tmp/scoop-bucket
git add bucket/cc-launcher.json
git -c user.name=wxyz -c user.email=wxyz@users.noreply.github.com commit \
    -m "chore(scoop): bump cc-launcher 到 v0.X.Y

v0.X.Y release 资产 hash: <hash> (windows-amd64 zip, '四约定' 下
byte-level 稳定) ..."
# With insteadOf unset (see step 2):
git push origin main
```

Always include the hash in the commit body — it lets future readers verify the manifest pointed to a real asset.

## Verification

After the push:

```bash
# Confirm GitHub main has the new commit
git ls-remote https://github.com/wzx-xle/scoop-bucket main
# Should show commit hash matching your local HEAD
```

Then `scoop update cc-launcher` will fetch and validate against the new hash. A failure here means the local hash in step 1 did not match the actual release asset — re-run the **Download from release** approach to get the real hash.

## Common Mistakes

- **Including `arm64` block** — causes `scoop install` to fail because the referenced `cc-launcher-windows-arm64.zip` does not exist in the release. Verified by the v0.11.2 release dropping arm64 (commit `299a2a4`).
- **Forgetting to restore `url.https://github.com/.insteadof` after push** — leaves the local environment in an inconsistent state for subsequent git operations. Always re-add both rules with `--add`, not set (see memory `github-push-via-ssh-with-insteadof`).
- **Pushing only to `origin` (git.wxyz.ren mirror) and not GitHub** — the bucket repo lives on GitHub only. Verify with `git ls-remote https://github.com/wzx-xle/scoop-bucket main` after pushing.
- **Using local hash without verifying release workflow succeeded** — if the workflow failed, the local binary differs from the (missing) release asset. Always confirm the release page has `cc-launcher-windows-amd64.zip` before relying on the local hash.

## Related

- `build.sh scoop-bump` subcommand — the source of the local hash
- `.github/workflows/release.yml` — what produces the release assets this skill tracks
- memory `scoop-bucket-zip-hash-stability` — the "four conventions" that make local hash byte-level match release hash
- memory `github-push-via-ssh-with-insteadof` — the SSH push workflow for the bucket repo
- memory `github-vs-gitea-usernames` — confirms `wzx-xle` is the GitHub org/user, not `wxyz`
