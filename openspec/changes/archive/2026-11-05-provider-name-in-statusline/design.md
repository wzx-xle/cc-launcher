## Context

### 现状

用户的 Claude Code 状态栏是自建的：`~/.claude/settings.json` 里配 `statusLine: {type: "command", command: "node /home/zxwang/.claude/statusline.js"}`，脚本用 stdin 读 Claude Code 喂的 JSON，stdout 打两行：

```
claude-sonnet-4-5 | cc-launcher | git(main)
ctx: ████░░░░░░ 42% | 5h: ████████░░ 78% | 7d: ██░░░░░░░░ 21%
```

`statusLine` 脚本拿到的 JSON 里有 `model.display_name`、`workspace`、`cost`、`context_window`、`rate_limits`、`vim.mode`、`worktree.name` 等字段——**没有 provider**。Claude Code 内部也没有"当前用了哪个 provider"的概念（provider 是 ccl 这一层的概念），所以状态栏无从得知。

于是只剩环境变量一条路：ccl 启动 Claude Code 时把 provider 名放进环境，状态栏脚本读 `process.env`。

### 关键约束（均已本机实测，2026-11-05，Claude Code 2.1.311）

用真 `claude` + pty 驱动 + 探针 statusLine 脚本（脚本把自己的 `process.env` 写盘）观测子进程环境，结论三条：

| 通道 | statusLine 子进程能否读到 |
|---|---|
| `--settings` 交付对象的 `env` 块 | ✅ 能 |
| 父进程环境 | ✅ 能 |
| **任何 `OTEL_` 前缀的变量** | ❌ **被剥离**（`OTELX_THING` 不受影响——是前缀规则，不是"含 OTEL 字样"） |

第三条是决定性的：Claude Code 会把 `OTEL_` 前缀的变量从子进程环境里摘掉，所以"复用已有的 `OTEL_RESOURCE_ATTRIBUTES` 里的 `ccl.provider` 属性来显示"这条零改动捷径**不可行**。这也顺带说明：ccl 现有的 launch context 通道（OTEL）服务的是 daemon 的数据链路，不是给消费方做显示用的，两者不能混用。

这条剥离与 `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` 开关**无关**——实测 `SCRUB=0` 时同样被剥离。

### `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` 的影响（实测，Claude Code 2.1.311）

该开关（默认启用，依赖 bubblewrap）会把**凭证类**变量从子进程环境里删掉。对照实验：

| 变量 | `SCRUB=0` | `SCRUB=1` |
|---|---|---|
| `CCL_PROVIDER`（普通值） | 在 | **在** |
| `ANTHROPIC_AUTH_TOKEN`（凭证） | 在 | **被剥离** |

结论有两层：其一，**本方案选的变量名不受 scrub 影响**——它没有任何凭证特征；其二，**变量名不能带凭证特征**，否则换个名字就会被误伤（这是变量名成为对外契约的又一条理由）。

### 项目约束

- CLAUDE.md 约束 6：**所有变量传递统一走 Claude Code 的 `--settings` 参数**。
- ccl 不再写 `~/.claude/settings.json` 的 provider 配置；`statusLine` 键同样不由 ccl 碰。
- 交付对象按内容哈希命名（`~/.cc-launcher/cache/settings-<hash>.json`），内容不同 → 哈希不同。

## Goals / Non-Goals

**Goals:**

- ccl 在启动 Claude Code 时，让"本次用的是哪个 provider"这件事对子进程可见。
- 变量名与取值语义写进 spec 与 `docs/`，成为可查的对外契约。
- 复用 ccl 既有的交付管道（`--settings` env 块 + 进程环境冗余），不新开通道、不改 schema、不改 settings 文件格式。

**Non-Goals:**

- 不接管 `~/.claude/settings.json` 的 `statusLine` 键，不分发状态栏脚本。渲染成什么样是用户自己的事。
- 不把 `CCL_PROVIDER` 做成"provider 信息的通用载体"（不放 `note`、不放 endpoint）。
- 不新增 provider 的显示名字段。
- 不改 `note` / `order` 的元数据地位，不改 provider 配置文件格式。

## Decisions

### 决策 1：变量名 `CCL_PROVIDER`

值 = 本次启动选中的 provider 的 `Name`（provider 配置文件名去掉 `.json`），例如 `claude-official`、`minimax-m3-stliu14`。

**备选与否决理由：**

| 备选 | 否决理由 |
|---|---|
| `ANTHROPIC_PROVIDER` | 占用 Anthropic 的命名空间。上游可能自己定义这个变量，或者将来用它表达"API 服务方"（`anthropic` / `bedrock` / `vertex`），语义会打架。 |
| `CLAUDE_CODE_PROVIDER` | 占用 Claude Code 的命名空间（该前缀下已有 `CLAUDE_CODE_*` 一整族）。上游一旦定义同名变量，语义冲突且难以排查。 |
| `CCL_PROVIDER_NAME` | 更明确，但 `CCL_PROVIDER` 已经足够——ccl 自己的命名空间里没有歧义。 |
| `CCL_PROVIDER_DISPLAY` | 暗示"展示用格式化后的值"，与实际语义（provider 的标识）不符。 |

选 `CCL_PROVIDER` 的理由：`CCL_` 是 ccl 自有命名空间，与上游两个命名空间都不冲突；且与已有的 OTEL 属性名 `ccl.provider` 呼应——同一个概念，两条通道，命名一致。

**这个名字还必须不带凭证特征**：Claude Code 的 `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`（默认启用）会从子进程环境里删掉凭证类变量（实测：`ANTHROPIC_AUTH_TOKEN` 在 scrub 开启时被剥离）。`CCL_PROVIDER` 不属于这类，安全；但 `CCL_PROVIDER_TOKEN`、`CCL_PROVIDER_KEY` 这类命名会被误伤——**改名时必须记住这条**。

### 决策 2：主通道 = `--settings` 的 env 块，冗余第二通道 = 进程环境

与 `OTEL_RESOURCE_ATTRIBUTES` 的双通道写法完全同构（`cmd/cc-launcher/launchctx.go:69-70`）。

- **主通道**：`--settings` 交付对象的 `env` 块。该层在优先级栈第 2 层，压过用户 `~/.claude/settings.json` 的 `env` 块、也压过进程环境。符合 CLAUDE.md 约束 6。
- **冗余第二通道**：进程环境 `os.Setenv`。父进程环境同样能被 statusLine 子进程继承（实测），万一上游某天不把 settings 的 env 块落到子进程环境，这一份还在。

**备选与否决理由：**

| 备选 | 否决理由 |
|---|---|
| 只走进程环境 | Claude Code 里 settings 文件的 `env` 块压过进程环境 → 用户在 `~/.claude/settings.json` 里写一个同名键就能把 ccl 的值顶掉。这正是 `provider-env-via-settings` 当初放弃纯 setenv 的原因。 |
| 只走 `--settings` | 少一条兜底。项目既有惯例就是双通道（provider env、OTEL 上下文都是），单独破例没有理由。 |
| 走 `--settings` 的 `statusLine` 顶层键 | **不可行**。`statusLine` 是标量键，按"整值取最高层"合并 → `--settings` 层的 `statusLine` 会**覆盖**用户 `~/.claude/settings.json` 里的 `statusLine`，ccl 就此接管了用户的显示层。而且 ccl 并不知道用户的 statusLine 命令是什么，也没有 placeholder 机制可以往里注入 provider 名（provider 的顶层键是无脑原样透传，不做展开）。 |

### 决策 3：显示 provider 的 `Name`，不显示 `note`，不显示 endpoint

- **`Name`**：provider 的标识。它本身已含账号区分信息——实际样本里 `minimax-m3` 与 `minimax-m3-stliu14` 是两个不同账号连同一个 endpoint，`stliu14` 正是账号拼音缩写。
- **不显示 `note`**：`note` 是用户的个人备忘，实际样本里是 `"潘振兴"`、`"刘世通"` 这样的人名，且大多为空。它不是 provider 标识，拿来显示只会显示人名或空串。
- **不显示 endpoint host**：三个 minimax provider 连的都是 `api.minimax.cn`，但账号不同——endpoint 区分不了账号，`Name` 能。显示 endpoint 只会给出"都一样"的假安全感。

因此这个变量里只放 `Name`，一个值，不做多字段拼接。

### 决策 4：ccl 侧注入原值不转义，渲染前的清洗交给消费方

provider 名来自文件名，理论上可含控制字符或 ANSI 转义序列。

- **ccl 侧**：注入**原值**。保持真值——这个变量表达的就是"provider 的名字"，ccl 不该改写它；且 OTEL 通道有自己的 percent-encoding 规则，两条通道各自编码、互不干扰。
- **消费方侧**：负责在渲染到终端前剥掉 ANSI/控制字符并限长（防止终端转义注入）。这条写进 spec，因为消费方多半不在本仓库内，写下来才有人看得到。

**备选与否决理由：**

| 备选 | 否决理由 |
|---|---|
| ccl 侧 percent-encode（同 OTEL 通道） | 消费方还得 decode，多一层；且显示出来的仍需还原，徒增复杂度。 |
| ccl 侧剥离控制字符 | 丢失真值：变量不再等于 provider 名，别的消费方（比如只想拿原始名做匹配）会拿到变形值。 |

### 决策 5：常量放在 `internal/provider`

`EnvProviderName = "CCL_PROVIDER"`，与 `Info.Name` 同源——变量的值就是 `provider.Info.Name`，常量跟它住一起。

**备选与否决理由：**

| 备选 | 否决理由 |
|---|---|
| `internal/launchctx` | 那个包的定位是"把启动上下文编码进 `OTEL_RESOURCE_ATTRIBUTES`"，塞进一个非 OTel 的变量名会破坏它的边界。 |
| `cmd/cc-launcher`（package main） | package main 的常量外部不可复用，测试也只能在包内。 |
| 新建一个包 | 为一个常量开一个包，过度设计。 |

### 决策 6：注入点放在 `buildLaunchOptions`，与 `ANTHROPIC_MODEL` 的处理并列

`cmd/cc-launcher/main.go` 的 `buildLaunchOptions` 已经有一个同构的先例：provider 配了顶层 `model` 键 → 同步放进交付的 env 块 + 进程环境。`CCL_PROVIDER` 走同一处，代码形态与心智模型都一致。

**provider 自己的 env 块若含 `CCL_PROVIDER`**：以 ccl 的为准（沿用"ccl 自己的键赢"的既有纪律）。注入放在 `deliver` 汇流之后即可自然满足。

### 决策 7：无 provider 时不注入

变量不存在 = "本次没有选 provider"。消费方据此不显示 provider 段——比注入一个空串更明确（空串既可能是"没有 provider"，也可能是"provider 名是空的"）。

### 决策 8：`internal/settingsschema` 不动

`env` 块的内部结构本来就不校验（`internal/settingsschema/table.go:116-122`，实测 Claude Code 自己会把非字符串值转成字符串）。加 `CCL_PROVIDER` 不触碰已知顶层键白名单。

## Risks / Trade-offs

- **[Claude Code 升级改变子进程环境策略]** → 本 change 的地基是"settings 的 env 块会落到子进程环境"（测于 2.1.311，2026-11-05）。上游若改掉，`CCL_PROVIDER` 会失效，且**表现为消费方读不到变量而不是报错**——静默降级。所以验证步骤里包含真机复测，并把实测结论记进 `docs/claude-code-settings-merge-semantics.md`（那份文档已经有"Claude Code 一升级要重验"的维护提醒）。
- **[用户自己在 `~/.claude/settings.json` 的 env 块里写了 `CCL_PROVIDER`]** → `--settings` 层压过用户层，ccl 的值赢。不会出问题，但用户会觉得"我写的怎么没生效"——README 里点一句。
- **[provider 名含控制字符时，消费方原样渲染]** → 消费方责任，已写进 spec 的 MUST。ccl 侧不做清洗是刻意的取舍（见决策 4）。
- **[变量名成为事实上的对外契约]** → 一旦发布就是契约：改名等于 breaking change。变量名写进 spec 与 `docs/`，`docs/` 里记录"改这个变量名需要什么流程"。
- **[交付文件哈希]** → 不同 provider 的 env 块内容本就不同，哈希本就不同。`CCL_PROVIDER` 加进去后，同一 provider 的内容哈希变了 → 首次升级会生成一个新缓存文件，旧文件按既有的一年清理规则回收。无需额外处理。
- **[变量名将来被改成凭证风格 → 被 scrub 剥掉]** → `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`（默认启用）会从子进程环境删掉凭证类变量。本方案选的 `CCL_PROVIDER` 不受影响（实测两种配置下都在），但"改名"这条路径上必须记住：**变量名不能带 `TOKEN` / `KEY` / `SECRET` / `PASSWORD` 这类特征**。改名本身是 breaking change，这条约束跟着走。
- **[消费方误以为 env 值会做 shell 展开]** → settings 的 `env` 块**不做** shell 展开（`~`、`$HOME` 保持字面）。provider 名来自文件名，理论上可含 `~` / `$`。ccl 侧注入原值是对的（保持真值），但消费方若自己拼 shell 命令处理这个值，会踩到"不展开"这条。
- **[消费方用 `/bin/sh` 而非 bash 写 inline 命令]** → Claude Code 的 statusLine 是用 `/bin/sh -c "<command>"` 执行的（本机 `/bin/sh` → dash），inline 命令只能写 POSIX sh 语法。消费方若把命令直接写成内联字符串，`[[ ]]`、数组、`source` 都会炸。写成 `node xxx.js` 这样的外部脚本则无此问题（推荐做法）。
- **[statusLine 是每次刷新新起的进程]** → 消费方每次被拉起都是新进程，`process.env` 现读现用，claude 进程的环境一更新，下一次刷新就反映新值。ccl 切 provider 本来就是重启 claude，不受影响——但这意味着消费方**不能缓存**这个值到会话级缓存。

## Migration Plan

无迁移：这是纯增量，不改任何既有配置格式，不改既有契约，不需要用户做一次性动作。

回滚：把 `buildLaunchOptions` 里的注入去掉即可，无残留状态（环境变量随进程结束消失）。

## Open Questions

- 无阻塞项。
- 待实现时确认：`CCL_PROVIDER` 是否也值得出现在 `ccl provider list` 的输出里（"当前会话用的 provider"）——本次不做，属独立需求。
