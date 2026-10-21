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

**注意两点**：
1. 临时 tag 会真的建 release、真的上传 ~30 MB 资产，验完必须删。
2. 临时 tag（`v0.0.0-ci.xxx`）在 CHANGELOG.md 里没有对应段 → inject 步骤会
   fail。用 `version` input 指定一个 CHANGELOG 里存在的版本号，让整条链路走绿：

   ```bash
   gh workflow run release.yml --ref "v0.0.0-ci.${SHA}" -f dry_run=false -f version=v0.14.0
   ```

   （`version` 只影响"从 CHANGELOG 里取哪一段"，release 本身仍然建在那个 tag 上。）

## 什么时候必须做哪一层

| 改动 | 最少要做 |
|---|---|
| `internal/release/` 的逻辑 | 第 1 层 |
| `CHANGELOG.md` 的标题写法 | 第 1 层 + 第 2 层（自检哨兵会红） |
| `.github/workflows/release.yml` | 第 2 层（actionlint）+ 第 3 层 |
| release 流程整体（含 gh API 调用） | 第 4 层 |
| 业务代码（`internal/collector`、`internal/stats` …） | 第 2 层（CI 自动跑） |

## 执行环境矩阵（"本机过、CI 挂"的坑都在这张表里）

| | 开发者本机 | CI runner (ubuntu-latest) | 终端用户机器 |
|---|---|---|---|
| 时区 | Asia/Shanghai (+08) | **UTC** | 任意 |
| `claude` CLI | 装了（`~/.nvm/.../claude`） | **没有** | 大概率有 |
| HOME | `/home/zxwang`（有**真实** `~/.cc-launcher/telemetry.db`） | `/home/runner`（干净） | 有真实数据 |
| GitHub API | 共享出口 IP → **未认证 60/hr 限流常被打满** | `GITHUB_TOKEN` 认证（5000/hr） | 同左 |
| 工具链 | go + gh + scoop… | go + gh（无登录态） | 未必有 go |

**由此产生的四条硬规则**（写测试 / 写脚本时照着做）：

1. **测试不得依赖"本机恰好装了某程序"** —— 要用就造假的。
   例：`internal/launcher` 的 env 转发测试 → 临时目录里放一个假 `claude` 放进 PATH
   （`withFakeClaude`）。2026-10-21 首次接 CI 时就是这条被违反，CI 立刻红。
2. **测试不得依赖真实 HOME 里的用户数据** —— 用 `t.TempDir()`；
   涉及 `HOME/.cc-launcher` 的用 `sandboxHome(t)`（cmd 层）或直接传临时 db 路径
   （internal 层）。
3. **时区敏感逻辑必须多时区验证** —— CI 跑 UTC（runner 默认）+ Asia/Shanghai
   （用户主时区）+ America/New_York（夏令时边界，只跑 internal/stats）。
   本地复现：`TZ=America/New_York go test ./internal/stats/...`
4. **调 GitHub API 必须带 token** —— 未认证额度 60/hr 且本机出口 IP 共享，
   很容易被打满；用 `gh auth token`（见 [[github-api-needs-auth-token]]）。

本地想复现 CI 的环境（一次性）：

```bash
TMPhome=$(mktemp -d)
env -u TZ -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_BASE_URL \
    HOME="$TMPhome" \
    PATH="$(echo "$PATH" | tr ':' '\n' | grep -v "$(dirname "$(which claude)")" | paste -sd:)" \
    go test -count=1 ./...
rm -rf "$TMPhome"
```

## 已知不做的事

- **没有对 `dist/` 产物做端到端断言**（zip 内容、shim 可用性）——目前靠人工下载试。
- **没有对 `gh release edit` 的 body 最终形态做快照测试**——body 由 3 个步骤拼成，
  靠 dry-run 打印 + 人工核对。
- **没有测试 `softprops/action-gh-release` 的 body 保留行为**——依赖第三方 action
  的实现细节，只能靠第 4 层真跑验证。
- **`internal/cli` 的权限模式契约测试在 CI 上恒 skip**：它要真的调 `claude --help`
  比对 choices，没装 Claude Code 的 runner 上 `t.Skip`。这条只能在本机 / 有
  Claude Code 的环境跑（`go test ./internal/cli/ -run TestContract -v`）。
