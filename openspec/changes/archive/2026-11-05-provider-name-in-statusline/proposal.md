## Why

Claude Code 的状态栏看不到当前会话用的是哪个 provider。

用户的 provider 列表里有 6 个 provider，其中三个 minimax（`minimax-m3`、`minimax-m3.1-flash`、`minimax-m3-stliu14`）连的是**同一个 endpoint** 但用的是**不同账号**（不同 token）——从状态栏显示的模型名、目录、分支都分不出来。多开几个 Claude Code 窗口时，"这个窗口到底连的是谁、用的哪个账号"是一个真实存在的歧义，只能靠记忆去猜。

Claude Code 的 `statusLine` 机制允许自定义状态栏，但它喂给脚本的 stdin JSON 里**没有 provider 信息**（只有 `model.display_name`、`workspace`、`cost`、`context_window`、`rate_limits` 等），statusLine 脚本自身也拿不到 Claude Code 内部的 provider 选择。唯一可行的通道是环境变量。

时机合适：ccl 的 provider 交付机制（`provider-env-via-settings` / `provider-settings-passthrough`）已经成熟，"`--settings` 的 env 块会落到进程环境"是已实测的事实，再挂一个只读变量是纯增量。

## What Changes

- **ccl 把本次启动选中的 provider 名放进环境变量 `CCL_PROVIDER`**，值是 provider 的 `Name`（provider 配置文件名去掉 `.json`）。
  - **主通道**：`--settings` 交付对象的 `env` 块——符合项目"所有变量传递统一走 `--settings`"的硬约束，该层在优先级栈第 2 层，压过用户 `~/.claude/settings.json` 的 `env` 块与进程环境。
  - **冗余第二通道**：进程环境（`os.Setenv`）——与项目既有惯例一致（`cmd/cc-launcher/launchctx.go` 里 `OTEL_RESOURCE_ATTRIBUTES` 的双通道写法）。
- **没有选中 provider 时不注入该变量**——变量不存在即"没有 provider"，消费方据此决定不显示。
- **provider 自己的 `env` 块里若出现同名键 `CCL_PROVIDER`，以 ccl 的为准**——沿用"ccl 自己的键赢"的既有纪律。
- **变量名 `CCL_PROVIDER` 是对外契约**，写进 spec 与 `docs/`，让消费方（用户自己的 statusLine 脚本）在换机器、换 Claude Code 版本后仍有据可依。

不做（non-goals）：

- 不接管、不修改 `~/.claude/settings.json` 的 `statusLine` 键——那是用户自己的显示层，ccl 只负责把名字放进环境。
- 不分发 statusLine 脚本，也不把消费方的渲染逻辑纳入本仓库。
- 不把这个变量扩展成"provider 信息的通用载体"：不放 `note`（是用户个人备忘，实际样本里是人名）、不放 endpoint host（三个 minimax provider 连同一个 endpoint 但账号不同，endpoint 区分不了账号，反而给出假安全感）。
- 不新增 provider 的显示名字段——provider 的 `Name` 本身已含账号区分信息（`minimax-m3-stliu14` 里的 `stliu14` 正是账号拼音缩写）。
- 不改变 `note` / `order` 的元数据地位，不改 provider 配置文件的格式。

## Capabilities

### New Capabilities

- `provider-name-in-statusline`: ccl 如何把本次启动选中的 provider 名放进环境变量 `CCL_PROVIDER`——值是什么、什么情况下不注入、与既有交付通道的关系、消费方的取值与渲染纪律。

### Modified Capabilities

（无。`claude-settings-delivery` 的要求约束的是交付对象的**形状**（env 块 + provider 透传键）与**顺序稳定性**，交付的 env 块多带一个键不改变这两条；`provider-settings-injection` 约束的是 provider 自身 env 的覆盖语义，不涉及 ccl 自己加的键。既有契约不动。）

## Impact

- `internal/provider/provider.go`：新增常量 `EnvProviderName = "CCL_PROVIDER"`，与 `Info.Name` 同源（值就是 provider 的名字）。
- `cmd/cc-launcher/main.go`：`buildLaunchOptions` 里把 `CCL_PROVIDER` 加进交付的 env 块，并 `os.Setenv` 一份冗余副本。
- `internal/settingsschema`：**无需改动**——`env` 块的内部结构本来就不校验（`table.go:122`），加这个键不触碰已知键白名单。
- `cmd/cc-launcher/settingsfile.go`：交付文件按内容哈希命名，不同 provider 的 env 块内容本来就不同，哈希本来就不同——机制不变，无需改写。
- 风险：**Claude Code 升级可能改变子进程环境的策略**。本 change 依赖"settings 的 env 块会落到子进程环境"这一实测行为（测于 Claude Code 2.1.311，2026-11-05）；若上游改掉该行为，`CCL_PROVIDER` 会失效（表现为消费方读不到变量，而不是报错）——所以验证步骤里包含真机复测，并把结论记进 `docs/`。
- 风险：`CCL_` 前缀是 ccl 自有命名空间，与 Claude Code 自身的环境变量（`CLAUDE_*` / `ANTHROPIC_*`）不冲突；但用户可以自己在 `~/.claude/settings.json` 的 `env` 块里写同名键——`--settings` 层压过用户层，ccl 的值仍然赢。
