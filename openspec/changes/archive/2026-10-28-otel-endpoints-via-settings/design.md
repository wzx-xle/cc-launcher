# Design: OTel 端点接管改走 `--settings`

## Context

v0.15.3（launch-context-settings-injection）已经把 launch context 搬到 `--settings`，但 OTel 端点接管仍然靠改写 `~/.claude/settings.json`。这导致两个通道语义不一致，并且把 ccl 的接管变成了**机器全局**的副作用（裸 `claude` 也被劫持）。

本 change 把第二个通道也搬到 `--settings`，并拆掉"因为要改文件才需要存在"的一整套机制：备份键（`CC_ORIG_*` / `FORWORD_*`）、控制权交接备份、`isUnderCclControl`。

## 三块地基（2026-10-28 真机实测，非推断）

在动代码前，用三个探针程序（`/tmp/cclprobe/`）对着**真实的 claude 二进制**验过三件事：

1. **`--settings` 的 `env` 逐键覆盖 settings 文件**。settings.json 写 `OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:43181`、`--settings` 写 `...=http://127.0.0.1:43182` → metrics **只**到了 43182，43181 一个都没收到。同时 settings.json 里 `OTEL_METRICS_EXPORTER` / `OTEL_EXPORTER_OTLP_PROTOCOL` / `CLAUDE_CODE_ENABLE_TELEMETRY` 照常生效（metrics 确实被导出了）。
   → 结论：接管可以在 `--settings` 里做，且**不会**破坏用户 settings 文件里的其它 key。

2. **纯 `--settings` 也能独立完成一次导出**。settings.json 全空（一个 OTel key 都没有），只靠 `--settings` 的 `env` 块给出端点 + exporter + telemetry 开关 → metrics 正常到达 `POST /v1/metrics`。
   → 结论：ccl 不需要 settings 文件里"已经有什么"就能接管整个 OTel 配置。

3. **per-signal 端点优先于 fallback**。settings.json 里 fallback=`43181` 且 `OTEL_LOGS_EXPORTER=otlp`，`--settings` 里给 per-signal metrics=`43182/v1/metrics` → metrics 去了 43182，logs 仍走 fallback 43181。
   → 结论：ccl 可以**只劫持 metrics**，把 logs/traces 留给用户自己的 collector。

另外一个观察：**没有端点时 Claude Code 根本不导出**（裸 claude 在没有 endpoint 的情况下不产生任何 OTLP 请求）。
→ 这就是"裸 claude 不再被记录"这条行为变化的物理依据，也是它能被写进 spec 的依据。

## Decisions

### D1: OTel key 的投递通道 = `--settings` 的 `env` 块（与 launch context 同一个 settings 对象）

`--settings` 的优先级是 **--settings > settings 文件 > 进程环境**，且 `env` 块是**逐键深合并**（不是整体替换）。这正好满足接管需要：只覆盖要接管的键，其余键让 settings 文件继续说话。

落地：`launchctx.go` 的 `buildLaunchSettingsJSON` 从"只写 `OTEL_RESOURCE_ATTRIBUTES`"改成"写 `OTEL_RESOURCE_ATTRIBUTES` + 6 个 OTel key"，仍然只输出一个 `env` 块（settings 文件里其它非 env 设置不受影响）。

### D2: 6 个 key 的策略（`envconfig.go` 的策略表重写）

| Key | 用户在 settings 文件里写了 | 没写 |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 交付 `http://localhost:4318`（接管） | 交付 `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | 交付 `http://localhost:4318/v1/metrics`（接管） | 交付 `http://localhost:4318/v1/metrics` |
| `OTEL_METRICS_EXPORTER` | **不交付这个键**（settings 文件的值生效） | 交付 `otlp` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | 不交付 | 交付 `http/protobuf` |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | 不交付 | 交付 `1` |
| `OTEL_LOGS_EXPORTER` | 不交付 | 不交付（不注入，daemon 不解析 logs） |

两条要点：

- **"尊重用户"= 不交付这个键**，而不是"交付用户的值"。因为 `--settings` 优先级最高，一旦交付就等于覆盖 settings 文件；不交付，settings 文件里的值自然生效（D1 的深合并）。
- **"用户值"的定义域只有 settings 文件的 `env` 块**，不含进程环境。这跟旧实现等价：旧实现把默认值写进 settings 文件，settings 文件的优先级高于进程环境，等于"进程环境里的值被默认值盖掉"；新实现直接在更高优先级的 `--settings` 里交付默认值，效果一致，但**不碰用户的文件**。

per-signal metrics 端点从"用户没写就不注入"改成"总是注入"：两者指向同一个地址（fallback `localhost:4318` + SDK 自动补 `/v1/metrics` ≡ per-signal `localhost:4318/v1/metrics`），行为等价，但显式写出来更不容易在 SDK 行为变化时掉链子。

### D3: 转发目标从"备份键"改成"用户原值 + 进程环境投递"

daemon 侧（`internal/collector/forwarder.go`）的契约**不动**：

- `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（per-signal，优先）
- `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`（fallback，兜底）
- 两者都指回环 → 禁用 forwarder（防环）

变的是**这两个变量从哪来**：

- 旧：ccl 写进 settings.json（`CC_ORIG_*`），再把同样的值塞进自己的进程环境，让 fork 出来的 daemon 继承。
- 新：ccl **只读** settings.json，取出用户的原值（fallback 端点 / per-signal metrics 端点），直接塞进自己的进程环境。daemon fork 时继承 → forwarder 照旧工作。

于是 `CC_ORIG_*` / `FORWORD_*` 不再是"settings 文件里的备份键"，而只是"ccl 进程环境里的转发目标变量"。名字保留是为了**不碰 daemon 代码、不碰 otel-receiver spec**。

一个兼容细节：如果 settings 文件里还残留着旧版的 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`，那才是用户真正的原值（live key 已被旧版改写成 localhost），必须**优先于** live key 取用。这覆盖了"清理没跑成功/还没跑"的时间窗。

### D4: 旧版遗留的一次性清理

旧版 ccl 在 settings 文件里留了两类痕迹：`CC_ORIG_*` / `FORWORD_*` 备份键，和被改写成 `http://localhost:4318` 的 live 端点。新实现不再写文件，但**必须把旧版留下的东西收拾掉**，否则：

- 用户的真实端点被埋在 `CC_ORIG_*` 里 → 转发目标错；
- 裸 claude 继续往 ccl 的 daemon 发 → 统计里永远有一堆 unknown 会话。

清理规则（`ccl serve --restore` 与自动迁移共用同一份实现）：

1. 有 `CC_ORIG_<KEY>` / `FORWORD_<KEY>` → 把备份值写回 `<KEY>`，删掉备份键。
   - **例外**：备份值恰好等于 ccl 自己的默认值 → 说明这个"备份"是旧版 ccl 给自己注入的值做的备份（自我备份），用户的原值并不存在 → 删掉 live 键而不是还原成默认值。
2. 没有备份键、且 live 值等于 ccl 的默认值 → 删掉（是 ccl 注入的，用户从来没有过）。
3. 其余键一律不动（用户的 `ANTHROPIC_*`、permissions、plugins…）。

触发条件（有歧义，宁可不动）：settings 文件里出现 `CC_ORIG_*` / `FORWORD_*` 键，**或者** live 端点等于 `http://localhost:4318`。没有触发条件就不碰用户的文件 —— 一个从没装过旧版 ccl 的用户，哪怕自己写了 `OTEL_METRICS_EXPORTER=otlp`，也不会被误删。

自动迁移**只跑一次**，标志记在 `state.json`（`otel_legacy_cleaned`）。理由：清理规则里有"值等于默认值就删"这种启发式，如果每次启动都跑，用户事后自己写进去的 `OTEL_METRICS_EXPORTER=otlp` 会被反复误删。跑一次就停。

清理前照例 `config.Manager.Backup()` 落一份文件级快照（和 provider 迁移一致），失败只警告不阻断。

### D5: `ccl serve --restore` 收敛成"清理旧版遗留"

旧语义："撤销 ccl 当前的接管，把 settings.json 还原到 ccl 之前的状态"。
新语义：ccl 当前**根本没有接管落盘**，所以"撤销接管"无对象；`--restore` 变成"清理旧版 ccl 在 settings.json 里的遗留痕迹"，即 D4 的清理逻辑，手动触发版（幂等）。自动迁移跑过之后它就是 no-op。

### D6: 删掉控制权交接备份

`isUnderCclControl` + "不在 ccl 控制下就 Backup 一次"这套机制**只因为要改写 settings.json 才需要**（怕把用户的文件改坏）。新实现不改文件 → 整套删掉，`isUnderCclControl` 这个函数也没有存在意义了。

连带删掉 `backupAndInjectOTel`（备份 + 改写合一）。`restoreOTelEnv` 保留并扩成 D4 的清理实现。

## Risks

| 风险 | 处置 |
|---|---|
| SDK 改优先级（per-signal 不再优先） | 兜底端点**也**在 `--settings` 里接管，两条路都指向 daemon |
| 老用户升级后转发目标丢失 | D4 的迁移把 `CC_ORIG_*` 还原回 live key；D3 的"备份键优先"再兜一层 |
| 清理误删用户自己的 key | 只在检测到 ccl 痕迹时触发；只跑一次；删除前落快照 |
| 裸 claude 不再被记录 | 这是本次改动的**目的**（消除 unknown 会话），写进 README + CHANGELOG |
| Windows `--settings` 内联 JSON 被 cmd.exe 剥引号 | 沿用 v0.15.3 的方案：Windows 写临时文件，Unix 内联 |

## Migration Plan

1. 用户升级到新版本
2. 第一次跑 `ccl`：检测到旧版痕迹 → 落快照 → 清理 → 打印一行提示 → state.json 记标志
3. 之后：settings 文件里只剩用户自己的配置，ccl 每次启动只读不写
4. 旧的 `ccl serve --restore` 变成手动清理入口，保留兼容

## Open Questions

无。用户已确认："在不改原值任何配置的情况下，把 otel 的配置加进去，而且目前通过参数化传变量的逻辑都能在这里统一处理。"
