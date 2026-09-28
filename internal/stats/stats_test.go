package stats

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cc-launcher/internal/collector"
)

func setupTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	store, err := collector.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// 2 sessions, 多个 metric
	if err := store.WriteSession(collector.Session{
		ID: "s1", StartedAt: now.Add(-2 * time.Hour),
		Provider: "deepseek", Cwd: "/home/u/proj-a", Model: "deepseek-chat",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteSession(collector.Session{
		ID: "s2", StartedAt: now.Add(-1 * time.Hour),
		Provider: "anthropic", Cwd: "/home/u/proj-b", Model: "claude-opus",
	}); err != nil {
		t.Fatal(err)
	}

	// s1: input=200, output=80, cacheCreation=10, cacheRead=20, cost=0.01
	// s2: input=500, output=200, cost=0.05
	for _, m := range []collector.Metric{
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 100, Labels: map[string]string{"type": "input"}, CapturedAt: now},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 100, Labels: map[string]string{"type": "input"}, CapturedAt: now},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 80, Labels: map[string]string{"type": "output"}, CapturedAt: now},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 10, Labels: map[string]string{"type": "cacheCreation"}, CapturedAt: now},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 20, Labels: map[string]string{"type": "cacheRead"}, CapturedAt: now},
		{SessionID: "s1", Name: "claude_code.cost.usage", Value: 0.01, CapturedAt: now},
		{SessionID: "s2", Name: "claude_code.token.usage", Value: 500, Labels: map[string]string{"type": "input"}, CapturedAt: now},
		{SessionID: "s2", Name: "claude_code.token.usage", Value: 200, Labels: map[string]string{"type": "output"}, CapturedAt: now},
		{SessionID: "s2", Name: "claude_code.cost.usage", Value: 0.05, CapturedAt: now},
	} {
		if err := store.WriteMetric(m); err != nil {
			t.Fatal(err)
		}
	}

	// 关 store flush + 让 reader 看到
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func TestQueryStats_BySession(t *testing.T) {
	dbPath := setupTestDB(t)
	res, err := QueryStats(dbPath, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if res.GroupBy != "session" {
		t.Errorf("group: want session, got %q", res.GroupBy)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(res.Sessions))
	}

	// s2 应排第一（更新）
	if res.Sessions[0].SessionID != "s2" {
		t.Errorf("first session should be s2 (newer), got %q", res.Sessions[0].SessionID)
	}

	// 找 s1 验证聚合
	var s1 *SessionStats
	for i := range res.Sessions {
		if res.Sessions[i].SessionID == "s1" {
			s1 = &res.Sessions[i]
		}
	}
	if s1 == nil {
		t.Fatal("s1 not found")
	}
	if s1.InputTokens != 200 {
		t.Errorf("s1 input: want 200, got %d", s1.InputTokens)
	}
	if s1.OutputTokens != 80 {
		t.Errorf("s1 output: want 80, got %d", s1.OutputTokens)
	}
	if s1.CacheCreate != 10 {
		t.Errorf("s1 cache_create: want 10, got %d", s1.CacheCreate)
	}
	if s1.CacheRead != 20 {
		t.Errorf("s1 cache_read: want 20, got %d", s1.CacheRead)
	}
}

func TestQueryStats_ByProvider(t *testing.T) {
	dbPath := setupTestDB(t)
	res, err := QueryStats(dbPath, Query{ByProvider: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.GroupBy != "provider" {
		t.Errorf("group: want provider, got %q", res.GroupBy)
	}
	// 期望：deepseek (200+80+10+20=310 tokens, 0.01 cost), anthropic (500+200=700, 0.05)
	if len(res.Sessions) != 2 {
		t.Fatalf("want 2 providers, got %d", len(res.Sessions))
	}
}

func TestQueryStats_ByProject(t *testing.T) {
	dbPath := setupTestDB(t)
	res, err := QueryStats(dbPath, Query{ByProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.GroupBy != "project" {
		t.Errorf("group: want project, got %q", res.GroupBy)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("want 2 projects, got %d", len(res.Sessions))
	}
}

func TestQueryStats_ProjectFilter(t *testing.T) {
	dbPath := setupTestDB(t)
	res, err := QueryStats(dbPath, Query{Project: "proj-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("want 1 session (proj-a), got %d", len(res.Sessions))
	}
	if res.Sessions[0].Project != "proj-a" {
		t.Errorf("project: got %q", res.Sessions[0].Project)
	}
}

func TestQueryStats_NoDB(t *testing.T) {
	_, err := QueryStats("/tmp/definitely-does-not-exist-12345.db", Query{})
	if err == nil {
		t.Fatal("expected error for missing db")
	}
	if !errors.Is(err, ErrNoDatabase) {
		t.Errorf("expected ErrNoDatabase, got %v", err)
	}
}

func TestFormatTable(t *testing.T) {
	dbPath := setupTestDB(t)
	res, err := QueryStats(dbPath, Query{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	FormatTable(&buf, res)
	out := buf.String()
	if len(out) == 0 {
		t.Fatal("empty table output")
	}
	// 应包含表头 + 数据行 + 总计行（共 4 行：header + 2 sessions + totals）
	lines := bytes.Count([]byte(out), []byte("\n"))
	if lines < 4 {
		t.Errorf("expected ≥4 lines (header + sessions + totals), got %d:\n%s", lines, out)
	}
	if !contains(out, "PROJECT") {
		t.Errorf("output missing PROJECT header: %s", out)
	}
	if !contains(out, "deepseek") {
		t.Errorf("output missing deepseek session: %s", out)
	}
}

func TestFormatJSON(t *testing.T) {
	dbPath := setupTestDB(t)
	res, err := QueryStats(dbPath, Query{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := FormatJSON(res)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal([]byte(s), &decoded); err != nil {
		t.Errorf("invalid JSON: %v\n%s", err, s)
	}
	if len(decoded.Sessions) != 2 {
		t.Errorf("round-tripped sessions: want 2, got %d", len(decoded.Sessions))
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}