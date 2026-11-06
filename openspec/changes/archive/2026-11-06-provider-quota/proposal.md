## Why

Claude Code 的状态栏有两个额度槽位（`5h` / `7d`），数据来自 Claude Code 自己维护的 `rate_limits`。但那是 **Anthropic 官方**的限流——**走中转/第三方模型时它拿不到数据**，槽位就一直是 `--`。

实测（2026-11-05，Claude Code 2.1.311 + MiniMax provider）：状态栏第二行是

```
ctx: ░░░░░░░░░░ -- | 5h: ░░░░░░░░░░ -- | 7d: ░░░░░░░░░░ --
```

两个额度槽位全是空的。用户**看不到自己的配额还剩多少**，只能等 429 报错才发现。而 MiniMax 明明提供了额度查询接口（`/v1/token_plan/remains`），返回的正是 5 小时窗口与周窗口的剩余百分比。

这个问题在多账号场景下更突出：现有 3 个 minimax provider 里，`minimax-m3` 与 `minimax-m3.1-flash` **共用同一把 API key**（同一个额度池），`minimax-m3-stliu14` 是另一个账号。额度按 key 分，状态栏上却什么都看不到。

换一个 provider 就更糟。ccl 的 provider 模板覆盖 deepseek / minimax / glm / aliyun-coding / aliyun-pay / aliyun-token / anthropic，**各家"额度"的形态完全不同**：

| provider | 额度形态 | 百分比可算？ | 会重置？ |
|---|---|---|---|
| Anthropic 官方 | 5h + 7d 窗口 | ✅ | ✅ |
| MiniMax 套餐 | 5h + 7d 窗口 | ✅ | ✅ |
| DeepSeek | 预付费余额（$） | ❌ **没有分母** | ❌ |
| 讯飞（New API 私有部署） | 令牌总额度（额度点） | ✅ 但分母是**用户自设上限** | ❌ |

**固定成"5 小时 + 7 天"两个槽位的输出形状表达不了这个多样性**：只有月额度的厂商塞不进 `five_hour`；只有余额的厂商算不出百分比；标签写 `5h` 实际是 30 天更是说谎。所以额度输出必须是一个**维度列表**，而不是两个固定槽位。

## What Changes

- **额度刷新由 `ccl serve` 负责**：`ccl serve` 已经是 ccl 自动拉起的常驻进程（`daemon-lifecycle` spec：每次 `ccl` 启动 Claude Code 前都会探 `:4318` 并 fork 它，且它**永不自动 idle 退出**）。在它里面加一个**周期刷新任务**（**默认 60 秒**，per-provider 可配），把额度写进 `~/.cc-launcher/quota.json`。
- **新增 `ccl quota` 子命令**：**默认只读本地数据文件，一次网络都不发**，输出 JSON——**一个额度维度列表**（`dimensions[]`），每个维度带 `kind`（`window` / `quota` / `balance`）、`duration_seconds`、`used_percentage`、`used` / `limit` / `remaining`、`unit`、`label`、`resets_at`。`label` 与 `unit` **必须全是 ASCII**（窗口型 label 从时长派生，非窗口型用 `unit` 的值）——状态栏是渲染到终端的一行文本，中文/缺字形就没法读。
- **新增 `internal/quota` 包**，三层拆分：
  - `adapter` 层——**厂商差异**（识别、端点推导、响应解析）全部关在这里，每个厂商一个 adapter
  - `model` 层——统一的 `Dimension` 类型与 `Kind` 分类，**只有一处**
  - `store` / `refresh` 层——数据文件读写、周期判定、额度池去重
- **内置三个 adapter**：`minimax`（5h/7d 窗口）、`deepseek`（余额）、`newapi`（New API / one-api 系部署，令牌额度）
- **新增 provider 配置键 `quota`**（对象）：`source` 选内置 adapter；`endpoint` 可选，覆盖自动推导；`quotaPerUnit` / `displayUnit` 可选，覆盖额度点换算与显示单位；`refreshInterval` 可选，覆盖刷新周期（秒，缺省 60）。它是 ccl 自己的元数据键，**必须加进 `provider.ReservedKeys`**——否则会被当成 Claude Code 的 settings 键原样透传。
- **`ccl quota --refresh`**：**手动联网刷一次**并写回数据文件（**不需要 serve 在跑**——serve 被 `ccl serve --stop` 关掉时的逃生舱）。写入走**读-改-写**，与 serve 并发写不需要文件锁；且 serve 的周期判定从数据文件的 `fetched_at` 读 → 两者天然互斥，不会重复查询。
- **状态栏脚本**：读 `ccl quota --json` 的 `dimensions[]`，按 `kind` 分三种画法（窗口/额度画条形图，余额只显示数字）；拿不到时回落到 Claude Code 的 `rate_limits`（Anthropic 官方 provider 的行为不变）。**只调命令，不直接读数据文件**——文件位置与格式是 ccl 的内部细节。

**为什么刷新要搬到 serve**：状态栏命令由 Claude Code 高频触发（实测生成过程中约**每 300ms 一次**，且有 **300ms 去抖窗口**+**每次执行前 abort 上一次**），而额度数据的变化速度是分钟级——**刷新频率与调用频率本来就该解耦**。由 serve 定时刷新后，状态栏路径恒定 ~15ms、一次网络都不发，也不需要 detached 子进程和刷新锁。

不做（non-goals）：

- 不做插件系统、不做声明式 JSON 路径映射、不做通用额度 DSL——厂商形状差异大到需要 DSL 的程度时，那是另一个提案。
- 不做 RPM/TPM 速率限制的建模——那是速率不是额度，语义不同。
- 不改动 Claude Code 自己的 `rate_limits` 语义，也不去动 `~/.claude/settings.json` 的 `statusLine` 配置。
- 不做额度告警、不做自动切换 provider、不做预算控制。
- 不在 `--settings` 的 env 块里交付额度数据——额度是**随时间滚动变化**的量，启动时的一次性快照在会话内会明显过时（5 小时窗口是滚动的）。

## Capabilities

### New Capabilities

- `provider-quota`: ccl 如何查询 provider 的额度——adapter 识别与端点推导、`quota` 配置块、统一的额度维度模型、周期刷新与数据文件、`ccl quota` 的输出契约、失败语义。

### Modified Capabilities

- `provider-settings-passthrough`: 顶层键的排除清单从 `note` / `order` 扩到 `note` / `order` / `quota`——新增的 `quota` 是 ccl 自己的元数据，不是 Claude Code 的 settings 键，同样不得透传。
- `daemon-lifecycle`: `ccl serve` 除了采集 OTel，还**周期刷新各 provider 的额度**（默认 60 秒，可配），刷新不得干扰 metrics 服务与 graceful shutdown。

## Impact

- **新增** `internal/quota/`：`model.go`（Dimension / Kind）、`adapter.go`（接口 + 注册表）、`adapter_minimax.go`、`adapter_deepseek.go`、`adapter_newapi.go`、`store.go`（数据文件读写）、`refresh.go`（枚举 / 去重 / 周期判定 / HTTP 客户端）。
- **新增** `cmd/cc-launcher/quotacmd.go`：`ccl quota` 子命令（`--provider` / `--json` / `--refresh`），**默认纯本地读**。
- **改动** `cmd/cc-launcher/serve.go`：接入额度刷新 ticker（启动即刷一轮；用独立 goroutine + ctx 取消，不干扰 metrics 与 5s graceful shutdown 预算）。
- **改动** `internal/provider/provider.go`：`ReservedKeys` 增加 `quota`；provider 配置读取能拿到该键。
- **改动** `cmd/cc-launcher/providerconfig.go`：`providerConfig` 增加 `Quota` 字段（从配置顶层取，不进透传键）。
- **改动** `cmd/cc-launcher/cli.go` / `main.go`：路由 `quota` 子命令；`--help` 与 shell 补全补上。
- **改动**（用户个人目录，不在本仓库）：`~/.claude/statusline.js` 读 `ccl quota --json` 的 `dimensions[]`，按 `kind` 分三种画法填进额度段。
- **新增** `docs/provider-quota.md`：额度查询的对外说明（`ccl quota` 的用法、输出契约、数据文件与刷新周期、支持的 provider 与 adapter）。
- 风险：额度接口是**外部行为**（各厂商侧的字段与端点可能变），解析失败必须**降级而不是崩**——serve 保留旧条目并让数据变 stale，状态栏拿不到数据就保持 `--`，绝不能让 Claude Code 启动失败。
