# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

Claude Code Lancher — 一个终端启动器，在启动 Claude Code 时提供提供商配置切换和权限模式选择功能。

## 技术选型（待决策）

需求要求评估 **Golang** 还是 **Bash** 实现。选型考量：
- Go：适合复杂状态管理（配置覆盖/还原），跨平台好，可测试性强
- Bash：零依赖，与终端生态天然契合，但复杂逻辑维护成本高

## 架构约束（来自 requirement.md）

1. 通过 `cc` 命令启动
2. 提供商配置结构必须与 Claude Code 自身配置结构一致（方便增减配置）
3. 切换提供商时：**提供商配置要覆盖**到 Claude Code 配置中
4. 切换提供商时：**原提供商配置不能残留**，但 **Claude Code 自身添加的配置要保留**
5. 流程：选择提供商 → 选择权限模式 → 打开 Claude Code，每一步都可取消

## OpenSpec 工作流

项目使用 OpenSpec 规范驱动开发（`spec-driven` schema），配置文件在 `openspec/config.yaml`。所有功能开发应遵循 OpenSpec 的 proposal → design → specs → tasks 流程。

## 项目启动前注意事项

- 项目名 "Lancher" 应为 "Launcher"（拼写错误），建议在正式开发前修正
- 当前仓库无任何代码，`go.mod`、目录结构等均待初始化
