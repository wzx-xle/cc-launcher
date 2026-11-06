## 1. collector：provider 活跃度跟踪

- [x] 1.1 新增 `internal/collector/activity.go`：`Activity` 类型（内存 map + mutex），`Mark(provider, t)` / `Recent(window) map[string]bool`
- [x] 1.2 `internal/collector/server.go`：`Server` 持有 `activity`；`handleMetrics` 解析出 session 的 provider 后 `Mark`
- [x] 1.3 暴露 `Server.Activity()` 供 serve 读取
- [x] 1.4 单测：Mark 后 Recent 命中；超出窗口不命中；空 provider 名不记；并发安全

## 2. serve：只刷新活跃的 provider

- [x] 2.1 `cmd/cc-launcher/serve.go`：刷新前用 `srv.Activity().Recent(window)` 过滤 provider 列表
- [x] 2.2 窗口常量 **5 分钟**（= 4 个 OTel 周期；实测 Claude Code 每 60 秒推一次 metrics）
- [x] 2.3 名单为空 → 不查任何 provider（一个请求都不发）
- [x] 2.4 单测：活跃的 provider 被查；不活跃的不被查；`--refresh` 不受影响（显式点名照查）

## 3. 文档

- [x] 3.1 `docs/provider-quota.md`：说明"只刷新最近有活动的 provider"（活动信号 = OTel，窗口 5 分钟）；`ccl quota --provider X` 对不活跃的 provider 无数据 → 提示用 `--refresh`

## 4. 验证与发布

- [x] 4.1 真机：停掉部分 provider 的活动后，观察它们不再被查；用起来后重新被查
- [x] 4.2 回归：`go test ./...` + `go test -tags smoke ./...`
- [x] 4.3 `CHANGELOG.md` 加 `0.20.3` 段（patch：起因是修 429，用户不需新动作；但"刷新的对象变少"是可见的行为变化，版本号若有异议按用户定）
- [x] 4.4 `openspec archive quota-active-providers-only`
- [x] 4.5 打 tag + 推送（scoop 是否跟发由用户定）
