## 1. quota 包基础

- [ ] 1.1 新增 `internal/quota/result.go`：`Balance` struct + `Checker` interface + `ErrNotImplemented` / `ErrAuth` / `ErrTimeout` / `ErrHTTP` 哨兵 error
- [ ] 1.2 新增 `internal/quota/registry.go`：`var checkers = map[string]Checker{}`，`Lookup(name string) (Checker, bool)`，`Register(c Checker)`，init() 自动注册
- [ ] 1.3 `quota_test.go`：registry 查找 hit / miss / not-implemented 三条路径

## 2. DeepSeek 实现

- [ ] 2.1 `internal/quota/deepseek.go`：`GET https://api.deepseek.com/user/balance`，Authorization: Bearer $ANTHROPIC_AUTH_TOKEN
- [ ] 2.2 解析响应为 `Balance{Currency, Total, Granted, ToppedUp, RawJSON, FetchedAt}`；多 currency 时返回首个 + 把其它写 `Note`
- [ ] 2.3 401 / 403 / 5xx → 对应哨兵 error
- [ ] 2.4 `deepseek_test.go`：用 `httptest.Server` mock 200 / 401 / 5xx 三种响应

## 3. MiniMax 实现

- [ ] 3.1 `internal/quota/minimax.go`：从 env 读 `ANTHROPIC_BASE_URL`，判断 `.cn` 或 `.io`
- [ ] 3.2 调对应域名的 `/api/openplatform/coding_plan/remains`
- [ ] 3.3 URL 解析失败返回明确 error（"cannot determine minimax region from BASE_URL"）
- [ ] 3.4 实现前必须 fetch 原页验证 endpoint 路径、auth、响应 schema；若与预期不一致，update spec 与设计
- [ ] 3.5 `minimax_test.go`：mock 两种 region + URL 解析失败

## 4. 兜底实现（5 家）

- [ ] 4.1 `internal/quota/glm.go` / `aliyun.go` / `anthropic.go`：实现 `Name()` + `Check()` 返回 `ErrNotImplemented` + 有用消息
- [ ] 4.2 anthropic 的 Check 返回"无官方 API，请到 console 查看"消息（与 spec 一致）

## 5. CLI 接入

- [ ] 5.1 `internal/cli/cli.go` `runProvider()`：识别 `--quota` 旗标
- [ ] 5.2 `--quota` 无参数 → 用 active provider；否则用给定 name
- [ ] 5.3 provider 不存在 → 报错退出非 0
- [ ] 5.4 `quota.Lookup` miss → 报错"X 余额检查尚未实现..."
- [ ] 5.5 格式化输出（表格或 key=value 行）
- [ ] 5.6 单元测试：CLI 调 `--quota` 三条路径（active / 指定 / 未实现）

## 6. 验证

- [ ] 6.1 `go test ./...` 全绿
- [ ] 6.2 `GOOS=windows go build ./...` 通过
- [ ] 6.3 手动：本机 DeepSeek key 跑 `ccl provider quota deepseek` 出真实余额
- [ ] 6.4 手动：`ccl provider quota glm` 出"未实现"消息
- [ ] 6.5 手动：删 ANTHROPIC_AUTH_TOKEN 后跑 → 出错信息不泄露 token

## 7. 提交

- [ ] 7.1 commit: feat(quota): internal/quota 包 + DeepSeek + MiniMax + 兜底实现
- [ ] 7.2 commit: feat(cli): ccl provider --quota 子命令
- [ ] 7.3 commit: test(quota): mock httptest 覆盖 200 / 401 / 5xx
- [ ] 7.4 commit: docs: "提供商余额" 小节在 README