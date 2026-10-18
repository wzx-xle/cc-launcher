## Context

GitHub release 是 cc-launcher 用户从 GitHub 装到本机的入口（`scoop install cc-launcher` 之前用户会去看 release notes）。v0.12.0 加 `--day` 后 `bump-scoop-hash-trace` change（v0.12.0 末）引入了 release body marker 机制——但只 append 了 hash marker，**没**有 changelog 内容，导致 release 页面**视觉上是空**的。

base spec `build-scoop/spec.md` Requirement 2 第 36 行**有写**：
> Append `\n\n<!-- sha256:auto-hash:${HASH} -->` to the body (preserving any existing body content such as auto-generated changelog)
> ...
> When an operator has added a changelog header to the release body before the hash step runs
> THEN the hash step preserves the existing body and appends the marker after a blank line; the original content is unchanged

该 spec 描述"如果有 changelog 就保留"——但**没有 step 实际注入**。本次把 spec 落实，新增 step 自动从 `CHANGELOG.md` 提取当前 tag 对应 `[0.X.Y]` 段注入 release body。

## Goals / Non-Goals

**Goals:**
- GitHub release 页面 release body 显示 CHANGELOG `[0.X.Y]` 段（Refactor / Fixes / Features / OpenSpec 等 H3 子段）
- hash marker 改名加真实文件名 `cc-launcher-windows-amd64.zip`
- `build.sh bump-scoop` regex 通用化，向前兼容旧 marker
- base spec `build-scoop` 同步改 MODIFIED 2 个 requirement
- 无 release asset 字节级变化（marker 是 HTML 注释不进 zip）

**Non-Goals:**
- 不改 changelog 格式（不改 CHANGELOG.md 段结构）
- 不改 release workflow 触发条件 / matrix
- 不改 `softprops/action-gh-release` 配置（CHANGELOG 注入用 `gh release edit` 而不是 `softprops` 的 `body:` 参数，因为 changelog 提取在 matrix job 内、create release 之后）
- 不改 hash 算法（仍是 SHA256）
- 不改 marker 形态的 HTML 注释包裹（保留 `<!-- ... -->`，避免 markdown 渲染把 hash 字符串染上排版）
- 不向后兼容 `<!-- sha256:auto-hash:HEX -->` 的旧格式（虽然 regex 兼容，但 marker 生成侧全切到新格式）
- 不动 `scoop-bucket-zip-hash-stability` memory 的"四约定"（hash 字节级稳定性由 `-trimpath` + ldflags 长度对齐保证，与本 change 解耦）

## Decisions

### Decision 1：CHANGELOG 注入点在 windows job 内、`append hash` step 之前

5 个 matrix job 各自独立跑，注入 step 不能跨 job。`softprops/action-gh-release@v2` create release 时**不**传 body（保持空 body），然后 windows-amd64 job 内：

1. Step A `inject CHANGELOG to release body`：从 `CHANGELOG.md` 提取 `[v0.X.Y]` 段（H2 标题 + 后续内容，到下一个 H2 `## [` 之前），用 `gh release edit --notes "$CHANGELOG_SECTION"` 写入
2. Step B `append windows zip SHA256 to release body`：读当前 body，append `\n\n<!-- sha256:cc-launcher-windows-amd64.zip:${HASH} -->`

**理由**：A 跑在 B 之前保证最终 body = `CHANGELOG 段 + 空行 + hash marker`；Step B 用现有 `gh release edit --notes "$NEW_BODY"` pattern 自动保留 Step A 写入的内容。

### Decision 2：CHANGELOG 段提取用 python 不用 sed

`CHANGELOG.md` 是多行 markdown，段边界是 H2 `## [version]`，子段是 H3 `###`。跨行 regex 用 python `re.search(r'## \[0\.X\.Y\][^#]*?(?=## \[|\Z)', body, re.DOTALL)` 干净——避免 sed 多行处理陷阱（`-z` flag 在 bash 里不直观）。

`workflows/release.yml` windows job 已经有 `setup-go` + checkout，python3 是 GitHub runner 默认有（`ubuntu-latest` image 自带）。不引入新依赖。

### Decision 3：marker 格式 `<!-- sha256:cc-launcher-windows-amd64.zip:HEX -->`

`auto-hash` 替换为真实文件名（`cc-launcher-windows-amd64.zip`），符合用户"加文件名"诉求。多个 hash 时不同文件名前缀区分（虽然目前只 append 一个 windows hash，未来可能多平台 hash）。

HTML 注释保留——避免 markdown 渲染把 hash 字符串染上反引号 / 代码块等排版，也避免 user 看到 "sha256:auto-hash" 这种内部约定名词。

### Decision 4：build.sh regex 通用化 `sha256:[^:]+:\K[a-f0-9]{64}`

`[^:]+` 匹配 `auto-hash` / `cc-launcher-windows-amd64.zip` / 未来任何文件名（不绑死具体值）。

**理由**：
- 向前兼容旧 `<!-- sha256:auto-hash:HEX -->` marker（v0.12.0 ~ v0.13.2 release 都还能 parse）
- 新 `<!-- sha256:cc-launcher-windows-amd64.zip:HEX -->` marker 也能 parse
- 如果未来要 append 多文件 hash（linux / darwin 等），regex 改一次即可
- Python 端 regex 同步改

## Risks / Trade-offs

- [CHANGELOG 段提取 regex 边界 bug——`## [0.X.Y]` 段不匹配怎么办] → 兜底：python 段找不到对应 version 标签时打 stderr warning 仍 inject 空 body（= 等同不 changelog 注入），不 fail workflow；**实际** version tag 与 `GITHUB_REF_NAME` 必然一致（tag 触发 workflow），fail 概率极低
- [CHANGELOG.md 末尾没有 `## [next]` 分隔时 regex `(?=## \[|\Z)` 用 `\Z` 兜底] → 已有
- [CHANGELOG 内容含 `<!--` HTML 注释是否影响 marker 提取] → 不影响：marker 提取 regex 仍按 `<!-- sha256:[^:]+:[a-f0-9]{64} -->` literal match
- [GH release API rate limit] → 现状已有 3 retries × 5s，本 change 不改
- [CHANGELOG 段太长，超过 65535 char markdown body 限制] → GH release body limit 实际是 125,000 字符，CHANGELOG 段远不达；不加 length check

## Migration Plan

无需迁移：
- 无 state.json / settings.json 变化
- 无 CLI 标志变化
- 无 release asset 字节变化
- `bucket/cc-launcher.json` 解析流程不变（regex 兼容旧 marker）
- 灰度：release workflow 单 PR 改 windows job 内 2 step（新增 + 改名），build.sh 单行 regex 改；release workflow 跑通即上线

## Open Questions

无。
