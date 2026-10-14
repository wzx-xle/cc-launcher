## Why

`build.sh bump-scoop v0.X.Y` 本机算的 zip SHA256 跟 GitHub release asset 的 hash **永远不一致**。两个 root cause：

1. `main.Commit` ldflag 长度不一致（build.sh 用 `git --short` 7 字符 vs CI `${GITHUB_SHA::8}` 8 字符）—— **v0.12.4 已 fix**（commit `0d09771`）

2. **Go build action hash 是 content hash of all inputs**（source + GOMODCACHE state + toolchain + env vars）。fresh CI release workflow（GOMODCACHE 干净）vs 本机 incremental cache 一定不一致 → binary 在 58824 字节 diff 散在 21822 个 region（pclntab function hash 表 + build ID 字符串）—— **物理不可修**（Go runtime 设计）

后果：每次 `bump-scoop` 都必须 `curl` 下载 release zip（18 MB/release）拿真值。下载流量在受限网络（手机热点 / 流量计费）下成本不可忽略，且 GitHub Actions release workflow 跑完后人工跑 bump-scoop 是 hot path 不能 always offload。

## What Changes

- **改 GitHub Actions release workflow**：Windows build job 跑完后算 `cc-launcher-windows-amd64.zip` SHA256 + `gh release edit` 把 hash append 到 release body（用一个 HTML 注释 marker 让 `bump-scoop` parse）
- **改 `build.sh bump-scoop` 流程**：不再依赖本地 zip hash match release，改为 `curl GitHub API release endpoint` fetch release body + parse hash（**不下载 zip**）
- **改 `.claude/commands/bump-scoop.md` skill**：文档说明新流程（GitHub API fetch + 不依赖本地 build）

## Capabilities

### Modified Capabilities

- `build-scoop`：本机 hash 与 release alias 永远不一致是物理事实——`bump-scoop` 流程改为 GitHub API 拿 hash（不下载 zip body）

### 新增小变更

- `.github/workflows/release.yml`：Windows job 末尾加 step（算 zip hash + `gh release edit` 写 release body）
- `build.sh`：`bump_scoop` 函数改写：先本机 build 出 zip，再 curl GitHub release API fetch release body + parse hash 写入 manifest JSON

## Impact

- **代码**：
  - `.github/workflows/release.yml` 加 step（用 `softprops/action-gh-release@v2` 已有的 action + `gh release edit`）
  - `build.sh bump_scoop` 函数改写（约 30 行 diff）
- **文档**：
  - `.claude/commands/bump-scoop.md`：把"4 步流程"改成 GitHub API first 流程
  - `openspec/specs/build-scoop/spec.md`：spec 改（如果有）
- **流量**：每次 `bump-scoop` 下载 **0 字节**（GitHub release endpoint 几 KB metadata），替代之前 18 MB zip 下载
- **依赖**：无新增依赖（`gh` CLI 在 CI 上 + 本机 `curl` + `jq`）