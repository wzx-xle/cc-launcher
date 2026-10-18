## Context

`ccl stats` 是 token / cost 聚合的查询入口，2 个视图：
- **detail 视图**（`--day` 不传）：每个 session 一行，含 STARTED / PROJECT / PROVIDER / MODEL / IN / OUT / CACHE_CRE / CACHE_READ / COST_USD，末尾 TOTAL
- **桶视图**（`--day N`）：按 N 个 hourly/daily 桶分桶

v0.12.0 加 `--day` 时，base spec `time-buckets/spec.md` Requirement 1 写"after the existing session detail table, print a second human-readable table"——把 detail 留下来了。该设计 v0.13.0 桶表改数字合计时也没动，v0.13.1 加 clock 注入时也没动。

UX 后果：用户 `ccl stats --day 30` 看月度趋势时，30 天 daily 桶之前被 N 个 session 明细（10-50 行）顶到屏幕底部，用户必须 scroll 才能看到真正想要的桶数据。Detail 跟桶视图表达的是两个不同的关注维度——detail 是"每个 session 怎么花"，桶是"时间趋势是什么"——把它们并列在一次输出里不是 additive 价值，是 additive 噪音。

## Goals / Non-Goals

**Goals:**
- `ccl stats --day N` 输出**只**含桶聚合表（+ hint）—— detail 表不再前置
- `ccl stats`（`--day` 不传）维持旧 detail-only 行为
- `ccl stats --json` / `ccl stats --json --day N` 输出 shape 完全不变（`sessions` / `totals` / `bucket_rows` / `bucket_totals` 全保留）
- base spec Requirement 1 文字同步改

**Non-Goals:**
- 不引入新 flag（不动 `--day` 语义，不加 `--detail` / `--no-detail` 二次开关）
- 不动 `internal/stats/format.go::FormatTable` 函数（`--day` 缺省路径仍用）
- 不动 JSON 输出
- 不动 `ccl stats --project` / `--by-provider` / `--by-project` 在 `--day` 缺省路径的 group by 行为

## Decisions

### Decision 1：`--day != -1` 路径砍 detail 表

`runStatsWithQuery` 当前 line 134-141（`--day` 启用分支）：
```go
if len(result.Sessions) > 0 {
    fmt.Printf("Window since: %s   Group by: ...\n\n", ...)
    stats.FormatTable(os.Stdout, result)
    fmt.Println() // 空行分隔 detail 与 桶聚合
} else {
    fmt.Printf("(no data in window since %s)\n", ...)
}
stats.FormatBucketTable(os.Stdout, result.BucketRows, result.BucketTotals)
```

新：
```go
if len(result.Sessions) == 0 {
    fmt.Printf("(no data in window since %s)\n", result.Since.Format("2006-01-02"))
}
stats.FormatBucketTable(os.Stdout, result.BucketRows, result.BucketTotals)
```

detail 头 / detail 表 / 空行全删。桶表本身的 hourly note / partial `*` 标记与"无 detail 头"无关（hourly note 仍由 `FormatBucketTable` 自己输出）。

`--day == -1` 路径完全不动（line 121-131）：detail 表 + `(no data)` hint。

### Decision 2：不引 `--detail` / `--no-detail` 二次开关

理论上 `ccl stats --day 7 --detail` 也能"既要 detail 又要桶表"。但：
- 想看 detail 的用户跑 `ccl stats`（无 `--day`）已经看到 detail；想看桶跑 `ccl stats --day 7`；两个 flag 互斥语义清晰
- 加二次 flag 引入"detail + 桶 = 90 行"最差情况，回到 v0.13.0 同样的 UX 问题
- 简化 spec：base spec Requirement 1 一段话就能讲清两个 flag 的语义

### Decision 3：`--json` 输出完全不动

`--json` 是 API 形态，caller pipe 到 jq 选择性提取字段。`--json --day 7` 仍给完整 `sessions` + `totals` + `bucket_rows` + `bucket_totals`：
- 想要 detail：`jq .sessions`
- 想要桶：`jq .bucket_rows`
- 想要 TOTAL：`jq .totals` / `jq .bucket_totals`

不动 JSON shape 是 backward-compatible，不破任何现有 jq 脚本。

### Decision 4：smoke test 断言加"无 detail 头"

sub-test `a_day7_text_has_7_buckets_and_total` 加 2 条断言：
- `!strings.Contains(out, "Window since:")` —— 无 detail 头
- `!strings.Contains(out, "STARTED")` —— 无 detail 表头

`b_day1_text_has_hourly_with_partial_note` 同样加 2 条。

`c_no_day_text_has_no_bucket_table` 不动（`--day` 缺省时仍走 detail 视图，detail 表头 `STARTED` 出现是正确行为，断言只检查**不**含 `TOTAL_TOKENS` / `BUCKET`）。

## Risks / Trade-offs

- [有人已习惯 detail + 桶同屏——] → 接受。detail 仍然 1 个命令之隔（`ccl stats` vs `ccl stats --day N`），不是不可用
- [脚本 grep detail 表的特征 + 桶表特征同时存在——] → 该类脚本已破 JSON 优先原则；文本本非 stable API；不维护
- [用户某天要 detail + 桶同屏——] → 走 `ccl stats --json --day 7 | jq '{detail: .sessions, buckets: .bucket_rows}'` 拼装；或两个 command 各跑一次

## Migration Plan

无需迁移：
- 无 state.json / settings.json 变化
- 无 CLI 标志变化
- 无 `--json` 字段变化
- 灰度：单 func 几行删除 + smoke test 断言调整，`go test ./...` + `go test -tags smoke ./...` 全绿即上线

## Open Questions

无。
