# Tasks: stats-day-no-detail-table

## 1. statscmd.go: --day 路径砍 detail 表

- [x] 1.1 `cmd/cc-launcher/statscmd.go::runStatsWithQuery` `--day != -1` 分支（当前 line 134-141）：删除 detail 头 `fmt.Printf("Window since: %s   Group by: %s\n\n", ...)`；删除 `stats.FormatTable(os.Stdout, result)` 调用；删除 `fmt.Println()` 空行
- [x] 1.2 保留并简化 hint：仅当 `len(result.Sessions) == 0` 时打 `(no data in window since %s)`；`len(result.Sessions) > 0` 时**不**打 hint
- [x] 1.3 保留桶聚合表 `stats.FormatBucketTable(os.Stdout, result.BucketRows, result.BucketTotals)` 调用（与之前一致）
- [x] 1.4 `--day == -1` 分支（line 121-131）**不动**——旧 detail-only 行为
- [x] 1.5 `ccl stats --json` / `--json --day 7` 两条 JSON 分支**不动**（line 112-119）

## 2. smoke_test.go: sub-test a/b 加"无 detail 头"断言

- [x] 2.1 sub-test `a_day7_text_has_7_buckets_and_total`：现有 `len(fields) < 8` 过滤仍成立；加 3 条断言：
  - `!strings.Contains(out, "Window since:")`（无 detail 头）
  - `!strings.Contains(out, "Group by:")`（无 detail 头）
  - `!strings.Contains(out, "STARTED")`（无 detail 表头列）
- [x] 2.2 sub-test `b_day1_text_has_hourly_with_partial_note`：同样加 3 条断言
- [x] 2.3 sub-test `c_no_day_text_has_no_bucket_table` **不动**（`--day` 缺省走 detail 视图，detail 表头 `STARTED` 出现是正确行为）
- [x] 2.4 跑 `go test -tags smoke ./cmd/cc-launcher/ -run TestStatsTimeBuckets` 全绿

## 3. 全量回归 + spec 同步

- [x] 3.1 跑 `go test ./...` 全绿
- [x] 3.2 跑 `go test -tags smoke ./...` 全绿
- [x] 3.3 跑 `go vet ./...` 无 warning
- [x] 3.4 跑 `go build ./...` 通过
- [x] 3.5 跑 `openspec validate stats-day-no-detail-table --strict` 通过
- [x] 3.6 跑 `openspec archive stats-day-no-detail-table --yes` 归档
- [x] 3.7 标记本文件所有 `- [x]` → `- [x]`
- [x] 3.8 `git add` 所有变更（不含 CHANGELOG），`git commit -m "fix(stats): --day 路径砍 detail 表,只输出桶视图"`

## 4. CHANGELOG + tag (v0.13.2 patch)

- [x] 4.1 `CHANGELOG.md` `[Unreleased]` 段加 v0.13.2 段（Fixes：`--day` 路径 detail 表移除）
- [x] 4.2 `git commit -m "docs: CHANGELOG 加 v0.13.2 stats-day-no-detail-table 段"`
- [x] 4.3 `git tag -a v0.13.2 HEAD -m "v0.13.2: fix stats --day no detail table"`
- [x] 4.4 一次推送：`git push origin main v0.13.2`（按 memory `cc-launcher-bump-version-lightweight-tag-trap` 显式推 lightweight tag）
