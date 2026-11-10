## 1. 共享路径工具包

- [x] 1.1 新建 `internal/pathutil/pathutil.go`：`Shorten(p string) string` —— `os.UserHomeDir()` 成功且 `strings.CutPrefix(p, home)` 命中时返回 `"~" + 剩余部分`，否则原样返回 `p`；home 取不到时原样返回（**不报错、不 panic**）。函数注释写明两件事：①语义与既有 `cmd/cc-launcher/statscmd.go` 的 `shortenHomePath` **逐字一致**（等价重构）；②**只用于展示**，不能拿它的结果去读写真实文件（那是 `~` 不是路径）
- [x] 1.2 新建 `internal/pathutil/pathutil_test.go`，覆盖四种情况：home 命中（`/home/u/.claude/settings.json` → `~/.claude/settings.json`）、路径不在 home 下（`/etc/x` → 原样）、`p` 恰好等于 home（→ `~`）、home 不可用（`HOME` / `USERPROFILE` 置空 → 原样返回）
      —— 用 `t.Setenv` 改环境变量，**不要用 `os.Setenv`**（后者在 `-race` 下会与其他并行测试串味，且不会自动还原）
- [x] 1.3 确认 `Shorten` **不做路径分隔符归一化**：Windows 上 `C:\Users\me\.claude\settings.json` → `~\.claude\settings.json`，反斜杠原样保留（design 决策 D4）

## 2. CLI 侧改用共享实现

- [x] 2.1 `cmd/cc-launcher/statscmd.go`：删掉私有的 `shortenHomePath`，调用点（`ccl serve --status` 里的 db 路径打印）改用 `pathutil.Shorten` —— **行为等价重构**，`ccl serve --status` 的输出必须逐字不变
- [x] 2.2 `go build ./... && go vet ./...` 确认没有遗留的 `shortenHomePath` 引用（grep 复核一次）

## 3. web Claude 页脱敏

- [x] 3.1 `internal/web/claude.go`：`handleAPIClaudeFiles` 组装 `claudeFileItem` 时，把 `Path` 设为 `pathutil.Shorten(spec.path)`
      —— **只在响应组装处脱敏**：`claudeFileSpecs` / `claudeFilePath` 保持绝对路径不动（读、写、备份三条路径都依赖它，改源头会直接把读写搞坏，design 决策 D2）
- [x] 3.2 `internal/web` 层补测试：`GET /api/claude/files` 返回的每个 `path` **以 `~` 开头**且**不含 home 的绝对路径**；同时断言 `GET`/`PUT /api/claude/file/{id}` 仍能正常读写（证明真实路径没被改坏）

## 4. 验证

- [x] 4.1 `go build ./... && go vet ./... && go test ./...` 全绿；`gofmt -l .` 为空（CI 已加 gofmt 检查，格式不对会红）
- [x] 4.2 `openspec validate web-home-path-tilde`（或该 schema 对应的校验命令）通过
- [x] 4.3 手工：起 daemon 后 `curl "localhost:4318/api/claude/files?token=$(cat ~/.cc-launcher/web-token)"`，确认 `path` 是 `~/.claude/settings.json` / `~/.claude/CLAUDE.md`，且响应体里搜不到 home 的绝对路径
- [x] 4.4 手工：web 页 Claude 视图，侧边栏条目与编辑区标题行都显示 `~` 形式；点进去仍能正常读到文件内容（读写没被脱敏改坏）
