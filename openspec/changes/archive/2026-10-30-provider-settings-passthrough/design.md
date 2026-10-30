## Context

ccl 通过 Claude Code 的 `--settings` 参数交付配置：把 provider 的 `env` 块、OTel 环境、启动上下文合并成一个 settings 对象，写进 `~/.cc-launcher/cache/settings-<sha256[:16]>.json`，再把这个文件路径作为 argv 传给 claude（`cmd/cc-launcher/settingsfile.go`、`cmd/cc-launcher/launchctx.go`）。

当前 `buildLaunchSettingsJSON`（`cmd/cc-launcher/launchctx.go:85`）把输出形状硬编码成 `{"env":{…}}`，`providerEnvMap`（`cmd/cc-launcher/main.go:246`）只从 provider 配置里取 `env` 块。provider 文件里的其它顶层键（`model`、`permissions`、`hooks` …）既不读取也不校验，静默丢弃。

**2026-10-29 在本机 Claude Code 2.1.308 上实测 + 官方文档核对**得到的外部事实（本次设计全部依赖它们）：

| 事实 | 依据 |
| --- | --- |
| `--settings` 的优先级高于 project local / shared project / user settings.json，低于 managed settings | 文档 settings.md + 实测 |
| `--settings` 能设置 user settings 能设的任何键（顶层 173 个），不能设 managed / `~/.claude.json` 的 global config 键 | 文档 settings.md |
| **列表键取并集**（`permissions.allow` 等），标量键整值取最高层；例外：`fallbackModel`/`modelPicker`/`availableModels`/`modelSettings` | 文档 settings.md + 实测（两个 hook 都执行） |
| **env 块逐变量并集**，同名高优先级赢 → ccl 只送 env 块**不会**吃掉用户 settings.json 的 env | 实测（`API_TIMEOUT_MS=3000000`、`MY_USER_ENV_PROBE` 均保留） |
| 用户 settings.json 的 env 块**压过进程环境**里的同名变量 | 文档 env-vars.md |
| **未知顶层键静默忽略**（zod 顶层 `passthrough()`），不 warning 不报错 | 实测 + 源码 |
| **已知键类型不对 → 整个 settings 文件被丢弃**（`These settings files did not load…`），连同里面的 env | 实测 |
| 条目级错误（数组里混入非字符串、未知 hook 事件名）只丢该条目，文件其余部分仍生效 | 实测 |
| `ANTHROPIC_MODEL` 环境变量**压过**任何文件里的 `model` 键 | 实测（settings 里放非法模型名 + env 有 `ANTHROPIC_MODEL` → 用 env 的；去掉 env 才用 settings 的） |

**关键约束**：Unix 上 launcher 用 `syscall.Exec` 替换进程（`internal/launcher/launcher_unix.go:13`），ccl 进程在 claude 启动的那一刻消失，**没有任何观察 Claude Code 输出的时机**；Windows 上走 `cmd.exe /c`（`internal/launcher/launcher_windows.go:34`），stderr 直通终端。任何"等 Claude Code 报错再处理"的方案在 Unix 上做不了。

## Goals / Non-Goals

**Goals:**

- provider 配置的非 `env` 顶层键真正到达 Claude Code，语义完全由 Claude Code 决定。
- 交付的 settings 对象**一定**被 Claude Code 接受：类型不对时在 ccl 侧拦住，绝不生成会被整体丢弃的文件。
- "provider 覆盖用户配置"成为显式契约：provider 配置的键赢，用户 settings.json 的同名键输，provider 未配置的键保持原状，ccl 不修改用户 settings 文件。
- provider 携带 `model` 时，真正生效（不被用户 shell 的 `ANTHROPIC_MODEL` 顶掉）。

**Non-Goals:**

- 不改权限模式的传递机制（继续用 `--permission-mode` flag）。虽然实测 `permissions.defaultMode` 可以从 `--settings` 设（`auto`/`bypassPermissions` 在 project/local 文件里不生效但从 `--settings` 生效），那是另一次改动。
- 不把 provider 的键写回 `~/.claude/settings.json`。
- 不让 `ccl provider add` 交互式录入非 `env` 键（先用手工编辑 provider 文件）。
- 不做 provider 配置的版本迁移（只有 `env` 的老 provider 文件照旧工作）。
- 不实现 managed settings 级别的策略（ccl 不部署企业策略）。

## Decisions

### 决策 1：schema 校验放在 ccl 侧，不依赖 Claude Code 的 stderr

**选择**：ccl 在写出 settings 文件前，按内置的 Claude Code settings 键/类型白名单校验 provider 配置；类型不符 → 打印明确错误并**不启动** claude。

**理由**：Unix 上 `syscall.Exec` 之后 ccl 进程消失，无法读取 claude 的 stderr（决策前提）。把校验前置到 ccl 侧，两个平台行为一致，且错误信息由 ccl 给出（比 Claude Code 的英文报错更可操作）。

**备选方案**：
- *Windows 上捕获 claude 的 stderr 匹配 `These settings files did not load`* —— 放弃：平台行为不一致，且 Unix 上做不到。
- *生成 settings 后调 `claude doctor` 校验* —— 放弃：doctor 读的是当前目录下的 settings 文件，不接受任意路径；每次启动多一次子进程调用。
- *干脆不校验，靠 Claude Code 丢弃整个文件* —— 放弃：这正是当前最危险的失败模式（provider 凭证随整个文件一起消失，Claude Code 静默退回用户自己的 provider）。

### 决策 2：白名单只做"明显写错"的判定，未知键一律透传

**选择**：内置一份 Claude Code settings 顶层键 → 类型（string/number/boolean/array/object）的白名单（来源：官方 settings-reference，标注抓取日期与 CLI 版本）。校验规则：

- 键在白名单内、类型与用户写的**大类**不符（如 `model` 是 string、用户给了 object）→ **报错，阻止启动**。
- 键在白名单内、类型相符 → 透传。
- 键不在白名单内 → 透传，**不校验**（与 Claude Code 自身"未知顶层键静默忽略"的行为一致，不制造假阳性）。
- `env` 块内的值不是字符串 → **转成字符串 + 警告**，不阻断。这条是实测结论（2026-10-30，Claude Code 2.1.308）：env 里的数字值不会让 Claude Code 丢弃整个文件，而是被它自己转成字符串写进进程环境，所以阻断在这里过严。

**理由**：白名单会有滞后（Claude Code 顶层键随版本增删）。若把未知键当错误，用户升级 Claude Code 后 ccl 会突然拒绝启动——这是不可接受的假阳性。反过来，已知键类型写错会**整文件丢弃**，必须拦。

**备选方案**：
- *只校验白名单内、类型不符时给警告不阻断* —— 放弃：警告会被忽略，失败仍然是静默的整文件丢弃，与本次目标相悖。
- *直接内嵌 json.schemastore.org 的 JSON Schema* —— 放弃：官方明确说该 schema "can lag behind the newest CLI releases"，且需要额外的 schema 解析依赖；用 Go 结构体表更轻更好维护。

### 决策 3：排除 ccl 自己的元数据键，provider 的其余顶层键全部透传

**选择**：provider 配置里排除三个键：`env`（单独走 env 通道）、`note`、`order`（ccl 自己的备注与排序元数据，见 `internal/provider/provider.go:26`）。其余顶层键原样进入交付对象。

**理由**：`note`/`order` 不是 Claude Code 的设置（173 个顶层键里没有它们），透传进去会被静默忽略——无害但无意义，且 `order` 这种通用词名有朝一日撞上真实键的风险。排除是零成本的防御。

**备选方案**：*把 `note`/`order` 改到 `ccl` 命名空间下（`ccl.note`）* —— 好设计，但会破坏现有 provider 文件的兼容性，属于另一次改动。

### 决策 4：`model` 键通过 env 块交付为 `ANTHROPIC_MODEL`

**选择**：provider 配置含顶层 `model` 键时，ccl 把该值作为 `ANTHROPIC_MODEL` 放进**交付的 env 块**（`--settings` 的 `env`），同时在进程环境里也 setenv 一份作为低优先级冗余。

**理由**（2026-10-30 真机实测，Claude Code 2.1.308 + 假 API 服务器）：

- `ANTHROPIC_MODEL` 环境变量压过任何文件里的 `model` 键（含 `--settings`）→ 只透传 `model` 键不够；
- 但用户 `~/.claude/settings.json` 的 **env 块**里的 `ANTHROPIC_MODEL` 又压过**进程环境** → 只 `os.Setenv` 也不够（实测：进程环境是 `shell-model`、用户 settings.json 的 env 块是 `stale-model` 时，请求里的 model 是 `stale-model`）；
- 放进 `--settings` 的 env 块 → 请求里的 model 是 provider 的值（实测）。

命令行层是优先级栈的最高层（仅次于 managed），所以 env 块是唯一能同时压过用户 settings 文件与进程环境的通道。

**边界**：provider 的 `env` 块里若自带 `ANTHROPIC_MODEL`，保留它不覆盖（两者都是 provider 自己的值，env 变量本来就压过 `model` 键）。

### 决策 5：进程环境里的 provider 副本降级为"daemon 转发用途"，不再是 provider 生效的保障

**选择**：保留"进程环境里也放一份 provider env"的现有行为（daemon 的 forwarder 需要它带着代理变量去连远端 collector），但**修改规格措辞**：这份副本不再是"provider 生效的兜底"。

**理由**：实测用户 settings.json 的 `env` 块**压过**进程环境。若 `--settings` 通道失效，用户 settings.json 里的 `ANTHROPIC_BASE_URL` 会盖住进程环境里那份 → 退化到"用户自己的 provider"，而不是"ccl 选的 provider"。把它叫兜底是虚假承诺。

**通道失效的处置**（让"退化"不可能发生）：
- 类型错误导致的失效 → 决策 1 的前置校验拦住，文件根本不会被生成。
- Claude Code 版本不支持 `--settings` → 启动前探测（决策 6）拦住，或由 claude 自己因未知参数报错退出（明确失败，不是静默退化）。

### 决策 6：启动前探测 Claude Code 是否支持 `--settings`（结果缓存）

**选择**：ccl 在启动 claude 前执行一次 `claude --help`，检查输出里是否有 `--settings`；结果按 claude 可执行文件的**路径 + 大小 + mtime** 缓存到 `~/.cc-launcher/cache/`。不支持 → 明确报错并给出升级提示。

**理由**：把"通道失效"从"运行到一半才发现"提前到启动前，给出可操作的提示（升级 Claude Code）。缓存避免每次启动多几百毫秒。

**备选方案**：*不探测，靠 claude 对未知参数报错* —— 也能算"明确失败"，但错误信息是 claude 的英文 unknown option，用户不一定知道该升级 Claude Code。探测的成本可控（缓存后为零），收益是错误可操作，故保留。

### 决策 7：交付对象的形状与 key 排序

**选择**：交付对象 = provider 的 `env` 块 + provider 的其余顶层键（排除 `note`/`order`）+ ccl 自己的 OTel env + 启动上下文（`OTEL_RESOURCE_ATTRIBUTES`）。env 块内 key 按字典序输出以保证内容稳定（现有 `buildLaunchSettingsJSON` 已有此约定），顶层键同样按字典序。

**理由**：内容哈希命名依赖"相同内容 → 相同文件"。key 顺序不确定会让同一份 provider 配置每次生成不同哈希 → 缓存文件堆积。顶层键排序是新增的确定性要求。

**冲突仲裁**：provider 的 env 键与 ccl 自己的 OTel/上下文键同名时，**ccl 自己的赢**（OTel 接管是 ccl 的核心职责，见 `cmd/cc-launcher/envconfig.go`）；provider 的非 env 顶层键与 ccl 自己的键同名时，同样 ccl 赢。

## Risks / Trade-offs

- **[白名单滞后导致假阳性]** → 只有"键在白名单内且类型大类不符"才阻断；未知键一律透传。白名单带来源与日期，后续版本可增量更新；`ccl provider validate <name>` 子命令供用户自查（不阻断启动）。
- **[透传的键与 Claude Code 未来语义冲突]** → 透传内容由用户显式配置，语义完全交给 Claude Code；ccl 只负责不把它弄丢。若某个键在 Claude Code 里被改义，ccl 不会察觉（这与手写 `~/.claude/settings.json` 的风险相同，可接受）。
- **[provider 文件里误放 `note`/`order` 之外的 ccl 内部键]** → 当前只有 `note`/`order` 两个 ccl 元数据键，排除清单固定在代码里；未来新增 ccl 元数据键时必须同步更新排除清单（在代码注释中写明这个约定）。
- **[探测 `--settings` 带来启动延迟]** → 首次约数百毫秒，之后按可执行文件指纹命中缓存；缓存缺失或指纹变化时重新探测。
- **[多 provider 场景下 `model` 键的意外影响]** → `ANTHROPIC_MODEL` 只在 provider 显式配置了顶层 `model` 时才设置；provider 不配 `model` 时，用户 shell 的 `ANTHROPIC_MODEL` 与 settings.json 的 `model` 行为完全不变（保持"provider 未配置的键保持原状"）。
- **[Claude Code 升级引入新的顶层键]** → 不在白名单的键照样透传并生效，白名单只影响"是否报错"，不影响"是否送达"。

## Migration Plan

- 无数据迁移。provider 文件格式向后兼容：只有 `env` 的老 provider 文件行为不变。
- 回滚：还原 ccl 二进制即可。回滚后 provider 文件里新增的非 `env` 键会被忽略（无害，不需要清理）。
- 灰度：校验失败时 ccl 报错退出而不是启动 claude，用户能立刻看到是哪个键、期望什么类型，改掉即可。

## Open Questions

- 是否要提供 `ccl provider validate <name>` 子命令？本 change 按"提供"实施（低成本、能显著降低用户困惑），若嫌 CLI 面太宽可以砍掉。
- `permissions.defaultMode` 走 `--settings`（而非 `--permission-mode` flag）留作后续 change，本次不动。
