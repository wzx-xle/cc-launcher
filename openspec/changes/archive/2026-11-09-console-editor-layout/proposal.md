## Why

**用户在真机体验控制台时发现三处布局问题**（v0.21.0 上线后的第一轮反馈）：

1. **配置编辑框固定 340px 高**——`min-height: 340px` 被写死（还盖过了 `max-height: 62vh`），
   短内容也要拖滚动条，长内容（比如带大量透传键的 provider）根本看不全。屏幕明明
   空着大半，编辑框却缩在中间。
2. **操作按钮在编辑器下方**——「保存 / 复制 / 删除」压在编辑区底下，视线要从编辑框
   拉到下面找按钮；而且按钮条本身占掉的高度正是编辑框缺的那部分。
3. **额度卡的「刷新」按钮紧贴标题**——标题行右侧空着一大片，按钮却挤在「额度」两个字
   旁边，布局不平衡。

三件事都指向同一个结论：**编辑区应该吃掉视口的剩余空间，而不是在页面里占一个固定
高度的小方块**。

顺带在核对实现时撞见**两个既有 bug**，都在这次要改的那几行代码上，一并修掉：

- **切换 provider 时，旧 provider 的迟到响应会灌进新 provider 的编辑器**
  （`providers.js` 的模块级 `editor` 变量已被重新赋值，回调里 `editor.setValue(...)`
  写的是新编辑器）→ 用户看着新 provider 的内容，点保存存成旧的
- **额度刷新失败会把「刷新」按钮一起抹掉**（错误分支 `host.textContent = ''` 换掉
  整张卡）→ 用户没法重试，只能切 provider 走一圈

## What Changes

- **编辑区改成「标题行 + 编辑器」两段式**（provider 视图与 Claude 视图都改）：
  标题行左侧是类型标题（provider 视图 `JSON`、Claude 视图按文件类型 `JSON` /
  `Markdown`）+ 状态提示位（「已保存」「JSON 解析失败」等），右侧是操作按钮；
  **编辑器下方不再有按钮条**。按钮顺序：**保存排最前**，其后是复制 / 删除
  （provider 视图）或 放弃修改 / 格式化（Claude 视图）。
- **编辑器高度改为吃掉视口剩余空间**：视图给出一个「至少占满剩余视口」的高度下限，
  内部用 flex 把剩余空间全部分给编辑器。窗口缩放自动跟随，不需要 JS 计算。
- **矮窗口下不回退**：编辑器的可见高度**不低于现状的 340px**。窗口不够高时页面
  重新出现竖向滚动条（现状本来就有），但编辑器不会比现在更矮——否则「长内容看不全」
  这个原始诉求在矮窗口下反被加重。
- **额度卡标题行的「刷新」按钮推到最右**，与标题之间留白。
- **去掉 textarea 的手动拖拽调高**（`resize: vertical`）——高度既然由布局决定，
  手动拖会破坏 flex 分配。
- **修跨渲染竞态**：切换 provider / 切换 Claude 文件时，**在途的旧请求不得写入新对象
  的界面**（用 render 序号守卫，参照 `usage.js` 的 AbortController 范式）。
- **修额度刷新失败抹掉刷新按钮**：刷新失败只替换卡片的内容区，**保留标题行**
  （含刷新按钮）——用户能直接再点一次重试。

不涉及后端接口、不涉及数据格式、不涉及任何架构约束。

## Capabilities

### New Capabilities

（无 —— 这是对现有控制台布局的改进，不引入新能力。）

### Modified Capabilities

- `web-config-console`: 控制台编辑区的**布局与操作位置**（编辑器撑满可用高度、操作
  按钮从编辑器下方移到编辑区标题行），以及两条**界面正确性**要求（在途请求不得写入
  新对象、额度刷新失败不得销毁刷新入口）。

## Impact

- `internal/web/assets/style.css`
  - 新增 `.editor-head`（编辑区标题行）；`.quota-head` 与它合并为一条规则
  - 编辑区改为 flex 链分配高度（视图最小高度 → `.editor-zone` → `.editor`）
  - 去掉 textarea 的 `resize: vertical` 与写死的 `min-height` / `max-height`
- `internal/web/assets/providers.js`
  - `renderMain` 重排为「页面头 → 额度卡 → 编辑区（标题行 + 编辑器）」
  - `renderQuota` 插入 `.spacer`（刷新按钮右对齐需要 DOM 侧的间隔元素）
  - **修竞态**：模块级 `editor` / `quotaHostEl` / `statusHost` 改为按轮次守卫
  - **修额度卡**：刷新失败只换内容区，保留 head
- `internal/web/assets/claude.js`：同样重排 + 同样修竞态
- `internal/web/assets/editor.js`：编辑器本体不变（仍是透明 textarea 叠高亮层）
- 后端（`internal/web/*.go`）：**不变** —— 纯前端改动
- spec：`web-config-console` 的编辑区布局要求 + 两条界面正确性要求
