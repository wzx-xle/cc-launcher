## 1. 基础设施：路径白名单、etag、原子写与备份

- [x] 1.1 新增 `internal/web/paths.go`：provider 名白名单校验（`^[A-Za-z0-9._-]{1,64}$`、拒分隔符与 `..`）、providers 目录内的路径拼装 + 前缀复核
- [x] 1.2 在 `internal/web/paths.go` 里加 Claude 文件标识 → 固定路径的映射（只有 `settings` / `claude-md` 两个 id，不接受路径参数）
- [x] 1.3 新增 `internal/web/etag.go`：文件内容 → etag（sha256 前 16 位）、etag 比较；补单测（含内容不变 etag 不变、内容变 etag 变）
- [x] 1.4 新增 `internal/safefile` 包：`Write(path, data, backupDir)` —— 先备份旧内容（`~/.cc-launcher/backups/`）、再原子写（tmp + rename），保留最近 N 份备份；补单测（备份内容正确、旧备份被裁剪、写后内容完整）
- [x] 1.5 `internal/provider` 改用 `safefile` 写 provider JSON（当前是裸 `os.WriteFile`，无备份无原子性）；补单测

## 2. 后端：provider 读接口

- [x] 2.1 `internal/web/server.go` 扩展 `web.Options`：`ProvidersDir`、`ClaudeSettingsPath`、`ClaudeMemoryPath`、`QuotaStore`、`ProvidersDir` 之外的写入口一律由本包拼装
- [x] 2.2 实现 `GET /api/providers`：返回 name / order / note / hasQuota / hasCredential；补单测（空目录返回空列表、note 缺失时为空串）
- [x] 2.3 实现 `GET /api/providers/{name}`：返回原始 JSON 文本 + etag 头；补单测（内容逐字节等于磁盘内容、名字非法 → 404、文件不存在 → 404）

## 3. 后端：provider 写接口

- [x] 3.1 实现写接口的同源闸：`Content-Type` 必须是 `application/json`（否则 415）、`Origin` 存在时必须同源（否则 403）；补单测
- [x] 3.2 实现 `PUT /api/providers/{name}`：`If-Match` 必填、不匹配 → 409 且不落盘、schema 校验不过 → 400 且不落盘、JSON 非法 → 400、先备份再原子写；补单测（四个场景）
- [x] 3.3 实现 `POST /api/providers`：body 支持 `{template, name}` 或 `{name, content}`；复用 `internal/cli` 的模板目录；名字已存在 → 409、名字非法 → 400；补单测
- [x] 3.4 实现 `POST /api/providers/{name}/copy`：复制配置到新名字；补单测
- [x] 3.5 实现 `DELETE /api/providers/{name}`：删配置 + 清 `quota.json` 里该 provider 的条目；补单测（条目确实被清掉、provider 不存在 → 404）

## 4. 后端：Claude 配置接口

- [x] 4.1 实现 `GET /api/claude/files`：两个文件（`settings` / `claude-md`）的标识、路径、大小、mtime；补单测（恰好两个、未知 id → 404）
- [x] 4.2 实现 `GET /api/claude/file/{id}`：原文 + etag；补单测
- [x] 4.3 实现 `PUT /api/claude/file/{id}`：乐观锁（不匹配 → 409）、`settings` 过 `settingsschema` 校验（不过 → 400）、先备份再原子写；补单测

## 5. 后端：额度接口

- [x] 5.1 实现 `GET /api/providers/{name}/quota`：复用 `internal/quota` 读取侧（零网络），返回 dimensions / stale / fetched_at；**没有条目时返回明确的"未采集"标记而不是空维度**；补单测（新鲜 / stale / 无条目三态）
- [x] 5.2 实现 `POST /api/providers/{name}/quota/refresh`：走 `quota.RefreshOne`，退避中 → 429 + 剩余秒数（不发请求）、查询失败 → 保留旧条目；补单测（退避场景用构造的退避状态）

## 6. 后端：接线

- [x] 6.1 `internal/web/server.go` 的 `Mount` 注册新路由（`/api/providers*`、`/api/claude*`），全部挂在既有 `protect` 中间件之后；补单测（未鉴权 → 401）
- [x] 6.2 `cmd/cc-launcher/serve.go`：给 `web.NewServer` 注入 providers 目录、Claude 两个文件路径、`quota.Store`；daemon 启动失败时看板不可用但**不得阻断 daemon**（保持现状）
- [x] 6.3 `cmd/cc-launcher/web.go`：`ccl web` 的帮助文案从"用量看板"改为"控制台"（用量 / 提供商 / Claude）；补 CLI 帮助快照测试（若有）

## 7. 前端：外壳与路由

- [x] 7.1 `internal/web/assets/index.html` 重构成外壳骨架：顶栏（三个视图入口 + 主题切换）、左栏容器、主区容器
- [x] 7.2 `internal/web/assets/style.css`：加三视图布局（顶栏固定高度、左栏固定宽度、主区自适应）、沿用现有 light/dark 变量
- [x] 7.3 `internal/web/assets/app.js` 改成入口：hash 路由（`#/usage`、`#/providers`、`#/claude`）+ 外壳渲染 + 视图切换
- [x] 7.4 现有 `app.js` 的用量渲染逻辑整体搬到 `internal/web/assets/usage.js`，**渲染行为一字不改**；`assets.go` 的 embed 与资源版本号机制同步调整

## 8. 前端：编辑器与高亮

- [x] 8.1 `internal/web/assets/highlight.js`：JSON tokenizer（key / string / number / bool / null / 标点）+ **HTML 转义**（凭证里可能有 `<` `&` `"`）；补单测（转义、字符串里的花括号不误切）
- [x] 8.2 `internal/web/assets/editor.js`：透明 textarea 叠高亮层、滚动同步、解析错误提示、非法 JSON 时禁用保存、带 etag 保存（409 → 提示重新加载）

## 9. 前端：提供商视图

- [x] 9.1 `internal/web/assets/providers.js`：左栏列表（按 order 排、note 副标题）+「+ 新建」「复制」「删除」
- [x] 9.2 新建流程：选模板 → 填名字 → 调 `POST /api/providers`；删除要二次确认
- [x] 9.3 额度卡：读取当前值 → 三态渲染（新鲜 / stale 灰显 + 采集时间 / 无条目 → "近期无活动，未采集额度"，**不得画成无限额**）
- [x] 9.4 手动刷新按钮：调 refresh 接口；429 → 显示"厂商限流中，X 分钟后可重试"；成功后立即重渲染额度卡
- [x] 9.5 额度卡渲染口径与状态栏一致（90/75/50 阈值、`█░` 条形图、已用/总量格式）

## 10. 前端：Claude 视图

- [x] 10.1 `internal/web/assets/claude.js`：左栏两个文件、右栏原文编辑、保存 / 放弃修改
- [x] 10.2 `settings.json` 编辑器给「格式化」按钮 + 保存前 JSON 合法性提示；`CLAUDE.md` 纯文本编辑（不高亮）
- [x] 10.3 保存成功后提示"已运行的 Claude Code 不会自动生效"；provider 保存后提示"下次启动 ccl 生效"

## 11. 测试与回归

- [x] 11.1 后端单测补齐：路径穿越拒绝、etag 冲突、schema 拒绝、备份产生、同源闸、三态额度、退避 429、删除清 quota
- [x] 11.2 前端烟测：沿用 `cmd/cc-launcher/web_smoke_test.go` 的路子 —— `/ui` 加载、外壳三个入口存在、静态资源可取
- [x] 11.3 回归：用量视图的原有渲染行为不变（KPI / 趋势 / 占比 / 明细表四件套仍在）
- [x] 11.4 `go test ./...` 全绿；`go vet ./...` 无告警

## 12. 收尾

- [x] 12.1 `docs/` 补一篇控制台说明（能改哪些文件、哪些不能、暴露面与 token 语义）
- [x] 12.2 `ccl web --help` 文案与实际能力对齐
- [x] 12.3 CHANGELOG 段（写在 `[Unreleased]` 下；发版时按仓库的 Release Tag 流程再落版本号，CHANGELOG commit 与 tag 同一次推送）
