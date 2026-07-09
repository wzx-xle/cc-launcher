# Claude Code Launcher — 设计文档

## 概述

一个终端启动器，在启动 Claude Code 时提供 AI 模型提供商配置切换和权限模式选择功能。

## 命令接口

### 入口

`ccl` 命令，通过 shell wrapper（别名/符号链接/PATH）指向 Go 编译的二进制 `cc-launcher`。

### 命令结构

```
ccl                              → 完整 TUI: 选提供商 → 选模式 → 启动 Claude Code

ccl provider                     → 列出所有提供商（标记当前激活的）
ccl provider <name>              → 切换到指定提供商，更新 state.json
ccl provider --new               → 交互式新增提供商（询问 name、base_url、api_key）
ccl provider --new <name>        → 直接新增并进入配置填写
ccl provider --list              → 列出所有提供商
ccl provider --delete <name>     → 删除指定提供商（当前激活的需二次确认）

ccl mode                         → 列出 6 种权限模式（标记当前目录默认模式）
ccl mode <name>                  → 设置当前目录的默认权限模式，更新 state.json
```

全局选项：`-h` / `--help`，`-v` / `--version`。

### 权限模式（6 种，与 Claude Code 原生一致）

| 模式 | `--permission-mode` 参数 |
|------|--------------------------|
| manual | `manual` |
| acceptEdits | `acceptEdits` |
| auto | `auto` |
| bypassPermissions | `bypassPermissions` |
| dontAsk | `dontAsk` |
| plan | `plan` |

## 文件结构

### 运行时数据目录：`~/.cc-launcher/`

```
~/.cc-launcher/
├── providers/                    # 提供商配置（一个文件一个提供商）
│   ├── anthropic.json
│   ├── bedrock.json
│   └── vertex.json
├── backups/                      # settings.json 写前备份
│   ├── settings-2026-07-09T15-04-05.json
│   └── settings-2026-07-08T10-30-12.json
└── state.json                    # 运行时状态
```

### 提供商配置文件

与 Claude Code `~/.claude/settings.json` 结构一致，支持所有字段（`env`、`model`、`modelOverrides`、`availableModels` 等），不限于 `env`。

示例：
```jsonc
// ~/.cc-launcher/providers/bedrock.json
{
  "env": {
    "CLAUDE_CODE_USE_BEDROCK": "1",
    "AWS_REGION": "us-east-1",
    "CLAUDE_CODE_AWS_PROFILE": "my-profile"
  },
  "model": "sonnet",
  "modelOverrides": {
    "claude-opus-4-8": "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-opus-4-8-v1:0"
  }
}
```

`--new` 创建的提供商仅包含基础字段（`ANTHROPIC_BASE_URL`、`ANTHROPIC_API_KEY`），其他字段用户手动编辑 JSON 补充。

### 状态文件

```jsonc
// ~/.cc-launcher/state.json
{
  "active_provider": "bedrock",
  "injected_snapshot": {
    "env": {
      "CLAUDE_CODE_USE_BEDROCK": "1",
      "AWS_REGION": "us-east-1",
      "CLAUDE_CODE_AWS_PROFILE": "my-profile"
    },
    "model": "sonnet",
    "modelOverrides": {
      "claude-opus-4-8": "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-opus-4-8-v1:0"
    }
  },
  "last_permission_modes": {
    "/home/zxwang/workspace/golang/cc-launcher": "plan",
    "/home/zxwang/workspace/study": "auto"
  }
}
```

## 核心逻辑

### 目标配置文件

**只操作 `~/.claude/settings.json` 的任意字段，不碰 `~/.claude.json`。**

### 完整 TUI 启动流程（`ccl`）

```
1. 加载 ~/.cc-launcher/providers/*.json
      ├─ 目录为空/不存在 → TUI 显示"暂无提供商配置"，Esc 退出
      ├─ JSON 解析失败 → 列表标记 ⚠️，跳过该项
      └─ 正常 → 进入列表

2. [TUI] 选择提供商
      ├─ 默认光标停在 active_provider（来自 state.json）
      ├─ ↑↓ 移动，Enter 确认，Esc/Ctrl+C 退出
      └─ 选择与 active_provider 相同 → 跳过合并，直接进入权限选择
          选择与 active_provider 不同 → 执行合并

3. 合并配置（仅提供商变更时执行）
      ├─ 备份 settings.json 到 ~/.cc-launcher/backups/，保留最近 10 个
      ├─ 递归比对 injected_snapshot 与 settings.json 当前值：
      │    当前值 == 快照值 → 删除（未被用户修改）
      │    当前值 != 快照值 → 保留（用户手动改过）
      │    快照有、settings.json 无 → 跳过
      ├─ 将新提供商配置写入 settings.json（覆盖对应字段）
      └─ 更新 state.json（injected_snapshot + active_provider）

4. [TUI] 选择权限模式
      ├─ 读取 last_permission_modes[当前目录]
      ├─ 有记录 → 默认光标停在上次模式
      │  无记录 → 默认停在 manual
      ├─ ↑↓ 移动，Enter 确认，Esc 返回提供商选择
      └─ 确认后更新 last_permission_modes[当前目录]

5. 启动 Claude Code
      ├─ 检查 claude 命令是否存在 → 不存在则提示并退出
      ├─ exec claude --permission-mode <mode>
      └─ exec 失败 → 打印 stderr 并退出
```

### 配置合并 — 错误处理

| 场景 | 处理 |
|------|------|
| `providers/` 目录为空 | TUI 提示，退出 |
| 单个提供商 JSON 解析失败 | 列表标记 ⚠️ 跳过，其他正常可用 |
| `settings.json` 不存在 | 视为 `{}`，首次写入 |
| `settings.json` 写入权限不足 | 提示路径和权限问题，退出 |
| `state.json` 损坏 | 视为首次运行，合并走全量覆盖 |
| 磁盘满 / IO 错误 | 捕获打印具体原因，不启动 claude |

通用原则：fail fast，不留下半脏状态，错误信息打印到 stderr。

### `ccl provider` 子命令行为

```
ccl provider --list              → 打印列表，▶ 标记当前激活的
ccl provider <name>              → 切换 active_provider，更新 state.json
ccl provider --new               → 交互式：name → base_url → api_key → 写入 JSON
ccl provider --new <name>        → 直接进入 base_url → api_key → 写入
ccl provider --delete <name>     → 删除 providers/<name>.json
                                  ├─ 非激活提供商 → 直接删除
                                  └─ 激活的提供商 → 二次确认 ⚠️
```

`ccl provider`（无参数）等价于 `ccl provider --list`。

### `ccl mode` 子命令行为

```
ccl mode                         → 打印 6 种模式，▶ 标记当前目录默认
ccl mode <name>                  → 更新 last_permission_modes[当前目录]
                                  └─ 非法值 → 提示合法值列表
```

## TUI 设计（bubbletea）

### 提供商选择界面

```
┌──────────────────────────────────────────┐
│  Claude Code Launcher                    │
│                                          │
│  选择提供商:                              │
│                                          │
│    Anthropic API                         │
│  ▶ AWS Bedrock           ← 上次使用      │
│    Google Vertex AI                      │
│                                          │
│  ─────────────────────────────────────── │
│  Enter 确认  Esc 退出  ↑↓ 移动          │
└──────────────────────────────────────────┘
```

### 权限模式选择界面

```
┌──────────────────────────────────────────────┐
│  Claude Code Launcher                        │
│                                              │
│  提供商: Anthropic API                        │
│                                              │
│  选择权限模式:                                │
│    manual             — 每次操作询问确认      │
│    acceptEdits        — 自动接受文件编辑      │
│    auto               — 自动执行非破坏性操作  │
│    bypassPermissions  — 跳过所有权限检查      │
│    dontAsk            — 不询问，直接执行      │
│  ▶ plan               — 只读，仅分析不执行    │
│                                              │
│  ─────────────────────────────────────────── │
│  Enter 确认  Esc 返回  ↑↓ 移动              │
└──────────────────────────────────────────────┘
```

### 启动过渡

```
正在切换提供商配置... ✓
正在启动 Claude Code (Anthropic API / plan)...
```

确认后打印状态行，立即 `exec` 替换当前进程。

## 项目结构

```
cc-launcher/
├── cmd/
│   └── cc-launcher/
│       └── main.go              # 入口，子命令路由
├── internal/
│   ├── config/
│   │   └── config.go            # settings.json 读/写/合并/备份
│   ├── provider/
│   │   └── provider.go          # 提供商 CRUD
│   ├── state/
│   │   └── state.go             # state.json 管理
│   ├── tui/
│   │   └── tui.go               # bubbletea 两个选择界面
│   └── launcher/
│       └── launcher.go          # exec Claude Code
├── go.mod
├── go.sum
└── CLAUDE.md
```

## 技术选型

| 维度 | 决策 |
|------|------|
| 语言 | Go |
| TUI 框架 | [bubbletea](https://github.com/charmbracelet/bubbletea) + [bubbles](https://github.com/charmbracelet/bubbles)（list 组件）+ [lipgloss](https://github.com/charmbracelet/lipgloss)（样式） |
| CLI 框架 | 标准库 `flag` 或 [cobra](https://github.com/spf13/cobra)（子命令较多时） |

## 非功能约束

- 提供商配置文件结构必须与 Claude Code settings.json 结构一致
- 切换提供商时：原提供商配置不残留，用户手动修改的配置不丢失
- 每步操作可取消（Esc 退出）
- `exec` 替换进程，不残留后台进程
