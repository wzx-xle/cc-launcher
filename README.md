# Claude Code Launcher

一个终端 TUI 启动器，在启动 Claude Code 时切换 AI 提供商配置和权限模式。

## 为什么需要

直接使用 Claude Code 时，切换提供商（Anthropic / Bedrock / Vertex 等）需要手动修改 `~/.claude/settings.json`，容易残留旧配置。本工具提供了：

- 一键切换提供商，**旧配置精准清理，用户手动修改不丢失**
- 按项目记住权限模式偏好
- 写前自动备份 settings.json

## 安装

```bash
# 编译 + 安装到 /usr/local/bin（需要 sudo）
./build.sh install

# 或安装到 ~/.local/bin（无需 sudo）
./build.sh local

# 仅编译
./build.sh build
```

之后直接使用 `ccl` 命令即可（`~/.local/bin` 需要加入 PATH）。

## 用法

| 命令 | 说明 |
|------|------|
| `ccl` | TUI 启动流程：选提供商 → 选权限模式 → 启动 Claude Code |
| `ccl provider --list` | 列出所有提供商，`▶` 标记当前激活的 |
| `ccl provider <name>` | 切换到指定提供商 |
| `ccl provider --new` | 交互式新增提供商（name、base_url、auth_token） |
| `ccl provider --new <name>` | 新增提供商并进入配置填写 |
| `ccl provider --delete <name>` | 删除提供商（激活的需二次确认） |
| `ccl mode` | 列出 6 种权限模式，`▶` 标记当前目录默认 |
| `ccl mode <name>` | 设置当前目录的默认权限模式 |

## 提供商配置

配置文件存放在 `~/.cc-launcher/providers/`，一个文件一个提供商，结构与 Claude Code 的 `settings.json` 一致：

```jsonc
// ~/.cc-launcher/providers/anthropic.json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-ant-xxx",
    "ANTHROPIC_MODEL": "sonnet"
  }
}

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

## 权限模式

| 模式 | 说明 | CLI 参数 |
|------|------|----------|
| manual | 每次操作询问确认 | `--permission-mode manual` |
| acceptEdits | 自动接受文件编辑 | `--permission-mode acceptEdits` |
| auto | 自动执行非破坏性操作 | `--permission-mode auto` |
| bypassPermissions | 跳过所有权限检查 | `--permission-mode bypassPermissions` |
| dontAsk | 不询问，直接执行 | `--permission-mode dontAsk` |
| plan | 只读，仅分析不执行 | `--permission-mode plan` |

## 配置合并机制

切换提供商时，框架执行以下步骤：

1. **备份** — 当前 settings.json 写入 `~/.cc-launcher/backups/`（保留最近 10 个）
2. **清理旧配置** — 按快照比对，删除未被用户修改的旧提供商字段
3. **注入新配置** — 写入新提供商的所有字段
4. **更新快照** — 记录本次注入的字段，供下次切换时比对

不会被删除的：不在快照中的字段（用户手动加的）、值与快照不同的字段（用户改过的）。

## 目录结构

```
~/.cc-launcher/
├── providers/              # 提供商配置（一个 JSON 一个提供商）
│   ├── anthropic.json
│   └── bedrock.json
├── backups/                # settings.json 写前备份
│   └── settings-2026-07-09T15-04-05.json
└── state.json              # 运行时状态（激活提供商、权限偏好）
```

## 开发

```bash
# 运行测试
./build.sh test

# 编译
./build.sh build

# 编译并运行（带参数）
./build.sh run
```
