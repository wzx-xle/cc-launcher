# Tasks: stats-bucket-tokens-column-numeric

## 1. format.go: TOTAL_TOKENS 列改为数字合计 + 删除 bucketBar

- [x] 1.1 `internal/stats/format.go::FormatBucketTable`：把末列 `bucketBar(t, maxTokens, r.IsCurrent)` 改为 `humanizeTokens(t)`（其中 `t = r.InputTokens + r.OutputTokens + r.CacheCreate + r.CacheRead`，已在原函数循环里算好）
- [x] 1.2 同函数 TOTAL 行：把 `strings.Repeat("─", 20) + " (total)"` 替换为 `humanizeTokens(totals.InputTokens + totals.OutputTokens + totals.CacheCreate + totals.CacheRead)`
- [x] 1.3 删 `bucketBar()` 函数本体（`format.go:194-247`）；同步删掉函数上面的 doc 注释块（`§D6 + §D8`）
- [x] 1.4 删 `format.go` 里 `math` import（bucketBar 删了之后只剩它用到 `math.Round`）；保留 `strings` / `strconv` 不动
- [x] 1.5 hourly note `(* = 当前小时,数据仍在累计)` 保持 defer Flush 后输出（与 base spec partial 标记得以共存，跟柱无关）
- [x] 1.6 跑 `go build ./internal/stats/...` 通过

## 2. bucket_test.go: 重写 / 删除所有 bucketBar / 柱相关断言

- [x] 2.1 `internal/stats/bucket_test.go::TestBucketBar_*` 整组测试函数（lines 360-468 范围内）全删（5 个 case：fullBar/max、halfBlocks/tokens、zero/max、partial/non-partial）
- [x] 2.2 `internal/stats/bucket_test.go::TestBucketBar_ZeroTokensWithPartial` 单独 case（如独立函数）也删
- [x] 2.3 `TestFormatBucketTable_TotalSeparator`：把"20 ─ + (total)"断言改为"TOTAL 行 TOTAL_TOKENS 列等于 `humanizeTokens(...)` 数字字符串"；顺便确认行里没有 `─` 字符
- [x] 2.4 新增 `TestFormatBucketTable_TokensSumColumn`：构造 3 桶 (e.g. 1000+200+150+650=2000 / 500+0+0+0=500 / 0+0+0+0=0)，断言每行 `TOTAL_TOKENS` 列分别为 `2.00K` / `500` / `0`；TOTAL 行 `TOTAL_TOKENS` 列等于 `humanizeTokens(2500) = "2.50K"`
- [x] 2.5 跑 `go test ./internal/stats/...` 全绿

## 3. smoke_test.go: 桶表文本断言改写

- [x] 3.1 找 `cmd/cc-launcher/smoke_test.go::TestCLISmoke_bucketTable` / 类似名下的所有 `TOTAL_TOKENS` / `⏳` / `·` / `─` / `bucketBar` 字符串断言
- [x] 3.2 把"TOTAL_TOKENS 列头存在"断言保留（列名不变）；把"bar alignment" / "⏳ 末位" / "· 占位" / "─ separator" 等断言全部改写或删除
- [x] 3.3 新增断言：每行 `TOTAL_TOKENS` 列内容是 `humanizeTokens` 输出形态（即匹配正则 `^[0-9]+(\.[0-9]+)?[KMB]?$` 或 `^0$`）；TOTAL 行 `TOTAL_TOKENS` 列是 humanize 总和；全表无任何 `█` / `▏▎▍▌▋▊▉` 半块字符 / `⏳` / `─` / `·`
- [x] 3.4 跑 `go test ./cmd/cc-launcher/...` 全绿

## 4. README.md: 同步删除柱描述

- [x] 4.1 第 315 行附近："每桶一行 `BUCKET | SESSIONS | IN | OUT | CACHE_CRE | CACHE_READ | COST_USD | TOTAL_TOKENS(柱)`"中的 `(柱)` 后缀删掉；"柱长按总 token 数 8 级半块（`▏▎▍▌▋▊▉█`）归一化"整句替换为"末列 TOTAL_TOKENS 为本桶 token 合计（humanize 化的 IN+OUT+CACHE_CRE+CACHE_READ）"
- [x] 4.2 同一段其后："`--json --day N` 时 JSON 加 `bucket_rows` 数组 + `bucket_totals` 对象。不传 `--day` 时维持旧 detail-only 行为。" 不动

## 5. 全量回归 + spec 同步

- [x] 5.1 跑 `go test ./...` 全绿（含 `internal/stats` / `internal/tui` / `cmd/cc-launcher` / `internal/cli` 等已有测试不破坏）
- [x] 5.2 跑 `go vet ./...` 无 warning
- [x] 5.3 跑 `go build ./...` 通过
- [x] 5.4 跑 `openspec validate stats-bucket-tokens-column-numeric --strict` 通过
- [x] 5.5 跑 `openspec archive stats-bucket-tokens-column-numeric --yes` 归档（archive 时 OpenSpec 把 spec delta 合入 `openspec/specs/time-buckets/spec.md`）
- [x] 5.6 `git add` 所有变更，`git commit -m "feat(stats): 桶聚合表 TOTAL_TOKENS 列改数字合计"`（commit message 用简体中文，按项目约定）

## 6. CHANGELOG + tag

- [x] 6.1 `CHANGELOG.md` `[Unreleased]` 段加新版本段（按既有 v0.x.y 格式 Refactor / Features / 分类）
- [x] 6.2 `git commit -m "docs: CHANGELOG 加 v0.X.Y <段名>"`（CHANGELOG commit）
- [x] 6.3 `git tag -a v0.X.Y HEAD -m "<release message>"`（tag 指向 CHANGELOG commit，按 CLAUDE.md 强约束）
- [x] 6.4 一次推送：`git push origin main --follow-tags`（commit + tag 一起推，符合 CLAUDE.md 的"CHANGELOG commit 与 tag 应在同一次推送内完成"约束）
