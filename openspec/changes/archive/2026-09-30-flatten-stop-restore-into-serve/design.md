## Context

`ccl` 当前顶层有 6 个子命令（serve / stats / stop / restore / provider / mode）。其中：
- `serve`（启动 OTel daemon，监听 :4318）
- `stop`（停 daemon）
- `restore`（还原 settings.json 里 ccl 的 OTel env 注入）

三个动作**操作同一对象（ccl 的 OTel 守护链）**的不同生命周期阶段，却分散成三个顶层子命令。这跟 `ccl provider` 把"动作"做成 flag 的内部风格（`provider --list/--new/--delete/--template`）不一致，也跟用户调研时表达的"动作应作为名词命令的参数"诉求矛盾。

现有实现的代码分布：
- `cmd/cc-launcher/main.go:57-65`：路由表分发到 `runServe`/`runStats`/`runStop`/`runRestore`
- `cmd/cc-launcher/serve.go`：`runServe` 直接进入 daemon 启动逻辑，无 flag 解析
- `cmd/cc-launcher/statscmd.go:107-219`：`runStop` + `runRestore` + `stopHelp` + `restoreHelp` + `groupByLabel`（groupByLabel 是 stats 的辅助函数，与 stop/restore 同文件但无依赖关系）
- `cmd/cc-launcher/stop_unix.go` / `stop_windows.go`：`stopDaemon(pid int) error` 跨平台实现（SIGTERM/SIGKILL + 5s 探活）
- `cmd/cc-launcher/statscmd.go:200+`：`restoreOTelEnv` 函数实现（扫 CC_ORIG_* + 删 ccl 默认值）

约束：
- 不引入新依赖（不引 cobra/urfave/cli 之类）
- 不破坏跨平台 stopDaemon 的 build tag 隔离
- 不动 stats（用户明确决定：stats 完全不动）
- 活动 spec 的 requirement 接口要同步更新（daemon-lifecycle、claude-code-env-config）
- archive/ 目录的历史 references 不动

## Goals / Non-Goals

**Goals：**
- `ccl serve` 默认行为不变 = 启动 daemon
- `ccl serve --stop` 等价于原 `ccl stop`
- `ccl serve --restore` 等价于原 `ccl restore`
- 复用 `stopDaemon`、`restoreOTelEnv`、settings.json 读/备份/写逻辑（不重复实现）
- 错误文案、help 文案、退出码语义保持一致
- 顶层 `ccl stop` / `ccl restore` 子命令直接砍掉（不保留兼容期）

**Non-Goals：**
- 不引入 subcommand 风格的 `ccl serve stop` / `ccl serve restore`（用户已选 flag 风格）
- 不重构 runServe 的 daemon 启动逻辑（端口探测、pidfile、SQLite、信号处理）
- 不动 stats 子命令
- 不动 provider / mode 子命令
- 不重构 stopDaemon / restoreOTelEnv 内部
- 不动 archive/ 目录
- 不引入 deprecation 警告 / 兼容期（用户明确要求直接砍）

## Decisions

### D1: 在 runServe 顶部加 flag 解析分发（不引入新的 dispatcher）

**决策**：在 `runServe(args)` 顶部用 `flag.NewFlagSet("serve", flag.ContinueOnError)` 注册 `--stop` 和 `--restore` 两个 bool flag。解析后：
- `--stop == true` → 调 `runStopInner()`（包内 unexported，等价于原 `runStop`，复用 `stopDaemon`）
- `--restore == true` → 调 `runRestoreInner()`（包内 unexported，等价于原 `runRestore`，复用 `restoreOTelEnv`）
- 都没给 → 走原 daemon 启动流程

**原因**：
- `flag.NewFlagSet` 跟 `statscmd.go:32` 的 `runStats` 风格统一，整个 cmd 包内一致
- 不引入 cobra 等外部依赖（仓库当前 0 外部 CLI 依赖，加进来不值）
- 不引入新 dispatcher 类型（c 包内 runServe 本来就是入口，加 flag 解析就地解决）
- 互斥保护：fs 解析后做 `if *stop && *restore { error }` 拒绝（防御性，虽然没人会这么用）

**备选方案**：
- A. 抽 `dispatcher` 类型统一分发 → 过度设计，3 个动作不值
- B. subcommand 风格 `ccl serve stop` → 用户明确选 flag 风格
- C. 用 cobra → 引入依赖、跟现有 style 不一致

### D2: runStop / runRestore 函数签名保留，函数名改 unexported

**决策**：把 `runStop` 改名为 `runStopInner`，`runRestore` 改名为 `runRestoreInner`，仍在 `statscmd.go` 里（因为同包内）。`runServe`（在 `serve.go` 里）调它们。`stopDaemon` / `restoreOTelEnv` / settings.json 处理逻辑**不动**。

**原因**：
- unexported 是因为新接口只剩 `runServe` 一个 caller，没必要继续 export
- 函数体一字不动 = 0 风险
- 跨包依赖不增加（同 main 包）

**备选方案**：
- 把 runStop/runRestore 整段代码直接 paste 到 runServe 里 → 重复代码、维护成本变高
- 移到独立文件 serve_lifecycle.go → 拆得过细

### D3: 顶层 ccl stop / ccl restore 路由表分支删除

**决策**：`main.go:57-65` 的 switch 删除 `case "stop"` 和 `case "restore"` 两行。路由表只保留 `serve` / `stats` / `-h/--help/help`。

**原因**：用户明确选不保留兼容。

**注意**：`main.go:81` (`return c.Run(args)`) 里的 CLI 子命令路由（provider / mode）保留。

### D4: 错误文案与帮助文本处理

**决策**：
- `ccl serve --stop`：找不到 daemon → "daemon not running"（原 stopHelp 文本）
- `ccl serve --restore`：找不到 settings.json → "no settings to restore"；无注入 → "settings.json 无 ccl 注入；restore 是 no-op"
- `ccl serve --help`：保留原 serveHelp，追加两段说明 `--stop` 与 `--restore`
- `ccl --help`（topHelp）：删 stop / restore 子命令条目；serve 一行改为 "serve / serve --stop / serve --restore 三种动作"

**原因**：用户明确要求"复用文案"。

### D5: topHelp 中 stop/restore 条目删除

**决策**：`statscmd.go:18-41` 的 topHelp 删 `stop` 和 `restore` 两行，serve 行扩写。

**注意**：topHelp 是 `ccl --help` 输出的内容，必须更新到与新接口一致。

### D6: README 第 318-325 行的"关闭 / 还原"小节重写

**决策**：把 `ccl stop` / `ccl restore` 命令示例改为 `ccl serve --stop` / `ccl serve --restore`；"不可逆"等说明保持。

### D7: 活动 spec 的 requirement 接口同步改写

**决策**：
- `openspec/specs/daemon-lifecycle/spec.md` 的 Requirement "ccl stop sends SIGTERM to daemon" 全部 scenario 的 WHEN 子句改 `ccl serve --stop`
- `openspec/specs/claude-code-env-config/spec.md` 的 Requirement "ccl restore removes all CC_ORIG_* and ccl-injected defaults" 全部 scenario 的 WHEN 子句改 `ccl serve --restore`

**delta spec** 写法：MODIFIED Requirements 整段重写（instruction 明确要求 "MUST include full updated content"）。

**原因**：保持活动 spec 描述的接口与实际接口一致。

**注意**：`openspec/specs/daemon-lifecycle/spec.md` 第 66 行 scenario "stale pidfile does not block new daemon" 也提到 "ccl stop" — 这是该 requirement 的 scenario 之一（在 "daemon writes a pidfile at startup" 下），同样在 spec delta 里改写。

### D8: 新增 `ccl serve --status`

**决策**：在 `runServe` 的 flag 集合中再增加一个 `--status` bool flag（与 `--stop` / `--restore` 互斥）。命中后调 `runStatusInner()` 输出 daemon 状态，**不走 daemon 启动流程**。

**实现要点**：
- **端口检测**：复用 `probeDaemonAddr()`（probe.go:15）
- **PID 读取**：复用 `readPidfile(pidPath)`
- **DB 文件大小**：`os.Stat(dbPath).Size()`，人类可读化（KB/MB）
- **Session 数**：新增 `stats.CountSessions(dbPath)` —— 用 SQL `SELECT COUNT(*) FROM sessions`，**read-only 打开 db**（同 stats 包用 `mode=ro`）

**状态判断矩阵**：

**输出格式（label 8 字符宽右对齐 + 子字段缩进 10 空格）**：

```
  daemon  running
          pid=N
          port :4318 reachable
      db  ~/.cc-launcher/telemetry.db
          1.2 MB
          12 sessions
```

四态 daemon 矩阵：

| 端口 :4318 | pidfile | daemon 行 |
|---|---|---|
| reachable | 存在 | `  daemon  running` / `pid=N` / `port :4318 reachable` |
| reachable | 缺失 | `  daemon  not running` / `port :4318 in use by foreign service (no pidfile)` |
| unreachable | 存在 | `  daemon  not running` / `stale pidfile pid=N` / `port :4318 unreachable` |
| unreachable | 缺失 | `  daemon  not running` / `port :4318 unreachable` |

DB 行：

- 存在 → `      db  ~/.cc-launcher/telemetry.db` / `<size>` / `<count> sessions`（三行缩进）
- 缺失 → `      db  missing (no telemetry yet)`（单行）

> 实施期用户反馈：原始提案的 `daemon: ...` 单行格式 + `db: <path> — <size> / <count>` 单行格式
> label 宽度不齐（`daemon:` 7 字符 / `port :4318:` 12 字符 / `db:` 3 字符），靠空格手填对齐显得凌乱；
> 绝对路径也太长。改为上述"缩进式分组"——主行 label 固定 8 字符（先左对齐，后改为右对齐），
> 子字段独立行。

**为什么不新增 `Store.Status()` 方法？**
- `--status` 不需要连接 daemon（daemon 可能压根没跑），新方法会绑到 Store，跟现有 `OpenStore` 的开 WAL + 启 worker 的副作用冲突
- 一个简单的 `internal/stats/count.go` 函数（只 COUNT + Close）更合适
- 现有 `stats.QueryStats` 已经是 read-only 打开 db，可以参考其打开方式

**为什么不加 --json？**
- 用户明确只 human-readable；保持改动最小
- 未来若用户需要可独立扩展（加 `--json` 是非破坏性新增）

## Risks / Trade-offs

**[R1] 旧命令直接砍，用户 muscle memory 失效**
→ 接受。v0.x 阶段无承诺兼容性；README 改动明确指向新命令。Mitigation：README "关闭 / 还原" 小节顶部加一行醒目提示。

**[R2] `serve` 字面语义变宽（daemon + settings）**
→ 接受。trade consistency 用 `narrow semantic`。`serve --help` 第一段明写 "OTel daemon lifecycle and ccl settings restore"。Mitigation：详见 D4。

**[R3] runServe 内嵌两种完全不同的代码路径（启动 daemon vs. 立即 stop/restore）**
→ 接受。函数体清晰分支，不引入抽象。Mitigation：runServe 顶部加注释 "if --stop or --restore: branch dispatch; else: start daemon"。

**[R4] `ccl serve --stop --restore` 同时给两 flag**
→ 防御性报错。Mitigation：D1 的互斥检查。

**[R5] 修改两个活动 spec 容易漏掉某处引用**
→ 用 delta spec 整段重写（MODIFIED Requirements）而不是 ADDED，避免场景遗漏。Mitigation：grep 验证 spec 内所有 `ccl stop` / `ccl restore` 都改完。

**[R6] archive/ 目录里也有 `ccl stop` / `ccl restore` 文字**
→ 故意不改。archive 是历史 evidence，改了破坏"那是历史"的语义。Mitigation：tasks.md 任务列表里写明"archive/ 不动"。

## Migration Plan

**部署**：直接改代码 + 改 README + 改活动 spec + 改 help 文案。无需分阶段。

**回滚**：git revert。如果发现问题，回滚到上一个版本即可——本次改动是单一 PR 内的纯接口扁平化，不涉及数据迁移或状态变更。

**测试**：cmd/cc-launcher 现有 `main_test.go` 已有 stop / restore 相关用例；如果有，需要更新断言（按 `--stop` / `--restore` 形式重写）。

（具体测试更新点进 tasks.md 列。）