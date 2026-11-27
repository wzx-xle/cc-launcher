## Why

**火山方舟 Agent Plan 个人版的额度现在完全看不见。** 本机已经在用方舟 Agent Plan（provider `ark-deepseek-flash`，`base_url` = `https://ark.cn-beijing.volces.com/api/plan`），但额度只在方舟控制台里能看到——ccl 的状态栏只能显示 Anthropic 自己的 `rate_limits`，对方舟套餐**一个字都不说**。套餐的额度窗口（5 小时 / 周 / 月）一旦耗尽，表现是"接口开始报错"，而用户看不出自己离重置还有多久、是不是已经用完了。

**方舟提供了额度查询接口，但它要另一套凭证。** `GetAFPUsage`（个人版套餐额度）返回各窗口的 `Quota` / `Used` / `ResetTime`，但**只接受 Access Key 签名鉴权**——本机 provider 那个 `ark-…` 的 API Key 打过去实测返回 `InvalidAuthorization`（HTTP 400）。这意味着适配它要多实现一套火山引擎 V4 签名，并给凭证另开一条通道。

**这条通道是通用的，不是为方舟开的后门。** Access Key 鉴权 + 签名头是火山引擎全线管控面 API 的统一方式；把签名做成一个独立、可单测的原件，比把签名字符串塞进某个 adapter 里更经得起厂商改协议。

## What Changes

- **新增 `ark` 额度 adapter**：调用 `POST https://ark.cn-beijing.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01`，输出**三个窗口维度**（5h / 周 / 月）——接口返回的第四个窗口（日额度）只计图片/语音模型与 Harness 能力，实测对文本模型推理**恒为 0**
- **新增火山引擎 V4 签名**（`internal/quota/arksign.go`）：只用标准库，无新增依赖；实现已与官方 `volcengine-go-sdk` 的签名输出**逐字节比对通过**，并已用真实 STS 凭证打出 HTTP 200
- **`quota` 块新增两个键**：`accessKey` / `secretKey`（ccl 自己的元数据，**不透传**给 Claude Code）；缺省时回退到进程环境变量 `VOLCENGINE_ACCESS_KEY_ID` / `VOLCENGINE_SECRET_ACCESS_KEY`
- **额度池改按 Access Key 划分**：方舟的 AFP 额度按**账号套餐**分，同一对 AK 下的多个 `ark-…` key 共用一个池——现有的"按 API key 分池"会让同一份额度被重复查询、重复存储
- **只做个人版**：企业版席位走另一个接口（`GetSeatAFPUsage`，需传 `SeatIDs`），不在本次范围内

## Capabilities

### New Capabilities

（无——这是既有额度能力的厂商扩展）

### Modified Capabilities

- `provider-quota`: 新增火山方舟 Agent Plan 额度接口的读取要求（三个窗口维度、`Quota`/`Used` 兼容 number 与 string 两种形态、Access Key 签名鉴权、凭证的来源与优先级）；额度池的划分依据从"一律按 API key"放宽为"厂商可自带池键"

## Impact

**新增**

- `internal/quota/arksign.go` —— 火山引擎 V4 签名（canonical request / stringToSign / 密钥派生链）
- `internal/quota/adapter_ark.go` —— 方舟 adapter（端点推导、请求、响应解析、窗口 → 维度）
- `internal/quota/arksign_test.go` / `internal/quota/adapter_ark_test.go` —— 签名黄金向量 + 响应解析单测

**修改**

- `internal/quota/adapter.go` —— `ProviderInfo` 增加 `AccessKey` / `SecretKey`；adapter 注册表加一行
- `internal/quota/refresh.go` —— `Config` 增加 `accessKey` / `secretKey` 解析 + 进程环境兜底；额度池键支持厂商自带
- `docs/provider-quota.md` —— 补方舟 adapter 的实测契约（接口、签名、字段类型、失败形态）

**行为变更**

- 配了 `quota` 块的 provider 若同时被别的 adapter 识别，端点解析顺序不变（显式 `source` 优先）
- 未配置 Access Key 的方舟 provider：额度查询失败并保留旧条目，**不影响 Claude Code 启动**

**不涉及**

- 不引入 `volcengine-go-sdk` 依赖（签名已自研验证等价）
- 不改 Claude Code 的任何文件、不改 daemon 的 telemetry schema
- 不读会话内容、不碰方舟的推理接口（只调管控面的额度查询）
