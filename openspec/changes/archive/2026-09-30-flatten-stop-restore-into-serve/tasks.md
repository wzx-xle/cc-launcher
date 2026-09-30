## 1. 准备 runStop / runRestore 内部化

- [x] 1.1 把 `cmd/cc-launcher/statscmd.go` 里的 `runStop` 改名为 `runStopInner`（函数体不动；stopHelp 文案保留供 serveHelp 复用）
- [x] 1.2 把 `cmd/cc-launcher/statscmd.go` 里的 `runRestore` 改名为 `runRestoreInner`（函数体不动；restoreHelp 文案保留供 serveHelp 复用）
- [x] 1.3 确认 `stopDaemon`（stop_unix.go / stop_windows.go）和 `restoreOTelEnv`（statscmd.go）函数签名 / 实现体不动

## 2. runServe 加 flag 解析分发

- [x] 2.1 在 `cmd/cc-launcher/serve.go` 的 `runServe` 顶部加 `flag.NewFlagSet("serve", flag.ContinueOnError)`，注册 `--stop` 和 `--restore` 两个 bool flag（含 / --status 占位）
- [x] 2.2 解析后：若 `*stop == true` → 调 `runStopInner()` 后 `return nil`（不走 daemon 启动）
- [x] 2.3 解析后：若 `*restore == true` → 调 `runRestoreInner()` 后 `return nil`（不走 daemon 启动）
- [x] 2.4 加互斥检查：`*stop && *restore` 同时为真时返回错误并打 "用法" 提示（含 / --status 三者互斥）
- [x] 2.5 未命中两个 flag 时走原 daemon 启动流程（端口探测 / SQLite / 信号处理等不动）

## 3. main.go 路由表删除

- [x] 3.1 删除 `cmd/cc-launcher/main.go:57-65` 中 `case "stop"` 和 `case "restore"` 两个分支
- [x] 3.2 路由表保留：`serve / stats / -h / --help / help` 五个 case + 落到 `cli.Run` 的兜底分支

## 4. help 文案同步

- [x] 4.1 `statscmd.go:18-41` 的 `topHelp`：去掉 stop 和 restore 两条子命令条目；serve 一行改写为"启动 / 停 daemon 或还原 settings.json（详见 `ccl serve --help`）"
- [x] 4.2 `serve.go:145-156` 的 `serveHelp`：在"用法: ccl serve"行下方追加 `--stop` 与 `--restore` 两个 flag 的说明；首段说明加上"OTEL daemon lifecycle and ccl settings restore"的措辞（应对 design D2 风险 R2）

## 5. README 同步

- [x] 5.1 README "用量监控 / 关闭 / 还原"小节（第 318-325 行）：把 `ccl stop` 命令示例改为 `ccl serve --stop`；把 `ccl restore` 改为 `ccl serve --restore`；"不可逆"等说明保持
- [x] 5.2 README 顶部如有 `ccl --help` 输出截图/示例，同步更新（grep `ccl stop\|ccl restore` 全仓验证 README 已无残留）

## 6. 活动 spec 同步

- [x] 6.1 `openspec/specs/daemon-lifecycle/spec.md`：Requirement "ccl serve --stop sends SIGTERM to daemon" 下的所有 scenario 的 WHEN 子句从 `ccl stop` 改为 `ccl serve --stop`；"stale pidfile does not block new daemon" scenario 也同步改写
- [x] 6.2 `openspec/specs/claude-code-env-config/spec.md`：Requirement "ccl serve --restore removes all CC_ORIG_* and ccl-injected defaults" 下的所有 scenario 的 WHEN 子句从 `ccl restore` 改为 `ccl serve --restore`
- [x] 6.3 **不动 archive/ 目录下任何引用**——archive 是历史 evidence；任务完成后 grep `openspec/changes/archive/` 内仍有 `ccl stop` / `ccl restore` 是预期行为

## 7. 测试更新与 smoke

- [x] 7.1 查看 `cmd/cc-launcher/main_test.go`：如有 stop / restore 相关用例，按新接口更新断言（`ccl serve --stop` / `ccl serve --restore`）；如无新增最小覆盖
- [x] 7.2 `go build ./...` 编译通过（cmd/cc-launcher + cmd/dbinspect）
- [x] 7.3 smoke 1：`ccl serve --help` 输出包含 `--stop` 与 `--restore` 两个 flag 说明
- [x] 7.4 smoke 2：`ccl serve --stop` 在 daemon 没跑（无 daemon.pid）时输出 "daemon not running"，exit code 0
- [x] 7.5 smoke 3：`ccl serve --restore` 在 `~/.claude/settings.json` 不存在时输出 "no settings to restore"，exit code 0
- [x] 7.6 smoke 4：`ccl serve --stop --restore` 同时给两 flag 时返回错误（非 0 exit）
- [x] 7.7 smoke 5：直接跑 `ccl stop` 和 `ccl restore` 都报错"未知子命令"，exit code 非 0（旧命令已砍）

## 8. 收尾

- [x] 8.1 grep `ccl stop\|ccl restore` 全仓非 archive 路径：除 prose（README 帮助文本 / spec 文字）外应无残留引用
- [x] 8.2 commit message 用简体中文（按 `git-commit-msg-chinese.md` 偏好）：`feat(serve): 把 stop 和 restore 整合为 flag（ccl serve --stop / ccl serve --restore）`
- [x] 8.3 （可选）若 git status 中 `M cmd/cc-launcher/main.go` 是用户已开启的本地未提交改动，先 stash 或协调合并顺序（当前 git status 无未提交 main.go 改动，跳过 stash）

## 9. 新增 ccl serve --status

- [x] 9.1 在 `internal/stats/` 下新增 `count.go`，提供 `func CountSessions(dbPath string) (sessions int64, err error)`：read-only 打开 db（同 `query.go` 的 `?mode=ro` 方式），`SELECT COUNT(*) FROM sessions`，defer close，返回值
- [x] 9.2 在 `cmd/cc-launcher/statscmd.go` 末尾新增 `runStatusInner()`（包内 unexported），函数体逻辑：
  - 读 `daemon.pid` 拿 pid
  - 调 `probeDaemonAddr()` 拿 portReachable
  - 按 D8 状态判断矩阵打印 daemon 行
  - `os.Stat(dbPath)` 拿 Size；不存在 → `db: missing (no telemetry yet)`；否则打印 `db: <path> — <size> KB, <count> sessions`
  - 返回 nil
- [x] 9.2a 新增 `humanBytes(size int64) string` 小 helper（包内 unexported，建议放在 statscmd.go 末尾靠近 runStatusInner）：
  - `< 1024` → `"<n> B"`
  - `< 1024*1024` → `"<n.n> KB"`（保留 1 位小数）
  - `< 1024*1024*1024` → `"<n.n> MB"`（保留 1 位小数）
  - `≥ 1 GB` → `"<n.n> GB"`（保留 1 位小数）
  - 仅用于 `--status` 输出，**不导出**，不做 locale 处理（统一 SI 单位，不用 KB=1024 之外的语义）
- [x] 9.3 `serve.go` 的 `runServe` flag 集合加 `--status` bool flag；解析后若为 true → 调 `runStatusInner()` 并 `return nil`（不走 daemon 启动）
- [x] 9.4 `runServe` 顶部加三 flag 互斥检查：`*stop && *restore`、`*stop && *status`、`*restore && *status` 三种组合都报错并打 "用法" 提示
- [x] 9.5 `serveHelp` 增加 `--status` 一段说明："打印 daemon 状态摘要（pid / 端口可达 / db 文件大小 / session 数）；db 缺失时不报错，输出 'db: missing'。与 --stop / --restore 互斥。"
- [x] 9.6 smoke 7：daemon 没跑（无 pidfile + 端口空）时 `ccl serve --status` 输出 `daemon: not running` + `port :4318: unreachable` + db 行（若 db 存在），exit 0
- [x] 9.7 smoke 8：fork `ccl serve` 后跑 `ccl serve --status`，输出 `daemon: running (pid=N, port :4318 reachable)` + db 行
- [x] 9.8 smoke 9：`ccl serve --stop --status` 同时给两 flag 时返回错误（非 0 exit）