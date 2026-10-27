// Package stats 提供 ccl stats 的查询逻辑：直接读 SQLite 文件，不走 daemon HTTP。
//
// 决策（见 design.md §7）：
//   - 单写者（daemon）+ 多读者（ccl stats）：WAL 保证并发安全
//   - 聚合在 SQL 层完成：按 metric name SUM(value) GROUP BY session / provider / project
//   - 不解析 protobuf：metrics 表已经是宽表
package stats

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNoDatabase 表示 db 文件不存在（user 没跑过 ccl 或没启用 OTel）。
var ErrNoDatabase = errors.New("telemetry db 不存在")

// Query 是 ccl stats 的查询参数。
//
// 决策（stats-time-buckets §D4 + §D10）：
//   - 砍掉 Since time.Time（由 `--day` 派生 detail 起点,避免 detail/summary 窗口割裂）
//   - Buckets *int：nil = 不启用桶聚合（向后兼容,旧 ccl stats 字节级一致 JSON）;
//     非 nil → 启用,*Buckets 即 N;kind 推导规则:N==1 → "hour",else → "day"
//   - Clock func() time.Time：nil = fallback time.Now（向后兼容,旧 caller 字节级一致）;
//     非 nil → 内部所有 time.Now() 走该函数,实现 deterministic 测试
//     （与 collector.Store.WithClock 模式对齐,见 spec/stats-query "stats query
//     accepts an optional clock"）。Query 不进 JSON 输出（Result 只用 Sessions /
//     Totals / BucketRows / BucketTotals 等具体字段,无 Query 反序列化需求）,
//     所以不加 json tag。
type Query struct {
	Buckets    *int    // nil = 不启用桶聚合;非 nil = 启用,N ∈ [1,90]
	ByProvider bool    // 按 provider 聚合
	ByProject  bool    // 按 project 聚合
	ByModel    bool    // 按 model 聚合（web-usage-stats-ui §D8）
	Project    string  // 模糊匹配 session.cwd 子串
	Model      string  // 模糊匹配 session.model 子串（SQL 层过滤）
	Provider   string  // 模糊匹配 session.provider 子串（SQL 层过滤）
	Clock      func() time.Time // nil = time.Now;非 nil = 注入 clock(测试 / 跨包 deterministic)
}

// now 返回 Query 注入的 clock 或 fallback time.Now。
//
// unexported helper:QueryStats 内部统一调用入口,避免每个 time.Now 点都重复
// "q.Clock != nil ? q.Clock() : time.Now" 三元式。
func (q Query) now() time.Time {
	if q.Clock != nil {
		return q.Clock()
	}
	return time.Now()
}

// SessionStats 是单个 session（或聚合行）的指标。
//
// StartedAt 改为指针类型：totals 行没有真实时间（time.Time 零值会让 JSON 输出
// "0001-01-01T00:00:00Z"，误导成数据错误），nil 时 omitempty 跳过。
type SessionStats struct {
	SessionID    string            `json:"session_id"`
	Project      string            `json:"project"`
	Provider     string            `json:"provider,omitempty"`
	Model        string            `json:"model,omitempty"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	InputTokens  int64             `json:"input_tokens"`
	OutputTokens int64             `json:"output_tokens"`
	CacheCreate  int64             `json:"cache_creation_tokens"`
	CacheRead    int64             `json:"cache_read_tokens"`
	CostUSD      float64           `json:"cost_usd"`
	ExtraLabels  map[string]string `json:"extra_labels,omitempty"`
}

// Result 是 ccl stats 的整体输出。
//
// 决策（stats-time-buckets §D4）：
//   - BucketRows / BucketTotals 用 omitempty;Query.Buckets == nil 时这两个字段
//     JSON 输出完全缺失,保证旧消费者字节级兼容
//   - BucketTotals 是 *BucketRow(nil=无桶聚合);非 nil 时 Label/BucketISO/BucketUnixMs/
//     IsCurrent 留零值(总和行无单一 bucket 标识)
type Result struct {
	Since        time.Time      `json:"since"`
	GroupBy      string         `json:"group_by,omitempty"` // "provider" / "project" / "session"
	Sessions     []SessionStats `json:"sessions"`
	Totals       SessionStats   `json:"totals"`
	BucketRows   []BucketRow    `json:"bucket_rows,omitempty"`
	BucketTotals *BucketRow     `json:"bucket_totals,omitempty"`
}

// DefaultSince 默认窗口为 7 天前。
func DefaultSince() time.Time { return time.Now().AddDate(0, 0, -7) }

// oldestBucketStart 返回 --day N 桶视图的最旧桶那天的本地 00:00。
//
// design Decision 6：detail 视图的窗口起点必须与桶视图的窗口起点重合，
// 否则 detail totals ≠ Σbucket rows（原实现 detail 覆盖 [now-Nd, now]、
// 桶覆盖 [todayStart-(N-1)d, todayStart]，两者错开约一天）。
//
// N=1 → 今天 00:00（hourly 桶覆盖当天 0..currentHour）。
func oldestBucketStart(now time.Time, n int) time.Time {
	if n < 1 {
		n = 1
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return midnight.AddDate(0, 0, -(n - 1))
}

// QueryStats 打开 db，读聚合，返回 Result。
//
// 调用方负责输出（表格 / JSON）。
//
// detail since 推导（stats-time-buckets §D10 + design Decision 6）：
//   - q.Buckets != nil:detail since = 最旧桶那天的本地 00:00（与桶视图完全同窗口）
//   - q.Buckets == nil:detail since = now - 7 days（不午夜对齐）
//
// 桶聚合（q.Buckets != nil 时）：
//   - kind 推导:N==1 → "hour",else → "day"
//   - 调 QueryBucketSummary;结果填到 result.BucketRows + result.BucketTotals
//   - ErrNoDatabase → 整 func return error;其它 SQL 错误 → stderr warning,
//     BucketRows/BucketTotals 留空(明细仍正常返回,不因桶失败而拖累 detail)
func QueryStats(dbPath string, q Query) (*Result, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s；请先跑 ccl 启动一个 session", ErrNoDatabase, dbPath)
		}
		return nil, fmt.Errorf("读 db 失败: %w", err)
	}

	// mode=ro 防止误写
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// detail since:有 Buckets 时对齐到**最旧桶那天的本地 00:00**(design Decision 6);
	// 否则默认 7 天前。
	// 时间源走 q.now()(Query.Clock 注入或 fallback time.Now),让 smoke test
	// 能与 collector.Store.WithClock 共享同一 mock today(见 spec/stats-query
	// "stats query accepts an optional clock")。
	var since time.Time
	if q.Buckets != nil {
		since = oldestBucketStart(q.now(), *q.Buckets)
	} else {
		since = q.now().AddDate(0, 0, -7)
	}

	groupBy := "session"
	switch {
	case q.ByProject && q.ByProvider:
		groupBy = "project+provider"
	case q.ByProject:
		groupBy = "project"
	case q.ByProvider:
		groupBy = "provider"
	case q.ByModel:
		groupBy = "model"
	}

	sqlStr, args := buildSessionStatsSQL(groupBy, q, since)

	rows, err := db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("查询失败: %w", err)
	}
	defer rows.Close()

	result := &Result{Since: since, GroupBy: groupBy}
	for rows.Next() {
		var ss SessionStats
		var cwd, provRaw, model, labelsJSON string
		var startedAtMs int64
		if err := rows.Scan(
			&ss.SessionID, &startedAtMs, &provRaw, &cwd, &model,
			&ss.InputTokens, &ss.OutputTokens, &ss.CacheCreate, &ss.CacheRead, &ss.CostUSD,
			&labelsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		startedAt := time.UnixMilli(startedAtMs)
		ss.StartedAt = &startedAt
		ss.Project = projectName(cwd)
		ss.Provider = providerName(provRaw, labelsJSON)
		ss.Model = model
		if labelsJSON != "" && labelsJSON != "{}" {
			_ = json.Unmarshal([]byte(labelsJSON), &ss.ExtraLabels)
		}
		result.Sessions = append(result.Sessions, ss)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	// sort: StartedAt 倒序。totals 行 nil 排最后（场景里 totals 不在 sessions 数组里，
	// 但保险起见 nil-aware）。
	sort.Slice(result.Sessions, func(i, j int) bool {
		a, b := result.Sessions[i].StartedAt, result.Sessions[j].StartedAt
		if a == nil && b == nil {
			return false
		}
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return a.After(*b)
	})
	result.Totals = sumSessions(result.Sessions)

	// 桶聚合：q.Buckets != nil 时启用；ErrNoDatabase 整体 return error,其它错误仅 warning
	if q.Buckets != nil {
		kind := "day"
		if *q.Buckets == 1 {
			kind = "hour"
		}
		sinceMs := since.UnixMilli()
		rows2, err := QueryBucketSummary(dbPath, kind, sinceMs, q.Clock)
		if err != nil {
			if errors.Is(err, ErrNoDatabase) {
				// 与 detail 一致：db 缺失整 func return error(caller 显示 stderr + exit 1)
				return nil, err
			}
			fmt.Fprintf(os.Stderr, "warning: bucket summary unavailable: %v\n", err)
		} else {
			result.BucketRows = rows2
			if len(rows2) > 0 {
				totals := sumBuckets(rows2)
				result.BucketTotals = &totals
			}
		}
	}

	return result, nil
}

// buildSessionStatsSQL 按 group 维度动态拼 SQL，返回 (sql, args)。
//
// 关键 metric name（Claude Code v2.1.x 实测）：
//   claude_code.token.usage       labels.type ∈ {input, output, cacheRead, cacheCreation}
//   claude_code.cost.usage
// 其它 name 在聚合里被忽略（不算 tokens/cost）。
//
// SQLite 用 CAST(... AS INTEGER) 把 token SUM 转 int64；cost 保留 REAL。
//
// 过滤全部在 **SQL 层**（web-usage-stats-ui §D8）：project / model / provider
// 三个子串过滤都进 WHERE，保证 totals == Σ rows（事后过滤聚合行做不到这点）。
//
// provider / cwd 列来自 ccl 启动时写进 OTEL_RESOURCE_ATTRIBUTES 的 launch 上下文
// （internal/launchctx）；没走 ccl 启动的会话这两列为空。
func buildSessionStatsSQL(groupBy string, q Query, since time.Time) (string, []any) {
	var groupExpr, groupClause string
	args := []any{since.UnixMilli()}

	switch groupBy {
	case "project":
		groupExpr = "s.cwd"
		groupClause = "GROUP BY s.cwd"
	case "provider":
		groupExpr = "COALESCE(s.provider, '')"
		groupClause = "GROUP BY s.provider"
	case "model":
		groupExpr = "COALESCE(s.model, '')"
		groupClause = "GROUP BY s.model"
	case "project+provider":
		// 单列表达式（不能是逗号分隔的两列——那会让 SELECT 多出一列，
		// 与 Scan 的列数对不上）。
		groupExpr = "COALESCE(s.cwd, '') || ' × ' || COALESCE(s.provider, '')"
		groupClause = "GROUP BY s.cwd, s.provider"
	default: // "session"
		groupExpr = "s.id"
		groupClause = "GROUP BY s.id"
	}

	// 窗口过滤按**消费时刻**（design Decision 4）：session 行只累加 captured_at
	// 落在窗口内的 metric；窗口外没有活动的 session 不会出现
	// （LEFT JOIN 退化为 INNER JOIN 语义）。session 元信息（MIN(s.started_at) /
	// model / cwd）不变——STARTED 列仍显示真实 session 起始（可能早于窗口）。
	where := "WHERE m.captured_at >= ?"
	if q.Project != "" {
		where += " AND s.cwd LIKE ?"
		args = append(args, likeArg(q.Project))
	}
	if q.Model != "" {
		where += " AND s.model LIKE ?"
		args = append(args, likeArg(q.Model))
	}
	if q.Provider != "" {
		where += " AND s.provider LIKE ?"
		args = append(args, likeArg(q.Provider))
	}

	sql := fmt.Sprintf(`
SELECT
    %s AS session_id,
    MIN(s.started_at) AS started_at,
    COALESCE(s.provider, '') AS provider,
    s.cwd AS cwd,
    COALESCE(s.model, '') AS model,
    CAST(COALESCE(SUM(CASE WHEN m.name = 'claude_code.token.usage' AND JSON_EXTRACT(m.labels, '$.type') = 'input'          THEN m.value END), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(CASE WHEN m.name = 'claude_code.token.usage' AND JSON_EXTRACT(m.labels, '$.type') = 'output'         THEN m.value END), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(CASE WHEN m.name = 'claude_code.token.usage' AND JSON_EXTRACT(m.labels, '$.type') = 'cacheCreation' THEN m.value END), 0) AS INTEGER) AS cache_create,
    CAST(COALESCE(SUM(CASE WHEN m.name = 'claude_code.token.usage' AND JSON_EXTRACT(m.labels, '$.type') = 'cacheRead'     THEN m.value END), 0) AS INTEGER) AS cache_read,
    COALESCE(SUM(CASE WHEN m.name = 'claude_code.cost.usage' THEN m.value END), 0.0) AS cost_usd,
    COALESCE(
        (SELECT m2.labels FROM metrics m2
         WHERE m2.session_id = %s
           AND m2.name = 'claude_code.token.usage'
         ORDER BY m2.captured_at DESC LIMIT 1),
        '{}'
    ) AS labels
FROM sessions s
LEFT JOIN metrics m ON m.session_id = s.id
%s
%s
`, groupExpr, sessionIDColumnForLabels(groupBy), where, groupClause)

	return sql, args
}

// sessionIDColumnForLabels 返回子查询里 session_id 应等于的列。
// 仅当 group 是 session 时用 s.id，其它用空串（子查询无意义，返回 '{}'）。
func sessionIDColumnForLabels(groupBy string) string {
	if groupBy == "session" {
		return "s.id"
	}
	return "''"
}

// projectName 从 cwd 提取项目名（basename）。空 cwd → ""。
func projectName(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// providerName 优先 sessions.provider；为空则从最近一帧 metric labels 推断。
//
// labels 里的兜底 key 是 `ccl.provider`——ccl 启动时写进
// OTEL_RESOURCE_ATTRIBUTES 的 launch 上下文会作为 resource attributes 随
// payload 送回，而 resource attrs 会自动合进 metric 的 labels。
// 注意这是**历史数据的兜底**：新写入的 session 行里 provider 列已有真值，
// sessions.provider 优先。
func providerName(sessProvider, labelsJSON string) string {
	if sessProvider != "" {
		return sessProvider
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
		return ""
	}
	if v := labels["ccl.provider"]; v != "" {
		return v
	}
	return labels["provider"]
}

// likeArg 把子串过滤值转成 LIKE 的 pattern 参数。
//
// 转义 "%" —— 否则用户传 "%" 就变成通配符，与"子串匹配"的语义不符。
func likeArg(substr string) string {
	return "%" + strings.ReplaceAll(substr, "%", `\%`) + "%"
}

// sumSessions 把所有 SessionStats 按字段求和。
func sumSessions(ss []SessionStats) SessionStats {
	var total SessionStats
	total.SessionID = "TOTAL"
	for _, s := range ss {
		total.InputTokens += s.InputTokens
		total.OutputTokens += s.OutputTokens
		total.CacheCreate += s.CacheCreate
		total.CacheRead += s.CacheRead
		total.CostUSD += s.CostUSD
	}
	return total
}

// sumBuckets 把所有 BucketRow 按数值字段求和（Label/BucketISO/BucketUnixMs/IsCurrent 留零值）。
//
// 用于 result.BucketTotals(JSON 字段 bucket_totals);TOTAL 行的 label / time 字段无单一
// bucket 标识,序列化时这些字段空,只填数值聚合。
func sumBuckets(rows []BucketRow) BucketRow {
	var total BucketRow
	for _, b := range rows {
		total.Sessions += b.Sessions
		total.InputTokens += b.InputTokens
		total.OutputTokens += b.OutputTokens
		total.CacheCreate += b.CacheCreate
		total.CacheRead += b.CacheRead
		total.CostUSD += b.CostUSD
	}
	return total
}