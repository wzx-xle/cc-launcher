package collector

import (
	"context"
	"database/sql"
	"errors"
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

// execOnDB 在 db 文件上执行一条 SQL（读写模式）。
func execOnDB(t *testing.T, dbPath, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// queryStartedAt 读 sessions.started_at。
func queryStartedAt(t *testing.T, dbPath, sessionID string) (int64, bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var startedAt int64
	err = db.QueryRow(`SELECT started_at FROM sessions WHERE id=?`, sessionID).Scan(&startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false
	}
	if err != nil {
		t.Fatalf("query started_at: %v", err)
	}
	return startedAt, true
}

// TestStore_SessionStartedAtNotRaisedByLaterBatch 覆盖 spec scenario
// "a later export batch does not move the start time forward"。
//
// 第一个 batch:session.count ts=t1；第二个 batch(+60s):ts=t2。
// 落库后 sessions.started_at 必须是 t1（ON CONFLICT 的 MIN 生效）。
func TestStore_SessionStartedAtNotRaisedByLaterBatch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	const (
		t1 = 1_790_664_811_109_000_000
		t2 = 1_790_664_871_109_000_000 // +60s
	)

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	for _, body := range [][]byte{
		makeSessionCountBatch(t, "sess-1", t1),
		makeSessionCountBatch(t, "sess-1", t2),
	} {
		res, err := ParseMetricsRequest(body)
		if err != nil {
			t.Fatalf("ParseMetricsRequest: %v", err)
		}
		for _, s := range res.Sessions {
			if err := store.WriteSession(s); err != nil {
				t.Fatalf("WriteSession: %v", err)
			}
		}
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, ok := queryStartedAt(t, dbPath, "sess-1")
	if !ok {
		t.Fatal("session row missing")
	}
	if got != t1/1_000_000 {
		t.Errorf("started_at: want %d (first batch ts), got %d", t1/1_000_000, got)
	}
}

// TestStore_BackfillSessionStartTimes 覆盖 spec scenario
// "existing rows are repaired on startup"。
//
// 构造"被污染"的 db：started_at = 最后一个 metric 的 ts（老版本行为），
// 迁移后应变成该 session 最早 metric（session.count）的 ts。
func TestStore_BackfillSessionStartTimes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	const (
		t1 = 1_790_664_811_109_000_000 // session.count
		t2 = 1_790_664_871_109_000_000 // 后续 metric（= 老版本记的 started_at）
	)

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	for _, m := range []Metric{
		{SessionID: "s1", Name: "claude_code.session.count", Value: 1, CapturedAt: time.UnixMilli(t1 / 1e6)},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 100, Labels: map[string]string{"type": "input"}, CapturedAt: time.UnixMilli(t2 / 1e6)},
	} {
		if err := store.WriteMetric(m); err != nil {
			t.Fatalf("WriteMetric: %v", err)
		}
	}
	// 污染：started_at = 最后一个 metric 的 ts
	if err := store.WriteSession(Session{
		ID: "s1", StartedAt: time.UnixMilli(t2 / 1e6), Cwd: "/tmp", Model: "m",
	}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 模拟老版本 db：把 user_version 归零，触发迁移
	execOnDB(t, dbPath, "PRAGMA user_version = 0")

	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("reopen OpenStore: %v", err)
	}
	if err := store2.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, ok := queryStartedAt(t, dbPath, "s1")
	if !ok {
		t.Fatal("session row missing")
	}
	if got != t1/1_000_000 {
		t.Errorf("started_at after backfill: want %d (first metric ts), got %d", t1/1_000_000, got)
	}
}

// TestStore_BackfillIsIdempotent 覆盖 spec scenario "repair is idempotent"。
//
// 连跑两次回填 → 结果一致。
func TestStore_BackfillIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	const (
		t1 = 1_790_664_811_109_000_000
		t2 = 1_790_664_871_109_000_000
	)

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	for _, m := range []Metric{
		{SessionID: "s1", Name: "claude_code.session.count", Value: 1, CapturedAt: time.UnixMilli(t1 / 1e6)},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 100, Labels: map[string]string{"type": "input"}, CapturedAt: time.UnixMilli(t2 / 1e6)},
	} {
		if err := store.WriteMetric(m); err != nil {
			t.Fatalf("WriteMetric: %v", err)
		}
	}
	if err := store.WriteSession(Session{ID: "s1", StartedAt: time.UnixMilli(t2 / 1e6)}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for i := range 2 {
		if err := backfillSessionStartTimes(db); err != nil {
			t.Fatalf("backfill #%d: %v", i+1, err)
		}
	}
	db.Close()

	got, ok := queryStartedAt(t, dbPath, "s1")
	if !ok {
		t.Fatal("session row missing")
	}
	if got != t1/1_000_000 {
		t.Errorf("started_at after 2 backfills: want %d, got %d", t1/1_000_000, got)
	}
}

// TestStore_BackfillRunsOnlyOnce 覆盖 spec scenario
// "existing rows are repaired on startup" 里的"exactly once; a subsequent
// daemon start does not re-run the repair"。
//
// 第一次启动（user_version=0）执行回填；随后把 started_at 改坏，再次启动
// （user_version=1）不应再回填 → 改坏的值保留原样。
func TestStore_BackfillRunsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	const (
		t1      = 1_790_664_811_109_000_000
		t2      = 1_790_664_871_109_000_000
		corrupt = 1_700_000_000_000
	)

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := store.WriteMetric(Metric{
		SessionID: "s1", Name: "claude_code.session.count", Value: 1,
		CapturedAt: time.UnixMilli(t1 / 1e6),
	}); err != nil {
		t.Fatalf("WriteMetric: %v", err)
	}
	if err := store.WriteSession(Session{ID: "s1", StartedAt: time.UnixMilli(t2 / 1e6)}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	execOnDB(t, dbPath, "PRAGMA user_version = 0")

	// 第一次启动：迁移执行
	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("reopen OpenStore: %v", err)
	}
	store2.Close(t.Context())

	if got, _ := queryStartedAt(t, dbPath, "s1"); got != t1/1_000_000 {
		t.Fatalf("first start should backfill: want %d, got %d", t1/1_000_000, got)
	}

	// 故意改坏
	execOnDB(t, dbPath, "UPDATE sessions SET started_at=? WHERE id=?", corrupt, "s1")

	// 第二次启动：user_version 已置位 → 不再回填
	store3, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("third OpenStore: %v", err)
	}
	store3.Close(t.Context())

	if got, _ := queryStartedAt(t, dbPath, "s1"); got != corrupt {
		t.Errorf("second start must NOT re-run backfill: want %d (untouched), got %d", corrupt, got)
	}
}

// TestStore_BackfillLeavesSessionWithoutMetrics 覆盖 spec scenario
// "sessions without any metrics are left untouched"。
func TestStore_BackfillLeavesSessionWithoutMetrics(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	const ghost = 1_700_000_000_000

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := store.WriteSession(Session{ID: "ghost", StartedAt: time.UnixMilli(ghost)}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	execOnDB(t, dbPath, "PRAGMA user_version = 0")

	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("reopen OpenStore: %v", err)
	}
	store2.Close(t.Context())

	got, ok := queryStartedAt(t, dbPath, "ghost")
	if !ok {
		t.Fatal("session row must not be deleted")
	}
	if got != ghost {
		t.Errorf("started_at of metric-less session: want %d (unchanged), got %d", ghost, got)
	}
}