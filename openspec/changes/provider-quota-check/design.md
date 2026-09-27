## Context

每个 provider 都有"余额查询"机制，但 API 不统一：
- **DeepSeek**：`GET /user/balance`，Authorization Bearer，CNY/USD 双币种余额
- **MiniMax**：`GET /api/openplatform/coding_plan/remains`，coding plan 专用
- 智谱 / 阿里云 / Anthropic：API 行为待验证，本次不做

ccl-launcher 需要一个统一接口，每个 provider 一个文件实现。

## Goals / Non-Goals

**Goals:**
- `ccl provider quota deepseek` 一条命令查余额
- 不同 provider 用不同 endpoint，但 CLI 输出格式统一
- 单次调用 ≤ 5s 超时（个人工具，不能卡住）

**Non-Goals:**
- 不做缓存（按需查就够）
- 不做定时轮询（用户主动触发）
- 不做 quota 用尽告警（独立 feature）
- 不接 anthropic / glm / aliyun（API 行为未验证，留后续 change）

## Decisions

### 1. 统一 `Checker` 接口

```go
package quota

type Balance struct {
    Currency  string    // "CNY" / "USD" / ...
    Total     string    // 总可用余额（用户最关心）
    Granted   string    // 未过期赠送
    ToppedUp  string    // 已充值
    RawJSON   string    // 原始响应，方便用户调试
    FetchedAt time.Time // 抓取时间
    Note      string    // 额外说明（如 "coding plan expires 2027-01-01"）
}

type Checker interface {
    Name() string                                          // "deepseek"
    Check(ctx context.Context, env map[string]any) (*Balance, error)  // env 是 provider 配置
}
```

`Name()` 返回内部 type 字符串（"deepseek" / "minimax"），与 `ccl provider --template` 用的 name 一致。

**理由**：接口统一后 cli 包只调一次，registry 按 name 找实现。

### 3. token 复用 ANTHROPIC_AUTH_TOKEN

`providers/deepseek.json` 里存的 `ANTHROPIC_AUTH_TOKEN` 就是 DeepSeek API key（DeepSeek 官方文档确认）。ccl 直接复用，**不为 quota 单独存 token**。

**理由**：避免冗余；token 跟 provider 配置走。

### 4. 余额单位保留字符串

DeepSeek 返回 `"12.34"`（string），不是 float。BigDecimal 精度问题：JSON 不该假设是 float。

**理由**：如实存原值；显示时再格式化。

### 5. 5s 超时

HTTP client 设 `Timeout: 5 * time.Second`；context 也加 5s 超时作双保险。

**理由**：个人 CLI 工具，5s 已经够久；超时即报错不阻塞用户。

### 6. 错误信息要可定位

错误包含：
- HTTP 状态码（如 `401 Unauthorized`）
- 响应 body 前 500 字符（方便用户对照 API 文档）
- token 长度（用户可判断是不是 token 错了，但不泄露 token 本身）

**理由**：用户跑一次就能定位问题（token 错？URL 错？网络错？）。

### 7. 未实现的 provider 返回明确错误

`ccl provider quota glm` 现在调用 `quota.Checker("glm")` → 没有实现 → 返回"glm 余额检查未实现；DeepSeek + MiniMax 已支持；后续扩展见 [issue/文档]"

**理由**：不静默 return 零，不假装支持；用户知道边界。

### 8. MiniMax 国内外 URL 选择

`providers/minimax.json` 里 BASE_URL 是国内（`api.minimax.cn/anthropic`）还是国际（`api.minimax.io/anthropic`）—— ccl 自动根据 URL 后缀判断：
- 含 `.cn` → 调 `api.minimax.cn/api/openplatform/coding_plan/remains`
- 含 `.io` → 调 `api.minimax.io/api/openplatform/coding_plan/remains`

**理由**：与 interactive guided flow 里的 cn/io 选择一致；不引入新概念。

## Risks / Trade-offs

- [token 通过 env 传出] Claude Code 写 settings.json 时存的是 `ANTHROPIC_AUTH_TOKEN`；quota 读它发到 provider。风险 = 设置文件被读。缓解：用户已有这个信任（Claude Code 也会发）。
- [balance 字符串含不可见字符] 罕见，但 UI 显示可能乱码。缓解：trim + 校验。
- [DeepSeek 返回 string 而不是 float] 显示时要 parse。缓解：用 `strconv.ParseFloat` 包 try-error。
- [MiniMax 余额 endpoint 未亲自 fetch 原页验证] 实现时必须先 fetch；本设计预留 URL，但 spec 标 "需 fetch 验证"。

## Migration Plan

无状态迁移。纯新增子命令。

## Open Questions

- 用户给 quota check 加 cache 吗？决定：不做，每次都现查（个人工具，频率低）
- 是否把 quota 接进 OTel？决定：下一版
- 余额耗尽提醒？决定：留给后续
- claude.go 接受 env map 参数（与 `provider.CreateWithEnv` 对齐）？决定：是