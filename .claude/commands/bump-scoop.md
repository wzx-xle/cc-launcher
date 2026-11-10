---
name: "bump-scoop"
description: 计算 cc-launcher Windows amd64 zip 的 SHA256 并更新 wzx-xle/scoop-bucket 仓的 bucket/cc-launcher.json
category: Workflow
tags: [release, scoop, sha256, manifest, github-api]
---

Bump the wzx-xle/scoop-bucket manifest for a new cc-launcher release.

**Input**: `/bump-scoop <version>` — version is the cc-launcher release tag (e.g. `v0.12.0`).

**Pair**: this command produces the manifest JSON via `./build.sh bump-scoop <version>` (POSIX) or `pwsh bin/bump-scoop.ps1 <version>` (Windows / PowerShell). Run after `bump-version` has pushed the v* tag and GitHub Actions release workflow has finished.

**stdout contract**: both scripts write **only the manifest JSON to stdout**; all progress / warning / error output goes to stderr. So `> cc-launcher.json` captures a directly-usable file (the POSIX script used to print build logs to stdout — fixed 2026-10-21). The two platforms' stdout is **byte-identical** (same md5 `031a2c95…`).

**Windows / PowerShell caveats** (found on a real Windows box 2026-10-21):

- **PowerShell 5.1 的 `>` 重定向产出 UTF-16LE**（`Out-File` 默认编码 Unicode）→ `pwsh script.ps1 > manifest.json` 在 PS 5.1 上写出来的不是 UTF-8 JSON。**用 `-OutFile` 参数**：`pwsh bin/bump-scoop.ps1 v0.14.0 -OutFile manifest.json` → 脚本自己按 UTF-8 **无 BOM** + LF 写盘，与 bash 版字节完全一致（PS 5.1 / 7+ 都一样）。
- **.ps1 文件必须带 UTF-8 BOM**：PS 5.1 读无 BOM 的 .ps1 按 Windows-1252 解码 → 中文注释乱码 → 语法错误。PS 7 默认 UTF-8 无 BOM，**所以在 PS 7 上测测不出来**。仓库用 `.gitattributes`（`*.ps1 text eol=crlf`）固定行尾，BOM 由 `internal/release/psscript_test.go` 守卫（CI 会红）。
- **在 Linux 上用普通工具编辑 .ps1 会破坏 BOM/CRLF**（Python 的通用换行转换会把 CRLF 变 LF）→ 编辑后跑 `go test ./internal/release/ -run TestPowerShellScripts`。

**Prerequisites**:
- `v*` tag pushed to `wzx-xle/cc-launcher`
- GitHub Actions release workflow finished (5 build jobs: 1 windows zip + 2 linux tar.gz + 2 darwin tar.gz). The windows-amd64 job's "append windows zip SHA256 to release body" step MUST have completed — it writes a `<!-- sha256:cc-launcher-windows-amd64.zip:HEX -->` marker into the release body.
  **Without this marker the new flow cannot fetch the hash.**
  (旧格式 `<!-- sha256:auto-hash:HEX -->` 是 v0.12.0~v0.13.2 的；两个脚本都兼容新旧格式)
- `wzx-xle/scoop-bucket` repo cloned to `/tmp/scoop-bucket` — `/tmp` 会被清空，每次都要确认
  在（`ls -d /tmp/scoop-bucket`）；没有就 clone：

  ```bash
  git clone https://github.com/wzx-xle/scoop-bucket /tmp/scoop-bucket
  ```

  **remote 用 https URL**（不是 `git@github.com:`）—— 本机的全局 insteadOf 规则会把
  https 改写成 SSH，正好走通；写成 `git@` 反而会被改写成 https 而直连超时

**Why hash comes from GitHub API (not local build)**

Go's build action hash is a content hash of all inputs (source + GOMODCACHE state + toolchain + env vars). Fresh CI release workflow's GOMODCACHE vs local incremental cache **always differ**, producing ~58K bytes of diff across ~22K regions in the resulting binary (Go runtime, pclntab function hash table, embedded build ID). The local zip SHA256 **will never byte-level match** the GitHub release asset SHA256, no matter what `-trimpath`/`touch -d @0`/`TZ=UTC zip -X` does.

Therefore the new flow fetches the release zip SHA256 from **GitHub release body** (which CI writes via `gh release edit`), avoiding both the 18 MB zip download AND the failed local-hash-matching-release assumption.

**Steps**

1. **Compute local zip + fetch release hash from GitHub API**:

   ```bash
   rm -rf dist && ./build.sh bump-scoop v0.X.Y > /tmp/cc-launcher-manifest.json
   ```

   stdout 是**纯 manifest JSON**（日志走 stderr）→ 必须重定向到文件，直接跑会把 JSON
   刷在终端日志里。

   **On Windows** (no Git Bash / python3 / gh needed):

   ```powershell
   pwsh bin/bump-scoop.ps1 v0.X.Y
   ```

   The script:
   - cross-compiles `cc-launcher-windows-amd64.zip` locally (kept for human verification)
   - fetches the release body via `curl https://api.github.com/repos/wzx-xle/cc-launcher/releases/tags/v0.X.Y`
   - parses `<!-- sha256:cc-launcher-windows-amd64.zip:HEX -->` from the body (3 retries × 5s backoff for race with release workflow's `gh release edit` step; 旧格式 `auto-hash` 也认)
   - emits the JSON with the **release asset's** hash (NOT the local one)

   Output: full manifest JSON.

   **If the script fails** with "fetch_release_zip_hash_gh_api 失败" (rate limit, network, or release workflow hasn't appended the marker yet):
   - Wait 30s and run `bash build.sh bump-scoop v0.X.Y` again (marker may not yet be in body — wait longer)
   - **Manual fallback** (download 18 MB zip, costs bandwidth — operator decides):

     ```bash
     curl -sSL https://github.com/wzx-xle/cc-launcher/releases/download/v0.X.Y/cc-launcher-windows-amd64.zip \
       -o /tmp/x.zip && sha256sum /tmp/x.zip
     ```

2. **写 `/tmp/scoop-bucket/bucket/cc-launcher.json`** —— **直接用步骤 1 的输出覆盖**（脚本输出
   就是完整 manifest，`pre_uninstall` 钩子已经在里面）：

   ```bash
   cp /tmp/cc-launcher-manifest.json /tmp/scoop-bucket/bucket/cc-launcher.json
   ```

   **只在需要手改时才照下面的格式写**（脚本输出已含 `pre_uninstall`，**手写时必须保留它**）：

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
       },
       "pre_uninstall": [
           "Write-Host '[cc-launcher] scoop uninstall hook: stopping processes that hold the app directory'",
           "try { ccl serve --stop } catch { }",
           "$d = $null; try { $d = (appdir 'cc-launcher') } catch { }",
           "if ($d) { Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -like \"$d*\" } | Stop-Process -Force -ErrorAction SilentlyContinue }",
           "Start-Sleep -Milliseconds 500"
       ]
   }
   ```

   **Do NOT include an `arm64` block** — the release workflow matrix dropped windows/arm64 as of v0.11.2; including it would point to a non-existent asset and break `scoop install`.

   **Do NOT use the local zip's SHA256** — local build hash ≠ GitHub release asset hash (see top of doc). The `hash` field MUST come from step 1's fetched value.

   **Do NOT drop `pre_uninstall`** — 它是 scoop 卸载时停掉 daemon 的钩子（漏了 → 卸载后
   daemon 还占着 app 目录，Windows 上文件被锁、卸载不干净）。2026-11-06 补上的，见
   memory `scoop-bucket-manifest-pre-uninstall`。

3. **Commit and push to wzx-xle/scoop-bucket**:

   ```bash
   cd /tmp/scoop-bucket
   git add bucket/cc-launcher.json
   git -c user.name=wxyz -c user.email=wxyz@users.noreply.github.com commit -F - <<'MSG'
   chore(scoop): bump cc-launcher to v0.X.Y

   v0.X.Y release asset hash: <hash> (windows-amd64 zip, fetched from GitHub
   release body via bump-scoop-hash-trace flow)
   MSG
   git push origin main
   ```

   **Do NOT touch the global `url.https://github.com/.insteadof` rules.** They read:

   ```
   url.https://github.com/.insteadof = git@github.com:
   url.https://github.com/.insteadof = ssh://git@github.com:
   ```

   `insteadOf` rewrites **https://github.com/ → SSH**（方向是"URL 写的是 https，实际走
   SSH 通道"）。本机到 GitHub 的 https **直连不通**（60s 超时），这两条规则正是让它
   走通的原因 —— clone / push 用 `https://github.com/...` 这个 URL 即可。**unset 掉它们
   反而会让 push 走直连 https 而超时失败**（2026-11-09 实测：保持原样，clone + push
   都成功；全局配置零改动）。

   只有在**手动**用 `git@github.com:` 形式的 remote 时才会被改写成 https 而失败 ——
   那时把 remote 换成 https URL 即可，**不要动全局配置**。

   **Commit message 用 `-F -` + heredoc**，不要用 `-m "...\n\n..."`：bash 的双引号里
   `\n` 是**字面两个字符**，不会换行 → commit message 里会出现字面的 `\n`。

   **Commit message is English** (NOT Chinese): the `wzx-xle/scoop-bucket` repo is cloned by Windows users via `scoop update`; git GUI / GitHub web on Windows mis-renders CJK characters without proper LANG/LC_ALL. cc-launcher's own commit message convention (Chinese) does NOT apply to the bucket repo — bucket commits stay ASCII to avoid encoding hazards for downstream consumers.

4. **Verify**:

   ```bash
   git ls-remote https://github.com/wzx-xle/scoop-bucket main

   # 再看一眼远端 manifest 的实际内容（确认 version/url/hash 都换对了）
   gh api repos/wzx-xle/scoop-bucket/contents/bucket/cc-launcher.json --jq '.content' \
     | base64 -d | grep -E '"version"|"hash"|"url"'
   ```

   Confirm the remote main has the new commit hash **and** the manifest's version / url / hash
   are the new ones. Users can then `scoop update cc-launcher` to receive v0.X.Y.

**Common mistakes**

- **Including an `arm64` block** in the manifest — breaks `scoop install` because the release no longer ships a windows/arm64 zip. Removed in commit `299a2a4`.
- **Unsetting the global `url.https://github.com/.insteadof` rules before pushing** — those rules rewrite
  https://github.com/ → SSH, which is exactly how this machine reaches GitHub (its https直连不通).
  Unsetting them makes `git push` go over a direct https connection and time out (2026-11-09 实测).
  **Leave the global config alone**; use an `https://github.com/...` remote URL.
- **Hand-writing the manifest and dropping `pre_uninstall`** — that hook stops the daemon during
  `scoop uninstall`; without it the app directory stays locked on Windows. Copy the script's output
  wholesale instead of retyping the JSON.
- **Pushing bucket to a non-GitHub remote** — bucket repo lives on GitHub only (`wzx-xle/scoop-bucket`); `git.wxyz.ren` mirror is for the cc-launcher source only and does not host the bucket. Verify the bucket repo's remote URL before `git push`.
- **Using `gh` on Windows** — the `gh` CLI has no official Windows build (Windows users install it via scoop/chocolatey). `bin/bump-scoop.ps1` deliberately uses only PowerShell built-ins (`Invoke-RestMethod`); set `GITHUB_TOKEN` for the higher API rate limit.
- **Redirecting the script's stdout into the bucket file without checking it starts with `{`** — both scripts now emit pure JSON on stdout, but older `build.sh` versions printed build logs there too.
- **Using the local zip's SHA256 for the manifest hash** — local build's SHA256 **never matches** GitHub release asset's SHA256 (Go build action hash includes GOMODCACHE state; fresh CI vs incremental local cache always differ). Always use the value fetched from GitHub release body by `build.sh bump-scoop` (or the manual download fallback).
- **Running bump-scoop before the release workflow's "append hash to release body" step completes** — the marker won't be in the release body yet, fetch will retry 3×5s then fail. Wait 30s and re-run, or check the release workflow status before running.
- **Using the wrong username in the manifest URL** — use `wzx-xle` (GitHub) for this repo, not `wxyz` (Gitea). See memory `github-vs-gitea-usernames`.

**Related**

- `build.sh bump-scoop` subcommand — produces local zip + fetches release hash via GitHub API
- `bin/bump-scoop.ps1` — PowerShell 版（Windows 原生，只需 pwsh；不打本地 zip，只取 release 真值 hash）
- `.github/workflows/release.yml` — produces the release assets + writes the marker into release body
- memory `scoop-bucket-zip-hash-stability` — historical context (the "four conventions" no longer suffice for byte-level match)
- memory `github-push-via-ssh-with-insteadof` — the SSH push workflow
  （注：该 memory 说"全局规则已删"，但 2026-11-09 实测**规则仍在**（两条 insteadOf），
  且正是它们让 github 的 https 走 SSH 通道 —— 见步骤 3）
- memory `github-vs-gitea-usernames` — `wzx-xle` vs `wxyz` distinction
- `.claude/commands/bump-version.md` — companion slash command
- `openspec/changes/archive/2026-10-14-bump-scoop-hash-trace/` — full change rationale