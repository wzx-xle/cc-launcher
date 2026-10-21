# release 流程：改完之后怎么冒烟测试

> 背景：2026-10-21 一次发版连续踩了三个 release workflow 的 bug
> （job 间竞态 / CHANGELOG 提取正则 / 未认证 API 限流），每一个都要"推 tag →
> 现场炸 → 手工修 release"。根因不是运气差，而是**release 逻辑全部埋在
> YAML 的 inline python/shell 里，零测试覆盖**，且**仓库 PR 阶段零 CI**。

这份文档回答：改完 release 相关的东西，**怎么在推 tag 之前验证**。

## 三层验证，从便宜到贵

### 第 1 层：本地跑单测（秒级，改一行就跑一次）

release 的解析逻辑抽在 `internal/release` 里，有单测：

```bash
go test ./internal/release/... -v      # CHANGELOG 段提取 / marker 解析
go test ./...                           # 顺带跑全仓
```

覆盖的坑（每个都是 2026-10-21 实际踩到的）：

| 坑 | 覆盖它的测试 |
|---|---|
| 版本号带不带 `v`（`v0.14.0` vs `## [0.14.0]`） | `TestExtractChangelogSection` |
| 正则缺行首锚点 → 命中正文里的字面量 | `TestExtractChangelogSection_IgnoresInlineLiteral` |
| CHANGELOG 写法与提取器约定漂移 | `TestExtractChangelogSection_RepoChangelog`（拿仓库真实 CHANGELOG 自检） |
| marker 格式老/新两种 | `TestExtractSHA256Marker` |

**改了 `internal/release/` 或 `CHANGELOG.md` 的标题写法 → 必须跑这一层。**

### 第 2 层：PR 期 CI（自动，1~2 分钟）

`.github/workflows/ci.yml` 在 push 到 main / 提 PR 时自动跑：

- `go build` / `go vet` / `go test ./...` / `go test -tags smoke ./...`
- `actionlint` 检查 `.github/workflows/release.yml` 语法
- **用仓库真实 `CHANGELOG.md` 跑一遍提取器**（自检哨兵）
- `cmd/release-notes` CLI 自检

**改了 workflow 文件 / release 逻辑 → 提 PR 看这条流水线绿了再合。**

### 第 3 层：workflow 改动本身的 dry-run（不产生任何 release）

`release.yml` 的 `workflow_dispatch` 现在有真能用的 dry-run：

```bash
# GitHub UI: Actions → release → Run workflow → 选分支 → dry_run 保持勾选
# 或命令行:
gh workflow run release.yml --ref main -f dry_run=true
```

dry-run 会：完整构建 + 打包 + 算 zip hash + 提取 CHANGELOG 段，**全部打印出来**，
但**不创建 release、不上传资产、不改任何 release body**。

适合验证：打包流程没坏、CHANGELOG 段能提取出来、hash 能算出来。

### 第 4 层：临时 tag 全链路烟测（会产生真实 release，用完即删）

dry-run 覆盖不到 `gh release create/edit/upload` 这段（需要真实 tag + 真实
release）。要测这一段时用临时 tag：

```bash
SHA=$(git rev-parse --short=8 HEAD)
git tag "v0.0.0-ci.${SHA}"          # 临时 tag
git push origin "v0.0.0-ci.${SHA}"  # 触发真实 workflow
# 等 workflow 跑完，检查：
gh release view "v0.0.0-ci.${SHA}" --repo wzx-xle/cc-launcher --json body,assets
# 验完清理：
gh release delete "v0.0.0-ci.${SHA}" --repo wzx-xle/cc-launcher --yes
git push origin --delete "v0.0.0-ci.${SHA}"
```

**注意**：临时 tag 会真的建 release、真的上传 ~30 MB 资产，验完必须删。

## 什么时候必须做哪一层

| 改动 | 最少要做 |
|---|---|
| `internal/release/` 的逻辑 | 第 1 层 |
| `CHANGELOG.md` 的标题写法 | 第 1 层 + 第 2 层（自检哨兵会红） |
| `.github/workflows/release.yml` | 第 2 层（actionlint）+ 第 3 层 |
| release 流程整体（含 gh API 调用） | 第 4 层 |
| 业务代码（`internal/collector`、`internal/stats` …） | 第 2 层（CI 自动跑） |

## 已知不做的事

- **没有对 `dist/` 产物做端到端断言**（zip 内容、shim 可用性）——目前靠人工下载试。
- **没有对 `gh release edit` 的 body 最终形态做快照测试**——body 由 3 个步骤拼成，
  靠 dry-run 打印 + 人工核对。
- **没有测试 `softprops/action-gh-release` 的 body 保留行为**——依赖第三方 action
  的实现细节，只能靠第 4 层真跑验证。
- **`internal/cli` 的权限模式契约测试在 CI 上恒 skip**：它要真的调 `claude --help`
  比对 choices，没装 Claude Code 的 runner 上 `t.Skip`。这条只能在本机 / 有
  Claude Code 的环境跑（`go test ./internal/cli/ -run TestContract -v`）。
