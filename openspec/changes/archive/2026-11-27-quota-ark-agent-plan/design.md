## Context

**现状**：ccl 有 4 个额度 adapter（minimax / deepseek / newapi / codex2api），全部走"provider 的 API key → Bearer 头"的鉴权模型。本机在用的火山方舟 Agent Plan（provider `ark-deepseek-flash`）**没有** adapter——它的额度在状态栏里完全不可见。

**接口事实**（已实测确认，非文档转述）：

```
POST https://ark.cn-beijing.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01
Content-Type: application/json
X-Date: 20261127T032146Z
X-Content-Sha256: 44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a
Authorization: HMAC-SHA256 Credential=<AK>/20261127/cn-beijing/ark/request,
  SignedHeaders=content-type;host;x-content-sha256;x-date, Signature=<hex>
body: {}

{"ResponseMetadata":{"RequestId":"...","Action":"GetAFPUsage","Version":"2024-01-01",
                     "Service":"ark","Region":"cn-beijing"},
 "Result":{"PlanType":"Large",
   "AFPFiveHour":{"Quota":"50.0","Used":"12.5","SubscribeTime":1778788800000,"ResetTime":1778806800000},
   "AFPDaily":   {...}, "AFPWeekly": {...}, "AFPMonthly": {...}}}
```

**两条实测结论决定了整个设计**：

1. **Bearer 鉴权被实测排除**。拿 provider 那个 `ark-…` key 打过去：HTTP 400 / `InvalidAuthorization`（CodeN 100024）；无鉴权则是 `InvalidCredential`（100025）。接口只认 Access Key 签名。
2. **`Quota` / `Used` 是 string**（`"50.0"`），不是数字。厂商为什么用字符串没说，但解析必须容错。

**签名算法**已从官方 `volcengine-go-sdk v1.0.260` 的 `base/sign.go` 读出，并**自研实现后与 SDK 输出逐字节比对通过**（同 AK/SK/时间戳下 `Authorization` 头完全相同）。

### 实测记录（2026-11-27，medium 档个人版）

用 `arkcli` 登录换到的火山引擎 STS 凭证（`~/.arkcli/identities/volc-2134993439/sts.json`）打真接口，**HTTP 200**：

```json
{"ResponseMetadata":{"RequestId":"...","Action":"GetAFPUsage","Version":"2024-01-01",
                     "Service":"ark","Region":"cn-beijing"},
 "Result":{"PlanType":"medium",
   "AFPFiveHour":{"Quota":10000,  "Used":328.5542,"SubscribeTime":1795748093000,"ResetTime":1795766093000},
   "AFPWeekly": {"Quota":35000,  "Used":328.5542,"SubscribeTime":1795363200000,"ResetTime":1795968000000},
   "AFPMonthly":{"Quota":100000, "Used":328.5542,"SubscribeTime":1795747501000,"ResetTime":1798387199000},
   "AFPDaily":  {"Quota":50000,  "Used":0,       "SubscribeTime":1795708800000,"ResetTime":1795795200000}}}
```

**这一发推翻了两处**：

1. **`Quota` / `Used` 是 number，不是文档写的 string**（见 D7）
2. **`AFPDaily.Used` 恒为 0**（见 D5）——同刻 5h 窗口有 328.5542 AFP 消耗，daily 却纹丝不动

**同时印证了三处**：档位表（medium → 5h=10000 / 日=50000 / 周=35000 / 月=100000，与调研的档位表逐项吻合）；5h 窗口锚在首次请求（Subscribe 00:14:53Z → Reset 05:14:53Z，正好 5 小时）；周窗口是自然周（周一 00:00 → 下周一 00:00，北京时间）。

**签名验证**：官方 SDK 与自研实现（均带 `X-Security-Token` 参与签名，`SignedHeaders=content-type;host;x-content-sha256;x-date;x-security-token`）产出的 `Authorization` 头**逐字节相同**，两个请求的响应除 `RequestId` 外完全一致。

**凭证时效**：STS 凭证约 1 小时过期（arkcli 会自动续期并写回 `sts.json`）。**长期使用必须用长期 AK/SK**。

**子账号 AK 的验证**（2026-11-27，第二次实测）：用户建了只读 IAM 子账号并把 provider 配置换成子账号 AK（`AKLTODUy…lNjg`）→ **HTTP 200，数据与主账号完全一致**（`Used: 863.3498`）。见 D12。

**约束**（来自项目 CLAUDE.md 与 `provider-quota` spec）：

- 额度接口是**外部行为**——厂商改字段必须**降级而不是崩**，且**绝不允许影响 Claude Code 启动**
- `quota` 块是 ccl 自己的元数据，**不透传**给 Claude Code（必须留在 `provider.ReservedKeys`）
- 标签/单位**永远不取自厂商响应的原始字符串**
- 额度刷新按**池**去重；429 必须走既有退避框架
- 改函数签名 / 删函数 / 跨包重构前用 LSP `findReferences` 确认影响面

## Goals / Non-Goals

**Goals:**

- 方舟 Agent Plan 个人版的额度出现在状态栏与网页控制台，5h / 周 / 月三个窗口齐全
- Access Key 签名用标准库自研，**不新增第三方依赖**
- 凭证**不进 Claude Code 的进程环境**，也不进 provider 的 `env` 块
- 同一对 Access Key 下的多个 provider **只查一次**额度
- 厂商改字段 / 鉴权失败 / 限流 → 一律降级，旧条目保留

**Non-Goals:**

- **企业版席位**（`GetSeatAFPUsage`，需传 `SeatIDs`）——另一个接口、另一个数据模型
- **不换算 AFP → token**：AFP = Agent Fuel Point，抵扣系数随模型变（`deepseek-v4.1-flash` 输入/输出系数 2.5，`deepseek-v4-flash` 0.5），1 AFP 不是固定 token 数，换算等于说谎
- 不读方舟的调用明细接口（"获取套餐用量详情"，数据有 0.5–1 天延迟）
- 不引入 `volcengine-go-sdk` 依赖
- 不改 Claude Code 的任何文件、不改 telemetry schema

## Decisions

### D1. adapter 名 `ark`，识别靠 host + path 两个条件

`Match` 要求 base URL 的 host 形如 `ark.<region>.volces.com` **且** path 含 `/api/plan`。

**为什么不能只看 host**：普通方舟按量 API 的 base URL 是 `https://ark.cn-beijing.volces.com/api/v3`——**同一个 host**，但它没有 Agent Plan 套餐，额度是钱不是 AFP。只看 host 会把按量用户也识别成套餐用户。

**为什么 path 判据够准**：`/api/plan` 是方舟文档里 Agent Plan 的专用路径前缀，不是通用约定也不是用户随便能撞上的形状。识别错的代价只是"去查一个没有套餐的账号 → 报错 → 降级"，不涉及凭证外泄。

**兜底**：用户在 provider 里显式写 `"quota": {"source": "ark"}` 即可绕过识别（与 newapi / codex2api 同款处置）。

### D2. 端点从 base URL 的 region 推导，不写死单 region

`https://ark.cn-beijing.volces.com/api/plan` → region `cn-beijing` → 管控面 `https://ark.cn-beijing.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01`。

**注意管控面 host 与数据面 host 不同**（`volcengineapi.com` vs `volces.com`）——不能拿 base URL 换个后缀了事。

region 从 base URL 的 host 解析（`ark.<region>.volces.com`），解析不出就报错并让用户显式配 `quota.endpoint`。这样其它 region（`cn-shanghai` 等）自动可用。

### D3. 自研 V4 签名，不引 SDK

签名逻辑单独放 `internal/quota/arksign.go`，只用 `crypto/hmac` + `crypto/sha256`。

**为什么不用官方 SDK**：SDK 拉进来 1600+ 个文件、拖一堆传递依赖，而我们只需要 60 行标准库。

**为什么敢自研**：已用官方 SDK 的 `Credentials.Sign()` 生成黄金向量，自研实现对同一输入产出**逐字节相同**的 `Authorization` 头。这条等价性会固化成单测（见 D9）。

签名要点（**任何一条写错都会 403**）：

- `Credential` 是**单斜杠**：`{AK}/{ShortDate}/cn-beijing/ark/request`，ShortDate 是 **8 位**日期（不是完整 X-Date）
- `X-Date` = `20060102T150405Z`（UTC）
- canonical query 用 `url.Values.Encode()` 排序编码，`+` 换成 `%20`
- canonical request 的段间用 `\n`；canonical headers 段**自带结尾换行**，其后还要接一个 `\n`（即 headers 与 signed headers 之间有一个空行）
- 密钥派生链：`kDate=HMAC(SK,ShortDate)` → `kRegion` → `kService` → `kSigning=HMAC(kService,"request")`

**参与签名的头必须按官方规则动态确定**，不能写死四个：官方 `sortHeaders` 的规则是——`Content-Type` / `Content-Md5` / `Host` / `X-Security-Token` **以及所有 `X-` 前缀的头**参与，其余不参与。写死四个在本 adapter 当前形状下等价，但将来多发一个 `X-` 头就会漏签 → 服务端 403，且报错信息完全看不出是漏签。

### D4. 凭证来源：quota 块优先，进程环境兜底

| 优先级 | 来源 | 说明 |
|---|---|---|
| 1 | `quota.accessKey` / `quota.secretKey` | ccl 自己的元数据，**不透传**给 Claude Code |
| 2 | `VOLCENGINE_ACCESS_KEY_ID` / `VOLCENGINE_SECRET_ACCESS_KEY` | daemon 进程继承的环境变量 |

**为什么 quota 块优先**：显式、可版本化、跟着 provider 走。

**为什么还要环境变量兜底**：让"不落盘"成为可能——用户不想把 AK/SK 写进 `~/.cc-launcher/providers/*.json` 时有路可走。

**为什么绝不能放 provider 的 `env` 块**：`env` 块会**原样交付给 Claude Code**（经 `--settings`），等于把火山引擎账号的凭证交给模型与它的 hook / MCP / 子进程。provider 的 `env` 块里现有的 `ANTHROPIC_AUTH_TOKEN` 已经是推理凭证，再加一条账号级凭证是权限倒挂。

**兜底的已知代价**：daemon 是常驻进程，环境变量在它启动时固定——改了环境变量必须重启 `ccl serve`。这一点写进文档。

### D5. 三个窗口 → 三个 window 维度（**实测修正**，原设计是四个）

| 接口字段 | Duration | 维度 ID | label（自动派生） | 实测 medium 档 |
|---|---|---|---|---|
| `AFPFiveHour` | 5h | `five_hour` | `5h` | 10000 |
| `AFPWeekly` | 7d | `weekly` | `7d` | 35000 |
| `AFPMonthly` | 30d | `monthly` | `30d` | 100000 |

`Kind = window`，`Unit = "AFP"`，`Limit = Quota`，`Used = Used`，`Remaining = Quota - Used`（夹到 0），`ResetsAt = ResetTime`（epoch 毫秒）。

**为什么只有三个（`AFPDaily` 被实测否掉）**：接口确实返回四个窗口，但 **`AFPDaily.Used` 在文本模型推理场景恒为 0**——实测同一时刻 `AFPFiveHour.Used = 328.5542` 而 `AFPDaily.Used = 0`。方舟文档写明日限额"仅限视觉/语音模型、Harness"，即日额度是给图片/语音模型与 Harness 能力调用单独计的，**Claude Code 走文本模型推理不计入**。官方 CLI 的 `arkcli usage plan` 同样只输出 5h / weekly / monthly 三个窗口——它做了同样的判断。报一个恒为 0 的维度只会在状态栏里多占一行。

> 这条是**实测推翻的原设计**：原设计据文档推断"日额度与月额度对所有模型都生效"，据此选了四个维度。实测后改为三个。

**为什么月窗口按 30d 近似**：实测月窗口是**订阅月**（订阅日 07:25 → 次月同日 23:59:59，本机是 27 号起），不是自然月、也不是固定 30 天。`Duration` 只用于**派生展示标签**（`30d`），准确的重置时刻由接口给的 `ResetTime` 承载，两者不冲突。

**周窗口是自然周**：实测 SubscribeTime = 周一 00:00（北京时间）→ ResetTime = 下周一 00:00，正好 7 天。

**消费方不用改**：状态栏"有几个显示几个"，网页额度卡按维度列表渲染。

### D6. 额度池按 Access Key 划分

**问题**：现有 `PoolID(endpoint, apiKey)` 一律按 API key 分池。但方舟的 AFP 额度是**账号套餐级**的——同一对 AK 下的多个 `ark-…` key 共用同一个套餐配额。按 API key 分池 → 同一个套餐被查多次、存多份、消耗多次管控面配额。

**改法**：加一个**可选**接口，厂商可自带池键；不实现的 adapter 行为完全不变。

```go
// PoolKeyer 是可选接口：额度池的划分依据与 API key 不同时实现它。
type PoolKeyer interface {
    PoolKey(p ProviderInfo) string
}
```

refresh 层用类型断言取池键，取不到就回退 `p.APIKey`。ark 的池键 = Access Key ID。

**为什么用可选接口而不是改 `Adapter` 接口**：改主接口会让 4 个已有 adapter 全部要改（破坏性），而实际只有 ark 需要。

**顺带的好处**：同 AK 下的多个 provider 现在会被"刷一个池"覆盖到——与 `quota-active-providers-only` 那条"额度数据只服务于当前会话那个 provider"的语义一致。

### D7. `Quota` / `Used` 的类型：文档说 string，**实测是 number** → 两种都要吃

官方文档的输出参数表写的是 `Quota string` / `Used string`，但 **2026-11-27 真机实测返回的是 JSON number**：

```json
"AFPFiveHour":{"Quota":10000,"Used":328.5542,...}
"AFPDaily":{"Quota":50000,"Used":0,...}
```

厂商改文档或改实现都是它的事，两种形态都要能解析。**用一个自定义反序列化类型同时接受 number 与 string**：

- 字段缺失 / `null` / 空串 → 该字段**未知**（`ok = false`）
- 字段存在但值非法（解析失败）→ 该字段**未知**（`ok = false`），**不返回 error**
- 合法 → `ok = true`

**为什么非法值报"未知"而不是 0**：0 会被读成"额度是 0 / 用量为 0"，那是**结论**；解析失败是**不知道**。两者混同就是"额度查询坏了"被读成"额度没了"。

**为什么反序列化不返回 error**：字段级非法只该丢**那一个维度**，不该让整个响应解析失败——那会把"某个字段变了"放大成"额度全查不到"。这与 `provider-quota` spec 的字段级降级契约一致。

**为什么 `Quota == 0` 要特殊处理**：`Unlimited = true`，不派生百分比、不报总额（与 codex2api 的 `quota_limit == 0` 同款处置）。

### D8. 失败一律降级，旧条目保留

沿用 `provider-quota` spec 既有契约：任何失败 → 保留该 provider 上一次的额度条目 → `fetched_at` 变旧 → stale 自动生效。

具体失败形态与处置：

| 形态 | 判据 | 处置 |
|---|---|---|
| HTTP 400 + `InvalidCredential` / `InvalidAuthorization` | body 的 `ResponseMetadata.Error.Code` | 失败（凭证不对），错误信息带上 `Code` 与 `Message` |
| HTTP 403 | 签名不对 / 权限不足 | 失败，错误信息提示"签名或权限"，**不能**只说"HTTP 403" |
| HTTP 429 | 状态码 | `RateLimitError` → 走既有退避框架 |
| `Result` 为空 | body 里没有 `Result` | 失败（接口形状变了） |
| `Quota` / `Used` 非法 | 单个字段 | **只丢那个维度**，其余维度照报 |

**错误信息必须区分"签名失败"和"网络失败"**：签名依赖本机时钟（±15 分钟窗口），机器时间跳变会表现为鉴权失败。笼统报"额度查询失败"会让用户查错方向。

### D9. 签名用黄金向量锁死

单测里硬编码一组由官方 SDK 产出的人工向量：

```
AK=AKTESTKEY  SK=SKTESTSECRET  X-Date=20261127T032146Z
body={}  →  X-Content-Sha256=44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a
Authorization=HMAC-SHA256 Credential=AKTESTKEY/20261127/cn-beijing/ark/request,
  SignedHeaders=content-type;host;x-content-sha256;x-date,
  Signature=17647640ac5545aa9c6d4990d0059db3ff19d4a098411b9381157953d62f78a5
```

有了它，签名实现任何改动都能被立刻发现——**不引 SDK 依赖也能锁住外部契约**。

### D11. 额度是**账号级**的，不是 API Key 级的 —— 接口层面就没有"按 key 查"的能力

`GetAFPUsage` 的入参里**没有 API Key 的位置**：签名只有 Access Key（代表"调用者是谁"），请求体固定 `{}`。它返回的是**调用者账号的 Agent Plan 套餐额度**，而不是"某把 `ark-…` key 的额度"。

**官方口径**：火山引擎文章明写"**主账号下所有子账号共享该额度池**"，某个子账号高频调用会耗尽全账号 AFP；控制台【额度管理】可以按应用设**单日上限**（是上限，不是拆分用量——默认仍是共享）。

**本机当前恰好相等，但不是保证**：账号下有两把 `ark-…` key，但只有 `ark-893a96…`（profile `agent-plan_cn-beijing_personal`）吃套餐额度，另一把 `ark-f0c738…`（profile `platform_cn-beijing_accountwide`）是 Platform 按量计费、不占套餐。Claude Code 用的正是前者 → **现在查到的 328.5542 AFP 就是这把 key 的消耗**。

**这个"相等"随时会被打破**：在别的工具（arkcli 自己、Cursor、Trae）也用同一套餐、或再建一把 Agent Plan key、或在方舟控制台用网页版对话 → 额度立刻变成共享的。

**由此产生的语义风险**：状态栏显示 `5h 已用 328/10000`，用户会读成"我这条 Claude Code 会话用了 328"，而实际是"你账号下所有方舟调用一共用了 328"。**这是语义误导，不是显示 bug**——所以维度必须被标注为账号额度。

**为什么不能改用"按 key 的用量统计"来替代**：`arkcli usage stats --by apikey` 确实能按 API Key 拆用量，但**实测对套餐用户返回空**（走的是按量计费口径的统计接口，套餐消耗不进），而且它只给**用量不给分母**（拿不到 5h/周/月 的额度上限，算不出百分比）。`arkcli usage plan-details` 能按**模型**拆套餐用量，但**没有 API Key 维度**。→ 想要"按 key 的百分比"，两条路都断。

**结论**：ccl 报的就是**账号套餐额度**。这不是缺陷，是这个接口唯一能给的答案。UI 与文档必须如实标注，且**账号下多 provider 时它们显示的是同一个数**——这正是 D6「池按 Access Key 划分」要表达的语义（同一把 AK = 同一个池 = 同一份额度）。

### D12. 子账号 AK 可行（**已实测**，最小权限方案落地）

用户选了**只读 IAM 子账号**而不是主账号 AK——查额度是纯只读需求，主账号 AK 是账号全权（能建资源、改账单、买套餐、读所有数据），权限与需求严重倒挂。

**2026-11-27 实测结论**：子账号 AK 调 `GetAFPUsage` → **HTTP 200**，数据与主账号 AK **完全一致**（`Used: 863.3498`）。

- 子账号 AK：`AKLTODUy…lNjg` → 200
- 主账号 AK：`AKLTMWYx…hODc` → 200（同一时刻，两次响应的 `Used` 相同）
- 两者签名均与官方 SDK 逐字节相同

**这次实测还顺带证实了 D11**：子账号看到的额度与主账号**完全相同** → 额度确实挂在账号上，不是挂在 key 上。

**仍然要注意的两个坑**（本次没踩到，但换个环境就可能踩）：

1. **AccessDenied**：子账号必须被授予方舟管控面的只读权限。官方文档里的预置策略有 `ArkReadOnlyAccess`（方舟全局只读）、`ArkPlanUserAccess`（Coding Plan 普通用户）；**没有列出 `GetAFPUsage` 的最小策略清单**（`ArkPlanReadOnlyAccess` 这个名字只在营销文章里出现过，官方策略列表里查不到）。所以"绑哪个策略"要**在控制台策略管理里搜 `Ark` 逐个确认**。
2. **套餐归属**：接口的语义是"查调用者**所属账号**的套餐"。子账号用自己的 AK 时必须**绑定到同一套餐主账号**下——本次是同一个账号，所以查到的是同一份额度。
3. **策略生效有延迟**：IAM 策略配置后约 1~5 分钟才生效（不同文档说法不同），刚建完就测可能仍报权限不足。

**为什么子账号 AK 仍然要由用户自己保管**：ccl 是通用工具，不能替用户创建 IAM 资源。凭证的来源与优先级见 D4（quota 块优先，环境变量兜底）。

### D10. 只做个人版，能力边界写进文档

`GetAFPUsage` 只适用个人版 Agent Plan。企业版席位是 `GetSeatAFPUsage`（入参 `SeatIDs`，返回 `SeatAFPUsages[]`，每个席位自己一套四窗口）。

**为什么不做企业版**：数据模型不同（套餐 vs 席位）、鉴权主体不同（企业版还要 `ListSeatAFPUsage` 拿席位列表才能逐个查）、配额口径不同。硬塞进一个 adapter 会让两边都别扭。

**为什么要在文档里写死**：否则用户会拿企业版账号来试，报错看不懂。

## Risks / Trade-offs

**[Access Key 权限过大]** → 火山引擎 AK 是账号级的，而查额度是纯只读需求。**建议用户建只授方舟管控面读权限的 IAM 子账号**，不要用主账号 AK。文档里写明这个建议。

**[本机时钟偏移导致签名失败]** → 签名对 ±15 分钟敏感。NTP 校正 / 手动改表会让额度突然全挂。缓解：错误信息明确说"签名被拒，先查本机时间"；并确认失败时**保留旧条目**（状态栏显示 stale 而不是空白）。

**[管控面限流]** → ccl 默认 60 秒刷新一次，一次只发一个请求，量级远低于人工轮询。但方舟管控面未公开限流阈值。缓解：429 走既有指数退避（1min→2min→…→封顶 30min），退避写进数据文件，跨 serve / `--refresh` / 重启都生效。

**[厂商改接口字段]** → 外部行为。缓解：`Quota`/`Used` 的字符串解析容错（D7）+ 字段级降级（丢一个维度不丢全部）+ 旧条目保留（D8）。契约记录在 `docs/provider-quota.md`，改 adapter 前先看它。

**[池划分改动波及已有 provider]** → `PoolKeyer` 是可选接口，不实现就回退 `p.APIKey`。缓解：只有 ark 实现它；用 LSP `findReferences` 确认 `PoolID` 的三个调用点都走同一条取值路径。

**[月度维度的 30d 近似不准确]** → 只影响展示标签（`30d`），不影响重置时刻（`ResetsAt` 用接口的 `ResetTime`）。缓解：标签是近似的这个事实写进注释。

**[把 AK/SK 写进 `quota` 块 = 明文落盘]** → 与已有的 `ANTHROPIC_AUTH_TOKEN` 同密级（同一批文件、同一台机器）。缓解：给环境变量兜底通道；文档说明取舍。

## Migration Plan

**纯增量，零迁移**：

1. 发版即包含新 adapter 与签名实现——**不配置任何东西就不会被触发**（既有 provider 的 `quota` 块与 env 都不受影响）
2. 用户要给方舟 provider 配额度时，才写 `"quota": {"source": "ark", "accessKey": "...", "secretKey": "..."}`
3. 回滚 = 把 provider 的 `quota` 块删掉（或换回旧值）——代码层的 adapter 可以留在原地，它只在 `source: "ark"` 时才被选中

**验证顺序**（实施时按此推进）：

1. 单测：签名黄金向量（D9）+ 响应解析（mock HTTP server）
2. 真机：**已用 arkcli 的 STS 临时凭证验证过**（见"实测记录"）——签名实现、响应形状、窗口语义都成立。长期 AK/SK 就位后需**再打一次**确认长期凭证的行为一致（STS 与长期 AK 的权限模型可能不同）
3. 端到端：`ccl quota --json` 看三个维度 → 状态栏 / 网页控制台显示

**前置条件**：长期 AK/SK 未就位——用户将建 IAM 子账号。**STS 凭证约 1 小时过期，不能用于常驻 daemon**；在长期凭证到位前，ccl 的额度查询会持续失败并保留旧条目（不影响 Claude Code 启动）。

## Open Questions

- **AK/SK 从哪来**：**已定**——用户去火山引擎控制台建一个只授方舟管控面读权限的 **IAM 子账号**，拿长期 AK/SK。arkcli 登录换到的 STS 凭证（约 1 小时过期）只够做一次性验证，不能用于常驻 daemon。
- **方舟管控面的实际限流阈值未知**：文档没公开。先按 60 秒周期跑，若观察到 429 再按实测调 `quota.refreshInterval`。
