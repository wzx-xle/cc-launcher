## Why

**改配置要离开浏览器，去翻配置文件。** provider 的 JSON、`~/.claude/settings.json`、
`~/.claude/CLAUDE.md` 全是手写文件——provider 的凭证键名（`ANTHROPIC_AUTH_TOKEN` /
`ANTHROPIC_BASE_URL`）得照着模板抄，抄错一个字符要到下次启动 Claude Code 才发现。

**看板已经是现成的落点。** 它在 daemon 进程里、token 保护、已经在读本机的
telemetry.db 和 quota.json——把"读本机状态"扩到"改本机配置"，不需要新端口、
新进程、新一套鉴权。

**额度只能在状态栏看一行。** 状态栏受宽度约束，只能挑 3 个维度显示；网页上能
看全，还能手动刷新（按需刷新，不受"只刷活跃 provider"的限制）。

## What Changes

- `/ui` 从单页只读看板重构成**三视图应用**：顶栏切主视图 + 左栏对象列表 +
  右栏详情/编辑区
  - **用量**：整块复用现有页面（结构搬进新外壳，渲染逻辑不改）
  - **提供商**：左栏 provider 列表，右栏原文 JSON 编辑（带语法高亮）；额度卡
    展示该 provider 的额度维度，带手动刷新按钮
  - **Claude**：左栏两个用户级文件（`~/.claude/settings.json`、`~/.claude/CLAUDE.md`），
    右栏原文编辑
- 新增写接口：provider 的新建 / 修改 / 复制 / 删除，Claude 配置文件的修改
- 新增额度接口：读取当前值（零网络）+ 手动刷新（联网，遵守退避）

## Capabilities

### New Capabilities

- `web-config-console`: 网页配置控制台——三视图结构、provider 在线编辑、
  Claude 用户级配置编辑、额度卡与手动刷新

### Modified Capabilities

- `web-usage-stats`: `/ui` 的页面结构从"单页看板"改为"外壳 + 三视图"，
  用量视图的行为与数据接口不变

## Impact

- **改动** `internal/web/`：新增写接口与读接口、路径白名单、乐观锁、备份
- **改动** `internal/web/assets/`：前端从单文件拆成多文件（外壳 / 用量 / 提供商 /
  Claude / 编辑器 / 高亮），仍是 `go:embed` 的静态资源，**不引入构建步骤**
- **改动** `cmd/cc-launcher/serve.go`：给 `web.Server` 注入 provider / config /
  quota 三个 Manager（daemon 进程里已有）
- **改动** `internal/provider`、`internal/config`：补上写路径需要的原子写 + 备份
  （provider 的写入目前是裸 `os.WriteFile`，没有备份）
- **风险**：token 的语义从"读"扩到"写"——同一个 token 现在能改本机配置。
  缓解手段现成（`ccl web --rotate` 立即吊销），但**暴露面不变**（仍是 :4318
  全网卡监听 + token 保护）
