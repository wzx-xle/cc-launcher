## Why

ccl-launcher 已经在做"用量监控 via OTel"——ccl 启动时改写 `~/.claude/settings.json` 里的环境变量，让 Claude Code 把运行数据推到本地 daemon，daemon 存进 SQLite（这样 `ccl stats` 才能查）+ 转发到用户自己的远程 collector。

但有个漏洞。Claude Code v2.1.x 在选 endpoint 时有优先级：

1. 先看分类型端点（`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`、`..._LOGS_ENDPOINT`、`..._TRACES_ENDPOINT`）
2. 没有才看兜底（`OTEL_EXPORTER_OTLP_ENDPOINT`）

ccl 现在**只改了兜底那一项**，分类型一个都没动。结果：用户在自己 settings.json 里写齐分类型端点（典型公司远程 collector 配置就是这样），Claude Code 会优先用分类型端点，metrics 直接推到远程 → **完全绕过本地 daemon** → `ccl stats` 永远是空的。

怎么修：只接管分类型里的 **metrics** 一个端点。logs/traces 用户本来就写好了去自己的远程 collector，已经直达了，ccl 再插一脚只是浪费流量 + 多一个可能挂的点。`ccl stats` 只需要 metrics 就够了。

## What Changes

- **新增**（envconfig.go 策略表加一行）：分类型 metrics 端点 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
  - 用户已经写了这个值 → 备份到一个**新名字** `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（用新命名空间，不用老的 `CC_ORIG_*`，原因见 design 决策 2），然后改写成 `http://localhost:4318/v1/metrics`，让 Claude Code 走本地 daemon
  - 用户没写 → 不动（让 Claude Code 自己 fallback 到兜底端点）
- **新增**（envconfig.go 加 restore 逻辑）：`ccl restore` 时把 `FORWORD_*` 还原回原值并删除 backup 键
- **修改**（forwarder.go）：触发 forwarder 的环境变量从 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 改为 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`；转发目标 URL 固定成 `/v1/metrics`（不再按类型拼路径）
- **修改**（server.go）：`/v1/logs` 和 `/v1/traces` 这两个端点的处理函数从"收下 + 转发"改成"只回 200，啥也不做"（用户的上游已经直达远程了）。函数顺便改名 `handleForwardOnly` → `handleAckOnly`，让代码意图更清楚。`/v1/metrics` 不变
- **新增**（main.go）：ccl 改完 settings.json 后，把 `FORWORD_*` 也 `os.Setenv` 到自己进程环境变量里——这样后面 fork 出来的 `ccl serve` 子进程才能从 `os.Environ()` 里拿到这个备份键

**保持不动的部分**（兼容性）：
- 现有 5 个兜底键的注入逻辑（`OTEL_EXPORTER_OTLP_ENDPOINT`、`OTEL_LOGS_EXPORTER`、`OTEL_METRICS_EXPORTER`、`OTEL_EXPORTER_OTLP_PROTOCOL`、`CLAUDE_CODE_ENABLE_TELEMETRY`）继续用 `CC_ORIG_*` 命名空间，**不重命名**——避免双重历史
- `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` / `TRACES_ENDPOINT` 用户原值直达远程，ccl 不碰
- 任何 `OTEL_*` 高级配置（export interval / resource attrs / protocol 等）都不动
- `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 作为兜底转发触发条件保留——如果用户只配了兜底端点没配分类型 metrics，他的 metrics 还是要能转出去

## Capabilities

### New Capabilities
无（这次都是改现有 spec，不新建）

### Modified Capabilities
- `otel-receiver`：`/v1/logs` `/v1/traces` 这两个端点的行为从"200 + 转发"改成"只回 200"；forwarder 触发条件从 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 改成 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（带兜底优先级）；加几个 metrics-only forwarder 的测试场景
- `claude-code-env-config`：新增分类型 metrics 端点处理；`ccl restore` 还原 `FORWORD_*` 备份；加"典型公司远程 collector 配置"场景

**Spec 影响**：
- `openspec/specs/otel-receiver/spec.md`：1 个 requirement 大改 + 1 个 requirement 新增
- `openspec/specs/claude-code-env-config/spec.md`：3 个 requirements 大改 + 1 个 requirement 新增

## Impact

**代码改动量**（约 80 行 + 测试）：
- `cmd/cc-launcher/envconfig.go`：策略表加一行 + `restoreOTelEnv` 加对应还原（约 30 行）
- `cmd/cc-launcher/main.go`：`injectOTelEnv` 末尾同步 `FORWORD_*` 到进程 env（约 10 行）
- `internal/collector/forwarder.go`：触发 env 常量改名 + 优先级逻辑 + URL 固定（约 20 行）
- `internal/collector/server.go`：`/v1/logs` `/v1/traces` handler 改 200 only（约 20 行）

**测试**（约 50 行）：
- envconfig 加 2 个 case（per-signal metrics 端点有/无）+ restore 1 个 case
- forwarder 加 3 个 case（优先级、兜底、localhost 跳过）
- server handler 加 2 个 200-only case
- 集成测试 3 个场景（A 典型配置、B 仅兜底用户、C 全新用户）+ restore 端到端

**文档**：加 README 一段说明 `FORWORD_*` 命名空间由来 + per-signal 接管范围；envconfig.go 顶部加一行注释解释为啥 metrics 用新名字其他用老的

**兼容性**（无破坏性）：
- 用户既有的 5 个 `CC_ORIG_*` 备份键全部保留
- 新增的 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 只在用户配了分类型 metrics 端点时才会出现
- `ccl restore` 自动覆盖新键，幂等
- 兜底 forwarder 路径保留——只配了兜底没配分类型 metrics 的用户，metrics 仍能转出