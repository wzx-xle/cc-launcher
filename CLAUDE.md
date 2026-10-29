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
   - provider 配置通过 `--settings` 的 `env` 块交付，**ccl 配置的优先级高于用户在 settings.json 里的同名值**（配了独立提供商就一定用它）；进程环境里另有一份低优先级副本（daemon 的转发要用用户的代理设置）
   - Claude Code settings.json 不再保留 provider 字段；老 ccl 注入的 ANTHROPIC_* 由 migrate 函数首次启动时自动清理
   - `--settings` 指向 `~/.cc-launcher/cache/` 下**内容哈希命名**的常驻文件（argv 里没有明文凭证；同内容复用、同名即同内容 → 并发启动各读各的）

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
