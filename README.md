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
└── state.json              # 运行时状态（激活提供商、权限偏好）
```

## 开发

```bash
# 运行测试
./build.sh test

# 运行契约测试（校验权限模式列表与本机 claude CLI 一致）
./build.sh contract

# 编译
./build.sh build

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
ccl stats --project cc-launcher # cwd 子串过滤
ccl stats --json                # JSON 输出（pipeable to jq）
ccl stats --json --day 7        # JSON 含 bucket_rows / bucket_totals 字段
```

`ccl stats --day N`（`1 ≤ N ≤ 90`）在 detail 表后追加"按时间桶聚合"小表：
- `--day 1` = 今天 24 小时 hourly（`HH:00` label，当前小时末尾加 `*` 标 partial）
- `--day N`（N≥2）= 最近 N 天 daily（`MM-DD` label）

每桶一行 `BUCKET | SESSIONS | IN | OUT | CACHE_CRE | CACHE_READ | COST_USD | TOTAL_TOKENS(柱)`，柱长按总 token 数 8 级半块（`▏▎▍▌▋▊▉█`）归一化。`--json --day N` 时 JSON 加 `bucket_rows` 数组 + `bucket_totals` 对象。不传 `--day` 时维持旧 detail-only 行为。

`ccl stats` **不依赖 daemon 正在跑**——它直接打开 `~/.cc-launcher/telemetry.db` 读。db 文件用 SQLite WAL，所以 daemon 写和 ccl stats 读并发安全。

### 自动备份时机

ccl 会把每次 `settings.json` 写入前的快照存到 `~/.cc-launcher/backups/settings-<时间戳>.json`，保留最近 30 个。三种触发时机：

1. **手动 `ccl serve --restore`** — 还原操作前做一份（操作本身的回滚锚点）
2. **切换 provider（`Manager.Merge`）** — 合并前做一份
3. **每次 `ccl` 启动的 `injectOTelEnv`** — **仅在控制权交接边界**触发：
   - ✅ 首次 ccl 启动（settings.json 没被 ccl 写过）
   - ✅ 第三方工具/手动编辑覆盖 OTel 端点（ccl 检测到端点不指向 `localhost:4318`）
   - ✅ settings.json 不含 `env` 字段（用户从未用 OTel）
   - ❌ 常态启动（端点已是 ccl 默认值）—— **不**触发，避免 30 个轮转被瞬态版本耗光

判断条件："OTEL 端点仍指向 ccl 默认值（兜底 `http://localhost:4318` 或 per-signal `http://localhost:4318/v1/metrics`）"则视为控制中，否则视为交接边界。

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
