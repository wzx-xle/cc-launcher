package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServeCompact_MutexWithBackfill 断言 `ccl serve --compact --backfill` 报互斥错误。
func TestServeCompact_MutexWithBackfill(t *testing.T) {
	err := runServe([]string{"--compact", "--backfill"})
	if err == nil {
		t.Fatal("runServe --compact --backfill 应报互斥错误")
	}
	if !strings.Contains(err.Error(), "互斥") {
		t.Errorf("错误信息应说明互斥, got %q", err.Error())
	}
}

// TestServeCompact_MutexWithStop 断言 `ccl serve --compact --stop` 也互斥。
func TestServeCompact_MutexWithStop(t *testing.T) {
	err := runServe([]string{"--compact", "--stop"})
	if err == nil {
		t.Fatal("runServe --compact --stop 应报互斥错误")
	}
	if !strings.Contains(err.Error(), "互斥") {
		t.Errorf("错误信息应说明互斥, got %q", err.Error())
	}
}

// TestServeCompact_HelpListsFlag 断言 `ccl serve --help` 列出 --compact。
func TestServeCompact_HelpListsFlag(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runServe([]string{"--help"}); err != nil {
			t.Errorf("runServe --help returned error: %v", err)
		}
	})
	if !strings.Contains(out, "--compact") {
		t.Errorf("serve --help 应列出 --compact, got:\n%s", out)
	}
}

// TestServeCompact_ShrinksFile 断言 --compact 让 db 文件**真的变小**。
//
// 为什么这条断言是关键：WAL 模式下 VACUUM 把新内容写进 -wal，主文件要等
// checkpoint 才被覆盖。少做那步 checkpoint，VACUUM 返回成功、文件却没变小
// → 功能等于空跑（实测 db 2.4MB → VACUUM 后主文件仍是 2437120 字节）。
//
// 造场景：写入一批数据 → 全删 → 文件不缩（空闲页）→ compact → 文件变小。
func TestServeCompact_ShrinksFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
		ended_at INTEGER, provider TEXT NOT NULL DEFAULT '', provider_endpoint TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '')`)
	db.Exec(`CREATE TABLE metrics (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
		name TEXT NOT NULL, value REAL NOT NULL, labels TEXT NOT NULL DEFAULT '{}', captured_at INTEGER NOT NULL)`)
	// 塞一批"占空间"的数据
	blob := strings.Repeat("x", 300)
	for i := 0; i < 150; i++ {
		db.Exec(`INSERT INTO metrics (session_id, name, value, labels, captured_at) VALUES (?,?,?,?,?)`,
			"s1", "claude_code.token.usage", 1.0, `{"type":"input","pad":"`+blob+`"}`, time.Now().UnixMilli())
	}
	db.Close()

	before := fileSize(dbPath)
	if before == 0 {
		t.Fatal("db 文件不存在")
	}
	// 全删 → 空闲页留下，文件不缩
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db2.Exec(`DELETE FROM metrics`)
	db2.Close()

	if err := vacuumDB(dbPath); err != nil {
		t.Fatalf("vacuumDB: %v", err)
	}
	after := fileSize(dbPath)
	if after >= before {
		t.Errorf("compact 后文件应变小: %d -> %d（VACUUM 后没 checkpoint？文件不会缩）", before, after)
	}
}

// TestServeCompact_PreservesRows 断言压缩不改变存储的数据。
func TestServeCompact_PreservesRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
		ended_at INTEGER, provider TEXT NOT NULL DEFAULT '', provider_endpoint TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '')`)
	db.Exec(`CREATE TABLE metrics (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
		name TEXT NOT NULL, value REAL NOT NULL, labels TEXT NOT NULL DEFAULT '{}', captured_at INTEGER NOT NULL)`)
	for i := 0; i < 20; i++ {
		db.Exec(`INSERT INTO sessions (id, started_at) VALUES (?,?)`, "s"+string(rune('a'+i)), time.Now().UnixMilli())
		db.Exec(`INSERT INTO metrics (session_id, name, value, labels, captured_at) VALUES (?,?,?,?,?)`,
			"s"+string(rune('a'+i)), "m", float64(i+1), `{"type":"input"}`, time.Now().UnixMilli())
	}
	db.Close()

	beforeSessions, beforeMetrics, beforeSum := compactStats(t, dbPath)
	if err := vacuumDB(dbPath); err != nil {
		t.Fatalf("vacuumDB: %v", err)
	}
	afterSessions, afterMetrics, afterSum := compactStats(t, dbPath)

	if beforeSessions != afterSessions || beforeMetrics != afterMetrics {
		t.Errorf("压缩改了行数: sessions %d->%d, metrics %d->%d",
			beforeSessions, afterSessions, beforeMetrics, afterMetrics)
	}
	if beforeSum != afterSum {
		t.Errorf("压缩改了 metric 值之和: %v -> %v", beforeSum, afterSum)
	}
}

// compactStats 读出 sessions 行数、metrics 行数、metric 值之和。
func compactStats(t *testing.T, dbPath string) (sessions, metrics int, sum float64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions)
	db.QueryRow(`SELECT COUNT(*) FROM metrics`).Scan(&metrics)
	db.QueryRow(`SELECT COALESCE(SUM(value), 0) FROM metrics`).Scan(&sum)
	return
}

// TestServeCompact_NoDaemonDoesNotStartOne 断言 daemon 没在跑时 compact 不拉起 daemon。
func TestServeCompact_NoDaemonDoesNotStartOne(t *testing.T) {
	dir := t.TempDir()
	// 让 state 目录指向临时目录（daemon.pid 不存在 → daemon 没在跑）
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".cc-launcher"), 0700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, ".cc-launcher", "telemetry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
		ended_at INTEGER, provider TEXT NOT NULL DEFAULT '', provider_endpoint TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '')`)
	db.Exec(`CREATE TABLE metrics (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
		name TEXT NOT NULL, value REAL NOT NULL, labels TEXT NOT NULL DEFAULT '{}', captured_at INTEGER NOT NULL)`)
	db.Close()

	if err := runCompactInner(); err != nil {
		t.Fatalf("runCompactInner: %v", err)
	}
	// 没拉起 daemon → 不该有 pidfile
	if _, err := os.Stat(filepath.Join(dir, ".cc-launcher", "daemon.pid")); err == nil {
		t.Error("daemon 本来没在跑，compact 不该拉起一个")
	}
	_ = context.Background()
}
