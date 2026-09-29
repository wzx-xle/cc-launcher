# claude-code-env-config Specification

## Purpose
TBD - created by archiving change usage-monitoring-via-otel. Update Purpose after archive.
## Requirements
### Requirement: ccl injects OTEL env into Claude Code settings with backup
On every `ccl` invocation (before fork-if-needed and exec Claude Code), ccl MUST inspect `~/.claude/settings.json` and ensure the `env` map contains the 5 OTel keys. For each key that already has a user value, ccl MUST first save a backup to a `CC_ORIG_<KEY>` key, then apply the action below. For missing keys, ccl MUST add the default without a backup.

The 5 keys and actions:

| Key | If user has value | If missing |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | backup to `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`; override to `http://localhost:4318` | add `http://localhost:4318` |
| `OTEL_LOGS_EXPORTER` | backup to `CC_ORIG_OTEL_LOGS_EXPORTER`; **respect user's value** | add `otlp` (default; user can edit) |
| `OTEL_METRICS_EXPORTER` | backup to `CC_ORIG_OTEL_METRICS_EXPORTER`; respect user's value | add `otlp` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | backup to `CC_ORIG_OTEL_EXPORTER_OTLP_PROTOCOL`; respect user's value | add `http/protobuf` |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | backup to `CC_ORIG_CLAUDE_CODE_ENABLE_TELEMETRY`; respect user's value | add `1` |

**Note**: ccl does NOT force-override `OTEL_LOGS_EXPORTER`. The local-privacy guarantee (no prompts in SQLite) comes from the daemon's architecture (it accepts /v1/logs but only forwards, never persists). User's original `OTEL_LOGS_EXPORTER=otlp` continues to work — logs reach the remote collector via forwarding.

#### Scenario: settings.json has only OTel endpoint (typical user)
- **WHEN** `~/.claude/settings.json` has only `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`
- **THEN** after ccl:
  - `OTEL_EXPORTER_OTLP_ENDPOINT` = `http://localhost:4318`
  - `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` = `https://company-otel:4317`
  - `OTEL_LOGS_EXPORTER` = `none` (newly added, no backup)
  - `OTEL_METRICS_EXPORTER` = `otlp` (newly added)
  - `OTEL_EXPORTER_OTLP_PROTOCOL` = `http/protobuf` (newly added)
  - `CLAUDE_CODE_ENABLE_TELEMETRY` = `1` (newly added)

#### Scenario: settings.json already has all 5 keys
- **WHEN** user has manually configured every OTel key
- **THEN** ccl backs up all 5 to `CC_ORIG_*` (only first time; if backups already present, don't overwrite); ccl force-overrides only `OTEL_LOGS_EXPORTER` to `none`; other 4 are preserved

#### Scenario: settings.json already has CC_ORIG_* from prior ccl run
- **WHEN** user runs `ccl` again and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` already exists
- **THEN** ccl does NOT overwrite the backup (preserves the original saved value); only adds missing keys

#### Scenario: user has OTEL_LOGS_EXPORTER=otlp (logs enabled) — ccl respects
- **WHEN** user has `OTEL_LOGS_EXPORTER=otlp`
- **THEN** ccl saves `CC_ORIG_OTEL_LOGS_EXPORTER=otlp` and **respects the value** (does NOT force to `none`); Claude Code pushes logs to wherever its OTEL_EXPORTER_OTLP_ENDPOINT points; if forwarding is configured, daemon forwards logs to backup endpoint

#### Scenario: user sets OTEL_METRICS_EXPORTER to a non-otlp value (e.g. prometheus)
- **WHEN** user has `OTEL_METRICS_EXPORTER=prometheus`
- **THEN** ccl backs up as `CC_ORIG_OTEL_METRICS_EXPORTER=prometheus` and respects the value; Claude Code will use prometheus, ccl stats will be empty (user opted out)

#### Scenario: env value points to a remote collector
- **WHEN** user has `OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example.com:4318`
- **THEN** ccl backs it up to `CC_ORIG_*`; daemon sees the backup at startup and **enables forwarder** to that URL; metrics flow: Claude Code → daemon → remote collector (and locally to SQLite)

#### Scenario: existing settings.json field order is preserved
- **WHEN** ccl writes new keys to an existing settings.json with keys in order `[env, permissions, enabledPlugins, ...]`
- **THEN** after write, the file still has the same key order with new keys appended in the env section, not inserted at the top of the file (ccl uses `internal/config/ordered.go` to preserve order)

### Requirement: ccl restore removes all CC_ORIG_* and ccl-injected defaults
`ccl restore` MUST read `~/.claude/settings.json` env field and:
- For each `CC_ORIG_<KEY>` found: write `<KEY>` = `CC_ORIG_<KEY>` value; delete the `CC_ORIG_*` key
- For each of the 5 OTel keys ccl would have injected as default: delete them if no backup exists (i.e., user never had them — they're purely ccl artifacts)
- The settings.json file structure MUST be preserved otherwise

#### Scenario: restore recovers original user config
- **WHEN** user runs `ccl restore` and settings.json has `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` (along with ccl's override to localhost:4318)
- **THEN** settings.json ends up with `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` and no `CC_ORIG_*` keys

#### Scenario: restore removes keys ccl added (no original)
- **WHEN** user runs `ccl restore` and settings.json has `OTEL_METRICS_EXPORTER=otlp` (ccl added it; user never had a value)
- **THEN** the key is removed (since no `CC_ORIG_OTEL_METRICS_EXPORTER` exists)

#### Scenario: restore is idempotent
- **WHEN** user runs `ccl restore` twice
- **THEN** the second run is a no-op; settings.json is unchanged

#### Scenario: restore keeps user-added non-ccl keys
- **WHEN** user has `ANTHROPIC_BASE_URL=https://...` (a ccl-doesn't-touch key)
- **THEN** `ccl restore` does not modify it

#### Scenario: settings.json does not exist
- **WHEN** user runs `ccl restore` and `~/.claude/settings.json` is missing
- **THEN** ccl prints "no settings to restore" and exits 0

### Requirement: daemon auto-enables forwarder based on CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT
On `ccl serve` startup, the daemon MUST inspect its own env for `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`. If the value is set AND is not `http://localhost:4318` (or 127.0.0.1), the daemon MUST enable a forwarder that POSTs each parsed metric to that URL after persisting locally.

#### Scenario: user has remote OTLP configured
- **WHEN** daemon starts and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`
- **THEN** daemon enables forwarder; each metric received via /v1/metrics is: (1) written to local SQLite (sync), (2) POSTed to remote in a goroutine (async, fire-and-forget)

#### Scenario: forwarder detects localhost (loop prevention)
- **WHEN** daemon starts and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` (or 127.0.0.1)
- **THEN** daemon does NOT enable forwarder; logs at startup "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT points to localhost; forwarder disabled to prevent loop"

#### Scenario: no backup env present
- **WHEN** daemon starts and `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` is unset
- **THEN** daemon does NOT enable forwarder; no log emitted

### Requirement: forwarder auth comes from CC_FORWARD_AUTH_HEADER env
The forwarder MUST check env `CC_FORWARD_AUTH_HEADER`. If set, every POST request MUST include it as a header. If unset, no auth header is sent. The daemon MUST NOT reuse `ANTHROPIC_AUTH_TOKEN` for forwarder auth.

#### Scenario: user sets CC_FORWARD_AUTH_HEADER
- **WHEN** env has `CC_FORWARD_AUTH_HEADER="Authorization=Bearer my-token"`
- **THEN** forwarder POSTs include `Authorization: Bearer my-token`

#### Scenario: user does not set auth
- **WHEN** env lacks `CC_FORWARD_AUTH_HEADER`
- **THEN** forwarder POSTs have no auth header

#### Scenario: forwarder error log does not echo auth
- **WHEN** forwarder POST returns non-2xx
- **THEN** daemon logs stderr `forwarder: post to <url> failed: status=401` — auth header value is NOT included

### Requirement: forwarder failures are logged and dropped
Forwarder failures (non-2xx response, timeout > 5s, connection error) MUST be logged to stderr in one line per failure and the metric MUST be dropped from the forwarder pipeline. The local SQLite write MUST have already succeeded before the failure.

#### Scenario: forwarder times out
- **WHEN** remote collector does not respond within 5s
- **THEN** daemon logs `forwarder: post to <url> timeout after 5s`; metric is already in local SQLite

#### Scenario: forwarder returns 5xx
- **WHEN** remote returns 503
- **THEN** daemon logs `forwarder: post to <url> status=503`; metric is already in local SQLite

#### Scenario: forwarder succeeds
- **WHEN** remote returns 200
- **THEN** daemon logs nothing for this metric (success); local SQLite has it

#### Scenario: forwarder does not block daemon
- **WHEN** forwarder is slow (but not timed out)
- **THEN** daemon accepts new OTLP requests at full speed; local SQLite writes proceed normally

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

