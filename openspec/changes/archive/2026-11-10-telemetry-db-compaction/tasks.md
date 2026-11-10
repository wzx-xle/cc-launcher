## 1. 白名单常量与叶子包

- [x] 1.1 新建 `internal/telemetry/labels/labels.go`：导出 `Stored = []string{"type", "model", "ccl.provider", "provider"}` + `func Contains(key string) bool`
      —— **必须放叶子包**：设计原写"在 `internal/stats` 的测试里断言 stats 用到的 key ⊆ 白名单"，但 `internal/stats` 目前**不 import 任何 internal 包**；让它 import `internal/collector` 会把 sqlite driver、forwarder 整套拖进 stats 的测试二进制，且方向不干净（collector 是写入侧、stats 是只读侧）。叶子包让两边都能 import 而不产生环（design 决策 D9）
- [x] 1.2 `internal/collector/store.go`：加 `func filterLabels(labels map[string]string) map[string]string` —— 只保留白名单 key
      —— **labels 为空/全被过滤掉时返回空 map 而不是 nil**：落库要序列化成 `{}`，不能是 `null`
- [x] 1.3 `internal/collector/store.go`：在 metric 落库前调用过滤（`insertMetric`，`store.go:478` 是全仓库**唯一**的 metrics INSERT 路径，已核实无第二条）
      —— **必须落在 store 层**：parser 层剥会破坏 `parser.go:160` 的 `meta.model = mp.Labels["model"]`（model 提取到 sessions 表这一条链）。注意 `ccl.cwd` / `ccl.provider` 取自 `resAttrs`（`parser.go:165-172`），**不受影响**（design 决策 D2）
- [x] 1.4 单测：写入一个带全套 attributes（`service.name` / `os.type` / `user.id` / `ccl.cwd` / `ccl.provider` / `type` / `model`）的 metric → 断言落库行的 `labels` 只有白名单 key，且 **sessions 行的 `model` / `cwd` / `provider` 仍然正确**
- [x] 1.5 单测：attributes 里没有任何白名单 key 时，落库 `labels` 是 `{}`（不是 `null`、不是空串）
- [x] 1.6 `internal/stats` 的测试：断言 stats SQL 里用到的每个 labels key（`$.type` / `ccl.provider` / `provider`）都在 `labels.Stored` 里 —— 漏一个即红
      —— 这是白名单方案**唯一的编译期保障**：将来 stats 加 `JSON_EXTRACT(m.labels,'$.xxx')` 而白名单没同步 → 那个维度静默变空，没人拦得住

## 2. 保留期剪枝（按 captured_at，含孤儿行）

- [x] 2.1 `internal/collector/store.go`：加 `func (s *Store) PruneOlderThan(cutoff time.Time) (sessionsRemoved, metricsRemoved int64, err error)`
      —— ① `DELETE FROM metrics WHERE captured_at < ?`（**含孤儿行**：它们没有 session 关联，但 captured_at 照样能判年龄；任何"按 session 选 metrics"的谓词都碰不到它们 → 永远清不掉）
      —— ② `DELETE FROM sessions WHERE started_at < ? AND NOT EXISTS (SELECT 1 FROM metrics m WHERE m.session_id = sessions.id)`
      —— **必须用 NOT EXISTS 而不是 NOT IN**：schema 没有外键，`store.go:333` 允许 `session_id` 为空 → `NOT IN (subquery)` 遇到 NULL 会返回 UNKNOWN → 整个谓词恒 false → 什么都删不掉（design 决策 D3）
- [x] 2.2 **剪枝不按 `sessions.started_at` 判生死**：一个 91 天前开始、今天仍在产生 metric 的 session，它的**今天**的 metrics 必须保留 —— 否则它今天的 token 从 `ccl stats` 与看板消失，而 `openspec/specs/stats-query/spec.md` 要求"a session started before the window still appears"，且看板最大窗口正好是 90 天（与保留期完全重合）
- [x] 2.3 `cmd/cc-launcher/serve.go`：daemon 启动后立刻清一次（保留期 90 天，常量 `retentionDays = 90`），之后每 24 小时清一次（ticker）
      —— 清理失败**只记日志、不阻断 daemon**（与迁移同一条原则）
- [x] 2.4 单测：造一个 100 天前开始的 session + 若干 metrics、一个昨天开始的 session、一个"100 天前开始但昨天仍有 metric"的 session、一条无 session 的孤儿 metric → 断言：老的被清、近的保留、长会话的近期 metrics 与 session 行保留、孤儿行被清、**没有残留孤儿**
- [x] 2.5 单测：剪枝**不做 VACUUM**（freelist 页被后续写入复用 → 文件不再涨，但大小不缩）

## 3. 一次性迁移（schemaVersion 2 → 3）

- [x] 3.1 `internal/collector/store.go`：`schemaVersion` 2 → 3；**新增独立函数** `migrateLabelsToWhitelist(db *sql.DB) (bool, error)`，在 `OpenStore` 里**单独调用**
      —— **不要**把 v3 塞进 `migrateStore` 的错误通道：`migrateStore` 现在的契约是"失败 → OpenStore 返回 error → `serve.go:135` 报『开 db 失败』→ daemon 起不来"。照原设计实现，**一次迁移失败就永久起不来 daemon**（design 决策 D4）
- [x] 3.2 迁移失败的处理：记日志 + **不升 `user_version`** + 返回 nil（daemon 照常启动）；下次启动自动重试
      —— v1/v2 的结构迁移失败保持 fatal（那是 schema 坏了，必须让人知道）
- [x] 3.3 **备份用 `VACUUM INTO '<backups>/telemetry.db.pre-compact-<ts>'`**，不是文件复制
      —— WAL 模式下最近提交的事务还在 `-wal` 里，复制主文件会**静默丢数据**（实测 5001 行 → 只剩 5000 行，无任何报错）。触发路径真实存在：`stop_unix.go:32-45` 的 SIGKILL 兜底会跳过 `store.go:606` 的 checkpoint，panic / OOM / 断电同理
      —— 落点必须在 **OpenStore 层**（那里有 `path`）：`migrateStore(db)` 只有连接句柄，拿不到路径
- [x] 3.4 **失败重试不重复备份**：迁移失败不升版本 → 下次会重试 → 不能每次都写一份 29 MB 备份（否则磁盘被撑爆）。做法：备份前检查目标文件是否已存在（或用独立的标记位）
- [x] 3.5 迁移用**单条** `UPDATE metrics SET labels = json_object(...)`，key 列表由 `labels.Stored` 拼出（仍是单一真相源）
      —— 逐行 UPDATE 在 10 万行上是秒级（实测 5 万行：读 122ms + 更新 813ms），且发生在监听端口之前 → 遥测推送全丢、还会撞 `probe.go:40` 的 3s 就绪预算。单条 UPDATE 是亚秒级
- [x] 3.6 **注意 `SetMaxOpenConns(1)` 下的死锁陷阱**：`db.Query` 持着 rows 再 `db.Exec` 会**永久死锁**（实测 3s 无返回）。若要逐行处理，必须"先物化成切片 → `rows.Close()` → 再开事务 UPDATE"。仓库已有正确写法可参照：`store.go:544-562` 的 `backfillCwdDB`
- [x] 3.7 单测：造一个老格式 db（`user_version = 2`，labels 带冗余 key）→ 打开后断言 labels 被重写、`user_version` 变 3、**备份存在且行数与原库一致**；再打开一次断言**不重复备份、不重复改写**（幂等）
- [x] 3.8 单测：迁移失败（如某行 labels 是畸形 JSON）→ 断言 daemon 侧不因它起不来、`user_version` **不**升到 3

## 4. `ccl serve --compact`

- [x] 4.1 `cmd/cc-launcher/serve.go`：`registerServeFlags` 加 `--compact`（返回第 6 个指针）
- [x] 4.2 互斥组：加进 `flags := []*bool{...}` 数组，用法提示与错误信息同步加上 `--compact`
- [x] 4.3 `cmd/cc-launcher/completion.go`：**`serveMutex` map 是硬编码的**（`completion.go:217-223`），**不随 `registerServeFlags` 自动跟随** → 必须手动加 `"--compact": true`。候选列表本身是自动跟随的（`completion.go:228-232`），但互斥防御不是 —— 漏改会让 spec 里"after --compact 输出为空"那条 scenario 必然失败
- [x] 4.4 `cmd/cc-launcher/serve.go`：加 `runCompactInner()` —— 流程：① daemon 在跑就停 → ② `VACUUM` → ③ **`PRAGMA wal_checkpoint(TRUNCATE)`** → ④ daemon **原本在跑**才重新拉起（复用 `forkDaemon` + `waitForDaemonReady`）
      —— **第 ③ 步不能省**：WAL 模式下 VACUUM 把新内容写进 `-wal`，主文件要等 checkpoint 才被覆盖。实测 db 2.4MB → VACUUM 后主文件仍是 2437120 字节（`-wal` 涨到 4.9MB）→ checkpoint 后才降到 1224704。省掉它，`ls -l` 看到"没变小"，功能等于空操作（design 决策 D6）
      —— **daemon 本来没在跑就不要拉起**（保持原状态）
      —— VACUUM 只读连接不阻塞（实测），真正要排除的是并发**写** → 停 daemon 是对的
- [x] 4.5 `cmd/cc-launcher/serve.go`：停止 daemon 的死线**放宽到 15s** —— `stop_unix.go` 现在是 5s，短于 daemon 自己的关闭预算（`server.go:149` 的 5s + `serve.go:242` 的 5s = 最多 10s）→ SIGKILL 兜底可达，而 SIGKILL 会**打断 `store.go:606` 的 checkpoint**，留下残缺 WAL
- [x] 4.6 `cmd/cc-launcher/serve.go`：开始前检查剩余磁盘空间 ≥ 2× db 尺寸（VACUUM 需要约 2 倍临时空间；29 MB 的库峰值约 90 MB：db + wal + 备份），不够就报错退出，别让 VACUUM 中途失败留下半成品
- [x] 4.7 `cmd/cc-launcher/serve.go`：`serveHelp` 文案加 `--compact` 的一行说明
- [x] 4.8 单测：互斥检查（`--compact --backfill` 同时给 → 报错）；`--compact` 在 daemon 没跑时能执行且不报错

## 5. 同步既有代码与文档（评审发现的漏改点）

- [x] 5.1 `README.md:570` 的 `ccl serve` 命令清单要补 `--compact`
- [x] 5.2 `internal/collector/strip.go:44-45` 的注释"本地存的 labels **保留** ccl.*"改完后变成半真（`ccl.cwd` 不再保留）→ 同步改写
- [x] 5.3 `internal/collector/store.go:492,503` 的注释还在说 `ccl web --backfill`（旧命令名，早就挪到 `ccl serve --backfill`）
- [x] 5.4 `openspec/specs/session-context-attrs/spec.md:157`"the local record MUST keep `ccl.*`" → 澄清指 session 行（metric 行只保留白名单）
- [x] 5.5 `openspec/specs/otel-receiver/spec.md:51`"labels (resource + scope + data-point attributes)" → 澄清落库前由 store 层按白名单裁剪
- [ ] 5.6 CHANGELOG（release 时补）：必须写明 `ccl stats --json` 的 `extra_labels` 内容变窄

## 6. 验证

> **真机验证必须对副本做**，不能直接动 `~/.cc-launcher/telemetry.db`——那是活数据（daemon 每 60 秒推一次 OTel，自己的 session 也在写），且迁移是一次性的（`user_version` 升到 3 后不再触发，"迁移前后"无法再造）。

- [x] 6.1 `go build ./... && go vet ./... && go test ./...` 全绿；`gofmt -l .` 为空（CI 已加 gofmt 检查）
- [x] 6.2 `openspec validate --all` 通过
- [x] 6.3 **对 fixture 验证迁移**：造一份 fixture db（`user_version = 2` + 已知 labels 的行）→ 跑迁移 → 断言：行数不变 / 每行 labels 只剩白名单 / `user_version` = 3 / 备份存在且**行数与原库一致**（验证备份不是 WAL 残缺快照）/ 二次运行不重复备份
- [x] 6.4 **对 fixture 验证 compact**：跑 `--compact` → 断言**文件真的变小**（不是只看 VACUUM 返回值）、且 `sessions` / `metrics` 行数与 `sum(value)` 前后相等
- [x] 6.5 **对同一 fixture 在改动前后各跑一次 `ccl stats --json`** 做 diff（固定 window），确认只有 `extra_labels` 变窄、token/cost 与各维度不变
- [x] 6.6 真机只留一条 sanity check：`ccl serve --status` 看 db 大小与 session 数正常、`ccl stats` 输出正常；**并把回滚命令写进文档**（停 daemon → 用备份替换 `telemetry.db` → **删掉 `telemetry.db-wal` 与 `telemetry.db-shm`** → 重开。旧 WAL 会覆盖回滚内容，漏删等于没回滚）
