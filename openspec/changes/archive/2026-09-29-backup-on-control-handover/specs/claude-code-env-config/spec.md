## ADDED Requirements

### Requirement: ccl 文件级备份仅在控制权交接时触发
在 `injectOTelEnv` 流程里（`SaveSettings` 前），ccl MUST 检测 `~/.claude/settings.json` 是否仍处于 ccl 当前控制之下；若不在，则调 `Manager.Backup()` 做一次文件级快照到 `~/.cc-launcher/backups/settings-<时间戳>.json`。若在 ccl 控制下，则 MUST NOT 触发 Backup（避免 backups/ 充斥同状态瞬态版本导致轮转耗光）。

**判断函数** `isUnderCclControl(env map[string]any) bool`：ccl 当前控制 = 满足下列任一：
- `env["OTEL_EXPORTER_OTLP_ENDPOINT"]` == `"http://localhost:4318"`（兜底端点被 ccl 改写）
- `env["OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"]` == `"http://localhost:4318/v1/metrics"`（per-signal metrics 端点被 ccl 接管）

两者都不是 → ccl 当前不在控制 settings.json → 视为"控制权交接边界"，触发 Backup。

**失败行为**：`Manager.Backup()` 返回错误时 ccl MUST 仅写 stderr 警告并继续 `injectOTelEnv` 主流程——不阻断 ccl 启动（与现有"Otl 注入失败仅警告"策略一致）。

#### Scenario: 首次 ccl 启动，settings.json 健康基线被备份
- **WHEN** `~/.claude/settings.json` 的 `env` map 含 `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`（用户原值，ccl 尚未注入），且不含 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
- **THEN** 触发 `Manager.Backup()` 一次；`~/.cc-launcher/backups/` 下新增 `settings-<timestamp>.json`，内容是当前 settings.json（含用户原值）；之后 ccl 才执行应用级备份与端点改写

#### Scenario: ccl 已在控制中（每次常规启动），跳过 Backup
- **WHEN** `~/.claude/settings.json` 的 `env` map 含 `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`（上轮 ccl 写入）
- **THEN** `isUnderCclControl` 返回 true → 跳过 `Manager.Backup()`；`backups/` 文件数不变；ccl 继续按现有逻辑更新 keys

#### Scenario: 第三方覆盖 OTel 端点但 CC_ORIG_* 残留，触发 Backup
- **WHEN** `~/.claude/settings.json` 的 `env` map 含 `OTEL_EXPORTER_OTLP_ENDPOINT=http://other-tool:4318`（第三方工具修改），且 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` 仍残留
- **THEN** `isUnderCclControl` 返回 false → 触发 Backup 一次；`backups/` 新增一份反映"第三方覆盖后状态"；ccl 重新接管端点但**不**擦除 `CC_ORIG_*`（那是 `ccl restore` 的工作）

#### Scenario: per-signal metrics 接管路径识别为"ccl 控制中"
- **WHEN** `~/.claude/settings.json` 的 `env` map 含 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics`（per-signal 接管），且 `OTEL_EXPORTER_OTLP_ENDPOINT` 是其他非 localhost 值
- **THEN** `isUnderCclControl` 返回 true → 跳过 Backup

#### Scenario: settings.json 不存在（首跑场景）
- **WHEN** `~/.claude/settings.json` 文件不存在
- **THEN** `isUnderCclControl` 视为 false → `Manager.Backup()` 检查到文件不存在直接返回 nil（no-op）；ccl 继续正常创建新 settings.json

#### Scenario: Backup 失败仅警告，不阻断 ccl 主流程
- **WHEN** `Manager.Backup()` 返回错误（如 backupDir 无写权限、磁盘满）
- **THEN** ccl 打印 stderr 一行警告（如 `injectOTelEnv: 备份 settings.json 失败: <err>`）但继续执行 `injectOTelEnv` 主流程；Claude Code 仍能正常启动
