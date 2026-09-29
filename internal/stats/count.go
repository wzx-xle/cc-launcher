package stats

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// CountSessions 返回 db 里 sessions 表的总行数。
//
// 决策（flatten-stop-restore-into-serve §9.1 + design.md §D8）：
//   - read-only 打开 db（?mode=ro），与 QueryStats 同源
//   - 单条 SQL：SELECT COUNT(*) FROM sessions
//   - 调用方负责 defer close
//   - db 文件不存在 → 返回 0 + nil（"无 telemetry"是合法状态，非 error）
//
// 注意：本函数不触发 SQLite worker 副作用，与 collector.OpenStore 隔离。
// `ccl serve --status` 在 daemon 没跑时也能调（这正是设计目标）。
func CountSessions(dbPath string) (int64, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat db 失败: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return 0, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	var n int64
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n); err != nil {
		return 0, fmt.Errorf("count sessions: %w", err)
	}
	return n, nil
}