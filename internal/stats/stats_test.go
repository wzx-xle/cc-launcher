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

// statsFixedNow 是 stats 窗口测试用的固定"现在"（2026-10-18 17:04 本地）。
//
// 与 spec scenario "--day N aligns the window to the bucket boundaries" 的
// 时间点一致：--day 7 时 since 应为 2026-10-12 本地 00:00。
func statsFixedNow() time.Time {
	return time.Date(2026, 10, 18, 17, 4, 0, 0, time.Local)
}

// setupWindowTestDB 构造"窗口内 / 窗口外"场景的 db（默认窗口 = 最近 7 天）：
//
//   - s-old  ：10 天前开始，只有 9 天前的活动 → 窗口外，不应出现
//   - s-span ：10 天前开始，9 天前有 100 token、1 天前有 500 token
//     → 应出现，且只含窗口内的 500 token
//   - s-in   ：2 小时前开始，2 小时前有 200 token → 应出现
func setupWindowTestDB(t *testing.T, now time.Time) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	store, err := collector.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	writeMetric := func(sid string, val float64, at time.Time) {
		t.Helper()
		if err := store.WriteMetric(collector.Metric{
			SessionID: sid, Name: "claude_code.token.usage", Value: val,
			Labels: map[string]string{"type": "input"}, CapturedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeSession := func(sid string, startedAt time.Time) {
		t.Helper()
		if err := store.WriteSession(collector.Session{
			ID: sid, StartedAt: startedAt, Cwd: "/home/u/" + sid, Model: "m",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// s-old：全部活动在窗口外
	writeSession("s-old", now.AddDate(0, 0, -10))
	writeMetric("s-old", 100, now.AddDate(0, 0, -9))

	// s-span：跨窗口边界，窗口内外都有
	writeSession("s-span", now.AddDate(0, 0, -10))
	writeMetric("s-span", 100, now.AddDate(0, 0, -9))
	writeMetric("s-span", 500, now.AddDate(0, 0, -1))

	// s-in：窗口内
	writeSession("s-in", now.Add(-2*time.Hour))
	writeMetric("s-in", 200, now.Add(-2*time.Hour))

	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// findSession 按 ID 找 detail 行。
func findSession(res *Result, id string) *SessionStats {
	for i := range res.Sessions {
		if res.Sessions[i].SessionID == id {
			return &res.Sessions[i]
		}
	}
	return nil
}

// TestQueryStats_WindowIsUsageTime 覆盖 spec scenario
// "usage outside the window is not counted" + "a session started before the
// window still appears" + "sessions without in-window activity are omitted"。
func TestQueryStats_WindowIsUsageTime(t *testing.T) {
	now := statsFixedNow()
	dbPath := setupWindowTestDB(t, now)

	res, err := QueryStats(dbPath, Query{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	// 窗口外活动的 session 不出现
	if findSession(res, "s-old") != nil {
		t.Errorf("s-old has no in-window activity and must not appear")
	}

	// 窗口前开始但窗口内有活动的 session 出现，且只含窗口内 tokens
	span := findSession(res, "s-span")
	if span == nil {
		t.Fatal("s-span must appear (it has in-window activity)")
	}
	if span.InputTokens != 500 {
		t.Errorf("s-span InputTokens: want 500 (in-window only), got %d", span.InputTokens)
	}
	// STARTED 列仍是真实起始（早于窗口）
	wantStart := now.AddDate(0, 0, -10)
	if span.StartedAt == nil {
		t.Fatal("s-span StartedAt must not be nil")
	}
	if !span.StartedAt.Equal(wantStart) {
		t.Errorf("s-span StartedAt: want %s (real session start), got %s", wantStart, span.StartedAt)
	}

	// 窗口内的另一个 session 正常出现
	in := findSession(res, "s-in")
	if in == nil {
		t.Fatal("s-in must appear")
	}
	if in.InputTokens != 200 {
		t.Errorf("s-in InputTokens: want 200, got %d", in.InputTokens)
	}
}

// TestQueryStats_DayNAlignsWindowToBucketBoundary 覆盖 spec scenario
// "--day N aligns the window to the bucket boundaries"：
// --day 7 → since = 最旧桶那天的本地 00:00（2026-10-12），恰好 7 个 daily 桶。
func TestQueryStats_DayNAlignsWindowToBucketBoundary(t *testing.T) {
	now := statsFixedNow()
	dbPath := setupWindowTestDB(t, now)

	days := 7
	res, err := QueryStats(dbPath, Query{Buckets: &days, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	wantSince := time.Date(2026, 10, 12, 0, 0, 0, 0, time.Local)
	if !res.Since.Equal(wantSince) {
		t.Errorf("since: want %s (local midnight of oldest bucket day), got %s", wantSince, res.Since)
	}
	if len(res.BucketRows) != days {
		t.Fatalf("want %d daily buckets, got %d", days, len(res.BucketRows))
	}
	if res.BucketRows[0].Label != "10-12" {
		t.Errorf("first bucket label: want %q, got %q", "10-12", res.BucketRows[0].Label)
	}
	last := res.BucketRows[len(res.BucketRows)-1]
	if last.Label != "10-18" {
		t.Errorf("last bucket label: want %q, got %q", "10-18", last.Label)
	}
}

// TestQueryStats_DetailTotalsMatchBucketTotals 覆盖 spec scenario
// "detail totals and bucket totals agree"。
//
// detail 与桶覆盖同一时间窗（Decision 6）→ 两侧 token / cost 必须完全相等。
func TestQueryStats_DetailTotalsMatchBucketTotals(t *testing.T) {
	now := statsFixedNow()
	dbPath := setupWindowTestDB(t, now)

	days := 7
	res, err := QueryStats(dbPath, Query{Buckets: &days, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if res.BucketTotals == nil {
		t.Fatal("bucket_totals must not be nil")
	}

	var sumIn, sumOut, sumCre, sumRead int64
	var sumCost float64
	for _, b := range res.BucketRows {
		sumIn += b.InputTokens
		sumOut += b.OutputTokens
		sumCre += b.CacheCreate
		sumRead += b.CacheRead
		sumCost += b.CostUSD
	}
	// 桶内部自洽
	if sumIn != res.BucketTotals.InputTokens {
		t.Errorf("bucket rows sum != bucket_totals: %d != %d", sumIn, res.BucketTotals.InputTokens)
	}

	// detail totals == Σbucket rows
	if res.Totals.InputTokens != sumIn {
		t.Errorf("detail totals InputTokens: want %d, got %d", sumIn, res.Totals.InputTokens)
	}
	if res.Totals.OutputTokens != sumOut {
		t.Errorf("detail totals OutputTokens: want %d, got %d", sumOut, res.Totals.OutputTokens)
	}
	if res.Totals.CacheCreate != sumCre {
		t.Errorf("detail totals CacheCreate: want %d, got %d", sumCre, res.Totals.CacheCreate)
	}
	if res.Totals.CacheRead != sumRead {
		t.Errorf("detail totals CacheRead: want %d, got %d", sumRead, res.Totals.CacheRead)
	}
	if res.Totals.CostUSD != sumCost {
		t.Errorf("detail totals CostUSD: want %v, got %v", sumCost, res.Totals.CostUSD)
	}
}

// TestQueryStats_WithoutDaySinceUnchanged 覆盖 spec scenario
// "without --day the window is unchanged"：since = 7 天前，不做午夜对齐。
func TestQueryStats_WithoutDaySinceUnchanged(t *testing.T) {
	now := statsFixedNow()
	dbPath := setupWindowTestDB(t, now)

	res, err := QueryStats(dbPath, Query{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	want := now.AddDate(0, 0, -7)
	if !res.Since.Equal(want) {
		t.Errorf("since: want %s (7 days before now, not midnight-aligned), got %s", want, res.Since)
	}
}