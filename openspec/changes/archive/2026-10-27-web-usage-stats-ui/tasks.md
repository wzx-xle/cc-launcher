## 1. launch 上下文：provider / cwd 随 export 送达（前置：provider / 项目维度的真值来源）

- [x] 1.1 `cmd/cc-launcher`：新增上下文组装器——输入 `result.Provider`（provider 名）、`os.Getwd()`（工作目录）、provider env 里的 `ANTHROPIC_BASE_URL`（端点 host），产出 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint` 三个属性值
- [x] 1.2 编码器：所有值 percent-encode（unreserved 之外全编）；编码后长度 > 255 字符的属性**直接丢弃不注入**（Claude Code 对超长值会毒掉整份属性 map）；编码必须可逆（decode 回原值）
- [x] 1.3 编码器单测：空格路径 / 逗号路径 / 中文路径 / 超长路径 四种情形；断言输出只含 unreserved 字符、超长属性被省略、decode 后与原值逐字节一致
- [x] 1.4 `OTEL_RESOURCE_ATTRIBUTES` 组装：合并子进程环境里已有的值（用户原值保留，`ccl.*` 同名 key 以 ccl 的为准）
- [x] 1.5 注入点：`runTUI` 里 provider 选定后、`launcher.Launch` 之前——**同时**写 `optsEnv`（Unix 上 provider 选中时 `opts.Env` 非 nil、是启动时的 `os.Environ()` 快照，必须显式 `envutil.Upsert`）与进程 env（`os.Setenv`，覆盖 `opts.Env == nil` 分支与 Windows 的 `cmd.Env=nil` 路径）
- [x] 1.6 **禁止**写入 `~/.claude/settings.json`：单测断言 settings.json 的 `env` 里不含 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint`，也不含 ccl 追加的 `OTEL_RESOURCE_ATTRIBUTES`
- [x] 1.7 检测到 `~/.claude/settings.json` 已设 `OTEL_RESOURCE_ATTRIBUTES` 时：向 stderr 警告（说明 settings.json 的 env 优先级高于进程 env，会盖掉注入）+ 降级（provider / cwd 显示未知）；**不擅自改**用户文件
- [x] 1.8 `internal/collector/parser.go`：从 resource attributes 读 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint` → `Session.Provider` / `Session.Cwd` / `Session.ProviderEndpoint`
- [x] 1.9 `internal/collector/store.go`：`sessions` 表新增 `provider_endpoint TEXT NOT NULL DEFAULT ''`；`schemaVersion` 1 → 2，迁移用 `ALTER TABLE ADD COLUMN`（幂等、不动其它列、不丢数据）
- [x] 1.10 store upsert 语义：`provider` / `cwd` / `provider_endpoint` 三列**空值不覆盖非空**（`CASE WHEN excluded.<col> <> '' THEN excluded.<col> ELSE sessions.<col> END`）
- [x] 1.11 collector 单测：带 `ccl.*` 的 payload → session 行三字段落库；不带 `ccl.*` 的 payload → 三字段为空且**不报错**；空值不覆盖非空；后到的非空值补上先前的空字段
- [x] 1.12 迁移单测：旧库启动后新列存在、旧行数据不丢；重复启动不重复加列
- [x] 1.13 `internal/stats/query.go`：`providerName()` 的兜底先看 label `ccl.provider`（resource attrs 已自动合进 metrics 的 labels）
- [x] 1.14 真机端到端：`ccl` 启动一次 → daemon 收到带 `ccl.*` 的 payload → `telemetry.db` 中该 session 的 provider / cwd / provider_endpoint 有真值（不接受 mock 替代）

## 2. session cwd 兜底补采集（launch 上下文之外的补充来源）

- [x] 2.1 新增 cwd resolver：定位 `~/.claude/projects/*/<session-id>.jsonl`，逐行解析并在**第一个含顶层 `cwd` 字段的记录处停止**；读不到返回空串且不报错。**禁止**用编码后的目录名反推 cwd
- [x] 2.2 **优先级**：payload 里有 `ccl.cwd` 时直接用它，**不再**走 resolver；resolver 只在 payload 无 `ccl.cwd` 时兜底（非 `ccl` 启动的会话 / 上下文机制出现之前的历史行）
- [x] 2.3 resolver 单测：命中 cwd / 文件缺失 / 无 cwd 字段 / 只读 cwd 不留存其它字段（断言 prompt 内容不进内存与日志）
- [x] 2.4 `internal/collector`：给 `Store` 注入可选的 `CwdResolver`（默认 nil = 不 enrich，保持既有测试纯度），写 session 行时把解析出的 cwd 写进 `sessions.cwd`
- [x] 2.5 collector 单测：解析成功则写入 cwd；解析失败**不影响写 session 行**；已存在的非空 cwd **不被空值覆盖**；后到的非空值可补上先前为空的 cwd；payload 带 `ccl.cwd` 时 resolver 的结果不生效
- [x] 2.6 历史回填核心函数：只处理 `cwd = ''` 的行（数据驱动幂等，**不用** `PRAGMA user_version` 一次性标记），返回 resolved / failed 计数
- [x] 2.7 daemon 启动时后台回填：限 500 行/次，**不阻塞** HTTP listener 就绪；回填期间打日志（resolved / failed 计数）
- [x] 2.8 回填单测：只补空行、幂等（跑两次结果一致）、不修改 schema（表结构与列集合不变）、非空 cwd 不被改写

## 3. 查询层：model 分组 / model 过滤 / provider 过滤

- [x] 3.1 `stats.Query` 新增 `ByModel bool` 与 `Model string`；`buildSessionStatsSQL` 增加 `case "model"` 分组（`GROUP BY s.model`）
- [x] 3.2 model 过滤放在 **SQL 层**（`AND s.model LIKE ?`），确保 totals 与行和一致（不是事后过滤聚合行）
- [x] 3.3 `stats.Query` 新增 `Provider string`（provider 子串过滤，同样在 **SQL 层**：`AND s.provider LIKE ?`）；不新增 `ByProvider`（已存在）
- [x] 3.4 `ccl stats` 新增 `--by-model` / `--model <子串>` / `--provider <子串>` 三个 flag（放在 `registerStatsFlags`，flag 改动自动同步 completion）
- [x] 3.5 stats 单测：按 model 分组、按 model 过滤、过滤后 totals == 行和、`--by-model` 与 `--model` 可组合；按 provider 过滤后 totals == 行和；`--provider` 与 `--project` 可组合
- [x] 3.6 回归：`ccl stats`（不带 `--day`）的 JSON 输出**字节级不变**（`ByModel`/`Model`/`Provider` 不进 JSON，omitempty 生效）

## 4. Web 后端：路由、鉴权、API

- [x] 4.1 `internal/collector/server.go`：`ServerOptions` 增加 `Mount func(*http.ServeMux)` 挂载入口；collector **不 import** internal/web（依赖方向单向）
- [x] 4.2 `internal/web` 骨架：包结构 + `//go:embed assets/*` 静态资源
- [x] 4.3 token 文件管理：`~/.cc-launcher/web-token`，32 字节随机 hex、权限 `0600`、原子写（tmp + rename）、不存在则生成、已存在则原样保留
- [x] 4.4 token 轮换：重新生成并覆盖；轮换后旧 session cookie 全部失效
- [x] 4.5 session cookie：`ccl_web`，值 = `<issuedAtUnix>.<hex(HMAC-SHA256(key=token, msg="ccl-web-session-v1|"+issuedAt))>`；`hmac.Equal` 常量时间比较 + 有效期校验；属性 `HttpOnly; SameSite=Lax; Path=/; Max-Age`
- [x] 4.6 鉴权中间件：`/ui`、`/ui/*`、`/api/stats` 全部受保护；`/ui?token=<tok>` 校验通过 → 种 cookie → **302 重定向到 `/ui`（去掉 query）**；未鉴权时页面返 401 + 提示 HTML，API 返 401 + JSON error
- [x] 4.7 `/api/stats` handler：解析 `window`（24h/7d/30d/90d，默认 7d）、`project`、`provider`、`model`、`group`（session/project/provider/model，默认 session）；未知 window/group → **400**；窗口语义与 `--day N` 完全一致
- [x] 4.8 `/api/stats` 内部跑三次查询：主查询（用户选的 group + 桶聚合）、按 project 分组、按 model 分组；占比在后两者结果上算份额。**provider 占比的来源待定**：优先复用主查询结果里已有的 provider 列在内存里分组（避免第四次查询），口径与 CLI 一致
- [x] 4.9 web 专用响应 DTO（**字段白名单**）：不透传 `extra_labels`（含 `user.id` 指纹 / `host.arch` / `os.version`）
- [x] 4.10 web 单测：token 缺失/错误 → 401；正确 token → 302 且地址栏无 token；cookie 可访问 API；轮换后旧 cookie 失效；未知 window → 400；`provider` 过滤生效；`group=provider` 生效；响应中不出现 labels 字段值

## 5. 前端页面（读 dataviz skill 后动手）

- [x] 5.1 **先读 `dataviz` skill**，确定调色板、图形规范与浅色/深色主题策略，再写任何图表代码
- [x] 5.2 `assets/index.html` 骨架 + `assets/style.css`：布局、响应式（窄屏不产生横向滚动）、主题变量；静态资源带版本化 query 防缓存
- [x] 5.3 KPI 卡片：总 input / output / cache_creation / cache_read token、总 cost、session 数
- [x] 5.4 趋势图（**手写 inline SVG，无图表库**）：token 按类型堆叠柱 + cost 折线，与桶粒度（hourly/daily）一致
- [x] 5.5 占比图（SVG 横向条形）：按 project、按 provider、按 model 的 token 与 cost 占比
- [x] 5.6 会话明细表：起始时间 / 项目 / provider / 模型 / 四类 token / cost，与终端表列一一对应；含"窗口内无用量"的空状态
- [x] 5.7 交互：时间窗口切换、项目 / provider / 模型筛选、手动刷新按钮、自动轮询（默认 30s，可关）
- [x] 5.8 切换筛选/窗口时用 `AbortController` 取消在途请求，迟到的旧响应不得覆盖当前视图

## 6. CLI 入口

- [x] 6.1 新增 `ccl web` 子命令（`cmd/cc-launcher/web.go`）：默认打印带 token 的 URL（LAN IP + 127.0.0.1 两个），daemon 未跑则先 fork 并等就绪；daemon 起不来也要打印 URL 并报告失败
- [x] 6.2 `ccl web --rotate`：轮换 token，提示旧 URL 与旧 session 失效
- [x] 6.3 `ccl web --backfill`：触发不限量的历史 cwd 回填
- [x] 6.4 `ccl web --open`：跨平台打开系统浏览器（`xdg-open` / `open` / `cmd start`），按现有 `probe_unix.go` / `probe_windows.go` 的分平台写法组织
- [x] 6.5 顶层帮助（`topHelp`）加入 `web` 条目 + `ccl web --help` 帮助文本；`ccl stats --help` 补 `--provider` 说明
- [x] 6.6 shell completion：`ccl __complete ""` 加入 `web`；`ccl __complete web ""` 输出 `--rotate / --backfill / --open / -h / --help`；`ccl __complete stats --` 含 `--provider`
- [x] 6.7 `ccl serve --status` 追加一行看板 URL

## 7. 端到端验证

- [x] 7.1 起 daemon → `curl -i "/ui?token=<tok>"` → 302 + `Set-Cookie`；带 cookie 请求 `/api/stats` → 200 + JSON
- [x] 7.2 无 token / 错 token → 401；JSON 端点返回带 `error` 字段的 JSON
- [x] 7.3 `ccl stats` 的 `PROJECT` 列出现真实项目名、`PROVIDER` 列出现真实 provider 名；`--by-project` 分出多行；`--by-provider` 分出多行；`--project <子串>` 与 `--provider <子串>` 能筛出结果（回归：修复前这几条是空壳）
- [x] 7.4 `ccl web` 打印的 URL 在浏览器中能打开并看到图表（含 provider 占比与筛选）；`ccl web --rotate` 后旧 URL 失效
- [x] 7.5 `go build ./...` + `go test ./...` 全绿；`build.sh` 三平台交叉构建通过
- [x] 7.6 浏览器实测：窄屏不横向滚动、浅色/深色主题均可读、30s 自动刷新生效

## 8. 文档与发布

- [x] 8.1 README 增加"用量看板"章节（访问方式、`ccl web` 用法、鉴权与安全提示、:4318 ingest 口无鉴权的已知风险）；补一段"provider / 项目维度从哪来"（launch 上下文 + jsonl 兜底）与"直接用 `claude` 启动的会话维度为空"
- [x] 8.2 `CHANGELOG.md` 的 `[Unreleased]` 段加本 change 记录（Refactor / Features / Fix / Tests / OpenSpec 分类），并说明 `ccl stats` 的 `PROJECT` / `PROVIDER` 列由空变有值这一行为变化
- [x] 8.3 归档：`openspec archive`；随后按 `CLAUDE.md` 的 release 流程推进版本 tag（CHANGELOG commit 与 tag 同一次推送）
