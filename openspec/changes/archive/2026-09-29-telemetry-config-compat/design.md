# Design: OTel per-signal endpoint 兼容

## Context

ccl-launcher 已经在做"用量监控 via OTel"这件事（详细见已归档的 `usage-monitoring-via-otel`）。简单说就是：ccl 启动时会改写 `~/.claude/settings.json` 里的几个环境变量，让 Claude Code 把运行数据推到本地 daemon（`localhost:4318`），daemon 负责两件事——把数据存到本地 SQLite（这样 `ccl stats` 才有东西可查）+ 把数据转发到用户自己的远程 collector。

**问题出在哪**：Claude Code v2.1.x 的 OTel SDK 在选 endpoint 时是按优先级来的：

1. 先看有没有专门的"分类型"端点（`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`、`..._LOGS_ENDPOINT`、`..._TRACES_ENDPOINT`）—— 谁有就用谁
2. 没有的话才用通用的 `OTEL_EXPORTER_OTLP_ENDPOINT` 兜底

ccl 当前只改写了兜底那一项，没动分类型那一项。所以这种典型用户配置：

```jsonc
"env": {
  "OTEL_EXPORTER_OTLP_ENDPOINT":            "http://remote:4318",
  "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":    "http://remote:4318/v1/metrics",
  "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":       "http://remote:4318/v1/logs",
  "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":     "http://remote:4318/v1/traces",
  // 7+ 高级配置（export interval / resource attrs / protocol 等）
}
```

Claude Code 会优先用分类型端点，metrics 直接推到了远程 `remote:4318` —— **完全绕过本地 daemon**。结果 `ccl stats` 看不到任何数据。

约束（用户原话）：
- **尽量少改动**
- 别动现有 `CC_ORIG_*` 这一套（已经有用户在那里了，不要再去动他们的认知）
- 新备份键的命名风格要跟用户自己的 OTel 配置一致（用户说："不要叫 cc_orig，改叫 forword"）

## Goals / Non-Goals

**Goals:**
- 让 `ccl stats` 在用户配齐 per-signal metrics endpoint 时仍能拿到数据
- 让用户的远程 collector 仍能收到 metrics（透传路径不丢）
- 不破坏现有 fallback key 注入行为（`CC_ORIG_*` 5 个 key 完全不动）
- `ccl restore` 幂等还原（含新 backup key）

**Non-Goals:**
- 接管 `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` / `TRACES_ENDPOINT`（用户原话"其他的都不动"）
- 读取/改写任何 `OTEL_*` 高级配置（export interval / resource attrs / protocol 等）
- 改变 forwarder 支持任意 signal scope（用户原话"只做 metrics 的转发"）
- TUI 引导新增 per-signal endpoint（留给后续 change）

## Decisions

### Decision 1：只接管 metrics，logs/traces 不动

**为什么**：用户的 logs/traces 端点已经写死指向他自己的远程 collector 了，Claude Code 会直接推过去 —— 中间再插一脚只是浪费流量 + 多一个可能挂的点。

唯一**必须**接管的只有 metrics，因为 `ccl stats` 要靠本地 SQLite 里的数据才能用。这是用户最核心的诉求。

**考虑过的其他方案：**
- 全部接管 → 浪费带宽，且 logs/traces 在 daemon 端要解析又要考虑"本地不存敏感数据"的隐私问题
- 只接管 logs → 解决不了 metrics 数据问题，`ccl stats` 还是空的
- 只接管 metrics ✓ → 当前选择

### Decision 2：备份键命名用 `FORWORD_*`，不沿用 `CC_ORIG_*`

**为什么**：用户明确要求"备份 key 也不要用 cc_orig，改用 forword 前缀"。原因是 settings.json 是用户的文件，他要能一眼认出哪些是"自己的"配置，哪些是 ccl 偷偷加进去的备份。如果 ccl 备份键用 `OTEL_*` 同前缀（`FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`），看起来就像用户自己写的另一个端点，而不是 ccl 后台塞的备份。

**没动**现有的 5 个 `CC_ORIG_*` 键。它们已经跟着用户跑了一段时间了，**重命名反而会造成双重历史**（一个用户旧的 settings.json 里有 `CC_ORIG_*`，新的又有 `FORWORD_*`，混乱）。

**考虑过的其他方案：**
- 复用 `CC_ORIG_` → 与用户原意冲突
- 用 `CC_FORWARD_*` → 同上（用户强调不要"cc_"开头）

### Decision 3：forwarder 只转发 metrics，触发条件保留两个

**为什么**：和"只转发 metrics"的目标一致。原来 forwarder 把三个类型都转，现在 logs/traces 已经由 Claude Code 直接推到远程，forwarder 只剩 metrics 这一份活儿。

触发条件（forwarder.go 的 `Enabled()` 函数）：

- **优先看**：`FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 有没有值且不指向 localhost
- **次选看**：`CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 有没有值且不指向 localhost

保留第二个触发的原因：如果用户只配了 fallback 端点（没配分类型 metrics 端点），他原来就靠 fallback 触发 forwarder 把 metrics 转发到远程。我们不能因为这次改动让他丢掉这个能力。

URL 固定为 `/v1/metrics`（不再按类型拼路径了，反正只发 metrics）。

**考虑过的其他方案：**
- 只保留第一个触发 → 用户如果只配了 fallback，metrics 就再也转不出去了，这是破坏性变更

### Decision 4：`/v1/logs` `/v1/traces` 收到请求就只回 200

**为什么**：既然 Claude Code 走分类型端点会直达远程，daemon 这边的 `:4318/v1/logs` 和 `:4318/v1/traces` 实际上**收不到请求**了（除非用户的兜底端点被改写成 localhost，Claude Code 才会走兜底到这里）。

兜底过来的请求也直接回 200 就行——客户端拿到 200 就 OK 了，不需要任何额外处理（不存、不转发）。

函数顺便重命名：`handleForwardOnly` → `handleAckOnly`（名字要能看出新语义：只是应答，不转发）。

### Decision 5：兜底注入逻辑保持不变

**为什么**：用户已经选了"保留兜底改写"。当用户**没**设分类型 metrics 端点时，ccl 还是把 `OTEL_EXPORTER_OTLP_ENDPOINT` 改写成 `http://localhost:4318`，让 Claude Code 走兜底路径 → metrics 到 daemon → `ccl stats` 有数据。这部分逻辑和现有 5 个键的注入是同一套代码，**不需要新写**。

## Data Flow

### 场景 A：用户有 per-signal metrics + logs endpoint（典型公司远程 collector）

注入后 settings.json：
```jsonc
"env": {
  // 现有 5 key + CC_ORIG_ 备份（不变）
  "OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
  "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT": "http://remote:4318",
  "OTEL_METRICS_EXPORTER":               "otlp",
  "OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
  "CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
  "OTEL_LOGS_EXPORTER":                  "otlp",

  // 新增 per-signal metrics endpoint 处理
  "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":         "http://localhost:4318/v1/metrics",        // 改写
  "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://remote:4318/v1/metrics",          // 备份

  // 用户原值不动
  "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":    "http://remote:4318/v1/logs",
  "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  "http://remote:4318/v1/traces"
}
```

运行时：
1. Claude Code → `POST localhost:4318/v1/metrics`（per-signal 改写）
2. daemon → parse + persist 到 SQLite + 异步 POST 到 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
3. 远程 collector 收到 metrics ✓
4. `ccl stats` 从 SQLite 读 → 显示 token / cost ✓
5. Claude Code → `POST remote:4318/v1/logs`（per-signal 未动）→ 直达远程 ✓
6. Claude Code → `POST remote:4318/v1/traces`（per-signal 未动）→ 直达远程 ✓

### 场景 B：用户没设 per-signal metrics（ccl stats 仍 work）

注入后 settings.json：
```jsonc
"env": {
  "OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
  "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT": "http://remote:4318",
  // ...
  // 没 per-signal metrics endpoint key — ccl 不注入
  "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":    "http://remote:4318/v1/logs",     // 用户原值
  "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  "http://remote:4318/v1/traces",   // 用户原值
}
```

运行时：
1. Claude Code → 优先用 per-signal logs/traces endpoint（直达 remote）✓
2. Claude Code → 没 per-signal metrics → 用 fallback `localhost:4318` → daemon `/v1/metrics` → 本地 stats ✓
3. daemon forwarder（CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT 触发）→ 转发 metrics 到 remote ✓

### 场景 C：用户从未碰过 OTel settings（最简情况）

注入后 settings.json（与现状完全一致）：
```jsonc
"env": {
  "OTEL_EXPORTER_OTLP_ENDPOINT":  "http://localhost:4318",
  "OTEL_METRICS_EXPORTER":        "otlp",
  "OTEL_EXPORTER_OTLP_PROTOCOL":  "http/protobuf",
  "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
  "OTEL_LOGS_EXPORTER":           "otlp"
}
```

运行时：与现状完全一致 — Claude Code 走 fallback → daemon → 本地 stats。`FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 与 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 均为空，**forwarder 禁用**（用户没配远程 collector，无需转发）。logs/traces 同样走 fallback → daemon `/v1/logs` `/v1/traces` → 返回 200。

### 场景 D：`ccl restore`

settings.json 还原（idempotent）：
1. `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` → 还原为 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`；删除 backup key
2. `CC_ORIG_*` 5 个 key 还原原值 + 删除 backup（与原 restore 逻辑一致）
3. 删除 ccl 注入的默认值（如 `OTEL_METRICS_EXPORTER=otlp` 无 `CC_ORIG_` 备份）
4. 保留用户自有 key（如 `ANTHROPIC_API_KEY`、`OTEL_LOGS_EXPORT_INTERVAL` 等）

## Component Changes

| 文件 | 改动 |
|---|---|
| `cmd/cc-launcher/envconfig.go` | 策略表加 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`；`restoreOTelEnv` 加对应还原；新 backup key 命名 `backupKeyForword()` 返回 `FORWORD_<KEY>` |
| `cmd/cc-launcher/main.go` | `injectOTelEnv` 同步 `FORWORD_*` 到 ccl 进程 env（让 fork 的 `ccl serve` 继承） |
| `internal/collector/forwarder.go` | env 常量改名 `envOrigEndpoint` → `envForwardMetricsEndpoint`；`Enabled()` 优先级 FORWORD_ > CC_ORIG_；URL 拼接改为 `/v1/metrics` 固定 |
| `internal/collector/server.go` | `/v1/logs` `/v1/traces` handler 改 200 only（不 forward、不 persist）；`handleForwardOnly` 改名 `handleAckOnly` |
| `cmd/cc-launcher/serve.go` | 调用 `NewServer` 时 `Forward` 仍传入（行为已 check 新 env） |

> 注：现有 forwarder 代码框架（接口/重试/超时）保持不变；trigger env + URL + handler 行为变更。

## Risks / Trade-offs

- **[backward compat] 现有用户 settings.json 里 5 个 `CC_ORIG_*` 不变；新增 1 个 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（用户有 per-signal 时才出现）—— 无破坏性**
- **[forwarder 行为变更] daemon 现在只 forward metrics（如启用），不再 forward logs/traces**。**用户 logs/traces 收集不再经过 ccl** — 若用户想用 ccl 做 logs 转发，ccl 不支持（决策 1）
- **[restore 范围扩展] `ccl restore` 新增 `FORWORD_*` 还原 — 确保 idempotent**
- **[外部架构决策记录] 项目内独立的架构决策文档（如有）中若提到"metrics 持久化、logs/traces 仅转发"措辞，需同步更新为"metrics 持久化 + 转发（如启用）、logs/traces 不再处理"。本设计文档的决策 5 是关于 fallback 注入的，与该项无关。本次 change 不动外部 ADR —— 留给单独的后续变更**
- **[命名拼写] `FORWORD_*` 是用户指定的前缀（不是 `FORWARD` 拼写错误）— 保留**

## Migration Plan

**无状态迁移**（新建 ccl 配置）：

1. 用户首次跑新版本 ccl → `injectOTelEnv` 检查 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`，如存在则备份 + 改写
2. 用户已有的 `CC_ORIG_*` 5 个 key **不动**（兼容）
3. `ccl serve` 启动时检查 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（优先）和 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`（兜底），按决策 3 启用 metrics-only forwarder
4. `ccl restore` 一键还原（含新 backup key）
5. 无需任何文档破坏性变更

## Open Questions

- TUI 多端点配置引导（用户写 `ccl provider --new` 时是否问 per-signal metrics endpoint）—— **不做**，留给后续变更