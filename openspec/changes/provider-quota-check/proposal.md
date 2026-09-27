## Why

ccl-launcher 现在能启动 Claude Code，但对"账号还剩多少"完全无感知。**用量监控**（sibling change `usage-monitoring-via-otel`）解决了"花了多少"问题；本 change 解决"还剩多少"问题。

两个问题是**完全不同**的数据源：
- **用量**：Claude Code 主动推（OTel）
- **余额**：每家 provider 自家的 REST API

本次**只做 DeepSeek + MiniMax**，理由：
- DeepSeek `/user/balance` 文档清晰、原页已 fetch 验证
- MiniMax coding_plan 余额 API 已在 Claude Code 接入文档里提及（`coding_plan/remains` 路径），可立即用
- glm / aliyun / anthropic 余额 API 行为**未充分验证**（阿里云百炼 `/api/v2/usage` 字段不全、Anthropic 无官方 API、智谱 coding_plan 余额 endpoint 待 fetch），留待后续 change 单独扩展

## What Changes

- **新增 `internal/quota` 包**：定义 `type Checker interface { Check(ctx, env map[string]any) (Balance, error) }`；每家一个文件
- **新增 `internal/quota/deepseek.go`**：`GET https://api.deepseek.com/user/balance`，Authorization: Bearer $ANTHROPIC_AUTH_TOKEN，解析 `balance_infos[]` → 标准 `Balance`
- **新增 `internal/quota/minimax.go`**：`GET https://api.minimax.cn/api/openplatform/coding_plan/remains`（国内）或 `api.minimax.io/...`（国际），按账号地域选；解析为 `Balance`
- **新增 `internal/quota/result.go`**：`type Balance struct { Currency string; Total string; Granted string; ToppedUp string; RawJSON string; FetchedAt time.Time }`
- **`internal/cli/cli.go` 扩展**：`ccl provider quota <name>` 子命令；`<name>` 留空时查 active provider
- **不动 usage-monitoring**：`ccl provider quota` 与 `ccl stats` 是独立命令、不同数据源

## Capabilities

### New Capabilities
- `provider-quota-check`: 统一的 `quota.Checker` 接口 + DeepSeek / MiniMax 两个实现；`ccl provider quota <name>` CLI；4 家 + anthropic 共 5 种 type，但只有 deepseek + minimax 真正实现，其它返回"未实现"

### Modified Capabilities
<!-- 无 -->

## Impact

- 代码：新增 `internal/quota` 包 + `cli.go` 加 quota 子命令
- 依赖：无新增（用 `net/http` + `encoding/json` 标准库）
- 隐私：调 provider API 必须发 `ANTHROPIC_AUTH_TOKEN`；这是业内标准做法（DeepSeek 文档明确）；localhost 调用、不出机器
- 失败行为：网络错误 / 401 / 解析失败 → 返回错误信息给用户，**不静默吞掉**
- 兼容性：纯新增 CLI 子命令；现有调用不受影响

## What is NOT in this change

明确**不做**：
- 智谱 GLM 余额 API（待 fetch 原页验证）
- 阿里云百炼 `/api/v2/usage`（字段不全、待补 spec）
- Anthropic 官方（无官方 API，只给控制台链接）
- 把 quota 结果缓存到 SQLite（按需查询即可，无需持久化）
- 把 quota 结果推送 OTel（独立维度，下一版再考虑）

这些等后续单独 change 扩展。