# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.11.0] - 2026-10-13 — provider-list-display-improvements

### Features
- **Provider list 视觉变更**：TUI 选择 provider 时默认项用左侧灰色 `[*]` 标识（原行尾"默认" tag 移除）。非默认项仍为 `[ ]`；cursor 行整行高亮。
- **Provider 配置新增 `note` / `order` 字段**：
  - `note` (string)：一行备注，渲染在 provider 名后、空格分隔、灰色显示
  - `order` (number)：排序权重，越小越靠前；缺省 0；同 order 时回退到文件名字典序
  - 旧 provider JSON 文件（无 `note` / `order`）继续工作
- **`ccl provider --new` 写盘后追加可选 `note` 询问与 `order` 建议值提示**：
  - 写盘成功后 `note (可选备注，回车跳过):` 让用户填 note（非空时通过 ReadFile + JSON patch 写回顶层 `note` 字段，不动 `env` 块）
  - 写盘成功后打印 `排序值: <max+10> (现有 max=<M>, 建议 max+10；手改 JSON order 字段覆盖)`（只显示不读输入）
  - note 阶段 EOF / 空字符串 = 跳过；template prompts 阶段 EOF = 取消（沿用现有语义）

### Tests
- `internal/provider/provider_test.go`：加 6 个新单元测试覆盖 `note` / `order` 解析、排序、容错、非数字 `order` 标 Error、errored 仍排序
- `internal/tui/tui_test.go`：加 5 个新单元测试覆盖 dim `[*]` 预选、note 渲染、note 在 errored 状态被压制、无预选时所有 marker 为 `[ ]`、行尾"默认" tag 移除
- `internal/cli/cli_test.go`：加 6 个新单元测试覆盖 `--new` 写盘后 note 询问（空跳过 / 非空写入）、order 提示（max+10 / 零 baseline / 排除新文件）、`ccl provider --list` 排序软测试

### OpenSpec
- 归档 change `provider-list-display-improvements` → `2026-10-13-provider-list-display-improvements`
- 新增 capability `provider-list-rendering`（5 个 ADDED Requirements + 13 个 Scenario）
- Modified capability `provider-new-guided-flow`（1 个 MODIFIED + 1 个 ADDED Requirement；锁定"template field prompts"边界，EOF 取消语义在 template 阶段保留）

## [0.10.0] - 2026-10-10 — template-driven-preask

### Refactor
- `cli.newProvider` 160 行混合职责拆开，模板自描述 + cli 数据驱动
- `templates.Template` 加 `PreAsks []PreAsk` 字段（模板声明字段收集前的额外问答）
- `templates.Template` 加 `PostOverrides []FieldOverride` 字段（模板声明字段收集后的字段值覆盖）
- `templates.TemplateField` 加 `SkipPrompt bool` 字段（模板声明此字段跳过 prompt 直接写入）
- `cli.newProvider` 拆成 `collectPreAsks` / `collectFields` / `applyPostOverrides` 三阶段函数
- cli 端去除所有 `tpl.Name == "aliyun-pay"` / `"minimax"` 硬编码（3 处），改为模板元数据驱动

### Templates
- `minimaxTpl` 加 `PreAsk (region)` + `PostOverride (BASE_URL)` + `ANTHROPIC_BASE_URL.SkipPrompt = true`
- `aliyunPayTpl` `ANTHROPIC_BASE_URL.SkipPrompt = true`（其它字段零修改）

### Tests
- `internal/templates`：加 4 个新单元测试（`TestMinimax_PreAskRegion` / `TestMinimax_PostOverride_BaseURL` / `TestAliyunPay_NoPreAsk` / `TestAliyunPay_BaseURL_SkipPrompt` / `TestMinimax_BaseURL_SkipPrompt`）
- `internal/cli/cli_test.go`：零修改（行为不变——5 个 guided-flow 测试原样通过）

### OpenSpec
- 归档 change `template-driven-preask` → `2026-10-10-template-driven-preask`
- 新增 capability `template-preask`（4 个 ADDED Requirements + 4 个 Scenario 合并到 `openspec/specs/template-preask/spec.md`）

### Compatibility
- 行为完全不变：`ccl provider --template minimax` / `--template aliyun-pay` 输出 JSON 与 v0.9.0 一致
- 现有 user 配置（`~/.cc-launcher/providers/*.json`）不受影响（只动创建流程，不动读取）
- 跨平台编译验证：linux / darwin / windows 三平台 build 通过

## [0.9.0] - 2026-10-09 — provider-setenv-cwd-binding

### Features
- **provider setenv 注入取代 settings.json 字段**：`runTUI` 通过子进程环境注入 provider 配置；
  `settings.json` 不再保留 `provider` 字段（架构约束 #6 落地）
- state 层加 `dir_bindings` 字段 + `Get/SetProviderForDir` 方法
- `launcher.Options.Env` 字段 + 跨平台 env 透传 + `envutil.Upsert` helper
- TUI 末尾 `stageRemember` + cwd binding 预选 + `runTUI` wiring
- `ccl provider --list` 显示 cwd binding `*` 标记，CLI 切换同步写 binding
- `__init` PowerShell 脚本 + `topHelp` 加 PowerShell 行

### Migration
- `migrateSettingsJSON` 一次性清理老 ccl 注入到 settings.json 的 `ANTHROPIC_*` 字段
  （首次启动自动触发，无需手动介入）

### Chore
- 删除 `internal/config/ordered.go` 死代码 + 修正 `completion.go` 错误信息
- §7 跨平台编译验证 checkbox 标 done
- `TestE2E_OldUserFirstRunTUI_BackupRhythm` 覆盖 §8.6

### Docs
- provider-setenv-cwd-binding §9 文档同步（README + CLAUDE.md）

## [0.8.0] - 2026-10-01 — stats-time-buckets

### Features
- `ccl stats --day N` 时间桶聚合（hourly / daily）

### Fix
- `bump-version` 保守默认：默认本地打 tag，`--push` opt-in 才推

### Docs
- README 用量监控 + tab completion 加 `--day`

### Test
- stats 时间桶 smoke + main 同步 completion 候选

## [0.7.0] - 2026-09-30 — cli-shell-completion + 版本号机制

### Features
- shell tab completion（`ccl __complete` + `ccl __init`）
- 引入版本号注入机制：`-V` 显示 + ldflags 注入 + `bump-version` 脚本
- `bump-version` 加 `auto` 子命令——按 conventional commit 自动推断 bump 类型
- `bump-version` 脚本语义化子命令 + 跨 OS + slash command

### Refactor
- bump 类型推断迁到 slash command（Claude 分析 commits）

### Fix
- `Version` 字段简化——只用 tag + dirty 标记，ahead 信息去 `Commit` 字段
- 恢复 `git describe --tags` 原样输出 + 保留 ahead 信息

### Chore
- 跟踪 `.claude/commands/opsx` + skills 配置
- 从 `changes/` 移除已归档的 `cli-shell-completion`

## [0.6.0] - 2026-09-30 — provider-flag-migration

### ⚠️ Breaking（潜在）
- `ccl provider` 子命令迁到 `flag.NewFlagSet`：
  - 接受 `-x` / `--x` 短形式
  - 互斥锁收紧（`--new` / `--template` 互斥显式化）
  - 保留 `--new` / `--template` 约定

### Features
- `ccl serve --restart` flag：热重启 daemon
- status 输出 session 数拆成「总数 + 今日新增」两统计

### Style
- status label 右对齐

## [0.5.0] - 2026-09-30 — flatten-stop-restore-into-serve

### Features
- 把 `stop` / `restore` 子命令整合为 `ccl serve` 上的 flag
- 新增 `ccl serve --status`
- 备份轮转阈值从 10 提高到 30（merge-on-rotate）

## [0.4.0] - 2026-09-29 — backup-on-control-handover + FORWORD_ namespace

### Features
- **FORWORD_ 命名空间**：OTel per-signal metrics endpoint 接管
  - `launcher.injectOTelEnv` 传播 `FORWORD_*` 到子进程环境
  - `envconfig`：backup + rewrite `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` under `FORWORD_` namespace
  - `collector` forwarder triggers on `FORWORD_`；`/v1/logs` / `/v1/traces` 返回 200 only
- `main` 加 `isUnderCclControl` + `injectOTelEnv` 条件 Backup 触发
- 加 `dbinspect` CLI：用于 `telemetry.db` 排查与清理

### Fix
- stats `TOTAL` 行起始时间零值显示 + JSON omitempty

### Test
- 控制权交接 backup 全 Scenario + 边界 case 覆盖
- `envconfig` / `collector` per-signal metrics E2E 覆盖
- §7.3–§7.6 backup 触发场景 sandbox E2E
- §7.7 归档任务完成

### Docs
- 文档化自动控制权交接 Backup 语义
- README 文档化 `FORWORD_` 命名空间 + per-signal metrics endpoint 接管
- 补 5 个 main spec 的 `Purpose` + `Requirements` headers

## [0.3.0] - 2026-09-29 — usage-monitoring-via-otel + daemon-lifecycle

### Features
- **OTel metrics 采集**：HTTP `:4318` receiver + parser + WAL store + forwarder
- **用量查询**：`ccl stats` 直读 SQLite + 表格 / JSON 输出
- **daemon 生命周期**：`ccl serve` + `Setpgid` fork + `ccl stop` + graceful SIGTERM
- `ccl stats` / `stop` / `restore` 子命令 + 5 个 OTEL env 键备份注入
- 9.3–9.8 manual smoke tests 标 done

### Fix
- 对齐真实 Claude Code v2.1.x OTel payload
- `--help` 输出末尾补换行

### Docs
- README 用量监控小节

## [0.2.0] - 2026-09-27 — improve-help-and-provider-templates

### Features
- `internal/templates` 包：7 个已验证模板
- `--help`、`--template` / `-T`、向导式 `--new` flow + `provider.Create` 重写

### Docs
- README 用法表 + 「用模板配置」章节 + 7-type reference

## [0.1.0] - 2026-09-26 — cross-platform-launcher-and-mode-contract（基线）

### Features
- 初始 Go 模块 + 项目脚手架
- state / provider / config / launcher 模块
- CLI 子命令、TUI、主入口
- TUI provider / mode 选择 + 默认 marker
- TUI 会话恢复步骤（`new` / `continue` / `resume-by-id`）
- session 选择默认 new，第三个选项打开内置 session list

### Fix
- 新 provider 使用 `ANTHROPIC_AUTH_TOKEN`（替代 `ANTHROPIC_API_KEY`）
- TUI 简化：用 `[*]` / `[ ]` 选择器替代 bubbletea list
- `settings.json` 有序读写，保留 key 顺序
- 自动清理冲突的 `ANTHROPIC_AUTH_TOKEN` 与 `ANTHROPIC_API_KEY`
- 命令从 `cc` 改为 `ccl`（避免与 C compiler 冲突）
- 使用 persistent `bufio.Reader` 处理交互输入

### Refactor
- 提取 `internal/modes` 包作为单一事实来源
- 提取 `resolveClaude` + Windows 平台支持

### Test
- 通过探查 Claude CLI 行为代替解析 `--help`
- 锁定 permission mode 列表到 claude CLI（保证 TUI/CLI 一致）
- 导出 `ValidModes` + 明确 permission mode 语义

### Docs
- 添加 README + 构建脚本 + 需求文档

[Unreleased]: https://example.com/compare/v0.9.0...HEAD
[0.9.0]: https://example.com/compare/v0.8.0...v0.9.0
[0.8.0]: https://example.com/compare/v0.7.0...v0.8.0
[0.7.0]: https://example.com/compare/v0.6.0...v0.7.0
[0.6.0]: https://example.com/compare/v0.5.0...v0.6.0
[0.5.0]: https://example.com/compare/v0.4.0...v0.5.0
[0.4.0]: https://example.com/compare/v0.3.0...v0.4.0
[0.3.0]: https://example.com/compare/v0.2.0...v0.3.0
[0.2.0]: https://example.com/compare/v0.1.0...v0.2.0
[0.1.0]: https://example.com/compare/initial...v0.1.0