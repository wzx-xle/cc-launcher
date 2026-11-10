## Why

`telemetry.db` 只增不减，已经长到没法看：Windows 主环境 **29 MB**，本机 9 MB + 4 MB WAL。

实测下来，体积**不是**"数据多"造成的，而是**每行重复存同一份主机级常量**：

- `internal/collector/parser.go` 把 `resAttrs + scopeAttrs + dpAttrs` **全量合并**进每一行 metric 的 `labels` JSON。于是每行都重复存一遍 `service.name` / `service.version` / `host.arch` / `os.type` / `os.version` / `terminal.type` / `query_source` / `effort` / `user.id`（**64 字节主机指纹**），真正每行不同的只有 `type`（input/output/cacheRead…）一两个。
- 实测每行文本字段平均 **433 字节**，其中 labels 占 ~380；一个 session 平均 162 行 metrics，每行重复同一份常量。
- `dbstat` 的账：`metrics` 表独占 **88%**（8.32 MB / 9.4 MB），索引 11%，而 `sessions` 表（105 行）只有 0.02 MB。
- **`freelist = 0`** —— 没有任何空闲页，说明这 29 MB 是实打实写进去的重复数据，**VACUUM 现在一分空间都省不出来**。

而真正读 `labels` 的只有两处：`JSON_EXTRACT(m.labels,'$.type')`（token 分类）和 `providerName()` 兜底读 `ccl.provider`。其余 key **从没被任何代码读过**；`user.id` / `host.arch` / `os.version` 甚至被 `internal/web/server.go` 的注释明确标为"直接 marshal 到局域网可访问的 API 等于泄露主机指纹"。

叠加第二个问题：**没有任何保留策略**，db 里的 session 只增不减，90 天前的数据照样留着。

## What Changes

- **写入时白名单剥离**：metric 行的 `labels` 只保留 `type` / `model` / `ccl.provider` / `provider` 四个 key，其余（`ccl.cwd`、`user.id`、`service.*`、`host.*`、`os.*`、`terminal.type`、`query_source`、`effort` …）一律不落库。落在 **store 层**（写库前最后一道关口），parser 提取 `model` / `ccl.cwd` 到 `sessions` 表的逻辑完全不受影响。
  - **有损**：那些维度以后没法统计。但它们从没被读过，且其中主机指纹类本就不该留存。
- **90 天保留策略**：删除 90 天前的**数据**——按 `captured_at` 判（不是按 session 开始时刻，否则"91 天前开始但今天仍在跑"的 session 今天的 token 会从看板消失），含永远清不掉的孤儿 metric 行。删除产生的空闲页被后续写入复用 → **文件不再涨**（但不自动缩小，见下）。
- **一次性迁移**：`schemaVersion` 2 → 3，daemon 启动时把存量行的 `labels` 重写成白名单形式。迁移只动内容、不动文件大小。迁移前用 `VACUUM INTO` 造一份**含 WAL 数据**的一致快照（直接复制 db 文件在 WAL 模式下会静默丢数据，实测 5001 行只剩 5000 行）。
- **`ccl serve --compact`**：新增子命令 flag（进 serve 的互斥组），停掉 daemon → `VACUUM` → **`PRAGMA wal_checkpoint(TRUNCATE)`** → 重新拉起 daemon。**最后那步 checkpoint 不能省**：WAL 模式下 VACUUM 把新内容写进 `-wal`，主文件要等 checkpoint 才被覆盖——省掉它，用户 `ls -l` 会看到"没变小"，功能等于空操作（实测：VACUUM 后主文件仍是 2437120 字节，checkpoint 后才降到 1224704）。

非破坏性：db 的 schema、列、索引全不变；token / cost / 各维度（project / model / provider）的**口径不变**（它们取自 `sessions` 表与 `labels.type`）。

**但有一处对外可见的输出变更**：`ccl stats --json` 的 `extra_labels` 字段会从"整段 labels JSON"缩到"只剩白名单 key"（`internal/stats/query.go:189-191` 把整段 labels 填进该字段）。web 侧不受影响（`apiResponse` 的白名单本来就不透传 `extra_labels`）。这必须进 CHANGELOG。

## Capabilities

### New Capabilities

（无 —— 本 change 不引入新能力，只修改既有能力的对外表现与存储行为。）

### Modified Capabilities

- `telemetry-storage`: 增加两条要求 —— ① metric 行的 `labels` 只保留白名单 key；② 超过保留期的**数据**（按产生时刻判，含孤儿行）会被删除，且剪枝本身不做 VACUUM。
- `daemon-lifecycle`: 增加两条要求 —— ① daemon 启动时执行一次性数据迁移（存量 labels 重写为白名单形式，失败不致命）；② `ccl serve --compact` 收缩 telemetry.db。
- `cli-help`: `ccl serve --help` 必须列出 `--compact`；serve 的互斥 flag 组从 5 个变成 6 个。
- `cli-shell-completion`: `ccl serve` 的补全候选增加 `--compact`；互斥组同步为 6 个。
- `session-context-attrs`: 澄清"本地记录保留 `ccl.*`"指的是 **session 行**，metric 行只保留白名单 key。
- `otel-receiver`: 澄清落库前 labels 由 store 层按白名单裁剪（parser 仍解析全量 attributes）。

## Impact

- **改动**：`internal/collector/store.go`（labels 白名单过滤、`PruneOlderThan`、`migrateStore` 的 v3 分支）、`cmd/cc-launcher/serve.go`（`--compact` flag + 互斥组 + 执行体 + help 文案）、`internal/stats/*`（无需改：只读打开，不触发迁移）。
- **新增**：`internal/collector` 的保留期清理与迁移函数，及对应单测。
- **数据影响**：存量 db 会在**下次 daemon 启动时**被重写 labels（迁移前自动备份一份）；`VACUUM` 收缩由用户显式跑 `ccl serve --compact` 触发。
- **依赖**：无新增。
