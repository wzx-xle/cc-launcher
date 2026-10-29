# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.16.1] - 2026-10-29 — otel-metrics-only-takeover

### Fixes
- **logs / traces 不再被 ccl 意外拦截**：v0.16.0 把 OTel 接管搬进 `--settings` 时**接管了两个端点**——per-signal metrics 端点 + **兜底端点** `OTEL_EXPORTER_OTLP_ENDPOINT`。兜底端点是"总配置"：**logs / traces 在没有 per-signal 端点时也走它**。而 daemon 是 metrics-only 的（`/v1/logs` `/v1/traces` 只回 200，不解析也不转发）→ 接管兜底端点 = 把用户的 logs / traces 一起劫持进 daemon **静默丢弃**。ccl 的用量统计只涉及 metrics 一个信号 → **只接管 metrics**，兜底端点键不再交付
  - **metrics 拦截不受影响**：per-signal metrics 端点**总是**被交付，而 OTel 规范规定 per-signal 端点**优先于**兜底端点 → metrics 一律到本地 daemon（哪怕用户配了远程兜底端点）
  - **logs / traces 回到用户自己的配置**：配了 per-signal logs/traces 端点 → 直达那个端点；只配了兜底端点 → 走兜底端点直达远程 collector，**不再经 daemon**
  - ⚠️ **更正 v0.16.0 的设计说法**：把"接管兜底端点"称作 metrics 拦截的兜底是错的——metrics 拦截从头到尾只靠 per-signal 端点，接管兜底端点**没有收益、只有一个副作用**。这个错误在 v0.16.0 的 design D2 与本文档里都写了，本次一并纠正
  - 真机验证（假远程 collector + 真 daemon + 真 claude）：collector 收到 `POST /v1/logs`（Claude Code **直发**，不经 daemon）+ `POST /v1/metrics`（**daemon 转发**）；daemon 落库仍带 provider / cwd

### Tests
- `cmd/cc-launcher`：`buildChildOtelEnv` 新增专项用例——**兜底端点永不交付**（无论用户配没配）；既有交付策略 / 转发目标 / 遗留清理用例同步
- 真机：交付给 Claude Code 的 `--settings` env 块里确认**没有**兜底端点键

### Compatibility
- **行为变化**：如果你在 v0.16.0 上配了兜底端点指远程 collector 且开着 `OTEL_LOGS_EXPORTER`，那些 logs 之前是被 ccl 的 daemon **吞掉**的；现在恢复为直达你的 collector
- 交付给 Claude Code 的 `--settings` env 块里不再有 `OTEL_EXPORTER_OTLP_ENDPOINT` 键（其余 4 个 OTel 键 + `OTEL_RESOURCE_ATTRIBUTES` 不变）
- 转发目标解析（仍读你的兜底 / per-signal 原值作为转发目的地）、daemon 侧代码、遗留清理规则、db schema / 看板 / 鉴权 / 顶层子命令全部不变

### OpenSpec
- archive `otel-metrics-only-takeover` (2026-10-29)

## [0.16.0] - 2026-10-28 — otel-endpoints-via-settings

### Refactor
- **OTel 端点接管从"改写 `~/.claude/settings.json`"改为"走 `--settings` 参数"**：v0.15.3 只把 launch 上下文（provider / cwd / 端点 host）搬进了 `--settings`，OTel 端点接管仍然靠改写用户的配置文件。两个通道语义不一致，而"改文件"是**机器全局**的副作用：
  - **裸 `claude` 被劫持**：settings.json 的 `env` 块对本机**所有** Claude Code 进程生效 → 直接敲 `claude`（不走 ccl）的会话也往 ccl 的 daemon 推 → 统计里留下一堆 provider / cwd 全空的"未知"会话
  - **用户文件里长期躺着两份历史**：live 值 + `CC_ORIG_*` 备份键，得靠 `ccl serve --restore` 手工收拾
  - **Windows 用户反馈的"settings.json 覆盖进程环境"问题**，根因正是"改文件 vs 传参"——同一个解法（`--settings`）已经用于 launch 上下文，端点接管现在也用上
- **6 个 OTel key 全部通过 `--settings` 的 `env` 块交付**（与 launch 上下文**同一个** settings 对象）：
  - 两个端点键（`OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`）**接管**成本地 daemon 地址——`--settings` 优先级高于 settings.json，所以你的原值不用动也能被接管
  - `OTEL_METRICS_EXPORTER` / `OTEL_EXPORTER_OTLP_PROTOCOL` / `CLAUDE_CODE_ENABLE_TELEMETRY`：你自己写过就**尊重**（= 不交付这个键 → settings.json 的值继续生效），没写才由 ccl 补默认值
  - `OTEL_LOGS_EXPORTER` **永不交付**（daemon 不解析 logs，隐私靠 daemon 架构保证）
- **转发目标改由进程环境投递**：ccl 从 settings.json 读出**用户自己配的**端点，写进自己的进程环境 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` / `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（fork daemon 时被继承）→ **daemon 侧代码与 otel-receiver spec 一个字没改**。没有对应端点时变量**不设置**（不留占位值）
- **删掉"控制权交接备份"整套机制**（`isUnderCclControl` + 交接边界的 `Manager.Backup()`）：它只因为要改文件才存在——不写文件就没有交接这回事

### Features
- **一次性清理旧版遗留**：升级后第一次跑 ccl，自动把旧版 ccl 留在 settings.json 里的痕迹收拾干净——备份值还原回原 key、删掉 ccl 注入的默认值、删掉 `CC_ORIG_*` / `FORWORD_*` 备份键；**用户自己写的键一律不动**。清理前备份到 `~/.cc-launcher/backups/`，失败只警告不阻断启动；**只跑一次**（标志记在 `~/.cc-launcher/state.json`）
- **`ccl serve --restore` 语义收敛**：从"撤销 ccl 当前的接管"改成"清理**旧版 ccl** 留下的痕迹"（幂等）。新版 ccl 不写 settings.json → 没有"当前接管"可撤销

### Fixes
- **转发到远程 collector 的目标不会退化成 localhost**：万一清理没跑成（settings.json 不可写等），读取转发目标时**备份键优先于被改写的 live 键** → 转发目标仍是用户的真原值，而不是旧版改写后的 `localhost:4318`（后者会让用户的公司 collector 静默收不到数据）

### Tests
- `cmd/cc-launcher`：交付策略（接管 / 尊重 / 永不交付 logs）、转发目标解析（per-signal 优先、遗留备份键优先、缺失时不设值）、遗留清理三条规则（还原 / 自我备份删除 / 无备份默认值删除）、幂等、用户自有键与非 string 值不动、**settings.json 不被改写**（字节级对比）、清理只跑一次
- `cmd/cc-launcher`（launchctx）：OTel 端点与 launch 上下文**合并进同一个 `--settings` 对象**（只含 env 块）
- smoke 测试同步：首跑不改用户文件、旧版痕迹清理一次、restore 语义

### Compatibility
- ⚠️ **行为变化：裸 `claude`（不走 ccl）的会话不再被 ccl 记录**。因为 ccl 不再往 settings.json 写全局端点，Claude Code 没有端点就根本不导出。**你自己配了 `OTEL_EXPORTER_OTLP_ENDPOINT` 的话，裸 claude 照旧发给你自己的 collector，只是不再经过 ccl 的 daemon**。历史数据里已有的"未知"会话仍在（不会回填）
- ⚠️ **行为变化：老版本 ccl 留在 `~/.claude/settings.json` 里的 OTel 痕迹会被自动清理**（升级后第一次跑 ccl）。你自己的端点会被**还原**（不是删除）；ccl 注入的默认值与备份键被删掉。**如果你的 `OTEL_METRICS_EXPORTER` / `OTEL_EXPORTER_OTLP_PROTOCOL` 恰好等于 ccl 的默认值**（`otlp` / `http/protobuf`，也是 OTel SDK 自己的默认值），它们也会被删——功能上无差别（SDK 默认值相同），但你的文件会变
- `ccl serve --restore` 不再撤销"当前接管"（当前接管不再落盘）；它只清理旧版遗留，幂等
- **转发到远程 collector 的链路不变**：daemon 仍读 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` / `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 两个进程环境变量，优先级与 loopback 防环行为都不变
- db schema / 看板 / 鉴权 / `ccl stats` 的 flag 与输出格式 / 顶层子命令列表全部不变

### OpenSpec
- archive `otel-endpoints-via-settings` (2026-10-28)

## [0.15.3] - 2026-10-28 — launch-context-settings-injection

### Fixes
- **Windows 上 `ccl stats` 的 PROVIDER 列恒空**：Claude Code 的 `~/.claude/settings.json` 里若已设 `OTEL_RESOURCE_ATTRIBUTES`（公司模板 / 旧脚本 / 别的教程），settings.json 的 `env` 块优先级**高于进程环境** → ccl 注入的 launch 上下文被**整体顶掉** → provider 维度恒空。2026-10-28 实测复验：settings.json 写 `X=2` + 进程环境写 `X=1` → Claude Code 实际拿到 `2`。**修复**：launch 上下文的注入通道从**进程环境**改为 Claude Code 官方参数 **`--settings`**（优先级实测：`--settings` > settings.json > 进程环境），绕开被覆盖的那一层
  - **用户的 settings.json 里有没有那个 key 都不再影响 ccl 的注入** → 之前"检测到就降级 + 警告"的行为**删除**（那条警告说的是"provider / 项目维度将为空"，实际只有 provider 维度真空——project 维度还有 jsonl 兜底，文案本身也是错的）
  - **用户原有的属性值不丢**：注入值 = 「settings.json 里的原值」+ 「ccl 的三个上下文属性」，合并而非替换；ccl 的同名 key 以 ccl 为准
  - **不改用户的 `~/.claude/settings.json`**：`--settings` 只作用于本次启动的进程 → 直接 `claude` 启动的会话**不受影响**（继续靠 jsonl 兜底拿 cwd，provider 显示"未知"）
  - 进程环境那条注入**保留**为冗余第二通道（settings.json 没设该 key 时仍然生效；万一某个 Claude Code 版本不认 `--settings` 还有一道防线）
- **转发给远程 collector 的 payload 里不再含 ccl 的三个属性**：`ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint` 是本地记账（描述"这次 ccl 启动用了什么"），对远程 collector 没价值，还可能泄露本机工作目录 → daemon 转发前剥掉它们，远程收到的资源属性与你没装 ccl 时一致。**本地 `telemetry.db` 仍保留这三个键**（CLI 的 provider 列兜底要用）
  - ⚠️ **实测踩坑**：Claude Code 的 OTel SDK 把 `OTEL_RESOURCE_ATTRIBUTES` 的内容**同时**放进 Resource 的 attributes **和每个 data point 的 attributes** → 只剥 resource 那一处，datapoint 里那份会原样转发出去。两处都剥才是完整的（单测只覆盖了 resource 那一处，是端到端验证抓出来的）

### Features
- **`ccl` 启动 Claude Code 时会带一个 `--settings` 参数**（内容形如 `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"<合并值>"}}`，只含 `env` 一个字段，不影响 settings.json 的其它设置）
  - **Unix**：内联 JSON 字符串（`syscall.Exec` 直传 argv，不经 shell → 双引号安全，且不产生临时文件）
  - **Windows**：写临时文件（`os.TempDir()`，权限 0600）→ `--settings <path>`。**必须用文件**：`launcher_windows.go` 走 `cmd /c claude ...`，cmd.exe 会把 JSON 的双引号当参数分隔符剥掉 → 内联 JSON 会被拆坏。文件在 claude 退出后由 ccl 删除
- `ccl stats` 的 PROJECT / PROVIDER 列在上述场景下**恢复有真值**（真机验证：settings.json 故意设冲突值 `team=platform` → daemon 仍收到 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint`，且用户的 `team=platform` 也在）

### Tests
- `internal/launcher`：`Options.ExtraArgs` 被正确拼进 argv
- `cmd/cc-launcher`：注入值合并逻辑（用户原值保留 + ccl 同名 key 覆盖）、additional settings 的 JSON 形状（只含 env 块）、settings.json 不被改动
- `internal/collector`：转发前剥除——resource attrs 与 datapoint attrs **两处**都剥、用户的键保留、metric 本身（名 / 值 / session.id）不受影响、畸形 payload 不 panic 且按原样转发

### Compatibility
- **`ccl` 启动 Claude Code 的命令行多了一个 `--settings` 参数**——若有脚本解析 `ccl` 的输出或包裹 claude 调用，需注意
- **转发给远程 collector 的 payload 内容变了**：不再含 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint`（语义上更接近"没装 ccl"的状态）。若你的远程 collector 依赖这三个键做聚合，需要另找来源
- **`~/.claude/settings.json` 里已设 `OTEL_RESOURCE_ATTRIBUTES` 的用户**：不再有任何警告或降级——注入照常生效，provider / 项目维度有真值
- db schema / 看板 / 鉴权 / `ccl stats` 的 flag 与输出格式全部不变

### OpenSpec
- archive `launch-context-settings-injection` (2026-10-28)

## [0.15.2] - 2026-10-27 — web-backfill-to-serve

### Refactor
- **`ccl web --backfill` 挪到 `ccl serve --backfill`**：`ccl web` 的 flag 此前是 `--rotate`（吊销 token）/ `--backfill`（回填历史 cwd）/ `--open`（开浏览器）——三个动作里**只有前两个跟看板有关**（`--backfill` 修的是 `sessions.cwd`，是 daemon 的数据维护动作，与看板页面无关，只是恰好共用同一个 `telemetry.db`）。用户想补历史数据却要先想到"web"这个看板入口，名字与动作对不上。现在 `ccl web` 只剩**关于看板本身**的三个动作（打印 URL / 吊销 / 打开），回填归位到 `ccl serve`
  - `ccl serve` 现有 4 个 flag（`--stop` / `--restore` / `--status` / `--restart`）**全部是"一次性动作后立即返回"的 daemon 生命周期 / 维护动作**——`--backfill` 正好是同一类，进同一个互斥组语义一致，不引入新类别
  - 为什么不是新增顶层 `ccl backfill`：那会让顶层命令从 5 个变成 6 个，与本次要解决的问题（顶层命令别 proliferate）背道而驰
  - 为什么不是 `ccl stats --backfill`：`ccl stats` 是**只读**的查询面，回填是**写**动作；往只读命令里塞写操作会破坏"ccl stats 不写任何东西"的心智模型
  - **顶层子命令数不变**（仍是 `serve / stats / web / provider / mode` 五个），看板的挂载方式、db schema、HTTP 契约全部不变

### Tests
- 新增 `cmd/cc-launcher/backfill_test.go`（常规测试，非 smoke）：`ccl serve --backfill --stop` / `--status` 报互斥错误（exit 非 0 + stderr 说明）；`ccl web --backfill` 报 unknown flag
- smoke 测试同步：web 的补全候选断言改为 `--rotate / --open`（不再含 `--backfill`）；新增"web 补全不含 `--backfill`"、"serve 补全含 `--backfill`"两条
- 真机验证：`ccl serve --backfill` 真跑两次，resolved / failed 计数一致（幂等）；`ccl web --backfill` exit 1 + "flag provided but not defined: -backfill"；`ccl web` / `ccl web --help` 不受影响

### OpenSpec
- archive `web-backfill-to-serve` (2026-10-27)

### Compatibility
- **破坏性变更**：`ccl web --backfill` 不再被识别（报 `flag provided but not defined: -backfill`，exit 1）。**不做别名兼容**——两个位置并存会让"到底哪个是对的"变成新的困惑源。新位置是 `ccl serve --backfill`（行为完全不变：不限量、幂等、不改 schema、打印 resolved / failed 计数）
- `ccl serve` 的互斥 flag 组从 4 个变成 5 个（`--stop / --restore / --status / --restart / --backfill`）；`ccl serve --backfill --status` 现在报互斥错误（一个写数据一个读状态，确实不该同时给）
- `ccl web --help` 与 `ccl __complete web ""` 的 flag 列表少一条 `--backfill`；`ccl serve --help` 与 `ccl __complete serve ""` 多一条 `--backfill`
- db schema / 顶层子命令列表 / 看板的 URL 与鉴权方式全部不变

## [0.15.1] - 2026-10-27 — web-stats-group-fixes

### Fixes
- **看板的「会话数」在分组查询下报的是分组数，不是会话数**：`/api/stats` 的 `totals.sessions` 直接用了 `len(主查询行数)`——`group=provider` 时那是 provider 个数（实测 2），不是窗口内活动的会话数（实测 6）。`group=session`（默认）时两者恰好相同，所以这个 bug 只在用户主动切分组时暴露。修复：新增 `stats.CountSessionsInWindow`（按**消费时刻** `metrics.captured_at` 计数，与 detail 行的口径一致；窗口起点与主查询对齐），单独数一次
- **看板的 provider 占比在 `group=project` / `group=model` 时归因错误**：占比原本复用主查询行里的 provider 列——这两种分组下每行是**一个分组**（一个 project / 一个 model），行的 provider 列只是该分组里**任意一个** provider，于是整个分组的用量被错误归到其中一个 provider 上（实测：`group=project` 时 provider 只剩 2 项、其中一项占比 100%）。修复：`group=project` / `group=model` 时单独跑一次按 provider 的聚合查询；`group=session`（每行一个 session，内存里按 provider 分组 == 真聚合）与 `group=provider`（主查询本身就是 provider 聚合）仍复用主查询，**不多查一次**
  - 修复后四种分组下 provider 占比完全一致（口径与 CLI 的 `--by-provider` 一致）

### Tests
- 补两条 web 单测锁死上述两个 bug：`group=provider` 时 `totals.sessions` 必须是会话数（4）而不是分组数（3）；`group=project` 时 provider 占比必须有 3 项且份额之和为 1
- 修测试库的样本：新增一条与 s1 同 project 但**不同 provider** 的 session，让"project 分组里的 provider 不唯一"这个前提成立（否则测试数据碰巧掩盖了 bug）

### Compatibility
- **db schema / CLI flag / JSON 字段名全部不变**；只改 `/api/stats` 的两个**数值口径**（`totals.sessions` 与 `providers[]` 的份额）——v0.15.0 的看板在这两项上是错的

## [0.15.0] - 2026-10-27 — web-usage-stats-ui

### Features
- **用量看板：浏览器里看 token / cost（`ccl web`）**。看板挂在 daemon **已监听的 :4318** 上（`/ui` + `/ui/<asset>` + `/api/stats`），**不新开端口、不新增进程**——页面随 daemon 生死，`ccl web` 负责把 daemon 拉起来。页面内容：KPI 卡片（各类 token / 总 cost / 会话数）、按时间桶的 token 堆叠柱 + cost 折线（**两个独立面板、两个独立 y 轴**，不做双轴图）、按项目 / 提供商 / 模型的 token 与 cost 占比、会话明细表（列与终端表一一对应）；支持窗口切换（24h / 7d / 30d / 90d）、三个维度的筛选、手动刷新、30s 自动刷新（可关）。前端资源用 `go:embed` 内嵌进二进制：**零新依赖**（无前端框架、无 CDN 图表库，图表是手写 inline SVG），离线可用。配色取自 dataviz 参考调色板并跑过 `validate_palette.js`（浅色 4 槽全项通过，aqua/yellow 对浅色底对比度 < 3:1 → 按 relief 规则配了「表格视图」切换与条形末端直接标数值）；浅色 / 深色两套**各自选定**（不是自动反色），系统偏好与页面内主题按钮都支持
- **`ccl web` 子命令**：打印带 token 的看板 URL（本机 LAN IP + 127.0.0.1 两个，daemon 没跑会先 fork 并等就绪，daemon 起不来也照样打印 URL 并报告失败）；`--rotate` 轮换 token（旧 URL 与旧 session 立即失效）；`--backfill` 触发**不限量**的历史 cwd 回填；`--open` 用系统默认浏览器打开（`xdg-open` / `open` / `cmd start` 分平台）
- **看板鉴权**：token 是 32 字节随机 hex（64 字符），存 `~/.cc-launcher/web-token`（权限 `0600`，原子写 tmp+rename，首次启动生成、已存在则原样保留）。访问 `/ui?token=<tok>` → 常量时间比对 → 种 HttpOnly + SameSite=Lax 的 session cookie → **302 重定向到不带 token 的 `/ui`**（token 不留在地址栏 / 浏览器历史 / Referer 里）。cookie 值是 `<签发时间>.<hex(HMAC-SHA256(key=token))>`，**不含 token 明文**，7 天有效；因为签名 key 就是 token，**轮换 token 即吊销全部旧 cookie**（daemon 每次请求重读 token 文件 → 轮换无需重启 daemon）
- **`ccl stats` 新增 model 维度**：`--by-model`（按模型聚合）、`--model <子串>`（按模型过滤）、`--provider <子串>`（按提供商过滤）。三个过滤都在 **SQL 层**（`AND s.model LIKE ?` / `AND s.provider LIKE ?`），保证 totals == Σ rows（事后过滤聚合行做不到这点）。`--provider` / `--model` / `--project` 可任意组合
- **provider / 项目维度从"恒空"变成"有真值"**（本次最底层的变化）。Claude Code 的 OTel payload 里**没有** cwd 也没有 provider，所以 ccl 走两条路补：
  - **launch 上下文（主路径）**：`ccl` 启动 Claude Code 时把当次的 provider 名 / 工作目录 / provider 端点 host 写进**子进程环境**的 `OTEL_RESOURCE_ATTRIBUTES`（`ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint`），Claude Code 的 OTel SDK 随 metrics 推送为 **resource attributes**，daemon 直接取。这是**逐次 launch 的真值**——provider 是"这次启动用的哪个 provider"的快照，不是"当前配置的 provider"。**刻意不写 `~/.claude/settings.json`**：那里的 `env` 会被所有 `claude` 进程继承，直接 `claude` 启动的会话会继承"上一次 ccl 的 cwd"，反而造出假数据
  - **jsonl 兜底**：payload 无 `ccl.cwd` 时（直接 `claude` 启动的会话、上下文机制出现之前的历史行），用 `session.id` 反查 `~/.claude/projects/*/<session-id>.jsonl`，**只取第一条带顶层 `cwd` 的记录**（prompt 内容不读、不存、不进日志），取到即停。查不到 → 留空（正常状态，不是错误）
  - **历史回填**：daemon 启动时后台回填 `cwd = ''` 的行（每次启动最多 500 行，**不阻塞**端口监听），`ccl serve --backfill` 可显式不限量补齐（v0.15.0 时该命令是 `ccl web --backfill`，v0.15.2 挪到 serve）。回填是**数据驱动幂等**的（只处理空行，不用 `PRAGMA user_version` 一次性标记），跑两次结果一致，之后新建的空行仍可被后续回填补上

### Fixes
- **`ccl stats --by-provider --by-project`（组合）此前必然失败**：`buildSessionStatsSQL` 的 `groupExpr` 在多维分组时展开成**逗号分隔的两列**，`SELECT %s AS session_id` 因此多出一列，与 `rows.Scan` 的 11 个目标列对不上 → 扫描报错。改为**单列表达式**（`cwd × provider` 拼接）+ `GROUP BY s.cwd, s.provider`，组合分组现在真的能出多行
- **`ccl stats --help` / `ccl web --help` 此前会以 exit 1 收场并打印 Go flag 的 usage 列表**：`flag.ErrHelp` 被当成普通错误 return 出去了（`ccl stats --help` 一直如此）。现在自己接管 help：打印本命令的用法说明并 exit 0；flag package 的 usage 噪音改为丢弃（真解析错误仍原样返回）
- **`sessions` 表新增 `provider_endpoint` 列**走**显式幂等迁移**：`PRAGMA user_version` 1 → 2，`ALTER TABLE ADD COLUMN`（先查列是否存在，存在则跳过）——只加列，不动其它列、不删行、不丢数据
- **session 行的 `provider` / `cwd` / `provider_endpoint` 改成"空值不覆盖非空"**（`CASE WHEN excluded.<col> <> '' THEN excluded.<col> ELSE sessions.<col> END`）：后续不带 `ccl.*` 的 export 不会再把已知维度抹掉；后到的非空值能补上先前的空字段
- **`providerName()` 的兜底先看 label `ccl.provider`**：resource attrs 已自动合进 metric 的 labels，历史数据（provider 还没写进 sessions 表时）也能显示 provider 名

### Tests
- 新增 `internal/launchctx`（percent-encode / 合并 / 端点 host 提取）+ 单测：空格 / 逗号 / 中文 / 超长四种路径，断言输出只含 unreserved 字符与合法 `%XX`、超长属性被省略、decode 后与原值逐字节一致、用户已有属性值保留且 ccl 同名 key 以 ccl 为准
- 新增 `internal/sessiondir`（session id → cwd 反查）+ 单测：命中 cwd / 文件缺失 / 无 cwd 字段 / 损坏文件 / 路径穿越拒绝 / **断言 prompt 内容不进结果**；实现用 `json.Decoder` 的 token 流逐个跳过非 cwd 的值，不把整条记录反序列化进 map
- 新增 `internal/web`（token / session cookie / API）+ 单测：token 文件首次生成（64 位 hex + 0600 权限）、已存在则原样保留、错 token → 401 且不种 cookie、正确 token → 302 + `Location` 不含 token、cookie 可访问 API、**轮换后旧 cookie 失效**、未知 window / group → 400、provider 过滤生效、`group=provider` 生效、**响应里不出现 labels 的值**（主机指纹 / arch / os 版本）
- 新增 collector 单测：带 `ccl.*` 的 payload → session 行三字段落库；不带 `ccl.*` → 三字段为空且**不报错**；空值不覆盖非空；后到的非空值补上空字段；老库启动后新列存在、旧行不丢、重复启动不重复加列；resolver 解析成功才写 cwd、解析失败不影响写行、payload 带 `ccl.cwd` 时 resolver 结果不生效；回填只补空行、幂等、不改 schema
- 新增 stats 单测：按 model 分组 / 按 model 过滤 / 过滤后 totals == 行和 / `--by-model` 与 `--model` 可组合 / 按 provider 过滤 / `--provider` 与 `--project` 可组合 / **`--by-provider --by-project` 组合可用**
- 新增 cmd 层单测：注入 launch 上下文后 **settings.json 的 `env` 里不含 `ccl.*` 也不含 ccl 追加的 `OTEL_RESOURCE_ATTRIBUTES`**（用户的 key 原样保留）；settings.json 已设 `OTEL_RESOURCE_ATTRIBUTES` 时不注入并降级
- 新增 smoke 测试：`ccl --help` 列出 `web`、`ccl web --help` 打印三个 flag、`ccl __complete ""` 含 `web`、`ccl __complete web ""` 输出 web 的 flag、`ccl __complete stats --` 含 `--provider`
- **前端资源守卫**：`internal/web` 加两条测试——三个资源必须内嵌且非空；`app.js` 用 `node --check` 过语法检查（环境无 node 时跳过）。意义：看板完全由 JS 驱动，`app.js` 一旦有语法错误，页面会**静默空白**（HTML 正常返回、JS 不执行），没有任何 Go 单测能发现这类故障——本次就是靠这个守卫抓到了首个版本的语法错误
- 真机端到端：注入上下文的真实 Claude Code 会话 → daemon 收到带 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint` 的 payload → `telemetry.db` 里该 session 三个字段都是真值（percent-encode 正确解码回明文）
- 浏览器实测（Playwright）：桌面 / 380px 窄屏均渲染正常，**窄屏页面不产生横向滚动**（宽表在自己的容器里滚）、浅色 / 深色主题均可读、30s 自动刷新实测生效（40s 内发出 2 次请求）、快速连切窗口时**迟到的旧响应不会覆盖当前视图**（AbortController）、`24h` 窗口给 24 个 hourly 桶、`--day` 语义的 daily 桶一致

### OpenSpec
- archive `web-usage-stats-ui` (2026-10-27)

### Compatibility
- **`ccl stats` 的 `PROJECT` / `PROVIDER` 列从"恒空"变为"有值"**（修复，非破坏）。变化来源：新会话来自 launch 上下文，旧会话来自 jsonl 兜底回填。**实测本机 48 条 session 里 31 条回填出了真实 cwd**（其余是 Claude Code 已清理的旧会话，按"未知项目"处理）。依赖旧输出（空 PROJECT 列）做对照的脚本需重算
- **`ccl stats` 新增三个 flag**（`--by-model` / `--model` / `--provider`）；既有 flag 的语义与输出格式不变
- **`ccl stats`（不带 `--day`）的 JSON 形状不变**：`ByModel` / `Model` / `Provider` 只进查询层，不进 JSON 输出
- **db schema 变更**：`sessions` 表新增 `provider_endpoint TEXT NOT NULL DEFAULT ''`（`PRAGMA user_version` 1 → 2，幂等 `ALTER TABLE ADD COLUMN`）。旧 binary 读新库会忽略该列，新 binary 读旧库会自动补列——**不丢数据、不动其它列**
- **新增文件 `~/.cc-launcher/web-token`**（权限 `0600`）：旧 binary 不认识它，留着无害
- **新增顶层子命令 `ccl web`** + 顶层 `--help` 的子命令列表多一行；`ccl __complete` 的顶层候选多 `web`
- **`ccl stats --help` / `ccl web --help` 现在 exit 0 并打印本命令的用法说明**（此前打印 Go flag 的 usage 列表并 exit 1）
- ⚠️ **已知风险（既存，非本次引入）**：同一个 `:4318` 上的 **OTel ingest 端点（`/v1/metrics`）仍无鉴权**——daemon 绑 `0.0.0.0`，同局域网的任何设备都能往里灌伪造 metrics。本次只给 UI 路由加了 token 保护，ingest 端点保持原样
- ⚠️ **provider 是"启动时的快照"**：provider 文件改名后，历史 session 仍显示旧名（标签语义，不是 bug）；**直接 `claude` 启动的会话 provider 恒空、显示「未知」**——ccl 不会拿"当前配置的 provider"去回填历史会话

## [0.14.1] - 2026-10-21 — windows-release-tooling

### Features
- **scoop 更新不再报"有进程在运行"**（改的是 `wzx-xle/scoop-bucket` 的 manifest）：scoop 卸载时用 `test_running_process` 检查**路径在 `scoop\apps\cc-launcher\` 下**的进程，有就直接报 `The following instances of "cc-launcher" are still running. Close them and try again.` —— 而 OTel daemon（`cc-launcher.exe serve`）是**常驻**的， exe 就在那个目录里 → 所以"每次更新都提示"。manifest 加 `pre_uninstall` hook（在 scoop 的进程检查**之前**执行）：先 `ccl serve --stop` 优雅停 daemon（顺带清 pid 文件），再强杀 app 目录里残留的 cc-launcher 进程（Windows 上 launcher 是父进程、`cmd.Run()` 等 claude 子进程 → 会话期间也在），最后 sleep 让文件句柄释放。⚠️ scoop 的 `pre_uninstall` 用的是**安装目录里存的 manifest 副本**（`installed_manifest`），不是 bucket 的当前 manifest → **v0.14.0 这一次更新仍需手动 `ccl serve --stop` 一次**，装上 v0.14.0 之后所有更新自动生效
- **Windows 上也能完成发布流程的最后一步**（`bin/bump-scoop.ps1`，与 `bin/bump-version.ps1` 对称）：scoop 用户就是 Windows 用户，但发布流程的"更新 scoop manifest"这步只有 bash 版（`build.sh bump-scoop`），而它依赖两个 Windows 上默认没有的东西——`zip` 命令（Git Bash 不带）和 `python3`（Windows 自带的 `python3.exe` 是 Microsoft Store 的 App Execution Alias，会弹商店）。新增 PowerShell 版，**只用内置能力**（`Invoke-RestMethod`），不需要 Git Bash / python3 / gh；hash 同样取自 release body 的 `sha256` marker（不打本地 zip——它是 verify-only，且本地 zip 的 hash 物理上不可能等于 release 的）。**刻意不用 `gh`**：gh 没有官方 Windows 版，Windows 用户得靠 scoop/choco 装；python 更通用
  - Windows 实机（用户 2026-10-21 反馈）另修两个 PS 5.1 专属的坑：① **.ps1 必须带 UTF-8 BOM**——PS 5.1 读无 BOM 的 .ps1 按 Windows-1252 解码 → 中文注释乱码 → 语法错误（PS 7 默认 UTF-8 无 BOM，**在 PS 7 上测测不出来**）；② **PS 5.1 的 `>` 重定向产出 UTF-16LE**（`Out-File` 默认编码 Unicode）→ 写出来的不是 UTF-8 JSON → 新增 `-OutFile` 参数，由脚本自己按 UTF-8 无 BOM + LF 落盘
  - 配 `internal/release/psscript_test.go` 守卫 BOM / CRLF：`.ps1` 行尾由 `.gitattributes`（`*.ps1 text eol=crlf`）固定，但**任何在 Linux 上用普通工具编辑 .ps1 的动作都可能悄悄破坏 BOM/CRLF**（Python 通用换行转换会把 CRLF 变 LF）→ CI 变红
- **`build.sh bump-scoop` 的 stdout 之前不是纯 JSON**：`cross_build` 的"==> 交叉编译 …"日志走 stdout，混进了 manifest 输出 → `> cc-launcher.json` 写出来的不是合法 JSON（以前靠人工裁剪）。现在两脚本统一契约：**stdout 只出 manifest JSON，进度/警告/错误全走 stderr**；两平台 stdout **字节完全一致**（同一 md5）
- **README 补 Windows 开发章节 + 澄清"打包"的三层含义**（本地编译产物 ✅ / 发到 GitHub Release 的 zip 只有 CI 能做且本地复现不了 / 更新 scoop manifest）：Windows 用户按需求选工具（写代码直接用 `go build`/`go test`，不必装 Git Bash；打 tag 用 `bin/bump-version.ps1`；bump scoop 用 `bin/bump-scoop.ps1`）
- **release 流程有了可测试的分层验证手段**（`docs/release-smoke-test.md`）：此前 release 逻辑全部埋在 `.github/workflows/release.yml` 的 inline python/shell 里**零测试覆盖**，且仓库**PR 阶段零 CI**——2026-10-21 一次发版连续踩三个 workflow bug，每个都要"推 tag → 现场炸 → 手工修 release"。现在：
  - `internal/release`（新包）：CHANGELOG 版本段提取 + SHA256 marker 解析，带单测覆盖 2026-10-21 实际踩到的每个坑（版本号 `v` 前缀 / 正则缺行首锚点误匹配正文 / CHANGELOG 写法与提取器漂移 / marker 新旧两种格式）；`cmd/release-notes` 是给 workflow 与 `build.sh` 共用的 CLI，**消除两处解析逻辑各自漂移**
  - `.github/workflows/ci.yml`（新）：push main / 提 PR 自动跑 `go build` / `go vet` / `go test ./...` / **多时区 `go test`** / `go test -tags smoke ./...` + `actionlint` + **用仓库真实 `CHANGELOG.md` 跑一遍提取器**（CHANGELOG 写法漂移的哨兵）
  - `release.yml` 的 `workflow_dispatch` 从**半残**改成真能用的 dry-run：新增 `dry_run`（默认 true）/ `version` 两个 input，dry-run 完整构建 + 打包 + 算 hash + 提取 CHANGELOG 段并全部打印，**不创建 release、不上传资产、不改 release body**（此前 dispatch 时 `ref_name` = 分支名 → `gh release create main --verify-tag` 必挂，"手动触发测试"这条路是断的）
  - `docs/release-smoke-test.md` 补**执行环境矩阵**：本机（+08 时区 / 装了 claude / 真实 HOME / API 限流）与 CI runner（UTC / 无 claude / 干净 HOME / 有 token）的差异表 + 四条硬规则（测试不依赖本机程序、不依赖真实 HOME、时区敏感逻辑多时区验证、调 API 带 token）

### Fixes
- **Windows zip 里的 `ccl.cmd` 改用 CRLF 行尾，并收敛成单一来源**：`.cmd` 是 cmd.exe 读的批处理文件，Windows 惯例（也是 cmd.exe 自己写出来的）是 **CRLF**；而 v0.14.0 及之前的 zip 里是 **LF**——那是 bash heredoc 的默认行为，且**这个 shim 从未在真 Windows 上执行过**（CI 只构建、不运行产物；`launcher_windows_test.go` 因 build tag 在 CI 上永远编译不到）。cmd.exe 的行解析在 label / goto / 块结构（`(` `)`）上有已知的 LF 坑，改成显式 CRLF 更稳。同时把 shim 的**两份实现**（release workflow 的 heredoc + build.sh 的 `ccl_cmd_shim` 函数）合并成一份：字节统一放在 `internal/release/shim.go`，release workflow 与 `build.sh` 都通过 `cmd/release-notes windows-shim` 取——避免再出现"两处各写各的、必然漂移"（与本次修掉的 marker 解析漂移同源）。改动会改变 zip 内容 → **下一个版本的 zip hash 会变**（scoop manifest 的 hash 一律取 release body 的 marker，不受影响）。**注意：v0.14.0 已发布的 zip 里 ccl.cmd 仍是 LF**（该修复在下一版生效；LF 版大概率能跑——就 2 行、无 label/goto/块结构，但确实从未在真 Windows 上执行过）
- **release workflow 的 job 间竞态导致 Windows zip 挂不上 release**：`windows-amd64` job 的两个 body 注入步骤（inject CHANGELOG / append SHA256 marker）跑在 `upload release assets` **之前**，却假设 release 已存在——而 release 是由 5 个 matrix job 里**谁先跑完谁创建**（`softprops/action-gh-release` 在各 job 末尾）。v0.14.0 实测踩中：windows job 抢在其它 job 之前跑 → `gh api /releases/tags/v0.14.0` 404 → job 失败 → `upload release assets` 步骤**根本没执行** → release 上只有 4 个 tar.gz、**缺 `cc-launcher-windows-amd64.zip`**，body 为空。修复：
  - 新增 `ensure release exists` 步骤（body 注入前显式保证 release 存在，`--verify-tag` 防建错 tag），body 注入不再依赖 job 间执行顺序
  - `upload release assets` 步骤**前移到** body 注入之前——资产是核心产出，body 只是展示层；body 注入再失败也不影响资产上传
  - 顺带确认 `softprops/action-gh-release` 更新已有 release 时 body 取 `workflowBody || existingReleaseBody`（未传 body 输入 → 保留已有 body）→ 其它 matrix job 后续 update 不会冲掉已注入的 CHANGELOG 段 / hash marker
- **`build.sh bump-scoop` 的取 hash 链路在本机必失败**：`fetch_release_zip_hash_gh_api` 未认证调 GitHub REST API，而未认证限流是 **60 req/hr**、本机出口 IP 是共享的 → 额度经常被打满，返回 `API rate limit exceeded` → 3 次重试全失败 → 拿不到 marker → bump-scoop 直接退出。修复：自动取 token（`GITHUB_TOKEN`/`GH_TOKEN` 环境变量 → `gh auth token` → 匿名兜底）并带上 `Authorization` + 显式 `User-Agent`，认证额度 5000/hr；顺带把 marker 提取从 `grep -oP`（GNU 专有，macOS 自带 BSD grep 不支持 → 在 macOS 上恒失败）换成可移植的 `grep -o` + BRE 区间 `\{64\}`
- **release body 的 CHANGELOG 段注入从来就没生效过**：`VERSION` 取自 `github.ref_name`（`v0.14.0`，**带 v**），提取正则是 `## \[v?` + version + `\]` → 只能匹配 `## [v0.14.0]`，而本仓 CHANGELOG 标题格式是 `## [0.14.0]` → **永远匹配不上** → v0.12.0 ~ v0.14.0 的注入全部静默 skip（用户打开 release 页面只看到一个 HTML 注释，看不到任何内容）。修复：先剥掉 version 的前导 `v`，`v?` 保留 → `## [0.14.0]` 与 `## [v0.14.0]` 两种写法都命中；且"文件在但段落找不到"从静默 skip 改为**直接 fail**（静默 skip 正是这个 bug 藏了三个版本的原因；`CHANGELOG.md` 文件本身不存在时仍 warning + skip）。正则另加**行首锚点**（`^## \[...` + 标题后必须行尾/空格）——否则 CHANGELOG 正文里出现的字面量（例：本文档自己描述这个 bug 时写的 `## [v0.14.0]`）会被当成标题，抽出半段散文写进 release body

### Compatibility
- **Windows zip 的内容变了**（`ccl.cmd` 从 LF 改成 CRLF）→ **zip 的 SHA256 与 v0.14.0 不同**。scoop manifest 的 hash 一律取 release body 的 `sha256` marker → 更新 manifest 时自动带上新 hash，无需人工干预；但**任何按 v0.14.0 的 hash 做校验的地方（缓存、镜像源）都会失配**
- **`build.sh bump-scoop` 的 stdout 契约变了**：之前 stdout 里混着 `==> 交叉编译 …` 的进度日志，manifest JSON 只能靠人工裁剪才能用；现在 **stdout 只有 manifest JSON**，进度/警告/错误全走 stderr。解析旧 stdout 输出的脚本需要改成只取 stdout
- **`ccl` 本身的 CLI flag / 输出格式 / db schema 全部未变**；本版本只影响**发布产物内容**与**发布工具链**，不影响 ccl 的运行时行为

## [0.14.0] - 2026-10-20 — stats-session-start-time-fix

### Fixes
- **`sessions.started_at` 记的不是 session 起始时刻，而是"最后一次 OTLP export 到达 daemon 的时刻"**：`internal/collector/parser.go` 建 session 行时用 `time.Now()`（= batch 处理时刻），`internal/collector/store.go::insertSession` 用 `INSERT OR REPLACE` 每次 export 整行覆盖 → 最终 `started_at` = 该 session 最后一次 export 的时刻。实测本机 43/43 session 的 `MAX(metrics.captured_at) == sessions.started_at`（误差 < 1s），以 `claude_code.session.count` 的时间戳为真值时 **25/41（61%）** 的 session 偏晚，最大 **64,479 秒 ≈ 18 小时**。后果：① 跨天 session 的 token 在 daily 桶里全堆到最后活跃那天；② `WHERE s.started_at >= since` 按"最后 export 时刻"划窗 → 数天前开始、今天仍在跑的 session 把**全生命周期** token 一次性计入。与 Anthropic 侧逐日对账偏差**双向且巨大**（实测 0.07x ~ 4.76x，非系统性倍数）。修复：
  - `parser.go`：`Session.StartedAt` 改取真实 session 起始时刻——优先 `claude_code.session.count` metric 的 `captured_at`（session 创建时刻的权威观测），无它时回退同 batch 内该 session 最早的 `captured_at`；**不再用 `time.Now()`**
  - `store.go::insertSession`：`INSERT OR REPLACE` → `INSERT ... ON CONFLICT(id) DO UPDATE`，其中 `started_at = MIN(sessions.started_at, excluded.started_at)`（只降不升，后续 batch 永远推不前），其余列（`ended_at` / `provider` / `cwd` / `model`）保持"后续 batch 覆盖"语义
  - `store.go`：新增 `backfillSessionStartTimes` + `PRAGMA user_version` 驱动的一次性历史回填（`COALESCE(session.count 的 MIN(captured_at), metrics 的 MIN(captured_at))`），修正**全部历史** db 的 `started_at`；没有任何 metric 的 session 行原样保留（不写 NULL、不删行）；回填幂等，每个 db 只执行一次
- **daily 桶的归桶键从 `s.started_at` 改为 `m.captured_at`**（按**消费时刻**归集，与 hourly 统一）：跨天 session 的 token 按实际消费日分布到各天，不再全堆在开始那天——这是逐日偏差的主因。桶表 `SESSIONS` 列**仍按 `s.started_at` 归桶**（"该时段开始了多少个 session"），与 token / cost 列（"该时段消耗了多少"）是两个独立的量，语义各正、不可混为一谈
- **detail 视图的窗口过滤从 `s.started_at >= since` 改为 `m.captured_at >= since`**：按用量发生时刻划窗，与 OTel 侧"按 API 调用发生时刻归集"口径一致；窗口外没有活动的 session 不再出现
- **`--day N` 时 detail 的 `since` 对齐到最旧桶那天的本地 00:00**：原实现 detail 覆盖 `[now-Nd, now]`、桶覆盖 `[todayStart-(N-1)d, todayStart]`，两者错开约一天 → detail totals ≠ Σbucket rows。对齐后两者完全重合（`--day 7` 恰好 7 个 daily 桶）
- **`bucketSkeleton` 的天数计算改四舍五入**：`(todayStartMs-sinceMs)/24h` 的向下取整在**有夏令时的本地时区**（春季调时）会少算一天、少一桶；四舍五入保证 `--day N` 恒定 N 个桶

### Compatibility
- **`ccl stats`（不带 `--day`）的 session 行语义变更：从"session 全生命周期用量"改为"窗口内用量"**——一行只累加 `captured_at` 落在窗口内的 metric。窗口前开始、窗口内仍在消耗的 session 仍会出现（`STARTED` 列仍显示真实起始时刻，可能早于窗口），但只显示窗口内消耗的 token；窗口外活动的 session 不再出现。理由：统计工具的默认视图应当是"这段时间用了多少"，而不是"这些 session 一辈子用了多少"；后者可由不带时间窗的独立查询提供（本次不做）。依赖旧口径做对照的脚本 / 手工数字需重算
- **`--json` 的 `since` 字段值会变早**：`--day N` 时从"7 天前的同一时刻"改为"最旧桶那天的本地 00:00"（最多早 1 天）；不带 `--day` 时不变（仍是 7 天前，不做午夜对齐）
- **db 列结构 / CLI flag / JSON 字段名 / 表格格式全部不变**；无 schema 迁移，只用 `PRAGMA user_version` 从 0 → 1 标记一次性回填已执行
- **历史数据立即生效**：桶视图按 `metrics.captured_at` 归集，对**全部历史**立即正确；`sessions.started_at` 经 daemon 启动时的一次性回填修正（`STARTED` 列随之修正）

### OpenSpec
- archive `stats-session-start-time-fix` (2026-10-20)

## [0.13.3] - 2026-10-18 — release-notes-changelog-injection

### Features
- **GitHub release 页面用户视觉上能看到 CHANGELOG 段**：`softprops/action-gh-release` create release 时 body 是空的，windows-amd64 job 末尾只 append `<!-- sha256:auto-hash:HEX -->` 一个 HTML 注释——用户打开 release 页面看不到任何内容。修复：windows-amd64 job 内新增独立 step `inject CHANGELOG [v0.X.Y] section to release body`（跑在 append-hash step 之前），用 python 从仓库根 `CHANGELOG.md` 提取 `## [v0.X.Y]` 段（H2 + 后续内容到下一个 `## [` 之前，含 H3 子段 Refactor / Fixes / Features / OpenSpec 等），`gh release edit --notes-file` 写入。CHANGELOG 段找不到时 stderr warn + skip（不 fail workflow），append-hash step 仍跑。最终 release body = `<CHANGELOG 段>\n\n<!-- sha256:cc-launcher-windows-amd64.zip:HEX -->`。

### Refactor
- **hash marker 改带文件名**：`<!-- sha256:auto-hash:HEX -->` → `<!-- sha256:cc-launcher-windows-amd64.zip:HEX -->`（`auto-hash` token 替换为真实 zip 文件名），build.sh `fetch_release_zip_hash_gh_api` regex 同步改通用格式 `sha256:[^:]+:\K[a-f0-9]{64}(?=\s*-->)` 向前兼容 v0.12.0 ~ v0.13.2 旧 marker。

### OpenSpec
- archive `release-notes-changelog-injection` (2026-10-18)

## [0.13.2] - 2026-10-18 — stats-day-no-detail-table

### Fixes
- **`ccl stats --day N` 砍 detail 表，只输出桶视图**：`--day` 路径原本在桶聚合表前还打一份 session 明细（`FormatTable`） + `Window since: ... Group by: ...` 头，是 v0.12.0 加 `--day` 时遗留的 UX bug——用户传 `--day` 的语义是"看 hourly/daily 趋势"，不关心每个 session 明细（要明细用 `ccl stats` 不传 `--day` 即可），detail 把桶表顶到屏幕底部看不见。本次：
  - `cmd/cc-launcher/statscmd.go::runStatsWithQuery` `--day != -1` 分支：删 detail 头 + `FormatTable` 调用 + 空行；只剩 `(no data in window ...)` hint（detail 空时）+ 桶聚合表
  - `smoke_test.go` sub-test `a` / `b` 加"无 `Window since:` / `Group by:` / `STARTED`" 断言
  - `--day` 缺省路径（detail-only）**不动**；`--json` 输出 shape **完全不动**（`sessions` + `totals` + `bucket_rows` + `bucket_totals` 全保留）
  - base spec `time-buckets` Requirement 1 改文字：明确"`--day N` MUST print ONLY the bucket table, NOT a preceding detail table"；Scenario 加"stdout does NOT contain `STARTED` / `Window since:` / `Group by:`"

### OpenSpec
- archive `stats-day-no-detail-table` (2026-10-18)

## [0.13.1] - 2026-10-18 — stats-clock-injection

### Fixes
- **`ccl stats` smoke test 跨日期边界稳定**：`internal/stats` 包对时钟不可控（`QueryStats` / `QueryBucketSummary` / `DefaultSince` 硬编码 `time.Now()`），smoke test `TestStatsTimeBuckets` 顶层 `collector.OpenStore.WithClock` 注入 2026-09-30 17:30 mock today 但 stats 路径走真实 today，expected dates `09-24..09-30` 在真实 today 跨过 9 月底后必然 fail（v0.12.0 起 pre-existing bug）。修复：
  - `stats.Query` struct 新增 `Clock func() time.Time` 字段（nil = fallback `time.Now`，向后兼容）
  - `QueryBucketSummary` 签名加 `nowFn func() time.Time` 第 4 参数（nil = fallback `time.Now`）
  - `QueryStats` 内部 `time.Now()` 全部走 `q.now()` / `q.Clock` 注入
  - `cmd/cc-launcher/statscmd.go` 抽 `runStatsWithQuery(base stats.Query, args)` 内部函数，`runStats` 改为 thin wrapper（main.go 调用不变）
  - smoke test 10 个 sub-test 全部走 `runStatsWithQuery(stats.Query{Clock: nowFn}, args)`，与 collector store 共享 mock today

### OpenSpec
- archive `stats-clock-injection` (2026-10-18)

## [0.13.0] - 2026-10-16 — stats-bucket-tokens-column-numeric

### Refactor
- **`ccl stats --day N` 桶聚合表 `TOTAL_TOKENS` 列改数字合计**：原用 20 字符 Unicode 柱（`▏▎▍▌▋▊▉█`，最右 8 半块字符集 + 零值 `·` 占位 + partial `⏳` 后缀 + TOTAL 行 `─── (total)` 横线）展示每桶 token 合计。改为 `humanizeTokens(IN+OUT+CACHE_CRE+CACHE_READ)` 数字列（`2.50K` / `150` / `0`），与 IN/OUT/CACHE 列同形态；零值桶天然 = `"0"`，partial ⏳ / 横线 separator 全删。hourly 模式的 `BUCKET` 列末尾 `*` partial marker 保留（与柱无关）。spec `time-buckets` Requirement 1 / 4 与对应 Scenario 同步改文字；archive 合并到 base spec。

### Compatibility
- `--json --day N` 输出**字节级不变**（`bucket_rows` / `bucket_totals` 仍是原始 int64 + 4 位 cost），JSON 消费者零影响。文本消费者：原正则 grep `TOTAL_TOKENS` 列的 `█` / `⏳` 等柱字符的脚本需更新；该列文本 shape 已变、文本本就是 human-readable、不可作 stable API。

### OpenSpec
- archive `stats-bucket-tokens-column-numeric` (2026-10-16)

## [0.12.0] - 2026-10-14 — tui-mode-default-marker + windows-ccl-detach-rollback

### Features
- **TUI 权限模式页默认项视觉变更**：默认项行首 dim `[*]` 标识（原行尾 "默认" tag 移除），与提供商选择页对齐。`renderModeView` marker 三态逻辑（`[ ]` / dim `[*]` / 亮 `[*]`）跟 `renderProviderView` 同构；cursor 行依然用 `cursorStyle` 整行高亮，dim `[*]` 仅在默认项不在 cursor 行时可见。无 state.json schema / CLI / launcher 改动。

### Fixes
- **Windows 上 Claude Code 收不到任何键盘事件（含 Ctrl+C）**：v0.11.1 (`9f9c110`) 引入的 detach 设计让 ccl 启动 Claude Code 后立即 `os.Exit(0)`，但 Windows console 的 input 是 kernel-managed 结构化事件流（不是 Unix 那种 generic byte stream），由 kernel 在 foreground process 持有该 console 时把 keystroke 投递到 console input queue。ccl 立即退 0 后 PowerShell prompt 立即抢回 interactive read 状态，在 Claude Code 完成 Ink TUI 初始化（几百毫秒到几秒）之前从 console input queue 截走所有 keystroke —— Claude Code 进入 read state 时 input queue 永远为空，连 Ctrl+C 也无效（Ctrl+C 是 console event 走同一路径）。撤回 detach 设计，恢复 v0.11.0 之前的 `cmd.Run()` 同步 wait + `os.Exit(exitErr.ExitCode())` 透传 exit code 行为。任务管理器中 `cc-launcher.exe` 会重新出现于 Claude Code 整个生命周期（v0.11.0 已知状态），作为 v0.11.1 detach 的反向 trade-off。
- **`build.sh <subcmd>` 失败 → 无限递归**：commit `2a8d1a5` 把 `build.sh` 函数 `local_install()` 重命名为 `local()`，与 bash builtin/reserved word `local` 同名（动态作用域下函数阴影 builtin）。`version_ldflags` 第 41 行 `local version commit` 命中函数 `local`（而不是 builtin）→ 函数 `local` → `build` → `$(version_ldflags)` 命令替换 → 再次进入递归。所有走 `build()` 的子命令（`build` / `install` / `local` / `run` / `windows` / `release` / `bump-scoop`）全坏，只有 `clean` / `test` / `contract` 不受影响。修复：函数名改回 `local_install()`，case dispatch 同步更新；对外子命令名 `local` 不变。
- `.github/workflows/release.yml` 注释里残留的旧子命令名 `scoop-bump`（v0.11.2 已 rename 为 `bump-scoop`）同步更新为 `bump-scoop`。

## [0.11.2] - 2026-10-13 — release-assets-tar-gz

### Breaking Changes
- **release 资产结构调整**：
  - 去掉 `windows/arm64`（matrix 减到 5 target：windows/amd64 + linux/{amd64,arm64} + darwin/{amd64,arm64}）
  - Windows 资产只 `cc-launcher-windows-amd64.zip`（不再单独传 `.exe`）
  - Linux/Darwin 资产改为 `cc-launcher-{os}-{arch}.tar.gz`（内含 `cc-launcher` 已 `chmod +x` + `README.md` + `LICENSE`，不再单独传裸 binary）

### Fixes
- `go build -trimpath` 移到命令行（不是 ldflags 的一部分；Go 1.21+ link 器拒绝作为 ldflags flag）。`build.sh build/cross_build` 与 `.github/workflows/release.yml build step` 同步加 `-trimpath`。v0.11.0 / v0.11.1 release workflow 因 ldflags 错误可能失败；v0.11.2+ 用新命令行为正确。

## [0.11.1] - 2026-10-13 — windows-detach-build-reproducible

### Breaking Changes
- **Windows ccl 启动 Claude Code 后立即退 0**：之前是 `cmd.Run()` wait 子进程退出后透传 exit code，现在改为 `cmd.Start()` 启动后立即 `os.Exit(0)`。ccl 进程不再在任务管理器里持续存在（与 Unix `syscall.Exec` 进程 image 替换的用户感知一致），但 **claude 退出 code 不再传给 ccl 调用者**。Claude 退出 code 由 `cmd.exe → node.exe → claude.exe` 进程链传递给 PowerShell 的 `$LASTEXITCODE`。写 `ccl && echo OK` 类脚本在 claude 失败时误判成功——这是预期 trade-off。

### Refactor
- `cmd/cc-launcher/version.go`：移除 `BuildTime` 字段与 `ccl --version` 输出中的 "Built" 行。`BuildTime` 每次 build 注入当前时间让 Go 二进制 byte-level 不可重现（同 commit + 同 source 下两次 build 也不同），导致 zip hash 跨环境无法对齐。`git log` + `Commit` 字段已足够追溯具体 build 时间。

### Build
- `build.sh version_ldflags` 加 `-trimpath`：剥离 GOMODCACHE 等 build 机器路径，让二进制在跨 build 目录（同 source code）下 byte-level 一致。
- `.github/workflows/release.yml`：go build 命令同步加 `-trimpath`，并删除 `-X main.BuildTime=`（与 version.go 字段移除对齐）。

### Compatibility
- `ccl --version` 输出从 5 行（Version / Commit / Built / Go / OS-Arch）变为 4 行（删 Built）。如脚本解析 `Built:` 行需更新。
- **v0.11.0 GitHub release zip 的 hash 不变**（v0.11.0 release workflow 跑在旧代码上），但**v0.11.0 之后**用新流程的 zip hash 在跨环境（CI runner vs 本机）byte-level 一致。
- 用户场景：
  - 已用 v0.11.0 release 安装的用户：升级 scoop manifest 用新流程后必须 `scoop update cc-launcher` 重新下载（hash 不同）
  - v0.11.1+ 发布：本机 `./build.sh scoop-bump v0.X.Y` 算的 hash byte-level = release workflow 出的 hash，**不**需要再从 GitHub download zip 算 hash

## [0.11.0] - 2026-10-13 — provider-list-display-improvements

### Features
- **Provider list 视觉变更**：TUI 选择 provider 时默认项用左侧灰色 `[*]` 标识（原行尾"默认" tag 移除）。非默认项仍为 `[ ]`；cursor 行整行高亮。
- **Provider 配置新增 `note` / `order` 字段**：
  - `note` (string)：一行备注，渲染在 provider 名后、空格分隔、灰色显示
  - `order` (number)：排序权重，越小越靠前；缺省 0；同 order 时回退到文件名字典序
  - 旧 provider JSON 文件（无 `note` / `order`）继续工作
- **`ccl provider --new` 写盘后追加可选 `note` 询问与 `order` 建议值提示**：
  - 写盘成功后 `note (可选备注，回车跳过):` 让用户填 note（非空时通过 ReadFile + JSON patch 写回顶层 `note` 字段，不动 `env` 块）
  - 写盘成功后打印 `排序值: <max+10> (现有 max=<M>, 建议 max+10；手改 JSON order 字段覆盖)`（只显示不读输入）
  - note 阶段 EOF / 空字符串 = 跳过；template prompts 阶段 EOF = 取消（沿用现有语义）

### Tests
- `internal/provider/provider_test.go`：加 6 个新单元测试覆盖 `note` / `order` 解析、排序、容错、非数字 `order` 标 Error、errored 仍排序
- `internal/tui/tui_test.go`：加 5 个新单元测试覆盖 dim `[*]` 预选、note 渲染、note 在 errored 状态被压制、无预选时所有 marker 为 `[ ]`、行尾"默认" tag 移除
- `internal/cli/cli_test.go`：加 6 个新单元测试覆盖 `--new` 写盘后 note 询问（空跳过 / 非空写入）、order 提示（max+10 / 零 baseline / 排除新文件）、`ccl provider --list` 排序软测试

### OpenSpec
- 归档 change `provider-list-display-improvements` → `2026-10-13-provider-list-display-improvements`
- 新增 capability `provider-list-rendering`（5 个 ADDED Requirements + 13 个 Scenario）
- Modified capability `provider-new-guided-flow`（1 个 MODIFIED + 1 个 ADDED Requirement；锁定"template field prompts"边界，EOF 取消语义在 template 阶段保留）

## [0.10.0] - 2026-10-10 — template-driven-preask

### Refactor
- `cli.newProvider` 160 行混合职责拆开，模板自描述 + cli 数据驱动
- `templates.Template` 加 `PreAsks []PreAsk` 字段（模板声明字段收集前的额外问答）
- `templates.Template` 加 `PostOverrides []FieldOverride` 字段（模板声明字段收集后的字段值覆盖）
- `templates.TemplateField` 加 `SkipPrompt bool` 字段（模板声明此字段跳过 prompt 直接写入）
- `cli.newProvider` 拆成 `collectPreAsks` / `collectFields` / `applyPostOverrides` 三阶段函数
- cli 端去除所有 `tpl.Name == "aliyun-pay"` / `"minimax"` 硬编码（3 处），改为模板元数据驱动

### Templates
- `minimaxTpl` 加 `PreAsk (region)` + `PostOverride (BASE_URL)` + `ANTHROPIC_BASE_URL.SkipPrompt = true`
- `aliyunPayTpl` `ANTHROPIC_BASE_URL.SkipPrompt = true`（其它字段零修改）

### Tests
- `internal/templates`：加 4 个新单元测试（`TestMinimax_PreAskRegion` / `TestMinimax_PostOverride_BaseURL` / `TestAliyunPay_NoPreAsk` / `TestAliyunPay_BaseURL_SkipPrompt` / `TestMinimax_BaseURL_SkipPrompt`）
- `internal/cli/cli_test.go`：零修改（行为不变——5 个 guided-flow 测试原样通过）

### OpenSpec
- 归档 change `template-driven-preask` → `2026-10-10-template-driven-preask`
- 新增 capability `template-preask`（4 个 ADDED Requirements + 4 个 Scenario 合并到 `openspec/specs/template-preask/spec.md`）

### Compatibility
- 行为完全不变：`ccl provider --template minimax` / `--template aliyun-pay` 输出 JSON 与 v0.9.0 一致
- 现有 user 配置（`~/.cc-launcher/providers/*.json`）不受影响（只动创建流程，不动读取）
- 跨平台编译验证：linux / darwin / windows 三平台 build 通过

## [0.9.0] - 2026-10-09 — provider-setenv-cwd-binding

### Features
- **provider setenv 注入取代 settings.json 字段**：`runTUI` 通过子进程环境注入 provider 配置；
  `settings.json` 不再保留 `provider` 字段（架构约束 #6 落地）
- state 层加 `dir_bindings` 字段 + `Get/SetProviderForDir` 方法
- `launcher.Options.Env` 字段 + 跨平台 env 透传 + `envutil.Upsert` helper
- TUI 末尾 `stageRemember` + cwd binding 预选 + `runTUI` wiring
- `ccl provider --list` 显示 cwd binding `*` 标记，CLI 切换同步写 binding
- `__init` PowerShell 脚本 + `topHelp` 加 PowerShell 行

### Migration
- `migrateSettingsJSON` 一次性清理老 ccl 注入到 settings.json 的 `ANTHROPIC_*` 字段
  （首次启动自动触发，无需手动介入）

### Chore
- 删除 `internal/config/ordered.go` 死代码 + 修正 `completion.go` 错误信息
- §7 跨平台编译验证 checkbox 标 done
- `TestE2E_OldUserFirstRunTUI_BackupRhythm` 覆盖 §8.6

### Docs
- provider-setenv-cwd-binding §9 文档同步（README + CLAUDE.md）

## [0.8.0] - 2026-10-01 — stats-time-buckets

### Features
- `ccl stats --day N` 时间桶聚合（hourly / daily）

### Fix
- `bump-version` 保守默认：默认本地打 tag，`--push` opt-in 才推

### Docs
- README 用量监控 + tab completion 加 `--day`

### Test
- stats 时间桶 smoke + main 同步 completion 候选

## [0.7.0] - 2026-09-30 — cli-shell-completion + 版本号机制

### Features
- shell tab completion（`ccl __complete` + `ccl __init`）
- 引入版本号注入机制：`-V` 显示 + ldflags 注入 + `bump-version` 脚本
- `bump-version` 加 `auto` 子命令——按 conventional commit 自动推断 bump 类型
- `bump-version` 脚本语义化子命令 + 跨 OS + slash command

### Refactor
- bump 类型推断迁到 slash command（Claude 分析 commits）

### Fix
- `Version` 字段简化——只用 tag + dirty 标记，ahead 信息去 `Commit` 字段
- 恢复 `git describe --tags` 原样输出 + 保留 ahead 信息

### Chore
- 跟踪 `.claude/commands/opsx` + skills 配置
- 从 `changes/` 移除已归档的 `cli-shell-completion`

## [0.6.0] - 2026-09-30 — provider-flag-migration

### ⚠️ Breaking（潜在）
- `ccl provider` 子命令迁到 `flag.NewFlagSet`：
  - 接受 `-x` / `--x` 短形式
  - 互斥锁收紧（`--new` / `--template` 互斥显式化）
  - 保留 `--new` / `--template` 约定

### Features
- `ccl serve --restart` flag：热重启 daemon
- status 输出 session 数拆成「总数 + 今日新增」两统计

### Style
- status label 右对齐

## [0.5.0] - 2026-09-30 — flatten-stop-restore-into-serve

### Features
- 把 `stop` / `restore` 子命令整合为 `ccl serve` 上的 flag
- 新增 `ccl serve --status`
- 备份轮转阈值从 10 提高到 30（merge-on-rotate）

## [0.4.0] - 2026-09-29 — backup-on-control-handover + FORWORD_ namespace

### Features
- **FORWORD_ 命名空间**：OTel per-signal metrics endpoint 接管
  - `launcher.injectOTelEnv` 传播 `FORWORD_*` 到子进程环境
  - `envconfig`：backup + rewrite `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` under `FORWORD_` namespace
  - `collector` forwarder triggers on `FORWORD_`；`/v1/logs` / `/v1/traces` 返回 200 only
- `main` 加 `isUnderCclControl` + `injectOTelEnv` 条件 Backup 触发
- 加 `dbinspect` CLI：用于 `telemetry.db` 排查与清理

### Fix
- stats `TOTAL` 行起始时间零值显示 + JSON omitempty

### Test
- 控制权交接 backup 全 Scenario + 边界 case 覆盖
- `envconfig` / `collector` per-signal metrics E2E 覆盖
- §7.3–§7.6 backup 触发场景 sandbox E2E
- §7.7 归档任务完成

### Docs
- 文档化自动控制权交接 Backup 语义
- README 文档化 `FORWORD_` 命名空间 + per-signal metrics endpoint 接管
- 补 5 个 main spec 的 `Purpose` + `Requirements` headers

## [0.3.0] - 2026-09-29 — usage-monitoring-via-otel + daemon-lifecycle

### Features
- **OTel metrics 采集**：HTTP `:4318` receiver + parser + WAL store + forwarder
- **用量查询**：`ccl stats` 直读 SQLite + 表格 / JSON 输出
- **daemon 生命周期**：`ccl serve` + `Setpgid` fork + `ccl stop` + graceful SIGTERM
- `ccl stats` / `stop` / `restore` 子命令 + 5 个 OTEL env 键备份注入
- 9.3–9.8 manual smoke tests 标 done

### Fix
- 对齐真实 Claude Code v2.1.x OTel payload
- `--help` 输出末尾补换行

### Docs
- README 用量监控小节

## [0.2.0] - 2026-09-27 — improve-help-and-provider-templates

### Features
- `internal/templates` 包：7 个已验证模板
- `--help`、`--template` / `-T`、向导式 `--new` flow + `provider.Create` 重写

### Docs
- README 用法表 + 「用模板配置」章节 + 7-type reference

## [0.1.0] - 2026-09-26 — cross-platform-launcher-and-mode-contract（基线）

### Features
- 初始 Go 模块 + 项目脚手架
- state / provider / config / launcher 模块
- CLI 子命令、TUI、主入口
- TUI provider / mode 选择 + 默认 marker
- TUI 会话恢复步骤（`new` / `continue` / `resume-by-id`）
- session 选择默认 new，第三个选项打开内置 session list

### Fix
- 新 provider 使用 `ANTHROPIC_AUTH_TOKEN`（替代 `ANTHROPIC_API_KEY`）
- TUI 简化：用 `[*]` / `[ ]` 选择器替代 bubbletea list
- `settings.json` 有序读写，保留 key 顺序
- 自动清理冲突的 `ANTHROPIC_AUTH_TOKEN` 与 `ANTHROPIC_API_KEY`
- 命令从 `cc` 改为 `ccl`（避免与 C compiler 冲突）
- 使用 persistent `bufio.Reader` 处理交互输入

### Refactor
- 提取 `internal/modes` 包作为单一事实来源
- 提取 `resolveClaude` + Windows 平台支持

### Test
- 通过探查 Claude CLI 行为代替解析 `--help`
- 锁定 permission mode 列表到 claude CLI（保证 TUI/CLI 一致）
- 导出 `ValidModes` + 明确 permission mode 语义

### Docs
- 添加 README + 构建脚本 + 需求文档

[Unreleased]: https://example.com/compare/v0.9.0...HEAD
[0.9.0]: https://example.com/compare/v0.8.0...v0.9.0
[0.8.0]: https://example.com/compare/v0.7.0...v0.8.0
[0.7.0]: https://example.com/compare/v0.6.0...v0.7.0
[0.6.0]: https://example.com/compare/v0.5.0...v0.6.0
[0.5.0]: https://example.com/compare/v0.4.0...v0.5.0
[0.4.0]: https://example.com/compare/v0.3.0...v0.4.0
[0.3.0]: https://example.com/compare/v0.2.0...v0.3.0
[0.2.0]: https://example.com/compare/v0.1.0...v0.2.0
[0.1.0]: https://example.com/compare/initial...v0.1.0