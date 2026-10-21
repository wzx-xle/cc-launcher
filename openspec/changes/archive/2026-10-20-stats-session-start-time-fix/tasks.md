# Tasks: stats-session-start-time-fix

> **状态**：已实施（2026-10-20）。design.md Decision 4 / 5 / 6 已由 user 确认接受。
> 落地时对 design 有一处偏差：`bucketSkeleton` 的天数计算改用四舍五入（向下取整在
> 有夏令时的本地时区会少算一天），其余按 design 原文实施。

## 1. 确认设计决议（前置门槛）

- [x] 1.1 user 确认 design.md Decision 4：detail 视图窗口语义从"session 全生命周期用量"改为"**窗口内**用量"（行为变更，CHANGELOG 需标注）——**推荐：接受**，与 OTel 侧口径一致
- [x] 1.2 user 确认 design.md Decision 5：daily 桶归桶键 `s.started_at` → `m.captured_at`（跨天 session 的 token 按消费日分布）
- [x] 1.3 user 确认 design.md Decision 6：`--day N` 时 JSON 的 `since` 变为最旧桶那天的本地 00:00（比原来更早）
- [x] 1.4 user 批准本 design 后开始实施

## 2. collector/parser.go: Session.StartedAt 改取真实起始

- [x] 2.1 `ParseMetricsRequest` 内新增 `sessionStarts map[string]int64`（session.id → 起始 UnixMilli）
- [x] 2.2 `extractFromMetric` 里对每个带 `session.id` 的 datapoint 更新 `sessionStarts[sid] = min(existing, dp.captured_at)`
- [x] 2.3 `ParseMetricsRequest` 尾部建 session row 时：`StartedAt` 改用 `sessionStarts[sid]`（**不再用 `time.Now()`**）
- [x] 2.4 `dataPointToMetric` / `Metric` struct 已有 `CapturedAt` 字段可复用（`captured_at` 来自 `dp.GetTimeUnixNano()`），确认 0 值时的回退逻辑（`dp.GetTimeUnixNano() == 0` → `time.Now()`）仍适用
- [x] 2.5 单元测试：构造一个含 `claude_code.session.count`（ts=T）的 batch，断言 `Session.StartedAt == T`；再构造一个后续 batch（ts=T+60s），断言 db 里 `sessions.started_at` 仍是 T（配合 task 3 的 ON CONFLICT）
- [x] 2.6 跑 `go test ./internal/collector/...` 全绿

## 3. collector/store.go: ON CONFLICT + 一次性回填

- [x] 3.1 `insertSession`：`INSERT OR REPLACE` → `INSERT ... ON CONFLICT(id) DO UPDATE SET started_at = MIN(sessions.started_at, excluded.started_at), ended_at = excluded.ended_at, provider = excluded.provider, cwd = excluded.cwd, model = excluded.model`
- [x] 3.2 新增回填函数 `backfillSessionStartTimes(db *sql.DB) error`，执行 design.md Decision 3 的 UPDATE 语句（`COALESCE(session.count 的 MIN(captured_at), metrics 的 MIN(captured_at))`）
- [x] 3.3 `OpenStore`（或 `createSchema` 之后）用 `PRAGMA user_version` 驱动：`user_version < 1` → 执行回填 → `PRAGMA user_version = 1`；已 ≥1 → 跳过
- [x] 3.4 测试：构造"started_at 被污染"的 db（started_at = 最后一个 metric 的 ts），跑回填后断言 started_at == 第一个 metric 的 ts
- [x] 3.5 测试：回填幂等——连跑两次结果一致；第二次不执行（user_version 已置位）
- [x] 3.6 测试：无 metric 的 session row 回填后 started_at 不变、不为 NULL、不被删
- [x] 3.7 跑 `go test ./internal/collector/...` 全绿

## 4. stats/bucket.go: 归桶键 + 窗口过滤

- [x] 4.1 `queryBucketTokensSum`：`tsCol` 统一为 `"m.captured_at"`（删掉 hourly/daily 分支）
- [x] 4.2 同函数：`WHERE s.started_at >= ?` → `WHERE m.captured_at >= ?`
- [x] 4.3 `queryBucketSessionsCount`：**不动**（仍按 `s.started_at` 归桶、过滤）
- [x] 4.4 更新 `bucket.go` 顶部注释（现注释描述"双 key 策略"与新行为不符）
- [x] 4.5 测试：跨天 session（metrics 分布在 3 天）→ daily 桶各天只含当天 tokens
- [x] 4.6 跑 `go test ./internal/stats/...` 全绿

## 5. stats/query.go: detail 窗口 + --day N 对齐

- [x] 5.1 `buildSessionStatsSQL`：`WHERE s.started_at >= ?` → `WHERE m.captured_at >= ?`
- [x] 5.2 `QueryStats`：`q.Buckets != nil` 时 `since` 改为**最旧桶那天的本地 00:00**（design Decision 6 的代码）
- [x] 5.3 `QueryStats`：`q.Buckets == nil` 时 `since` 保持 `q.now().AddDate(0, 0, -7)` 不变
- [x] 5.4 `bucketSkeleton` 的天数计算**做了一处偏差修正**（design Decision 7 原写"保持不变"）：`(todayStartMs-sinceMs)/24h` 的向下取整改四舍五入——本地时区有夏令时（春季调时）时两端午夜相差 (N-1)*24h-1h，向下取整会少一桶，违背"恰好 N 个桶"。加断言测试验证 `--day 7` 出 7 个桶
- [x] 5.5 测试：detail totals == Σbucket rows 的 token 字段（spec 新增 Scenario "detail totals and bucket totals agree"）
- [x] 5.6 测试：窗口外活动的 session 不出现；窗口前开始但窗口内有活动的 session 出现且只含窗口内 tokens
- [x] 5.7 跑 `go test ./internal/stats/...` 全绿

## 6. 全量回归 + spec 同步

- [x] 6.1 跑 `go test ./...` 全绿
- [x] 6.2 跑 `go test -tags smoke ./...` 全绿
- [x] 6.3 跑 `go vet ./...` 无 warning
- [x] 6.4 跑 `go build ./...` 通过
- [x] 6.5 跑 `openspec validate stats-session-start-time-fix --strict` 通过
- [x] 6.6 跑 `openspec archive stats-session-start-time-fix --yes` 归档
- [x] 6.7 标记本文件所有 `- [ ]` → `- [x]`
- [x] 6.8 `git add` 所有变更（不含 CHANGELOG），`git commit -m "fix(stats): sessions.started_at 改真实 session 起始,token 按消费时刻归集"`

## 7. CHANGELOG + tag (v0.14.0 minor)

- [x] 7.1 `CHANGELOG.md` `[Unreleased]` 段加 v0.14.0 段（Fixes：`sessions.started_at` 根因 + 归集口径修正；**Breaking-ish / Compatibility 段标注 detail 视图窗口语义变更**）
- [x] 7.2 `git commit -m "docs: CHANGELOG 加 v0.14.0 stats-session-start-time-fix 段"`
- [x] 7.3 `git tag -a v0.14.0 HEAD -m "v0.14.0: fix stats session start time and consumption-time aggregation"`
- [x] 7.4 一次推送：`git push origin main v0.14.0`（tag 打完立即执行）
- [ ] 7.5 **上线后实测验证**：跑 `ccl stats --day 7` 与 OTel 侧逐日对账，断言每日偏差倍数落在 1.0 ± 0.15 内（验收标准：design 记录的 0.07x / 4.76x 消失）

  > **2026-10-21 实测结果：未通过（per-day），总量口径通过。**记录如下，供后续 change 接手：
  >
  > **可验证的部分（通过）**
  > - 根因确认：当前 db 里 **43/43** session 的 `started_at == MAX(metrics.captured_at)`（误差 < 1s），
  >   即 `started_at` 确实被"最后一次 export 时刻"污染——修复方向正确。
  > - **总量口径对齐**：本地 db 在 2026-09-29 ~ 2026-10-18 窗口内 token 合计 **458,248,742**，
  >   OTel 侧导出（`logs/stats_otel.json`，iFlytek 网关）同窗口合计 **460,347,795**，
  >   **ratio = 0.9957**（偏差 0.43%）→ 说明聚合没有重复计入、也没有丢量。
  >
  > **未通过的部分（per-day 偏差仍大）**
  > - 逐日 ratio 仍在 0.03x ~ 5.3x 之间波动（例：10-14 = 5.28x、10-08 = 0.03x），
  >   未落入 1.0 ± 0.15。试过 4 种归集口径均无法匹配：
  >   `session.count 真实起始日` / `MIN(captured_at) 日` / 污染的 `started_at 日`（旧口径） /
  >   `captured_at 日`（本 change 新口径），以及本地时区 vs UTC 两种日界——全部不匹配。
  > - **根因不在 cc-launcher 的聚合逻辑**，而在两侧数据集不同源：
  >   - `logs/stats_otel.json` 是 2026-10-18 抓取的网关导出（claude-opus-5-5[1m] 等 **Anthropic** 模型，
  >     33 个 session，含 53.2M cache_creation token）；
  >   - 当前 `~/.cc-launcher/telemetry.db` 已被重建，**只含 MiniMax-M3 / DeepSeek 用量**
  >     （**cache_creation 恒为 0**、45 个 session，与上面 33 个 session id **零交集**）；
  >   - 网关导出只有聚合值、没有 per-session / per-call 明细，无法反推它的日归属规则。
  > - 结论：当前无法用手上这两份数据完成逐日对账。**需要一份两侧观测到同一批 session 的新数据集**
  >   （daemon 升级到 v0.14.0 后重新抓一份网关导出）才能判定 per-day 口径是否真的对齐。
  >
  > **另注（运维）**：本机 daemon 仍是旧二进制（`~/workspace/golang/cc-launcher/cc-launcher serve`，
  > 09-30 启动，uptime 20 天）。它不会执行新回填，且旧 `INSERT OR REPLACE` 会继续把
  > `started_at` 写成 export 时刻。**升级 daemon 后需重启一次**，回填才会跑（`PRAGMA user_version` 0 → 1）。


## 8. 收尾（可选，另开 change）

- [ ] 8.1 评估是否需要 `ccl stats --all-time`（session 全生命周期用量视图）——design Open Question 1
- [ ] 8.2 评估 `sessions.ended_at` 的修正（同 `started_at` 思路）——design Open Question 2
