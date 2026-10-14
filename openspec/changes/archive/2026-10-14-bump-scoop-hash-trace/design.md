## Context

`build.sh bump-scoop v0.X.Y` 输出 zip SHA256 作为 scoop manifest 的 `hash` 字段。原设计假设本机 hash byte-level == GitHub release asset hash（"四约定"：`-trimpath` + 去掉 `BuildTime` + `touch -d @0` + `TZ=UTC zip -X`）。

**实测不可能**。Go build action hash 是 **content hash of all inputs**（source + GOMODCACHE state + toolchain + env vars）。fresh CI release workflow 跟本机 incremental build 的 GOMODCACHE state 一定不一致 → 58824 字节 diff 散在 21822 个 region（pclntab function hash 表 + build ID 字符串），**无法手动 patch**。

后果：每次 bump-scoop 必须 download 18 MB release asset 拿真值，下载流量在受限网络下成本不可忽略。

## Goals / Non-Goals

**Goals:**
- bump-scoop 不依赖 download release asset（GitHub API fetch metadata 即可拿 hash）
- Release workflow 在 GitHub side 算 hash + 写 release body（自动化，不增加人工操作）
- 兼容 `gh` CLI 在 GitHub Action runner 上（无需新 secret，已有 `secrets.GITHUB_TOKEN`）

**Non-Goals:**
- 不改 Go binary 本身的 byte-level 重现（物理不可能）
- 不改 `build.sh` 自身 build 出的 binary 内容（仍本地 build，跟 release 行为无关）
- 不动 `bucket-scoop-zip-hash-stability` memory 的"四约定"——那部分描述是 build action ID 的一部分，但 release asset hash 才是 scoop manifest 需要的真值

## Decisions

### Decision 1: hash 写 release body 用 HTML 注释 marker

**决策**：release workflow 算 zip hash 后，append 到 release body 用 `<!-- sha256:auto-hash:abc... -->` 格式（HTML 注释 + 自定义 marker）。

**为什么用 marker**：
- 不会被 GitHub UI 渲染（注释）
- bump-scoop 用 `grep -oP` 精确 parse，不依赖 release body 其他内容
- 易于人工 verify（view source 看到 hash）

**为什么用 release body**（不是 asset metadata / Gist / separate file）：
- GitHub API `releases/tags/vX.Y.Z` 端点直接返回 `body` 字段，**一次 fetch 拿到**
- 不需要 list assets / 单独 fetch asset
- 跟 release tag 自然关联（release body 是 un 关联于 tag 的）

### Decision 2: bump-scoop 用 GitHub API 不下载

**决策**：`build_scoop` 函数 fetch 阶段：
1. 本地 build 出 windows zip（保留本机 build，因为 zip 内容即使 hash 不一致也是正确 binary）
3. `curl https://api.github.com/repos/wzx-xle/cc-launcher/releases/tags/vX.Y.Z` → JSON → `body` → `grep sha256:auto-hash` → 真 hash
4. 用真 hash 写 bucket manifest JSON
5. 把本地 build 的 zip 用作 zip 内容文件（manifest hash 跟它**不一致** —— 这是对的，hash 写的是 release asset 那个 zip 的 hash）

**为什么 manifest hash 必须用 release asset hash**：
- scoop 用户 `scoop install cc-launcher` 时 scoop 客户端验证本地下载的 zip hash == manifest hash
- scoop 下载是 GitHub release asset（即 CI 出的 zip），不是 build.sh 本机 build 的 zip
- 所以 manifest hash 必须是 GitHub release asset 的 hash（不是本机的）

**GitHub API rate limit**：
- unauthenticated 60 req/hour —— release workflow 偶尔跑，**足够**
- bump-scoop 一天可能跑几次，**足够**

### Decision 3: 不依赖 `softprops/action-gh-release@v2` 的 `body` 字段

**原因**：`softprops/action-gh-release@v2` 的 `body` 参数在 release 创建时设置一次。但 release body 在跑完后可能**已有内容**（e.g. 自动 changelog），我们要的是**append hash 到现有 body**，不是覆盖。

实施：用 GitHub Action runner 上 implicit `gh` CLI（`GITHUB_TOKEN` 已 inject）：
```yaml
- name: append windows zip SHA256 to release body
  if: matrix.target.os == 'windows' && matrix.target.arch == 'amd64'
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  run: |
    HASH=$(sha256sum cc-launcher-windows-amd64.zip | awk '{print $1}')
    BODY=$(gh release view "${GITHUB_REF_NAME}" --json body --jq .body)
    NEW_BODY="${BODY}

    <!-- sha256:auto-hash:${HASH} -->"
    gh release edit "${GITHUB_REF_NAME}" --notes "$NEW_BODY"
```

（`gh release edit` 已经有 `-F / -notes` flag 接受 new body via flag）

### Decision 4: bump-scoop manifest hash 是 src 字段而不是 self hash

**澄清**：当前 scoop manifest 的 `hash` 字段语义是**安装时校验的 zip hash**——scoop 用户下载 zip 后的 hash 必须等于 manifest hash。这 hash 必须是 **GitHub release asset 的 hash**，**不是**本机 build 的 zip hash。

所以本 change 的 manifest 字段用 release asset hash 写，不是本机 build hash。本机 build 的 zip 在 bump-scoop 流程里**仍然产生**（作为 content file），但它的 hash **不写进 manifest**——它仅是给人工 verify 用的"本地 build 出来应该长这样" reference，不影响 scoop install。

## Risks / Trade-offs

- [GitHub API rate limit] → 60/hr unauthenticated。如果用户网络 IP 被 limit，bump-scoop 失败。缓解——可以加 GHPAT 环境变量支持（bump-scoop 检测 GITHUB_TOKEN env var，有就加 `Authorization: token $GITHUB_TOKEN` header）
- [release workflow 跑完后可能晚几秒才 append hash] → bump-scoop 紧跟跑可能 race。bump-scoop 流程 fail-with-retry 3 次，每次等 5 秒
- [release body 格式变化] → bump-scoop marker 解析失败。bump-scoop 显式 fail 而不是 fallback 到 download
- [本地 build zip 跟 release zip 内容不一致] → bump-scoop 的 zip file 在 bucket commit 阶段有用但**最终 scoop install 不下这个**，scoop 下 release asset。所以本地 zip 的 hash 跟 manifest hash 不一致**是对的**

## Migration Plan

- 无需数据迁移（scoop bucket 不变——hash 字段填的是真值）
- 改 build.sh + `.github/workflows/release.yml` + `.claude/commands/bump-scoop.md`
- 现有 bucket/cc-launcher.json (v0.12.0 manifest) **已用 release asset hash**（之前 bump-scoop fallback download 拿的）—— 无需重 bump

## Open Questions

无。