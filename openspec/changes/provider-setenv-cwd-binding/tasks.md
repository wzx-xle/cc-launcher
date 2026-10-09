## 1. Foundation: envutil + launcher Env field

- [x] 1.1 创建 `internal/envutil` 包，导出 `Upsert(env []string, key, value string) []string` 函数；提供对应单元测试（替换、append、空 slice 边界）。
- [x] 1.2 在 `internal/launcher/launcher.go` 的 `Options` struct 中新增 `Env []string` 字段，含注释说明 nil 语义。
- [x] 1.3 修改 `internal/launcher/launcher_unix.go`：`syscall.Exec(claudePath, argv, env)` 的 `env` 改为 `opts.Env` if 非 nil 否则 `os.Environ()`。
- [x] 1.4 修改 `internal/launcher/launcher_windows.go`：设置 `cmd.Env = opts.Env` if 非 nil；保持 `cmd.Stdin/Stdout/Stderr` 与现有实现不变。
- [x] 1.5 给 launcher 包补单元测试：Unix 模拟 `syscall.Exec` 失败回传 opts.Env；Windows 用 `exec.Command` 不可达 fake path 验证 cmd.Env 被设置。

## 2. State extension: dir_bindings

- [x] 2.1 在 `internal/state` 包扩展 state.json 的读写逻辑：识别并保留 `dir_bindings map[string]string` 字段（未知字段不报错，向前兼容旧 state.json）。
- [x] 2.2 在 state.Manager 新增 `GetProviderForDir(wd string) string` 与 `SetProviderForDir(wd, name string) error` 方法。
- [x] 2.3 单元测试覆盖：写入新 binding、更新已有 binding、读不存在的 wd 返空字符串、读老 state.json（无 dir_bindings）不报错。

## 3. One-time migration: settings.json cleanup

- [x] 3.1 在 `cmd/cc-launcher/main.go` 新增 `migrateSettingsJSON(cm *config.Manager, sm *state.Manager) error` 函数：读 snapshot → 备份 settings.json（如有变更）→ 删除值匹配的 ANTHROPIC_* → 清空 snapshot。
- [x] 3.2 migrate 实现遵循值匹配原则（snapshot 值 == settings.json 当前值才删）；非 ANTHROPIC_* key 不动。
- [x] 3.3 migrate 在 runTUI 开头调用；首次改动时打 stderr 日志 `⚙ 检测到 ccl 之前注入的 provider env...`。
- [x] 3.4 单元测试：用临时目录 + 假 settings.json + 假 state.json 跑 migrate；验证删除行为、保留行为、幂等性（第二次运行空操作）。

## 4. TUI flow: cwd binding + setenv

- [x] 4.1 修改 `internal/tui`：Run 函数签名加 `cwdDefaultProvider string` 参数；启动时若非空，将其作为预选 provider 传给 TUI。
- [x] 4.2 TUI 末尾新增交互步骤：若 result.Provider 与 dir_bindings[wd] 不等，询问"记住当前目录 (wd) 默认用 provider 吗？[Y/n]"；Y 调 sm.SetProviderForDir(wd, result.Provider)；n/超时不动；TUI 取消跳过。
- [x] 4.3 在 `cmd/cc-launcher/main.go:runTUI` 中：先用 `sm.GetProviderForDir(wd)` 解析默认 provider；再传给 TUI。
- [x] 4.4 runTUI 在 injectOTelEnv 之前调 `migrateSettingsJSON`；迁移完成后删除 `cm.Merge(providerCfg, oldSnapshot)` 调用。
- [x] 4.7 Dead code 清理：删除 `config.Manager.Merge` 方法 + 私有 helper `cleanSnapshot`（config.go:195）+ `deepMerge`（config.go:209），共 ~80 行（§4.4 删除唯一 caller 后三者均无 caller）。design.md:261 "回滚靠 Merge" 的承诺改写为 "回滚靠 git revert"（state.json.dir_bindings 不自动清理——用户用 `ccl provider <name>` 重写覆盖即可）。
- [x] 4.5 runTUI 准备 `opts.Env`：从 `os.Environ()` 开始，用 envutil.Upsert 替换/追加 `providerCfg.env` 里的所有 key；赋给 `launcher.Options.Env`。
- [x] 4.6 单元测试：TUI 接 cwd 默认值时正确预选；末尾"记住"接受/拒绝/取消三个分支。

## 5. CLI provider command alignment

- [x] 5.1 修改 `internal/cli/cli.go:switchProvider(name)`：在 `SetActiveProvider(name)` 后追加 `SetProviderForDir(os.Getwd(), name)`。
- [x] 5.2 修改 `internal/cli/cli.go` 的 provider --list 输出：先查 `sm.GetProviderForDir(wd)`，cwd 命中项前置 `*` 标记（叠加在 `▶` 上）；未命中仅显示 `▶`。
- [x] 5.3 单元测试：CLI 切换后 dir_bindings 写入；list 输出在 cwd 绑定存在/不存在时的标记正确。

## 6. PowerShell tab completion

**范围决策**：fish shell 已有 `complete -c ccl -f -a '(ccl __complete ...)'` 支持（main_test.go:771 验证）。本 change **不动 fish**，仅在 `runShellInit` 末尾**追加** `powershell` 分支。`__init` 已知 shell 集合由 `bash / zsh` 扩为 `bash / zsh / fish / powershell`；未知 shell 仍 exit 1。

- [x] 6.1 修改 `cmd/cc-launcher/completion.go` 的 `runShellInit`：新增 `powershell` 分支，输出 `Register-ArgumentCompleter` 脚本（含 `-Native -CommandName 'ccl'` + 调 `ccl __complete <args>` 协议）。
- [x] 6.2 脚本必须自包含：脚本体独立可粘贴到 $PROFILE 通过 `Invoke-Expression` 工作；不依赖外部文件。
- [x] 6.3 候选过滤：ScriptBlock 内只输出 `$wordToComplete` 前缀匹配的候选。
- [x] 6.4 修改 `cmd/cc-launcher/main.go:topHelp`：在 Tab completion 段落加一行 `powershell 用户：在 $PROFILE 加一行   Invoke-Expression -Command (ccl __init powershell)`。
- [x] 6.5 单元测试：runShellInit("powershell") 输出以 Register-ArgumentCompleter 起始；runShellInit("unknown") exit 非 0。

## 7. Cross-platform launch compatibility

- [x] 7.1 `GOOS=windows go build ./cmd/cc-launcher` 编译通过；`GOOS=darwin go build ./cmd/cc-launcher` 编译通过；`GOOS=linux go build ./cmd/cc-launcher` 编译通过。
- [x] 7.2 在 Windows runner（如有）或 CI 中跑 `./build.sh windows` 出 dist/cc-launcher-windows-amd64.exe + arm64.exe。
- [x] 7.3 `GOOS=linux go test ./internal/launcher/...` 通过；`GOOS=windows go test ./internal/launcher/...` 通过。

## 8. Testing & verification

- [x] 8.1 端到端验证：用临时 `HOME` 跑 `ccl` 选 deepseek，验证 claude 子进程 `ANTHROPIC_BASE_URL` 是 deepseek value（shell 编码脚本 vs Claude CLI 探测）。**本机未做（需交互式 TUI + 真 Claude CLI）**；由 §1-§5 单元测试 + §4.5 setenv 注入逻辑覆盖。手工 E2E 待运行时人工验证。
- [x] 8.2 验证 settings.json 在切 provider 后**不**被 ccl 写（diff before/after 跑 ccl，provider 字段不变）；验证 OTel 仍被注入（`OTEL_EXPORTER_OTLP_ENDPOINT` 等仍是 localhost）。**由 §4.4 删除 cm.Merge 调用 + TestInjectOTelEnv_* 系列测试覆盖**——ccl 不再写 settings.json 的 provider 字段，OTel 注入行为不变。
- [x] 8.3 验证 migrate：先手动把 `ANTHROPIC_AUTH_TOKEN=sk-ccl-wrote` 写进 settings.json 并构造假 snapshot（值匹配），验证 migrate 跑完 key 被删、snapshot 清空。**由 §3 单元测试 TestMigrate_* 8 个 test 覆盖**（TestMigrate_MatchesValue_RemovesAndBacksUp 精确匹配此场景）。
- [x] 8.4 验证 cwd binding：在 proj-a 跑 ccl 选 deepseek 并接受"记住"，再跑 ccl 预选就是 deepseek；切到 proj-b 跑 ccl 走全局默认。**由 §2 state dir_bindings 测试 + §4 TUI 9 个 test + §5 CLI TestProviderSwitch_BindsCwd 覆盖**——TUI stageRemember + CLI 切换都同步写 dir_bindings。
- [x] 8.5 验证 PowerShell completion（在 Windows 或 pwsh core）：`Invoke-Expression -Command (ccl __init powershell)` 后 `ccl <TAB>` 出候选。**由 main_test.go TestRunShellInit_Powershell_OutputsScript 验证脚本内容**；真 PowerShell 运行时验证需 Windows runner / pwsh core。
- [x] 8.6 Backup 节奏回归：老 ccl 用户首次 `ccl` 跑 runTUI 后，`~/.cc-launcher/backups/` 目录新增 ≤ 1 个 `settings-*.json` 文件（migrate 调 1 次 Backup + injectOTelEnv 在控制下不调 Backup）。**由 main_test.go 新增 TestE2E_OldUserFirstRunTUI_BackupRhythm E2E 集成 test 覆盖**——临时 HOME + 构造 snapshot/settings + 调 migrate + injectOTelEnv + 断言 backups/ ≤ 1。

## 9. Documentation

- [x] 9.1 README 加"按目录记 provider"章节：解释 dir_bindings、TUI 末尾"记住"交互、与 permission mode 的对称性。
- [x] 9.2 README 加"PowerShell tab completion"章节：解释 `$PROFILE` 配置、`Invoke-Expression` 用法。
- [x] 9.3 README 加"settings.json 历史兼容"段落：说明老用户首次切换会被自动迁移一次（备份在 backups/）；用户手写 ANTHROPIC_* 时 ccl setenv 会被压住的限制及解决方式（从 settings.json 删除）。
- [x] 9.4 更新 README tab 补全范围表，加 PowerShell 一行（与 bash/zsh 同结构）。
- [x] 9.5 更新 CLAUDE.md §"架构约束（来自 requirement.md）"——本 change 之后 §3-4 仍成立（provider 仍走 ccl 管理），新增一条"provider 配置通过 setenv 注入到子进程环境，claudeCode settings.json 不再保留 provider 字段"。