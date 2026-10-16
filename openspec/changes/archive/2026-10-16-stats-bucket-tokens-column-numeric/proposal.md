## Why

`ccl stats --day N` 桶聚合表（`spec/time-buckets`）现已在最右一列用 20 字符 Unicode 柱（`▏▎▍▌▋▊▉█`）展示每桶的总 token 数。该柱复用早期 POSIX `df -h` 类的"密度条"惯例，但终端宽度紧时（`COLUMNS=80` spec 是 hard cap）柱与右侧 ⏳/· 后缀会挤占 BUCKET/CACHE 列空间；并且桶之间比例靠肉眼对齐，肉眼估读有偏。用户反馈柱不如直接数字一目了然——本次把 `TOTAL_TOKENS` 列从柱状图改为人类可读数字合计，砍掉归一化 + half-block 字符集这套视觉编码，转为人话可直接 compare 的数字。

## What Changes

- `internal/stats/format.go::FormatBucketTable`：删掉 `bucketBar()` 调用；该列改为 `humanizeTokens(t)`，其中 `t = InputTokens+OutputTokens+CacheCreate+CacheRead`。TOTAL 行也按 humanize 总和展示，不再用 `──────────────────── (total)` 横线。
- `internal/stats/format.go`：删除 `bucketBar()` 函数本身（不再被任何调用点引用）。
- `internal/stats/bucket_test.go`：删掉 bar 长度精度 / partial / zero-token 占位 / TOTAL 行横线 4 类测试断言；改写为新列数字断言（humanize 输出字符串）。
- `cmd/cc-launcher/smoke_test.go`：桶表测试里所有 `TOTAL_TOKENS` / `⏳` / `·` / 横线相关断言替换为新列数字断言；80 列宽度对齐测试可以继续保留（不受影响），但 bar alignment 相关单独 case 删除。
- `README.md`（第 315 行附近）：删掉"柱长按总 token 数 8 级半块归一化"的描述。
- spec 同步改：`spec/time-buckets` Requirement 1 的 `TOTAL_TOKENS(柱)` 文字、`─` separator 那一段、Scenario "TOTAL_TOKENS bar length is proportional"、"bucket table renders within 80-column"、"TOTAL row shows humanized aggregates with a horizontal-rule separator" 全部改写为"每行数值化合计 + TOTAL 行数字总和"语义。其它 Requirement（JSON shape 不变、bucket label / 边界 / `--day` 校验）一字不动。
- 不动 `internal/stats/bucket.go::QueryBucketSummary`（SQL / 数据层不变）。
- 不动 `internal/stats/query.go`（detail 表 `FormatTable` 已无柱，`FormatTable` 不受影响）。
- 不动 `cmd/cc-launcher/statscmd.go` 的 `--day` flag 校验 / JSON 输出 shape（`bucket_rows[].input_tokens` 等仍是原始 int64 字段）。
- 不动 `--json` 输出（已全是原始 int64 + 4 位 cost，与显示形态无关）。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `time-buckets`：Requirement 1（桶表 schema + TOTAL_TOKENS 列渲染方式）+ Requirement 4（humanize 规则的"TOTAL_TOKENS bar visualization"那一段）改文字；Scenario "TOTAL_TOKENS bar length is proportional to total tokens"、"bucket table renders within 80-column terminal width without column overflow"、"TOTAL row shows humanized aggregates with a horizontal-rule separator"、"--day N tolerates empty db gracefully" 中涉及柱/·/⏳/─ 的子句改数字化合计语义；Requirement 2 / 3 / 5 + 其它 Scenario 一字不动。

## Impact

- `internal/stats/format.go` — `FormatBucketTable` 函数体重写（删 bucketBar 调用 + 删函数本身）
- `internal/stats/bucket_test.go` — 重写约 9 个测试 case（bucketBar 相关 + FormatBucketTable_TotalSeparator）
- `cmd/cc-launcher/smoke_test.go` — 重写 4-6 个桶表断言 case（桶文本对齐 / 80 列宽度 / TOTAL 行）
- `README.md` — 第 315 行附近一句修改
- `openspec/specs/time-buckets/spec.md` — 主体 spec 改 MODIFIED Requirements（archive 时合并到 base spec）
- `openspec/changes/stats-bucket-tokens-column-numeric/specs/time-buckets/spec.md` — delta spec（MODIFIED Requirements 整段重写）
- 无 schema 变化、无 CLI 标志变化、`--json` 字节级兼容、用户/管道消费 `ccl stats` 文本输出的脚本会因为 `TOTAL_TOKENS` 列从柱→数字而不再匹配旧正则（**breaking for text consumers**, but text 本就是 human-readable、不可当作 API）
- 不破坏 `telemetry-storage`、`stats-query`、`launcher-provider-env` 等已有 contract
