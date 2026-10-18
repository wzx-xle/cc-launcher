# Tasks: release-notes-changelog-injection

## 1. .github/workflows/release.yml: windows-amd64 job 内 changelog 注入 + marker 改名

- [x] 1.1 在 `append windows zip SHA256 to release body` step **之前**新增 step `inject CHANGELOG [v0.X.Y] section to release body`
- [x] 1.2 新 step 用 python 从 `${GITHUB_WORKSPACE}/CHANGELOG.md` 提取 `## [v0.X.Y]` 段（H2 + 后续内容到下一个 H2 `## [` 之前，含 H3 子段 Refactor/Fixes/Features/OpenSpec 等）；`re.search(r'## \[v' + version.lstrip('v') + r'\][^#]*?(?=## \[|\Z)', body, re.DOTALL)`
- [x] 1.3 提取到 → `gh release edit "$GITHUB_REF_NAME" --repo "$GITHUB_REPOSITORY" --notes "$CHANGELOG_SECTION"`；提取不到 → stderr warning + skip（不 fail workflow）
- [x] 1.4 改 `append windows zip SHA256 to release body` step 内 marker 文本：`<!-- sha256:auto-hash:${HASH} -->` → `<!-- sha256:cc-launcher-windows-amd64.zip:${HASH} -->`（line 145）
- [x] 1.5 保持 `if: matrix.target.os == 'windows' && matrix.target.arch == 'amd64'` 不变（line 131）

## 2. build.sh: regex 通用化

- [x] 2.1 `fetch_release_zip_hash_gh_api` 内部 `grep -oP` regex 改：`'sha256:auto-hash:\K[a-f0-9]{64}(?=\s*-->)'` → `'sha256:[^:]+:\K[a-f0-9]{64}(?=\s*-->)'`
- [x] 2.2 Python fallback（如果用到的）regex 同步改通用
- [x] 2.3 3 retries × 5s backoff 行为保留

## 3. 全量回归

- [x] 3.1 跑 `go test ./...` 全绿（build.sh 改动不影响 Go 代码）
- [x] 3.2 跑 `go test -tags smoke ./...` 全绿
- [x] 3.3 跑 `go vet ./...` 无 warning
- [x] 3.4 跑 `bash -n build.sh` 语法 ok（POSIX 语法检查）
- [x] 3.5 跑 `bash -n .github/workflows/release.yml` 实际是 YAML 不走 bash check；用 `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"` 校验 YAML 合法
- [x] 3.6 跑 `openspec validate release-notes-changelog-injection --strict` 通过
- [x] 3.7 跑 `openspec archive release-notes-changelog-injection --yes` 归档
- [x] 3.8 标记本文件所有 `- [x]` → `- [x]`
- [x] 3.9 `git add` 所有变更（不含 CHANGELOG），`git commit -m "feat(release): workflow 注入 CHANGELOG + hash marker 加文件名"`

## 4. CHANGELOG + tag (v0.13.3 patch)

- [x] 4.1 `CHANGELOG.md` `[Unreleased]` 段加 v0.13.3 段（Features：CHANGELOG 注入；Refactor：marker 改名）
- [x] 4.2 `git commit -m "docs: CHANGELOG 加 v0.13.3 release-notes-changelog-injection 段"`
- [x] 4.3 `git tag -a v0.13.3 HEAD -m "v0.13.3: release notes changelog injection"`
- [x] 4.4 一次推送：`git push origin main v0.13.3`
- [x] 4.5 （可选）`cd /tmp/scoop-bucket && rm -rf dist && bash build.sh bump-scoop v0.13.3` 验证 marker regex 兼容新格式（**不在**本 release 改动内，仅 smoke test）
