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
| `ccl` / `ccl --help` | TUI 启动流程：选提供商 → 选权限模式 → 启动 Claude Code |
| `ccl provider --help` | provider 子命令帮助 |
| `ccl mode --help` | mode 子命令帮助 |
| `ccl serve --help` | OTel daemon 帮助（启动 / `--stop` / `--restore` / `--status`） |
| `ccl stats --help` | stats 子命令帮助 |
| `ccl provider --list` | 列出所有提供商，`▶` 标记当前激活的 |
| `ccl provider <name>` | 切换到指定提供商 |
| `ccl provider --template` / `-T` | 列出 7 种支持的提供商模板 |
| `ccl provider --template <type>` | 打印指定 type 的 JSON 模板（如 `--template deepseek`） |
| `ccl provider --new` | 交互式新增提供商（选模板 → 按字段填值） |
| `ccl provider --new --type <type>` | 用指定模板引导式新建（跳过选模板步骤） |
| `ccl provider --delete <name>` | 删除提供商（激活的需二次确认） |
| `ccl mode` | 列出 6 种权限模式，`▶` 标记当前目录默认 |
| `ccl mode <name>` | 设置当前目录的默认权限模式 |
| `ccl serve --stop` | 优雅停 OTel daemon（5s 内 graceful shutdown） |
| `ccl serve --restore` | 还原 `~/.claude/settings.json` 到 ccl 之前的状态 |
| `ccl serve --status` | 打印 daemon 状态摘要（pid / 端口可达 / db 大小 / session 数） |

## Tab completion

ccl 内置 shell tab completion——按 `<Tab>` 自动补全子命令 / flag / 模板名 / 权限模式名。一次性启用：

**bash**：在 `~/.bashrc` 加一行

```bash
source <(ccl __init bash)
```

**zsh**：在 `~/.zshrc` 加一行

```zsh
source <(ccl __init zsh)
```

**powershell**：在 `$PROFILE` 加一行

```powershell
Invoke-Expression -Command (ccl __init powershell)
```

或管道方式（仅当前 session）：

```powershell
ccl __init powershell | Out-String | Invoke-Expression
```

启用后重启 shell 或 `source ~/.bashrc` / `source ~/.zshrc` 即生效。powershell 需重开 PowerShell 窗口。`fish` 当前不支持（已规划）。

> PowerShell completion 用 `String.StartsWith` 做字面前缀过滤，不用 `-like` 通配符匹配——避免 `ccl proj*<TAB>` 这类通配 token 膨胀候选列表。

补全范围（bash / zsh / powershell 三 shell 一致）：

- 顶层 `ccl <TAB>` → `serve / stats / provider / mode / -h / --help / help`
- `ccl serve <TAB>` → `--stop / --restore / --status / --restart`（互斥）
- `ccl stats <TAB>` → `--day / --by-provider / --by-project / --project / --json`
- `ccl provider --template <TAB>` / `-T <TAB>` → 7 种模板名（`anthropic / deepseek / minimax / glm / aliyun-coding / aliyun-pay / aliyun-token`）
- `ccl provider --new --type <TAB>` → 同上模板名
- `ccl mode <TAB>` → 6 种权限模式名
- 用户私有 provider 文件名由 shell 文件名补全处理（ccl 不补位置参数）

## 提供商配置

配置文件存放在 `~/.cc-launcher/providers/`，一个文件一个提供商，结构与 Claude Code 的 `settings.json` 一致。

### 用模板配置（推荐）

```bash
# 看有哪些模板
ccl provider --template
# 7 种：anthropic, deepseek, minimax, glm, aliyun-coding, aliyun-pay, aliyun-token

# 看某个模板的字段（无需联网、可被 jq 处理）
ccl provider --template deepseek

# 引导式填值，按提示回答即可
ccl provider --new --type deepseek my-deepseek
```

每个 type 都已校对官方原页（`internal/templates/templates.go` 注释里有 `// verified 2026-09-27: <URL>`）：

| Type | base_url | 默认模型 | 备注 |
|------|---------|---------|------|
| `anthropic` | （官方） | （官方） | 2 字段 |
| `deepseek` | `api.deepseek.com/anthropic` | `deepseek-flash[1m]` | 7 字段 |
| `minimax` | `api.minimax.cn/anthropic`（国内）/ `api.minimax.io/anthropic`（国际） | `MiniMax-M3[1m]` | 6 字段；交互流会让用户选 cn/io |
| `glm` | `open.bigmodel.cn/api/anthropic` | HAIKU=`glm-5.3-flash[1m]`, SONNET/OPUS=`glm-5.3[1m]` | 8 字段（智谱 Coding Plan） |
| `aliyun-coding` | `coding.dashscope.aliyuncs.com/apps/anthropic` | `qwen3.7-plus` | 6 字段 |
| `aliyun-pay` | `{WorkspaceId}.cn-beijing.maas.aliyuncs.com/apps/anthropic` | `qwen3.7-max` | 7 字段；交互流会让用户填 WorkspaceId |
| `aliyun-token` | `token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic` | `auto` | 7 字段 |

未列出的提供商：可直接编辑 `~/.cc-launcher/providers/<name>.json`，结构同上。

### 历史示例（仍可参考）

```jsonc
// ~/.cc-launcher/providers/anthropic.json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-ant-xxx",
    "ANTHROPIC_MODEL": "sonnet"
  }
}

// ~/.cc-launcher/providers/bedrock.json（已废弃，请改用 aliyun-coding / aliyun-pay）
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
| manual | 标准行为，危险操作逐次询问确认（Claude Code 内部 `default` 的别名） | `--permission-mode manual` |
| acceptEdits | 自动接受文件编辑操作，其余操作仍询问 | `--permission-mode acceptEdits` |
| auto | 由模型分类器自动批准/拒绝权限请求 | `--permission-mode auto` |
| bypassPermissions | 跳过所有权限检查（危险，仅建议在隔离沙箱使用） | `--permission-mode bypassPermissions` |
| dontAsk | 不弹权限询问；未被 allow 规则预先授权的操作**直接拒绝** | `--permission-mode dontAsk` |
| plan | 计划模式，只分析不实际执行工具 | `--permission-mode plan` |

> 语义以本机 `claude --help` / Claude Code 2.1.x 为准。注意 `dontAsk` 是**最严格**的非交互模式（适合 CI），不是"自动放行"。

## 按目录记 provider

与"按目录记权限模式"对称，ccl 也可按当前工作目录（cwd）记录默认 provider：

- TUI 选完 provider + mode + session 后，ccl 末尾询问"记住当前目录 (`<wd>`) 默认用 `<provider>` 吗？[Y/n]"
  - Y → 写入 `state.json.dir_bindings[wd] = "<provider>"`；下次在同 cwd 跑 ccl 预选该 provider
  - n → 不记；走全局 `active_provider` 兜底
- `ccl provider <name>`（CLI 切换）也同步写 cwd binding——切完即生效
- `ccl provider --list` 输出标记：
  - `*` 标记 cwd 命中 `dir_bindings[wd]` 的 provider（cwd 覆盖是最具体的 scope）
  - `▶` 标记全局 `active_provider`（保留 legacy 行为）
  - 两者叠加为 `*▶`（cwd binding == active provider 时）

**示例**：

```bash
# 在 proj-a 跑 ccl 选 deepseek 并 Y 记住
cd ~/work/proj-a && ccl
# 选 deepseek → 末尾"记住当前目录 (~work/proj-a) 默认用 deepseek 吗？[Y/n]" → Y
# state.json.dir_bindings["/home/you/work/proj-a"] = "deepseek"

# 下次在 proj-a 跑 ccl 预选就是 deepseek
cd ~/work/proj-a && ccl
# 选 provider stage 时 cursor 默认在 deepseek

# 在 proj-b 跑 ccl（无 binding）走全局默认 anthropic
cd ~/work/proj-b && ccl
# 预选 anthropic（=active_provider）
```

**清理解除**：在同 cwd 跑 `ccl provider <name>` 重写 binding；或直接编辑 `~/.cc-launcher/state.json` 的 `dir_bindings` 字段（key 是 `os.Getwd()` 解析的绝对路径）。

## 配置合并机制

切换提供商时，框架执行以下步骤：

1. **备份** — 当前 settings.json 写入 `~/.cc-launcher/backups/`（保留最近 30 个）
2. **清理旧配置** — 按快照比对，删除未被用户修改的旧提供商字段
3. **注入新配置** — 写入新提供商的所有字段
4. **更新快照** — 记录本次注入的字段，供下次切换时比对

不会被删除的：不在快照中的字段（用户手动加的）、值与快照不同的字段（用户改过的）。

> **备份触发语义**（详见"用量监控" → "自动备份时机"）：除切换提供商外，每次 `ccl` 启动时也会**条件性**触发备份——仅在检测到 settings.json 不再处于 ccl 控制下时（如首次启动、第三方工具覆盖 OTel 端点后）。常态启动（仍在 ccl 控制下）不产生新备份。

### settings.json 历史兼容

老 ccl 版本（≤ provider-setenv-cwd-binding change 前）通过 `config.Manager.Merge` 把 provider env 写到 `~/.claude/settings.json`，但**写时不删**——切 provider 时旧 `ANTHROPIC_*` 可能残留。**本 change 改用 setenv 注入到子进程环境**（不再写 settings.json 的 provider 字段），但**首次升级时** ccl 会自动：

1. 读 `state.json.injected_snapshot`（老 ccl 写入的"上次 Merge 留下的字段"）
2. 跟 `settings.json` 当前值**值匹配比对**：值完全相同的 key 才删（**保护用户手改的字段**——CLAUDE.md §"Claude Code 本身添加的配置要保留"）
3. 删除值匹配的 `ANTHROPIC_AUTH_TOKEN / ANTHROPIC_BASE_URL / ANTHROPIC_MODEL / ...`；OTel / `CC_ORIG_*` / `FORWORD_*` 等非 provider 字段**不动**
4. 首次删除时打 stderr 日志 `⚙ 检测到 ccl 之前注入的 provider env，正在清理 settings.json（备份到 ~/.cc-launcher/backups/）... ✓ (N 个 key)`，并把 settings.json 备份到 `backups/settings-<时间戳>.json`
5. 清空 `state.json.injected_snapshot` 标记完成

之后 settings.json 不再被 ccl 写 provider 字段（OTel 注入行为不变）。

**用户手写了 `ANTHROPIC_*` 的情况**：

如果在老 ccl 写入后**手改过** `settings.json` 的 `ANTHROPIC_AUTH_TOKEN` 等 key，migrate 会**保留**（值与 snapshot 不等视为用户意图）。但**这意味着 ccl 的 setenv 注入会被 settings.json 压住**——Claude Code 启动时按 `settings.json` env 优先于 shell env。**解决方法**：

- 从 `settings.json` 删除手写的 `ANTHROPIC_*` key，让 ccl 接管 provider 配置
- 或用 `ccl provider <name>` 明确覆盖（仍受 settings.json 压住，需先清手写字段）

## 目录结构

```
~/.cc-launcher/
├── providers/              # 提供商配置（一个 JSON 一个提供商）
│   ├── anthropic.json
│   └── bedrock.json
├── backups/                # settings.json 写前备份
│   └── settings-2026-07-09T15-04-05.json
├── state.json              # 运行时状态（激活提供商、权限偏好）
├── telemetry.db            # 用量数据（SQLite / WAL）
└── web-token               # 用量看板的访问 token（0600）
```

## 开发

```bash
# 运行测试
./build.sh test

# 运行契约测试（校验权限模式列表与本机 claude CLI 一致）
./build.sh contract

# 编译
./build.sh build

### "打包" 指哪一层

三件事常被混称为"打包"，实际责任方不同：

| 层 | 产物 | 谁来做 | 本地能复现吗 |
|---|---|---|---|
| **编译** | `.exe` / `tar.gz` | 任何人：`./build.sh windows`（只出 .exe）、`./build.sh release`（5 个平台） | ✓ |
| **发到 GitHub Release** | `cc-launcher-windows-amd64.zip`（scoop 用户下载的就是它） | **只有 CI**（Linux runner 上 `zip -X` 打包） | ✗ 设计如此：Go build 产物含 GOMODCACHE state，fresh CI 与本地 cache 永远不 byte-level 一致（差 ~58KB / ~22K region） |
| **更新 scoop manifest** | `bucket/cc-launcher.json` | `bump-scoop`：取 release body 里的 `sha256` marker（**不是**本地 zip 的 hash） | ✓（但要等 CI 跑完） |

所以：**本地编译产物 → 可以；本地复现 release 的 zip → 不行（也不需要）**；
scoop manifest 的 hash 一律来自 release，不来自本地。

### Windows 开发

Windows 上分三种需求，所需工具不同：

| 需求 | 需要什么 | 怎么做 |
|---|---|---|
| 写代码 / 跑测试 | **Go**（[go.dev/dl](https://go.dev/dl/)） | `go build ./...` / `go test ./...` —— Windows 上直接用 go 命令，**不必装 Git Bash** |
| 交叉编译产物 | 同上 | `go build -trimpath -ldflags="-s -w" -o dist/cc-launcher-windows-amd64.exe ./cmd/cc-launcher/` |
| 打 tag | git + **PowerShell** | `pwsh bin/bump-version.ps1 bump minor` |
| 更新 scoop manifest | **PowerShell** | `pwsh bin/bump-scoop.ps1 v0.14.0` |

`build.sh` 需要 **Git Bash**（Windows 默认没有 bash）。在 Git Bash 里可用的子命令：
`build` / `install` / `local` / `run` / `test` / `contract` / `windows` / `release` / `clean`；
**`bump-scoop` 在 Windows 上跑不了**（依赖 `zip` 命令和 `python3`，Git Bash 默认都没有）→
用 `bin/bump-scoop.ps1`（纯 PowerShell 内置能力，不需要 Git Bash / python3 / gh）。
两个脚本产出的 manifest JSON **字节完全一致**。

Windows 上写 manifest 文件用 `-OutFile`（**不要**用 `>` 重定向）：

```powershell
pwsh bin/bump-scoop.ps1 v0.14.0 -OutFile cc-launcher.json
```

PowerShell 5.1 的 `>` 走 `Out-File`，默认编码是 **UTF-16LE** → 写出来的不是 UTF-8 JSON。
`-OutFile` 由脚本自己按 UTF-8 无 BOM + LF 落盘，PS 5.1 / 7+ 结果一致。

# 编译并运行（带参数）
./build.sh run
```

### 契约测试

`--permission-mode` 的合法取值由 Claude Code 定义，会随其版本变动。
`internal/cli/permission_contract_test.go`（build tag `contract`）会执行本机
`claude --help`，解析出实际的 choices 集合并与 `cli.ValidModes` 比对，
不一致就报错并列出需要新增/移除的模式。

- 用 build tag 隔离，不污染常规 `go test ./...`
- 本机没有 `claude` 命令时自动 SKIP，CI 环境安全
- `internal/tui/modes_test.go` 走常规测试，锁死 TUI 展示列表与 `cli.ValidModes` 一致

也就是说：Claude Code 一旦增删权限模式，`./build.sh contract` 会直接变红。

## 用量监控

ccl 在后台启一个轻量 OTel daemon，自动收集每次 Claude Code session 的 token / cost 消耗。

### 数据来源

Claude Code v2.1.x 原生支持 OTel 推送（`CLAUDE_CODE_ENABLE_TELEMETRY=1`）。ccl 检测到 settings.json 里没配 OTel 时，会自动写入 5 个 key + 视情况接管 1 个 per-signal metrics 端点：

| Key | 默认值 | ccl 是否改写 |
|-----|--------|------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://localhost:4318` | **是**（让 Claude Code 推到本地 daemon） |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | `http://localhost:4318/v1/metrics` | **仅当用户已设该 key**（备份为 `FORWORD_*`，覆盖为 localhost） |
| `OTEL_METRICS_EXPORTER` | `otlp` | 否（备份用户原值） |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` | 否 |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | `1` | 否 |
| `OTEL_LOGS_EXPORTER` | _不主动注入_ | 否 |

**改写机制**：
- 5 个 fallback key 备份到 `CC_ORIG_<KEY>` 后只对 `OTEL_EXPORTER_OTLP_ENDPOINT` 改写为本地 daemon 地址；其它 4 个 key 保留用户原值。
- per-signal metrics 端点用 **新的 `FORWORD_<KEY>` 命名空间**（不沿用 `CC_ORIG_`）——用户原话要求"备份 key 也不要用 cc_orig，改叫 forword"。详见下面"per-signal endpoint 兼容"段落。
- `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` / `_TRACES_ENDPOINT` 不动（ccl 不接管 logs/traces 的端点）。

### 自动启动 daemon

每次 `ccl` 启动前会探活 `:4318`：
- 端口已在用 → 已有 daemon，跳过 fork
- 端口空 → fork `ccl serve`（新进程组，不影响 Claude Code），等 ≤ 3s 就绪

fork 失败 / 端口冲突 → ccl 仍 exec Claude Code（OTel 推送静默失败，但 Claude Code 正常使用）。

### 查看 stats

```bash
ccl stats                       # 最近 7 天的所有 session（表格）
ccl stats --day 1               # 今天 24 小时 hourly 桶聚合（柱状图）
ccl stats --day 7               # 最近 7 天 daily 桶聚合
ccl stats --day 30              # 最近 30 天 daily 桶聚合
ccl stats --by-provider         # 按提供商聚合
ccl stats --by-project          # 按项目（cwd basename）聚合
ccl stats --by-model            # 按模型聚合
ccl stats --project cc-launcher # cwd 子串过滤
ccl stats --model MiniMax       # model 子串过滤
ccl stats --provider minimax    # provider 子串过滤
ccl stats --json                # JSON 输出（pipeable to jq）
ccl stats --json --day 7        # JSON 含 bucket_rows / bucket_totals 字段
```

`ccl stats --day N`（`1 ≤ N ≤ 90`）在 detail 表后追加"按时间桶聚合"小表：
- `--day 1` = 今天 24 小时 hourly（`HH:00` label，当前小时末尾加 `*` 标 partial）
- `--day N`（N≥2）= 最近 N 天 daily（`MM-DD` label）

每桶一行 `BUCKET | SESSIONS | IN | OUT | CACHE_CRE | CACHE_READ | COST_USD | TOTAL_TOKENS`；末列 `TOTAL_TOKENS` 为本桶 token 合计（`humanizeTokens(IN+OUT+CACHE_CRE+CACHE_READ)`，例如 `2.50K` / `150` / `0`）。`--json --day N` 时 JSON 加 `bucket_rows` 数组 + `bucket_totals` 对象，字段都是原始 int64。不传 `--day` 时维持旧 detail-only 行为。

`ccl stats` **不依赖 daemon 正在跑**——它直接打开 `~/.cc-launcher/telemetry.db` 读。db 文件用 SQLite WAL，所以 daemon 写和 ccl stats 读并发安全。

### provider / 项目维度从哪来

Claude Code 的 OTel payload 里**没有** cwd，也没有 provider——它只推 `session.id / model / type / start_type` 和 `host.arch / os.version / user.id` 这类机器信息。所以 ccl 用了两条路补这两个维度：

1. **launch 上下文（主路径）**：`ccl` 启动 Claude Code 时，把当次的 provider 名、工作目录、provider 端点 host 写进 `OTEL_RESOURCE_ATTRIBUTES`：

   | key | 值 |
   |-----|-----|
   | `ccl.provider` | provider 名（如 `minimax-m3.1-flash`） |
   | `ccl.cwd` | 启动时的绝对工作目录 |
   | `ccl.provider_endpoint` | provider 的 `ANTHROPIC_BASE_URL` 的 host（如 `api.minimax.cn`） |

   Claude Code 的 OTel SDK 会把它们作为 **resource attributes** 随 metrics 推送，daemon 直接取。这是**逐次 launch 的真值**——provider 是"这次启动用的哪个 provider"的快照，不是"当前配置的 provider"。

   **注入通道是 `--settings` 参数**（Claude Code 官方参数），不是进程环境：

   ```bash
   claude --settings '{"env":{"OTEL_RESOURCE_ATTRIBUTES":"team=platform,ccl.provider=...,ccl.cwd=..."}}'
   ```

   为什么不用进程环境：**`~/.claude/settings.json` 的 `env` 块优先级高于进程环境**（实测：settings.json 写 `X=2` + 进程环境写 `X=1` → Claude Code 实际拿到 `2`）。若你的 settings.json 里已经设了 `OTEL_RESOURCE_ATTRIBUTES`，走进程环境就会被整体顶掉 → provider / 项目维度恒空。`--settings` 的优先级**高于 settings.json**（实测：`--settings` > settings.json > 进程环境），所以走它就绕开了这一层——**你的 settings.json 里有没有那个 key 都不影响 ccl 的注入**。

   值的编码有硬约束：Claude Code 按 `,` / `=` 拆属性，且**一个非法值会让整份属性 map 被丢弃**（不是只丢那一个）。所以 ccl 对**自己追加的**值强制 percent-encode（unreserved 之外全编），编码后 > 255 字符的属性直接不注入。**你原有的值原样保留**（不重新编码），ccl 的同名 key 以 ccl 为准。

   ccl **不会**改你的 `~/.claude/settings.json`——`--settings` 只作用于本次启动的进程，直接 `claude` 启动的会话不受影响。

   ⚠️ **转发到远程 collector 时**，daemon 会剥掉 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint` 这三个键再转发（它们是本地记账，描述的是"这次 ccl 启动用了什么"，对远程 collector 没价值，还可能泄露本机工作目录）。你的远程 collector 收到的资源属性与你没装 ccl 时一致。**本地** `telemetry.db` 里仍然保留这三个键（CLI 的 provider 列兜底要用）。

2. **jsonl 兜底（补历史 + 非 ccl 启动的会话）**：payload 里没有 `ccl.cwd` 时，daemon 用 `session.id` 反查 Claude Code 的会话记录 `~/.claude/projects/*/<session-id>.jsonl`，读其中第一条带顶层 `cwd` 字段的记录（**只取 cwd 一个字段**，prompt 内容不读、不存、不记日志）。查不到就留空——那是正常状态，不是错误。

   daemon 启动时会在后台回填历史行（每次启动最多 500 行，**不阻塞**端口监听）；想一次补齐就跑 `ccl serve --backfill`（不限量）。回填是**数据驱动幂等**的：只处理 `cwd = ''` 的行，跑两次结果一致，之后新建的空行仍可被后续回填补上。

**直接用 `claude`（而不是 `ccl`）启动的会话**：provider 恒为空、cwd 靠 jsonl 兜底（常常也查不到，因为 Claude Code 会清理旧会话）。这类会话在 CLI 与看板上显示为「未知」——ccl **不会**拿"当前配置的 provider"去回填历史会话，那是假数据。

### 用量看板（浏览器）

`ccl stats` 只有终端一条路。想在浏览器里看（投屏汇报、远程开发），用 `ccl web`：

```bash
ccl web              # 确保 daemon 在跑，打印带 token 的看板 URL
ccl web --rotate     # 轮换访问 token（旧 URL 与旧 session 立即失效）
ccl web --open       # 用系统默认浏览器打开看板

ccl serve --backfill # 触发历史 session 的 cwd 回填（不限量）
```

> 回填是 **daemon 的数据维护动作**（修 `sessions.cwd`），跟看板页面无关，所以放在
> `ccl serve` 而不是 `ccl web`——`ccl web` 的 flag 全部是关于看板本身的。

`ccl web` 会打印两个地址（本机 LAN IP + 127.0.0.1），例如：

```
http://192.168.1.20:4318/ui?token=<64 位 hex>
http://127.0.0.1:4318/ui?token=<64 位 hex>
```

首次访问 `/ui?token=...` 会校验 token → 种一个 HttpOnly session cookie → **302 重定向到不带 token 的 `/ui`**（token 不留在地址栏、不进浏览器历史、不随 Referer 泄漏）。之后靠 cookie 访问。

看板本身挂在 daemon 已监听的 **:4318** 上，**不新开端口、不新增进程**——daemon 停了看板就没了（`ccl web` 会自动把 daemon 拉起来）。页面内容：KPI 卡片（各类 token / 总 cost / 会话数）、按时间桶的 token 堆叠柱 + cost 折线、按项目 / 提供商 / 模型的占比、会话明细表；支持窗口切换（24h / 7d / 30d / 90d）、三个维度的筛选、手动刷新与 30s 自动刷新。前端资源内嵌在二进制里（`go:embed`，零 CDN、零前端框架、图表是手写 SVG），离线可用。

**鉴权与安全**：

- token 是 32 字节随机 hex（64 字符），存在 `~/.cc-launcher/web-token`（权限 `0600`），首次启动自动生成、已存在则原样保留
- session cookie 的值是 `HMAC-SHA256(key=token, msg="ccl-web-session-v1|<签发时间>")`，**不含 token 明文**；校验用常量时间比较，7 天过期
- `--rotate` 轮换 token → 此前签发的所有 cookie 立即失效（cookie 的签名 key 就是 token）
- API 响应是**字段白名单**：不透传 metric labels（那里有 `user.id` 主机指纹哈希、`host.arch`、`os.version`）
- ⚠️ **已知风险**：同一个 `:4318` 上的 **OTel ingest 端点（`/v1/metrics`）没有鉴权**——daemon 绑 `0.0.0.0`，同局域网的任何设备都能往里灌伪造 metrics。这是**既存**风险（不是看板引入的），本次只给 UI 路由加了 token 保护。介意的话把 daemon 绑到 localhost 或用防火墙限制 `:4318` 的来源。

### 自动备份时机

ccl 会把每次 `settings.json` 写入前的快照存到 `~/.cc-launcher/backups/settings-<时间戳>.json`，保留最近 30 个。三种触发时机：

1. **手动 `ccl serve --restore`** — 还原操作前做一份（操作本身的回滚锚点）
2. **切换 provider（`Manager.Merge`）** — 合并前做一份
3. **每次 `ccl` 启动的 `injectOTelEnv`** — **仅在控制权交接边界**触发：
   - ✅ 首次 ccl 启动（settings.json 没被 ccl 写过）
   - ✅ 第三方工具/手动编辑覆盖 OTel 端点（ccl 检测到端点不指向 `localhost:4318`）
   - ✅ settings.json 不含 `env` 字段（用户从未用 OTel）
   - ❌ 常态启动（端点已是 ccl 默认值）—— **不**触发，避免 30 个轮转被瞬态版本耗光

判断条件："OTel 端点仍指向 ccl 默认值（兜底 `http://localhost:4318` 或 per-signal `http://localhost:4318/v1/metrics`）"则视为控制中，否则视为交接边界。

`backups/` 里现在会有三类快照：恢复点、provider 切换点、控制权交接点——按时间戳排序即可选择回滚目标。

### 端口冲突怎么办

如果 `:4318` 被别的 OTel collector 占了，`ccl serve` 会报错退出。两种解法：

```bash
# 方案 1：停掉冲突服务
sudo lsof -i :4318   # 找占用进程

# 方案 2：让 Claude Code 推到其它端口
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:14318
ccl  # 此时 ccl 不会 fork daemon（决策：远程 endpoint 不拉本地 daemon）
```

### 隐私默认

- daemon **不解析 logs / traces 的内容**——`/v1/logs` `/v1/traces` 仅回 HTTP 200，不持久也不转发
- 本地 SQLite **永远不会**有 prompt / response 文本
- 仅 metrics（含 token / cost / session 数）入库

如果你希望 logs 也推到本地 SQLite——ccl 不做，**永远不做**。日志走 fallback 路径时 daemon 也只回 200，不会持久化。

### per-signal endpoint 兼容

Claude Code v2.1.x 的 OTel SDK 按"先分类型端点 → 没有才用兜底"的顺序选 endpoint。如果用户在 settings.json 里**写齐了**分类型端点（典型公司远程 collector 配置），Claude Code 会优先用分类型端点把 metrics 直接推到远程 collector，`ccl stats` 看不到任何数据。

为接管这一场景，ccl 在检测到用户**已设** `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 时会：

1. 备份原值到 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（**新命名空间**，不复用 `CC_ORIG_`）
2. 把该 key 改写为 `http://localhost:4318/v1/metrics`，让 Claude Code 把 metrics 推到本地 daemon
3. daemon 把 metrics 异步转发到 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（用户原值）

**为什么是 `FORWORD_` 不是 `CC_ORIG_`**：用户原话要求"备份 key 也不要用 cc_orig，改叫 forword"。`CC_ORIG_<KEY>` 命名空间保留给 5 个 fallback key，避免双重历史（用户已有 `CC_ORIG_*` 不被重命名造成混乱）。

**为什么只接管 metrics，不接管 logs/traces**：logs/traces 用户本来就写好了去自己的远程 collector，已经直达了；ccl 再插一脚只是浪费流量 + 多一个可能挂的点。`ccl stats` 只需要 metrics 就够了。

**典型公司远程 collector 配置**（场景 A）：

```jsonc
// ~/.claude/settings.json 注入前
{
    "env": {
        "OTEL_EXPORTER_OTLP_ENDPOINT":            "http://remote:4318",
        "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":    "http://remote:4318/v1/metrics",
        "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":       "http://remote:4318/v1/logs",
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":     "http://remote:4318/v1/traces"
    }
}
```

注入后：

```jsonc
{
    "env": {
        // ccl 改写
        "OTEL_EXPORTER_OTLP_ENDPOINT":                "http://localhost:4318",            // 改写
        "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT":        "http://remote:4318",               // 备份
        "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":        "http://localhost:4318/v1/metrics", // 改写
        "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":"http://remote:4318/v1/metrics",    // 备份

        // 用户原值（ccl 不动）
        "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":           "http://remote:4318/v1/logs",
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":         "http://remote:4318/v1/traces",

        // 其余 4 个 fallback key 照常备份 + 注入默认
        "OTEL_METRICS_EXPORTER":                      "otlp",
        "OTEL_EXPORTER_OTLP_PROTOCOL":                "http/protobuf",
        "CLAUDE_CODE_ENABLE_TELEMETRY":               "1"
    }
}
```

### 转发到远程 collector

如果你公司有 OTel collector（如 `otel.example.com:4317`），ccl 启动后会自动：

1. 把你的 `OTEL_EXPORTER_OTLP_ENDPOINT` 备份到 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`
2. 把 endpoint 改写到 `http://localhost:4318`（本地 daemon）
3. daemon 把 metrics **异步**转发到 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`（或 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`，按优先级）

> **telemetry-config-compat 变更**：daemon 现为 metrics-only forwarder。logs/traces 不再经 daemon 转发——`/v1/logs` `/v1/traces` 收到请求只回 200，bytes 不落本地也不外发。

如需鉴权：
```bash
export CC_FORWARD_AUTH_HEADER="Authorization=Bearer my-token"
ccl  # daemon 自动加上 auth 头
```

转发失败：daemon stderr 一行（**不带 auth 值**），本地 SQLite 仍完整。

### 关闭 / 还原

> **Breaking change (v0.x)**：原顶层子命令 `ccl stop` / `ccl restore` 已整合为 `ccl serve` 的 flag。

```bash
ccl serve --stop       # 优雅停 daemon（5s 内 graceful shutdown）
ccl serve --restore    # 还原 settings.json 到 ccl 之前的状态（删 CC_ORIG_* + FORWORD_* + ccl 注入的默认）
ccl serve --status     # 打印 daemon 状态摘要（pid / 端口可达 / db 文件大小 / session 数）
ccl serve --restart    # 热重启 daemon（升级 ccl 二进制后用，Claude Code session 不受影响）
```

`ccl serve --restore` 是**不可逆**操作（spec 明确）。会先备份到 `~/.cc-launcher/backups/settings-<时间戳>.json`。

### 升级 ccl 二进制

```bash
./build.sh local       # 替换 ~/.local/bin/cc-launcher
ccl serve --restart    # 旧 daemon graceful shutdown + fork 新 binary daemon 上位
```

`--restart` 让新版本 daemon 上位而不打断 Claude Code session（OTel SDK 间隙自动重连，session 本身跟 daemon 完全解耦）。

### 文件位置

| 文件 | 用途 |
|------|------|
| `~/.cc-launcher/telemetry.db` | SQLite 数据（WAL 模式） |
| `~/.cc-launcher/telemetry.db-wal` / `-shm` | WAL 配套文件，sqlite 自动管理 |
| `~/.cc-launcher/daemon.pid` | daemon 运行时 PID 文件 |
| `~/.cc-launcher/state.json` | ccl 自身状态（active provider / permission mode） |
| `~/.cc-launcher/backups/` | settings.json 自动备份轮转（最近 30 个） |
| `~/.cc-launcher/web-token` | 用量看板的访问 token（32 字节随机 hex，权限 0600） |
