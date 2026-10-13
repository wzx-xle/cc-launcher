# Tasks: windows-ccl-detach-after-launch

## 1. 改 launcher_windows.go

- [x] 1.1 `internal/launcher/launcher_windows.go`：`cmd.Run()` → `cmd.Start()`
- [x] 1.2 删除 `errors.As` + `*exec.ExitError` + `os.Exit(exitErr.ExitCode())` 块（不再 wait 子进程）
- [x] 1.3 `cmd.Start()` 失败仍 `return err`（保留诊断能力）
- [x] 1.4 启动成功：`os.Exit(0)` 立即终止 ccl 进程
- [x] 1.5 函数注释更新：从"等待其结束后以相同退出码退出"改为"启动后立即 os.Exit(0)，与 Unix syscall.Exec 进程 image 替换的用户感知一致"
- [x] 1.6 删除不再用的 `errors` import（如整个文件不再用 errors）

## 2. 验证

- [x] 2.1 `go build ./...` 通过
- [x] 2.2 `GOOS=windows go build ./...` 通过
- [x] 2.3 `go test ./internal/launcher/...` 全绿（现有 2 个 Windows 测试用 configureCmd hook + panic 中断 cmd.Run/Start，不影响）
- [x] 2.4 `go test ./...` 全量绿
- [x] 2.5 `go vet ./...` 无 warning

## 3. 文档

- [x] 3.1 `CHANGELOG.md` `[Unreleased]` 段加 **BREAKING** 条目：Windows ccl 启动 claude 后立即退 0，不再 wait 透传 exit code
- [x] 3.2 `README.md`（如有 "TUI 启动" 或 "跨平台行为" 小节）补一句："Windows 上 ccl 启动 Claude Code 后立即退 0，claude 退出 code 由 cmd.exe → node.exe → claude.exe 进程链传递给 PowerShell 的 $LASTEXITCODE（与 Unix 上 ccl 被替换进程 image 行为对齐）"

## 4. OpenSpec 归档

- [x] 4.1 `openspec validate windows-ccl-detach-after-launch --strict` 通过
- [x] 4.2 `openspec archive windows-ccl-detach-after-launch --yes`
