# Tasks: stats-clock-injection

## 1. internal/stats/query.go: Query.Clock 字段 + QueryStats 走注入 clock

- [x] 1.1 `Query` struct 加 `Clock func() time.Time` 字段（不加 `json` tag，`Query` 不走 `Result.MarshalJSON`）
- [x] 1.2 同文件加 unexported helper `func (q Query) now() time.Time`：`q.Clock != nil` 走 `q.Clock()`，否则 `time.Now()`
- [x] 1.3 `QueryStats` 函数体内：line 107 `since = time.Now().AddDate(0, 0, -*q.Buckets)` 改 `since = q.now().AddDate(0, 0, -*q.Buckets)`；line 110 `since = DefaultSince()` 改 `since = q.now().AddDate(0, 0, -7)`（与 q.Buckets==nil 路径等价，inline 而非再调 DefaultSince）
- [x] 1.4 `DefaultSince()` 函数保留 `time.Now()` 内部实现（向后兼容，外部 caller 可调；本 change 不引入新 caller 调它）
- [x] 1.5 跑 `go build ./internal/stats/...` 通过

## 2. internal/stats/bucket.go: QueryBucketSummary 加 nowFn 参数 + QueryStats 内部传 now

- [x] 2.1 `QueryBucketSummary(dbPath, kind, sinceMs, nowFn func() time.Time)` 签名加 `nowFn` 作为第 4 参数
- [x] 2.2 内部 `time.Now()` 改 `nowFnOrDefault(nowFn) := nowFn != nil ? nowFn() : time.Now()`（helper or inline）
- [x] 2.3 私有 helper `todayStartMillisAt(now time.Time) int64` 替代 `startOfDayMillis(now)`（rename 为更明确语义，或保持 startOfDayMillis）
- [x] 2.4 `TodayStartMillis()` 公开签名不变：内部仍 `time.Now()` → `time.Date(...).UnixMilli()`（不动 `CountSessionsByWindow` / `runStatusInner` 的用法）
- [x] 2.5 `QueryStats` 内部 line 180 `QueryBucketSummary(dbPath, kind, sinceMs)` 改 `QueryBucketSummary(dbPath, kind, sinceMs, q.Clock)`
- [x] 2.6 跑 `go build ./internal/stats/...` 通过

## 3. internal/stats/bucket_test.go: 4 个 QueryBucketSummary 调用方补参数

- [x] 3.1 `bucket_test.go:311` 的 `QueryBucketSummary("/tmp/...", "hour", 0)` 改 `QueryBucketSummary("/tmp/...", "hour", 0, nil)`
- [x] 3.2 grep 全仓其它 `QueryBucketSummary(` 调用方（预期 3-4 处），全部补 `nil` 作为 nowFn
- [x] 3.3 跑 `go test ./internal/stats/...` 全绿

## 4. cmd/cc-launcher/statscmd.go: 抽 runStatsWithQuery 内部函数

- [x] 4.1 现有 `runStats(args []string) error` 函数体重命名为 `runStatsWithQuery(q stats.Query, args []string) error`，函数体内构造 `q` 的代码删掉（直接用入参）
- [x] 4.2 新增 thin wrapper `runStats(args []string) error { return runStatsWithQuery(stats.Query{}, args) }`（main.go 调的还是 `runStats`，签名不变）
- [x] 4.3 跑 `go build ./cmd/cc-launcher/...` 通过

## 5. cmd/cc-launcher/smoke_test.go: 顶层 now 注入 stats query + a/b/h 改 mock today

- [x] 5.1 `TestStatsTimeBuckets` 顶层 `now := time.Date(2026, 9, 30, 17, 30, 0, 0, time.Local)` 已存在（line 686），不需要新加
- [x] 5.2 所有 sub-test 把 `runStats(args)` 改为 `runStatsWithQuery(stats.Query{Clock: func() time.Time { return now }}, args)`
- [x] 5.3 sub-test `a_day7_text_has_7_buckets_and_total`：expected dates 改回 `09-24..09-30`（与 mock today 对齐，本 change 修复目标）
- [x] 5.4 sub-test `b_day1_text_has_hourly_with_partial_note`：`currentHour := time.Now().Hour()` 改 `currentHour := now.Hour()` = 17
- [x] 5.5 sub-test `h_json_day1_marks_current_hour`：`currentHour := time.Now().Hour()` 改 `currentHour := now.Hour()` = 17
- [x] 5.6 跑 `go test -tags smoke ./cmd/cc-launcher/ -run TestStatsTimeBuckets` 全绿（a/b/c/d/e/f/g/h/i/j 全部 PASS，pre-existing a_day7 fail 修好）

## 6. 全量回归 + spec 同步

- [x] 6.1 跑 `go test ./...` 全绿
- [x] 6.2 跑 `go test -tags smoke ./...` 全绿
- [x] 6.3 跑 `go vet ./...` 无 warning
- [x] 6.4 跑 `go build ./...` 通过
- [x] 6.5 跑 `openspec validate stats-clock-injection --strict` 通过
- [x] 6.6 跑 `openspec archive stats-clock-injection --yes` 归档
- [x] 6.7 标记本文件所有 `- [x]` → `- [x]`
- [x] 6.8 `git add` 所有变更，`git commit -m "fix(stats): Query.Clock + QueryBucketSummary nowFn 注入,smoke test 不再依赖真实 today"`

## 7. CHANGELOG + tag (v0.13.1 patch)

- [x] 7.1 `CHANGELOG.md` `[Unreleased]` 段加 v0.13.1 段（Fixes：smoke test 跨日期边界修复 + clock 注入基础设施）
- [x] 7.2 `git commit -m "docs: CHANGELOG 加 v0.13.1 stats-clock-injection 段"`
- [x] 7.3 `git tag -a v0.13.1 HEAD -m "v0.13.1: fix stats clock injection"`
- [x] 7.4 一次推送：`git push origin main v0.13.1`（commit + tag 一起推，按 CLAUDE.md 强约束；避免 `git push --follow-tags` 不推 lightweight tag 的陷阱——见 memory `cc-launcher-bump-version-lightweight-tag-trap`）
