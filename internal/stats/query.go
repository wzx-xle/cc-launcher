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
type Query struct {
	Buckets    *int    // nil = 不启用桶聚合;非 nil = 启用,N ∈ [1,90]
	ByProvider bool    // 按 provider 聚合
	ByProject  bool    // 按 project 聚合
	Project    string  // 模糊匹配 session.cwd 子串
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

// QueryStats 打开 db，读聚合，返回 Result。
//
// 调用方负责输出（表格 / JSON）。
//
// detail since 推导（stats-time-buckets §D10）：
//   - q.Buckets != nil:detail since = now - *q.Buckets days（与桶聚合同窗口）
//   - q.Buckets == nil:detail since = DefaultSince()（7 天前,旧 ccl stats 字节级一致）
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

	// detail since:有 Buckets 时跟桶对齐;否则默认 7 天
	var since time.Time
	if q.Buckets != nil {
		since = time.Now().AddDate(0, 0, -*q.Buckets)
	} else {
		since = DefaultSince()
	}

	groupBy := "session"
	switch {
	case q.ByProject && q.ByProvider:
		groupBy = "project+provider"
	case q.ByProject:
		groupBy = "project"
	case q.ByProvider:
		groupBy = "provider"
	}

	sqlStr, args := buildSessionStatsSQL(groupBy, q.Project, since)

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
		rows2, err := QueryBucketSummary(dbPath, kind, sinceMs)
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
// 注意：Claude Code 不在 OTel payload 里推 provider / cwd；sessions 表这两列恒为空。
// 因此 --by-provider 实际退化为按 sessions 表的 provider 列（恒空）；功能仅对未来扩展有效。
// 真实场景下用 --by-project 等替代。
func buildSessionStatsSQL(groupBy, projectFilter string, since time.Time) (string, []any) {
	var groupExpr, groupClause string
	args := []any{since.UnixMilli()}

	switch groupBy {
	case "project":
		groupExpr = "s.cwd"
		groupClause = "GROUP BY s.cwd"
	case "provider":
		groupExpr = "COALESCE(s.provider, '')"
		groupClause = "GROUP BY s.provider"
	case "project+provider":
		groupExpr = "s.cwd, COALESCE(s.provider, '')"
		groupClause = "GROUP BY s.cwd, s.provider"
	default: // "session"
		groupExpr = "s.id"
		groupClause = "GROUP BY s.id"
	}

	where := "WHERE s.started_at >= ?"
	if projectFilter != "" {
		where += " AND s.cwd LIKE ?"
		args = append(args, "%"+strings.ReplaceAll(projectFilter, "%", `\%`)+"%")
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
func providerName(sessProvider, labelsJSON string) string {
	if sessProvider != "" {
		return sessProvider
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
		return ""
	}
	return labels["provider"]
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