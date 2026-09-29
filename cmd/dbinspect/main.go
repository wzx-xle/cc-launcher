package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func main() {
	// dbinspect: SQLite 排查工具
	//
	// 用法:
	//   dbinspect                       → 只读报告（sessions + metrics 概况）
	//   dbinspect cleanup --id <sid>    → 删除指定 session
	//                                       先 dry-run 报告影响行数
	//                                       需要 --yes 才真删
	args := os.Args[1:]
	if len(args) >= 1 && args[0] == "cleanup" {
		fs := newFlagSet("cleanup")
		id := fs.String("id", "", "session id to delete")
		yes := fs.Bool("yes", false, "actually DELETE (otherwise dry-run)")
		if err := fs.Parse(args[1:]); err != nil {
			os.Exit(2)
		}
		if *id == "" {
			fmt.Fprintln(os.Stderr, "missing --id")
			os.Exit(2)
		}
		cleanupSession(*id, *yes)
		return
	}
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: dbinspect [cleanup --id <sid> --yes]")
		os.Exit(2)
	}
	report()
}

func dbPathRO() string {
	return os.ExpandEnv("$HOME/.cc-launcher/telemetry.db") + "?mode=ro"
}

func dbPathRW() string {
	return os.ExpandEnv("$HOME/.cc-launcher/telemetry.db")
}

func report() {
	db, err := sql.Open("sqlite", dbPathRO())
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer db.Close()

	fmt.Println("=== sessions ===")
	rows, _ := db.Query("SELECT id, started_at, datetime(started_at/1000,'unixepoch','+8 hours') ts8, provider, cwd, model FROM sessions ORDER BY started_at")
	for rows.Next() {
		var id, prov, cwd, model, ts string
		var ms int64
		_ = rows.Scan(&id, &ms, &ts, &prov, &cwd, &model)
		fmt.Printf("  %s  started=%s(%d)  provider=%q cwd=%q model=%q\n", id, ts, ms, prov, cwd, model)
	}
	rows.Close()

	fmt.Println("\n=== per-session metric summary ===")
	rows, _ = db.Query(`
		SELECT s.id, COUNT(m.id) total,
		       SUM(CASE WHEN JSON_EXTRACT(m.labels,'$.model') IS NOT NULL THEN 1 ELSE 0 END) with_model,
		       GROUP_CONCAT(DISTINCT m.name) names
		FROM sessions s LEFT JOIN metrics m ON m.session_id=s.id
		GROUP BY s.id ORDER BY s.started_at`)
	for rows.Next() {
		var id, names string
		var total, withm int
		_ = rows.Scan(&id, &total, &withm, &names)
		fmt.Printf("  %s  metrics=%d  with_model_label=%d  names=%s\n", id, total, withm, names)
	}
	rows.Close()
}

// cleanupSession 删除指定 session 及其 metrics。
// 先 dry-run 报告影响行数；要求 --yes 确认才真删。
func cleanupSession(id string, yes bool) {
	// dry-run: 用 read-only 连接统计
	db, err := sql.Open("sqlite", dbPathRO())
	if err != nil {
		fmt.Println("open ro:", err)
		return
	}
	var sessExists int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, id).Scan(&sessExists)
	var metricCnt int
	_ = db.QueryRow(`SELECT COUNT(*) FROM metrics WHERE session_id=?`, id).Scan(&metricCnt)
	db.Close()

	fmt.Printf("dry-run: sessions=%d metrics=%d for id=%q\n", sessExists, metricCnt, id)
	if sessExists == 0 && metricCnt == 0 {
		fmt.Println("nothing to delete")
		return
	}
	if !yes {
		fmt.Println("re-run with --yes to actually DELETE")
		return
	}

	// 真删：用 RW 连接（WAL 下与 daemon 单写并发安全）
	dbw, err := sql.Open("sqlite", dbPathRW())
	if err != nil {
		fmt.Println("open rw:", err)
		return
	}
	defer dbw.Close()

	tx, err := dbw.Begin()
	if err != nil {
		fmt.Println("begin tx:", err)
		return
	}
	r1, err1 := tx.Exec(`DELETE FROM metrics WHERE session_id=?`, id)
	if err1 != nil {
		_ = tx.Rollback()
		fmt.Println("delete metrics:", err1)
		return
	}
	r2, err2 := tx.Exec(`DELETE FROM sessions WHERE id=?`, id)
	if err2 != nil {
		_ = tx.Rollback()
		fmt.Println("delete session:", err2)
		return
	}
	if err := tx.Commit(); err != nil {
		fmt.Println("commit:", err)
		return
	}
	n1, _ := r1.RowsAffected()
	n2, _ := r2.RowsAffected()
	fmt.Printf("deleted: metrics=%d sessions=%d\n", n1, n2)
}
