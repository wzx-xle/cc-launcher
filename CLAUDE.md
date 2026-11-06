# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

Claude Code Launcher — 一个终端启动器，在启动 Claude Code 时提供提供商配置切换和权限模式选择功能。

## 架构约束（来自 requirement.md）

1. 通过 `ccl` 命令启动 `cc`（Claude Code）
2. 提供商配置结构必须与 Claude Code 自身配置结构一致（方便增减配置）
3. 切换提供商时：**提供商配置要覆盖**到 Claude Code 配置中
4. 切换提供商时：**原提供商配置不能残留**，但 **Claude Code 自身添加的配置要保留**
5. 流程：选择提供商 → 选择权限模式 → 打开 Claude Code，每一步都可取消
6. **所有变量传递统一走 Claude Code 的 `--settings` 参数**（provider-env-via-settings change 起）：
   - provider 配置通过 `--settings` 的 `env` 块交付，**ccl 配置的优先级高于用户在 settings.json 里的同名值**（配了独立提供商就一定用它）；进程环境里另有一份低优先级副本（daemon 的转发要用用户的代理设置）——**那份副本不是 provider 生效的保障**（settings.json 的 env 块压过进程环境）
   - **provider 的顶层键（`model`/`permissions`/`hooks` …）也一起交付**（provider-settings-passthrough change 起）；`note`/`order` 是 ccl 自己的元数据，不交付；列表键取并集
   - **交付前必须过 schema 校验**（`internal/settingsschema`）：已知键类型写错会让 Claude Code 丢弃**整个** settings 文件（provider 凭证一起消失）→ 校验不过就**拒绝启动**，不静默退化
   - **`model` 键要额外作为 `ANTHROPIC_MODEL` 放进交付的 env 块**（`ANTHROPIC_MODEL` 环境变量压过任何文件里的 `model` 键，而 settings.json 的 env 块又压过进程环境 → env 块是唯一同时压过两者的通道）
   - Claude Code settings.json 不再保留 provider 字段；老 ccl 注入的 ANTHROPIC_* 由 migrate 函数首次启动时自动清理
   - `--settings` 指向 `~/.cc-launcher/cache/` 下**内容哈希命名**的常驻文件（argv 里没有明文凭证；同内容复用、同名即同内容 → 并发启动各读各的）
   - 外部行为（合并语义、优先级、校验粒度）的实测结论见 `docs/claude-code-settings-merge-semantics.md`——改交付逻辑前先看它，Claude Code 升级后要重验
   - **`CCL_PROVIDER` 是 ccl 注入给消费方的只读变量**（provider-name-in-statusline change 起）：值 = 本次选中的 provider 名，走同一条 `--settings` env 块通道 + 进程环境冗余一份；它**与 provider 的生效链路无关**（生效靠 provider 自己的 env 块），只负责把名字告诉仓库外的程序（如状态栏脚本）。变量名与值语义都是对外契约，改名 = breaking change；名字不能带凭证特征（`TOKEN`/`KEY`/`SECRET`）——Claude Code 的 subprocess scrub 会把凭证类变量从子进程环境删掉。完整契约见 `docs/provider-name-env.md`
7. **额度查询（provider-quota change 起）**：
   - `quota` 是 ccl 自己的 provider 元数据键（与 `note`/`order` 同等），**不透传**给 Claude Code——必须留在 `provider.ReservedKeys` 里
   - **额度刷新由 `ccl serve` 负责**（常驻进程，周期默认 60 秒、per-provider 可配）：serve 写 `~/.cc-launcher/quota.json`（0600、原子写）；`ccl quota` **只读本地文件，一次网络都不发**（状态栏命令约每 300ms 调一次，读取侧不能有网络）
   - **额度接口是外部行为**：厂商改了字段/端点 → 解析失败必须**降级而不是崩**（serve 保留旧条目 → `fetched_at` 变旧 → stale 自动生效；读取侧不受影响；绝不允许影响 Claude Code 启动）。实测结论见 `docs/provider-quota.md`——改 adapter 前先看它
   - 额度输出是**维度列表**（`window`/`quota`/`balance` 三种 kind），不是两个固定槽位：`label`/`unit` 必须纯 ASCII 且**永远不取自厂商响应的原始字符串**
   - **刷新不得"同一时刻打一批"**（quota-refresh-stagger change）：额度按 key 分，N 个 provider = N 个额度池 = N 个请求。同一刷新周期的一批池必须**各占一个固定相位**（周期长度不变，只是时刻错开），一轮内相邻请求还要留最小间隔；**serve 的 ticker 周期必须细于相位分辨率**（否则窗口边界一次性全到期 → 错开白做）
   - **只刷新"最近有活动"的 provider**（quota-active-providers-only change）：额度数据的唯一消费者是状态栏，而状态栏显示的是**当前会话那个 provider** 的额度 → 其余 provider 的额度数据**当前没人看**，查它们 = 纯浪费 + 触发厂商限流。活动信号取自 **OTel**（`ccl.provider` resource attribute → collector 解析 metrics 时就知道是哪个 provider；实测 Claude Code 每 60 秒推一次 → 窗口 5 分钟）。**"本轮不刷它" ≠ "provider 被删了"**：`Prune` 的 live 集合必须用**全部** provider，不能用过滤后的
   - **被厂商限流（429）必须退避**，不能每轮硬打：退避时长指数增长（1min→2min→…→封顶 30min）且**写进数据文件**（跨 serve / `--refresh` / 重启都生效）；退避期间条目保留（stale 自动生效），`--refresh` 也要遵守退避。退避**按额度池**记录（限流针对 key，provider 换 key = 换池 → 不该被老退避拦住）
8. **改函数签名 / 删函数 / 跨包重构前，用 LSP `findReferences` 确认影响面**，不用 `grep -rn`
   - grep 是文本匹配，会把同名方法一并算进来：改 `provider.Manager.Get` 时，`grep "\.Get("` 会把 `internal/web/server.go` 里 7 处 `url.Values.Get` 甩出来，误判成要动一片无关代码；LSP 是符号级匹配，能精确区分
   - LSP 工具已启用（`ENABLE_LSP_TOOL=1` + `gopls-lsp` 插件），但它是 deferred 的——先用 `ToolSearch` 加载 schema 再调

## OpenSpec 工作流

项目使用 OpenSpec 规范驱动开发（`spec-driven` schema），配置文件在 `openspec/config.yaml`。所有功能开发应遵循 OpenSpec 的 proposal → design → specs → tasks 流程。

## Release Tag 流程

**强约束**：打 tag 之前必须先把 `CHANGELOG.md` 该版本的变更记录写好；CHANGELOG commit 与 tag 应在**同一次推送**内完成，不能"今天 commit changelog、明天打 tag"——否则 `git checkout v0.X.Y` 拿到的代码没有对应 CHANGELOG 段。

操作顺序：

1. 完成 OpenSpec change（实施 + `openspec archive` 归档）
2. 在 `CHANGELOG.md` 的 `[Unreleased]` 段下加新版本段（按既有版本格式：Refactor / Features / Fix / Tests / OpenSpec / Compatibility 等分类）
3. `git commit -m "docs: CHANGELOG 加 v0.X.Y <change-name> 段"`（CHANGELOG commit）
4. `git tag -a v0.X.Y HEAD -m "<release message>"`（tag 指向 CHANGELOG commit）
5. 一次推送：`git push origin main --follow-tags`（commit + tag 一起推）

如果历史 tag 指向了不带 changelog 的 commit，修正方法是 `git tag -d v0.X.Y` + `git push origin :refs/tags/v0.X.Y` + 在正确的 commit 上重打 tag + `git push origin v0.X.Y`，**不动 commit history**。
