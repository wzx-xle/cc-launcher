package stats

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// CountSessionsByWindow 返回 db 里 sessions 表的 (total, sinceCount) 两计数。
//
// sinceMs 是 UnixMilli 时间戳起点；started_at >= sinceMs 的 session 计入 sinceCount。
//
// 用途：ccl serve --status 同时显示"总 sessions 数"和"今日新增 sessions 数"。
//
// 决策（flatten-stop-restore-into-serve §9.1 + design.md §D8）：
//   - read-only 打开 db（?mode=ro），与 QueryStats 同源
//   - 两条 SQL：COUNT(*) + COUNT(*) WHERE started_at >= ?
//   - 调用方负责 defer close
//   - db 文件不存在 → 返回 (0, 0) + nil（"无 telemetry"是合法状态，非 error）
//
// 注意：本函数不触发 SQLite worker 副作用，与 collector.OpenStore 隔离。
// `ccl serve --status` 在 daemon 没跑时也能调（这正是设计目标）。
func CountSessionsByWindow(dbPath string, sinceMs int64) (total, sinceCount int64, err error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("stat db 失败: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return 0, 0, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err = db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("count sessions total: %w", err)
	}
	if err = db.QueryRow(
		"SELECT COUNT(*) FROM sessions WHERE started_at >= ?", sinceMs,
	).Scan(&sinceCount); err != nil {
		return 0, 0, fmt.Errorf("count sessions since: %w", err)
	}
	return total, sinceCount, nil
}

// CountSessionsInWindow 返回"窗口内有活动的 session 数"。
//
// 与 CountSessionsByWindow 的区别：后者按 **started_at** 计数（"这个时段
// 开始了多少个 session"），本函数按 **消费时刻**（metrics.captured_at）计数
// ——与 QueryStats 的 detail 行口径一致（"窗口内有活动的 session 才出现"）。
//
// 过滤条件（project / model / provider）与 QueryStats 相同，保证数字对得上。
func CountSessionsInWindow(dbPath string, q Query) (int64, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return 0, ErrNoDatabase
		}
		return 0, fmt.Errorf("stat db 失败: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return 0, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	since := q.now()
	if q.Buckets != nil {
		since = oldestBucketStart(since, *q.Buckets)
	} else {
		since = since.AddDate(0, 0, -7)
	}

	where := "WHERE m.captured_at >= ?"
	args := []any{since.UnixMilli()}
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

	var n int64
	err = db.QueryRow(
		`SELECT COUNT(DISTINCT s.id) FROM sessions s
		 JOIN metrics m ON m.session_id = s.id `+where, args...,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count sessions in window: %w", err)
	}
	return n, nil
}