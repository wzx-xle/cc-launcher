## Context

`ccl stats --day N` 是桶聚合表的入口（`statscmd.go:142` → `stats.FormatBucketTable`）。表 schema 由 `spec/time-buckets` Requirement 1 锁定为 8 列 `BUCKET | SESSIONS | IN | OUT | CACHE_CRE | CACHE_READ | COST_USD | TOTAL_TOKENS(柱)`，末列 `TOTAL_TOKENS` 用 20 字符 Unicode 柱 + 8 半块字符集 + `(max=0)` 时 `·` + 19 空格占位 + partial 桶末追加 `⏳` + TOTAL 行换成 20 个 `─` + ` (total)`。

现实里这个设计偏"视觉密度条"哲学：
- `spec/scenario 4` 强制 80-col hard cap，导致柱列挤掉 CACHE 列空间（实测正常终端没问题，但任何 prompt 窄 20 列就崩）
- 用户在多桶间比较 token 数时，要用肉眼数多少"半块+整块"，估读误差 ~5-10%
- `⏳` / `·` / `─` 占位符表达"partial / 零值 / separator"语义，分散阅读注意力

本次把末列改为"每桶 token 合计数字"（humanize: `12.3K` / `1.23M`），与 IN/OUT/CACHE 列同一形态对齐。

## Goals / Non-Goals

**Goals:**
- 桶表 `TOTAL_TOKENS` 列改为 `humanizeTokens(Input+Output+CacheCreate+CacheRead)` 数字
- TOTAL 行 `TOTAL_TOKENS` 列改为 humanize 总和（与数据行同形态，无视觉分隔符）
- 移除 20 字符柱渲染与半块字符集逻辑（`bucketBar()` 函数删掉）
- 移除 80-col hard cap 与柱 alignment 相关测试 / spec scenario
- 保留 `*` 标记当前小时桶的语义（spec requirement 1 的 partial marker 不变）

**Non-Goals:**
- 不改 detail 表 `FormatTable`（已无柱）
- 不改 `bucket_rows` / `bucket_totals` JSON shape（已纯数值）
- 不动 SQL 层 `QueryBucketSummary`
- 不引入"按 IN/OUT/CACHE 列宽度自适应"等排版优化（boundary 与本 change 解耦）
- 不动 spec requirement 2/3/5 与"--since 删除"等历史契约

## Decisions

### Decision 1：末列与 IN/OUT/CACHE 列同形态——`humanizeTokens(total)`

`FormatBucketTable` 每行 token 合计 `t = r.InputTokens + r.OutputTokens + r.CacheCreate + r.CacheRead` 在原代码已算好（`format.go:287`），仅渲染成柱而非数字。新代码：

```go
fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%.4f\t%s\n",
    r.Label,
    r.Sessions,
    humanizeTokens(r.InputTokens),
    humanizeTokens(r.OutputTokens),
    humanizeTokens(r.CacheCreate),
    humanizeTokens(r.CacheRead),
    r.CostUSD,
    humanizeTokens(t),  // ← 替换 bucketBar(t, maxTokens, r.IsCurrent)
)
```

`humanizeTokens`（`format.go:151`）已处理 `0 / 1..999 / 1K..999_499 / 999_500..999_999 → M / 1M.. / 999_500_000..999_999_999 → B / ≥1B` 五档；无需新规则。

**理由**：与 IN/OUT/CACHE 列沿用同一函数，零新逻辑；zero-token 桶已经走 `humanizeTokens(0) = "0"`，无需占位符占位（取代原来的 `·`）。

### Decision 2：TOTAL 行 `TOTAL_TOKENS` 列展示 humanize 总和

原 TOTAL 行用 `──────────────────── (total)` 作视觉分隔符，与 `humanizeTokens(totals.TotalTokens)` 取代：

```go
if totals != nil {
    fmt.Fprintf(tw, "TOTAL\t%d\t%s\t%s\t%s\t%s\t%.4f\t%s\n",
        totals.Sessions,
        humanizeTokens(totals.InputTokens),
        humanizeTokens(totals.OutputTokens),
        humanizeTokens(totals.CacheCreate),
        humanizeTokens(totals.CacheRead),
        totals.CostUSD,
        humanizeTokens(totals.InputTokens + totals.OutputTokens + totals.CacheCreate + totals.CacheRead),
    )
}
```

**理由**：与数据行同形态，眼睛沿"TOTAL_TOKENS 列"扫到底，看到最大值的相对量级（不是绝对数）；`TOTAL` label + SESSIONS count 已经足够把它与数据行区分，不需要额外的横线。

**Risks**：原来用 `─` 横线在视觉上把 TOTAL 与数据行隔开；新方案靠"`TOTAL` 字符串 + SESSIONS 数为所有桶之和"来区分——大多数 CLI 工具（`df -h`、`free -h` 的 totals 行）也是这么做。

### Decision 3：删 `bucketBar()` 函数

`bucketBar`（`format.go:194-247`）原来唯一的调用点是 `FormatBucketTable` 末列；删后 grep 全仓零引用。

**理由**：不留 dead code；该函数 50 行（半块字符集 + byte/rune 防护 + partial 替换）若保留为 unused 后人不知道用途。

**Risks / Migration**：零——`bucket_test.go:360-468` 的 5 个 `bucketBar` 单元测试随函数删；它们只是测试实现细节，删除等于清掉对 spec 无关的关注点。

### Decision 4：保留 `(* = 当前小时,数据仍在累计)` hourly 注与 `*` 后缀

`spec/time-buckets` Requirement 1 的 partial 桶标记（`BUCKET = "17:00*"` + hourly 头部注）不动——这是"当前桶仍在累计"的语义信号，跟 `⏳` 是不同的两层抽象：
- `*` / 注：标识"这是当前小时，count 不全"——spec contract，保留
- `⏳`：仅作为柱末标记，跟柱一起删（spec 跟着移除这一子句）

**理由**：用户想砍柱 = 想砍视觉编码、不等于砍 partial 标识。`*` 后缀已经在 BUCKET 列稳了位置（`format.go` `Label` 字段构造时拼进去），与柱无关。

### Decision 5：spec 改动用 `## MODIFIED Requirements` 整段重写

`openspec/specs/time-buckets/spec.md` Requirement 1 整段文字要改（柱字符集、partial ⏳、80-col cap、`─` separator）。OpenSpec 文档明确要求"MODIFIED Requirements MUST include full updated content"（见 `archive/2026-09-30-flatten-stop-restore-into-serve/design.md:110`）。`specs/time-buckets/spec.md` 写完整的修订后 Requirement 文本（含改后 Scenario）；archive 时 OpenSpec CLI 把 delta 合入 base spec。

**替代方案**：把"展示形态"从 spec 里删掉（spec 只约束 JSON shape + 数据正确性，不约束文本渲染）——但已存在的 `spec/scenario "TOTAL_TOKENS bar length is proportional"` 等是 spec 历史承诺；删它们算失约。MODIFIED 整段改是**保留 spec/sc 契约完整性**的做法。

## Risks / Trade-offs

- [文本消费者（grep TOTAL_TOKENS 列文本）被打破] → text 本就是 human-readable、不可当 stable API；JSON 消费者零影响。若有用户脚本 grep `█` / `⏳` 等柱字符需要更新——但 column text shape 变了它们也要改，反正是单点迁移
- [柱 alignment 测试不再有效] → 删；相关 smoke 子测试整段移除
- [80-col hard cap 失去依据] → 新方案的列宽上限由各列字符串自然决定（最长是 `humanizeTokens` 最多 5 字符 "12.3K"，BUCKET 7 字符 "MM-DD" / 5 字符 "HH:00"，CACHE 5 字符 "12.3K"，COST 7 字符 "0.0000"；全表 ≤ 70 列）——比旧版更紧凑，原 hard cap 不再需要
- [TOTAL 行失去 `─` 视觉分隔] → 靠 `TOTAL` 字符串 + SESSIONS 数为全桶和区分；参考 `df -h` 同模式

## Migration Plan

无需 state.json 迁移 / 配置迁移 / CLI 命令迁移：
- 无 schema 变化
- 灰度：渲染层单点替换，所有现有 db 不受影响；`go test ./...` 全绿即上线

用户已确认设计（A：数字 / 列名：保留 `TOTAL_TOKENS`），无需再问开放问题。

## Open Questions

无。
