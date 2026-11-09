## Context

### 现状

| 组件 | 位置 | 能力 |
|---|---|---|
| 看板页面 | `internal/web/assets/`（index.html 76 行 + app.js 680 行 + style.css 362 行） | **只读**，`/api/stats` 一个接口 |
| 鉴权 | `internal/web/auth.go` | token → session cookie（HMAC 签名，7 天 TTL），`ccl web --rotate` 轮换即吊销 |
| 挂载 | `cmd/cc-launcher/serve.go:142` | 挂在 daemon 的 `:4318`（**全网卡**），不新增端口/进程 |
| provider 管理 | `internal/cli/cli.go` + `internal/provider` | CLI 侧齐全（新建/删除/校验/模板/切换）；文件在 `~/.cc-launcher/providers/*.json` |
| settings.json | `internal/config/config.go` | 有 Manager + 备份（`~/.cc-launcher/backups/settings-*.json`） |
| 额度 | `internal/quota` | serve 周期刷新写 `~/.cc-launcher/quota.json`；`ccl quota` 只读本地文件，零网络 |

### 目标

`/ui` 变成三视图控制台：用量（复用）、提供商（可编辑）、Claude（用户级两文件可编辑）。

### 四条硬约束

1. **没有前端构建步骤。** 资源 `go:embed` 进二进制，CI（`.github/workflows/release.yml`）
   只有 `go build` + `zip`。引入构建会动发布链路，而发布链路有 **byte-level 可重现**
   的约定（去 BuildTime + `-trimpath` + `touch -d @0` + `TZ=UTC zip -X`），scoop
   manifest 的 hash 依赖它（`build.sh bump-scoop` 与 CI 要算出同一个 hash）。
2. **不能影响 Claude Code 启动路径。** 网页只是另一个入口；启动链路的既有契约
   （provider 走 `--settings` 交付、schema 校验不过就拒绝启动）一个字不改。
3. **凭证在配置文件里是明文**（`ANTHROPIC_AUTH_TOKEN` 等）。网页把它们显示出来，
   等于把凭证搬到了浏览器能看见的地方。
4. **Claude Code 也在写 `~/.claude/settings.json`。** 网页和它会抢同一个文件。

---

## Decisions

### 决策 1：外壳 + 三视图，前端零依赖零构建

`app.js` 按视图拆成多个文件（`go:embed` 本来就支持目录）：

```
internal/web/assets/
  index.html      外壳骨架（顶栏 + 左栏容器 + 主区容器）
  style.css       现有样式 + 三视图布局
  app.js          入口：路由 + 外壳渲染
  usage.js        用量视图（**现有 app.js 的渲染逻辑整体搬进来**）
  providers.js    提供商视图
  claude.js       Claude 视图
  editor.js       原文编辑器（textarea + 高亮 overlay + 乐观锁）
  highlight.js    JSON 语法高亮
```

路由用 **hash 路由**（`#/usage`、`#/providers`、`#/claude`）——服务端不需要 SPA
回退逻辑，也不怕刷新时 404。

**为什么不用框架**：见约束 1。引入 esbuild + React/Vue 要么改 `release.yml`
（连带重验 scoop hash 约定），要么把构建产物 commit 进仓库（源码与产物可能不同步，
得再加一道 CI 校验）。为了三个视图引入这一整套，不划算。

**为什么不用 CDN 引框架**：离线不可用（这台机器连厂商 API 都得配代理），且引入
第三方远程脚本等于引入供应链风险。

### 决策 2：provider 用原文 JSON 编辑 + 自写语法高亮

用户明确要求：provider 先做原文编辑，带语法高亮，凭证不掩码。

**高亮实现**：透明 `textarea` 叠在预渲染层上（两层共享同一套字体度量，滚动同步）。
JSON 的 token 种类很少（key / string / number / bool / null / 标点），tokenizer
一百来行足够。

**必须转义后再上色**：provider JSON 里有凭证、可能有 `<` `&` `"`——高亮层用
`textContent` 逐 token 构造或先做 HTML 转义，**不能把原文直接塞 `innerHTML`**。

**保存前必须过 `settingsschema.Validate`**：已知键类型写错会让 Claude Code 丢弃
**整个** settings 文件（连带 provider 凭证一起消失）。网页是"写之前拦住"的最后
一道，比启动时才发现强。校验不过 → 400 + 错误列表，**不写盘**。

### 决策 3：Claude 段只管用户级的两个文件

`~/.claude/settings.json` + `~/.claude/CLAUDE.md`。

**排除 `~/.claude.json`**（80KB 的全局状态文件）：

- Claude Code 高频写它 → 乐观锁会一直撞
- 它是状态不是配置
- 里面有 `oauthAccount`（OAuth 账号信息）、`machineID` / `userID`（主机指纹）

**排除项目级**：网页挂在 daemon 里，是全局的，不知道"当前项目"是哪个；而且项目级
文件跟着 git 工作区走，在网页上改完容易和 git 打架。

**排除 `~/.claude/settings.local.json`**：用户级几乎不用（常规操作在项目级）。

### 决策 4：所有写操作带乐观锁（ETag = 文件内容 sha256）

读接口返回 `etag`（内容 sha256 前 16 位），写接口必须带 `If-Match`；对不上 → **409**
+ 当前内容，前端提示"文件已被其他程序修改，请重新加载"。

**为什么必须有**：Claude Code 自己会改 `~/.claude/settings.json`（写权限、hook
配置等），ccl 的 TUI 也会。网页读完之后用户去终端改了东西，网页再存回去就把人家
的改动抹了——而且是静默的：用户看着"保存成功"，实际丢了一次配置。

### 决策 5：写之前先备份，写用原子替换

| 目标 | 备份 | 写入 |
|---|---|---|
| `~/.claude/settings.json` | 复用 `internal/config.Manager` 的既有备份机制 | 原子写（tmp + rename） |
| `~/.claude/CLAUDE.md` | 新增：复用同一个备份目录 | 原子写 |
| `provider/*.json` | 新增：`~/.cc-launcher/backups/provider-<name>-<ts>.json` | 原子写（**当前是裸 `os.WriteFile`**） |

备份目录统一在 `~/.cc-launcher/backups/`，保留最近 N 份（`config.Manager` 已有这个
逻辑，沿用）。

### 决策 6：额度卡默认展示当前值 + 手动刷新按钮

**读**走 `internal/quota` 的读取侧（`ccl quota` 用的同一个函数）→ **一次网络都不发**，
serve 每 60s 写一次 `quota.json`，网页 30s 轮询（跟用量页一致）。

**手动刷新**走 `quota.RefreshOne`（`ccl quota --refresh` 用的同一个函数）→ 联网查
一次并写回数据文件。**必须遵守退避**：被厂商限流（429）时返回 `BackoffError` →
HTTP 429 + 剩余退避秒数，UI 显示"厂商限流中，X 分钟后可重试"。退避状态存在数据
文件里，跨进程跨重启生效——网页刷新也绕不过去（这是对的）。

**三种状态必须分开画**：

| 状态 | 显示 |
|---|---|
| 有条目且新鲜 | 正常额度卡 |
| 有条目但 `stale` | 灰显 + "N 分钟前采集" |
| **没有条目** | **"近期无活动，未采集额度"** |

第三种最容易骗人：serve 只刷新"最近有活动"的 provider（活动信号来自 OTel，
5 分钟窗口）。"没数据"被当成"无限额"，然后放心把量用爆。**"没采到" ≠ "没限制"**。

**渲染口径必须和状态栏一致**：同一套阈值（90/75/50 → 红/黄/绿）、同一种条形图
（`█░`）、同一种"已用/总量"格式。否则网页显示 72%、状态栏显示 68%，用户得开始
怀疑哪个是真的——**两个数打架的界面比没有界面更糟**。

**手动刷新是"按需"**：serve 的周期刷新受"只刷活跃 provider"限制，网页的手动
刷新不受——用户点哪个 provider 就查哪个（这正是用户要的"按需刷新"）。

### 决策 7：路径白名单（不接收任意路径）

- **provider 名**：`^[A-Za-z0-9._-]{1,64}$`，且不含 `..`；路径由
  `filepath.Join(providersDir, name+".json")` 拼出来后**再校验前缀**在
  `providersDir` 之内
- **Claude 文件**：**不接受路径参数**，只有两个固定标识（`settings` / `claude-md`），
  服务端映射到固定路径
- 绝不接受请求里传来的绝对路径或 `..`

### 决策 8：暴露面不变，写接口额外做同源检查

用户决定：鉴权与部署照旧（同一个 `/ui`、同一个 token、同一端口）。

补一道 **CSRF 闸**：写接口要求 `Content-Type: application/json` 且 `Origin` 与
请求同源。session cookie 是 `SameSite=Lax`，跨站表单发不了 JSON；再叠 Origin 检查
是双保险。

**残余风险要说清楚**：daemon 监听 `:4318` 是**全网卡**的，`ccl web` 会打印 LAN
IP 的 URL——也就是说**同一个局域网里拿到 token 的人，现在能改你的 provider 配置
（含明文凭证）和 CLAUDE.md**。缓解手段现成：`ccl web --rotate` 立刻吊销全部
session。但这是"知情接受"，不是"已消除"。

---

## API 清单

所有接口都在现有 `protect` 中间件后面（token → cookie）。

### 用量（不变）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/stats` | 现有接口，参数与返回结构不变 |

### 提供商

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/providers` | 列表：`name` / `order` / `note` / `hasQuota` / `hasCredential` |
| GET | `/api/providers/{name}` | 原始 JSON **文本** + `etag` |
| PUT | `/api/providers/{name}` | 保存（body = JSON 文本；带 `If-Match`；先校验再备份再原子写） |
| POST | `/api/providers` | 新建：body = `{template?, name?, content?}`（模板 → `internal/cli` 已有实现） |
| POST | `/api/providers/{name}/copy` | 复制一份（body = 新名字） |
| DELETE | `/api/providers/{name}` | 删除（连带清 `quota.json` 里该 provider 的条目） |
| GET | `/api/providers/{name}/quota` | 当前额度值（零网络）+ `stale` + `fetched_at` |
| POST | `/api/providers/{name}/quota/refresh` | 手动刷新（联网；退避中 → 429 + 剩余秒数） |

### Claude

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/claude/files` | 两个可编辑文件的标识与元信息（路径、大小、mtime） |
| GET | `/api/claude/file/{id}` | 原文 + `etag`（`id` ∈ `settings` / `claude-md`） |
| PUT | `/api/claude/file/{id}` | 保存（`If-Match`；`settings` 先过 schema 校验；先备份再原子写） |

---

## 前端结构

```
┌─────────────────────────────────────────────────────────────┐
│ ccl 看板    [ 用量 ] [ 提供商 ] [ Claude ]        ◐ 主题     │  顶栏（外壳）
├──────────────┬──────────────────────────────────────────────┤
│ 左栏          │  右栏 / 主区                                 │
│ （对象列表）  │  （详情 / 编辑区 / 看板）                      │
└──────────────┴──────────────────────────────────────────────┘
```

- **用量视图**：左栏**留空**（主区全宽），现有看板整块搬进来，行为一字不改
- **提供商视图**：左栏 = provider 列表（按 `order` 排，`note` 作副标题）+「+ 新建」；
  右栏 = 额度卡（顶部）+ 原文 JSON 编辑器 + 「保存」「删除」「复制」
- **Claude 视图**：左栏 = `settings.json` / `CLAUDE.md`；右栏 = 原文编辑器
  （`settings.json` 额外给「格式化」+ 保存前 JSON 合法性提示）

---

## Risks / Trade-offs

| 风险 | 缓解 |
|---|---|
| token 语义从"读"扩到"写" | 同 token 吊销现成（`--rotate`）；写接口同源检查；设计文档明示残余风险 |
| 覆盖别人的改动（Claude Code / TUI 同时在写） | 乐观锁（决策 4），冲突 → 409 而不是静默覆盖 |
| 写坏配置导致 Claude Code 丢弃整个 settings 文件 | 保存前 schema 校验（决策 2）；每次写前备份（决策 5） |
| 额度"没采到"被误读成"无限额" | 三种状态分开画（决策 6） |
| 网页与状态栏显示的额度不一致 | 共用读取侧 + 共用渲染口径（决策 6） |
| provider 写坏了 Claude Code 起不来 | 校验 + 备份；备份在 `~/.cc-launcher/backups/` |
| 前端重构回归（现有看板坏掉） | 用量视图**整块复用**现有渲染逻辑，只搬位置不改逻辑；回归测试沿用 `web_smoke_test.go` 的路子 |

**明确不做（non-goals）**：

- 不做"切换当前 provider"（网页改 `state.json` 的 active provider 对用户没价值）
- 不做项目级配置的编辑
- 不编辑 `~/.claude.json`
- 不做用户级 `commands/` / `skills/` 的编辑
- 不引入前端框架 / 构建步骤
- 不改 daemon 的监听地址、不加第二套鉴权

---

## What's left for the builder

- 前端拆文件后的具体 CSS 布局（网格 / flex 的取值）
- JSON 高亮器的配色（跟随现有 `style.css` 的 light/dark 变量）
- 编辑器 overlay 的滚动同步与字体度量细节
- `internal/provider` / `internal/config` 补原子写 + 备份时的函数签名
