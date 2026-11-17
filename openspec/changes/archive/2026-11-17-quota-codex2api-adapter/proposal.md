## Why

本机新增了一个 provider（`gpt-6-bqshao3`，"邵宝泉-公司Codex账号"），它指向的是一台自建的 **codex2api** 服务（`http://49.51.34.139:8317`，Go+Gin，v3.1.6）—— 把公司的 ChatGPT Codex 账号池转成 OpenAI/Anthropic 兼容 API。

ccl 目前内置三个额度 adapter（minimax / deepseek / newapi），都不认这个服务 → 该 provider **查不到任何额度**，状态栏上这个 provider 的额度段是空的，用户看不出"这个 key 用了多少、花了多少钱"。

codex2api 本身提供了面向 API Key 持有者的只读自用量接口（`GET /api/key-usage/summary`，Bearer 鉴权），实测可用，正好能补上这一块。

## What Changes

- **新增 codex2api 额度 adapter**（内部 `source: "codex2api"`），读取 `GET <base_url>/api/key-usage/summary`：
  - 认证：把 provider 的 API Key 当 Bearer token 发送（与 codex2api 公开用量页同一套鉴权）。
  - 端点推导：`<base_url>` 去掉尾斜杠 + `/api/key-usage/summary`（base_url 里的路径前缀会保留，反代到子路径的部署也能用）。
  - **识别靠显式配置**：codex2api 跑在任意 host 上，无法靠 host 识别（跟 New API 同理）→ provider 配置里写 `"quota": {"source": "codex2api"}` 才启用；`Match()` 恒为 false。
- **输出一个额度维度**（key 级金额预算）：`quota_used` = 已用金额（USD），`quota_limit` = 总额度（USD）。
  - `quota_limit` 为 0 = **无限额度** → 维度标记为 unlimited，**不派生已用百分比**（没有分母）。
  - `quota_limit` > 0 时 → 已用 / 总额 / 剩余三个量齐备，状态栏能画条形图。
- **降级不崩**：接口 404（服务端关闭公开用量页）、字段改名、解析失败 → 该次刷新失败，**保留上一次的额度条目**（自动变 stale），**绝不影响 Claude Code 启动**。429 走既有的退避框架。

非破坏性：命令行接口、配置格式（只新增一个可选的 `quota` 块）、数据文件格式、`ccl quota --json` 的对外结构都不变；对外只是多了一个 `source` 取值。

## Capabilities

### New Capabilities

（无 —— 不引入新能力，只给既有的额度机制加一个厂商。）

### Modified Capabilities

- `provider-quota`: 新增需求 "ccl reads a codex2api quota interface" —— 规定该厂商的端点推导、鉴权方式、成败判据、维度口径（`quota_used` / `quota_limit`）与无限额度的处理。

## Impact

- **改动**：
  - `internal/quota/adapter_codex2api.go`（新增）：adapter 实现。
  - `internal/quota/adapter.go`：内置 adapter 注册表加一行。
  - `internal/quota/adapter_test.go`：端点推导 / 识别 / 解析 / 无限额度 / 失败降级的单测。
  - `docs/provider-quota.md`：补 codex2api 的实测结论（端点、响应形状、字段语义）。
  - provider 配置（本机 `~/.cc-launcher/providers/gpt-6-bqshao3.json`）：加 `"quota": {"source": "codex2api"}`。
- **对外契约**：不变（`ccl quota --json` 的 `dimensions` 结构、`source` 字段语义都不变，只是多一个取值）。
- **无依赖变更**，无 schema 变更，不新增可执行文件。
