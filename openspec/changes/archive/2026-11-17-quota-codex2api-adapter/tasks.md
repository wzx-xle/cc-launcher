## 1. Adapter（先写测试）

- [x] 1.1 `internal/quota/adapter_test.go`：先写 codex2api 的单测（红）——
  - 端点推导：`http://49.51.34.139:8317` → `http://49.51.34.139:8317/api/key-usage/summary`；base_url 带尾斜杠 → 不出双斜杠；base_url 带路径前缀 → 前缀保留；空 base_url → 推导失败
  - 识别：`Match()` 对任何 host 都返回 false；`DetectAdapter` 不认得 codex2api 的 host
  - 解析：响应 `quota_used: 0.8024772` / `quota_limit: 100` → 维度 used=0.8024772、limit=100、unit=USD、kind=quota
  - 无限额度：`quota_limit: 0` → `Unlimited=true`、不派生百分比、limit 为 nil
  - 口径隔离：`total_used: 12.5` 不进 used，只进 `Raw`
  - 失败：HTTP 404 → 报错；body 解析失败 → 报错（且都**不**返回维度）
- [x] 1.2 `internal/quota/adapter_codex2api.go`：实现 `codex2apiAdapter`——
  - `Name()` = `"codex2api"`；`Match()` 恒 false
  - `Endpoint()`：`trimTrailingSlash(baseURL) + "/api/key-usage/summary"`，空 base_url 返回 false
  - `Fetch()`：`fetchJSON`（复用现成的：Bearer 头 + 1MB 上限 + 429 → `RateLimitError`）→ 解析 `key` 对象 → 组装一个 `KindQuota` 维度 → `Finalize()`
  - 维度：`ID="quota"`、`Unit="USD"`（可被 `displayUnit` 覆盖）、`Used=quota_used`、`Limit=quota_limit`（>0 时）、`Remaining=limit-used`（>0 时）、`Unlimited=(quota_limit==0)`
  - `Raw`：保留 `name`/`status`/`quota_limit`/`quota_used`/`total_used`/`reset_count`/`last_reset_at`/`created_at`（排查口径用）
  - **不填 `ResetsAt`**（"周期"由管理员手动重置决定，下次重置时间不可知）
- [x] 1.3 `internal/quota/adapter.go`：把 `codex2apiAdapter{}` 加进 `adapters` 注册表

## 2. 文档

- [x] 2.1 `docs/provider-quota.md`：补 codex2api 一节——端点、鉴权、响应形状、**字段口径**（`quota_used` = 当前配额周期内累计 / `total_used` = 历史累计 / `quota_limit=0` = 无限）、404 的两种含义（公开用量页关闭 / accounts 权限未开）、以及"**流量窗口没有分母，不映射成额度维度**"的结论（改 adapter 前先看它）

## 3. 验证

- [x] 3.1 `go build ./... && go vet ./... && go test ./...` 全绿；`gofmt -l .` 为空
- [x] 3.2 `openspec validate --all` 通过
- [x] 3.3 **变异验证**：把 adapter 里的 `quota_used` 换成 `total_used` → 对应测试必须红（否则测试没鉴别力）

## 4. 本机落地（**不进仓库**）

- [x] 4.1 `~/.cc-launcher/providers/gpt-6-bqshao3.json`：加 `"quota": {"source": "codex2api"}`
- [x] 4.2 实测：`ccl quota --refresh` → `ccl quota` 表格 / `--json` 输出里出现 codex2api 的维度，且 `source` 为 `codex2api`
- [x] 4.3 记录实测结论：这个 key 的 `quota_limit=0`（无限额度）→ 状态栏当前渲染成 `--`（消费方渲染的事，见 design 的 Open Questions），要么服务端给 key 配金额上限，要么状态栏脚本加 fallback
