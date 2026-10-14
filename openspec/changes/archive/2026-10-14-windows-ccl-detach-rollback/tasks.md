# Tasks: windows-ccl-detach-rollback

## 1. 改 launcher_windows.go

- [x] 1.1 `internal/launcher/launcher_windows.go`：`cmd.Start() + os.Exit(0)` → `cmd.Run()` + ExitError 透传 + `os.Exit(0)`
- [x] 1.2 恢复 `errors.As` + `*exec.ExitError` + `os.Exit(exitErr.ExitCode())` 块（恢复 child exit code 透传）
- [x] 1.3 恢复 `errors` import（cmd.Start() 不再需要 errors 包，但 cmd.Run() + ExitError 需要）
- [x] 1.4 `configureCmd` 注释从 "cmd.Start() 之前" 改回 "cmd.Run() 之前"
- [x] 1.5 函数 doc 注释从 "启动后立即退出，claude 独立运行" 改回 "启动子进程、继承当前终端的 stdio，等待其结束后以相同退出码退出，从而对用户表现得与 Unix 上的'替换进程'一致"
- [x] 1.6 删除 `cmd.Start() + return err` 路径（恢复 `cmd.Run()` 单一路径）
- [x] 1.7 字面等价 v0.11.0 之前的 `internal/launcher/launcher_windows.go`（git show 9f9c110~1:internal/launcher/launcher_windows.go 对照——`diff` 已验证 BYTE-EXACT）

## 2. 验证

- [x] 2.1 `go build ./...` 通过
- [x] 2.2 `GOOS=windows go build ./...` 通过
- [x] 2.3 `GOOS=linux go build ./...` 通过（确认 Unix 端无意外）
- [x] 2.4 `go test ./internal/launcher/...` 全绿（现有 2 个 Windows 测试用 configureCmd hook + panic 中断 cmd.Run，不影响）
- [x] 2.5 `go test ./...` 全量绿
- [x] 2.6 `go vet ./...` 无 warning

## 3. 文档

- [x] 3.1 `CHANGELOG.md` `[Unreleased]` 段加 revert 条目："v0.11.1 detach 设计在 Windows Terminal 下导致 Claude Code 收不到键盘事件（连 Ctrl+C 也不行），撤回 detach 设计，恢复 v0.11.0 之前的 `cmd.Run()` 同步 wait + 透传 exit code 行为"
- [x] 3.2 `README.md` "Windows 启动行为" 段（如有）：grep 无匹配，README 无 Windows 段，跳过

## 4. OpenSpec 归档

- [x] 4.1 `openspec validate windows-ccl-detach-rollback --strict` 通过
- [x] 4.2 `openspec archive windows-ccl-detach-rollback --yes`
- [x] 4.3 确认 `openspec/specs/cross-platform-launcher/spec.md` 的 Windows 段已恢复 wait 语义（`diff` 验证 BYTE-EXACT 跟 v0.11.0 之前一致）