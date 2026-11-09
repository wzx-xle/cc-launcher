# ccl 控制台（`/ui`）：本机配置的在线编辑

> 状态：ccl v0.21.0 起生效（`web-config-console` change）。
> 页面结构从"单页用量看板"变成"外壳 + 三视图"；**用量视图的行为一个字没改**。

## 一句话

`ccl web` 打开的控制台有三个视图：

| 视图 | 能干什么 |
|---|---|
| **用量** | 原看板页：token / cost 统计（行为不变） |
| **提供商** | 在线编辑 provider 配置、查看与按需刷新额度 |
| **Claude** | 在线编辑用户级的 `~/.claude/settings.json` 与 `~/.claude/CLAUDE.md` |

## 能改什么，不能改什么

| 能改 | 不能改（**故意的**） |
|---|---|
| `~/.cc-launcher/providers/*.json` | `~/.claude.json` —— Claude Code 每次会话都在写它，乐观锁会一直撞；它是状态不是配置；里面还有 OAuth 账号信息与主机指纹 |
| `~/.claude/settings.json` | 任何**项目级**配置 —— 网页挂在 daemon 里是全局的，不知道"当前项目"是哪个；项目文件跟着 git 走，在网页上改容易和 git 打架 |
| `~/.claude/CLAUDE.md` | 用户级 `commands/` / `skills/` —— 偶尔才加一个，先不做 |

改 provider 时可以新建（选模板起个 JSON 骨架）、复制、删除；额度卡上可以**手动刷新**
（联网查一次）。

## 安全：拿到 token 就能改本机配置

**这是这个功能最需要知道的一条。**

- token 存在 `~/.cc-launcher/web-token`（0600），`ccl web` 打印的 URL 里带它
  （`?token=…`），首次访问会换成 HttpOnly session cookie 并重定向掉 token
- **token 现在的语义从"看用量"扩到了"改配置"**：provider 配置里有**明文凭证**
  （`ANTHROPIC_AUTH_TOKEN`），`CLAUDE.md` 决定 Claude Code 的行为
- daemon 监听 `:4318` 是**全网卡**的，`ccl web` 会打印局域网 IP 的 URL ——
  同一个局域网里拿到 token 的人，同样能改你的配置
- **泄漏了怎么办**：`ccl web --rotate` 立刻作废旧 token 与全部已签发的 session
  （cookie 的 HMAC key 就是 token）

写接口额外做两道闸（防 CSRF）：请求的 `Content-Type` 必须是 `application/json`，
`Origin`（若存在）必须与请求同源。路径**只走白名单**：provider 名要匹配
`^[A-Za-z0-9._-]{1,64}$`，Claude 文件只有 `settings` / `claude-md` 两个固定标识，
**不接受任何客户端传来的路径**。

## 并发：改了别人也改了的文件怎么办

`settings.json` 同时被三方写：Claude Code 自己、ccl 的 TUI、这个网页。

所以每个写接口都带**乐观锁**：

1. 读文件时服务端返回 `etag`（文件内容的 sha256 前 16 位）
2. 写的时候必须带上 `If-Match: <etag>`
3. 对不上 → **409**，**不写盘**，并把当前文件内容一起带回来

网页上的提示是"文件已被其他程序修改。请重新加载后再保存——改动还在编辑框里，
没丢"。**不会静默覆盖别人的改动。**

## 备份

每次写之前，旧内容先复制进 `~/.cc-launcher/backups/`：

| 文件 | 备份名 |
|---|---|
| `settings.json` | `settings.json-<时间戳>.bak` |
| `CLAUDE.md` | `CLAUDE.md-<时间戳>.bak` |
| `<provider>.json` | `<provider>.json-<时间戳>.bak` |

每个文件保留最近 30 份（旧的自动裁掉）。写盘是原子的（临时文件 + rename），
不会留下半个文件。

> **`settings.json` 在这个目录里会有两套备份名**：`settings.json-<时间戳>.bak`
> 是网页控制台写的，`settings-<时间戳>.json` 是 ccl 自己的配置管理器
> （`internal/config.Manager`）写的。两套各保留 30 份、互不清理——去
> `~/.cc-launcher/backups/` 找备份时两种名字都属正常。

## 额度卡的三种状态（别看错）

serve 只刷新**最近有活动**的 provider（活动信号来自 OTel，Claude Code 每 60 秒
推一次，窗口 5 分钟）。所以网页上会看到三种状态：

| 状态 | 显示 | 含义 |
|---|---|---|
| 有条目且新鲜 | 正常额度条 | 数据可信 |
| 有条目但过期 | 灰显 + "N 分钟前采集" | 数据旧了 |
| **没有条目** | **"近期无活动，未采集额度"** | **不知道** —— 不是"无限额" |

第三种最容易骗人："没采到"被当成"没限制"，然后放心把量用爆。点额度卡上的
「刷新」按需查一次（联网，**遵守退避**：被厂商限流后会等 1→2→4→…→30 分钟，
手动刷冲不掉它，因为它打的是同一个接口、同一把 key）。

### 额度卡的数值口径

额度卡的**数值**要跟状态栏对齐 —— 网页显示 72%、状态栏显示 68% 会让人开始怀疑
哪个是真的。

**按维度 kind 分派**（跟状态栏的 `renderDimension` 一样）：

| kind | 显示 | 例子 |
|---|---|---|
| `window` | 只有百分比（**不带已用/总量**），百分比四舍五入到整数 | `5h  ▓░░░░░░░░░  7%  -> 19:58` |
| `balance` | 只有余额数字，**无条形** | `CNY: 5.17` |
| 其余（`quota`） | 百分比 + 条形 + 已用/总量 | `USD  ▓░░░░░░░░░  9% 37.46/399.95` |

`window` 为什么**不带**已用/总量：minimax 这类维度的 `limit` 就是 100、单位就是 `%`，
`7/100 %` 与 `7%` 是同一件事，写两遍纯冗余。而 `quota` 维度的 `limit` 是 399.95
这种真实分母，`9%` 与 `37.46/399.95` 不是一回事 → 保留。

**条形呈现介质不同，但口径一致**：状态栏用字符 `█░`（终端里是点阵等宽字体，才
整齐），网页用 CSS 进度条 —— 字符 `█`(U+2588) 填满字框、`░`(U+2591) 只占下半部分，
在浏览器里拼起来**上下参差**，字体还常把 `░` 渲染成斜线纹理。两边必须一致的
是：**条形代表的比例**、**配色阈值**（90/75/50 → 红/黄/绿）、**旁边的数字文本**。

但这里有件事得说清楚：**状态栏脚本不在本仓库里**（`~/.claude/statusline.js`
是用户自己的配置），所以"一致"靠的是**两边各写一份、以状态栏为准**，
不是共享代码。两边的落点：

| 东西 | 状态栏 | 网页 |
|---|---|---|
| 按 kind 分派 | `~/.claude/statusline.js` 的 `renderDimension` | `internal/web/assets/providers.js` 的 `dimKind` / `dimText` |
| 阈值 90/75/50 | 同上的 `pctColor` | 同上的 `pctColor` |
| 百分比口径 | `rateLimitStr`（window）/ `quotaKindStr`（其余） | `dimPctText` |

**改口径要改两处**（状态栏是真相来源，网页向它对齐）。想彻底消除这个隐患，得把
阈值与口径从服务端下发（额度接口多带一个 `thresholds` 字段），让网页读接口而不是
硬编码 —— 目前还没这么做。

网页侧的口径有 node 单测守着（`internal/web/jstest/quotaformat.test.mjs`），
改坏了会红。

### `has_quota` 在两个接口里不是一回事

别被同名字段绕晕：

| 接口 | `has_quota` 的含义 |
|---|---|
| `GET /api/providers`（列表） | 这个 provider **声明了** `quota` 块 → 也就是 spec 说的 "declares a quota interface" |
| `GET /api/providers/{name}/quota`（详情） | 这个 provider **探测到**有额度接口 —— 即使没写 `quota` 块，只要 `base_url` 能认出 adapter（DeepSeek / MiniMax）也算有 |

所以一个只配了 `ANTHROPIC_BASE_URL` 的 DeepSeek provider，在列表里是
`has_quota: false`，在额度接口里是 `has_quota: true`。**两个都对**，问的问题不同。

网页的额度卡用的是**额度接口**那个（详情），所以它显示"这个 provider 没有额度
接口"时是真的没有。

## 保存之后什么时候生效

| 改了什么 | 什么时候生效 |
|---|---|
| provider 配置 | **下次启动 ccl**（已在跑的 Claude Code 不会热更新） |
| `settings.json` | **下次启动 Claude Code**（它只在启动时读配置） |
| `CLAUDE.md` | 下次启动 Claude Code |

删 provider 时会连带清掉额度数据文件里它的条目 —— 不然删掉的 provider 还留着
额度数据，看着像"这个 provider 还在用"。

## 写坏了会怎样

**保存前就拦下来**：

- JSON 解析不过 → 400，不写盘
- 已知键类型错（比如 `env` 不是对象）→ 400 + 错误列表，不写盘

为什么要这么严：Claude Code 对 settings 的校验粒度是**整个文件**——一个已知键
的类型不对，它会丢弃**整个** settings 文件，provider 的凭证跟着一起消失。
校验不过就**拒绝启动**，不静默退化。网页是这条链路上更早的一道闸。

## 故障排查

| 现象 | 原因 |
|---|---|
| 页面 401 | 没带 token：`ccl web` 重新打印 URL |
| 「跨源请求被拒绝」 | 页面不是从 `ccl web` 打印的地址打开的 |
| 「文件已被其他程序修改」 | 乐观锁冲突（Claude Code / TUI 同时在改）→ 重新加载再改 |
| 「厂商限流中，N 分钟后可重试」 | 额度池在退避中，等着就行 |
| 额度显示「近期无活动，未采集额度」 | 这个 provider 最近没被用过，serve 没刷它 → 点「刷新」 |
