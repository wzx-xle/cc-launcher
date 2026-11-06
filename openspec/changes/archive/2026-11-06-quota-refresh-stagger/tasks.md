## 1. 数据文件：新增相位与退避字段

- [x] 1.1 `internal/quota/store.go`：`Entry` 增加 `NextAt`（下次该刷的时刻，Unix 秒）/ `RetryAfter`（退避到什么时候）/ `BackoffSeconds`（当前退避时长）——**都是可选字段**，老数据文件零值 → 退化成旧行为（json 忽略未知字段）
- [x] 1.2 单测：老格式数据文件能被读出（这三个字段为零值）；写入后能读回

## 2. adapter：识别 429

- [x] 2.1 `internal/quota/adapter.go`：新增 `RateLimitError{Status, RetryAfter, Msg}` 类型
- [x] 2.2 `fetchJSON` 在 HTTP 429 时返回 `*RateLimitError`（读 `Retry-After` 响应头，没有则 0）
- [x] 2.3 单测：429 → `errors.As` 能识别；Retry-After 头被读出；其它状态码仍是普通错误

## 3. 刷新逻辑：相位错开

- [x] 3.1 `internal/quota/refresh.go`：到期判定改为优先用 `next_at`（没有则退回 `fetched_at + interval`）
- [x] 3.2 首次分配相位：按 `(interval, pool)` 分组，组内按 pool 名排序，第 k 个拿 `round(k × interval / n)` 秒
- [x] 3.3 刷新成功后写回 `next_at = old_next_at + interval`（**递推**，不是 `now + interval`）；递推后已过期则反复 +interval 跳到未来（**相位保持**）
- [x] 3.4 单测：8 个同周期 provider → 相位互不相同且均匀分布；刷新后 next_at 递推且相位不变；错过周期后相位仍保持

## 4. 刷新逻辑：轮内间隔

- [x] 4.1 `Config` 增加 `MinRequestGap`（毫秒；缺省 500）
- [x] 4.2 `RefreshAll` 在相邻两个请求之间 sleep 该间隔（**尊重 ctx 取消**）
- [x] 4.3 provider 配置的 `quota.minRequestGapMs` 解析（缺省 500）
- [x] 4.4 单测：两个请求之间的间隔 ≥ 配置值；ctx 取消时立即返回

## 5. 刷新逻辑：429 退避

- [x] 5.1 命中 `*RateLimitError` → `backoff = min(max(backoff×2, 1min), 30min)`（首次 1min），`retry_after = now + backoff`，写回 entry
- [x] 5.2 成功刷新一次 → `backoff` 与 `retry_after` 复位
- [x] 5.3 判定：`now < retry_after` → 跳过该 provider（不发请求）
- [x] 5.4 退避期间条目保留（不删）→ `fetched_at` 变旧 → stale 自动生效
- [x] 5.5 单测：429 → 下轮跳过；退避结束后恢复；连续 429 → 退避时长翻倍并封顶 30min；成功后复位

## 6. `ccl quota --refresh` 遵守退避

- [x] 6.1 `cmd/cc-launcher/quotacmd.go`：`--refresh` 前检查 `retry_after` → 退避中则**不发请求**，stderr 提示剩余秒数 + 退出码 1
- [x] 6.2 单测：退避中的 provider → `--refresh` 不发请求 + 提示

## 7. 文档

- [x] 7.1 `docs/provider-quota.md` 增补"刷新错开与退避"一节：相位错开是什么、429 退避规则、`minRequestGapMs` 配置项
- [x] 7.2 `CLAUDE.md` 约束 7 增补：刷新不得"同一时刻打一批"（相位错开）；429 必须退避而不是每轮硬打

## 8. 真机验证

- [x] 8.1 造 8 个共用 mock 接口的 provider（不同 key → 8 个额度池）→ 断言一轮刷新里请求**时间戳分散**在周期内（不是同一瞬间）
- [x] 8.2 mock 接口返回 429 → 断言下一轮**不再请求**、条目保留 + stale、退避时长翻倍
- [x] 8.3 回归：`go test ./...` + `go test -tags smoke ./...`
