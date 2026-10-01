## Context

`ccl stats` 当前聚焦"明细 + 7 天默认窗口",缺少时间序列视角。用户调研后选择**参数化时间桶**(`--day N`),而非固定 3 行聚合:
- `ccl stats --day 1` → 今天 24 小时 hourly
- `ccl stats --day 7` → 最近 7 天 daily
- `ccl stats --day 30` → 最近 30 天 daily

`internal/stats/query.go` 已有 `buildSessionStatsSQL` 模式(GROUP BY 维度,SUM CASE WHEN),但只产出明细聚合。`internal/stats/count.go` 的 `CountSessionsByWindow` 已示范"轻量两条 COUNT SQL"模式。

需要新增"按时间桶聚合"查询:桶维度 = 小时或本地日历日,每桶一行 sessions + tokens + cost,柱状图可视化总 token 用量。

## Goals / Non-Goals

**Goals:**
- `ccl stats --day N` 输出一张时间桶聚合表(hourly 或 daily),与现有 session 明细并存
- 柱状图长度 = IN + CACHE_CRE + CACHE_READ + OUT(total tokens),Unicode 8 级半块自适应归一化
- humanize 单位:`< 1K` 原样,`≥ 1K` 2 位有效数字 K/M/B
- `--day 1` 含当前小时(partial bucket),桶 label 加 `*` 标识 + 表头注
- JSON 输出在 `--day N --json` 同时使用时包含 `bucket_rows` 字段(数组 + `bucket_totals`)
- 时区语义与 `serve --status` 一致(本地日历日,`TodayStartMillis()`)
- `--day` 不传时,行为与现状字节级一致(向后兼容,用户旧脚本无感)
- `--since` 砍掉(由 `--day` 派生 detail since)
- `db` 不存在仍走现有 `ErrNoDatabase` 错误路径
- `N` 范围校验:1 ≤ N ≤ 90,越界报错退出

**Non-Goals:**
- 不做任意桶粒度(只 hourly / daily 两档,由 N 自动推导)
- 不做"环比 / 同比 / 趋势预测"计算(只看绝对数)
- 不做可配置柱宽(固定 20 字符)
- 不引入新依赖(继续 `modernc.org/sqlite` + `text/tabwriter`)

## Decisions

### D1. 桶 SQL:两条独立 SQL(sessions count + tokens/cost sum)

**选择**:每个 kind 两条 SQL:
- `SELECT bucket, COUNT(*) FROM sessions WHERE started_at >= ? GROUP BY bucket` —— 仅 sessions 表
- `SELECT bucket, SUM(...) FROM sessions LEFT JOIN metrics ... GROUP BY bucket` —— tokens + cost

理由:
- sessions 没 metric 行的 session 也要计入桶数(LEFT JOIN 后 NULL 行会让 COUNT(DISTINCT) 失真)
- `CountSessionsByWindow` 已是"两条独立 COUNT SQL"模式,认知一致
- 分两条 SQL 让 sessions 数 100% 准确(metrics 表可能因 collector 重启丢行,但 sessions 表一旦写入即持久)
- Go 端按 bucket label map merge

拒绝"一条 LEFT JOIN + COUNT(DISTINCT)"的原因:上面提到的 NULL 行问题;且 `COUNT(DISTINCT)` 在 LEFT JOIN 后跨 bucket 时容易算多。

### D2. `QueryBucketSummary` 是独立函数,不内嵌到 QueryStats

**选择**:导出 `QueryBucketSummary(dbPath, kind, sinceMs) ([]BucketRow, error)`,调用方决定是否调用。

理由:
- 单元测试可独立测
- `CountSessionsByWindow` 也是同样独立函数模式
- `QueryStats` 保持单一职责(按 group 维度查明细)

### D3. 桶 kind 枚举 "hour" / "day",`--day N` 自动推导

**选择**:`kind = "hour"` iff N=1,else `kind = "day"`。`QueryBucketSummary` 收 `kind` 参数。

理由:
- 用户不需要显式选粒度(简单)
- hourly 在大 N 时会爆桶(7天×24=168 行),规则固定避免失控
- SQL 两种 kind 走不同分支(`strftime('%H', ..., 'localtime')` vs `strftime('%Y-%m-%d', ..., 'localtime')`)

拒绝"加 `--bucket hour|day` flag 让用户覆盖"的原因:复杂度上升且无人用(7天 hourly 实际是垃圾输出)。

### D4. `Query.Summary` 字段删除,改用 `Query.Buckets *int` 表达"启用桶聚合 + N 值"

**选择**:`Query{Buckets: *int}` + `Result{BucketRows []BucketRow \`json:"bucket_rows,omitempty"\`}` + `BucketTotals BucketRow \`json:"bucket_totals,omitempty"\``。

理由:
- `*int` 区分"未启用"(nil,JSON omitempty 跳过)vs "启用 N=0"(理论值,实际被 CLI 校验拦掉)
- `BucketTotals` 单独字段,文本输出对应 TOTAL 行
- 未传 `--day` 时 JSON 字节级一致(`bucket_rows` + `bucket_totals` 均 omitempty)

拒绝"`Query{Summary bool, Buckets int}`"双字段:bool + int 重复表达同一开关,容易出现 `Summary=true Buckets=0` 的非法状态。

### D5. `TodayStartMillis` 单一来源,跨包漂移风险归零

**选择**:`internal/stats/time.go` 新增导出 `TodayStartMillis()`;`statscmd.go::runStatusInner` 改用 `stats.TodayStartMillis()`;`statscmd.go` 本地的 `todayStartMillis` 函数删除。

理由:
- 桶聚合 today 起点 = `TodayStartMillis()`(同一来源)
- `serve --status` 今日 sessions 数 = `CountSessionsByWindow(dbPath, TodayStartMillis())`(同一来源)
- 两处语义不可能漂移

### D6. 桶 label 与桶内 partial 标记

**选择**:
- hourly 桶 label = `strftime('%H:00', m.captured_at/1000, 'unixepoch', 'localtime')` → 如 `17:00`
- daily 桶 label = `strftime('%m-%d', s.started_at/1000, 'unixepoch', 'localtime')` → 如 `09-30`(text)或 ISO `2026-09-30`(JSON `bucket_iso`)
- JSON 加 `bucket_unix_ms` 字段(消费方友好)
- `--day 1` 当前小时桶 label 加 `*` 后缀,text 输出表头注 `(* = 当前小时,数据仍在累计)`;JSON `is_current: true` 字段
- **partial 桶柱状图末端追加 `⏳`(U+23F3 HOURGLASS WITH FLOWING SAND)**——与 label `*` 形成"文本标记 + 视觉标记"双重提示;归一化按已累计值算柱长必然偏短,`⏳` 让用户一眼识别 partial 桶,避免误读为"低用量"

理由:
- `localtime` modifier 跟 `TodayStartMillis` 一致
- `*` 后缀文本最小侵入(单字符视觉提示)
- JSON 用独立字段(结构化,机器友好)
- partial 桶的柱短是**结构问题不是数据问题**,需要专门符号标识;`⏳` 比色块/反色更轻量,避免引入 ANSI 转义依赖

### D7. humanize 策略:`< 1K` 原样,`≥ 1K` 2 位有效数字 K/M/B

**选择**:`humanizeTokens(n)` 函数:
```
0        → "0"
1-999    → 原样整数("350")
1,000+   → 2 位有效数字("1.23K" / "12.3K" / "123K" / "1.23M" / "12.3M" / "123M" / "1.23B" / "12.3B")
```

理由:
- 2 位有效数字保留精度(12345 → 12.3K vs 12K 更精确)
- `< 1K` 不强行 K 单位(避免 "0.35K" 这种人类反感的写法)
- 单测函数 `TestHumanizeTokens` 覆盖边界

拒绝"3 位有效数字"的原因:1234567 → "1.23M"(2 位)vs "1.235M"(3 位)——3 位在小窗口里视觉噪声大。

### D8. 柱状图归一化 + Unicode 8 级半块 + 零值占位 + TOTAL 行横线分隔

**选择**:`bucketBar(tokens int64, max int64) string`:
- 柱长固定 20 字符
- `cells = round(tokens / max * 20 * 8)`(8 = Unicode 半块级数)
- 字符选择:`▏▎▍▌▋▊▉█`(完整 8 级,1/8 到 8/8 全覆盖——**补 `▉` 修正**此前 7 级字符集丢级问题)
- **除零保护 + 零值占位**:`max == 0` 或 `tokens == 0` 时直接返回 `·` + 19 个空格(1 个 U+00B7 MIDDLE DOT 占位 + 19 padding),不进入 cells 计算
- **TOTAL 行**:不调用 `bucketBar`,改输出 20 个 `─`(U+2500 BOX DRAWINGS LIGHT HORIZONTAL)+ ` (total)` 文本,作为视觉分隔锚点

理由:
- 固定柱宽,跨终端一致
- 完整 8 级字符集在 20 字符下 = 160 个独立可视单元;补 `▉` 后 75%-99% 段不再丢级(此前 6 个半块 + `█` 实际只到 7 级,差一级精度)
- `·` 占位解决"今天没用"与"数据丢失"在视觉上无法区分的问题——空 db 时全 24/7 行仍能扫出"全零"事实
- `────────────── (total)` 把最该看的汇总行从柱状图列剥离,避免"满分桶"暗示
- `▏▎▍▌▋▊▉█` + `─` + `·` 均为中性宽度字符,主流 UTF-8 终端正常显示

拒绝"满桶 ROW 不画柱 / 太短行用空格":柱状图的核心是"比例可见",0 vs 100 vs 10000 必须区分。
拒绝"TOTAL 行画满格柱":仅一个 TOTAL 行无 max 参考;且满格会让用户误以为"总分 = max",与 N 桶求和的实际语义错位。

### D9. `--day` 缺失时维持旧行为(向后兼容)

**选择**:不传 `--day` 时,`runStats` 走"7 天 detail 明细 + 无桶聚合"路径;`stats.Query.Buckets` 为 nil;`QueryStats` 不调 `QueryBucketSummary`。

理由:
- 旧 `ccl stats` 默认就是"7 天 sessions 明细",无破坏性
- 旧 `ccl stats --json` 消费者零感知(JSON 字节级一致)
- 旧脚本 / pipeline 不需迁移

拒绝"默认 `--day 7`"的原因:用户调研明确说"想看时间桶时才传",不该默认开。

### D10. `--since` 砍掉,detail since 由 `--day` 派生

**选择**:`runStats` 不再支持 `--since`;detail since = `now - N days`(N = `--day` 值)。不传 `--day` 时 detail since 走默认 7 天(`DefaultSince()`)。

理由:
- `--since` 是 detail-only 参数,跟 `--day`(桶维度)概念正交,用户困惑
- `--day N` 已经隐含"看最近 N 天",detail since 跟 summary 桶同窗口,逻辑自洽
- 砍一个 flag 让 CLI 更聚焦

拒绝"保留 `--since` 让 detail 用 since,summary 用 day"的原因:detail 跟 summary 时间尺度不同步,UX 割裂(`--since 2026-01-01 --day 7` → detail 一年,summary 7 天,无法自圆)。

## Risks / Trade-offs

- **柱状图 + humanize 的认知切换** → humanize 后数字跟原值有差异(12345 vs 12.3K),但柱状图本身是"比例可视化",不是"精确读数"。文本用户读 6 列 humanize 数字精确,柱状图看趋势。
- **`--day 1` partial bucket 误导** → 桶 label 加 `*` + 表头注,但用户可能不看注。Mitigation:spec scenario 显式断言 partial 行的存在 + 标识。
- **砍 `--since` 破坏旧脚本** → OpenSpec 允许 CLI 行为演进;用户旧脚本传 `--since` 直接报错,易修复(改 `--day`)。
- **JSON `bucket_rows` 字段新增是 schema 扩展** → `omitempty`,不传 `--day` 时字节级一致;OpenSpec 约定 JSON 字段新增向后兼容。
- **大 N(N=90)的 90 行输出** → 屏幕高度不够时用户 pipe `| less` / `| head -n 20`;本 change 不强制 scroll 处理。
- **`bucket_unix_ms` 跨时区歧义** → 字段值是 UTC 毫秒(消费者需自转时区);`bucket_iso` 字段是本地时区 ISO 字符串(消费者直接用)。两个都给,各自适用。
- **跨包漂移再次出现** → `TodayStartMillis` 提到 `internal/stats` 包为唯一导出,任何调用必须走它;本 change 后无副本。
- **detail since 强制跟随 `--day`** → 用户失去"看特定时段"的能力(原来可 `--since 2026-01-01`)。Mitigation:不传 `--day` 时 detail 走默认 7 天,日常使用无感;需要"看特定时段"的高级用法留后续 change。

## Migration Plan

- 砍 `--since`:旧脚本传 `--since` 直接报"flag provided but not defined",用户改 `--day` 即可
- 砍 `--summary`:旧提案不发布,无外部用户依赖
- 新增 `--day`:纯新增 flag,无破坏
- 现有 `ccl stats` / `ccl stats --json` 输出格式不变(除非传 `--day`)
- README:更新"用量监控"小节,把 `--summary` 字样换成 `--day`

回滚:删除 4 个新文件 + 回退 statscmd.go + query.go + format.go 即可,无 schema / 数据 / 状态变更。

## Open Questions

- 是否后续加 `--bucket hour|day` 让用户覆盖规则?→ 不在本 change;若用户调研发现需要,后续 change。
- 是否后续加 `--width N` 自定义柱宽?→ 不在本 change;固定 20 字符够用。
- 是否后续加 `--compare` 上一周期对比?→ 不在本 change;spec 已留扩展点(`bucket_rows` 是数组)。