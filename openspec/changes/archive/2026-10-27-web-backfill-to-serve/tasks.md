## 1. flag 搬家：--backfill 从 ccl web 挪到 ccl serve

- [x] 1.1 `cmd/cc-launcher/web.go`：`registerWebFlags` 去掉 `--backfill`（返回值从三元组变二元组）；`runWeb` 去掉 `--backfill` 分支
- [x] 1.2 `cmd/cc-launcher/web.go`：`webHelp` 去掉 `--backfill` 行
- [x] 1.3 `cmd/cc-launcher/serve.go`：`registerServeFlags` 加 `--backfill`；互斥组从 4 个扩到 5 个；分发到回填实现（`if *backfillFlag { return runBackfillInner() }`）
- [x] 1.4 `cmd/cc-launcher/serve.go`：新增 `runBackfillInner` —— 不限量的历史 cwd 回填（复用 `collector.BackfillCwdInFile` + `newCwdResolver()`），打印 resolved / failed 计数
- [x] 1.5 `cmd/cc-launcher/serve.go`：`serveHelp` 加 `--backfill` 段（一行说明 + 语义边界）
- [x] 1.6 单测：`ccl serve --backfill --stop` 报互斥错误（exit 非 0 + stderr 说明）；`ccl web --backfill` 报 unknown flag

## 2. 契约同步

- [x] 2.1 顶层 `topHelp` 不变（`web` 条目仍在）——但 `web` 的一行描述里不得再提回填
- [x] 2.2 shell completion 自动跟随：`ccl __complete web ""` 输出 `--rotate / --open / -h / --help`（无 `--backfill`）；`ccl __complete serve ""` 输出含 `--backfill`
- [x] 2.3 `ccl __complete serve --stop ""` 仍输出空（互斥防御覆盖新增的第 5 个 flag）
- [x] 2.4 smoke 测试补断言：web 补全候选不含 `--backfill`；serve 补全候选含 `--backfill`

## 3. 端到端验证

- [x] 3.1 `ccl serve --backfill` 真跑一次：resolved / failed 计数打印、cwd 被补上、跑两次结果一致
- [x] 3.2 `ccl web --backfill` → 报 unknown flag 并 exit 非 0
- [x] 3.3 `ccl web` / `ccl web --help` 仍正常（看板入口不受影响）
- [x] 3.4 `go build ./...` + `go test ./...` + `go test -tags smoke ./...` 全绿
- [x] 3.5 `openspec validate --all --strict` 通过；`openspec archive` 归档
