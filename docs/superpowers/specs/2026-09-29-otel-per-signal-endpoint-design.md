# Design: OTel per-signal endpoint 兼容（telemetry-config-compat）

## Why

ccl-launcher 已实现"用量监控 via OTel"（归档于 `openspec/changes/archive/2026-09-29-usage-monitoring-via-otel/`），其中 ccl 注入 5 个 OTel env key 到 `~/.claude/settings.json` 的 `env` 块。

但 Claude Code v2.1.x 的 OTel SDK 按以下优先级决定 endpoint：

1. `OTEL_EXPORTER_OTLP_{METRICS,LOGS,TRACES}_ENDPOINT`（per-signal，最优先）
2. `OTEL_EXPORTER_OTLP_ENDPOINT`（fallback）

ccl 当前只覆盖 fallback，未碰 per-signal。**结果**：用户在 settings.json 里配齐 per-signal endpoint 时，Claude Code 把 metrics/logs/traces 都直接推到远程 collector，**完全绕过本地 ccl daemon** → `ccl stats` 拿不到数据。

实测复现（用户原配置）：

```jsonc
"env": {
  "OTEL_EXPORTER_OTLP_ENDPOINT":            "http://172.31.242.75:4318",
  "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":    "http://172.31.242.75:4318/v1/metrics",
  "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":       "http://172.31.242.75:4318/v1/logs",
  "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":     "http://172.31.242.75:4318/v1/traces",
  // ... 7+ 个高级配置（export interval / resource attrs / log content 级别等）
}
```

ccl 当前注入后，Claude Code 仍走 per-signal endpoint → 三个 signal 直达远程 → ccl stats 空。

## What Changes

最小化变更以解决兼容问题（用户要求）：

### 新增
- `envconfig.go` 策略表加 1 个 key：`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
  - 用户原值非空 → 备份到 **`FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`**（新命名空间，不用 CC_ORIG_）+ 改写为 `http://localhost:4318/v1/metrics`
  - 用户原值缺失 → 不注入（Claude Code 用 fallback）
- `envconfig.go` `restoreOTelEnv` 加 1 个 key：还原 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` → 原值，删除备份 key

### 不动
- 现有 5 个 fallback key 注入逻辑（`OTEL_EXPORTER_OTLP_ENDPOINT`、`OTEL_LOGS_EXPORTER`、`OTEL_METRICS_EXPORTER`、`OTEL_EXPORTER_OTLP_PROTOCOL`、`CLAUDE_CODE_ENABLE_TELEMETRY`）继续用 `CC_ORIG_*` 命名空间
- `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`、`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` 用户原值直达 remote（ccl 不接管）
- 任何 `OTEL_*` 用户高级配置（export interval / resource attrs / protocol 等）

### 改
- `internal/collector/forwarder.go`：
  - 触发条件从 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` → `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
  - 仅当该 env 非空且非 localhost 时启用 **metrics-only** forwarder
  - auth 仍走 `CC_FORWARD_AUTH_HEADER`（命名空间保留）
- `internal/collector/server.go`：
  - `/v1/logs` `/v1/traces` handler：从"200 + forward"改为 **"200 only"**（用户上游已直达 remote，daemon 再转发是浪费 + 重复）
  - `/v1/metrics` handler：parse + persist + 转发（如启用 forwarder）

## Design Decisions

### 1. 仅接管 metrics per-signal，不接管 logs/traces
**理由**：logs/traces 用户**已经**显式推到 remote 自己的 collector（`172.31.242.75:4318/v1/{logs,traces}`）。Claude Code SDK 按优先级用 per-signal endpoint，所以这些**已经**直达 remote — ccl 拦截再转发纯属浪费带宽 + 加一层失败点。

ccl 唯一**必须**接管的信号是 metrics（让 `ccl stats` 有数据）。其它用户配置原状保留。

### 2. 新 backup 命名空间用 `FORWORD_*`，不复用 `CC_ORIG_*`
**理由**：用户原话"备份 key 也不要用 cc_orig，改用 forword 前缀"——用户的 settings.json 命名空间需保持他自己的 OTel 风格（`OTEL_*`），ccl 的 backup key 不能污染他自己的命名空间认知。新名字 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 与 `OTEL_*` 同前缀，ccl 注入时看起来像用户自己的 forwarder 配置。

**未触及**现有 `CC_ORIG_*`（5 个 fallback key 仍在用），因为"其他的都不动"——不重命名已注入的 key（避免双重历史遗留）。

### 3. forwarder 改为 metrics-only + 触发条件对齐
**理由**：与"只做 metrics 的转发"一致。原先 fallback forwarder 转 3 个 signal；现在 logs/traces 已直达 remote，**只 metrics 还需要 forwarder** 把 daemon 解析后的 metrics 再转到 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`。

触发条件对齐语义：原先看 fallback 是否被改写（间接证明有远程 endpoint），现在看 `FORWORD_*` 是否存在（直接证明用户配置了 metrics remote forwarding）。

### 4. `/v1/logs` `/v1/traces` 改 200 only
**理由**：Claude Code SDK 走 per-signal endpoint 直达 remote 后，**不会再推到 daemon 的 :4318**（除非用户改了 URL）。如果 Claude Code 因 fallback 走到 daemon `/v1/logs`（用户 fallback 改写为 localhost），daemon 返回 200 + 不持久 + 不 forward——客户端拿到 200，OK。这保留了"daemon 接受 OTel 任何路径"的兼容性，但不再转发（不浪费）。

### 5. 不动 fallback 注入（仍改写 + CC_ORIG_ 备份）
**理由**：用户已选"保留 fallback 改写"。当用户**没**设 per-signal metrics endpoint 时，ccl 仍把 fallback 改写到 localhost:4318，让 Claude Code 走 fallback → metrics 到 daemon → `ccl stats` 有数据。这路径与现有 5 key 注入逻辑一致，**无新代码**。

## Data Flow

### 场景 A：用户有 per-signal metrics + logs endpoint（典型公司远程 collector）

注入后 settings.json：
```jsonc
"env": {
  // 现有 5 key + CC_ORIG_ 备份（不变）
  "OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
  "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT": "http://172.31.242.75:4318",
  "OTEL_METRICS_EXPORTER":               "otlp",
  "OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
  "CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
  // logs_exporter 用户原本为 otlp → 不动

  // 新增 per-signal metrics endpoint 处理
  "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":         "http://localhost:4318/v1/metrics",   // 改写
  "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://172.31.242.75:4318/v1/metrics",  // 备份

  // 用户原值不动
  "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://172.31.242.75:4318/v1/logs",
  "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://172.31.242.75:4318/v1/traces"
}
```

运行时：
1. Claude Code → `POST localhost:4318/v1/metrics`（per-signal 改写）
2. daemon → parse + persist 到 SQLite + 异步 POST 到 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（= 172.31.242.75:4318/v1/metrics）
3. 远程 collector 收到 metrics ✓
4. `ccl stats` 从 SQLite 读 → 显示 token / cost ✓
5. Claude Code → `POST 172.31.242.75:4318/v1/logs`（per-signal 未动）→ 直达远程 ✓
6. Claude Code → `POST 172.31.242.75:4318/v1/traces`（per-signal 未动）→ 直达远程 ✓

### 场景 B：用户没设 per-signal metrics（ccl stats 仍 work）

注入后 settings.json：
```jsonc
"env": {
  // 现有 5 key + CC_ORIG_ 备份（不变）
  "OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
  "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT": "http://172.31.242.75:4318",
  // ...
  // 没 per-signal metrics endpoint key — ccl 不注入
  "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://172.31.242.75:4318/v1/logs",  // 用户原值
  "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://172.31.242.75:4318/v1/traces",  // 用户原值
}
```

运行时：
1. Claude Code → 优先用 per-signal logs/traces endpoint（直达 remote）✓
2. Claude Code → 没 per-signal metrics → 用 fallback `localhost:4318` → daemon `/v1/metrics` → 本地 stats ✓

### 场景 C：用户从未碰过 OTel settings（最简情况）

注入后 settings.json：
```jsonc
"env": {
  // 现有 5 key + CC_ORIG_ 备份（不变，与 cccl-fallback 一致）
  "OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
  "OTEL_METRICS_EXPORTER":               "otlp",
  "OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
  "CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
  "OTEL_LOGS_EXPORTER":                  "otlp"
  // 没 per-signal key（ccl 不注入，Claude Code 用户没配）
}
```

运行时：与现状完全一致 — Claude Code 走 fallback → daemon → 本地 stats + forwarder（CC_ORIG 触发）→ remote 收 3 个 signal。

### 场景 D：`ccl restore`

settings.json 还原：
1. `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` → 还原为 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`；删除 backup key
2. `CC_ORIG_*` 5 个 key 还原原值 + 删除 backup（与原 cccl-restore 逻辑一致）
3. 删除 ccl 注入的默认值（如 `OTEL_METRICS_EXPORTER=otlp` 无 CC_ORIG_ 备份）
4. 保留用户自有 key（如 `ANTHROPIC_API_KEY`、`OTEL_LOGS_EXPORT_INTERVAL` 等）

## Component Changes

| 文件 | 改动 |
|---|---|
| `cmd/cc-launcher/envconfig.go` | 策略表加 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`；`restoreOTelEnv` 加对应还原 |
| `internal/collector/forwarder.go` | 触发条件 env 常量改名 `envOrigEndpoint` → `envForwardMetricsEndpoint`；URL 拼接改为 `/v1/metrics` 固定 |
| `internal/collector/server.go` | `handleForwardOnly` 改名 `handleAckOnly`（200 only），不再 forward；删 forwarder 参数；移除 forwarder 调用 |
| `cmd/cc-launcher/serve.go` | 调用 `NewServer` 时不再传 Forward；或保留传但 forwarder 行为已 check new env |
| `cmd/cc-launcher/main.go` | `injectOTelEnv` 在 ccl 进程 env 设 `FORWORD_*`（让 fork 的 ccl serve 继承） |

> 注：现有 forwarder 仍需保留——cccl-archive 后的代码里 `/v1/metrics` handler 仍调 `s.fwd.Forward("metrics", ...)`。forwarder 触发 env 从 CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT 改到 FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT，影响 forwarder 启用判定逻辑，不影响 handler 代码。

## Risks / Trade-offs

- [backward compat] 现有用户 settings.json 里 5 个 CC_ORIG_* 不变；新增 1 个 FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT（用户有 per-signal 时才出现）——**无破坏性**
- [forwarder 行为变更] daemon 现在只 forward metrics（如启用），不再 forward logs/traces。**用户 logs/traces 收集不再经过 ccl**——若用户想用 ccl 做 logs 转发，ccl 不支持（决策 1）
- [restore 范围扩展] `ccl restore` 新增 `FORWORD_*` 还原——确保 idempotent
- [decision 文档] 原 decision 5（"metrics 持久化、logs/traces 仅转发"）需要更新为"metrics 持久化 + 转发（如启用）、logs/traces 不再处理（用户上游直达）"——属 sibling change 范围

## Migration Plan

无状态迁移（新建 ccl 配置）：

1. 用户首次跑新版本 ccl → 启动后 injectOTelEnv 检查 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`，如存在则备份+改写
2. 用户已有的 `CC_ORIG_*` 5 个 key **不动**（兼容）
4. `ccl restore` 一键还原（含新 backup key）
5. 无需任何文档破坏性变更

## Out of Scope

- `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` / `TRACES_ENDPOINT` 改写（用户原话"其他的都不动"）
- `OTEL_LOG_*`、`OTEL_RESOURCE_ATTRIBUTES`、`OTEL_METRIC_EXPORT_INTERVAL` 等高级 OTel 配置读取（用户原话"尽量减少改动"）
- `OTEL_EXPORTER_OTLP_{METRICS,LOGS}_PROTOCOL` 改写（用户已显式设为 `http/protobuf`，与 fallback 协议不冲突；ccl 当前也不管这些）
- 把 forwarder 改为支持任意 signal scope（用户原话"只做 metrics 的转发"）

## Open Questions

- TUI 多端点配置引导（用户写 `ccl provider --new` 时是否问 per-signal metrics endpoint）—— 不做，留给后续
- 历史 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 是否保留 fallback forwarder 兜底（用户没 per-signal 时靠 fallback forwarder 转 logs/traces 到 remote）—— **当前设计：保留**（fallback 触发逻辑不变；只有 per-signal metrics 路径才用新的 FORWORD_ 触发）。如果用户有 per-signal metrics endpoint + 没 per-signal logs endpoint，logs 会走 fallback（被改写到 localhost）→ 走到 daemon `/v1/logs` → 200 only — Claude Code 以为推到 remote fallback URL 实际收到 200 — **可以接受**

## Spec Changes (Sibling to usage-monitoring-via-otel)

- `specs/otel-receiver/spec.md`：
  - Scenario "claude code pushes logs" 改：daemon 返回 200，不再 forward
  - Scenario "claude code pushes traces" 改：同上
  - 加 Scenario "forwarder triggers on FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
- `specs/claude-code-env-config/spec.md`：
  - 加 Requirement "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT 也被 ccl 备份+改写"
  - 加 Scenario "典型公司远程 collector 配置"（用用户原 settings.json 做 example）
  - 加 Scenario "ccl restore 还原 FORWORD_*"

## Implementation Tasks (预估)

参考 sibling change `usage-monitoring-via-otel` 的拆分：

1. **§1 envconfig**：envconfig.go 加 per-signal metrics key 策略（约 30 行）
2. **§2 forwarder + server**：forwarder 触发 env 改名；server `/v1/logs` `/v1/traces` handler 改 200 only（约 40 行）
3. **§3 main.go**：injectOTelEnv 同步 FORWORD_* 到进程 env（约 10 行）
4. **§4 测试**：envconfig 加 2 case；forwarder 加 metrics-only test；server handler 改 200 only test（约 50 行）
5. **§5 spec 更新**：spec scenarios 增改（约 50 行）
6. **§6 docs**：README 描述新行为（`requests.forword` 命名空间 + per-signal 接管语义）
8. **§7 commits**：3-4 个 logical commit