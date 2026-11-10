## Context

`~/.cc-launcher/telemetry.db` 是 ccl 唯一的遥测存储。两张表：`sessions`（每个 Claude Code 会话一行）与 `metrics`（每个 metric datapoint 一行）。当前体积（本机 9.4 MB 主文件 + 4.1 MB WAL）：

| dbstat 对象 | 大小 | 占比 |
|---|---|---|
| `metrics` 表 | 8.32 MB | **88%** |
| `idx_metrics_session_time` | 1.00 MB | 11% |
| `sessions` 表（105 行） | 0.02 MB | 0.2% |
| freelist（空闲页） | **0** | — |

16983 行 metrics，每行文本字段平均 433 字节，其中 `labels` JSON 占 ~380。抽一行看内容：

```json
{"effort":"high","host.arch":"amd64","model":"MiniMax-M3[1m]","os.type":"linux",
 "os.version":"6.8.0-138-generic","query_source":"main","service.name":"claude-code",
 "service.version":"2.1.277","terminal.type":"ssh-session","type":"input",
 "user.id":"cd2f3d5514fba56a74cf1cf6fe2f4c80c4624911ed07525218c570823042dcd9"}
```

同一 session 的 162 行里，上面 12 个 key 里有 11 个**逐字相同**（`type` 是唯一每行不同的）。成因在 `internal/collector/parser.go`：`resAttrs + scopeAttrs + dpAttrs` 三层全量合并进 `labels`——resource attributes 是**主机级/会话级常量**，本该只存一份，却被复制到了每一行。

**消费方盘点**（全仓库核对，见 `internal/stats/query.go`、`internal/stats/bucket.go`、`internal/web/server.go`）：

| 读 `metrics.labels` 的地方 | 用到哪些 key |
|---|---|
| `internal/stats/query.go` / `bucket.go` 的 8 处 SQL | `$.type` |
| `providerName()` 兜底（`query.go:351-363`） | `ccl.provider`，再兜底 `provider` |
| `ccl stats --json` 的 `extra_labels`（`query.go:75` + `:189-191` → `statscmd.go:171`） | **整段 labels JSON** |
| `internal/web/server.go` 的 `apiResponse` | **不读**（`extra_labels` 明确不透传） |

`model` 与 `ccl.cwd` 另有一份：它们被 parser 提取进 `sessions.model` / `sessions.cwd` 两列，stats 的看板维度（project / model / provider）全部取自 `sessions` 表，**不读 metrics.labels**。

三个既有问题叠加：

1. **每行重复存主机级常量**（上文）。
2. **没有任何保留策略**——`sessions` 只增不减；且 `store.go:333` 允许 `session_id` 为空的"孤儿 metric"落库，这类行在任何"按 session 选 metrics"的清理里都碰不到 → **永远不会被清理**。
3. **`freelist = 0`** —— 从来没删过数据，所以 VACUUM 现在一分钱都省不出来。

SQLite 行为（本机用仓库同版本驱动 modernc.org/sqlite v1.59.0 实测）：

| 现象 | 实测结论 |
|---|---|
| WAL 模式下复制 db 主文件当备份 | **静默丢数据**：5001 行只复制到 5000 行（缺的在 `-wal` 里）。`VACUUM INTO` 则完整（5001 行） |
| WAL 模式下 `VACUUM` | 主文件**不变**（2437120 字节），`-wal` 反而涨到 4906952；**必须再 `PRAGMA wal_checkpoint(TRUNCATE)`** 才降到 1224704 |
| 删掉 90% 行后 | 主文件不缩（4194304 不变），但 freelist 被后续写入**复用** → 文件不再涨。freelist 页无类型，表页与索引页可互换 |
| 只读连接 / 活跃读事务 | **不阻塞** VACUUM；VACUUM 真正要排除的是并发**写** |

## Goals / Non-Goals

**Goals:**

- 新写入的 metric 行，`labels` 只保留 `type` / `model` / `ccl.provider` / `provider`。
- 存量 db 在 daemon 启动时被自动重写为白名单形式（幂等、可重试、失败不致命）。
- 超过保留期的**数据**被删除（含孤儿 metric 行）；删除产生的空闲页被后续写入复用，**文件不再持续增长**。
- 用户有一个显式命令把文件**真正缩小**。
- 迁移失败**不得**阻止 daemon 启动——db 保持旧格式，功能照常。

**Non-Goals:**

- 不做"按 session 归一化"（把 resource attributes 拆到 `sessions` 表）。收益（再省 ~30%）远小于改 schema 与查询层的风险。
- 不压缩 `labels`（zlib blob）——会废掉 `JSON_EXTRACT`。
- 不改 `sessions` 表结构、不加索引、不调 `page_size`。
- 不动 `internal/stats` 的查询逻辑。
- 不保证删除后的文件立刻变小（那是 `--compact` 的职责）。

## Decisions

### D1: 白名单而非黑名单

**决策**：`labels` 只保留 `type`、`model`、`ccl.provider`、`provider` 四个 key，其余一律丢弃。

**理由**：白名单对**未来**同样成立——Claude Code 以后加新的 resource attribute，黑名单会让它照旧撑大每一行。仓库里已有同款思路：`internal/web/server.go` 的 `apiResponse` 就是用白名单挡掉 `extra_labels`。

**代价（明说）**：**有损**。丢掉 `os.type` / `host.arch` / `terminal.type` 之后，将来想按这些维度统计就做不了了（用户已明确接受）。这些维度从没被任何代码读过；`user.id` 反而是该丢的（`apiResponse` 的注释早就把它标为"泄露主机指纹"）。

**为什么保留 `model`**：每行约 15 字节（相对 380 字节微不足道），但保留"在 metrics 级按 model 查"的能力——`sessions.model` 只有 session 粒度，一个 session 换过 model 就查不出来。

**为什么保留 `ccl.provider` 与 `provider`**：`providerName()` 的两级兜底都读它们。非 ccl 启动的会话 `sessions.provider` 为空，兜底就靠 `ccl.provider`。

**为什么丢掉 `ccl.cwd`**：完整路径字符串（几十到上百字节），是剩下最肥的一项；而 `sessions.cwd` 已有它、看板也只读 `sessions.cwd`。

### D2: 剥离发生在 store 层，不在 parser 层

**决策**：在 `internal/collector/store.go` 写库前过滤 `labels`，`parser.go` 一行不改。

**理由**：`parser.go:160` 的 `meta.model = mp.Labels["model"]` 依赖**合并后**的 labels——在 parser 层剥离会破坏 model 提取到 `sessions` 表这一条链。而 `meta.provider` / `meta.cwd` / `meta.endpoint`（`parser.go:165-172`）取自 `resAttrs`，**不受影响**。也就是说，parser 层剥离会破坏的是 **model 一条**（不是 `ccl.cwd`——这一点在评审中被纠正过，原稿写错了）。

在 store 层剥离则 parser 完全不知情，落库的只剩白名单。

**已核实**：`metrics` 表在全仓库非测试代码里只有**一条** INSERT（`store.go:478`，`WriteMetric → enqueue → worker → writeOne → insertMetric`），没有批量写 / Upsert / 第三条路径 → store 层过滤覆盖全部写入。

### D3: 保留期按"数据产生时刻"剪枝，不按"会话开始时刻"

**决策**：
- 删 `metrics WHERE captured_at < cutoff`（**含孤儿行**——它们没有 session 关联，但 `captured_at` 照样能判年龄）。
- 删 `sessions WHERE started_at < cutoff AND NOT EXISTS (SELECT 1 FROM metrics WHERE metrics.session_id = sessions.id)`。
- **不**在剪枝后 VACUUM。

**理由（这是评审抓到的真 bug）**：原设计按 `sessions.started_at` 判生死，会把"91 天前开始、但今天仍在产生 metric"的 session 连同它**今天**的 metrics 一起删掉 → 它今天的 token 从 `ccl stats` 和看板消失。而 `openspec/specs/stats-query/spec.md` 明确要求"a session started before the window still appears"（窗口按**消费时刻**算，跨窗口开始的 session 仍要出现）。更巧的是看板最大窗口正好是 **90 天**（`web-usage-stats/spec.md`），与保留期完全重合 → 90 天视图的最老一天会随剪枝一起消失。

改成按 `captured_at` 判：既满足 stats-query 的口径，又顺带清掉孤儿行（它们永远匹配不上 `session_id IN (...)` 谓词）。

**为什么不剪枝后 VACUUM**：删除产生的空闲页会被后续写入**复用** → 文件**不再涨**（实测成立）。而 VACUUM 需要独占访问、且在 WAL 模式下还必须配 checkpoint 才能真正缩小文件（见 D5）——那是 `--compact` 的职责。

**已知取舍**：`sessions` 行被清理后，同一 session id 再次推送时会重新 INSERT，`started_at` 变成当时的 `captured_at`（`store.go:455` 的 MIN 只在行已存在时生效）→ 该 session 的起始时间被重置。这个行为在 90 天后的 session 上才会发生，可接受，写进 spec。

### D4: 迁移独立成函数，失败只记日志

**决策**：新增独立函数 `migrateLabelsToWhitelist(db *sql.DB) (applied bool, err error)`，在 `OpenStore` 里**单独调用**（不进 `migrateStore` 的错误通道）。失败时：记日志、**不升 `user_version`**、返回 nil（daemon 照常启动）。下次启动自动重试。

**理由（评审纠正）**：`migrateStore` 现在的契约是"失败 = `OpenStore` 返回 error → `serve.go:135` 报『开 db 失败』→ daemon 起不来"。把 v3 塞进它的错误通道，会让**一次迁移失败就永久起不来 daemon**——正是这个 change 承诺要避免的。v1/v2 的结构迁移失败保持 fatal（那是 schema 坏了，必须让人知道）。

**幂等**：由 `user_version` 保证——`user_version >= 3` 直接跳过。`user_version` 的写入在 `migrateStore` 里（事务外单独提交），迁移函数自己管升版本。

**实现约束（评审抓到的死锁陷阱）**：本库 `SetMaxOpenConns(1)`（`store.go:134`），**`db.Query` 持着 rows 再 `db.Exec` 会永久死锁**（实测 3s 无返回）。所以必须"先把要改的行**物化**成切片 → `rows.Close()` → 再开事务 UPDATE"。仓库已有正确写法可参照：`store.go:544-562` 的 `backfillCwdDB`（先物化 id 列表、关 rows、再 Exec）。

**性能**：逐行 UPDATE 在 10 万行上是秒级（实测 5 万行：读 122ms + 更新 813ms），且发生在监听端口之前 → 这段时间 Claude Code 的遥测推送全丢。改用**单条** `UPDATE metrics SET labels = json_object(...)`，key 列表由白名单常量拼出（仍是单一真相源），10 万行也是亚秒级。

### D5: 备份用 `VACUUM INTO`，不是文件复制

**决策**：迁移前用 `VACUUM INTO '<backups>/telemetry.db.pre-compact-<ts>'` 造一份**事务一致**的快照（实测该驱动支持，且产出完整 5001 行）。

**理由（三个评审一致 + 实测）**：WAL 模式下 db 常驻 WAL（`store.go:136`），最近提交的事务还在 `-wal` 里。直接复制主文件会**静默丢数据**（实测 5001 行 → 只有 5000 行），且**没有任何报错**。触发路径真实存在：`stop_unix.go:32-45` 的 SIGKILL 兜底会跳过 `store.go:606` 的 checkpoint，panic / OOM / 断电同理。

**落点**：`migrateStore(db)` 只有连接句柄，拿不到路径 → 备份必须在 **OpenStore 层**做（那里有 `path`），或把路径传进迁移函数。

**失败重试不重复备份**：迁移失败时不升 `user_version` → 下次启动会重试 → **不能每次都写一份 29 MB 备份**（否则磁盘被撑爆）。做法：备份文件名带时间戳，但**只在"还没有备份过"时写**（用一个 `user_version` 之外的标记，或检查目标文件是否已存在）。

**回滚步骤**（必须写全，否则回滚等于没回滚）：停 daemon → 用备份替换 `telemetry.db` → **删掉残留的 `telemetry.db-wal` 与 `telemetry.db-shm`** → 重开。旧 WAL 会被恢复流程应用到刚拷回来的旧主文件上，把回滚内容覆盖掉。

### D6: `VACUUM` 之后必须 `wal_checkpoint(TRUNCATE)`，否则 `--compact` 是空操作

**决策**：`ccl serve --compact` 的执行体固定为：停 daemon（若在跑）→ `VACUUM` → `PRAGMA wal_checkpoint(TRUNCATE)` → 打印压缩前后大小。

**理由（实测，A/C 均未发现）**：WAL 模式下 `VACUUM` 把新内容写进 `-wal`，**主文件要等下一次 checkpoint 才被覆盖**。实测 db 2.4MB → VACUUM 后主文件仍是 2437120 字节（`-wal` 涨到 4.9MB）→ 用户 `ls -l` 看到"没变小" → 功能等于空操作；再 `wal_checkpoint(TRUNCATE)` 后主文件才降到 1224704。

**磁盘峰值**：VACUUM 需要约 2× db 空间的临时空间。29 MB 的库 → 峰值约 90 MB（db + wal + 备份）。开始前检查剩余磁盘空间，不够就报错退出（而不是让 VACUUM 中途失败留下半成品）。

### D7: `--compact` 进 serve 的互斥组，且要按"原状态"恢复

**决策**：`ccl serve --compact` 进 serve 的互斥组（第 6 个成员，与 `--backfill` 同族）。执行流程：① daemon 在跑就停 → ② `VACUUM` → ③ `wal_checkpoint(TRUNCATE)` → ④ daemon **原本在跑**才重新拉起。

**理由**：
- **为什么进 serve 的互斥组**：它与 `--backfill` 是同一类东西——"对 daemon 数据的一次性维护动作"。`--backfill` 已经在组里（web-backfill-to-serve §D1 的先例）。
- **为什么停 daemon**：VACUUM 要排除并发**写**（只读连接不阻塞它，实测）。daemon 是唯一的写者 → 停掉它。
- **为什么按原状态恢复**：daemon 停了用户的 Claude Code 就推不上遥测。执行完自动拉起，用户无感；daemon 本来就没在跑则不拉起。
- **停止的死线要放宽**：`stop_unix.go` 的 5s 死线短于 daemon 自己的关闭预算（`server.go:149` 的 5s + `serve.go:242` 的 5s = 最多 10s）→ SIGKILL 兜底可达，而 SIGKILL 会**打断 `store.go:606` 的 checkpoint**，留下残缺 WAL。`--compact` 走停止流程时把死线放宽到 15s。
- **不需要额外备份**：VACUUM 是**重写文件**而非删除数据，中途失败 SQLite 保证原文件完好（原子性），不像"重写 labels"那种有损操作。

### D8: `ccl stats --json` 的 `extra_labels` 会变（对外可见的行为变更）

**决策**：不改 `internal/stats` 的代码（labels 少了它自然就少了），但**必须**在 spec、proposal 与 CHANGELOG 里写明：`ccl stats --json` 的 `extra_labels` 从"整段 labels"缩到"只剩白名单 key"。

**理由**：`query.go:189-191` 把整段 labels JSON unmarshal 进 `ExtraLabels`，该字段带 `json:"extra_labels,omitempty"` 直接进 `ccl stats --json` 的输出（`statscmd.go:171` 的 `MarshalIndent`）。web 侧确实不读（`apiResponse` 白名单挡掉了），**CLI 侧读**——这是最初盘点时漏掉的消费方。

**性质**：这是**有损的输出变更**（不是 bug 修复）。token / cost / 各维度的**口径不变**（那些取自 `sessions` 表与 `labels.type`），只有 `extra_labels` 这个"额外附带"字段的内容变窄。

### D9: 白名单常量放叶子包，避免 `internal/stats` 反向依赖 `internal/collector`

**决策**：白名单常量放到一个叶子包（如 `internal/telemetry/labels`），`internal/collector` 与 `internal/stats` 各自 import。

**理由**：设计里原本写"在 `internal/stats` 的测试里断言 stats 用到的 labels key ⊆ 白名单"——这是白名单方案**唯一的编译期保障**（将来 stats 加 `JSON_EXTRACT(m.labels,'$.xxx')` 而白名单没同步 → 那个维度静默变空，没人拦得住）。但 `internal/stats` 目前**不 import 任何 internal 包**；让它 import `internal/collector` 会把 sqlite driver、forwarder 整套拖进 stats 的测试二进制，且方向上不干净（collector 是写入侧、stats 是只读侧）。叶子包让两边都能 import 而不产生环。

## Risks / Trade-offs

- **[丢掉 `os.type` / `host.arch` / `terminal.type` 等维度后无法回溯统计]** → 用户已明确接受。备份里保留着迁移前的完整数据，理论上可回滚（但回滚要手工操作，见 D5）。

- **[`ccl stats --json` 的 `extra_labels` 变窄]** → D8。这是对外可见的输出变更，必须进 CHANGELOG。

- **[迁移要重写全部历史行，daemon 启动会变慢]** → 改用单条 UPDATE 后是亚秒级。但迁移发生在**监听端口之前** → 这段时间 Claude Code 的遥测推送全丢；更糟的是 `probe.go:40` 的"3s 内未就绪"预算 → 用户会看到"3s 内未就绪"，且此窗口内第二次 `ccl` 会因端口未 bind 而**再 fork 一个 daemon**（`probe.go:97`）→ 两个 daemon 同时跑迁移 → 一个 SQLITE_BUSY 退出。**缓解**：迁移前先 bind 端口（把 `serve.go:119` 的探端口提前到开 db 之前），或把迁移放到监听之后的后台 goroutine（但那会占死唯一写连接 → 见下条）。

- **[迁移/剪枝占住唯一写连接时队列溢出会静默丢指标]** → `store.go:134` 是 `SetMaxOpenConns(1)`，`store.go:87` 的队列 10000 满了**丢最旧、不报错**，而 HTTP handler 只入队就立刻回 200（OTel SDK 认为投递成功）→ 丢的指标永久丢失且客户端无感。**缓解**：迁移留在 OpenStore 的**同步**路径（worker 那时还没起，`store.go:163-164`）→ 天然无并发；**不要**挪到后台 goroutine（那会让 UPDATE 事务占住那条唯一连接 → worker 的 INSERT 全部排队 → 队列爆）。

- **[保留期删除是不可逆的数据删除]** → 90 天是用户自己选的。删除前打印删掉了多少行。

- **[白名单与 stats 层的依赖关系没有编译期保障]** → D9 的叶子包 + 断言测试来兜。

- **[`sessions` 行被清理后重建导致 `started_at` 重置]** → D3 的已知取舍，写进 spec。

## Migration Plan

1. 用户升级到新版本 → 下次 daemon 启动时：检测到 `user_version == 2` → 用 `VACUUM INTO` 造一份**完整**快照到 backups → 单条 UPDATE 重写存量 labels → 升 `user_version = 3`。任一步失败 → 记日志、**不升版本**、daemon 照常启动、下次重试。
2. 之后所有新写入的 metric 行都只有白名单 key。
3. daemon 启动时清一次 90 天前的**数据**，之后每 24 小时清一次。
4. 用户想要**文件变小** → 跑 `ccl serve --compact`（停 daemon → VACUUM → checkpoint → 按原状态拉起）。

**回滚**：停 daemon → 用备份替换 `~/.cc-launcher/telemetry.db` → **删掉 `telemetry.db-wal` 与 `telemetry.db-shm`** → 重开。

## Open Questions

（无 —— 关键取舍已在 proposal 与本 design 中定稿；本 design 已按三路独立评审的结论修订。）
