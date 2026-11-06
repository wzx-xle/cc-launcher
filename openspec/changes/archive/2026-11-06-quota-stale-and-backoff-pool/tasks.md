## 1. stale 判定：给取整误差 1 秒容忍

- [x] 1.1 `internal/quota/model.go`：新增 `StaleTolerance = 1s` 常量 + 注释（取整误差的上界恰是 1 秒）
- [x] 1.2 `cmd/cc-launcher/quotacmd.go`：stale 判定改为 `age > interval + StaleTolerance`
- [x] 1.3 单测：数据年龄略超周期（≤ 1 秒）→ **不**标 stale；真的超过周期 + 容忍 → 标 stale

## 2. 退避状态改按额度池记录

- [x] 2.1 `internal/quota/store.go`：`File` 增加 `Backoffs map[string]Backoff`（key = pool 哈希）；`Backoff{RetryAfter, Seconds}`
- [x] 2.2 `internal/quota/store.go`：`Entry` 去掉 `RetryAfter` / `BackoffSeconds` 两个字段
- [x] 2.3 `internal/quota/store.go`：`Load()` 时**惰性迁移**——条目里的旧退避字段并入 `backoffs[entry.Pool]`（条目没有 pool 则丢弃）
- [x] 2.4 `internal/quota/store.go`：新增 `SetBackoff(pool, retryAfter, seconds)`（读-改-写）
- [x] 2.5 `internal/quota/refresh.go`：退避判定与写回改按 pool（`file.Backoffs[pool]`）
- [x] 2.6 `cmd/cc-launcher/quotacmd.go`：`--refresh` 的退避检查改查 pool（先 `Resolve()` 拿端点 → `PoolID`）
- [x] 2.7 单测：退避按 pool 生效；**换 key（换池）后不受老退避影响**；老数据文件的退避能被迁移（不丢）

## 3. 文档

- [x] 3.1 `docs/provider-quota.md`：数据文件结构补 `backoffs` 段；stale 语义补"1 秒取整误差容忍"

## 4. 验证与发布

- [x] 4.1 真机：本机数据文件升级后（老格式 → 新格式）退避不丢、stale 不再误报
- [x] 4.2 回归：`go test ./...` + `go test -tags smoke ./...`
- [ ] 4.3 `CHANGELOG.md` 加 `0.20.2` 段（按既有格式，分类用 Fixes）
- [ ] 4.4 `openspec archive quota-stale-and-backoff-pool`
- [ ] 4.5 打 tag `v0.20.2` + 推送（**本次不发 scoop**，用户明确要求）
