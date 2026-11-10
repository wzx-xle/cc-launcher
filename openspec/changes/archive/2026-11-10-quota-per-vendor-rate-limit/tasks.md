## 1. 厂商级请求间隔（A）

- [x] 1.1 `internal/quota/refresh.go`：新增 `EndpointThrottle` —— 按 endpoint 记"上次请求时刻"，`Wait(ctx, endpoint, gap)` 在间隔不足时阻塞等待，`Mark(endpoint)` 在请求发出时记录时刻
      —— **必须跨轮有效**：节流状态由 daemon 持有（内存），否则跨轮相邻的两次请求管不住 —— 而日志里两个讯飞池的 429 时刻只隔 8-20 秒，正是跨轮形态
- [x] 1.2 `internal/quota/refresh.go`：`Config` 新增 `VendorGap time.Duration`（JSON key `vendor_gap`，毫秒）+ `DefaultVendorGap = 10 * time.Second`
- [x] 1.3 `internal/quota/refresh.go`：`RefreshAll` 的请求循环里，发请求前 `throttle.Wait(ctx, endpoint, gap)` → `throttle.Mark(endpoint)` → `Fetch`
      —— 顺序要点：Mark 必须在**请求发出时**，不是轮开始时
- [x] 1.4 `cmd/cc-launcher/serve.go`：`quotaRefreshLoop` 持有 `quota.NewEndpointThrottle()`，每轮传给 `RefreshAll`（跨轮复用同一实例）
- [x] 1.5 单测：同一 endpoint 的两个池，第二次请求的**发起时刻**距第一次 ≥ vendor_gap（用假 adapter 记录时刻）；不同 endpoint 的两个池**不受**该间隔约束

## 2. 退避按厂商记（B）

- [x] 2.1 `internal/quota/store.go`：退避记录的键从"池哈希"改为"厂商哈希"（endpoint 的 sha256 前缀，与 `PoolID` 同样口径）
      —— 旧记录（64 位十六进制池哈希）读不出 → 视为无退避（无需迁移脚本）
- [x] 2.2 `internal/quota/refresh.go`：429 时写**厂商级**退避；判定退避时按**该池的 endpoint** 查 → 同厂商所有池一起暂停
- [x] 2.3 `internal/quota/refresh.go`：`RefreshOne`（`ccl quota --refresh`）也按厂商查退避 —— 手动刷新同样要遵守厂商级等待
- [x] 2.4 退避条目里记 **key 指纹**（key 的哈希，不存明文），**仅用于诊断日志**，不用于豁免（design D3：厂商限的是账号/出口 IP，换 key 躲不掉）
- [x] 2.5 单测：池 A 被 429 → 池 B（同 endpoint、不同 key）在退避期内**也被跳过**；池 C（不同 endpoint）**不受影响**

## 3. 退避降一级而非清零

- [x] 3.1 `internal/quota/refresh.go`：成功刷新时不再 `ClearBackoff`（清零），改为把该厂商的退避**降一级**（时长减半，下限为初始值 1 分钟；已在最小值则删除）
      —— 理由（来自日志）：13:59 已退避 4m，14:24 又从 1m 重新开始 —— 中间一次成功把退避清零了，而讯飞的限流是**速率型**的，一次成功不代表可以立刻恢复高频
- [x] 3.2 单测：退避 4m → 成功一次 → 变成 2m（不是 0）；连续成功 → 降到 1m 后删除

## 4. 验证

- [x] 4.1 `go build ./... && go vet ./... && go test ./...` 全绿；`gofmt -l .` 为空
- [x] 4.2 `openspec validate --all` 通过
- [x] 4.3 **变异验证**：把厂商间隔改成 0 → 对应测试必须红（否则测试没鉴别力）
- [x] 4.4 真机：升级后观察 `serve.log` 的「额度刷新一轮」——同厂商两次请求的间隔应 ≥ 10s；429 的退避应能在同厂商的另一个池上观察到（"未到期或退避跳过"）
