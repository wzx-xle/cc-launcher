## 1. 火山引擎 V4 签名（internal/quota/arksign.go）

- [x] 1.1 新建 `internal/quota/arksign.go`：定义签名所需的输入（AK / SK / service / region / host / method / path / query / headers / body / 时间）与输出（`Authorization` 头、`X-Date`、`X-Content-Sha256`）。**只用标准库**（`crypto/hmac`、`crypto/sha256`、`encoding/hex`、`net/url`、`sort`、`strings`），**不引 `volcengine-go-sdk`**
- [x] 1.2 实现 canonical request 拼接：`method \n path \n query \n canonicalHeaders \n signedHeaders \n bodyHash`。**canonicalHeaders 段自带结尾换行，其后还要再接一个 `\n`**（即 headers 段与 signed headers 段之间有一个空行）——写错这一处签名全错
- [x] 1.3 实现 canonical query：`url.Values.Encode()` 后把 `+` 换成 `%20`（`Encode()` 用 `+` 表示空格，签名要求 `%20`）
- [x] 1.4 **按官方规则动态确定参与签名的头**：`Content-Type` / `Content-Md5` / `Host` / `X-Security-Token` **以及所有 `X-` 前缀的头**参与，其余不参与；头名小写后按 ASCII 排序；值两侧去空白。**不要写死四个头**——将来多发一个 `X-` 头就会漏签，服务端 403 且报错完全看不出是漏签
- [x] 1.5 签名实现支持**可选的 session token**：给了就发 `X-Security-Token` 头**并让它参与签名**（STS 临时凭证必须这样，否则 401 `InvalidSecretToken`）
- [x] 1.6 实现密钥派生链：`kDate=HMAC(SK, ShortDate)` → `kRegion=HMAC(kDate, region)` → `kService=HMAC(kRegion, service)` → `kSigning=HMAC(kService, "request")`；`ShortDate` 是 `X-Date` 的**前 8 位**（`20261127`），不是完整时间戳
- [x] 1.7 实现 `Authorization` 头：`HMAC-SHA256 Credential={AK}/{ShortDate}/{region}/{service}/request, SignedHeaders={h1;h2;...}, Signature={hex}`。**Credential 是单斜杠**（写成 `AK//20261127/...` 会 403）
- [x] 1.8 `X-Date` 用 **UTC** 格式 `20060102T150405Z`；`X-Content-Sha256` = body 字节的 sha256 十六进制（空 body 用 sha256(空串) = `e3b0c442…b855`）
- [x] 1.9 **写单测（黄金向量）**：硬编码 `AK=AKTESTKEY` / `SK=SKTESTSECRET` / `X-Date=20261127T032146Z` / body `{}` / host `ark.cn-beijing.volcengineapi.com`，断言 `Authorization` 整串等于
      `HMAC-SHA256 Credential=AKTESTKEY/20261127/cn-beijing/ark/request, SignedHeaders=content-type;host;x-content-sha256;x-date, Signature=17647640ac5545aa9c6d4990d0059db3ff19d4a098411b9381157953d62f78a5`
      （该向量由官方 `volcengine-go-sdk v1.0.260` 的 `base.Credentials.Sign()` 产出，已比对通过）。**这条测试是签名实现的全部外部契约**——它红了就意味着签名错了
- [x] 1.10 单测再覆盖：多一个 `X-Foo` 头时它**必须**进入 signed headers（护栏：防止后来者把 1.4 改成写死列表）；query 含需转义的字符；空 body
- [x] 1.11 **第二条黄金向量（带 session token）**：用固定 session token 与固定 `X-Date` 断言整串 `Authorization` 等于
      `HMAC-SHA256 Credential=AKTESTKEY/20261127/cn-beijing/ark/request, SignedHeaders=content-type;host;x-content-sha256;x-date;x-security-token, Signature=<用固定 X-Date 算出的值>`
      ——**注意 `X-Date` 必须是固定时刻**（签名依赖它），实施时选一个固定 UTC 时刻（如 `20261127T053922Z`）用官方 SDK 重算一次并把结果硬编码进测试

## 2. 凭证通道（quota 块 + 环境变量兜底）

- [x] 2.1 `internal/quota/refresh.go` 的 `Config` 加 `AccessKey` / `SecretKey` 两个字段
- [x] 2.2 `ParseConfig` 解析 `quota` 块里的 `accessKey` / `secretKey`（字符串，类型错 → error，与其它字段同样处理）
- [x] 2.3 `Config` 加一个取值方法：两个字段都空时回退读进程环境 `VOLCENGINE_ACCESS_KEY_ID` / `VOLCENGINE_SECRET_ACCESS_KEY`（**只在这两个都缺时读环境**；有一个有一个没有 → 视为配置错误并报出来，不要用一半配一半）
- [x] 2.4 `internal/quota/adapter.go` 的 `ProviderInfo` 加 `AccessKey` / `SecretKey` 两个字段（adapter 层不直接读环境，也不直接读 provider 配置——那些由 refresh 层填好，形态跟 `QuotaPerUnit` / `DisplayUnit` 一致）
- [x] 2.5 `ProviderRef.providerInfo` 把 `Config` 里的凭证填进 `ProviderInfo`
- [x] 2.6 **护栏测试**：provider 的 `env` 块里即使有 `VOLCENGINE_ACCESS_KEY_ID` 之类的键，**也不得**被当作凭证来源（`env` 块会原样交付给 Claude Code）
- [x] 2.7 护栏测试：环境变量兜底生效；只配了 access key 没配 secret key → 报错而不是"用一半"

## 3. 额度池按 Access Key 划分

- [x] 3.1 `internal/quota/adapter.go` 加**可选**接口 `PoolKeyer`（`PoolKey(p ProviderInfo) string`）——**不要改 `Adapter` 主接口**（改了 4 个已有 adapter 全要动）
- [x] 3.2 `internal/quota/refresh.go`：算池键时用类型断言取 `PoolKeyer`，取不到就回退 `p.APIKey`。**三个 `PoolID(...)` 调用点**（`refresh.go:662` / `:926` / `:1029`）必须走同一条取值路径——用 LSP `findReferences` 确认，不要 grep
- [x] 3.3 `internal/quota/adapter_ark.go` 实现 `PoolKey`：返回 Access Key（方舟的 AFP 额度按**账号套餐**分，同 AK 下多个 `ark-…` key 共用一个池）
- [x] 3.4 护栏测试：同 AK、不同 API key 的两个 provider → **只发一次请求**，两个 provider 记录到同一个池；既有 adapter（minimax 等）行为不变（仍按 API key 分池）
- [x] 3.5 护栏测试：**池键不明文落盘**（数据文件里只有池键的哈希，与现有 `PoolID` 口径一致）

## 4. 方舟 adapter（internal/quota/adapter_ark.go）

- [x] 4.1 定义 adapter：`Name() = "ark"`；`Match(baseURL)` 要求 host 形如 `ark.<region>.volces.com` **且** path 含 `/api/plan`（**只看 host 不行**——按量部署是同 host 的 `/api/v3`，没有套餐）
- [x] 4.2 `Endpoint(baseURL)`：从 host 的 `ark.<region>.volces.com` 取 region，推导 `https://ark.<region>.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01`。**管控面 host 与数据面 host 不同**（`volcengineapi.com` vs `volces.com`），不能换后缀了事。region 解析不出 → 返回 false，让用户显式配 `quota.endpoint`
- [x] 4.3 `Fetch`：POST 到端点，body 固定 `{}`，`Content-Type: application/json`，用 `arksign.go` 加上 `X-Date` / `X-Content-Sha256` / `Authorization`。**HTTP 客户端走 `newHTTPClient(p.Env)`**（代理设置来自 provider 的 env 块）
- [x] 4.4 解析响应：`Result.PlanType` + 窗口对象。**`Quota` / `Used` 实测是 JSON number**（`10000` / `328.5542`），**但官方文档写的是 string** → 用自定义反序列化类型**两种都吃**：字段缺失 / `null` / 空串 / 解析失败 → 该字段**未知**（`ok = false`），**不返回 error**（字段级非法只丢那一个维度，不该放大成"整个响应解析失败"）
- [x] 4.5 **三个维度**（`AFPDaily` 不报——实测它对文本模型推理恒为 0，日额度只计图片/语音模型与 Harness 能力）：`AFPFiveHour` → ID `five_hour` / Duration 5h；`AFPWeekly` → `weekly` / 7d；`AFPMonthly` → `monthly` / 30d。`Kind = KindWindow`，`Unit = "AFP"`（label 由 `Finalize()` 从 Duration 派生 → `5h`/`7d`/`30d`）
- [x] 4.6 `Limit = Quota`、`Used = Used`、`Remaining = Quota - Used`（**夹到 0**，超了就是没剩不是负数）、`ResetsAt = ResetTime`（epoch 毫秒）
- [x] 4.7 `Quota == 0` → `Unlimited = true`，不派生百分比、不报总额（与 codex2api 的 `quota_limit == 0` 同款处置）
- [x] 4.8 `PlanType` 进 `Raw`（排查用）
- [x] 4.9 失败形态：无 `Result` → 失败；HTTP 429 → `RateLimitError`（进既有退避框架）；HTTP 400/403 → 失败且**错误信息带上 `ResponseMetadata.Error.Code` 与 `Message`**（区分"凭证不对"和"签名被拒"和"网络不通"——签名依赖本机时钟，笼统报"查询失败"会让用户查错方向）
- [x] 4.10 **错误信息里绝不能出现 AK / SK**（加护栏测试）
- [x] 4.11 单测（mock HTTP server）：三个窗口 → 三个维度且数值正确；`Quota`/`Used` 分别是 **number** 与 **string** 两种形态都能解；非法 `Quota` → 丢该维度但保留其余两个；`Quota=0` → unlimited；无 `Result` → 失败且旧条目保留；429 → RateLimitError；错误信息含厂商的 Code
- [x] 4.12 单测：用**实测的真实响应样本**做夹具（`PlanType: medium`、`AFPFiveHour{Quota:10000, Used:328.5542, SubscribeTime:1795748093000, ResetTime:1795766093000}`、`AFPWeekly{35000/328.5542}`、`AFPMonthly{100000/328.5542}`、`AFPDaily{50000/0}`）→ 断言产出 5h/7d/30d 三个维度且**没有** daily 维度

## 5. 接线与注册

- [x] 5.1 `internal/quota/adapter.go` 的 `adapters` 注册表加 `arkAdapter{}`
- [x] 5.2 `internal/quota/refresh.go`：`Resolve` 那条路径确认 `source: "ark"` 能选到新 adapter（现有逻辑按 `AdapterByName` 查，应无需改动，但**要跑测试确认**）
- [x] 5.3 确认 `provider.ReservedKeys` **仍然**包含 `"quota"`——quota 块新增的 `accessKey`/`secretKey` 是**块内部**的键，不影响顶层保留键清单；但要确认 quota 块整体仍不透传给 Claude Code
- [x] 5.4 `cmd/cc-launcher/providerconfig.go`：确认 quota 块解析失败只警告不阻断的既有行为对新字段同样成立（凭证字段类型错 → 警告 → 该 provider 额度查询不可用 → **不影响 Claude Code 启动**）

## 6. 文档

- [x] 6.1 `docs/provider-quota.md`：新增一节写方舟 adapter 的**实测契约**——接口地址与入参、窗口字段、**`Quota`/`Used` 实测是 number（文档写 string）→ 两种都吃**、签名方案与黄金向量、错误码（`InvalidCredential` 100025 / `InvalidAuthorization` 100024 / `InvalidSecretToken` 100026 均为实测）、**`AFPDaily` 对文本模型恒为 0 所以不报**、只适用个人版（企业版走 `GetSeatAFPUsage`）
- [x] 6.2 `docs/provider-quota.md` 补凭证配置说明：`quota.accessKey` / `quota.secretKey` 优先，环境变量 `VOLCENGINE_ACCESS_KEY_ID` / `VOLCENGINE_SECRET_ACCESS_KEY` 兜底，**以及"改了环境变量要重启 `ccl serve`"这条代价**（daemon 是常驻进程）
- [x] 6.3 `docs/provider-quota.md` 写明**安全建议**：用只授方舟管控面读权限的 **IAM 子账号**，不要用主账号 AK（查额度是纯只读需求，主账号 AK 是账号全权）
- [x] 6.4 `docs/provider-quota.md` 补 AFP 单位说明：AFP = Agent Fuel Point，`1 AFP ≈ 0.01 元`（按量口径）；**1 AFP 不是固定 token 数**（抵扣系数随模型变，`deepseek-v4.1-flash` 系数 2.5、`deepseek-v4-flash` 0.5）→ 不换算成 token 显示
- [x] 6.5 补月度维度 30d 是**近似**的说明（真实规则是"每订阅月第 1 日重置"，订阅月不是固定 30 天；准确的重置时刻由 `ResetTime` 承载）

## 7. 收尾验证

- [x] 7.1 `go build ./...` 与 `go test ./...` 全绿（含新增的 `arksign` / `adapter_ark` 用例）
- [x] 7.2 **真机验证**：签名实现与响应形状**已用 arkcli 的 STS 临时凭证验证通过**（HTTP 200，2026-11-27，见 design.md「实测记录」）。**仍需用长期 AK/SK 再打一次**——STS 与长期 AK 的权限模型可能不同，长期凭证才是 ccl 实际要用的
- [x] 7.3 端到端：给本机 provider `ark-deepseek-flash` 的配置加上 `"quota": {"source": "ark", "accessKey": "...", "secretKey": "..."}`，跑 `ccl quota --json`，确认输出三个维度（`5h`/`7d`/`30d`）+ 正确的 Used/Limit/ResetsAt
- [x] 7.4 确认状态栏与网页控制台能显示这三个维度（消费方不用改，但要**实际看一眼**——状态栏一行放 3 个维度可能过长，若过长在文档里说明）
- [x] 7.5 确认**没配 Access Key** 时：额度查询失败、状态栏显示 stale 或空白、**Claude Code 启动完全不受影响**
- [x] 7.6 用 LSP `findReferences` 确认改动波及面（`ProviderInfo` / `Config` / `PoolID` / `ParseConfig`），特别确认没有别的 adapter 因新增字段而行为改变
- [x] 7.7 归档前把 `CHANGELOG.md` 的对应版本段写好（见 CLAUDE.md 的 Release Tag 流程）
