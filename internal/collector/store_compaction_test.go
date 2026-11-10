package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// dbLabels 读出某 session 的所有 metric 行的 labels（原始 JSON 字符串）。
func dbLabels(t *testing.T, dbPath, sessionID string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT labels FROM metrics WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

// dbCount 读出两张表的行数。
func dbCount(t *testing.T, dbPath string) (sessions, metrics int) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions)
	db.QueryRow(`SELECT COUNT(*) FROM metrics`).Scan(&metrics)
	return
}

// TestStore_LabelsWhitelist 断言落库的 labels 只保留白名单 key，且 sessions 行
// 里的 model / cwd / provider 不受影响。
//
// 为什么 sessions 行必须不受影响：parser 在**合并后**的 labels 里取 model
// （parser.go:160），而 provider / cwd 取自 resAttrs。裁剪发生在 store 层，
// 所以 parser 的提取链完好——这条断言就是它的守门人。
func TestStore_LabelsWhitelist(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	sess := Session{
		ID:               "sess-1",
		StartedAt:        time.Now(),
		Provider:         "minimax",
		Cwd:              "/home/u/proj",
		ProviderEndpoint: "api.example.com",
		Model:            "some-model",
	}
	if err := s.WriteSession(sess); err != nil {
		t.Fatal(err)
	}
	// 一条带全套主机级常量的 metric
	if err := s.WriteMetric(Metric{
		SessionID:  "sess-1",
		Name:       "claude_code.token.usage",
		Value:      42,
		CapturedAt: time.Now(),
		Labels: map[string]string{
			"type":         "input",
			"model":        "some-model",
			"ccl.provider": "minimax",
			"provider":     "minimax",
			// 下面这些都该被剥掉
			"ccl.cwd":         "/home/u/proj",
			"service.name":    "claude-code",
			"service.version": "2.1.277",
			"host.arch":       "amd64",
			"os.type":         "linux",
			"os.version":      "6.8.0",
			"terminal.type":   "ssh-session",
			"query_source":    "main",
			"effort":          "high",
			"user.id":         "0123456789abcdef",
		},
	}); err != nil {
		t.Fatal(err)
	}
	// 等 worker 写完
	waitForRows(t, dbPath, 1)

	got := dbLabels(t, dbPath, "sess-1")
	if len(got) != 1 {
		t.Fatalf("metric 行数 = %d，期望 1", len(got))
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(got[0]), &m); err != nil {
		t.Fatalf("labels 不是合法 JSON: %v (%q)", err, got[0])
	}
	for _, k := range []string{"type", "model", "ccl.provider", "provider"} {
		if _, ok := m[k]; !ok {
			t.Errorf("labels 应保留 %q，实际 = %v", k, m)
		}
	}
	for _, k := range []string{"ccl.cwd", "service.name", "host.arch", "os.type",
		"os.version", "terminal.type", "query_source", "effort", "user.id",
		"service.version"} {
		if _, ok := m[k]; ok {
			t.Errorf("labels 不该保留 %q，实际 = %v", k, m)
		}
	}

	// sessions 行必须完好
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var prov, cwd, model string
	if err := db.QueryRow(`SELECT provider, cwd, model FROM sessions WHERE id = ?`, "sess-1").
		Scan(&prov, &cwd, &model); err != nil {
		t.Fatal(err)
	}
	if prov != "minimax" || cwd != "/home/u/proj" || model != "some-model" {
		t.Errorf("sessions 行被裁剪影响了: provider=%q cwd=%q model=%q", prov, cwd, model)
	}
}

// TestStore_LabelsEmptyObject 断言没有白名单 key 时落库 "{}" 而不是 "null"。
func TestStore_LabelsEmptyObject(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	if err := s.WriteMetric(Metric{
		SessionID:  "sess-x",
		Name:       "claude_code.cost.usage",
		Value:      1,
		CapturedAt: time.Now(),
		Labels:     map[string]string{"service.name": "claude-code", "os.type": "linux"},
	}); err != nil {
		t.Fatal(err)
	}
	waitForRows(t, dbPath, 1)

	got := dbLabels(t, dbPath, "sess-x")
	if len(got) != 1 {
		t.Fatalf("metric 行数 = %d，期望 1", len(got))
	}
	if got[0] != "{}" {
		t.Errorf("labels = %q，期望 \"{}\"（不能是 null）", got[0])
	}
}

// TestStore_PruneOlderThan 断言剪枝按 captured_at 判年龄、保留仍在产生数据的
// 长会话、且清掉孤儿 metric 行。
func TestStore_PruneOlderThan(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	now := time.Now()
	old := now.Add(-100 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)

	// 老 session：只有老数据
	s.WriteSession(Session{ID: "old", StartedAt: old})
	s.WriteMetric(Metric{SessionID: "old", Name: "m", Value: 1, CapturedAt: old})
	// 长会话：100 天前开始，但昨天仍有数据
	s.WriteSession(Session{ID: "long", StartedAt: old})
	s.WriteMetric(Metric{SessionID: "long", Name: "m", Value: 1, CapturedAt: old})
	s.WriteMetric(Metric{SessionID: "long", Name: "m", Value: 2, CapturedAt: recent})
	// 新 session
	s.WriteSession(Session{ID: "new", StartedAt: recent})
	s.WriteMetric(Metric{SessionID: "new", Name: "m", Value: 3, CapturedAt: recent})
	// 孤儿 metric：没有对应 session 行
	s.WriteMetric(Metric{SessionID: "", Name: "m", Value: 4, CapturedAt: old})

	waitForRows(t, dbPath, 5)

	cutoff := now.Add(-90 * 24 * time.Hour)
	sessions, metrics, err := s.PruneOlderThan(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	// 删 3 条：老 session 的那条、长会话的**老**那条（剪枝按 captured_at 判年龄，
	// 长会话的老数据同样过期）、孤儿行那条。长会话的**近期**那条保留。
	if metrics != 3 {
		t.Errorf("删除的 metrics = %d，期望 3（老 session 的 + 长会话的老的 + 孤儿行的）", metrics)
	}
	if sessions != 1 {
		t.Errorf("删除的 sessions = %d，期望 1（只剩老数据的那个）", sessions)
	}

	// 长会话的近期数据必须还在
	got := dbLabels(t, dbPath, "long")
	if len(got) != 1 {
		t.Errorf("长会话剩余 metric 行 = %d，期望 1（近期数据必须保留）", len(got))
	}
	// 孤儿行必须没了
	orphans := dbLabels(t, dbPath, "")
	if len(orphans) != 0 {
		t.Errorf("孤儿 metric 行还在: %v", orphans)
	}
	// 新 session 必须还在
	if len(dbLabels(t, dbPath, "new")) != 1 {
		t.Errorf("新 session 的 metric 行不见了")
	}

	// 再跑一次不应有副作用
	s2, m2, err := s.PruneOlderThan(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if s2 != 0 || m2 != 0 {
		t.Errorf("第二次剪枝删了东西: sessions=%d metrics=%d", s2, m2)
	}
}

// TestStore_PruneKeepsFileSize 断言剪枝**不做** VACUUM（空闲页留给后续写入复用）。
func TestStore_PruneKeepsFileSize(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	old := time.Now().Add(-100 * 24 * time.Hour)
	for i := 0; i < 50; i++ {
		s.WriteMetric(Metric{SessionID: "old", Name: "m", Value: 1, CapturedAt: old})
	}
	waitForRows(t, dbPath, 50)

	before := fileSizeOf(t, dbPath)
	if _, _, err := s.PruneOlderThan(time.Now().Add(-90 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	after := fileSizeOf(t, dbPath)
	if after < before {
		t.Errorf("剪枝不该缩小文件: %d -> %d", before, after)
	}
}

// TestStore_MigrateLabelsToWhitelist 断言存量 db 在打开时被迁移：labels 收敛、
// 行数不变、user_version 升到 3、备份存在且行数与原库一致。
func TestStore_MigrateLabelsToWhitelist(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	// 造一个"老格式" db：user_version=2，labels 带冗余 key
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
		ended_at INTEGER, provider TEXT NOT NULL DEFAULT '', provider_endpoint TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '')`)
	db.Exec(`CREATE TABLE metrics (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
		name TEXT NOT NULL, value REAL NOT NULL, labels TEXT NOT NULL DEFAULT '{}', captured_at INTEGER NOT NULL)`)
	db.Exec(`PRAGMA user_version = 2`)
	for i := 0; i < 20; i++ {
		db.Exec(`INSERT INTO metrics (session_id, name, value, labels, captured_at) VALUES (?,?,?,?,?)`,
			"s1", "claude_code.token.usage", float64(i),
			`{"type":"input","model":"m","ccl.provider":"p","service.name":"claude-code","user.id":"deadbeef"}`,
			time.Now().UnixMilli())
	}
	db.Close()

	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	// labels 收敛
	for _, l := range dbLabels(t, dbPath, "s1") {
		var m map[string]string
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("labels 不是合法 JSON: %v", err)
		}
		if _, ok := m["service.name"]; ok {
			t.Errorf("labels 还留着冗余 key: %v", m)
		}
		if _, ok := m["user.id"]; ok {
			t.Errorf("labels 还留着 user.id: %v", m)
		}
		if m["type"] != "input" || m["model"] != "m" || m["ccl.provider"] != "p" {
			t.Errorf("白名单 key 丢了: %v", m)
		}
	}
	// 行数不变
	_, metrics := dbCount(t, dbPath)
	if metrics != 20 {
		t.Errorf("迁移后 metrics 行数 = %d，期望 20", metrics)
	}
	// user_version 升到 3
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var v int
	db2.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != labelsWhitelistVersion {
		t.Errorf("user_version = %d，期望 %d", v, labelsWhitelistVersion)
	}
	// 备份存在，且行数与原库一致（WAL 模式下"只复制主文件"会少数据）
	backupDir := filepath.Join(dir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("读备份目录失败: %v", err)
	}
	found := ""
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".db" || len(e.Name()) > len("telemetry.db.pre-compact") {
			found = filepath.Join(backupDir, e.Name())
		}
	}
	if found == "" {
		t.Fatalf("备份目录里没有备份文件: %v", entries)
	}
	_, backupMetrics := dbCount(t, found)
	if backupMetrics != 20 {
		t.Errorf("备份里的 metrics 行数 = %d，期望 20（备份必须是完整快照）", backupMetrics)
	}

	// 幂等：再打开一次不重复备份
	entriesBefore, _ := os.ReadDir(backupDir)
	s.Close(context.Background())
	s2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close(context.Background())
	entriesAfter, _ := os.ReadDir(backupDir)
	if len(entriesAfter) != len(entriesBefore) {
		t.Errorf("重复打开产生了新备份: %d -> %d", len(entriesBefore), len(entriesAfter))
	}
}

// TestStore_MigrateFailureNotFatal 断言迁移失败不阻止 daemon 启动。
//
// 怎么做失败：把 metrics 表换成一张列结构不对的表 → 迁移的 UPDATE 会报错。
// 此时 OpenStore 必须**成功返回**（db 保持旧格式，功能不受影响）。
func TestStore_MigrateFailureNotFatal(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// 造一个"结构不对"的 metrics 表：缺 labels 列
	db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
		ended_at INTEGER, provider TEXT NOT NULL DEFAULT '', provider_endpoint TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '')`)
	db.Exec(`CREATE TABLE metrics (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
		name TEXT NOT NULL, value REAL NOT NULL, captured_at INTEGER NOT NULL)`)
	db.Exec(`PRAGMA user_version = 2`)
	db.Close()

	// createSchema 用 CREATE TABLE IF NOT EXISTS → 不会补列 → 迁移的 UPDATE 报
	// "no such column: labels"。
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("迁移失败不该让 OpenStore 失败（db 保持旧格式即可用）: %v", err)
	}
	defer s.Close(context.Background())

	// user_version 不该被升到 3（否则下次启动不重试）
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var v int
	db2.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v >= labelsWhitelistVersion {
		t.Errorf("迁移失败后 user_version = %d，不该升到 %d（下次启动要重试）", v, labelsWhitelistVersion)
	}
}

// waitForRows 等 worker 把队列里的行写进 db（最多 3s）。
func waitForRows(t *testing.T, dbPath string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, n := dbCount(t, dbPath)
		if n >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, n := dbCount(t, dbPath)
	t.Fatalf("等 %d 行超时，实际 %d 行", want, n)
}

// fileSizeOf 返回文件大小。
func fileSizeOf(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
