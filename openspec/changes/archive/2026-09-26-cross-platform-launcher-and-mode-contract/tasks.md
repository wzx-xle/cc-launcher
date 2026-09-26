## 1. Launcher 重构与跨平台支持

- [x] 1.1 把 `launcher.Launch` 拆成跨平台的 `resolveClaude(opts) (path, args, err)` 与平台特定的 `Launch`
- [x] 1.2 新增 `internal/launcher/launcher_unix.go`（`//go:build !windows`），保留 `syscall.Exec` 语义
- [x] 1.3 新增 `internal/launcher/launcher_windows.go`（`//go:build windows`），实现 spawn 子进程 + 透传退出码 + `.cmd/.bat` 走 `cmd /c`
- [x] 1.4 `launcher_windows.go` 末尾 `os.Exit(0); return nil` 加注释说明（Go 编译器要求 + 模拟 syscall.Exec 语义）

## 2. 权限模式列表维护

- [x] 2.1 `internal/cli/cli.go`：把 `validModes` 重命名为 `ValidModes` 并导出
- [x] 2.2 `internal/tui/tui.go`：修正 `modeItems` 的中文描述（特别是 `dontAsk`、`auto`、`bypassPermissions`、`plan`）
- [x] 2.3 `internal/tui/tui.go`：把 `manual` 的描述与 Claude Code 内部 `default` 别名关系说清楚

## 3. 契约测试

- [x] 3.1 新增 `internal/tui/modes_test.go`：常规测试，锁死 `modeItems` 与 `cli.ValidModes` 排序后一致 + 每个 mode 都有非空中文描述
- [x] 3.2 新增 `internal/cli/permission_contract_test.go`（`//go:build contract`）：解析 `claude --help` 的 `(choices: ...)`，与 `cli.ValidModes` 比对；本机无 `claude` 时 SKIP

## 4. 构建脚本与文档

- [x] 4.1 `build.sh` 新增 `cross_build` / `windows_build` / `release` 子命令
- [x] 4.2 `build.sh` 新增 `contract_test` 子命令并接入 `case` 分支
- [x] 4.3 `build.sh` 的 `clean` 同步清理 `dist/`
- [x] 4.4 `.gitignore` 增加 `/dist/`
- [x] 4.5 `README.md` 更新权限模式表（修正描述、加 dontAsk 警告），新增"契约测试"小节，说明 `./build.sh contract` 与 build tag 隔离

## 5. 验证

- [x] 5.1 `go build ./...` 在 Linux 上通过
- [x] 5.2 `GOOS=windows go build ./...` 交叉编译通过
- [x] 5.3 `go vet ./...` 无警告
- [x] 5.4 `go test ./...` 全部通过
- [x] 5.5 `go test -tags contract ./...` 通过（契约测试在本机跑通或 SKIP）

## 6. 提交

- [x] 6.1 commit: refactor+feat(launcher) — 拆分 Launch、引入 Windows 实现
- [x] 6.2 commit: feat(cli)+feat(tui) — 导出 ValidModes、修正 mode 描述
- [x] 6.3 commit: test — 新增 contract test + TUI/CLI 一致锁
- [x] 6.4 commit: docs+chore — README 契约章节、build.sh 新子命令、.gitignore
- [x] 6.5 commit: openspec change artifacts — proposal/design/specs/tasks

## 7. 重构与契约测试加固

- [x] 7.1 新增 `internal/modes` 包：把 names + 中文描述从 cli / tui 抽出，作为单一事实源；`cli` 与 `tui` 都依赖 `modes`；删除 `internal/tui/modes_test.go` 的跨包 import，改在 `internal/modes/modes_test.go` 锁 names / descriptions / ByName。
- [x] 7.2 把契约测试从脆弱的 `--help` 文本解析改为行为探测：`claude --permission-mode <probe>` 解析错误信息中的 `Allowed choices are ...`；本机 `claude --permission-mode __cc_launcher_probe__` 实测返回稳定单行消息，可直接正则提取。
- [x] 7.3 `internal/cli/cli.go` 移除 `ValidModes`，改为 `modes.Names()`。
- [x] 7.4 `internal/tui/tui.go` 移除 `modeItem` / `modeItems`，model.modes 改用 `[]modes.Mode`，字段访问从 `.mode/.desc` 改为 `.Name/.Description`。