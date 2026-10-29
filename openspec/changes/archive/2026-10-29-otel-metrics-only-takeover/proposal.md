## Why

v0.16.0 把 OTel 端点接管搬到了 `--settings`，但**接管了两个端点**：

- `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（per-signal metrics）—— ccl 统计确实需要它
- `OTEL_EXPORTER_OTLP_ENDPOINT`（兜底 / 总配置）—— **ccl 统计完全不需要它**

兜底端点是"总配置"：logs / traces 在没有 per-signal 端点时**也走它**。接管它 = 把用户的 logs / traces 一起劫持进 daemon，而 daemon 按设计只回 200、**不解析也不转发** → **用户的远程 collector 静默收不到日志**。

ccl 的统计只涉及 metrics 一个信号。**为了 metrics 去劫持总配置，代价是砍掉用户的日志链路——范围搞宽了。**

## What Changes

**`--settings` 的 env 块不再交付兜底端点键**。ccl 只接管 metrics 这一个信号：

- 交付：`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics`（接管）+ `OTEL_METRICS_EXPORTER` / `OTEL_EXPORTER_OTLP_PROTOCOL` / `CLAUDE_CODE_ENABLE_TELEMETRY`（缺失时补默认值）
- **不交付**：`OTEL_EXPORTER_OTLP_ENDPOINT`（兜底 / 总配置）、`OTEL_LOGS_EXPORTER`

**metrics 拦截不受影响**：per-signal metrics 端点**总是**被交付，而 OTel 规范规定 per-signal 端点优先于兜底端点 → metrics 仍然一律到本地 daemon。**这里没有丢兜底**——v0.16.0 把"接管兜底端点"说成 metrics 拦截的兜底，那个说法是错的：metrics 拦截从头到尾只靠 per-signal 端点。

**logs / traces 的去向恢复成用户自己的配置**：
- 用户配了 per-signal logs/traces 端点 → 直达远程 collector（本来就不经 daemon）
- 用户只配了兜底端点 → logs 走兜底 → 直达远程 collector ✓（**不再被 daemon 吞掉**）

## Impact

- Affected specs: `claude-code-env-config`（交付表：兜底端点从"接管"改为"不交付"）
- Affected code: `cmd/cc-launcher/envconfig.go`（策略表加 `deliver` 字段，兜底端点 `deliver=false`）
- Unaffected: 转发目标解析（仍读用户的兜底 / per-signal 原值作为转发目的地）、daemon 侧代码、遗留清理规则、provider env 注入、Web 看板、db schema
- 用户可感知：**logs / traces 不再被 ccl 意外拦截**（如果之前配了兜底端点指远程 collector，之前那些日志是丢的）
