## Why

`ccl stats --day N` 当前同时输出**两段表格**：
1. **detail 表**（`FormatTable`）—— 按 `StartedAt` 倒序，每个 session 一行
2. **桶聚合表**（`FormatBucketTable`）—— N 个 hourly/daily 桶 + TOTAL

base spec `time-buckets/spec.md` Requirement 1 写"after the existing session detail table, print a second human-readable table"——v0.12.0 加 `--day` 时把 detail 留下来了。但这是 UX bug：用户传 `--day` 的语义就是"看 hourly/daily token 趋势"（具体 session 明细请用 `ccl stats` 不传 `--day`），50 session + 30 桶 = 80 行表格，桶表被 detail 顶到屏幕底部看不见。本 change 砍掉 `--day` 路径的 detail 表，只保留桶视图；`--day` 不传时维持旧 detail-only 行为不变。`--json` 输出 shape 不动（`sessions` / `totals` / `bucket_rows` / `bucket_totals` 全保留，pipe 到 jq 时 caller 自选）。

## What Changes

- `cmd/cc-launcher/statscmd.go::runStatsWithQuery` `--day != -1` 分支：删除 `FormatTable(os.Stdout, result)` 调用 + 删除 `Window since: ... Group by: ...` 头 + 删除空行；detail 空时仍打 `(no data in window since ...)` hint；之后只打桶聚合表
- `openspec/specs/time-buckets/spec.md`：MODIFIED Requirement 1 文字，去掉"after the existing session detail table"那段，改为"`--day N` MUST print a time-bucket aggregate table (and ONLY the bucket table, NOT a preceding session detail table)"；对应 Scenario 同步
- `cmd/cc-launcher/smoke_test.go`：sub-test `a_day7_text_has_7_buckets_and_total` 改断言：输出**不**含 detail 表头 `STARTED`、不含 `Window since: ...` 头；`b_day1_text_has_hourly_with_partial_note` 同样；`c_no_day_text_has_no_bucket_table` 维持不变（`--day` 缺省时仍走 detail，detail 头 `STARTED` 应出现）
- 不动 `internal/stats/format.go`（`FormatTable` 函数保留，旧 detail-only 路径仍用）
- 不动 `internal/stats/bucket.go` / `query.go`（数据层不变）
- 不动 `--json` 输出 shape

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `time-buckets`：MODIFIED Requirement 1 文字，明确"`--day N` 输出**仅**含桶表，无前置 detail 表"。对应 Scenario "omitting --day keeps existing detail-only output" 不动（旧行为），Scenario "--day 7 produces 7 daily buckets in chronological order" 加一句"output contains only the bucket table, no preceding detail table / no `Window since: ...` header"。

## Impact

- `cmd/cc-launcher/statscmd.go` — `runStatsWithQuery` `--day` 分支减 4-5 行（删 detail 头 + 空行 + FormatTable 调用）
- `cmd/cc-launcher/smoke_test.go` — sub-test `a` / `b` 加"无 detail 头"断言；现有 7-bucket 计数断言保留
- `openspec/specs/time-buckets/spec.md` — MODIFIED Requirement 1（openspec archive 时自动合并到 base spec）
- 用户可见变化：`ccl stats --day 7` 输出从 14-15 行缩到 8-9 行（header + 7 daily + TOTAL），屏幕利用率大幅提升
- 无 JSON 字段变化（`--json --day 7` 仍给完整结构）
- 无 schema 变化、无 state.json 变化
- **Breaking for 文本消费者**：若有脚本 grep `Window since:` / `STARTED` 头 + 桶表组合的特征需更新；这种脚本不应存在（文本非 stable API）
