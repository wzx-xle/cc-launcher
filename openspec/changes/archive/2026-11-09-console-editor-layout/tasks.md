## 1. 样式：标题行与高度链

- [x] 1.1 `internal/web/assets/style.css`：把 `.quota-head` 与新增的 `.editor-head` **合并为一条规则**（`display: flex; align-items: center; gap: 10px; flex-wrap: wrap`）+ 共享 `.spacer { flex: 1 }`（两处观感统一靠**同一条规则**保证，不靠"看起来像"）；`.quota-head` 的 `align-items` 从 `baseline` 改 `center`
- [x] 1.2 `internal/web/assets/style.css`：`.editor-head .hint { margin: 0 }` —— `.hint` 自带 `margin: 6px 0 0`，放进 flex 行会与 h3 / 按钮错位 6px
- [x] 1.3 `internal/web/assets/style.css`：`#view-providers:not([hidden])` 与 `#view-claude:not([hidden])` 设 `display: flex; flex-direction: column; min-height: calc(100vh - 122.5px)`
      —— **必须带 `:not([hidden])`**：隐藏视图的规则是 `.view[hidden] { display: none }`（特异性 0,2,0），而裸的 `#view-providers`（1,0,0）**特异性更高会盖过它** → 隐藏的视图照样 `display:flex` 显示、两个视图叠在一起（design 决策 6，已实测）
      —— `122.5px` 的来历：顶栏实测 56.5 + content padding-top 18 + padding-bottom 48 = 122.5，**不取整**（取整到 122 会让总高 900.5 → scrollHeight 向上取整成 901 → 多出 1px 竖向滚动条；123 则底部留白略少）
- [x] 1.4 `internal/web/assets/style.css`：新增 `.editor-zone { flex: 1; min-height: 0; display: flex; flex-direction: column }` —— zone 自身可以收缩
- [x] 1.5 `internal/web/assets/style.css`：`.editor { flex: 1; min-height: 340px }` —— `340px` 是**编辑器可见高度的下限**（= 现状值，design 决策 2）
      —— **下限必须挂在 `.editor`（编辑器本体）上，不能挂 `.editor-zone`**：zone 里还有一行标题行（≈53px），挂 zone 上会让**编辑器本体**只剩 340−53.5 = 286.5px → 比现状还矮（实测踩过）
- [x] 1.6 `internal/web/assets/style.css`：`.editor textarea` 改 `position: absolute; inset: 0; width: 100%; height: 100%`；**删掉** `min-height: 340px`、`max-height: 62vh`、`resize: vertical`
      —— textarea 上**不留** `min-height` 兜底：绝对定位后它不占 `.editor` 的内容高度，而 `.editor` 是 `overflow: hidden` → 兜底会让编辑器被裁成一条缝（design 决策 5）
      —— **`resize` 必须显式写 `none`**：textarea 的 UA 默认样式就是 `resize: both`，只把 `vertical` 删掉等于退回默认（还能拖）——实测 computed 会变成 `both`

## 2. provider 视图重排

- [x] 2.1 `internal/web/assets/providers.js`：`renderMain` 重排为「页面头 → 额度卡 → `.editor-zone`」，`.editor-zone` 内是「`.editor-head` → `.editor`」
      —— **必须删掉中间那个「编辑器宿主」普通 div**：`.editor` 的父若是 block 容器，`.editor { flex: 1 }` 完全无效 → 实测编辑器塌成 **2px**（textarea 24px），等于编辑器消失（design 决策 3）
- [x] 2.2 `internal/web/assets/providers.js`：`.editor-head` 内容 = `JSON` 标题 + 状态提示位 + `spacer` + **[保存][复制][删除]**（**保存排最前**）
- [x] 2.3 `internal/web/assets/providers.js`：`setStatus` 改成直接写 statusHost 的 `textContent` + `className`（复用既有的 `.hint` / `.hint.ok` / `.hint.err`），不再往容器里塞 span
- [x] 2.4 `internal/web/assets/providers.js`：删掉底部的 `.editor-foot`（状态与按钮都已上移）；**新建 provider 表单（`renderCreateForm`）的「取消 / 创建」按钮保留在底部** —— 那是表单不是编辑区
- [x] 2.5 `internal/web/assets/providers.js`：`renderQuota` 里在 `h3` 与刷新按钮**之间插入一个 `el('span','spacer')`**
      —— 光加 CSS 匹配不到任何节点 → 按钮仍紧贴「额度」，spec 那条必然不通过（design 决策 8）

## 3. Claude 视图重排

- [x] 3.1 `internal/web/assets/claude.js`：`renderMain` 同样重排为「页面头 → `.editor-zone`」，**同样删掉编辑器宿主 div**，`.editor` 直接挂 `.editor-zone`
- [x] 3.2 `internal/web/assets/claude.js`：`.editor-head` 标题按文件类型取 `JSON`（settings.json）/ `Markdown`（CLAUDE.md）；内容 = 标题 + 状态提示位 + `spacer` + **[保存][放弃修改][格式化]**（保存排最前）
- [x] 3.3 `internal/web/assets/claude.js`：删掉底部的 `.editor-foot`；`setStatus` 与 providers 视图保持同一种写法

## 4. 修既有 bug（都在本次要动的代码上）

- [x] 4.1 `internal/web/assets/providers.js`：**修跨渲染竞态** —— 模块级的 `editor` / `statusHost` / `quotaHostEl` 会被下一轮 `renderMain` 重新赋值，**迟到的旧响应因此写进新 provider 的界面**（旧配置灌进新编辑器 + `state.etag` 被覆盖 → 点保存存成旧的）。修法：`renderMain` 里用**局部** `const ed = createEditor(...)` + 一个自增的 render 序号，回调里**比对序号一致才写**（`editor.setValue` / `state.content` / `state.etag` / `renderQuota` / `setStatus` 五处都要挡）
- [x] 4.2 `internal/web/assets/claude.js`：同样修跨渲染竞态（`claude.js:146-152`）
- [x] 4.3 `internal/web/assets/providers.js`：**修额度刷新失败抹掉刷新按钮** —— 错误分支 `host.textContent = ''` 把整张卡换掉 → `.quota-head`（含刷新按钮）一起消失 → 用户没法重试。修法：错误提示渲染到卡片的**内容区**，head 不动

## 5. 验证（Playwright MCP 手工实测）

> **本 change 没有自动化测试覆盖**：仓库里**没有任何浏览器测试设施**（根目录 `package.json` 是 0 字节空文件、无 node_modules、无 playwright 依赖）；唯一的前端测试 `node --test internal/web/jstest/*.test.mjs`（32 条）只测纯函数，**验证不了任何布局**。本次验收 = 本节的**人工实测**（与 v0.21.0 那次真机验证同款，Playwright MCP 驱动真实浏览器）。
>
> **daemon 已经在 :4318 跑着，不要重复起** —— `serve.go` 启动前会探端口，占用即 EADDRINUSE 退出。访问 `GET /ui?token=<~/.cc-launcher/web-token 的内容>` → 302 到 `/ui` 并种 session cookie。
>
> 前端资源是 `go:embed` 进二进制的 → **改完必须 `go build -o cc-launcher ./cmd/cc-launcher` 并重启 daemon**，否则测的还是旧资源。

- [x] 5.1 **编辑器不再塌陷**：`.editor` 与 textarea 的 `getBoundingClientRect().height` 都 > 0（≥ 250px），且 `.editor` 的**父元素就是 `.editor-zone`**（没有中间宿主 div）
- [x] 5.2 **高度随视口**：三档视口 **493 / 700 / 900** 下量 textarea 高度。判定：**900 档 ≥ 500px**（实测 538.5）；700 档 **≥ 340px**（额度卡占 149px 是固定开销，剩余就这么多 —— 别拿 400px 当阈值）
- [x] 5.3 **矮窗口不回退**：493 档下 `.editor` **≥ 340px**（= 现状，实测 340），且 `documentElement.scrollHeight > innerHeight`（页面可滚，实测 702 > 493）
- [x] 5.4 **滚动语义不变**：切到用量视图，确认 `content.scrollHeight == content.clientHeight`（内容区**不**内部滚）且 `documentElement.scrollHeight > innerHeight`（页面滚）—— 证明没给 `#content` 加确定高度
- [x] 5.5 **隐藏视图不叠**：切到 Claude 视图时 `#view-providers` 的 computed `display === 'none'` 且高度 0（验 1.3 的 `:not([hidden])` 没写错）
- [x] 5.6 **额度卡按钮右对齐**：`.quota-head` 内刷新按钮的 `right` 与 `.quota-head` 的 `right` 差值 ≤ ~20px（只剩 padding 量级）
- [x] 5.7 **标题行结构**：provider 视图的 `.editor-head` 依次是 `JSON` 标题、状态位、`[保存][复制][删除]`，且**编辑器下方没有 `.editor-foot`**（新建表单除外）；Claude 视图标题为 `JSON` / `Markdown`
- [x] 5.8 **保存排最前**：`.editor-head` 里第一个 `button` 的文本是 `保存`
- [x] 5.9 **竞态不串台**：在 provider 列表里**快速连点两个不同的 provider**，确认后者的编辑器内容不被前者的迟到响应覆盖
- [x] 5.10 **额度刷新失败仍能重试**：让额度接口返回 500 → 点「刷新」→ 卡片里**仍能看到刷新按钮**（4.3 的验收）
- [x] 5.11 **编辑器不能拖拽调高**：textarea 的 computed `resize === 'none'`
- [x] 5.12 **顶栏高度实测校准**：在 380px / 780px / 1280px 三种视口宽度下量顶栏高度，确认 `122px` 这个下限在窄视口下的偏差可接受（窄视口顶栏会换行成 2 行 → 真实开销 162.5px，design 决策 1 已记录该偏差）

## 6. 收尾

- [x] 6.1 `go build ./...` + `go vet ./...` + `go test ./...` 全绿（本次零 Go 改动，跑一遍确认没手滑）
- [x] 6.2 `node --test internal/web/jstest/*.test.mjs` 全绿（32 条；布局改动不应影响纯函数）
- [x] 6.3 `docs/web-console.md`：额度卡刷新按钮的位置变了（标题行最右），顺手更新一句
- [x] 6.4 `openspec archive console-editor-layout`
- [x] 6.5 CHANGELOG 段（写在 `[Unreleased]` 下；版本号按改动性质定 —— 布局改进 + 两个 bug 修复，不预设）
