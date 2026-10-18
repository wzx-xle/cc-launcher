## MODIFIED Requirements

### Requirement: build.sh bump-scoop fetches release zip SHA256 from GitHub release body, not local build

When `build.sh bump-scoop vX.Y` is invoked, it SHALL:

- Cross-compile `cc-launcher-windows-amd64.exe` via existing `windows` + `windows_build` flow, then run `pack_windows_zip_for_scoop` to produce the local `cc-launcher-windows-amd64-scoop.zip` (kept for sourceable verification, not for distribution)
- Fetch `https://api.github.com/repos/wzx-xle/cc-launcher/releases/tags/vX.Y` via `curl` and parse `body` field
- Extract the substring matching the regex `sha256:[^:]+:\K[a-f0-9]{64}(?=\s*-->)` (i.e. `<!-- sha256:<filename>:<64 hex chars> -->` for any filename) from the release body and use the 64-hex-char value as the `hash` field in `bucket/cc-launcher.json`. The regex accepts any filename in the marker (e.g. `cc-launcher-windows-amd64.zip` for v0.13.3+, `auto-hash` for v0.12.0 ~ v0.13.2), so historical releases remain parseable.
- If the GitHub API call fails (rate limit, network, missing marker): respond with explicit failure. Manual fallback to `curl -sSL https://github.com/wzx-xle/cc-launcher/releases/download/vX.Y/cc-launcher-windows-amd64.zip -o /tmp/x.zip && sha256sum /tmp/x.zip` is documented in `.claude/commands/bump-scoop.md` as the manual fallback (NOT run automatically — manual operator decision to incur the bandwidth cost)
- Retry the GitHub API call up to 3 times with 5-second backoff if the release body doesn't yet contain the marker (race with release workflow `append hash` step)

The local zip SHA256 MUST NOT be written to `bucket/cc-launcher.json` — local build action hash differs from CI release asset hash because Go build action hash includes GOMODCACHE state, which differs between fresh CI runner and incremental local cache.

#### Scenario: GitHub release body contains marker with filename
- **WHEN** GitHub API fetch returns body containing `<!-- sha256:cc-launcher-windows-amd64.zip:abc... -->`
- **THEN** use `abc...` as `bucket/cc-launcher.json` `hash` field

#### Scenario: GitHub release body contains legacy auto-hash marker
- **WHEN** GitHub API fetch returns body containing `<!-- sha256:auto-hash:abc... -->` (v0.12.0 ~ v0.13.2 release)
- **THEN** use `abc...` as `bucket/cc-launcher.json` `hash` field (backward-compatible regex match)

#### Scenario: GitHub release body missing marker
- **WHEN** GitHub API fetch returns body but body does not contain a `<!-- sha256:...:HEX -->` marker
- **THEN** retry up to 3 times with 5-second backoff (release workflow may not have finished `gh release edit` yet); if still missing, exit with error message instructing operator to manually run download fallback

#### Scenario: GitHub API rate limit exceeded
- **WHEN** GitHub API responds with 403 rate limit error
- **THEN** exit with error message; do NOT auto-fallback to download (operator decides)

### Requirement: GitHub Actions release workflow appends windows zip SHA256 to release body

After the `windows-amd64` job's `package` step produces `cc-launcher-windows-amd64.zip`, the release workflow SHALL run two sequential body-edit steps in this order:

1. **`inject CHANGELOG [v0.X.Y] section to release body`**: read `CHANGELOG.md` from the repo root, extract the H2 section `## [v0.X.Y] - <date> — <change-name>` (the H2 header line plus all subsequent content up to the next H2 `## [` heading or end of file — this includes the version's H3 subsections such as `### Refactor` / `### Fixes` / `### Features` / `### OpenSpec`), and use `gh release edit "${GITHUB_REF_NAME}" --notes "$CHANGELOG_SECTION"` to set the release body to that section. If the section cannot be found (e.g. tag version doesn't match any `## [...]` header in CHANGELOG.md), print a warning to stderr and skip the inject (release body stays empty until the next step appends the hash marker). This step MUST run before the `append hash` step so the marker is appended *after* the changelog content.

2. **`append windows zip SHA256 to release body`**: read the current release body (which now contains the changelog section from step 1), compute `HASH=$(sha256sum cc-launcher-windows-amd64.zip | awk '{print $1}')`, append `\n\n<!-- sha256:cc-launcher-windows-amd64.zip:${HASH} -->` to the body, and write back via `gh release edit "${GITHUB_REF_NAME}" --notes "$NEW_BODY"`. The marker embeds the **filename** (not the legacy `auto-hash` token) so multiple platforms' hashes can coexist with distinct markers in future.

Both steps SHALL run only on `matrix.target.os == 'windows' && matrix.target.arch == 'amd64'` to avoid duplicate edits from other build jobs (linux / darwin variants).

The final release body structure MUST be: `<CHANGELOG [v0.X.Y] section>\n\n<!-- sha256:cc-launcher-windows-amd64.zip:<64 hex chars> -->`. The marker remains an HTML comment (hidden in the rendered release page) so the SHA256 string does not appear as user-visible markdown and `build.sh bump-scoop` can extract it via the regex `sha256:[^:]+:\K[a-f0-9]{64}(?=\s*-->)`.

#### Scenario: release workflow injects CHANGELOG section before appending hash
- **WHEN** the windows-amd64 build job's `package` step produces `cc-launcher-windows-amd64.zip` and `CHANGELOG.md` has a `## [v0.X.Y] - <date> — <change-name>` section
- **THEN** the release body is set to that section via `gh release edit --notes`, and a subsequent step computes the SHA256 and appends `\n\n<!-- sha256:cc-launcher-windows-amd64.zip:... -->` to it; the final body shows the CHANGELOG section to GitHub users and ends with the hidden HTML-comment hash marker

#### Scenario: CHANGELOG section not found for the tag
- **WHEN** the windows-amd64 build job runs and `CHANGELOG.md` does NOT contain a `## [v0.X.Y]` section matching the current tag (e.g. operator forgot to update CHANGELOG before tagging)
- **THEN** the inject step prints a stderr warning and skips; the append-hash step still runs and the release body becomes only `\n\n<!-- sha256:cc-launcher-windows-amd64.zip:... -->` (legacy single-marker form); workflow does NOT fail

#### Scenario: marker survives release body edit
- **WHEN** the inject step has written a CHANGELOG section to the release body before the hash step runs
- **THEN** the hash step preserves the existing body and appends the marker after a blank line; the original changelog content is unchanged

#### Scenario: marker filename is the zip basename, not the legacy auto-hash token
- **WHEN** the append-hash step writes the marker
- **THEN** the marker text is `<!-- sha256:cc-launcher-windows-amd64.zip:<64 hex chars> -->` (filename = `cc-launcher-windows-amd64.zip`), NOT the legacy `<!-- sha256:auto-hash:<64 hex chars> -->` form
