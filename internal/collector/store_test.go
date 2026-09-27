package collector

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_OpenAndWrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// 写一个 session
	if err := s.WriteSession(Session{
		ID:        "sess-1",
		StartedAt: time.Now(),
		Provider:  "deepseek",
		Cwd:       "/tmp/proj",
		Model:     "deepseek-chat",
	}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}

	// 写两个 metric
	for i, val := range []float64{100, 50} {
		if err := s.WriteMetric(Metric{
			SessionID:  "sess-1",
			Name:       "claude_code.token.usage",
			Value:      val,
			Labels:     map[string]string{"type": "input", "model": "deepseek-chat"},
			CapturedAt: time.Now(),
		}); err != nil {
			t.Fatalf("WriteMetric #%d: %v", i, err)
		}
	}

	// Close 触发 flush + WAL checkpoint
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 验证：直读 SQLite 看数据是否真在
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, "sess-1").Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 1 {
		t.Errorf("sessions: want 1, got %d", n)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM metrics WHERE session_id=?`, "sess-1").Scan(&n); err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	if n != 2 {
		t.Errorf("metrics: want 2, got %d", n)
	}
}

func TestStore_WALEnabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close(context.Background())

	// OpenStore 立即 PRAGMA journal_mode=WAL；后续 sqlite3 读应该看到 wal
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode: want wal, got %q", mode)
	}
}

func TestStore_Backpressure(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// 写超过队列上限的量；至少应有丢弃
	for i := range writeQueueSize * 2 {
		_ = s.WriteMetric(Metric{Name: "x", Value: float64(i)})
	}
	if s.Dropped() == 0 {
		t.Errorf("expected some drops with 2x queue size")
	}

	// 短 ctx 触发 close timeout——预期行为
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = s.Close(ctx) // 不检查；timeout 是预期
}

func TestStore_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// 第二次 Close 不应 panic（closeOnce 保护）
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("second close: %v", err)
	}

	// 关后再 Write 应返回 error
	err = s.WriteMetric(Metric{Name: "x"})
	if err == nil {
		t.Errorf("write after close should error")
	}
}

func TestStore_CreateFileAndParentDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "deeper", "t.db") // 多层目录不存在
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore should mkdir -p: %v", err)
	}
	s.Close(context.Background())

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db file not created: %v", err)
	}
}