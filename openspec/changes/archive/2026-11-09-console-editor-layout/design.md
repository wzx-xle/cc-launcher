## Context

### 现状（v0.21.0 真实浏览器实测）

provider 视图的 DOM 结构：

```
#view-providers
  .page-head        provider 名 + 文件路径
  .quota-card       .quota-head(额度 + 刷新按钮) + .quota-dim×N + .quota-note
  (div)             编辑器宿主          ← 普通 block div
    .editor         .editor > (pre.hl + textarea)
  .editor-foot      状态提示 … spacer … [复制][删除][保存]
```

Claude 视图同构（`.page-head` → 编辑器 → `.editor-foot`）。

关键 CSS（`internal/web/assets/style.css`）：

```css
.editor textarea { min-height: 340px; max-height: 62vh; resize: vertical; }
```

textarea 的 auto 高度由 `rows`（默认 2）决定、**不随内容增长**（实测塞 200 行仍是
340px，内容在内部滚），所以盒子被 `min-height` 抬到 340px；`max-height` 只在视口
< 548px（62vh < 340）时才参与，那时 min 赢。**结论：高度被钉死 340px。**

滚动结构：`.shell { min-height: 100vh }`。`#content` 的 `overflow-x: hidden` 让
`overflow-y` 的 computed 值变成 `auto`（`visible` 被算成 `auto`）——**但它今天不
内部滚，纯粹因为它高度是 auto**（内容撑开）。实测 `content.scrollHeight ==
content.clientHeight`（560 视口 506/506；700 视口 1566/1566）。

`web-usage-stats` 的 spec **只约束横向滚动**（"页面本身不横向滚动、宽表在自己容器里
滚"），**没有**任何纵向滚动容器条款 → "纵向由页面自己滚"是本次**自设**的约束。

### 约束

- **永远不给 `#content` 一个确定高度**（它一旦有了确定高度 + `overflow-y: auto`，
  用量视图的长内容就会变成内部滚动，滚动条从窗口挪进内容区）。本次**不动** `#content`
  的任何盒模型属性。
- **零构建步骤**：纯手写 CSS/JS，资源 `go:embed` 进二进制。
- 额度渲染口径与终端状态栏对齐的约束（`web-config-console`）不受本次影响——本次不改
  `dimKind` / `dimText` / `pctColor` 任何逻辑。

## Goals / Non-Goals

**Goals**

- 编辑器吃掉视口的剩余空间，窗口缩放自动跟随；**矮窗口下不低于现状**
- 操作按钮从编辑器下方移到编辑区标题行（视线不用来回跑），**保存排最前**
- 状态提示与按钮同在标题行，编辑器下方整行让给编辑器
- 额度卡的「刷新」按钮推到标题行最右
- 修跨渲染竞态与「额度刷新失败抹掉刷新按钮」两个既有 bug

**Non-Goals**

- 不做代码折叠 / 行号 / 多标签页等编辑器增强
- 不改编辑器本体（仍是透明 textarea 叠高亮层，`editor.js` 的滚动同步逻辑不动）
- 不动用量视图的任何布局与滚动行为
- 不动后端（本次**零** Go 代码改动）
- 不引入浏览器端自动化测试设施（见「Risks」）

## Decisions

### 决策 1：高度用「视图最小高度下限 + flex 分配」，不是「精确 flex + 内部滚动」

| 方案 | 做法 | 代价 |
|---|---|---|
| A 精确 flex | `.shell { height: 100vh }` + `#content { overflow-y: auto }` + 编辑器 `flex: 1` | 编辑器**精确**填满，但 `#content` 变内部滚动容器 → **用量视图的滚动条从窗口挪进内容区** |
| **B 下限 + flex** | 视图给「至少占满剩余视口」的高度下限，内部 flex 分配 | 编辑器高度是近似的；**用量视图滚动行为一字不变** ✓ 已实测 |
| C 切视图换 class | JS 在编辑视图激活时给 `#content` 换 class | 精确且不影响用量视图，但视图切换瞬间滚动容器会变，可能跳一下 |

**选 B**，已实测验证：加新规则前后用量视图完全一致（`docSH 1663`、
`contentSH == contentCH == 1566`、`contentScrollsInternally=false`、隐藏的编辑视图
`display:none`）。`usage.js` 全文无 `scrollTop` / `clientHeight` / `scrollHeight`
（只在 `usage.js:143-145` 用 `window.innerWidth/innerHeight` 定位 tooltip），
不依赖任何具体滚动容器 ✓。

**下限值 = 122.5px**（不是拍脑袋的 120）：

```
顶栏（实测）    56.5px   padding 12+12 + 内容 31.5 + border 1
#content padding-top   18px
#content padding-bottom 48px
                ─────
                 122.5px
```

用 **122.5px**（不取整）：取整到 122 会让总高 900.5 → `scrollHeight` 向上取整成 901
→ 多出 1px 的竖向滚动条；123 则总高 899.5（底部留白略少）。122.5 正好让总高 = 视口高，
滚动条不出现（实测 900 视口下 `scrollHeight === innerHeight === 900`）。

> 这是**空间充足时**的账。空间不足时（编辑器触发 340px 下限），总高必然超出视口 →
> 出现滚动条 —— 这正是决策 2 接受的取舍。

**这是魔数，代价与缓解**：顶栏高度依赖字体与是否换行——**380px 宽的窄视口下顶栏会
换行成 2 行（56.5 → 96.5px，差 40px）**，真实开销变成 162.5px。缓解：偏差只让编辑器
少高几十像素，不影响可用性（内容都在，flex 分配不重叠不溢出）；窄视口本来就可能
有滚动条。**不使用 JS 测量**——那会把布局变成运行时计算，resize 要重算。

### 决策 2：编辑器可见高度有下限 = 现状的 340px（不回退）

纯"吃掉剩余空间"在矮窗口下会让编辑器**比现在更矮**：实测 493px 视口下新方案只有
**163px**，而现状是写死的 340px——**「长内容看不全」这个原始诉求在矮窗口下反被
加重**。

所以给 **`.editor`（编辑器本体）** 一个 `min-height: 340px`（= 现状值）：

| 视口高 | 编辑器高度 | 页面 |
|---|---|---|
| 493px | 340px（= 现状，不回退） | 出现竖向滚动条（**现状本来就有**） |
| 700px | 340px（下限生效：额度卡占 149px 是固定开销） | 出现竖向滚动条（总高 702 > 700） |
| 900px | 538.5px（撑满剩余） | 无滚动条（900 === 900） |

**代价**：窗口不够高时多一条竖向滚动条。这不是新问题——现状总高 690 > 视口 493，本来
就滚。放弃的是"页面几乎不出现滚动条"这个自加的卖点，换"任何窗口下编辑器都不比现在
更矮"。

> **下限必须挂在 `.editor` 上，不能挂 `.editor-zone`**：zone 里还有一行标题行
> （≈53px），挂在 zone 上时**编辑器本体**只剩 340 − 53.5 = 286.5px → 比现状还矮
> （实测踩过）。

### 决策 3：删掉「编辑器宿主」div —— 否则 flex 链断，编辑器塌成 2px

**这是本设计里最容易照着 tasks 写错的一处。**

`providers.js:367` / `claude.js:93` 把编辑器宿主建成**普通 block div**，`editor.js:43`
再把 `.editor` 挂进它 → `.editor` 的父不是 flex 容器 → `.editor { flex: 1 }`
**完全无效**。实测：

| 结构 | `.editor` 高度 | textarea |
|---|---|---|
| `.editor-zone > (.editor-head + 宿主 div > .editor)` | **2px** | 24px |
| `.editor-zone > (.editor-head + .editor)` | **258px** | 256px |

**照原样实施 = 编辑器消失。** 所以：宿主 div 删掉，`.editor` 直接挂 `.editor-zone` 下。

（`editor.js` 会把 `.editor` 挂进传入的容器——传 `.editor-zone` 本身即可，不需要额外
的 div。）

### 决策 4：textarea 绝对定位的真正理由是「父级要有确定高度」，不是「百分比高度解析」

原 design 写的理由（"不依赖父元素百分比高度能否解析"）**是错的**——实测
`inset: 0` 与 `height: 100%` **一起塌**，塌的根因是**父级没有确定高度**（`.editor`
塌成 2px、只剩边框）。

真正的保险是 **`.editor` 自身有确定高度**（由 flex 链给，见决策 3 + 决策 5）。绝对定位
本身仍有价值：它让 textarea 精确铺满 `.editor` 的 padding box，不依赖百分比高度。

实测绝对定位后：两层几何完全一致（各 587.19px 高，content box 都 563.19px）→ 字体
度量与基线对齐不变 → **`editor.js:74-77` 的滚动同步仍然成立**（实测 `ta.scrollTop=120`
→ `pre.scrollTop=120`）✓ 层叠顺序也正确（`editor.js:41-43` 是 pre 先 ta 后，textarea
在上 → 插入符可见、点击能落到输入框）。

### 决策 5：兜底高度落在 `.editor-zone` / `.editor`，**不落在 textarea**

textarea 改成绝对定位后**不占 `.editor` 的内容高度**。若给 textarea 加 `min-height`
兜底，`.editor` 塌成 2px 时 textarea 会超出，而 `.editor` 是 `overflow: hidden`
（style.css:588）→ **整个编辑器被裁成一条缝，比塌成 0 更糟**。

所以下限的归属是：

- `.editor { flex: 1; min-height: 340px }` —— 决策 2 的下限（**编辑器可见高度**；
  必须挂在 .editor 上而不是 .editor-zone，见决策 2 的说明）
- `.editor-zone { flex: 1; min-height: 0 }` —— zone 自身可以收缩

textarea 自己**不留** `min-height` 兜底。

另外 `resize` 必须**显式写 `none`**：textarea 的 UA 默认样式就是 `resize: both`，只把
`vertical` 删掉等于退回默认（还能拖）——实测 computed 会变成 `both`（实施时踩过）。

### 决策 6：`:not([hidden])` —— ID 选择器会盖过隐藏规则

```css
.view[hidden] { display: none; }      /* 特异性 (0,2,0) */
#view-providers { display: flex; }     /* 特异性 (1,0,0) —— ID 更大，赢！ */
```

**ID 选择器特异性更高** → 隐藏的视图照样 `display: flex` 显示出来（**两个视图叠在
一起**）。实测：隐藏态的 `#view-claude` 加裸 ID 规则后 `getComputedStyle().display
=== "flex"`、高度 440px —— 坑是真的。

所以必须写成：

```css
#view-providers:not([hidden]), #view-claude:not([hidden]) { display: flex; ... }
```

实测有效：隐藏时 `display: none`、高度 0；显示时 `display: flex` ✓

前提成立：`app.js:45-50` 切视图靠 `panel.hidden = v.id !== name`（HTML `hidden`
属性），不是 class ✓。另外全文扫描确认**目前没有第二条会盖过 `.view[hidden]` 的规则**
（`#view-usage` 只设 `max-width`/`margin`，不设 `display`）→ 只要新增的两条 ID 规则都
带 `:not([hidden])`，就没有第三个冲突源。

### 决策 7：按钮与状态提示移到编辑区标题行，保存排最前

```
.editor-zone
  .editor-head
    h3        类型标题（provider: JSON；claude: JSON / Markdown）
    span      状态提示位（.hint / .hint.ok / .hint.err）
    span.spacer
    button    [保存] [复制] [删除]      ← provider
    button    [保存] [放弃修改] [格式化] ← claude
```

- **保存排最前**：主操作在前，视线落点即主操作
- **状态提示放标题行**：提示与触发它的按钮同处一行；底部整行让给编辑器
- `.quota-head` 与 `.editor-head` **合并为一条 CSS 规则**（`display:flex;
  align-items:center; gap:10px`）+ 共享 `.spacer { flex: 1 }` → 两处观感统一，且
  不靠"看起来像"来验收
- `.editor-head .hint { margin: 0 }` —— `.hint` 自带 `margin: 6px 0 0`，放进 flex 行会
  与 h3 / 按钮错位 6px
- `.editor-head` 必须 `flex-wrap: wrap` —— 「已保存。下次启动 ccl 生效」约 28 个汉字
  （≈350px），窄视口会把按钮挤到第二行

### 决策 8：额度卡刷新按钮右对齐用 spacer，不用 `justify-content: space-between`

两种都能把按钮推到右边，选 spacer 是为了**与 `.editor-head` 同一套标题行模板**，也避免
`space-between` 在标题换行时把按钮甩到奇怪的位置。

**注意**：spacer 是 **DOM 元素**，光加 CSS 规则匹配不到任何节点 → `providers.js` 的
`renderQuota` 必须**在 h3 与刷新按钮之间插入一个 `el('span','spacer')`**，否则按钮
仍然紧贴「额度」。

### 决策 9：修跨渲染竞态 —— render 序号守卫

`providers.js` 的 `editor` / `statusHost` / `quotaHostEl` 是**模块级变量**，
`renderMain` 每次重建 DOM 并重新赋值 → 引用本身不会失效，但**迟到的响应会写进新一
轮的 DOM**：

- 旧 provider 的 GET 返回后 `if (editor) editor.setValue(data.content)` —— 此时
  `editor` 已是**新 provider** 的 → 旧配置灌进新编辑器，`state.content` / `state.etag`
  也被覆盖 → 用户点保存就存成旧的
- 旧额度 GET 返回后 `renderQuota(quotaHostEl, q)` → 旧额度画进新卡
- 旧请求的 `setStatus` → 旧错误出现在新 provider 的标题行

**修法**：`renderMain` 里用**局部** `const ed = createEditor(...)` + 一个自增的 render
序号，回调里比对序号一致才写。仓库里已有正确范式可参照：`web-usage-stats` 的 spec 要求
在途请求必须被取消（`usage.js` 用 `AbortController`）——但渲染层用序号守卫更轻（不必
真的 abort，且能防住非 fetch 的异步）。

`claude.js:146-152` 同样。

### 决策 10：额度刷新失败只换内容区，保留标题行

`providers.js:262-273` 的错误分支 `host.textContent = ''` 把**整张额度卡**换成一行
提示 → `.quota-head`（含刷新按钮）一起消失 → 用户没法重试。

修法：错误提示渲染到卡片的**内容区**（`.quota-dim` 那一层），`head` 不动。本次正好
要动额度卡标题行，顺手修。

## Risks / Trade-offs

| 风险 | 缓解 |
|---|---|
| **`.editor` 与 `.editor-zone` 之间隔了普通 div → `flex:1` 断链，编辑器塌成 2px** | **决策 3 专门记了这处**（附实测对比表）；tasks 里要求删掉宿主 div；验证任务实测 `.editor` 高度 > 0 |
| 魔数 122px 不准（用户改字号 / 顶栏换行） | 窄视口实测顶栏换行会让真实开销变成 162.5px（差 40px）。缓解：只影响编辑器少高几十像素，内容不重叠不溢出；窄视口本来就可能有滚动条 |
| 矮窗口下多一条竖向滚动条 | 决策 2 明确取舍：换"编辑器任何窗口下都不比现在更矮"。现状本来就有滚动条，不是新问题 |
| 用量视图的滚动行为被误改 | 决策 1 选 B 的核心理由：**#content 的任何盒模型属性都不动**（护栏写成"永远不给 #content 确定高度"，因为它的 computed `overflow-y` 实际已是 auto）；验证任务实测 `content.scrollHeight == content.clientHeight` |
| `:not([hidden])` 漏写 → 两个视图叠着显示 | 决策 6 专门记了这个坑（附实测）；验证任务专门查另一视图的 computed `display` |
| textarea 绝对定位后高亮层/滚动同步失效 | 决策 4 已实测：两层几何一致、`ta.scrollTop` → `pre.scrollTop` 同步仍成立、层叠顺序正确（textarea 在上） |
| 兜底 `min-height` 放错元素 → 编辑器被裁成一条缝 | 决策 5 明确下限归属（`.editor-zone`），textarea 不留兜底 |
| **本 change 无自动化测试覆盖** | 仓库里**没有任何浏览器测试设施**（`package.json` 是 0 字节空文件、无 node_modules、无 playwright 依赖）；`node --test internal/web/jstest/*.test.mjs`（32 passed）只测纯函数，**验证不了任何布局**。本次验收 = tasks 第 4 节的**人工 playwright 实测**（与 v0.21.0 那次真机验证同款，Playwright MCP 驱动）。这在本仓库有先例，但**必须在 tasks 里写明**，免得归档时误以为有测试守着 |
| 改竞态可能引入新问题 | 序号守卫是纯前端局部改动；修完必须手动切几个 provider 验证"慢响应不串台" |

## Migration Plan

纯前端改动，无数据迁移、无接口变更、无配置开关。回滚 = revert 前端资源即可
（资源随二进制走，旧二进制自带旧布局）。

## Open Questions

- 122px 与 340px 两个数在实施后要**实测校准**（tasks 4.6/4.7），不同浏览器/字号下顶栏
  高度可能差几像素
- 验证用的具体视口档位在 tasks 里写死（493 / 700 / 900 三档），实施时若发现档位不够
  可加
