## Context

`web-usage-stats-ui`（v0.15.0 / v0.15.1 已发布）落地了看板 + provider/项目维度。实施后的复核指出 CLI 契约上的一处错位：`ccl web --backfill` 这个 flag 挂在看板入口下，但它做的事（回填 `sessions.cwd`）与看板页面无关。

本 change 只做** flag 搬家 + 契约同步**，不动看板、不动 db、不动 HTTP 面。

## Decisions

### D1. `--backfill` 挪到 `ccl serve`，进现有的互斥 flag 组

`ccl serve` 现有 4 个 flag 全是"一次性动作后立即返回"，语义上属于 daemon 的生命周期 / 维护面：

| flag | 动作 |
|------|------|
| `--stop` | 停 daemon |
| `--restore` | 还原 settings.json |
| `--status` | 打印状态摘要 |
| `--restart` | 热重启 daemon |
| **`--backfill`** | **回填历史 session 的 cwd** |

五个都是"对 daemon 数据 / 状态的维护动作"，放进同一个互斥组是**语义一致**的，不需要引入第二类 flag。

**为什么不是新增顶层 `ccl backfill`**：那会让顶层命令从 5 个变成 6 个——与本次要解决的问题（顶层命令别 proliferate）背道而驰。

**为什么不是 `ccl stats --backfill`**：`stats` 是**只读**的查询面（直读 db 打印表格），而回填是**写**动作。往只读命令里塞写操作会破坏 `ccl stats` 的心智模型（"它不写任何东西"）。

### D2. `ccl web` 收敛为纯看板入口

保留：

| 用法 | 行为 |
|------|------|
| `ccl web` | 确保 daemon 在跑（不在则 fork），打印带 token 的看板 URL |
| `ccl web --rotate` | 轮换 token，提示旧 URL 与旧 session 失效 |
| `ccl web --open` | 用系统默认浏览器打开看板 URL |

这三个动作全部是"**关于看板**"的——打印入口、吊销入口、打开入口。名字与动作对齐。

### D3. 回填实现挪到 serve 侧，逻辑不变

回填的实现在 `internal/collector.BackfillCwdInFile`（独立进程直接操作 db 文件）。本次只是把**调用点**从 `cmd/cc-launcher/web.go` 挪到 `cmd/cc-launcher/serve.go`，并让它复用 `newCwdResolver()`（本来就在 cmd 包里，跨文件同包可见）。

行为完全不变：不限量、幂等（只处理 `cwd = ''` 的行）、不改 schema、打印 resolved / failed 计数。

### D4. 破坏性变更的处理

`ccl web --backfill` 从此是 unknown flag → 报 `flag provided but not defined: -backfill` 并 exit 1。

这是**有意的破坏性变更**：回填是低频维护动作，用户看到 CHANGELOG 的 Compatibility 段就能知道新位置。**不做别名兼容**——`ccl web --backfill` 与 `ccl serve --backfill` 并存会让"到底哪个是对的"变成新的困惑源。

## Risks / Trade-offs

- **[R1] 旧脚本里的 `ccl web --backfill` 会失败** → CHANGELOG 的 Compatibility 段明确写出新位置；报错信息本身（unknown flag）足够自解释
- **[R2] `ccl serve` 的 flag 从 4 个变 5 个，帮助文本变长** → 接受：`serve` 本来就是"daemon 维护"这一类命令的容器，5 个动作仍在一屏内；换来的是顶层命令数不变
- **[R3] 互斥组变 5 个** → `ccl serve --backfill --status` 现在会报互斥错误。两者语义上确实不该同时给（一个写数据一个读状态），报错是可接受的

## Migration Plan

1. 改 `cmd/cc-launcher/web.go`（去 `--backfill`）+ `cmd/cc-launcher/serve.go`（加 `--backfill`）
2. 同步 `cli-help` / `cli-shell-completion` / `daemon-lifecycle` 三个 spec
3. 单测 + smoke 测试锁死：web 的补全候选不含 `--backfill`；serve 的含 `--backfill`；`ccl serve --backfill` 真跑一次回填
4. 发布为 patch 版本（v0.15.2），CHANGELOG 的 Compatibility 段写明 flag 搬家

## Open Questions

- 是否该把 `ccl stats` 也做成"只读"到极致（比如未来加 `--web` 直接开看板）？当前认为不需要——`ccl web --open` 已经覆盖
