## Why

`ccl stats` 当前只能查"明细 + 7 天默认窗口"。用户想看"今天每个小时"或"最近 7 天每天"或"最近 30 天每天"的趋势对比,需要手算或改 `--since`,体验割裂。

`ccl serve --status` 已经在 db 行末尾同时打印了"总 sessions + 今日新增"两统计(`e6f4562`)。把"多窗口并列"思路扩展到 `ccl stats` 的诉求合理,但**用户最终选型不是固定 3 行聚合,而是参数化的"按时间桶"切法**(`--day N`),更接近 `gh api / git log --since` 的体验。

## What Changes

- **新增** `ccl stats --day N` flag(int,1 ≤ N ≤ 90):在 session 明细之后(或单独)额外打印一张"按时间桶聚合"的小表。
  - 桶规则:`--day 1` = 24 行 hourly(`HH:00` label),`--day N`(N≥2)= N 行 daily(`MM-DD` 或 `Sep 24` label)
  - 列:`BUCKET | SESSIONS | IN | OUT | CACHE_CRE | CACHE_READ | COST_USD | TOTAL_TOKENS(柱)`
  - 柱长 = IN + CACHE_CRE + CACHE_READ + OUT(总 token,Unicode 8 级半块 `▏▎▍▌▋▊█` 自适应归一化)
  - 表尾追加 TOTAL 行(纯数字汇总,不画柱)
  - **`--day 1` 含当前小时**(partial bucket 桶 label 加 `*` + 表头注)
  - humanize:`< 1K` 原样整数,`≥ 1K` 2 位有效数字 K/M/B(12345 → `12.3K`,1234567 → `1.23M`)
- **新增** `internal/stats/bucket.go`:导出 `QueryBucketSummary(dbPath string, kind string, sinceMs int64) ([]BucketRow, error)`,复用 `?mode=ro` 打开 + 桶 group SQL。
- **修改** `internal/stats/format.go`:新增 `FormatBucketTable(w io.Writer, rows []BucketRow, totals BucketRow, kind string, currentHourLabel string)`,渲染桶聚合表格(含柱状图、TOTAL 行)。
- **修改** `cmd/cc-launcher/statscmd.go`:
  - 砍掉 `--since` flag,`--day N` 接管 detail 窗口起点(detail since = `now - N days`)
  - 加 `--day N`(int) flag;未传时维持"7 天 detail 明细"旧行为(向后兼容)
  - 传 `--day` 时:detail + 桶聚合表;空 detail 时:`(no data ...)` + 桶聚合表
  - `runStatusInner` 改用 `stats.TodayStartMillis()`(单一来源)
- **重构** `internal/stats/time.go`(新增):导出 `TodayStartMillis()`,把 `cmd/cc-launcher/statscmd.go:316-320` 的本地时区日历日逻辑统一到一处;`statscmd.go` 删除本地 `todayStartMillis` 函数。
- **修改** `internal/stats/query.go`:`Query` 砍掉 `Since`(由 `--day` 派生),`Result` JSON 增加可选 `bucket_rows []BucketRow`(omitempty,不传 `--day` 时不出现)。
- **修改** stats 帮助(`statsHelp`):更新到 `--day` 语义。
- **时区**:复用 `TodayStartMillis()` 本地时区;桶 label 用 SQLite `strftime(..., 'localtime')` 拿本地日历日/小时。
- **空数据**:db 不存在仍走 `ErrNoDatabase` 错误路径(不变);窗口桶内零计数 humanize 为 `0`。
- **BREAKING**:
  - `--since` 被 `--day` 取代(若用户旧脚本传 `--since`,报错"flag provided but not defined")
  - 砍掉旧提案里的 `--summary`(本 change 不发布它)

## Capabilities

### New Capabilities
- `stats-time-buckets`:在 `ccl stats` 子命令下输出"按时间桶(today 24 小时 hourly / 最近 N 天 daily)聚合"的小表,与现有 session 明细列表并存;支持 humanize 单位、柱状图可视化、TOTAL 行汇总、JSON `bucket_rows` 字段。

### Modified Capabilities
(无现有 capability 的 requirement 需修改。`stats-query` 的 requirement 描述"按 session / provider / project 聚合的明细",不与新桶聚合重叠;`--since` 删除属于 CLI 行为变更,但 `stats-query` spec 不约束 `--since` 的存在性。)

## Impact

- 代码:
  - `internal/stats/bucket.go`(新增):`QueryBucketSummary` + `BucketRow` 类型 + 桶 group SQL
  - `internal/stats/time.go`(新增):`TodayStartMillis`
  - `internal/stats/format.go`:加 `FormatBucketTable` + `humanizeTokens` + `bucketBar`(柱状图归一化)
  - `internal/stats/query.go`:`Query.Since` 字段删除;`Result.BucketRows []BucketRow \`json:"bucket_rows,omitempty"\`` 新增
  - `internal/stats/bucket_test.go`(新增):覆盖 hourly/daily/边界/partial/范围/ErrNoDatabase
  - `cmd/cc-launcher/statscmd.go`:大改 -- 加 `--day` flag、砍 `--since`、`statsHelp` 更新、`runStatusInner` 改用 `stats.TodayStartMillis`、`runStats` 输出桶聚合分支
- spec:
  - 新增 `openspec/specs/stats-time-buckets/spec.md`
- 现有 `ccl stats` 的人类读者:不传 `--day` 时输出与现状完全一致(向后兼容)
- 现有 `ccl stats --json` 消费者:不传 `--day` 时 JSON 字节级一致(`bucket_rows` omitempty)
- 依赖、API、跨平台:均无影响(仍 `?mode=ro` 读 SQLite,不引入新依赖)
- 回滚:删除 4 个新文件 + 回退 statscmd.go + query.go + format.go 即可