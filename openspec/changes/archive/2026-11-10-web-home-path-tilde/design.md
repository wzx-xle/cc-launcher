## Context

web 控制台的 Claude 视图（`internal/web/assets/claude.js`）在两处展示文件的**绝对路径**：侧边栏条目（`f.path`）与编辑区标题行（`file.path`）。路径由后端 `GET /api/claude/files` 的 `path` 字段提供，源头是 `internal/web/claude.go` 的 `claudeFileSpecs()`，值来自 `cmd/cc-launcher/serve.go` 传入的 `ClaudeSettingsPath` / `ClaudeMemoryPath`。

两个约束决定了实现边界：

1. **该接口局域网可访问**。daemon 监听 `:4318` 全网卡（`serve.go` 启动日志明写"监听 :4318（全网卡）"），访问控制靠 URL 里的 token。绝对路径因此会把用户名暴露给同网段拿到 token 的人。仓库里已有同类判断先例：`internal/web/server.go` 的 `apiResponse` 注释明确写着 `extra_labels` 里的 `user.id`（主机指纹）"直接 marshal 到局域网可访问的 API 等于泄露主机指纹"，所以它被排除在响应之外。
2. **脱敏不能碰读写路径**。`internal/web/claude.go` 的 `claudeFilePath(id)` 用同一批 `path` 值做实际读写（`os.Stat` / `safefile.Write`），一旦把绝对路径换成 `~` 形式，读写会直接失败。

CLI 侧已有等价逻辑：`cmd/cc-launcher/statscmd.go` 的 `shortenHomePath(p)`——用 `os.UserHomeDir()` + `strings.CutPrefix` 把主目录前缀换成 `~`，home 不可用或路径不在 home 下时原样返回。该函数目前是 `package main` 的私有函数，`internal/web` 用不到。

## Goals / Non-Goals

**Goals:**

- `GET /api/claude/files` 的 `path` 字段是 `~` 形式，不含用户主目录的绝对路径。
- 脱敏逻辑只有**一份实现**，CLI 与 web 行为一致。
- 平台原生路径分隔符保持不变（Windows 上仍是 `~\.claude\settings.json`，不强行改成 `/`）。
- 读写路径、备份路径、文件校验全部不受影响。

**Non-Goals:**

- 不改前端 JS（它只展示后端给的字段，后端改完即生效）。
- 不对 provider 页、备份目录、db 路径等其他绝对路径做脱敏——目前 web 上除 Claude 页外没有展示真实绝对路径的地方（`providers.js` 里的备份路径是手写的 `~/.cc-launcher/backups` 字符串）。
- 不做 `~` 的反向展开（把 `~/x` 还原成绝对路径）——本 change 只有"显示"方向，没有"输入"方向。
- 不改变 API 字段名、接口路径、响应结构。

## Decisions

### D1: 脱敏放在共享包 `internal/pathutil`，而不是各写一份

**决策**：新增 `internal/pathutil` 包，导出 `Shorten(p string) string`；`cmd/cc-launcher/statscmd.go` 的 `shortenHomePath` 改为转调它（保留私有包装层，或直接替换调用点）；`internal/web` 直接调用 `pathutil.Shorten`。

**理由**：仓库已经因为"同一逻辑两处实现"吃过亏——CLI 有 `shortenHomePath`、web 没有，正是本 change 要补的洞。若在 `internal/web` 里另写一份，下次 home 目录判定规则一变（比如要处理 symlinked home）就得改两处，且只有一处被测试覆盖。

**备选方案**：
- *放在 `internal/sessiondir`*：该包已有 `os.UserHomeDir()` 用法，但它的语义是"session 目录"，不是"路径工具"——塞进通用路径处理会模糊职责。
- *放在 `internal/config`*：同理，config 管配置文件的读写，不管显示格式化。
- *在 `internal/web` 内自建*：最省事，但重复实现，正是要消除的东西。

### D2: 脱敏点在"组装响应"处，不在"生成路径"处

**决策**：路径的**生成与解析**（`claudeFileSpecs` / `claudeFilePath`）保持绝对路径不动；只在 `handleAPIClaudeFiles` 组装 `claudeFileItem` 时对 `Path` 字段做脱敏。

**理由**：读、写、备份三条路径全都依赖 `claudeFilePath(id)` 返回真实绝对路径。在源头脱敏会让三处一起坏掉；在响应边界脱敏则只影响"给人看的那个副本"。这也让"脱敏"成为一个纯粹的**展示层**关注点，未来若要恢复绝对路径只需改这一处。

**备选方案**：
- *前端 JS 脱敏*：后端 API 仍返回绝对路径 → 局域网里拿到 token 的人照样看得到用户名，等于没解决安全问题；且前端要自己判断 home 前缀，与 CLI 又成两套。
- *在 `serve.go` 传入 `ServerOptions` 时就脱敏*：同上，`ClaudeSettingsPath` 同时被 web 的读写逻辑使用，源头脱敏会破坏读写。

### D3: `Shorten` 的判定规则沿用 CLI 现有语义，但补上路径边界检查

**决策**：`Shorten` 沿用现有 `shortenHomePath` 的判定骨架——`os.UserHomeDir()` 成功且路径在 home 下时返回 `"~" + 剩余路径`；否则原样返回 `p`。home 不可用（`os.UserHomeDir()` 报错）时原样返回，不报错、不 panic。**但多一条路径边界检查**：home 是 `/home/user` 时，`/home/userx` **不算** home 下。

**理由**：纯字符串前缀匹配（`strings.CutPrefix`）会把 `/home/userx` 判成 home 下的路径，显示成 `~x`——那是一条**不在 home 里**的路径被谎报成 home 下的。现有 CLI 实现就有这个毛病；把它下沉成共享实现时顺手修掉。现有两个调用点（`~/.claude/...`、`~/.cc-launcher/...`）都是 home 的直接子目录，修掉对它们的输出**零影响**。

**备选方案**：
- *原样照搬 CLI 的前缀匹配*：保持"纯重构"，但把一个已知的小缺陷固化成共享契约，以后每个调用方都要自己记住"前缀匹配不等于 home 下"。
- *用 `filepath.Rel` 判断是否在 home 下*：语义更严格（会处理 `..`），但 `Rel` 会把 `../userx` 这类路径也判成"不在 home 下"而我们的需求只是"前缀 + 边界"，`CutPrefix` + 分隔符检查更直接、也更贴近原语义。

**边界情况**：
- 路径恰好等于 home（`p == home`）→ 返回 `"~"`。合理。
- 路径不在 home 下（如 `/etc/foo`）→ 原样返回。合理。
- home 是 `/` 的病态环境 → prefix 变成 `/`，`/etc/x` → `~/etc/x`。与原 CLI 行为一致，不在本 change 修正。

### D4: Windows 保留原生分隔符

**决策**：Windows 上输出 `~\.claude\settings.json`，不把 `\` 换成 `/`。

**理由**：这是"截掉前缀"而不是"重新拼路径"，剩下的部分原样保留即可。改成 `/` 需要额外的分隔符归一化逻辑，且会让显示值与磁盘上的真实路径在字符层面不一致，反而更难核对。

## Risks / Trade-offs

- **[调用方可能依赖 `path` 是绝对路径]** → 脱敏只发生在 `GET /api/claude/files` 的响应体；仓库内唯一消费方是前端 `claude.js`，只用于展示（`f.path` / `file.path` 两处，均为纯文本渲染）。改动前用 grep 确认过 `path` 字段没有别的用途。外部消费者若依赖绝对路径会受影响——但这是 web 控制台的私有接口，且脱敏后的值仍能唯一定位文件。

- **[Windows 上 `~\.claude\...` 对不熟悉该写法的人略反直觉]** → 与 `ccl serve --status` 的现有输出一致，用户在同一套工具里看到同一种写法；且比 `C:\Users\xxx\.claude\...` 短得多，也不再泄露用户名。

- **[home 目录含符号链接时，`Shorten` 可能不命中]** → 与现有 CLI 行为一致（同样不命中、同样原样返回）。这是既有行为，不是本 change 引入的回归。

- **[把 `shortenHomePath` 下沉到共享包会动到 CLI 代码]** → 行为等价重构，`ccl serve --status` 的输出逐字不变；`internal/pathutil` 的单测覆盖 home 命中 / 不命中 / home 不可用三种情况。

## Migration Plan

无数据迁移、无配置变更、无接口形状变更。

回滚：改动集中在 `internal/pathutil`（新增）、`internal/web/claude.go`（一行脱敏）、`cmd/cc-launcher/statscmd.go`（转调共享实现）——revert 即可，无残留状态。

## Open Questions

（无 —— 关键取舍已在 proposal 与本 design 中定稿。）
