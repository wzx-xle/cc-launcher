package stats

// Package stats 的时间桶聚合查询。
//
// 决策（stats-time-buckets design.md §D1-D6）：
//   - 两条独立 SQL：sessions 计数 / tokens+cost 求和（避免 LEFT JOIN + NULL 行失真）
//   - 双 key 策略：sessionsCount 用 s.started_at；tokensSum hourly 用 m.captured_at,
//     daily 用 s.started_at。两者字符串 key 形式都是 "HH:00" / "YYYY-MM-DD",
//     merge 阶段按 string key 匹配 —— 每行同时拿到"s.started_at 视角 sessions"
//     和"对应 hour/days 视角 tokens",hourly 长 session 跨小时时 token 仍按
//     真实采集小时归属（spec 第 D6 明确要求）
//   - "hour" kind = currentHour+1 行（0..currentHour，含当前 partial）
//   - "day" kind = N 行 daily（todayStart → sinceMs 推算）
//   - 缺失桶补 0（不返回稀疏切片）

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

// BucketRow 是时间桶聚合表的一行（demo 给 fmt 用）。
//
// 字段语义：
//   - Label：JSON `bucket,omitempty`（空时省略,符合 spec bucket_totals 不含 bucket 的约定）；
//     text 与 JSON 都用同一字段（"17:00" / "09-24"），partial 小时末尾加 `*`
//   - BucketISO：JSON `bucket_iso`；"YYYY-MM-DD" 或 "YYYY-MM-DDTHH:00:05"(本地)
//   - BucketUnixMs：JSON `bucket_unix_ms`；该桶起点（本地时区）的 UTC 毫秒
//   - IsCurrent：JSON `is_current,omitempty`；当前小时桶置 true，daily 全 false
//   - Sessions/InputTokens/.../CostUSD：桶聚合值；空 db / 缺失桶 = 0
type BucketRow struct {
	Label         string  `json:"bucket,omitempty"`
	BucketISO     string  `json:"bucket_iso"`
	BucketUnixMs  int64   `json:"bucket_unix_ms"`
	IsCurrent     bool    `json:"is_current,omitempty"`
	Sessions      int64   `json:"sessions"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CacheCreate   int64   `json:"cache_creation_tokens"`
	CacheRead     int64   `json:"cache_read_tokens"`
	CostUSD       float64 `json:"cost_usd"`
}

// QueryBucketSummary 返回时间桶聚合结果（per hour / per local calendar day）。
//
// 参数：
//   - dbPath：SQLite 文件路径；不存在 → ErrNoDatabase 包裹
//   - kind ∈ {"hour", "day"}；其它值 → 报错
//   - sinceMs：sessions.started_at 下界（UnixMilli,本地时区零点）
//   - nowFn func() time.Time：nil = fallback time.Now；非 nil = 注入 clock
//     （测试 / 跨包 deterministic,与 collector.Store.WithClock / stats.Query.Clock
//      模式对齐,见 spec/stats-query "stats query accepts an optional clock"）
//
// 返回：
//   - hourly：currentHour+1 行（0..currentHour,partial 当前小时标记）
//   - daily：(todayStart - sinceMs)/86400000 + 1 行（最旧 → 今天,包含今天）
//   - 空 db 仍返回完整骨架（零值桶）
func QueryBucketSummary(dbPath string, kind string, sinceMs int64, nowFn func() time.Time) ([]BucketRow, error) {
	now := time.Now()
	if nowFn != nil {
		now = nowFn()
	}
	return queryBucketSummaryAt(dbPath, kind, sinceMs, now)
}

// queryBucketSummaryAt 同 QueryBucketSummary,但显式接受 now（测试用 mock 当前小时）。
//
// public-private 拆分:same package 的 _test.go 可访问本函数;非测试代码走 QueryBucketSummary。
func queryBucketSummaryAt(dbPath string, kind string, sinceMs int64, now time.Time) ([]BucketRow, error) {
	if kind != "hour" && kind != "day" {
		return nil, fmt.Errorf("桶 kind 非法: %q（仅支持 hour/day）", kind)
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNoDatabase, dbPath)
		}
		return nil, fmt.Errorf("stat db 失败: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// 两条独立 SQL(D1);map merge 阶段保证 bucket key 一致
	sessionsCount, err := queryBucketSessionsCount(db, kind, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("桶 sessions count 失败: %w", err)
	}
	tokensSum, err := queryBucketTokensSum(db, kind, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("桶 tokens sum 失败: %w", err)
	}

	// 骨架 + 填充(skeleton 已按时间升序)
	todayStartMs := startOfDayMillis(now)
	skeleton := bucketSkeleton(kind, now, todayStartMs, sinceMs)

	rows := make([]BucketRow, 0, len(skeleton))
	for _, sk := range skeleton {
		r := BucketRow{
			Label:        sk.label,
			BucketISO:    sk.iso,
			BucketUnixMs: sk.unixMs,
			IsCurrent:    sk.isCurrent,
		}
		if c, ok := sessionsCount[sk.key]; ok {
			r.Sessions = c
		}
		if t, ok := tokensSum[sk.key]; ok {
			r.InputTokens = t.in
			r.OutputTokens = t.out
			r.CacheCreate = t.cre
			r.CacheRead = t.read
			r.CostUSD = t.cost
		}
		rows = append(rows, r)
	}

	return rows, nil
}

// startOfDayMillis 返回指定时间所在天 00:00 的 UnixMilli（TodayStartMillis 的可注入等价）。
//
// 本函数不调 time.Now(),保证测试可注入 now。
func startOfDayMillis(now time.Time) int64 {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return start.UnixMilli()
}

// 内部 map key 类型（SQL bucket 表达式产物）
type bucketKey = string

type sessionsCountMap map[bucketKey]int64

type tokensSum struct {
	in, out, cre, read int64
	cost               float64
}
type tokensSumMap map[bucketKey]tokensSum

// queryBucketSessionsCount 只走 sessions 表,按桶 key COUNT(*)。
//
// 决策：D1 拒绝"LEFT JOIN + COUNT(DISTINCT)",NULL 行会让计数失真。
// sessions 一旦写入即持久,collector 重启丢 metric 行不影响 sessions 计数。
func queryBucketSessionsCount(db *sql.DB, kind string, sinceMs int64) (sessionsCountMap, error) {
	bucketExpr := bucketStrftimeExpr(kind, "s.started_at")
	sqlStr := fmt.Sprintf(`
SELECT %s AS bucket, COUNT(*) AS sessions
FROM sessions s
WHERE s.started_at >= ?
GROUP BY bucket
`, bucketExpr)

	rows, err := db.Query(sqlStr, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := sessionsCountMap{}
	for rows.Next() {
		var b string
		var n int64
		if err := rows.Scan(&b, &n); err != nil {
			return nil, err
		}
		out[b] = n
	}
	return out, rows.Err()
}

// queryBucketTokensSum 走 sessions LEFT JOIN metrics,按桶 key SUM 6 字段。
//
// 决策（stats-time-buckets §D6 + tasks §1.2 双 key 策略）：
//   - hourly kind：用 m.captured_at（让 token 归属到被采集的小时,符合 spec 文本）
//   - daily kind：用 s.started_at（daily 跨日概率低,与 sessions 计数对齐）
//
// 与 queryBucketSessionsCount 的 hour bucket key 都是 "HH:00" 字符串,
// 故 merge 阶段仍按 string key 匹配。两 SQL 的"小时"语义不同（前者:session
// 起始;后者:metric 抓取）,但字符串 key 形式相同 —— merge 后每行 sessions 数
// (s.started_at 视角)+ tokens(captured_at 视角)各自归位,符合"每桶聚合"直觉。
func queryBucketTokensSum(db *sql.DB, kind string, sinceMs int64) (tokensSumMap, error) {
	tsCol := "s.started_at"
	if kind == "hour" {
		tsCol = "m.captured_at"
	}
	bucketExpr := bucketStrftimeExpr(kind, tsCol)
	sqlStr := fmt.Sprintf(`
SELECT %s AS bucket,
    CAST(COALESCE(SUM(CASE WHEN m.name = 'claude_code.token.usage' AND JSON_EXTRACT(m.labels, '$.type') = 'input'          THEN m.value END), 0) AS INTEGER) AS in_tok,
    CAST(COALESCE(SUM(CASE WHEN m.name = 'claude_code.token.usage' AND JSON_EXTRACT(m.labels, '$.type') = 'output'         THEN m.value END), 0) AS INTEGER) AS out_tok,
    CAST(COALESCE(SUM(CASE WHEN m.name = 'claude_code.token.usage' AND JSON_EXTRACT(m.labels, '$.type') = 'cacheCreation' THEN m.value END), 0) AS INTEGER) AS cre_tok,
    CAST(COALESCE(SUM(CASE WHEN m.name = 'claude_code.token.usage' AND JSON_EXTRACT(m.labels, '$.type') = 'cacheRead'     THEN m.value END), 0) AS INTEGER) AS read_tok,
    COALESCE(SUM(CASE WHEN m.name = 'claude_code.cost.usage' THEN m.value END), 0.0) AS cost
FROM sessions s
LEFT JOIN metrics m ON m.session_id = s.id
WHERE s.started_at >= ?
GROUP BY bucket
`, bucketExpr)

	rows, err := db.Query(sqlStr, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := tokensSumMap{}
	for rows.Next() {
		var b string
		var t tokensSum
		if err := rows.Scan(&b, &t.in, &t.out, &t.cre, &t.read, &t.cost); err != nil {
			return nil, err
		}
		out[b] = t
	}
	return out, rows.Err()
}

// bucketStrftimeExpr 返回 SQLite strftime 表达式（SQL 注入安全:kind 与 tsCol 都被上游限定）。
//
//   - "hour" → "strftime('%H:00', tsCol/1000, 'unixepoch', 'localtime')"
//   - "day"  → "strftime('%Y-%m-%d', tsCol/1000, 'unixepoch', 'localtime')"
//
// 复用 'localtime' modifier 保证与 TodayStartMillis 本地时区语义一致。
func bucketStrftimeExpr(kind, tsCol string) string {
	switch kind {
	case "hour":
		return fmt.Sprintf("strftime('%%H:00', %s/1000, 'unixepoch', 'localtime')", tsCol)
	case "day":
		return fmt.Sprintf("strftime('%%Y-%%m-%%d', %s/1000, 'unixepoch', 'localtime')", tsCol)
	}
	return "''" // unreachable,QueryBucketSummary 已校验
}

type bucketSlot struct {
	key       bucketKey // SQL bucket key（"17:00" 或 "2026-09-24"）
	label     string    // text 用（"17:00*" / "09-24"）
	iso       string    // JSON bucket_iso
	unixMs    int64     // JSON bucket_unix_ms
	isCurrent bool
}

// bucketSkeleton 生成桶骨架（按时间升序）。
//
//   - kind="hour":rows = currentHour+1（0..currentHour,partial 当前小时末尾加 "*"）
//   - kind="day": rows = (todayStart - sinceMs)/86400000 + 1
//
// sinceMs 必须对齐到本地时区午夜(由 runStats 在调 QueryBucketSummary 前保证);
// 否则 day count 可能偏 1。本函数不做容错——caller 责任。
func bucketSkeleton(kind string, now time.Time, todayStartMs int64, sinceMs int64) []bucketSlot {
	switch kind {
	case "hour":
		currentHour := now.Hour()
		out := make([]bucketSlot, 0, currentHour+1)
		for h := 0; h <= currentHour; h++ {
			hourStart := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
			key := fmt.Sprintf("%02d:00", h)
			label := key
			if h == currentHour {
				label = key + "*"
			}
			out = append(out, bucketSlot{
				key:       key,
				label:     label,
				iso:       hourStart.Format("2006-01-02T15:04"), // "YYYY-MM-DDTHH:00" 严格匹配 spec 措辞
				unixMs:    hourStart.UnixMilli(),
				isCurrent: h == currentHour,
			})
		}
		return out
	case "day":
		todayStart := time.UnixMilli(todayStartMs)
		// sinceMs → todayStart 整 0 点的天数(含 today)
		days := max(int((todayStartMs-sinceMs)/int64(24*time.Hour/time.Millisecond))+1, 1)
		out := make([]bucketSlot, 0, days)
		for i := days - 1; i >= 0; i-- {
			dayStart := todayStart.AddDate(0, 0, -i)
			key := dayStart.Format("2006-01-02")
			out = append(out, bucketSlot{
				key:       key,
				label:     dayStart.Format("01-02"),
				iso:       dayStart.Format("2006-01-02"),
				unixMs:    dayStart.UnixMilli(),
				isCurrent: false,
			})
		}
		return out
	}
	return nil
}