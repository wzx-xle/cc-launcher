## 1. internal/stats 数据层

- [x] 1.1 新增 `internal/stats/time.go`:导出函数 `TodayStartMillis() int64`——本地时区日历日 00:00 的 UnixMilli。函数体直接从 `cmd/cc-launcher/statscmd.go:316-320` 的 `todayStartMillis()` 搬过来(保持语义一致)。`runStatusInner` 改为 `stats.TodayStartMillis()`;`statscmd.go` 本地的 `todayStartMillis` 函数删除。**单一来源**:本 change 后本地日历日逻辑只在 `internal/stats` 包定义。
- [x] 1.2 新增 `internal/stats/bucket.go`:导出类型 `BucketRow`(字段:`Label string \`json:"bucket,omitempty"\``(text+JSON 共用;omitempty 让 bucket_totals 不含 bucket 字段,符合 spec §4)、`BucketISO string \`json:"bucket_iso"\``、`BucketUnixMs int64 \`json:"bucket_unix_ms"\``、`IsCurrent bool \`json:"is_current,omitempty"\``、`Sessions int64 \`json:"sessions"\``、`InputTokens int64 \`json:"input_tokens"\``、`OutputTokens int64 \`json:"output_tokens"\``、`CacheCreate int64 \`json:"cache_creation_tokens"\``、`CacheRead int64 \`json:"cache_read_tokens"\``、`CostUSD float64 \`json:"cost_usd"\``)。导出 `QueryBucketSummary(dbPath string, kind string, sinceMs int64) ([]BucketRow, error)`,内部:
  - `?mode=ro` 打开 db(与 `CountSessionsByWindow` / `QueryStats` 同源)
  - 桶起点 SQL `strftime` 表达式按 kind 分:
    - `kind == "hour"`:sessions 计数 SQL 用 `s.started_at`(sessions 表没 captured_at 字段);tokens 求和 SQL 用 `m.captured_at`(让 token 归属真实采集小时,符合 spec §D6 + §1.2 双 key 策略)
    - `kind == "day"`:两 SQL 都用 `s.started_at`(daily 跨日概率低)
  - **两条独立 SQL**(D1):
    1. `SELECT bucket_str AS bucket, COUNT(*) AS sessions FROM sessions WHERE started_at >= ? GROUP BY bucket_str` —— 仅 sessions 表
    2. `SELECT bucket_str AS bucket, SUM(CASE WHEN m.name='claude_code.token.usage' AND JSON_EXTRACT(m.labels,'$.type')=... THEN m.value END) AS in/out/cache_cre/cache_read, SUM(CASE WHEN m.name='claude_code.cost.usage' THEN m.value END) AS cost FROM sessions LEFT JOIN metrics m ON m.session_id=s.id WHERE s.started_at >= ? GROUP BY bucket_str` —— tokens + cost
  - Go 端按 bucket label map merge 两结果;`kind == "hour"` 时遍历 `00..currentHour` 补齐缺失桶(全零行,`IsCurrent` 标记当前小时);`kind == "day"` 时遍历 `now-N+1 .. now` 补齐缺失 daily 桶
  - 排序:桶 label `string <` 升序
  - db 不存在 → 返回 `ErrNoDatabase` 包裹的错误
- [x] 1.3 修改 `internal/stats/query.go`:`Query` 结构砍掉 `Since time.Time` 字段(由 `StatsCmd` 派生),新增 `Buckets *int \`json:"-"\``(nil = 不启用);`Result` 结构加 `BucketRows []BucketRow \`json:"bucket_rows,omitempty"\`` + `BucketTotals *BucketRow \`json:"bucket_totals,omitempty"\``。`QueryStats` 末尾(原 `return result, nil` 之前)若 `q.Buckets != nil`,调 `QueryBucketSummary(dbPath, kind, sinceMs)`:
  - `kind` 推导:`*q.Buckets == 1` → `"hour"`,else → `"day"`
  - `sinceMs` = `now.AddDate(0, 0, -*q.Buckets).UnixMilli()`(detail 跟 `BucketSummary` 同窗口,D10)
  - `result.BucketRows = rows`(可能空切片)
  - `result.BucketTotals` = 所有桶求和的 `BucketRow`(Sessions/InputTokens/OutputTokens/CacheCreate/CacheRead/CostUSD 加和;Label/BucketISO/BucketUnixMs/IsCurrent 留零值)
  - 错误分级(跟原 `--summary` review 修复 3 一致):`errors.Is(err, ErrNoDatabase)` → 整体 return error;其它 SQL 错误 → `fmt.Fprintf(os.Stderr, "warning: bucket summary unavailable: %v\n", err)`,`BucketRows` 保持空,`BucketTotals` 保持 nil。明细仍正常返回。
- [x] 1.4 修改 `internal/stats/format.go`:
  - 新增 `humanizeTokens(n int64) string`:D7 规则(`< 1K` 原样整数;`≥ 1K` 三档 trimK + K/M/B 后缀:0<v<10 → "%.2f",10≤v<100 → "%.1f",100≤v<1k → "%.0f";边界 ≥999_500 切到下一档用 "%.2f",避免 "1000K" / "1000M")。
  - 新增 `bucketBar(tokens int64, max int64, partial bool) string`:固定 20 rune 宽度(注意是 rune 不是 byte;`strings.Builder.Len()` 是 byte 数,会让柱状图变短):
    - **除零保护 + 零值占位**:`max == 0` 或 `tokens == 0` 时直接返回 `·` + 19 空格(1 个 U+00B7 MIDDLE DOT 占位 + 19 padding),不进入 cells 计算
    - 正常路径:`cells = round(tokens/max*20*8)`(0 ≤ cells ≤ 160)
    - **完整 8 级字符集**:`▏▎▍▌▋▊▉█`(1/8..7/8 半块 + 8/8 整块,补 `▉` 让 75%-99% 段不丢级)
    - 按 `cells/8` 个 `█` + 1 个半块字符(对应余数 0..7)+ 空格补到 20 rune
    - partial=true 时末位 rune 替换为 `⏳`(U+23F3),整体仍 20 rune
  - 新增 `FormatBucketTable(w io.Writer, rows []BucketRow, totals *BucketRow)`:渲染表头 `BUCKET | SESSIONS | IN | OUT | CACHE_CRE | CACHE_READ | COST_USD | TOTAL_TOKENS(柱)` + 数据行(`Label` 字段、5 个 humanize tokens、`COST_USD` 用 `%.4f`、柱状图)。`--day 1` partial 桶(label 带 `*` 标记)柱末追加 `⏳`(U+23F3)作为视觉补充,整体仍保持 20 字符宽度(柱长按比例缩短 1 字符容纳 `⏳`)。`--day 1` 注 `(* = 当前小时,数据仍在累计)` 放在 `header + data + TOTAL` 之下(footnote 位置),通过 defer 在 `tw.Flush()` 之后写到 w —— 不进 tabwriter(否则 7 个 \t 会撑宽 column 8 到 28 chars 超出 80-col)。最末追加 `TOTAL` 行(`Label="TOTAL"`、所有字段 humanize 汇总、**柱列渲染为 20 个 `─`(U+2500)+ ` (total)` 横线分隔,不是 bar**)。
  - **tabwriter padding=1**(非默认 2):column 总宽更紧,控 ≤80-col(spec scenario 4 hard cap)。
- [x] 1.5 新增 `internal/stats/bucket_test.go`:覆盖 spec 全部 scenario,17 个测试函数(超过最低 8 个要求)。用 `queryBucketSummaryAt(dbPath, kind, sinceMs, now time.Time)` internal helper 注入 mock 当前小时,`setupBucketTestDB` 用 `collector.WithClock(func() time.Time { return fixedNow() })` 注入固定时间。
  - **Bucket_Hourly_24Rows** → scenario "--day 1 produces 24 hourly buckets" → mock now=17:30 → 18 行(00..17);首行 "00:00"、末行 "17:00*"
  - **Bucket_Hourly_PartialMarker** → scenario "--day 1 includes the current hour with partial marker" → 17:00 行 IsCurrent=true,Label="17:00*",恰好 1 个 IsCurrent=true 且为末行;前一行 16:00 IsCurrent=false;BucketISO="2026-09-30T17:00"
  - **Bucket_Hourly_CrossHour_TokensOnCaptureHour** → 验证 §D6 双 key 策略:session 15:30 + metric captured_at 16:45 → 15:00 桶 sessions=1/tokens=0,16:00 桶 sessions=0/tokens=100
  - **Bucket_Daily_7Rows** → scenario "--day 7 produces 7 daily buckets in chronological order" → 7 行 daily,labels 09-24..09-30
  - **Bucket_BoundariesInclusive** → scenario "--day 7 boundary aligns with local calendar day" + "--day 30 boundary ends today" → session 在 todayStartMs 整 0 分时计入今日桶(末行)
  - **Bucket_TokenSums** → scenario "token / cost values follow the same metric-name rules" → 6 字段 SUM 跟 base stats 同规则
  - **Bucket_EmptyDB_HourlyRows** → scenario "--day N tolerates empty db gracefully" → 空 db 调 hourly 仍返回 currentHour+1 行零值桶(mock 17 时为 18 行)
  - **Bucket_Daily_FillsMissing** → daily 桶 7 天无 session 也返回 7 行零值桶(不是 0 行)
  - **Bucket_ErrNoDatabase** → db 不存在 → `errors.Is(err, ErrNoDatabase)`
  - **HumanizeTokens_Boundaries** → 0/350/999/1000/1234/12345/123456/999_499/999_500/999_999/1M/1.23M/12.3M/123M/999_499_999/999_500_000/1.23B 全覆盖(含 K/M/B 边界切档)
  - **BucketBar_Proportional** → scenario "TOTAL_TOKENS bar length is proportional to total tokens" + table-driven 8 case 覆盖完整 8 级字符集 `▏▎▍▌▋▊▉█`(cells=1..7 各 remainder 命中对应半块字符 + cells=8 remainder=0 全格)
  - **BucketBar_ZeroTokens** → scenario "0-token buckets render · placeholder" → `tokens=0` 或 `max=0` → bar = `·` + 19 空格(20 rune,首字符 `·`,其余空格);不进入 cells 计算
  - **BucketBar_Partial** → scenario "partial bucket bar ends with ⏳" → partial 桶 → bar 末位是 `⏳`;非 partial 桶 → 末位是空格或半块字符(非 `⏳`)
  - **BucketBar_PartialZeroTokens** → partial 零值桶仍保留 `⏳` 标记(辅助覆盖)
  - **BucketBar_TotalSeparator** → scenario "TOTAL row shows horizontal-rule separator" → TOTAL 行 TOTAL_TOKENS 列 = `──────────────────── (total)`(20 个 `─` + ` (total)`),不是 bar
  - **FormatBucketTable_HourlyNote** → hourly 模式表头注 "(* = 当前小时,数据仍在累计)" 存在
  - **Bucket_JSONFieldBucket** → spec §4 必填字段"bucket"(/JSON key)=text + single-char `*` partial marker 均保留;`BucketTotals` Label 空→ omitempty 跳过 bucket 字段

## 2. CLI 入口

- [x] 2.1 修改 `cmd/cc-launcher/statscmd.go::registerStatsFlags`:砍 `--since` flag;新增 `--day` int flag,**default = -1**(Go flag Int 无法区分"未传"与"显式 0",0 本身非法,所以 -1 同时承担"未传"和"非法"标识)。同步更新 `completion.go::completeAtStats` 共用同一函数(cli-shell-completion §D2,自动跟随)。新增 `statsExitFn` 钩子(类比 `completion.go::exitFn`),smoke test 覆盖为 no-op 避免真退出整个 test 进程。
- [x] 2.2 修改 `cmd/cc-launcher/statscmd.go::runStats`:砍 `--since` 解析分支;构造 `stats.Query{Buckets: day > 0 ? &day : nil}`(`-1`=未启用,`>0`=启用);调用 `QueryStats(dbPath, q)`(内部已处理桶聚合)。输出分支:
  - `--json` 路径:`json.MarshalIndent(result, ...)`(result.BucketRows / BucketTotals 由 QueryStats 自动填,`omitempty`)
  - `--day` 未启用(`*day == -1`)+ 明细非空:WindowHeader + `FormatTable`(现有行为)
  - `--day` 未启用 + 明细空:`(no data in window since ...)`
  - `--day` 启用 + 明细非空:WindowHeader + `FormatTable` + 空行 + `FormatBucketTable(os.Stdout, result.BucketRows, result.BucketTotals)`
  - `--day` 启用 + 明细空:`(no data in window since ...)` + 直接接 `FormatBucketTable`(无空行,跟现有 hint 紧凑)
  - **校验**:`*day != -1 && (*day < 1 || *day > 90)` → `fmt.Fprintf(os.Stderr, "--day 必须在 1 到 90 之间, got: %d\n", *day)` + `statsExitFn(2)`(Go flag parse error 约定;`statsExitFn` 钩子,smoke test 可覆盖为 no-op)
- [x] 2.3 修改 `cmd/cc-launcher/statscmd.go::runStatusInner`:line 300 的 `todayStartMillis()` 改为 `stats.TodayStartMillis()`。本地 `todayStartMillis` 函数(line 316-320)删除。
- [x] 2.4 修改 `cmd/cc-launcher/statscmd.go::statsHelp`:砍 `--since` 说明;新增 `--day` 说明;把"默认最近 7 天"措辞改成"不传 --day 时默认 7 天 detail"。

## 3. 验证

- [x] 3.1 `cd cmd/cc-launcher && go build ./...` + `go test ./internal/stats/... ./cmd/cc-launcher/...` 全绿。
- [x] 3.2 `cmd/cc-launcher/smoke_test.go` 新增 `TestStatsTimeBuckets` 段(参考 `TestServeStatusQuery` 的 db mock 风格),覆盖 spec 里"JSON shape 行为" + "命令行文本输出" + "校验路径" + "跨表示一致性" 四类 scenario,10 个子测试(单元测试覆盖数据层;smoke test 覆盖 CLI 集成):
  - (a) `ccl stats --day 7` 文本含 `09-` 开头的 7 个 daily 桶 label + `TOTAL_TOKENS` 列头 + `TOTAL` 行(对应 scenario "--day 7 produces 7 daily buckets in chronological order" + "TOTAL row shows humanized aggregates without a bar")
  - (b) `ccl stats --day 1` 文本含 0..currentHour hourly 桶 label(动态)+ `(* = 当前小时,数据仍在累计)` 注 + 当前小时 label 带 `*`(对应 scenario "--day 1 produces 24 hourly buckets" + "--day 1 includes the current hour with partial marker")
  - (c) `ccl stats` 不传 `--day` 时文本不含 `TOTAL_TOKENS` / `BUCKET` 头(对应 scenario "omitting --day keeps existing detail-only output")
  - (d) `ccl stats --json --day 7` JSON 含 `bucket_rows` 数组(7 个对象)+ `bucket_totals` 对象 + 每对象含 `bucket`(含 `*`)、`bucket_iso` / `bucket_unix_ms` 字段(对应 scenario "--json --day 7 emits bucket_rows array and bucket_totals object" + spec §4 必填字段 `bucket`)
  - (e) `ccl stats --json` 不传 `--day` 时 JSON 无 `bucket_rows` / `bucket_totals` 键;raw 文本也无 `bucket_rows` / `bucket_totals` 字串(对应 scenario "--json without --day omits bucket_rows and bucket_totals" + 字节级一致)
  - (f) `ccl stats --since 2026-09-20` 报 "flag provided but not defined: -since"(对应 scenario "--since is removed")
  - (g) `ccl stats --day 0` / `--day 91` exit 非 0 + stderr 含 `--day 必须在 1 到 90 之间, got:`(对应 scenario "--day N validates N is in [1, 90]")
  - (h) `ccl stats --json --day 1` 当前小时桶对象含 `is_current: true` 字段,`BucketUnixMs` 推 hour 等于 currentHour(对应 scenario "--json --day 1 marks current hour bucket")
  - (i) 在 ≤80-col 终端宽度下,data 行宽度 ≤ 80 chars + 柱状图右边界 `±1 column tolerance` 对齐(对应 scenario "bucket table renders within 80-column terminal width without column overflow")。该 case 在 smoke_test 中通过 `stats.FormatBucketTable` 直接渲染 + 按行 rune-count 切分 + 断言柱列右边界一致来验证。note / header / TOTAL 行不参与 bar alignment 比较(spec 设计如此)。
  - (j) 跨 text/JSON 一致性:同 db 跑 `--day 7`(text)+ `--json --day 7`(JSON),parse 各自输出,断言 bucket_rows[i] 与 text 第 i 桶的 sessions / input_tokens / output_tokens / cache_creation_tokens / cache_read_tokens / cost_usd 6 字段相等(对应 scenario 4 末尾 "bucket_rows numeric fields match the human-readable table")。
- [x] 3.3 README"用量监控"小节(如有)更新:把 `--summary` 字样去掉;加 `ccl stats --day 7` / `--day 1` 例子说明。若 README 已有该小节才改,没有则跳过。
- [x] 3.4 手工验证 `humanizeTokens` 边界 case(12345 → `12.3K`,1234567 → `1.23M`,1234567890 → `1.23B`,350 → `350`,0 → `0`)—— 仅在 3.1 单元测试失败时人工核查。