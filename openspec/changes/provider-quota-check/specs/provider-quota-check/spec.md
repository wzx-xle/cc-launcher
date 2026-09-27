## ADDED Requirements

### Requirement: ccl provider quota <name> queries a provider balance
`ccl provider quota <name>` MUST look up the named provider in `~/.cc-launcher/providers/<name>.json`, read its `ANTHROPIC_AUTH_TOKEN`, call the provider's balance API, and print a human-readable balance. Exit code 0 on success, non-zero on error.

#### Scenario: deepseek quota query succeeds
- **WHEN** user runs `ccl provider quota deepseek` and `~/.cc-launcher/providers/deepseek.json` has a valid `ANTHROPIC_AUTH_TOKEN`
- **THEN** stdout shows the currency, total balance, granted balance, topped-up balance; exit code 0

#### Scenario: provider name defaults to active provider
- **WHEN** user runs `ccl provider quota` (no name)
- **THEN** ccl uses the active provider from `state.json`; if no active provider, exits with "no provider specified and no active provider"

#### Scenario: provider not configured
- **WHEN** user runs `ccl provider quota nonexistent` and no `~/.cc-launcher/providers/nonexistent.json` exists
- **THEN** ccl exits with "提供商 nonexistent 不存在" error

### Requirement: deepseek uses GET /user/balance with Bearer auth
The DeepSeek checker MUST `GET https://api.deepseek.com/user/balance` with `Authorization: Bearer <token>` header. The response is parsed from the JSON into `Balance`.

#### Scenario: deepseek returns available balance
- **WHEN** the API responds 200 with `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.34","granted_balance":"10.00","topped_up_balance":"2.34"}]}`
- **THEN** `Balance.Currency="CNY"`, `Total="12.34"`, `Granted="10.00"`, `ToppedUp="2.34"`

#### Scenario: deepseek returns 401 unauthorized
- **WHEN** the token is invalid or expired
- **THEN** ccl exits with error containing "401" and the response body prefix; no balance printed

#### Scenario: deepseek returns non-200
- **WHEN** the API responds 5xx or 403
- **THEN** ccl exits with error containing the status code and body prefix

#### Scenario: deepseek returns multiple currencies
- **WHEN** the response has `balance_infos` with both `CNY` and `USD` entries
- **THEN** ccl prints both; primary view shows CNY (first array entry); `Note` mentions USD is also available

### Requirement: minimax uses coding_plan/remains with region-aware URL
The MiniMax checker MUST `GET https://api.minimax.{cn|io}/api/openplatform/coding_plan/remains` with `Authorization: Bearer <token>`. The `{cn|io}` is chosen by inspecting the provider config's `ANTHROPIC_BASE_URL`:
- contains `.cn` → `api.minimax.cn`
- contains `.io` → `api.minimax.io`

#### Scenario: minimax provider uses CN base URL
- **WHEN** `~/.cc-launcher/providers/my-minimax.json` has `ANTHROPIC_BASE_URL=https://api.minimax.cn/anthropic`
- **THEN** the checker calls `api.minimax.cn/api/openplatform/coding_plan/remains`

#### Scenario: minimax provider uses IO base URL
- **WHEN** the provider config has `ANTHROPIC_BASE_URL=https://api.minimax.io/anthropic`
- **THEN** the checker calls `api.minimax.io/api/openplatform/coding_plan/remains`

#### Scenario: minimax base URL is malformed
- **WHEN** the provider config has `ANTHROPIC_BASE_URL=https://other-host.example/anthropic`
- **THEN** the checker returns error "cannot determine minimax region from BASE_URL"; ccl prints it

### Requirement: unimplemented providers return a clear message
When user queries a provider that has a registered `tpl` but no `quota.Checker` implementation, `ccl provider quota <name>` MUST return an error listing which providers ARE supported and pointing to the docs for the rest.

#### Scenario: querying glm returns "not yet implemented"
- **WHEN** user runs `ccl provider quota glm`
- **THEN** ccl exits with: `glm 余额检查尚未实现；目前支持 deepseek、minimax；glm/aliyun/anthropic 待后续扩展（见 docs/discussions/）`

#### Scenario: querying anthropic returns "see web console"
- **WHEN** user runs `ccl provider quota anthropic`
- **THEN** ccl exits with: `anthropic 无官方 API 查询余额；请在 https://console.anthropic.com 查看`

### Requirement: HTTP client has a 5-second timeout
Every provider checker MUST use an HTTP client with `Timeout: 5 * time.Second`. The context passed to `Check(ctx, ...)` MUST also have a 5s deadline.

#### Scenario: provider endpoint is unreachable
- **WHEN** the provider endpoint does not respond within 5s (DNS timeout, network blackhole)
- **THEN** ccl exits with error containing "timeout after 5s" and the endpoint URL

### Requirement: error messages never echo the API key
The error message MUST NOT contain the raw `ANTHROPIC_AUTH_TOKEN` value. It MAY contain the token's length (e.g., "token length=35") to help users debug.

#### Scenario: 401 error does not leak token
- **WHEN** the API returns 401
- **THEN** the printed error contains "401" and "token length=35" but NOT the token string itself

### Requirement: balance output is single-line readable
For each balance row, ccl MUST print at minimum: currency, total, granted, topped-up, fetched-at (local time). One line per currency.

#### Scenario: ccl provider quota deepseek output format
- **WHEN** user runs `ccl provider quota deepseek` and the API returns CNY balance 12.34
- **THEN** stdout contains a row like `CNY  total=12.34  granted=10.00  topped_up=2.34  fetched_at=2026-09-27T...`