## Why

GitHub release 页面 release body 现状是**只**含 `<!-- sha256:auto-hash:HEX -->` 一个 HTML 注释 marker。用户在 release 页面视觉上**看不到任何 changelog**（HTML 注释不渲染）——只有打开"Edit release"或 API `GET /releases/tags/...` 才看得到 hash 字符串。base spec `build-scoop` Requirement 2 第 36 行说"preserving any existing body content such as auto-generated changelog"——但**没有任何 step 实际注入 changelog**，spec 是空头承诺。

本次：
1. release workflow 新增独立 step `inject changelog to release body`：从仓库 `CHANGELOG.md` 提取当前 tag 对应 `[0.X.Y]` 段（H3 子段：Refactor / Fixes / Features / OpenSpec 等），用 `gh release edit --notes` 写进 release body
2. hash marker 加文件名：`<!-- sha256:auto-hash:HEX -->` → `<!-- sha256:cc-launcher-windows-amd64.zip:HEX -->`（`auto-hash` 替换为真实文件名）
3. `build.sh bump-scoop` 的 `fetch_release_zip_hash_gh_api` regex 同步改通用格式 `sha256:[^:]+:\K[a-f0-9]{64}(?=\s*-->)`（向前兼容旧 marker，新 marker 都能 parse）

**用户最终看到**：release 页面顶部是 CHANGELOG 段（用户视觉可见的 markdown），底部是 hash marker（hidden HTML 注释，build.sh parse 用）。

## What Changes

- `.github/workflows/release.yml`：windows-amd64 job 内新增 step `inject CHANGELOG [v0.X.Y] section to release body`（跑在 `append windows zip SHA256` step **之前**），用 python 从 `CHANGELOG.md` 提取该版本段 + `gh release edit --notes` 写入；`append windows zip SHA256` step 内 marker 格式改 `<!-- sha256:cc-launcher-windows-amd64.zip:${HASH} -->`
- `build.sh` `fetch_release_zip_hash_gh_api`：regex 改通用 `sha256:[^:]+:\K[a-f0-9]{64}(?=\s*-->)`；Python API response parse 后用同 regex 提取
- `openspec/specs/build-scoop/spec.md`：MODIFIED Requirement 1（regex 格式）+ Requirement 2（marker 格式 + 新增 changelog 注入 step + Scenario 覆盖）

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `build-scoop`：MODIFIED Requirement 1（marker regex 通用化）+ Requirement 2（marker 加文件名 + 新增 changelog 注入 step 描述 + Scenario 覆盖 marker+changelog 顺序与不破坏）

## Impact

- `.github/workflows/release.yml` — windows-amd64 job 新增 1 step（python extract CHANGELOG 段 + gh release edit）；marker 改带文件名
- `build.sh` — `fetch_release_zip_hash_gh_api` regex 改通用；保留 3 retries × 5s backoff 行为
- `openspec/specs/build-scoop/spec.md` — MODIFIED 2 个 requirement
- 用户视觉变化：GitHub release 页面从"空白"变为"CHANGELOG 段 + 隐藏 hash marker"
- **无** release asset 字节级变化（marker 是 HTML 注释不参与 zip 字节）
- **无** JSON shape 变化 / CLI 变化 / state.json 变化
- `bucket/cc-launcher.json` 解析流程不变（hash 仍从 marker 拿）
- 旧 release 的 `<!-- sha256:auto-hash:HEX -->` marker 仍可被新 regex 匹配（`auto-hash` 是 `[^:]+`），向后兼容
