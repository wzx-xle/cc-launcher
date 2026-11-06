## 1. provider 配置：`quota` 块

- [x] 1.1 `internal/provider/provider.go`：`ReservedKeys` 增加 `quota`——它和 `note` / `order` 一样是 ccl 自己的元数据，**不加这里就会被原样透传**给 Claude Code（`provider-settings-passthrough` 的排除清单同步扩一个键）
- [x] 1.2 `cmd/cc-launcher/providerconfig.go`：`providerConfig` 增加 `Quota` 字段（结构体：`Source` / `Endpoint` / `QuotaPerUnit` / `DisplayUnit` / `RefreshInterval`），从清洗后的配置顶层取（**不进 `Passthrough`**）
- [x] 1.3 `RefreshInterval` 单位 = **秒**，缺省 **60**
- [x] 1.4 单测：provider 配了 `quota` 块 → 既能被读到、又不出现在透传键里；没配时为空；`refreshInterval` 缺省为 60

## 2. `internal/quota/model.go`：统一额度模型

- [x] 2.1 `Dimension` 结构体：`ID` / `Label` / `Kind` / `Duration` / `Used` / `Limit` / `Remaining` / `Unit` / `ResetsAt` / `ExpiresAt` / `Unlimited` / `Raw`
- [x] 2.2 `Kind` 三分类：`window`（有周期 + 有分母）、`quota`（无周期 + 有分母）、`balance`（无分母）
- [x] 2.3 **`UsedPct()` 是派生的**：`*Used / *Limit * 100`；`Limit == nil` / `*Limit == 0` / `Unlimited` → 返回 nil（**不是 0**）
- [x] 2.4 **`Used` 与 `Remaining` 分成两个字段**，不合并成一个 `Amount`——余额是"剩余"、额度是"已用"，**方向相反**，合并就要靠约定猜方向
- [x] 2.5 **`label` / `unit` 必须是纯 ASCII**：窗口型 label 从 `duration_seconds` 派生（`18000`→`5h`、`604800`→`7d`、`2592000`→`30d`）；**非窗口型 label = `unit` 的值**（`CNY` / `USD` / `tokens`）；**label 永远不取自厂商响应的原始字符串**（可能是中文、可能含控制字符）
- [x] 2.6 单测：三种 kind 的构造与 `UsedPct()` 的各种边界（无分母 / 零分母 / 无限 / 有分母）；label 的 ASCII 与派生规则

## 3. `internal/quota/adapter.go`：厂商差异关进盒子

- [x] 3.1 `Adapter` 接口：`Name()` / `Match(baseURL)` / `Endpoint(baseURL)` / `Fetch(ctx, ProviderInfo)`
- [x] 3.2 `ProviderInfo`：`BaseURL` / `APIKey` / `Model`
- [x] 3.3 注册表 `var adapters = []Adapter{...}` + 按 `source` 查找 + 按 `Match` 兜底检测
- [x] 3.4 **`Fetch` 拥有整个请求**（认证头、签名、超时都归 adapter）；ccl 的其它层只管去重、周期、文件、输出
- [x] 3.5 **端点推导是 per-adapter，不是全局规则**——三家的推导规律各不相同（MiniMax 换子域、DeepSeek 同主机换路径、New API 同前缀加后缀），任何全局规则都会推出错端点

## 4. 三个内置 adapter

- [x] 4.1 **MiniMax adapter**
  - [x] 4.1.1 识别：剥掉前导 `api.` / `www.` 后主机名**恰好等于** `minimax.cn` / `minimax.io`；`minimax-proxy.corp.example` 这类"含 minimax 但不相等"的**不识别**
  - [x] 4.1.2 端点：`https://www.<剥前缀的主机名>/v1/token_plan/remains`（**不许硬编码单一域名**——`.cn` 与 `.io` 的 key 不通用）
  - [x] 4.1.3 成败：**必须查 `base_resp.status_code == 0`**，不能只看 HTTP 状态码（错误也返回 HTTP 200）
  - [x] 4.1.4 取值：`model_remains` 里优先取 `model_name == "general"` 的条目，没有则取第一项；数组为空 → 该维度缺失（不编造 0）
  - [x] 4.1.5 语义转换：剩余% → 已用%（`100 - remaining`，clamp 到 [0,100]）
  - [x] 4.1.6 **窗口时长从 `end_time - start_time` 算**（实测恒为 18000000ms = 5h；`weekly_end_time - weekly_start_time` = 604800000ms = 7d）——**不许写死 "5h" / "7d"**，MiniMax 改了 interval 标签要自动跟着变
  - [x] 4.1.7 重置时间：`remains_time` / `weekly_remains_time`（毫秒）→ `resets_at`（Unix 秒）
  - [x] 4.1.8 `current_interval_status` / `current_weekly_status` **不解释**（语义未知），原值进 `Dimension.Raw`
- [x] 4.2 **DeepSeek adapter**
  - [x] 4.2.1 端点：`https://<base_url 主机名>/user/balance`（**同主机换路径**，不是换子域）
  - [x] 4.2.2 成败：看 HTTP 状态码（401 = key 不对）
  - [x] 4.2.3 **错误体可能是纯文本**（无 Authorization 头时返回 `Authentication Fails (governor)`）→ 错误解析必须容忍非 JSON
  - [x] 4.2.4 取值：`balance_infos[0]` 的 `total_balance` → **剩余**（不是已用）；`currency` → 单位（CNY / USD）；**字符串数字要转 float**
  - [x] 4.2.5 **没有总额度 → `Limit` 留 nil → 派生不出百分比 → kind = `balance`**
  - [x] 4.2.6 `is_available: false` → 映射成 `Remaining = 0`
- [x] 4.3 **New API adapter**（New API / one-api 系部署）
  - [x] 4.3.1 端点：**`<base_url>/api/usage/token`**（同前缀加后缀）
  - [x] 4.3.2 成败：看 body 的 `code == true`；**同时要容忍 401 + `{"error":{...}}` 的认证层失败体**
  - [x] 4.3.3 取值：`data.total_granted` → **总量**（`Limit`）、`data.total_used` → **已用**、`data.total_available` → **剩余**
  - [x] 4.3.4 单位换算：除以配置的 `quotaPerUnit`（缺省 500000 = New API 出厂默认）；显示单位取 `displayUnit`（缺省 `USD`）
  - [x] 4.3.5 `unlimited_quota: true` → `Unlimited = true`（**不派生百分比**）
  - [x] 4.3.6 `expires_at != 0` → `ExpiresAt`；`== 0` → 无到期时间
  - [x] 4.3.7 `model_limits` / `model_limits_enabled`：当前实测未启用，**只透传进 `Raw`**，不做分模型额度

## 5. `internal/quota/store.go`：数据文件

- [x] 5.1 路径 `~/.cc-launcher/quota.json`；权限 **0600**；**原子写**（临时文件 + rename，与 `settingsfile.go` 一致）→ 读取侧并发读不会读到半个文件
- [x] 5.2 结构：`{version, updated_at, entries: {<provider名>: {source, pool, fetched_at, dimensions}}}`
  - [x] 5.2.1 **按 provider 名索引**（读取侧 `ccl quota --provider X` 要按名字直接找）
  - [x] 5.2.2 **冗余存储维度**：同一额度池被多个 provider 引用时内容重复——冗余换来读取侧零查找成本，且每个条目自带 `fetched_at`
  - [x] 5.2.3 `pool` = `sha256(端点 + "\x00" + API key)`：**含 key**（额度按 key 分不按 provider 名分）；**只存哈希不存明文**
- [x] 5.3 **写入用「读-改-写」，不用全量重建**：`store.Update(name, dims)` = 读现有文件 → 改这一条 → 原子写回；`store.Prune(live)` 删掉已不存在的 provider 的条目
  - [x] 5.3.1 **为什么不用全量重建**：serve 与 `ccl quota --refresh` 会**并发写同一个文件**。读-改-写让两边的写冲突只剩「读和写之间」的几毫秒，且冲突时写的是**同一个额度池的数据**（内容等价）→ 无害；全量重建则可能整份覆盖掉对方的条目
  - [x] 5.3.2 **刷新失败的 provider 不调 `Update`** → 旧条目保留（`fetched_at` 不变 → 越来越旧 → stale 自动生效）
- [x] 5.4 单测：原子写（并发读不到半个文件）/ 0600 / 不含明文 key / 残留条目清理 / 失败保留旧条目

## 6. `internal/quota/refresh.go`：刷新逻辑

- [x] 6.1 枚举 `~/.cc-launcher/providers/*.json` → 挑出**配了 `quota` 块**的（也包括能按 base_url 自动识别的）→ 解析 adapter
- [x] 6.2 **按 (端点, key) 去重**：同一额度池**只查一次网络**，结果写进所有引用它的 provider 条目（`minimax-m3` 与 `minimax-m3.1-flash` 共用一把 key → 只查一次）
- [x] 6.3 **周期判定**：距上次刷新不足该 provider 的 `refreshInterval`（缺省 60s）→ 跳过这一轮
  - [x] 6.3.1 **「上次刷新时间」必须从数据文件里的 `fetched_at` 读**，不能只记在 serve 的内存 map 里 → `ccl quota --refresh` 刚写过的 provider，serve 下一轮会自动跳过（**两个写者天然互斥**，不用锁）
- [x] 6.4 **HTTP 客户端**：`Authorization: Bearer <key>`，超时 5s；网络层错误与业务层错误（由 adapter 判定）分别归类
- [x] 6.5 **跟随重定向，不改写 scheme**：用 Go 默认的重定向行为。**Go 实测：跨 http→https 重定向会保留 `Authorization`**（`shouldCopyHeaderOnRedirect` 按 `url.Hostname()` 剥端口比对，同主机即保留）→ **不要主动升级 scheme**——只提供 http 的内网额度服务升级后会 TLS 握手失败，额度永远查不到；用户显式配的 `quota.endpoint` 尤其不该被改
- [x] 6.6 **尾斜杠规范化**：从 base_url 推导端点前**去掉尾斜杠**（本机 iflytek 的 base_url 带尾斜杠；实测该网关双斜杠也能通，但那是宽容行为不是契约）
- [x] 6.7 **走 provider 自己的网络路径**：HTTP 客户端从**该 provider 的 env 块**取 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`（**不能用 daemon 自己的环境**——provider 的 env 是经 `--settings` 交付给 Claude Code 的，daemon 从 ccl fork 继承的是**用户 shell 的环境**）；`NO_PROXY` 按 host 后缀匹配，`no_proxy` 小写形式也认；**不引入 `golang.org/x/net/http/httpproxy`**，手写几十行即可
  - [x] 6.7.1 单测：`NO_PROXY` 命中 → 直连；未命中 → 走代理；`no_proxy` 小写形式同样生效；provider 未配代理 → 直连
- [x] 6.8 单测：端点推导（三家 + 覆盖 + 重定向跟随 + 尾斜杠）；解析（三家各自的正常 / 失败 / 边界）；去重（共用 key 只查一次）；周期判定（未到周期跳过）

## 7. `ccl serve` 接入刷新

- [x] 7.1 `cmd/cc-launcher/serve.go`：起一个 **ticker** 做额度刷新
  - [x] 7.1.1 **ticker 周期 = 所有待刷 provider 的 `refreshInterval` 的最小值**（缺省 60s）；每轮遍历 provider，**距上次刷新不足其自身周期的跳过**（时间从数据文件的 `fetched_at` 读，见 6.3.1）
  - [x] 7.1.2 **启动后立即刷一轮**（不等一个周期）→ 用户跑完一次 `ccl`（它会拉起 serve）就能读到数据
  - [x] 7.1.3 **刷新不得干扰 daemon 的本职**：metrics 端点照常服务；**graceful shutdown 仍在 5s 预算内完成**（刷新用独立 goroutine + ctx 取消）
- [x] 7.2 刷新失败只进 serve 日志，**不写进数据文件**（不调 `store.Update` → 旧条目保留，`fetched_at` 越来越旧 → stale 自动生效）
- [x] 7.3 `ccl serve --status` 的摘要里可加一行额度状态（可选）

## 8. `ccl quota` 子命令（**默认纯本地读**；`--refresh` 才联网）

- [x] 8.1 新增 `cmd/cc-launcher/quotacmd.go`：`runQuota(args)`
- [x] 8.2 **provider 三级回落**：显式 `--provider` > 环境变量 `CCL_PROVIDER` > `state.json` 的 active provider（状态栏子进程继承得到 `CCL_PROVIDER`，所以消费方只要传 `--provider "$CCL_PROVIDER"` 就行）
- [x] 8.3 **只读 `~/.cc-launcher/quota.json`，一次网络都不发**（实测状态栏命令约每 300ms 调一次 → 必须是纯本地读）
- [x] 8.4 `--json` 输出维度列表：`{provider, source, stale, fetched_at, dimensions: [...]}`；**带 `fetched_at`**（消费方不必去读数据文件就知道数据多旧）；**每个维度省略自己没有的字段**（不写 null）；`used_percentage` 由 `UsedPct()` 派生后写进 JSON（消费方不用自己算）
- [x] 8.5 **stale 判定**：用条目的 `fetched_at` 算 age，超过该 provider 的 `refreshInterval`（**兜底 60s** = 默认刷新周期；provider 配置读不到时用它）→ 输出标 `stale: true`
- [x] 8.6 非 `--json`：给人看的表格（provider、来源、每个维度的 kind / 已用% / 绝对量 / 重置或到期时间、数据是否 stale）
- [x] 8.7 **失败语义**：该 provider 无额度接口 → 退出码 **2**（**纯离线可判**）；无记录 / 文件不存在 → 退出码 **1**；成功 → **0**；失败时**不打印额度文档**
- [x] 8.8 路由：`cli.go` / `main.go` 加 `quota` 子命令；`ccl --help` 与 `ccl quota --help` 补上；shell 补全（bash / zsh / powershell）补 `quota`
- [x] 8.9 **`--refresh` flag**：**联网查一次 → 写回数据文件 → 照常输出**（输出的是刚查到的值）。**不拆成两个 flag**——一步到位，`--json` 可直接 pipe 给 jq
  - [x] 8.9.1 `quota.RefreshOne(ctx, ProviderRef)`：单 provider 版刷新（serve 的全量版复用它）
  - [x] 8.9.2 写回走 `store.Update(name, dims)`（**读-改-写**）
  - [x] 8.9.3 **失败时不写文件**（旧条目保留）+ 退出码 **1** + stderr 说明
  - [x] 8.9.4 **无额度接口 → 不联网 + 退出码 2**（离线判定在联网之前）
  - [x] 8.9.5 **`--refresh` 不需要 serve 在跑**——这是 serve 被 `ccl serve --stop` 关掉时的逃生舱
  - [x] 8.9.6 补全：`completeAtQuota` 加 `--refresh`
- [x] 8.10 单测：`--refresh` 会发网络请求；不带 `--refresh` **一次网络都不发**；`--refresh` 失败不覆盖旧条目；serve 的周期判定读的是**文件里的** `fetched_at`（不是内存 map）

## 9. 状态栏接入（`~/.claude/statusline.js`，**用户个人目录，不进本仓库**）

- [x] 9.1 读 `ccl quota --provider "$CCL_PROVIDER" --json`，取 `dimensions[]`（**只调命令，不直接读数据文件**——文件位置与格式是 ccl 的内部细节）
- [x] 9.2 **两条分支不合并**：`ccl quota` 退出码 0 → 完全用 quota 的维度清单（有几个显示几个）；非 0 → 保持今天 Claude Code 的 `rate_limits` 两个固定槽位（缺就显示 `--`，**Anthropic 官方 provider 的输出一个字节都不变**）
- [x] 9.3 **按 kind 三种画法**：`window` → 复用现有 `rateLimitStr()`（dimension 是 `rate_limits` 项的严格超集，**渲染函数一行不改**）；`quota` → 条形图 + 绝对值；`balance` → **只显示数字，不画条形图**（无分母画不出，且"已用"与"剩余"方向相反）
- [x] 9.4 **标签规则（与 2.5 一致）**：窗口型 → `duration_seconds` 派生（`18000`→`5h`、`604800`→`7d`、`2592000`→`30d`）；**非窗口型 → `unit` 的值**（`CNY` / `USD` / `tokens`）→ 显示成 `CNY: 5.17`；`duration_seconds > 86400` → 重置时间总带日期（"7 天后重置"只写 `03:14` 会让人以为是今天）。ccl 给的 `label` 优先，但**仍要过 `sanitizeLabel`**（防御性）
- [x] 9.5 **超时保护：`execSync` timeout 设 250ms**——Claude Code 状态栏有 **300ms 去抖窗口**且**每次执行前 abort 上一次**（实测：超时 10 分钟不是瓶颈，300ms 窗口才是）。正常路径 ~15ms，留 15 倍余量；任何失败**静默**回落（状态栏不能因为额度查询卡住或崩掉）
- [x] 9.6 认知：状态栏命令**生成过程中约每 300ms 被调一次**（`tokenUsage` 变化驱动）→ 每秒 3.3 次调用是常态，**设计必须假设"调用极其频繁"**；由于命令纯本地读，耗时恒定
- [x] 9.7 可选：维度过多时按 `duration_seconds` 升序截断（终端宽度是硬约束）——**显示策略，不是契约**

## 10. 明文 base_url 提示

- [x] 10.1 `cmd/cc-launcher/providerconfig.go`：`ANTHROPIC_BASE_URL` 以 `http://` 开头 → 追加一条 `Warnings`（**非阻断**）：点名 provider 与明文 base_url，说明 key 会明文传输
- [x] 10.2 **不自动改写用户的 base_url**——那是静默改用户配置语义，ccl 只提示不替用户做主
- [x] 10.3 单测：http:// 的 base_url 产生 warning，https:// 的不产生

## 11. 文档

- [x] 11.1 新增 `docs/provider-quota.md`：`ccl quota` 的用法与输出契约（维度列表 + 三种 kind + stale 语义）、**数据文件由 serve 维护**（刷新周期默认 60s、可配）、**支持的 adapter 与各自的 `source` 名**、provider 配置里 `quota` 块的写法（含"这个地址会收到你的 API key"的提醒）、**外部行为实测结论**（厂商改了字段怎么办）
- [x] 11.2 `README.md` 增补：状态栏显示额度（怎么用、为什么 Anthropic 官方 provider 下显示的是 Claude Code 自己的限流）
- [x] 11.3 `CLAUDE.md` 约束 6 增补：`quota` 是 ccl 自己的元数据键（与 `note` / `order` 同等，不透传）；额度刷新由 `ccl serve` 负责

## 12. 真机验证（用本机已配置的 provider）

本机 provider 清单：`minimax-m3` / `minimax-m3.1-flash`（**共用一把 key**）/ `minimax-m3-stliu14`、`deepseek-v4` / `deepseek-flash`、`iflytek-jlcao3`、`claude-official`（无 base_url）。

- [x] 12.1 `minimax-m3` → 断言解析出的**已用%**（`100 - remaining_percent`）、**窗口时长（从 `end_time - start_time` 算，实测恒为 18000s）**、周窗口时长（604800s）与重置时间合理
- [x] 12.2 **`minimax-m3` 与 `minimax-m3.1-flash` 共用一把 key** → 断言一轮刷新只发**一次**请求（额度池按 (端点,key) 去重）
- [x] 12.3 `deepseek-v4` → 断言剩余金额（字符串数字要转 float）、货币（CNY/USD）、**无分母 → 无百分比 → kind = balance**
- [x] 12.4 `iflytek-jlcao3` → 断言 `total_granted / total_used / total_available` 三者的**代数自洽**（granted - used == available）、单位换算（÷ quotaPerUnit）、**尾斜杠规范化后端点无双斜杠**
- [x] 12.5 `claude-official`（无额度接口）→ `ccl quota` **退出码 2** + stderr 说明，**不打印额度文档**
- [x] 12.6 **serve 刷新**：起 serve → 确认数据文件被写出 → 确认 **60 秒后自动再刷一次**（`fetched_at` 变化）→ 确认**启动后立即就有第一份数据**（不等 60 秒）
- [x] 12.7 **`ccl quota` 纯读**：断网状态下跑 `ccl quota --json` → 仍能返回数据（证明不发网络请求）
- [x] 12.8 端到端：`ccl quota --json` → 喂给状态栏脚本 → 真 claude 跑一次 → 状态栏上出现**真实额度数字**（不再是 `--`）
- [x] 12.9 失败路径：serve 停掉 → `ccl quota` **退出码 1** → 状态栏回落到 Claude Code 的 `rate_limits`；serve 在跑但厂商接口挂了 → 数据文件保留旧条目 → `ccl quota` 返回**旧值 + stale**
- [x] 12.10 **`--refresh`**：serve 停掉的情况下跑 `ccl quota --refresh --json` → 仍能返回**刚查到**的真实额度（证明不依赖 serve），且数据文件被更新；紧接着跑不带 `--refresh` 的 `ccl quota --json` → 返回同一份数据（证明写回生效）
- [x] 12.11 **代理路径**：本机 `iflytek` 的 `NO_PROXY` 含 `.iflytek.com` → 断言**直连**（不走 `t14.local:7890`）；`claude-official` 只有 `HTTP_PROXY` 但无额度接口 → **"有额度接口且需走代理"这条路径本机无法真机覆盖，只能靠 6.7.1 的单测**
- [x] 12.12 回归：`go test ./...` + `go test -tags smoke ./...`（注意：`internal/collector` 的 `TestServer_ScenarioA_PerSignalMetrics_EndToEnd` 是**既有失败**，与本改动无关）

## 13. 发布

- [x] 13.1 `CHANGELOG.md` 的 `[Unreleased]` 下加 `0.20.0` 段（按既有分类格式）
- [x] 13.2 `openspec archive provider-quota`
- [x] 13.3 按 CLAUDE.md 的 Release Tag 流程：`git commit` CHANGELOG → `git tag -a v0.20.0` → **一次推送** `git push origin main` + 单独推 tag（annotated tag，`--follow-tags` 会带上；`bin/bump-version.sh` 建的是 lightweight tag，那个必须单独推）
