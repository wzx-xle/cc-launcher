# provider 额度查询（`ccl quota`）

> 状态：v0.20.0 起可用。本文档描述的是**对外契约**——`ccl quota --json` 的输出
> 形状是稳定的，仓库外的消费方（如 Claude Code 状态栏脚本）可以依赖它。

## 为什么需要

Claude Code 的状态栏有两个额度槽位（`5h` / `7d`），数据来自 Claude Code 自己
维护的 `rate_limits`——那是 **Anthropic 官方**的限流。走第三方模型时它拿不到
数据，槽位就一直是 `--`：

```
ctx: ░░░░░░░░░░ -- | 5h: ░░░░░░░░░░ -- | 7d: ░░░░░░░░░░ --
```

用户看不到自己的配额还剩多少，只能等 429 报错才发现。而各家"额度"的形态
完全不同：

| provider | 额度形态 | 百分比可算？ | 会重置？ |
|---|---|---|---|
| Anthropic 官方 | 5h + 7d 窗口 | ✅ | ✅ |
| MiniMax 套餐 | 5h + 7d 窗口 | ✅ | ✅ |
| DeepSeek | 预付费余额（$） | ❌ **没有分母** | ❌ |
| 讯飞（New API 私有部署） | 令牌总额度（额度点） | ✅（分母是用户自设上限） | ❌ |

**固定成"5 小时 + 7 天"两个槽位表达不了这个多样性**——只有月额度的厂商塞不
进 `five_hour`，标签写 `5h` 实际是 30 天更是说谎。所以额度输出是一个**维度
列表**，不是一个固定形状。

## 用法

```bash
# 看当前 provider 的额度（人看的表格）
ccl quota

# 指定 provider + JSON 输出（pipeable to jq）
ccl quota --provider minimax-m3 --json

# 手动联网刷一次（不需要 serve 在跑）
ccl quota --refresh --json
```

**默认只读本机数据，不向任何额度接口发请求。** 数据由 `ccl serve` 周期刷新
（默认 60 秒）写入 `~/.cc-launcher/quota.json`；`ccl quota` 优先经 daemon 的
**回环端点**读它（daemon 不可达时直读同一个文件）——**回环 HTTP ≠ 向外网发
请求**，原纪律的意图（不打扰厂商的额度接口）不变。

> 状态栏要**零进程**读额度，见下一节「[零进程读额度](#零进程读额度daemon-的只读端点)」。

### provider 怎么定

三级回落：

1. 显式 `--provider <名字>`
2. 环境变量 `CCL_PROVIDER`（ccl 启动 Claude Code 时注入；状态栏子进程继承得到）
3. `state.json` 的 active provider（上次在 TUI 里选的）

所以状态栏脚本只要 `execSync('ccl quota --provider "$CCL_PROVIDER" --json')`
就行——不传 `--provider` 也能靠环境变量兜住。

### 退出码

| 码 | 含义 | 消费方该怎么做 |
|---|---|---|
| `0` | 读到额度 | 用 `dimensions` |
| `1` | **读不到数据**：serve 没跑 / 首次刷新还没完成 / 该 provider 尚无条目 | 回落 Claude Code 的 `rate_limits` |
| `2` | 该 provider **没有额度接口**（正常状态，不是错误）——**纯离线可判** | 回落 Claude Code 的 `rate_limits` |

分级的意义：消费方能区分"这厂商没额度"（2）和"数据还没准备好"（1）。
**失败时不打印额度文档**（stdout 只有成功时才有内容）。

## 输出契约

```json
{
  "provider": "minimax-m3",
  "source": "minimax",
  "stale": false,
  "fetched_at": 1793943914,
  "dimensions": [
    {
      "id": "interval",
      "label": "5h",
      "kind": "window",
      "duration_seconds": 18000,
      "used_percentage": 4,
      "used": 4,
      "limit": 100,
      "unit": "%",
      "resets_at": 1793961914
    },
    {
      "id": "weekly",
      "label": "7d",
      "kind": "window",
      "duration_seconds": 604800,
      "used_percentage": 33,
      "used": 33,
      "limit": 100,
      "unit": "%",
      "resets_at": 1794323556
    }
  ]
}
```

### 顶层字段

| 字段 | 说明 |
|---|---|
| `provider` | provider 名 |
| `source` | 产出这份数据的 adapter 名（`minimax` / `deepseek` / `newapi`） |
| `stale` | 数据是否已过期（见下） |
| `fetched_at` | 抓取时间（Unix 秒）——消费方不必读数据文件就知道数据多旧 |
| `dimensions` | 额度维度列表（**有几个显示几个**） |

### 维度字段

| 字段 | 说明 |
|---|---|
| `id` | 稳定标识（`interval` / `weekly` / `balance` / `quota`） |
| `label` | 展示名，**纯 ASCII**（见下） |
| `kind` | `window` / `quota` / `balance`（见下） |
| `duration_seconds` | 窗口时长（秒）；**没有的字段不写**（不写 null） |
| `used_percentage` | **已用**百分比（ccl 派生，消费方不用自己算）；算不出时缺席 |
| `used` / `limit` / `remaining` | 绝对量；`limit` 缺席 = 不知道分母 |
| `unit` | 单位（`%` / `USD` / `CNY` / `tokens`），**纯 ASCII** |
| `resets_at` / `expires_at` | Unix 秒 |
| `unlimited` | 无限额度（此时无 `used_percentage`） |
| `raw` | 厂商原始字段（排查用） |

**查一个"最近没用过"的 provider**：`ccl quota --provider X` 是**纯本地读**，
而 serve 只刷新最近有活动的 provider → X 没有条目 → **退出码 1、没有输出**。
想立刻拿到 X 的额度，用 `ccl quota --provider X --refresh`（联网查一次并写回）。

### 三种 kind

| kind | 语义 | 典型 | 状态栏画法 |
|---|---|---|---|
| `window` | 有周期 + 有分母 | MiniMax 5h / 7d | 条形图 + 百分比 + 重置时间 |
| `quota` | 无周期 + 有分母 | New API 令牌额度 | 条形图 + 绝对值 |
| `balance` | **无分母** | DeepSeek 预付费余额 | **只显示数字**（画不出条形图，且"已用"与"剩余"方向相反） |

### label 与 unit 的规则

- **`label` 与 `unit` 永远是纯 ASCII**——它们要渲染到终端的一行文本里，
  中文字形缺失时无法读取。
- **`label` 永远不取自厂商响应的原始字符串**（厂商的字段可能是中文、可能含
  控制字符）：
  - 窗口型 → 从 `duration_seconds` 派生：`18000`→`5h`、`604800`→`7d`、
    `2592000`→`30d`
  - 非窗口型 → **`unit` 的值**（`CNY` / `USD` / `tokens`）
- **ccl 给的 `label` 优先**，但消费方仍应自己过一遍 `sanitizeLabel`（防御性）。

### stale 语义

`stale` 由 ccl 判定：条目的 `fetched_at` 距今超过该 provider 的
`refreshInterval`（**兜底 60 秒**）**+ 1 秒容忍** → `stale: true`。

**那 1 秒容忍是必须的**：`fetched_at` 只存到秒（向下取整），而数据年龄的峰值
≈ 周期 − 刷新耗时 → **取整误差最多 +1 秒**。阈值正好等于周期时，稳态下每次刷新
前约 1 秒会误报 stale（实测 age 峰值 60.75~61.48 秒，42 秒采样里每个 provider
越线 4~8 次）——状态栏上表现为 `(stale)` 偶尔闪一下，而数据其实是新鲜的。

**stale 是连续的状态**，不是"这次请求失败"：serve 刷新失败时**保留旧条目**
（`fetched_at` 不变 → 越来越旧 → stale 自动变 true）。所以：

- 额度不会因为一次网络抖动就变没——状态栏从数字跳回 `--` 是很糟的体验
  （用户会以为额度用光了）
- 用户看得出"数据不新鲜"，而不是"额度没了"

（**当前没人在用的** provider 靠兜底保鲜每 10 分钟才刷新一次 → 它们的 `stale`
会持续为 true，因为数据**确实**已经旧了。这是**可见**的，不是静默失效。切换到
这样的 provider 时，ccl 会立刻声明并触发刷新，数字随即变新。）

## 零进程读额度（daemon 的只读端点）

### 为什么

状态栏是 ccl 额度的唯一消费者，而它的调用节奏极其苛刻：Claude Code 的状态栏
约每 **300ms** 刷新一次，每次都是**新起的进程**。

走命令的问题不在"读"，在"起进程"。Windows 实测（2026-11-12）：

| 调用方式 | 耗时 |
|---|---|
| `ccl quota --json`（node `execSync`，状态栏的真实路径） | p50 **166~190ms**，max **252ms** |
| 同一命令在 PowerShell 里直接调 | p50 **266ms**，max **299ms** |

耗时的**全部**在进程启动上：`cmd.exe(scoop shim)` → `cmd.exe(app\ccl.cmd)` →
`cc-launcher.exe`。而 `ccl quota` 本身已经是纯本地读（读一个 JSON 文件），
毫秒级。

状态栏脚本给这次调用设了 `timeout: 250`——**余量只有 60~80ms**。系统繁忙时
（Claude Code 刚启动、用户配的三个 hook 各起一个 node 进程、Defender 实时扫描
18MB 的 exe 与上百 MB 的 node.exe、daemon 正在写额度数据文件）→ 这次调用必然
超过 250ms → 脚本 `catch` → 返回 null → **状态栏回落到 Claude Code 自己的
`rate_limits` 槽位**。

用户看到的就是"额度显示成默认的用量"，而且**只在忙的时候错**——闲下来
（166ms）就对。这解释了"刚启动不对、等会就正常"。

**daemon 是常驻的**：HTTP 回环请求是**毫秒级**，且**不落进 300ms 的去抖
窗口**（它不是"新起的进程"，只是一次连接）。

### 端点契约

```
GET http://127.0.0.1:4318/ccl/quota?provider=<名字>
```

| 情况 | 状态码 | body |
|---|---|---|
| 有记录 | **200** | 额度文档（与 `ccl quota --json` **逐字段同构**） |
| `provider` 缺失或空 | **400** | 纯文本 |
| 来源不是回环 | **403** | 纯文本 |
| provider 未配置 / 没有额度记录 | **404** | 纯文本 |
| provider 没有额度接口 | **422** | 纯文本 |

- **200 的 body** 就是上面「[输出契约](#输出契约)」那份文档：`provider` /
  `source` / `stale` / `fetched_at` / `dimensions`——**消费方换通道时解析逻辑
  一行都不用改**。
- **404 与 422 要分开**：消费方要能区分"这厂商没额度"（422）与"数据还没准备好"
  （404）——`ccl quota` 的退出码 2 与 1 就是这个区分。
- **端点未注册时**（daemon 没跑这个能力）请求得到 **404**，且响应**不带**
  `X-Ccl-Endpoint: quota` 头 → 客户端据此知道"这个端点不存在"（而不是
  "没有额度记录"）→ **回落直读本地数据文件**。这在 **ccl 升级但 daemon 还是
  旧进程**时会发生。
- 端点**只读**：不写数据文件、不触发刷新。

### 回环限制

端点**只接受回环来源**（连接的来源地址是回环地址），否则 **403**。

- 判定看的是 daemon **实际接受的那个连接的来源地址**（`RemoteAddr`），
  **不看任何请求头**——daemon 是裸 HTTP server，没有可信的转发链，读
  `X-Forwarded-For` 等于把判定交给请求方（伪造一个头就能拿到额度数据）。
- **为什么必须限**：daemon 监听 `:4318` = **所有网卡**。不限的话，局域网内
  任意机器都能读到本机额度余额。
- **为什么限回环就够**：本机任何进程本来就能直读 `~/.cc-launcher/quota.json`
  （0600 只挡别的用户，挡不住同用户的进程）→ "回环来源"这个边界**恰好等于
  本机进程能直接读文件的边界**，不引入新的本机暴露面。
- 同样的限制也套在 `POST /ccl/activate`（ccl 声明本次用哪个 provider）上——
  它与新端点一样属于"ccl 自己 ↔ daemon"的内部交换。

### `CCL_QUOTA_URL` 的契约

ccl 启动 Claude Code 时，把额度端点的地址放进环境变量 **`CCL_QUOTA_URL`**：

```
CCL_QUOTA_URL=http://127.0.0.1:4318/ccl/quota
```

| 契约 | 说明 |
|---|---|
| **名字** | `CCL_QUOTA_URL`。**改名 = breaking change**（会静默弄坏仓库外的消费方）。名字**不能带凭证特征**（`TOKEN`/`KEY`/`SECRET`/`PASSWORD`）——Claude Code 的 subprocess scrub 会把凭证类变量从子进程环境里删掉 |
| **值** | daemon 的固定端口 + 端点路径。**URL 形态也是契约**——改形态按 breaking change 处理 |
| **两条交付通道** | ① `--settings` 交付对象的 `env` 块（**主通道**，命令行层压过用户 `settings.json` 的 env 块，也压过进程环境）；② 进程环境（**冗余第二通道**，万一某版 Claude Code 不把 settings 的 env 块落到子进程环境） |
| **ccl 的键赢** | provider 配置的 `env` 块里若写了同名键 → **ccl 的值覆盖**；用户 `settings.json` 里的同名键也压不过它（主通道优先级更高） |
| **无条件交付** | **不因 daemon 没起来就不给**——消费方拿到 URL 连不上就自己回落。条件交付会把"交付"与"daemon 状态"耦合起来 |
| **缺席 ≠ 空串** | 直接跑 `claude`（不经 ccl）的会话**拿不到**这个变量 → 消费方知道"这个会话不是 ccl 起的"。ccl **永不交付空值** |

它**不承载**额度的生效语义——额度数据由 daemon 周期刷新写进本地数据文件，
这个变量只告诉消费方"去哪儿读"。

### 状态栏脚本的推荐写法

**HTTP 优先（不起进程，几 ms）+ 命令兜底（永远可用，但慢）**：

```js
#!/usr/bin/env node
// ~/.claude/statusline.js
const fs = require('fs');
const http = require('http');
const { execFileSync } = require('child_process');

const input = JSON.parse(fs.readFileSync(0, 'utf8')); // Claude Code 的 stdin JSON

// 回环 HTTP 读额度：不起进程，几 ms。
function fetchQuota(base, provider, timeoutMs) {
  return new Promise((resolve, reject) => {
    const req = http.get(
      `${base}?provider=${encodeURIComponent(provider)}`,
      { timeout: timeoutMs },
      (res) => {
        let body = '';
        res.setEncoding('utf8');
        res.on('data', (c) => { body += c; });
        res.on('end', () => {
          if (res.statusCode !== 200) { reject(new Error(`status ${res.statusCode}`)); return; }
          try { resolve(JSON.parse(body)); } catch (e) { reject(e); }
        });
      },
    );
    req.on('timeout', () => req.destroy(new Error('timeout')));
    req.on('error', reject);
  });
}

async function loadQuota() {
  const provider = process.env.CCL_PROVIDER;   // undefined = 不是 ccl 起的会话
  if (!provider) return null;
  const base = process.env.CCL_QUOTA_URL;       // undefined = 没有端点可用
  if (base) {
    try {
      const q = await fetchQuota(base, provider, 200); // 超时给足余量
      if (q && Array.isArray(q.dimensions)) return q;
    } catch (_) { /* 端点不可用 → 走命令 */ }
  }
  try {                                          // 兜底：daemon 没跑 / 端点不可用
    return JSON.parse(execFileSync('ccl', ['quota', '--json'], { timeout: 250, encoding: 'utf8' }));
  } catch (_) { return null; }
}

loadQuota().then((q) => {
  // 渲染是你自己的事；注意 q 的形状与 `ccl quota --json` **完全一样**。
  const q5h = q?.dimensions?.find((d) => d.id === 'interval');
  const pct = q5h?.used_percentage;
  const line = `5h: ${pct == null ? '--' : pct + '%'}`;
  process.stdout.write(line + '\n');
});
```

**两条通道的输出同构**（同一份文档）→ 消费方拿到什么就渲染什么，**解析逻辑
一行都不用改**。

> **为什么是异步**：Node 的 `http` 模块只有异步 API（没有同步 socket）。上面
> 的写法在 promise 落定后才写 stdout 再退出——Claude Code 读的是进程的
> stdout，Node 在事件循环空时退出，所以不会截断输出。若你的脚本必须是**纯
> 同步**的，那就只能用命令兜底那条路（慢，但正确）。

### 哪些端点不受回环限制（维护者判据）

daemon 上有三类无鉴权端点，语义不同，**不能一刀切**：

| 端点 | 合法调用方 | 收紧会打断谁 | 处置 |
|---|---|---|---|
| `/ccl/activate`、`/ccl/quota` | 只有本机 ccl 与本机消费方 | 无人 | **限回环** |
| `/v1/metrics`、`/v1/logs`、`/v1/traces` | 任意 OTLP 客户端 | **WSL2 / 容器里的 Claude Code**：WSL2 连 Windows 的 `localhost:4318` 走 NAT，Windows 侧看到的源地址是虚拟网段（`172.x`），**不是回环** → 一收紧遥测就断，且报错难懂 | 不动 |
| `/ui`、`/api/*` | 用户浏览器 | 从别的机器看看板（已有 token 保护） | 不动 |

**daemon 的监听地址保持 `:4318` 不变**——把它绑到 `127.0.0.1` 会同时打断上面
两类，所以收紧只发生在**端点级**。

`/v1/*` 的收紧若将来要做，前置条件是**先确认没有 WSL2 / 容器 / 跨机 Claude Code
推遥测的场景**，否则要配套一个"可信来源白名单"配置项。

## 数据文件

`~/.cc-launcher/quota.json`，权限 **0600**，**原子写**（临时文件 + rename）→
并发读不会读到半个文件。

```jsonc
{
  "version": 1,
  "updated_at": 1793943914,
  "entries": {
    "minimax-m3": {
      "source": "minimax",
      "pool": "3f2a9c…",        // sha256(端点 + "\0" + API key)，**不含明文 key**
      "fetched_at": 1793943914,
      "next_at": 1793943974,    // 下次该刷的时刻（相位错开用）
      "dimensions": [ … ]
    }
  },
  // 退避状态：**按额度池**记录（限流是厂商针对那把 key 的，provider 换 key
  // = 换池 → 老退避不该拦住它）
  "backoffs": {
    "3f2a9c…": { "retry_after": 1793944332, "seconds": 60 }
  }
}
```

**按 provider 名索引**（读取侧要按名字直接找）；**冗余存储维度**（同一额度池
被多个 provider 引用时内容重复——冗余换来读取侧零查找成本，且每个条目自带
`fetched_at`）。

**额度池按 `(端点, key)` 去重**：额度是**按 key 分**的，不是按 provider 名分
的。现有 3 个 minimax provider 里 `minimax-m3` 与 `minimax-m3.1-flash` 共用
同一把 key → 同一个额度池 → **一轮刷新只发一次网络请求**，结果写进所有引用它
的条目。

**写入是「读-改-写」**，不是全量重建：serve 与 `ccl quota --refresh` 会并发
写同一个文件，读-改-写让冲突只剩几毫秒，且冲突时写的是同一个额度池的数据
（内容等价）→ **不需要文件锁**。最坏后果是某个条目短暂缺失一个刷新周期，
serve 下轮自愈。

## provider 配置

### `quota` 块（可选）

`quota` 是 **ccl 自己的元数据键**（与 `note` / `order` 同等），**不交付**给
Claude Code。

```jsonc
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://one.iflytek.com/api/llm/console/chat/",
    "ANTHROPIC_AUTH_TOKEN": "sk-xxx"
  },
  "quota": {
    "source": "newapi",          // 选内置 adapter
    "endpoint": "https://…",     // 可选：覆盖自动推导
    "quotaPerUnit": 500000,      // 可选：额度点换算比例
    "displayUnit": "USD",        // 可选：显示单位
    "refreshInterval": 60        // 可选：刷新周期（秒），缺省 60
  }
}
```

| 字段 | 必填 | 含义 |
|---|---|---|
| `source` | ❌ | 选内置 adapter（`minimax` / `deepseek` / `newapi`）。**不配就按 base_url 的 host 自动识别**（认得出 MiniMax / DeepSeek） |
| `endpoint` | ❌ | 覆盖自动推导（私有网关、二开路径） |
| `quotaPerUnit` | ❌ | 额度点换算比例（默认 500000 = New API 出厂默认） |
| `displayUnit` | ❌ | 显示单位（默认由 adapter 决定） |
| `refreshInterval` | ❌ | 刷新周期，**秒**（缺省 60） |

> ⚠️ **`quota.endpoint` 会把你的 API key 发给那个地址**。它是你自己的选择
> （provider 配置本来就带端点与凭证），但配置前想清楚：这个地址是你信任的吗？

### 支持的 adapter

| source | 识别方式 | 额度端点 | 判据 |
|---|---|---|---|
| `minimax` | host 剥掉 `api.`/`www.` 后**恰好等于** `minimax.cn` / `minimax.io` | `https://www.<host>/v1/token_plan/remains` | body 的 `base_resp.status_code == 0`（**错误也返回 HTTP 200**） |
| `deepseek` | host 剥掉 `api.`/`www.` 后**恰好等于** `deepseek.com` | `https://<host>/user/balance` | HTTP 状态码（401 = key 不对） |
| `newapi` | **只能显式配 `source`**（任何 host 都可能是它） | `<base_url>/api/usage/token/` | body 的 `code == true` |

**识别规则为什么这么严**：用"包含 minimax"会把 `minimax-proxy.corp.example`
这类自建域名误判成 MiniMax，推出一个不存在的端点——**识别错比识别不到更
糟**（识别错 = 拿着你的 API key 去打一个不存在的地址）。

**端点推导是 per-adapter 的**，不是全局规则：三家的规律完全不同（MiniMax 换
子域、DeepSeek 同主机换路径、New API 同前缀加后缀）。任何全局规则都会推出
错端点。

### 代理

额度查询走 **provider 自己的网络路径**：`HTTP_PROXY` / `HTTPS_PROXY` /
`NO_PROXY` 从**该 provider 的 `env` 块**取（不是 ccl 进程的环境——provider
的 env 是经 `--settings` 交付给 Claude Code 的，daemon 从 ccl fork 继承的是
用户 shell 的环境）。

**一个"必须走代理才能访问"的 provider，Claude Code 能用但额度查询会超时**
→ 所以额度查询必须复用 provider 的代理配置。

`NO_PROXY` 按 host 后缀匹配（`.example.com` 排除 `api.example.com`），
`no_proxy` 小写形式也认。

### 明文 base_url

provider 的 `ANTHROPIC_BASE_URL` 是 `http://` 时，ccl 会给一条**非阻断**警告
（key 会明文传输）。**ccl 不自动改写你的 base_url**——那是静默改你的配置
语义，ccl 只提示不替你做主。

## 刷新由谁负责

**`ccl serve`**（常驻，ccl 自动拉起，永不自动 idle 退出）：

- **刷新的粒度是"额度池"，不是 provider** —— 同一把 key 的多个 provider 共用一个
  池，额度是**一个事实**，必须同源。池内任一 provider 活跃 → 整池刷新 → 结果
  写给池内**所有** provider（它们的 `fetched_at` 完全相同）
  - **为什么**：按 provider 过滤会让同池的两个 provider 显示不同时刻的数据
    （实测：`minimax-m3` 与 `minimax-m3.1-flash` 同 key，`fetched_at` 差 38
    分钟）——用户看到的是"同一把 key 的余额，两个数字"
- **优先刷新"最近有活动"的池** —— 活动信号有两个来源：
  1. **ccl 的主动声明**（主路径，见下节）：ccl 启动 Claude Code 前就知道这次用
     哪个 provider → 立刻告诉 daemon → daemon **立即**刷新该池（不等 ticker）
  2. **OTel**（兜底）：ccl 启动时把 provider 名注入了 `OTEL_RESOURCE_ATTRIBUTES`
     的 `ccl.provider`，collector 解析每条 metrics 时就知道是哪个 provider →
     serve 查**最近 5 分钟内有 OTel 活动**的池（5 分钟 = 4 个 OTel 周期；实测
     Claude Code 每 60 秒推一次 metrics）
  - **为什么**：额度数据的唯一消费者是状态栏，而状态栏显示的是**当前会话那个
    provider** 的额度 → 其余池的额度数据**当前根本没人看**。查它们 = 纯浪费 +
    触发厂商的速率限制（实测：8 个 provider 的机器一轮就 429）
  - `ccl quota --provider X --refresh` **不受此限制**（用户显式点名 → 照查）
- **兜底保鲜：超过 10 分钟没更新的池也会被刷一次** —— 即便当前没有活跃成员
  - **为什么**：用户切到一个几小时没用过的 provider 时，状态栏显示的是那个时刻
    的旧数字（带 stale 标记）。保鲜给"最旧能有多旧"设了个上限
  - **量级**：每个池每 10 分钟 1 次 → 4 个池 ≈ 24 请求/小时（可忽略）；8 个池的
    机器 ≈ 48 请求/小时（仍远低于限流阈值）
  - **它不是刷新周期**：`refreshInterval`（默认 60 秒）管"有人在用时多久刷
    一次"，保鲜管"没人用时数据最多能旧多久"
  - **限流优先于保鲜**：正在退避的池即使超过 10 分钟也不会被刷
- **启动即刷一轮**（不等一个周期）→ 跑完一次 `ccl` 就能读到数据
  - daemon 的活跃度是**纯内存**状态，刚起来时是空的 → 那一轮会一个池都不查
    （实测 serve.log：`查了 0 / 不活跃跳过 6`，且整整 60 秒才恢复）→ 所以
    daemon 启动时会把 `state.json` 里记录的 active provider 标为活跃，**兜住**
    这个空窗
- ticker 周期 = 所有待刷 provider 的 `refreshInterval` 的**最小值**；每轮遍历
  额度池，**距上次刷新不足其自身周期的跳过这一轮**
- **刷新失败只进 serve 的日志，不写数据文件** → 旧条目保留 → stale 自动生效
- **刷新轮次的汇总日志按"池"计数**（请求本来就按池发）：
  `[quota] 额度刷新一轮（池）：查了 1 / 不活跃跳过 3 / 未到期或退避跳过 1 / 失败 0`
  —— **只在真的发了请求（或有失败）时才打**：ticker 每 8 秒一轮，全打的话绝大
  多数行都是"查了 0 / 跳过 N"，把真正的限流与失败信息淹在噪音里。零请求的
  轮次没有信息量（没有任何池需要查）；被退避拦下的池另有 429 那行单独记录
- 独立 goroutine + ctx 取消，不干扰 metrics 服务与 graceful shutdown

### ccl → daemon 的激活通道

`ccl` 启动 Claude Code 前会向 daemon 发一个 `POST /ccl/activate?provider=X`，
daemon 收到后**立刻**标记该 provider 活跃，并**立即触发一轮刷新**（不等 15 秒
的 ticker）。

- **为什么需要它**：daemon 判断"谁在用"的信号原本只有 Claude Code 推来的
  OTel —— 但那要等 Claude Code 起来（实测 daemon 重启后**整整 60 秒**盲窗，
  60 秒 = Claude Code 的 OTel 推送周期）。**ccl 才是唯一确知这次用哪个 provider
  的角色**（它在 exec Claude Code 之前就选定了）→ 用最权威、最早可得的信号
- **fire-and-forget**：ccl 不等响应、不阻塞启动；daemon 没起来就连不上 → 静默
  忽略（Claude Code 的 OTel 是兜底路径，功能不会因此坏掉）
- **白名单校验**：daemon 只接受 `providers/` 目录里**真实存在**的 provider 名
  （否则这个端点就成了"让 daemon 带用户的 key 去访问任意 URL"的放大器）
- daemon 独立启动（不经 ccl）时，靠 `state.json` 里记录的 active provider 兜底

**`ccl quota --refresh`** 是 serve 被 `ccl serve --stop` 关掉时的**逃生舱**：
联网查一次 → 写回数据文件 → 照常输出（**不需要 serve 在跑**）。

它与 serve **天然互斥**：serve 的周期判定从**数据文件里的 `fetched_at`** 读
（不是内存 map），所以 `--refresh` 刚写过的 provider，serve 下一轮会自动跳过。

### 刷新错开与退避（多 provider 场景必读）

**问题**：额度是**按 key 分**的——8 个 provider 用 8 把不同的 key 就是 8 个额度
池，一轮必然 8 个请求。它们若挤在一起打，厂商侧的速率限制会直接返回 429，
而且**每一轮都打** → 8 个 provider 的额度全部变 stale，额度功能实际瘫掉。

**解法一：相位错开**。每个额度池在自己的刷新周期内占一个**固定相位**，相位由
"同周期的一批池"均分（8 个 → 周期内每 7.5 秒一个）：

```
挤在一起:  |████ 8 个请求在 2 秒内 ████|          |████ 8 个请求 ████|
相位错开后: |·A·|···B···|····C····|·····D····|····E····|··F··|····G····|
```

**刷新周期不变**：刷新时刻 = 周期网格点 + 相位，相邻两次恰好差一个
`refreshInterval`（配 60 秒就是 60 秒，不是 1-2 倍）。

相位分配一次后**永久保持**（记在数据文件的 `next_at` 里，用 `next_at +=
interval` 递推，不用"now + interval"重算——后者会把刷新耗时累积进周期、相位
还会漂）。serve 重启也保持：重启后只有"相位已到"的池会被请求，不会一次性
打一批。

**解法二：轮内最小间隔**。首轮（还没分配相位）时请求仍会密集 → 相邻两个请求
之间强制间隔，缺省 **500ms**，可按 provider 配：

```jsonc
"quota": { "source": "newapi", "minRequestGapMs": 1000 }
```

**解法三：429 指数退避**。命中 429 → 自动把该 provider 的刷新间隔拉长：

```
1min → 2min → 4min → … → 封顶 30min
```

- 厂商给了 `Retry-After` 响应头 → 取它与退避时长的**较大者**
- 退避状态**写在数据文件里**（顶层的 `backoffs` 段，**按额度池**记）→ serve 与
  `ccl quota --refresh` 两个写者都遵守，跨 serve 重启也生效
- **按池而不是按 provider 名**：限流是厂商针对**那把 key** 的，额度也是按 key
  分的。provider 换了 key = 换了池 → 老退避不该拦住它（否则用户修好了 key 还要
  干等退避结束）
- **退避期间条目保留** → `fetched_at` 变旧 → stale 自动生效（用户看得出"数据
  不新鲜"，而不是"额度没了"）
- 连续成功一次后**复位**（下次 429 从 1min 重新开始）
- `ccl quota --refresh` **也遵守**退避（手动刷冲掉退避等于让限流持续），并提示
  还剩多少秒可再试

**已知的边界**：厂商限流阈值因部署而异（有的按"每分钟 N 次"，有的按"滑动
窗口"）。相位错开 + 轮内间隔能覆盖绝大多数；若某个厂商特别严格，把它的
`refreshInterval` 调大、`minRequestGapMs` 调大是最直接的缓解。

## 状态栏怎么用

状态栏脚本**只调命令或打端点，不直接读数据文件**（文件位置与格式是 ccl 的内部
细节）。**优先走 daemon 的回环端点**（零进程，几 ms），命令兜底——见
「[零进程读额度](#零进程读额度daemon-的只读端点)」里的推荐写法。

**两条分支不合并**：

| 来源 | 缺一个维度 | 含义 | 该怎么办 |
|---|---|---|---|
| ccl quota 的 `dimensions` | 厂商就没这个限制 | 确定没有 | **不显示**（不占槽位） |
| Claude Code 的 `rate_limits` | 官方没填这个槽位 | 可能是"没这个限制"，也可能是"这次没拿到" | **保留 `--`**（保持今天行为，不猜） |

合并会把这两个语义抹平。分支 B 保留 `--` → **Anthropic 官方 provider 的输出
一个字节都不变**。

```js
// 分支 A：ccl quota 退出码 0 → 完全用 quota 的维度清单
// 分支 B：ccl quota 退出码非 0 → 保持今天 Claude Code 的 rate_limits 两个固定槽位
```

**超时保护**：`execSync` 的 timeout 设 **250ms**（Claude Code 状态栏有 300ms
去抖窗口且每次执行前 abort 上一次——超时 10 分钟不是瓶颈，300ms 窗口才是）。
走端点时给 **200ms** 就够（回环请求是毫秒级）。任何失败**静默回落**，状态栏
不能因为额度查询卡住或崩掉。

> **为什么不能只留命令那条路**：Windows 上命令的 p50 是 170ms，而预算只有
> 250ms——**余量 60~80ms**。系统繁忙时必然超时 → 状态栏回落到 Claude Code 自己的
> `rate_limits` → 用户看到"额度显示成默认的用量"，而且**只在忙的时候错**。

**渲染约定**：**label 已经是 unit 时不要重复显示单位**（非窗口型维度的 label
就是 unit）——`"CNY: 5.17 CNY"` 应渲染成 `"CNY: 5.17"`。

**颜色阈值**：额度条沿用 Claude Code `rate_limits` 的既有阈值（≥90 红闪 /
≥75 红 / ≥50 黄 / 其余绿），与上下文条（`ctx`，≥60 转黄）**各用各的**——
上下文接近上限才是真问题，额度是消耗类指标宁可早一点预警。

**调用极其频繁是常态**：状态栏命令在生成过程中约每 300ms 被调一次（每秒 3.3
次）→ 设计必须假设这一点。**读取侧只读本机数据**（走回环端点或直读文件），
**一次向外网的请求都不发**。

## 外部行为实测结论

额度接口是**外部行为**——厂商改了字段或端点，ccl 就读不出来。

**2026-11-06 实测基线**（改动实现前先看这里）：

| 厂商 | 端点 | 成败判据 | 错误体形状 |
|---|---|---|---|
| MiniMax | `www.<host>/v1/token_plan/remains` | `base_resp.status_code == 0` | **HTTP 200 也可能是错误**（`1004` 登录失败 / `2049` key 无效） |
| DeepSeek | `<host>/user/balance` | HTTP 状态码 | **可能是纯文本**（无 Authorization 头时 `Authentication Fails (governor)`） |
| New API 系 | `<base_url>/api/usage/token/` | body 的 `code == true` | 认证层 `{"error":{…,"type":"new_api_error"}}` + 401；API 层 `{"code":false,"message":…}` |

各家的响应形状（实测样本见 design.md）：

- **MiniMax**：`model_remains[]` 里取 `model_name == "general"` 的条目；窗口
  时长从 `end_time - start_time` **算**（实测恒为 18000000ms = 5h，周窗口
  604800000ms = 7d）——**不许写死 "5h"/"7d"**；剩余百分比要转成已用百分比
  （`100 - remaining`，clamp 到 [0,100]）；`remains_time` 是**距重置的毫秒数**
- **DeepSeek**：`balance_infos[0].total_balance` 是**剩余**金额（不是已用），
  **字符串**要转 float；**没有总额度** → 算不出百分比 → kind = `balance`
- **New API**：`total_granted` 是**建令牌时设的上限**（分母），`total_used` 是
  已用，`total_available` 是剩余；三者**代数自洽**（granted - used =
  available）；单位换算除以 `quotaPerUnit`（默认 500000）

### 厂商改了字段怎么办

**降级而不是崩**：

1. 解析失败发生在 **serve 侧** → 旧条目保留（`fetched_at` 变旧 → 自动
   `stale: true`）+ 错误进 serve 日志
2. 读取侧完全不受影响——`ccl quota` 仍返回旧数据（标 stale）
3. **绝不允许**额度查询失败影响 Claude Code 启动（额度是锦上添花，provider
   能不能生效才是生死线）
4. 状态栏拿不到数据就保持 `--`，不崩

要修的话：改 `internal/quota/adapter_<厂商>.go` 的解析，重新验证，并把新的实测
结论更新到本节。**升级 Claude Code 后也要重验一遍**（Claude Code 升级不影响
额度接口，但会影响 `--settings` 的交付语义）。

### 已知偏差

- **`quotaPerUnit` 默认 500000 是"假设默认值"**：实测某实例的 `quota_per_unit`
  恰好等于 New API 出厂默认值，但通用 adapter **不应假定能访问 `/api/status`**
  （该路径在网关部署下被 403）→ 已知偏差时显式配置 `quotaPerUnit`
- **`model_limits` 启用后的语义未实测**：实测 `model_limits_enabled: false`；
  启用后 `total_*` 仍是**令牌级总量**，不随 model 分池
- **`current_interval_status` / `current_weekly_status` 的取值语义未知**
  （只见 `1`）：当前**不解释**，原值进 `dimension.raw`
- **New API 的 API 层失败体形状未实测**：需同时容忍 `code:false` 与
  `error` 对象两种
