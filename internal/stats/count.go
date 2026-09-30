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