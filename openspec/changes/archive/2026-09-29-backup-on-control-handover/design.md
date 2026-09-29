## Context

ccl 改 `~/.claude/settings.json` 的流程涉及两类"备份"：

1. **应用级备份**（已实现）：`backupAndInjectOTel()` 把原 env 值写到 `CC_ORIG_*` / `FORWORD_*` 键内（settings.json 内部）
2. **文件级备份**（已实现但触发点不全）：`Manager.Backup()` 把整个 `~/.claude/settings.json` 复制到 `~/.cc-launcher/backups/settings-<时间戳>.json`

文件级备份**目前只在两个时机触发**：
- `runRestore`（用户手动命令）
- `Manager.Merge`（切换 provider 时）

`injectOTelEnv`（每次 `ccl` 启动都跑）**不调 Backup**——这是核心漏洞。结果：backups/ 里**从来没有过"ccl 首次注入之前"的健康基线**。

更深的问题：CC_ORIG_* 残留不等于 ccl 仍在控制 settings.json。第三方工具/手动编辑覆盖 OTel 端点后，CC_ORIG_* 仍在，但 ccl 实际已被踢出控制——此时若按"CC_ORIG_* 存在 ⇒ 健康"判断会误判。

## Goals / Non-Goals

**Goals:**
- backups/ 在"控制权交接"边界产生快照：进入 ccl 生命周期（首跑）/ 离开 ccl 生命周期（第三方覆盖）→ 备份一次作为基线
- ccl 已在控制中的常态启动 → **不**触发 Backup，避免 10 个轮转被瞬态版本耗光
- 判断逻辑只读 env map，不破坏 settings.json 结构或顺序
- Backup 失败仅警告，不阻断 ccl 主流程

**Non-Goals:**
- 不引入新的 Backup 触发点（如 `ccl backup` 子命令）——后续独立提案
- 不改 `Manager.Backup()` 的实现、轮转策略（maxBackups=10）、时间戳格式
- 不改应用级备份（`backupAndInjectOTel` 写 `CC_ORIG_*` / `FORWORD_*` 的逻辑）
- 不改 `ccl restore` 的 Backup 行为——它继续备份当前 settings.json（含 ccl 注入态），目的是给 restore 操作本身一个回滚锚点
- 不引入"ccl 仍在控制"的更多判断字段（如 `OTEL_METRICS_EXPORTER == otlp`）——保持判断条件最小化、零歧义

## Decisions

### 决策 1：判断"ccl 当前是否控制"的最小字段集

**选择**：只检查两个端点字段是否指向 ccl 默认值：
- `OTEL_EXPORTER_OTLP_ENDPOINT` == `http://localhost:4318`（兜底端点）
- `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` == `http://localhost:4318/v1/metrics`（per-signal metrics 接管）

**理由**：
- 这两个值是 ccl 注入的"硬特征"——其他工具不会恰好写这两个精确字符串
- 检查 `OTEL_METRICS_EXPORTER == otlp` 不够稳——用户可能主动设 otlp 而不用 ccl
- 检查 `CLAUDE_CODE_ENABLE_TELEMETRY == 1` 同样不稳——这是 Claude Code 的默认值

**被否方案 A**：检查 `CC_ORIG_*` / `FORWORD_*` 是否存在（用户最初提出的 C 方案）。已被反例击穿：CC_ORIG_* 残留 ≠ ccl 仍在控制。

**被否方案 B**：检查 `OTEL_*` 全部字段是否一致。这是过拟合——ccl 只动端点，不该扩展到其它字段。

### 决策 2：触发点放在 `injectOTelEnv`（不是 `runRestore`）

**选择**：在 `injectOTelEnv` SaveSettings 前调 `cm.Backup()`，仅在 `!isUnderCclControl(env)` 时。

**理由**：
- `injectOTelEnv` 是"ccl 修改 settings.json"的主路径——所有写都从这里走
- 放在 `runRestore` 救不回"已经发生过的事实"（backups/ 已无健康基线）
- `Manager.Merge`（provider 切换）也会调 Backup——但 Merge 触发 Backup 不需要新逻辑，因为 Merge 的 Backup 调用点本身就在写 settings.json 之前，**不过**它目前是无条件 Backup——是否要同步改为条件 Backup 留作 future work（不在本 change 范围）

**被否方案 C**：改 `runRestore` 加 Backup 前置条件检测。这只能解决"未来 restore 前的备份"，救不回历史缺口。

### 决策 3：Backup 失败仅警告，不阻断

**选择**：`cm.Backup()` 返回错误时 `log.Printf` 到 stderr，继续 injectOTelEnv 主流程。

**理由**：
- 现有 `injectOTelEnv` 失败时也是仅警告——一致性
- Backup 不影响 OTel 注入本身的正确性
- 严格阻断会让 backups/ 目录无写权限时整个 ccl 不可用——过度耦合

**被否方案 D**：Backup 失败 → 阻断 ccl 启动。会让"backupDir 写满 / 权限丢失"的运维事故扩散成"ccl 完全不能用"。

### 决策 4：不修 `Manager.Backup()` 时间戳精度问题

**选择**：保留 `2006-01-02T15-04-05` 秒级时间戳格式。

**理由**：
- 现有 10 个轮转下，新触发点（控制权交接）频率极低——同一秒连撞的概率可忽略
- 改时间戳格式会影响已有 backups/ 文件名排序规律（运维/调试时已习惯）
- 真要修应独立提案 + 评估向后兼容

## Risks / Trade-offs

- **判断条件漂移**：如果未来 ccl 默认值变化（如改端口、加新端点），`isUnderCclControl` 需同步更新 → **缓解**：常量集中在 main.go 顶部，注释指明"同步更新位置"
- **多入口未来扩展**：未来加新 OTel 端点（如 `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`）需要考虑是否纳入判断 → **缓解**：在 spec 的 Scenario 列举边界 case；未来扩展走独立 change
- **Provider 切换路径未优化**：`Manager.Merge` 仍无条件 Backup，若用户频繁切换 provider 也会占轮转 → **缓解**：当前不在本 change 范围；future work 考虑让 Merge 也走 `isUnderCclControl` 检测
- **第三方控制被覆盖后 ccl 重新接管**：ccl 启动检测到"不在控制"→ Backup 一次→ ccl 接管 endpoint。backup 反映的是"第三方接管态"，回滚方向是"放弃 ccl 接管回到三方接管"——这是合理语义

## Migration Plan

无需迁移。本 change 是**新增触发点** + **新增判断函数**，不修改任何已有数据格式或 Backup 接口签名。已有 backups/ 文件保持不动。

**回滚**：删除 `injectOTelEnv` 里的条件 Backup 调用块即可，恢复到当前行为（仅 restore / Merge 触发 Backup）。

## Open Questions

- 是否需要在 `ccl --help` 或 `ccl restore --help` 文案里加一句"backups 包含自动控制权交接快照"？倾向加，但属于 docs-only follow-up
- `Manager.Merge` 是否也走 `isUnderCclControl` 检测？倾向延后——本次只解决"健康基线缺失"，Merge 的轮转压力暂不紧迫
