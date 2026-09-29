## Why

ccl 改 `~/.claude/settings.json` 有两类"备份"语义：

1. **应用级备份**：把原 env 值写到 `CC_ORIG_*` / `FORWORD_*` 键内（settings.json 内部，每次 `ccl` 启动都做）
2. **文件级备份**：`Manager.Backup()` 把整个 `~/.claude/settings.json` 复制到 `~/.cc-launcher/backups/settings-<时间戳>.json`（**目前只有 `ccl restore` 和切换 provider 的 `Merge` 触发**）

文件级备份的当前触发点有问题：`injectOTelEnv` 改 settings.json 时**不做文件级备份**——所以 backups/ 里**从来没有过"ccl 首次注入之前"的健康基线**。任何时间点 `ccl restore` 做的 Backup，备份的都是**已被 ccl 注入过**的版本。如果回滚到这份"备份"，等于还原到带 ccl 注入的状态——没有用。

更隐蔽的失效场景：第三方工具/手动编辑覆盖了 OTel 端点，但 `CC_ORIG_*` 还在（残留）→ Claude Code 推数据到错误地址 → `ccl stats` 永远空 → CC_ORIG_* 不代表 ccl 当前还控制着 settings.json。

## What Changes

- **新增**（main.go `injectOTelEnv`）：在 SaveSettings 前调 `cm.Backup()` —— **但仅在检测到 settings.json 不在 ccl 当前控制下时**才备份。判断条件是"OTel 端点还指向 localhost:4318"（ccl 默认值），否则视为"控制权交接边界"，备份一次当前快照作为基线。
- **新增**（main.go / 新函数）：`isUnderCclControl(env map[string]any) bool` —— 检查 `OTEL_EXPORTER_OTLP_ENDPOINT` 和 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（per-signal 接管路径）是否还指向 ccl 默认地址。
- **调整**（main.go `injectOTelEnv`）：失败时 Backup 仅警告、不阻断——和现有"OTel 注入失败仅警告"的策略一致。
- **文档化**（不实现，仅澄清）：`ccl restore` 的 Backup 行为**不改变**——它继续备份当前 settings.json（哪怕已是"已注入"态），目的是给 restore 操作本身一个回滚锚点。新增的"控制权交接 Backup"在 injectOTelEnv 触发，专门负责健康基线。

**不动的部分**（兼容性）：
- `Manager.Backup()` 的实现、轮转策略（maxBackups=10）、时间戳格式 —— 全部保留
- 应用级备份（`backupAndInjectOTel` 写 `CC_ORIG_*` / `FORWORD_*`）—— 完全不动
- `ccl restore` 的 Backup 触发 —— 完全不动
- 第三方控制权判断**只读 settings.json 的 `env` 字段**，不写、不改顺序

## Capabilities

### New Capabilities
无（这次改的是现有 requirement 的触发条件和判断逻辑，不引入新 capability）

### Modified Capabilities
- `claude-code-env-config`：扩展"ccl injects OTEL env into Claude Code settings with backup" requirement，新增"控制权交接时做文件级备份"子规则；新增"isUnderCclControl 判断条件"的 Scenario。

## Impact

- **代码**：
  - `cmd/cc-launcher/main.go` — `injectOTelEnv` 加条件 Backup 调用 + 新增 `isUnderCclControl` 函数
  - `internal/config/config.go` — `Manager.Backup()` 不动（已有接口足够）
- **测试**：
  - `cmd/cc-launcher/main_test.go` — 加"settings.json 不在 ccl 控制下时调 Backup"的 Scenario 测试
  - `cmd/cc-launcher/envconfig_test.go` — 不动（应用级备份逻辑没变）
- **用户可见行为**：
  - backups/ 现在会多出"健康基线"快照（首次 ccl 跑之前 / 第三方覆盖之后），便于回滚到 pristine 状态
  - ccl 每次启动（如果仍在 ccl 控制下）—— **不**产生新备份，10 个轮转保留更久
  - 出错路径——Backup 失败仅警告，OTel 注入失败策略不变（不阻断 ccl 主流程）
- **风险**：
  - isUnderCclControl 误判可能让 backups/ 充斥瞬态版本——通过单测锁定边界 case
  - 如果未来 ccl 默认值变化（如 endpoint 改端口），判断条件需同步更新——加注释指明
