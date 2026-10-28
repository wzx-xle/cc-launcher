## Why

v0.15.3 把 launch context（provider / cwd / provider_endpoint）搬进了 `--settings`，但**只搬了一半**：

- launch context → `--settings` ✅
- OTel 端点接管 → 仍然靠**改写 `~/.claude/settings.json`** ❌

改写 settings.json 是一台机器上**全局**的副作用，代价有三层：

1. **裸 `claude` 被劫持**：settings.json 的 `env` 块对本机**所有** Claude Code 进程生效。用户直接敲 `claude`（没走 ccl）也会把 telemetry 打到 ccl 的 daemon，在统计里留下一堆 provider/cwd 全空的 "unknown" 会话。
2. **和用户的配置互相覆盖**：settings.json 的 `env` 块优先级**高于进程环境**。ccl 为了让端点接管生效，必须把用户自己的 `OTEL_EXPORTER_OTLP_ENDPOINT` 备份到 `CC_ORIG_*`、把 live key 改写成 `localhost:4318`。于是用户文件里长期躺着两份历史（live + `CC_ORIG_*`），还得靠 `ccl serve --restore` 手工收拾。
3. **Windows 上的一个硬伤**：Windows 用户反馈 `~/.claude/settings.json` 的 `env` 覆盖了 ccl 注入的进程环境 —— 同一个坑，同一个根因（改文件 vs 传参）。

第 3 点已经在 v0.15.3 用 `--settings` 解决了一半（launch context）。剩下的一半（OTel 端点）必须用同一招解决，否则两个通道语义不一致，改一个漏一个。

## What Changes

**ccl 不再写 `~/.claude/settings.json` 的任何 OTel key。** 6 个 OTel key 全部通过 `--settings` 的 `env` 块交付，和 launch context 走同一个通道、同一个 settings 对象。

- **接管**：`OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 在 `--settings` 里被改写成 ccl 的本地 daemon 地址（`--settings` 优先级高于 settings 文件，用户原值不动也能被接管）。
- **尊重**：`OTEL_METRICS_EXPORTER` / `OTEL_EXPORTER_OTLP_PROTOCOL` / `CLAUDE_CODE_ENABLE_TELEMETRY` / `OTEL_LOGS_EXPORTER`，用户自己在 settings 文件里写了就照用（不交付这个 key，让 settings 文件的值生效）；没写才由 ccl 补默认值。
- **转发**：用户原来的端点值（fallback / per-signal metrics）从 settings 文件读出来，写进 ccl 自己的进程环境 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` / `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`，daemon fork 时继承 → 远程 collector 的转发链路不变，**daemon 侧代码一行不改**。
- **一次性清理**：旧版 ccl 留在 settings 文件里的 `CC_ORIG_*` / `FORWORD_*` 备份键、以及被改写掉的端点，在升级后的**第一次** ccl 启动时自动还原成用户原值并删掉（备份先落盘，可回滚；只跑一次，state.json 记标志）。
- **`ccl serve --restore`** 语义收敛成"清理旧版 ccl 的遗留痕迹"，不再承担"撤销当前接管"（当前接管根本不再落盘）。

**行为变化（必须写进文档）**：裸 `claude` 会话不再被 ccl 记录 —— 因为 ccl 不再往 settings 文件里写全局端点，Claude Code 没有端点就根本不导出。用户自己配了 `OTEL_EXPORTER_OTLP_ENDPOINT` 的话，裸 `claude` 照旧发给他自己的 collector，只是**不再经过 ccl 的 daemon**。

## Impact

- Affected specs: `claude-code-env-config`（重写注入/还原/转发来源）、`provider-setenv-migration`（provider 迁移与 OTel 清理的边界）、`session-context-attrs`（裸 claude 的会话从"unknown 桶"变成"根本不导出"）
- Affected code: `cmd/cc-launcher/envconfig.go`（策略表重写）、`cmd/cc-launcher/main.go`（`injectOTelEnv` → 只读 + 进程环境投递）、`cmd/cc-launcher/launchctx.go`（`--settings` env 块合并 OTel key）、`cmd/cc-launcher/statscmd.go` + `serve.go`（`--restore` 语义）、`internal/state`（一次性标志）
- Unaffected: `internal/collector/**`（daemon 的转发契约一个字不动）、provider env 注入（`provider-setenv-migration`）、Web 看板
- 用户可感知：settings.json 不再被 ccl 动；裸 claude 不再进统计；旧版用户升级后第一次启动会看到一行"清理旧版 ccl 遗留"的提示
