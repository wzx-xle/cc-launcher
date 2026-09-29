## 1. 实现 isUnderCclControl 判断函数

- [x] 1.1 在 `cmd/cc-launcher/main.go` 新增 `isUnderCclControl(env map[string]any) bool`：检查 `OTEL_EXPORTER_OTLP_ENDPOINT` 和 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 是否指向 ccl 默认地址。**复用** `cmd/cc-launcher/envconfig.go` 已定义的常量 `keyEndpoint` / `defaultEndpoint` / `keyMetricsEndpoint` / `defaultMetricsEndpoint`（避免重复定义；常量同步位置在 `envconfig.go:26-27`）。函数上方加注释：若未来 ccl 默认值变化，需同步更新此函数
- [x] 1.2 在 `cmd/cc-launcher/main_test.go` 新增 `TestIsUnderCclControl`：覆盖 4 个 case——兜底 endpoint 是 localhost、per-signal metrics endpoint 是 localhost、两者都是 localhost、两者都不是 localhost
- [x] 1.3 在同一 test 文件覆盖边界 case——env 字段不存在、字段值为 nil、字段值为非 string 类型

## 2. 修改 injectOTelEnv 加条件 Backup 调用

- [x] 2.1 在 `cmd/cc-launcher/main.go` 的 `injectOTelEnv` 函数里，`cm.LoadSettings()` 之后、`backupAndInjectOTel(env)` 之前插入判断块：`if !isUnderCclControl(env) { if err := cm.Backup(); err != nil { log.Printf("injectOTelEnv: 备份 settings.json 失败: %v", err) } }`
- [x] 2.2 在 `cmd/cc-launcher/main_test.go` 扩展现有 `TestInjectOTelEnv_*` 系列：加 scenario "settings.json 不在 ccl 控制下时调 Backup"——验证 Backup 被调且 `backupDir` 下出现新文件
- [x] 2.3 加 scenario "settings.json 在 ccl 控制下时跳过 Backup"——验证 Backup 未被调。**判断方法**：`config.Manager` 是 concrete type 没有 interface 可 mock；用 `t.TempDir()` + `os.ReadDir(backupDir)` 在 injectOTelEnv 前后文件数对比作为间接证据（控制下：文件数不变；不在控制：+1）

## 3. 单元测试覆盖完整 Scenario 集

- [x] 3.1 覆盖 spec 的 "首次 ccl 启动，settings.json 健康基线被备份" scenario——构造 `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317`，断言 Backup 被调、产生快照含原值
- [x] 3.2 覆盖 spec 的 "ccl 已在控制中（每次常规启动），跳过 Backup" scenario——构造 `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`，断言 Backup 未被调
- [x] 3.3 覆盖 spec 的 "第三方覆盖 OTel 端点但 CC_ORIG_* 残留，触发 Backup" scenario——构造 `OTEL_EXPORTER_OTLP_ENDPOINT=http://other-tool:4318` + `CC_ORIG_*` 残留，断言 Backup 被调、ccl 重新接管但不擦除 `CC_ORIG_*`
- [x] 3.4 覆盖 spec 的 "per-signal metrics 接管路径识别为 ccl 控制中" scenario——构造 per-signal metrics 接管 + 兜底非 localhost，断言 Backup 未被调
- [x] 3.5 **回归测试**：验证 `Manager.Backup()` 对不存在的 `settings.json` 返回 nil（已有行为不被新代码破坏）——直接调 `cm.Backup()` 而非通过 injectOTelEnv
- [x] 3.6 覆盖 spec 的 "Backup 失败仅警告，不阻断 ccl 主流程" scenario。**测试方式**：`os.Chmod(backupDir, 0)` 制造不可写 + `defer os.Chmod(backupDir, 0755)` 恢复；断言 injectOTelEnv 仍继续完成（应用级备份 + SaveSettings 仍发生，stderr 含警告行）。**平台限制**：仅 Linux/macOS 生效；Windows 用 `t.Skipf("Backup 权限测试在 Windows 不可靠")` 跳过
- [x] 3.7 **新增场景**："settings.json 存在但无 env 字段"——构造 `settings.json = {"enabledPlugins": {...}}`，断言 `isUnderCclControl` 返回 false → Backup 触发（用户从未用 OTel 也是控制权交接边界）

## 4. 文档更新（可选）

- [x] 4.1 在 `cmd/cc-launcher/statscmd.go` 的 `runRestore` 帮助文案 `restoreHelp` 补一句"backups 包含自动控制权交接快照，可用于回滚到第三方覆盖前的状态"
- [x] 4.2 更新 README "用量监控" 小节（如有），说明新的备份触发语义

## 5. Future Work（不在本 change 范围，但记录跟踪）

- [x] 5.1 `Manager.Merge` 走 `isUnderCclControl` 检测：当前切换 provider 时 `Merge` 内部无条件 Backup，若用户频繁切 provider 会占轮转。复用本次的 `isUnderCclControl` 函数，让 Merge 只在"控制权交接"时备份。**本 change 仅记录跟踪；独立 change 提案留待后续**（理由：本次目标限定 injectOTelEnv 路径，Merge 走 config 包层、影响面更大；解耦更稳妥）

## 6. Logical Commits

- [x] 6.1 commit 1：`feat(main): 新增 isUnderCclControl + injectOTelEnv 条件 Backup 触发`（涵盖 §1、§2）
- [x] 6.2 commit 2：`test(main): 覆盖控制权交接备份的全部 Scenario + 边界 case`（涵盖 §3）
- [x] 6.3 commit 3：`docs(statscmd,readme): 文档化自动控制权交接 Backup 语义`（涵盖 §4；仅 §4 任务实际执行时才需要该 commit）

## 7. 验证（手动 smoke test + 归档）

- [x] 7.1 跑 `openspec validate backup-on-control-handover` 校验 artifacts 结构
- [x] 7.2 跑 `openspec show backup-on-control-handover --json` 检查 artifacts 完整性
- [x] 7.3 smoke test — 首跑 backup：准备 pristine settings.json（含 `OTEL_EXPORTER_OTLP_ENDPOINT=https://company-otel:4317` 用户原值）→ 跑 `ccl` → 验证 `~/.cc-launcher/backups/` 新增一份快照含原值
- [x] 7.4 smoke test — 控制中跳过 backup：跑 `ccl` 第二次 → 验证 `backups/` 文件数**不**变（endpoint 已为 localhost）
- [x] 7.5 smoke test — 第三方覆盖 backup：手动改 endpoint 为非 localhost（如 `http://other-tool:4318`）→ 跑 `ccl` → 验证 `backups/` 新增"三方覆盖态"快照
- [x] 7.6 smoke test — restore 不受影响：跑 `ccl restore` → 验证仍按原逻辑还原 CC_ORIG_* + 删 ccl 默认值，settings.json 等价于"首次 ccl 注入前 + restore 已清 backup"
- [ ] 7.7 所有 task 完成 + 上述 smoke test 通过后跑 `opsx:archive backup-on-control-handover` 归档
