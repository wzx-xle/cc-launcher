## ADDED Requirements

### Requirement: build.sh bump-scoop fetches release zip SHA256 from GitHub release body, not local build

When `build.sh bump-scoop vX.Y` is invoked, it SHALL:

- Cross-compile `cc-launcher-windows-amd64.exe` via existing `windows` + `windows_build` flow, then run `pack_windows_zip_for_scoop` to produce the local `cc-launcher-windows-amd64-scoop.zip` (kept for sourceable verification, not for distribution)
- Fetch `https://api.github.com/repos/wzx-xle/cc-launcher/releases/tags/vX.Y` via `curl` and parse `body` field
- Extract the substring matching `<!-- sha256:auto-hash:[0-9a-f]{64} -->` from the release body and use that value as the `hash` field in `bucket/cc-launcher.json`
- If the GitHub API call fails (rate limit, network, missing marker): respond with explicit failure. Manual fallback to `curl -sSL https://github.com/wzx-xle/cc-launcher/releases/download/vX.Y/cc-launcher-windows-amd64.zip -o /tmp/x.zip && sha256sum /tmp/x.zip` is documented in `.claude/commands/bump-scoop.md` as the manual fallback (NOT run automatically — manual operator decision to incur the bandwidth cost)
- Retry the GitHub API call up to 3 times with 5-second backoff if the release body doesn't yet contain the marker (race with release workflow `append hash` step)

The local zip SHA256 MUST NOT be written to `bucket/cc-launcher.json` — local build action hash differs from CI release asset hash because Go build action hash includes GOMODCACHE state, which differs between fresh CI runner and incremental local cache.

#### Scenario: GitHub release body contains marker
- **WHEN** GitHub API fetch returns body containing `<!-- sha256:auto-hash:abc... -->`
- **THEN** use `abc...` as `bucket/cc-launcher.json` `hash` field

#### Scenario: GitHub release body missing marker
- **WHEN** GitHub API fetch returns body but body does not contain `<!-- sha256:auto-hash: -->`
- **THEN** retry up to 3 times with 5-second backoff (release workflow may not have finished `gh release edit` yet); if still missing, exit with error message instructing operator to manually run download fallback

#### Scenario: GitHub API rate limit exceeded
- **WHEN** GitHub API responds with 403 rate limit error
- **THEN** exit with error message; do NOT auto-fallback to download (operator decides)

### Requirement: GitHub Actions release workflow appends windows zip SHA256 to release body

After the `windows-amd64` job builds and packages `cc-launcher-windows-amd64.zip`, the release workflow SHALL:

- Compute SHA256 via `sha256sum`
- Read current release body via `gh release view "${GITHUB_REF_NAME}" --json body --jq .body` (using implicit `GITHUB_TOKEN`)
- Append `\n\n<!-- sha256:auto-hash:${HASH} -->` to the body (preserving any existing body content such as auto-generated changelog)
- Write back via `gh release edit "${GITHUB_REF_NAME}" --notes "$NEW_BODY"`

This step SHALL run only on `matrix.target.os == 'windows' && matrix.target.arch == 'amd64'` to avoid duplicate appends from other build jobs (linux / darwin variants).

#### Scenario: release workflow appends hash after packaging
- **WHEN** the windows-amd64 build job's `package` step produces `cc-launcher-windows-amd64.zip`
- **THEN** a subsequent step computes the SHA256 and appends it to the release body using the `<!-- sha256:auto-hash:... -->` marker

#### Scenario: marker survives release body edit
- **WHEN** an operator has added a changelog header to the release body before the hash step runs
- **THEN** the hash step preserves the existing body and appends the marker after a blank line; the original content is unchanged