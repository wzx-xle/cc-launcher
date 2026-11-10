## Why

web 控制台的 Claude 视图把两个可编辑文件的**绝对路径**原样显示出来（`/home/zxwang/.claude/settings.json`）。这段路径几乎全是用户主目录前缀，既占地方又不带任何信息量——用户关心的只是"是哪个文件"。

更要紧的是 `/api/claude/files` 是**局域网可访问**的接口（daemon 监听 `:4318` 全网卡，token 保护），绝对路径会顺带把**用户名**暴露给同网段拿到 token 的人。

CLI 侧早就有现成解法——`ccl serve --status` 用 `shortenHomePath` 把绝对路径缩成 `~/...`——web 页只是没接上这套逻辑，两边各写各的、迟早漂移。

## What Changes

- 新增共享包 `internal/pathutil`，提供 `Shorten(p string) string`：把用户主目录前缀替换成 `~`，其余路径原样返回。
- `cmd/cc-launcher` 中仅供 `ccl serve --status` 使用的私有 `shortenHomePath` 改为调用共享实现（消除重复实现，两边行为从此一致）。
- `GET /api/claude/files` 返回的 `path` 字段改为 `~` 形式：Linux/macOS 为 `~/.claude/settings.json`，Windows 为 `~\.claude\settings.json`（保留原生路径分隔符）。
- **读写文件的内部路径完全不变**：仍然用绝对路径读写、脱敏只发生在"返回给前端展示"的那一层。

非破坏性：CLI 输出格式、API 字段名、接口路径都不变；只有 `path` 字段的**值**从绝对路径变成 `~` 形式。

## Capabilities

### New Capabilities

（无 —— 本 change 不引入新能力，只修改既有能力的对外表现。）

### Modified Capabilities

- `web-config-console`: "the user-level Claude configuration files can be edited" —— 增加约束：文件列表返回的 `path` MUST 是 `~` 形式，MUST NOT 含用户主目录的绝对路径。

## Impact

- **新增**：`internal/pathutil` 包（`Shorten` + 单测）。
- **改动**：`internal/web/claude.go` —— 组装 `GET /api/claude/files` 响应时对 `path` 脱敏；`cmd/cc-launcher/statscmd.go` —— 私有 `shortenHomePath` 改为转调共享实现。
- **不改**：前端 `internal/web/assets/claude.js`（它只负责展示 `path` 字段，后端给什么显示什么）；`internal/web/claude.go` 里读写文件的路径解析逻辑（`claudeFilePath`）。
- **无依赖变更**，无 schema 变更，无数据迁移。
