# Design: 只接管 metrics，不碰兜底端点

## Context

v0.16.0（otel-endpoints-via-settings）把 OTel 接管搬进了 `--settings`，但交付了两个端点键：per-signal metrics 端点 + **兜底端点**。

兜底端点 `OTEL_EXPORTER_OTLP_ENDPOINT` 是"总配置"：**logs / traces 在没有 per-signal 端点时也走它**。而 daemon 是 metrics-only forwarder——`/v1/logs` `/v1/traces` 只回 200，不解析、不转发。于是接管兜底端点 = 把用户的 logs / traces 一起劫持进 daemon 并丢弃。

ccl 统计只涉及 metrics。**为一个信号去动总配置，代价是砍掉用户的日志链路。**

## 先纠正一个错误说法

v0.16.0 的 design D2 把"接管兜底端点"写成 metrics 拦截的**兜底**（"两条路都指向 daemon，SDK 行为变化时掉链子的保险"）。**这个说法是错的**：

metrics 拦截从头到尾只靠**per-signal metrics 端点**这一条——它**总是**被交付，而 OTel 规范规定 per-signal 端点**优先于**兜底端点（不是 Claude Code 的实现细节，是规范级保证，2026-10-28 实测亦复验）。

所以"接管兜底端点"没有任何收益，只有一个副作用：吞 logs / traces。

## Decisions

### D1: 交付策略表加 `deliver` 字段

原策略表的语义是"takeover → 总是交付 / 否则缺失才补"，无法表达"这个键**永不**交付"。加一个正交字段：

```go
type otelKeyPolicy struct {
	key          string
	defaultValue string
	deliver      bool  // false = 永不进交付的 env 块
	takeover     bool  // true = 不管用户写没写，都交付 defaultValue
	backupPrefix string
}
```

| Key | deliver | takeover | 说明 |
|---|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | **false** | false | 兜底 / 总配置 → 不交付 |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | true | **true** | 唯一的接管点 |
| `OTEL_METRICS_EXPORTER` | true | false | 缺失才补 `otlp` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | true | false | 缺失才补 `http/protobuf` |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | true | false | 缺失才补 `1` |
| `OTEL_LOGS_EXPORTER` | false | false | 永不交付 |

兜底端点键**仍留在表里**：遗留清理要用它的默认值判定"自我备份"（旧版 ccl 把兜底端点改写成 `localhost:4318` 再给自己备份的那种）。

### D2: 转发目标解析不变

转发目的地读的是**用户自己的原值**（`resolveForwardTarget`），跟交付什么无关：

- 用户配了 per-signal metrics 端点 → 转发到它（优先）
- 用户只配了兜底端点 → 转发到它（兜底，daemon 会补 `/v1/metrics` 后缀）

**注意**：这跟"交付哪个键"是两件事。交付决定"Claude Code 把 metrics 发给谁"，转发目标决定"daemon 把收到的 metrics 发给谁"。两者都指向用户的原值，恰好是同一个键集合——但语义不同，改动只碰前者。

### D3: 遗留清理规则不变

旧版 ccl 把兜底端点改写成 `localhost:4318` 留在用户文件里 → 清理时按"自我备份"规则删掉 live 键 ✓（这正是把总配置还给用户）。

## 各配置组合下的实际行为

| 用户 settings.json | metrics 发给谁 | logs 发给谁 | daemon 转发到哪 |
|---|---|---|---|
| 什么都没配 | per-signal → daemon | 不导出 | 不转发（无目标） |
| 只配兜底 `remote:4318` | per-signal → daemon ✓ | 兜底 → remote ✓ | remote ✓ |
| 只配 per-signal metrics `remote:4318/v1/metrics` | per-signal → daemon ✓ | 兜底 → SDK 默认 `localhost:4318` → daemon（丢弃） | remote ✓ |
| 两个都配 | per-signal → daemon ✓ | 兜底 → remote ✓ | per-signal ✓ |

对比 v0.16.0（接管兜底）：第 2、4 行的 logs 会被劫持进 daemon 并丢弃。

## Risks

| 风险 | 处置 |
|---|---|
| SDK 不再优先 per-signal 端点 → metrics 漏到用户远程 collector | OTel 规范规定 per-signal 优先（规范级保证，非实现细节）；实测复验 |
| 用户没配兜底端点时 logs 走 SDK 默认 `localhost:4318` → 到 daemon 被丢弃 | 这是用户配置不全（没告诉 Claude Code 去哪发日志），不是 ccl 造成的；ccl 不再加剧它 |
| 兜底端点写的是 gRPC 端口（4317）→ daemon 用 HTTP POST 失败 | 既存问题，与本次改动无关；daemon stderr 会报一行 |

## 真机验证（2026-10-29）

- **假远程 collector（192.168.1.20:43181）** 收到 `POST /v1/logs × 2`（Claude Code **直发**，不经 daemon）+ `POST /v1/metrics × 1`（**daemon 转发**）
- daemon 的 SQLite：`provider=probe` / `project=probe` → metrics 拦截 + launch context 都生效
- 结论：logs 回到用户自己的 collector，metrics 仍被 ccl 统计
