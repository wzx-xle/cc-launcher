# Tasks: bump-scoop-hash-trace

## 1. Release workflow：写 hash 到 release body

- [x] 1.1 `.github/workflows/release.yml`：在 windows amd64 build job 末尾 + `package` step 之后 + 整体 zip output 之前，加 `append windows zip SHA256 to release body` step（用 `gh release edit`）
- [x] 1.2 该 step 用 `GH_TOKEN` env var + `gh release view --json body` 读当前 body + append `<!-- sha256:auto-hash:${HASH} -->` + `gh release edit --notes` 写回去
- [x] 1.3 step 用 `if: matrix.target.os == 'windows' && matrix.target.arch == 'amd64'` 保证只 windows job 跑（其他 target 5 个不重复造）
- [x] 1.4 验证：`push_via_workflow_dispatch` dry-run 看 body 是否含 marker — push 时验证；本地不能 dry-run GitHub API

## 3. build.sh 改：从 GitHub API 拿 hash

- [x] 3.1 `build.sh` 的 `bump_scoop` 函数改：先本机 build 出 zip（保留，用于 verify），但**不**用本地 hash 写 manifest —— 改用 GitHub API 拿 release asset hash
- [x] 3.2 `bump_scoop` 内加 fetch_release_zip_hash_gh_api 函数：curl GitHub releases API → python3 parse `.body` → grep `sha256:auto-hash` → 拿 hash string
- [x] 3.3 fallback logic：若 GitHub API fetch 失败（rate limit / network / no marker）→ 显式报错 + 给 manual download instructions（**不**自动 download —— 用户明确反对每次下载流量）
- [x] 3.4 重试逻辑：GitHub API 调用 fail-with-retry 3 次（每次等 5 秒）防 release workflow 跟 bump-scoop race
- [x] 3.5 regex fix：grep -oP 用 `(?=\s*-->)` lookahead 而不是 `\s*-->` consume（避免 `\K` 之后 consume 部分被 capture 进 hash）

## 4. bump-scoop skill 文档更新

- [ ] 4.1 `.claude/commands/bump-scoop.md`：把"4 步流程"改成"GitHub API first"流程；移除"本地 build hash 与 release asset byte-level 一致"陈述；改为"本地 build 出 zip 用于 verify，hash 来自 GitHub release body"
- [ ] 4.2 加 common mistake 警告："不要用本机 build 的 zip hash 写 bucket manifest — 那个 hash 永远不 match release asset"

## 5. 验证

- [x] 5.1 `bash -n build.sh` 通过
- [x] 5.2 `bash build.sh bump-scoop v0.12.0` 在 main 上跑 — local zip build OK，GitHub API fetch (因 rate limit 失败) + clear fallback instructions 打印
- [x] 5.3 unit test：grep -oP `(?=\s*-->)` lookahead regex 解析 fake body → 拿 64-char hex hash (PASS)
- [ ] 5.3 bucket/cc-launcher.json v0.12.0 hash 跟 GitHub release asset SHA256 一致 — 待下个 release (release workflow 跑完 append marker 后才能 verify)
- [x] 5.4 `openspec validate bump-scoop-hash-trace --strict` 通过
- [ ] 5.5 `openspec archive bump-scoop-hash-trace --yes`

## 6. 文档与归档

- [ ] 6.1 CHANGELOG.md `[Unreleased]` 段加条目（取决于是否 bump）
- [ ] 6.2 commit message 用中文 + conventional commit prefix (`ci:` / `feat:` / `fix:`)
- [ ] 6.3 archive 后 change 移到 `openspec/changes/archive/`