package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cc-launcher/internal/collector"
)

// fixedNow 返回测试用的固定时间点(本地时区 17:30,2026-09-30)。
//
// spec scenario "--day 1 includes the current hour with partial marker" 要求在 17:30 跑,
// 此固定点保证 17:00 行 IsCurrent=true 且其它行 IsCurrent=false。
func fixedNow() time.Time {
	return time.Date(2026, 9, 30, 17, 30, 0, 0, time.Local)
}

// fixedNowMs 返回 fixedNow 的 UnixMilli。
func fixedNowMs() int64 {
	return fixedNow().UnixMilli()
}

// fixedTodayStartMs 返回 fixedNow 当天 00:00 的 UnixMilli。
func fixedTodayStartMs() int64 {
	return startOfDayMillis(fixedNow())
}

// setupBucketTestDB 在临时目录建 telemetry.db,写 fixedNow 时刻的 2 sessions + 完整 metric 集。
//
// 用 collector.WithClock 注入固定时钟,让所有 metric 的 captured_at / session 的
// started_at 都对齐 fixedNow,SQL 桶表达式可重现。
func setupBucketTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	now := fixedNow()
	store, err := collector.OpenStore(dbPath, collector.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	// s1 起 fixedNow -2h (15:30 桶)
	if err := store.WriteSession(collector.Session{
		ID: "s1", StartedAt: now.Add(-2 * time.Hour),
		Provider: "deepseek", Cwd: "/home/u/proj-a", Model: "deepseek-chat",
	}); err != nil {
		t.Fatal(err)
	}
	// s2 起 fixedNow -1h (16:30 桶)
	if err := store.WriteSession(collector.Session{
		ID: "s2", StartedAt: now.Add(-1 * time.Hour),
		Provider: "anthropic", Cwd: "/home/u/proj-b", Model: "claude-opus",
	}); err != nil {
		t.Fatal(err)
	}

	metrics := []collector.Metric{
		// s1: input=200, output=80, cacheCreation=10, cacheRead=20, cost=0.01
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 100, Labels: map[string]string{"type": "input"}, CapturedAt: now.Add(-2 * time.Hour)},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 100, Labels: map[string]string{"type": "input"}, CapturedAt: now.Add(-2 * time.Hour)},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 80, Labels: map[string]string{"type": "output"}, CapturedAt: now.Add(-2 * time.Hour)},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 10, Labels: map[string]string{"type": "cacheCreation"}, CapturedAt: now.Add(-2 * time.Hour)},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 20, Labels: map[string]string{"type": "cacheRead"}, CapturedAt: now.Add(-2 * time.Hour)},
		{SessionID: "s1", Name: "claude_code.cost.usage", Value: 0.01, CapturedAt: now.Add(-2 * time.Hour)},
		// s2: input=500, output=200, cost=0.05
		{SessionID: "s2", Name: "claude_code.token.usage", Value: 500, Labels: map[string]string{"type": "input"}, CapturedAt: now.Add(-1 * time.Hour)},
		{SessionID: "s2", Name: "claude_code.token.usage", Value: 200, Labels: map[string]string{"type": "output"}, CapturedAt: now.Add(-1 * time.Hour)},
		{SessionID: "s2", Name: "claude_code.cost.usage", Value: 0.05, CapturedAt: now.Add(-1 * time.Hour)},
	}
	for _, m := range metrics {
		if err := store.WriteMetric(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// TestBucket_Hourly_24Rows 覆盖 spec scenario "--day 1 produces 24 hourly buckets"。
//
// mock now 任意 17:30 → hourly 应返回 18 行 (00..17)。
func TestBucket_Hourly_24Rows(t *testing.T) {
	dbPath := setupBucketTestDB(t)
	rows, err := queryBucketSummaryAt(dbPath, "hour", fixedTodayStartMs(), fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 18 {
		t.Fatalf("want 18 hourly rows (00..17), got %d", len(rows))
	}
	// 首行 label = "00:00",末行 label = "17:00*"
	if rows[0].Label != "00:00" {
		t.Errorf("first row label: want %q, got %q", "00:00", rows[0].Label)
	}
	if rows[len(rows)-1].Label != "17:00*" {
		t.Errorf("last row label: want %q, got %q", "17:00*", rows[len(rows)-1].Label)
	}
	// 顺序:key "00:00" < "01:00" < ... < "17:00"
	for i := 1; i < len(rows); i++ {
		if rows[i].BucketUnixMs <= rows[i-1].BucketUnixMs {
			t.Errorf("rows not chronological at i=%d: %d !< %d", i, rows[i].BucketUnixMs, rows[i-1].BucketUnixMs)
		}
	}
}

// TestBucket_Hourly_PartialMarker 覆盖 spec scenario "--day 1 includes the current hour with partial marker"。
//
// mock now=17:30 → rows[17] IsCurrent=true,其它行 IsCurrent=false;rows[17] Label="17:00*"。
func TestBucket_Hourly_PartialMarker(t *testing.T) {
	dbPath := setupBucketTestDB(t)
	rows, err := queryBucketSummaryAt(dbPath, "hour", fixedTodayStartMs(), fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	var currentRow *BucketRow
	for i := range rows {
		if rows[i].IsCurrent {
			currentRow = &rows[i]
			break
		}
	}
	if currentRow == nil {
		t.Fatal("no IsCurrent row found")
	}
	wantCurrentHour := fixedNow().Hour()
	wantCurrentLabel := fmt.Sprintf("%02d:00*", wantCurrentHour)
	if currentRow.Label != wantCurrentLabel {
		t.Errorf("IsCurrent row label: want %q, got %q", wantCurrentLabel, currentRow.Label)
	}
	if currentRow.BucketISO != fmt.Sprintf("2026-09-30T%02d:00", wantCurrentHour) {
		t.Errorf("IsCurrent row ISO: want %q, got %q",
			fmt.Sprintf("2026-09-30T%02d:00", wantCurrentHour), currentRow.BucketISO)
	}
	// 恰好 1 个 IsCurrent=true 行,且为末行
	currentCount := 0
	for _, r := range rows {
		if r.IsCurrent {
			currentCount++
		}
	}
	if currentCount != 1 {
		t.Errorf("want exactly 1 IsCurrent=true row, got %d", currentCount)
	}
	// 前一行(hour-1)不是 current
	if wantCurrentHour > 0 && rows[wantCurrentHour-1].IsCurrent {
		t.Errorf("row[%d] (%02d:00) should not be current", wantCurrentHour-1, wantCurrentHour-1)
	}
}

// TestBucket_Daily_7Rows 覆盖 spec scenario "--day 7 produces 7 daily buckets in chronological order"。
//
// 7 天前 → 今天 = 7 daily 桶,MM-DD 升序。
func TestBucket_Daily_7Rows(t *testing.T) {
	dbPath := setupBucketTestDB(t)
	since := fixedTodayStartMs() - int64(6*24*time.Hour/time.Millisecond) // 6 天前 0 点 → 共 7 天(含今天)
	rows, err := queryBucketSummaryAt(dbPath, "day", since, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 {
		t.Fatalf("want 7 daily rows, got %d", len(rows))
	}
	// labels 09-24 → 09-30(7 天范围:fixedNow=09-30 → 6 天前=09-24)
	wantLabels := []string{"09-24", "09-25", "09-26", "09-27", "09-28", "09-29", "09-30"}
	for i, w := range wantLabels {
		if rows[i].Label != w {
			t.Errorf("row[%d] label: want %q, got %q", i, w, rows[i].Label)
		}
	}
	// 桶 unixMs 升序
	for i := 1; i < len(rows); i++ {
		if rows[i].BucketUnixMs <= rows[i-1].BucketUnixMs {
			t.Errorf("daily rows not chronological at i=%d", i)
		}
	}
}

// TestBucket_BoundariesInclusive 覆盖 spec scenario "--day 7 boundary aligns with local calendar day" + "--day 30 boundary ends today (inclusive)"。
//
// 在 todayStartMs 整 0 点的 session 应计入今日桶;在 todayStartMs - 1ms 的 session 不计。
func TestBucket_BoundariesInclusive(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	now := fixedNow()                       // 2026-09-30 17:30
	todayStart := startOfDayMillis(now)     // 2026-09-30 00:00
	store, err := collector.OpenStore(dbPath, collector.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	// boundary session: started_at = todayStart 整 0 点
	if err := store.WriteSession(collector.Session{
		ID: "boundary", StartedAt: time.UnixMilli(todayStart),
		Provider: "anthropic", Cwd: "/tmp", Model: "claude-opus",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// 7 天 daily:since = todayStart - 6 天,todayStart 桶应是最后一行
	since := todayStart - int64(6*24*time.Hour/time.Millisecond)
	rows, err := queryBucketSummaryAt(dbPath, "day", since, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 {
		t.Fatalf("want 7 daily rows, got %d", len(rows))
	}
	if rows[len(rows)-1].Sessions != 1 {
		t.Errorf("today bucket should include boundary session: want 1, got %d", rows[len(rows)-1].Sessions)
	}
}

// TestBucket_TokenSums 覆盖 spec scenario "token / cost values follow the same metric-name rules"。
//
// fixedNow 时刻 2 sessions: s1 在 15:30 桶(input=200, output=80, cacheCreation=10, cacheRead=20, cost=0.01);
// s2 在 16:30 桶(input=500, output=200, cost=0.05)。SQL 用 SUM CASE WHEN 与 base stats 同规则。
func TestBucket_TokenSums(t *testing.T) {
	dbPath := setupBucketTestDB(t)
	rows, err := queryBucketSummaryAt(dbPath, "hour", fixedTodayStartMs(), fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	// 找 15:00 桶(s1)和 16:00 桶(s2)
	var s1Row, s2Row *BucketRow
	for i := range rows {
		if rows[i].Label == "15:00" {
			s1Row = &rows[i]
		}
		if rows[i].Label == "16:00" {
			s2Row = &rows[i]
		}
	}
	if s1Row == nil || s2Row == nil {
		t.Fatal("missing 15:00 or 16:00 row")
	}
	if s1Row.Sessions != 1 || s1Row.InputTokens != 200 || s1Row.OutputTokens != 80 ||
		s1Row.CacheCreate != 10 || s1Row.CacheRead != 20 {
		t.Errorf("15:00 row metrics: sess=%d in=%d out=%d cre=%d read=%d, want 1/200/80/10/20",
			s1Row.Sessions, s1Row.InputTokens, s1Row.OutputTokens, s1Row.CacheCreate, s1Row.CacheRead)
	}
	if s2Row.InputTokens != 500 || s2Row.OutputTokens != 200 || s2Row.CacheRead != 0 || s2Row.CacheCreate != 0 {
		t.Errorf("16:00 row metrics: in=%d out=%d cre=%d read=%d, want 500/200/0/0",
			s2Row.InputTokens, s2Row.OutputTokens, s2Row.CacheCreate, s2Row.CacheRead)
	}
}

// TestBucket_EmptyDB_24Rows 覆盖 spec scenario "--day N tolerates empty db gracefully"。
//
// 空 db 调 QueryBucketSummary(hour) 仍返回 18 行(0..17 此刻,no data)零值桶。
func TestBucket_EmptyDB_HourlyRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	store, err := collector.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	rows, err := queryBucketSummaryAt(dbPath, "hour", fixedTodayStartMs(), fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("expected hourly rows even with empty db, got 0")
	}
	for i, r := range rows {
		if r.Sessions != 0 || r.InputTokens != 0 || r.CostUSD != 0 {
			t.Errorf("row[%d] should be zero, got %+v", i, r)
		}
	}
}

// TestBucket_Daily_FillsMissing 覆盖 spec scenario "--day 7 行" + "补齐缺失 daily 桶"。
//
// 7 天 daily 范围,sessions 全无 → 仍 7 行零值桶,不是 0 行。
func TestBucket_Daily_FillsMissing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	store, err := collector.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	since := fixedTodayStartMs() - int64(6*24*time.Hour/time.Millisecond)
	rows, err := queryBucketSummaryAt(dbPath, "day", since, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 {
		t.Fatalf("want 7 daily rows even with empty db, got %d", len(rows))
	}
}

// TestBucket_ErrNoDatabase 覆盖 spec scenario db 不存在 → errors.Is(err, ErrNoDatabase)。
func TestBucket_ErrNoDatabase(t *testing.T) {
	_, err := QueryBucketSummary("/tmp/definitely-not-exist-bucket.db", "hour", 0, nil)
	if err == nil {
		t.Fatal("expected error for missing db")
	}
	if !errors.Is(err, ErrNoDatabase) {
		t.Errorf("expected ErrNoDatabase, got %v", err)
	}
}

// TestBucket_Daily_KeyedByConsumptionTime 覆盖 spec scenario
// "daily buckets are keyed by consumption time"。
//
// 一个 session 起于 09-28，metric 分布在 09-28 / 09-29 / 09-30：
//   - daily 桶各天只含当天 captured 的 token（design Decision 5）
//   - SESSIONS 列仍按 s.started_at 归桶 → 只有 09-28 桶 sessions=1
func TestBucket_Daily_KeyedByConsumptionTime(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	now := fixedNow() // 2026-09-30 17:30

	store, err := collector.OpenStore(dbPath, collector.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteSession(collector.Session{
		ID: "x", StartedAt: time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local),
		Provider: "anthropic", Cwd: "/tmp", Model: "claude-opus",
	}); err != nil {
		t.Fatal(err)
	}
	// 每天一个 input metric
	for _, d := range []struct {
		day int
		val float64
	}{{28, 100}, {29, 200}, {30, 300}} {
		if err := store.WriteMetric(collector.Metric{
			SessionID: "x", Name: "claude_code.token.usage", Value: d.val,
			Labels:     map[string]string{"type": "input"},
			CapturedAt: time.Date(2026, 9, d.day, 12, 0, 0, 0, time.Local),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// 3 个 daily 桶：09-28 / 09-29 / 09-30
	since := time.Date(2026, 9, 28, 0, 0, 0, 0, time.Local).UnixMilli()
	rows, err := queryBucketSummaryAt(dbPath, "day", since, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 daily rows, got %d", len(rows))
	}
	wantTokens := []int64{100, 200, 300}
	wantLabels := []string{"09-28", "09-29", "09-30"}
	for i := range rows {
		if rows[i].Label != wantLabels[i] {
			t.Errorf("row[%d] label: want %q, got %q", i, wantLabels[i], rows[i].Label)
		}
		if rows[i].InputTokens != wantTokens[i] {
			t.Errorf("row[%d] (%s) InputTokens: want %d, got %d (bucket must be keyed by consumption time)",
				i, rows[i].Label, wantTokens[i], rows[i].InputTokens)
		}
	}
	// SESSIONS 列仍按 s.started_at 归桶：只有 09-28 桶有 session
	if rows[0].Sessions != 1 {
		t.Errorf("09-28 bucket Sessions: want 1 (session started here), got %d", rows[0].Sessions)
	}
	if rows[1].Sessions != 0 || rows[2].Sessions != 0 {
		t.Errorf("09-29/09-30 Sessions: want 0/0, got %d/%d", rows[1].Sessions, rows[2].Sessions)
	}
}

// TestBucketSkeleton_DSTSpringForward 验证**夏令时春季调时**当天，桶天数不偏一。
//
// 背景：days 的计算原本是 (todayStartMs-sinceMs)/24h 向下取整。本地时区若有
// 夏令时，"跨 N 个日历日"的两端午夜相差 (N-1)*24h **±1h** —— 春季调时少一小时
// → 向下取整少算一天 → --day N 少一个桶。改成四舍五入后恢复正确。
//
// 2026-03-01 00:00 EST → 2026-03-09 00:00 EDT 跨 8 个日历日，但实际只有
// 8*24-1 = 191 小时（03-08 02:00 跳到 03:00）→ 191/24 = 7.958：
//   - 向下取整 → 7 → days = 8（少一个桶，bug）
//   - 四舍五入 → 8 → days = 9（正确）
func TestBucketSkeleton_DSTSpringForward(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("本机无 tzdata,跳过夏令时场景: %v", err)
	}

	now := time.Date(2026, 3, 9, 17, 0, 0, 0, loc)
	since := time.Date(2026, 3, 1, 0, 0, 0, 0, loc).UnixMilli()

	rows := bucketSkeleton("day", now, startOfDayMillis(now), since)
	if len(rows) != 9 {
		t.Fatalf("春季调时跨 8 个日历日 → 应有 9 个 daily 桶,got %d", len(rows))
	}
	// 首尾日期也要对（桶是按日历日推的，不是按 24h 算的）
	if rows[0].label != "03-01" {
		t.Errorf("首桶 label: want %q, got %q", "03-01", rows[0].label)
	}
	if last := rows[len(rows)-1]; last.label != "03-09" {
		t.Errorf("末桶 label: want %q, got %q", "03-09", last.label)
	}
}

// TestBucketSkeleton_NormalDaysUnaffected 确认普通时区（非夏令时）天数没被改坏。
func TestBucketSkeleton_NormalDaysUnaffected(t *testing.T) {
	now := time.Date(2026, 9, 30, 17, 30, 0, 0, time.Local)
	since := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local).UnixMilli()

	rows := bucketSkeleton("day", now, startOfDayMillis(now), since)
	if len(rows) != 7 {
		t.Fatalf("跨 6 个日历日 → 应有 7 个 daily 桶,got %d", len(rows))
	}
	if rows[0].label != "09-24" {
		t.Errorf("首桶 label: want %q, got %q", "09-24", rows[0].label)
	}
}

// TestHumanizeTokens_Boundaries 覆盖 spec scenario "humanize 边界 case"。
func TestHumanizeTokens_Boundaries(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{350, "350"},
		{999, "999"},
		{1000, "1.00K"},
		{1234, "1.23K"},
		{12345, "12.3K"},
		{123456, "123K"},
		{999_499, "999K"},
		{999_500, "1.00M"},
		{999_999, "1.00M"},
		{1_000_000, "1.00M"},
		{1_234_567, "1.23M"},
		{12_345_678, "12.3M"},
		{123_456_789, "123M"},
		{999_499_999, "999M"},
		{999_500_000, "1.00B"},
		{1_234_567_890, "1.23B"},
	}
	for _, tc := range cases {
		got := humanizeTokens(tc.in)
		if got != tc.want {
			t.Errorf("humanizeTokens(%d): want %q, got %q", tc.in, tc.want, got)
		}
	}
}

// TestFormatBucketTable_TokensSumColumn 覆盖 spec scenario "TOTAL_TOKENS column
// shows the humanized per-bucket sum"。
//
// 决策(stats-bucket-tokens-column-numeric §Decision 1)：每行 TOTAL_TOKENS 列为
// humanizeTokens(Input+Output+CacheCreate+CacheRead);TOTAL 行同样 humanize 总和;
// 零值桶天然 humanize 成 "0",不再需要 · / ─ / 柱占位。
func TestFormatBucketTable_TokensSumColumn(t *testing.T) {
	rows := []BucketRow{
		// sum = 1000+200+150+650 = 2000 → humanizeTokens(2000) = "2.00K"
		{Label: "09-24", Sessions: 1, InputTokens: 1000, OutputTokens: 200, CacheCreate: 150, CacheRead: 650},
		// sum = 500 → "500"
		{Label: "09-25", Sessions: 2, InputTokens: 500, OutputTokens: 0, CacheCreate: 0, CacheRead: 0},
		// sum = 0 → "0"
		{Label: "09-26", Sessions: 0},
	}
	// total = 1500+200+150+650 = 2500 → humanizeTokens(2500) = "2.50K"
	totals := &BucketRow{Sessions: 3, InputTokens: 1500, OutputTokens: 200, CacheCreate: 150, CacheRead: 650}
	var sb strings.Builder
	FormatBucketTable(&sb, rows, totals)

	out := sb.String()
	// 每行 TOTAL_TOKENS 列断言(humanize 输出形态)
	wantDataTokens := []string{"2.00K", "500", "0"}
	for _, want := range wantDataTokens {
		if !strings.Contains(out, want) {
			t.Errorf("output should contain %q (humanize Tokens sum), got:\n%s", want, out)
		}
	}
	// 找 TOTAL 行,断言最后一个 field(经 tabwriter pad 后)等于 "2.50K"
	var totalLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "TOTAL ") {
			totalLine = l
			break
		}
	}
	if totalLine == "" {
		t.Fatalf("TOTAL row not found in output:\n%s", out)
	}
	totalFields := strings.Fields(totalLine)
	if got := totalFields[len(totalFields)-1]; got != "2.50K" {
		t.Errorf("TOTAL row TOTAL_TOKENS column: want %q, got %q", "2.50K", got)
	}

	// 整表不应出现旧版的柱字符、半块字符、·、⏳、─
	// 旧版柱子集全替换为 humanize;无效字符必须消失
	forbidden := []string{"█", "▏", "▎", "▍", "▌", "▋", "▊", "▉", "·", "⏳", "─", " (total)"}
	for _, f := range forbidden {
		if strings.Contains(out, f) {
			t.Errorf("output should NOT contain %q (removed bar glyphs), got:\n%s", f, out)
		}
	}
}

// TestFormatBucketTable_TotalSeparatorRemoved 覆盖 spec scenario "TOTAL row shows
// humanized aggregates without any separator characters"。
//
// 旧版 TOTAL 行 TOTAL_TOKENS 列是 20 个 ─ + " (total)";新版本改为 humanizeTokens 总和,
// 不再有任何 box-drawing / middle-dot / hourglass 字符。
func TestFormatBucketTable_TotalSeparatorRemoved(t *testing.T) {
	rows := []BucketRow{
		{Label: "09-24", Sessions: 1, InputTokens: 100, OutputTokens: 50, CacheCreate: 0, CacheRead: 0},
	}
	// total tokens = 150 → humanizeTokens(150) = "150"
	totals := &BucketRow{Sessions: 1, InputTokens: 100, OutputTokens: 50, CacheCreate: 0, CacheRead: 0}
	var sb strings.Builder
	FormatBucketTable(&sb, rows, totals)

	out := sb.String()
	// 旧版的水平分隔符 "──────────────────── (total)" 必须消失
	wantOldSep := strings.Repeat("─", 20) + " (total)"
	if strings.Contains(out, wantOldSep) {
		t.Errorf("TOTAL row should NO LONGER contain old %q separator, got:\n%s", wantOldSep, out)
	}
	// TOTAL 行 TOTAL_TOKENS 列应等于 humanizeTokens(150) = "150"
	// tabwriter Flush 后字段间已被 pad 为空格,strings.Fields 按任意空白切。
	lines := strings.Split(out, "\n")
	var totalLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "TOTAL ") {
			totalLine = l
			break
		}
	}
	if totalLine == "" {
		t.Fatalf("TOTAL row not found in output:\n%s", out)
	}
	fields := strings.Fields(totalLine)
	wantTokens := humanizeTokens(150)
	if got := fields[len(fields)-1]; got != wantTokens {
		t.Errorf("TOTAL row TOTAL_TOKENS column: want %q, got %q (full line=%q)", wantTokens, got, totalLine)
	}
}

// TestFormatBucketTable_HourlyNote 覆盖 hourly 模式表头注 "(* = 当前小时,数据仍在累计)"。
func TestFormatBucketTable_HourlyNote(t *testing.T) {
	rows := []BucketRow{
		{Label: "17:00*", IsCurrent: true, Sessions: 1},
	}
	var sb strings.Builder
	FormatBucketTable(&sb, rows, nil)

	out := sb.String()
	if !strings.Contains(out, "(* = 当前小时,数据仍在累计)") {
		t.Errorf("hourly output should contain partial note, got:\n%s", out)
	}
}

// TestBucket_JSONFieldBucket 覆盖 spec scenario 4 必填字段"bucket (label string)"。
//
// Label 现在 JSON 序列化为 `bucket` 字段,partial 小时桶 label 末尾 "*" 保留;
// BucketTotals 的 Label 为空 → omitempty 跳过 bucket 字段(spec bucket_totals
// 不含 bucket 字段)。
func TestBucket_JSONFieldBucket(t *testing.T) {
	dbPath := setupBucketTestDB(t)
	rows, err := queryBucketSummaryAt(dbPath, "hour", fixedTodayStartMs(), fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}

	// daily rows: marshal 一行验证 "bucket" 字段存在且等于 Label
	dailyRows := []BucketRow{
		{Label: "09-24", BucketISO: "2026-09-24", BucketUnixMs: 1758672000000, Sessions: 1, InputTokens: 100},
	}
	dailyJSON, err := json.Marshal(dailyRows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dailyJSON), `"bucket":"09-24"`) {
		t.Errorf("daily bucket JSON missing 'bucket' field, got: %s", dailyJSON)
	}

	// hourly rows: hourly 模式 17:00* partial marker 应保留
	if rows[len(rows)-1].Label != "17:00*" {
		t.Fatalf("setup mismatch: last row label %q", rows[len(rows)-1].Label)
	}
	hourlyJSON, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hourlyJSON), `"bucket":"17:00*"`) {
		t.Errorf("hourly bucket JSON should preserve partial '*' suffix in 'bucket' field, got: %s", hourlyJSON)
	}

	// BucketTotals:Label 空 → omitempty,JSON 不应含 bucket 字段
	totals := sumBuckets(rows)
	totalsJSON, err := json.Marshal(&totals)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(totalsJSON), `"bucket":`) {
		t.Errorf("bucket_totals JSON should not contain 'bucket' field (omitempty), got: %s", totalsJSON)
	}
	// 但其它数值字段应保留
	if !strings.Contains(string(totalsJSON), `"sessions":`) {
		t.Errorf("bucket_totals JSON should contain 'sessions', got: %s", totalsJSON)
	}
}

// TestBucket_Hourly_CrossHour_TokensOnCaptureHour 覆盖 spec §D6 双 key 策略:
//
//   - hourly kind:sessions 算 s.started_at 所在小时,tokens/cost 算 m.captured_at 所在小时
//   - 长 session 跨小时时,token 归属真实采集小时（而非 session 起始小时）
//
// mock 场景:session 在 15:30 开始,metric captured_at = 16:45 (长 session 跨小时)。
// 预期:
//   - 15:00 桶:sessions=1(但 tokens=0,因为 metric 不在该小时)
//   - 16:00 桶:sessions=0(但 tokens>0,因为 metric captured 在该小时)
func TestBucket_Hourly_CrossHour_TokensOnCaptureHour(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cross.db")
	now := fixedNow() // 2026-09-30 17:30

	store, err := collector.OpenStore(dbPath, collector.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	// session 在 15:30 开始（2 小时前,15:00 桶）
	if err := store.WriteSession(collector.Session{
		ID: "cross", StartedAt: now.Add(-2 * time.Hour),
		Provider: "anthropic", Cwd: "/tmp", Model: "claude-opus",
	}); err != nil {
		t.Fatal(err)
	}
	// metric captured_at 显式给 16:45（绕开 WithClock 默认 now(),让 captured_at 与 started_at 跨小时）
	if err := store.WriteMetric(collector.Metric{
		SessionID: "cross", Name: "claude_code.token.usage",
		Value: 100, Labels: map[string]string{"type": "input"},
		CapturedAt: now.Add(-45 * time.Minute), // 16:45
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// mock now 仍是 17:30,query hourly 视角
	rows, err := queryBucketSummaryAt(dbPath, "hour", startOfDayMillis(now), now)
	if err != nil {
		t.Fatal(err)
	}

	// 找 15:00 与 16:00 桶
	var hour15, hour16 *BucketRow
	for i := range rows {
		switch rows[i].Label[:5] {
		case "15:00":
			hour15 = &rows[i]
		case "16:00":
			hour16 = &rows[i]
		}
	}
	if hour15 == nil || hour16 == nil {
		t.Fatal("15:00 or 16:00 bucket missing")
	}
	// 期望:15:00 桶 sessions=1(因 s.started_at 在 15:30),tokens=0(metric captured 在 16:00 之后)
	if hour15.Sessions != 1 {
		t.Errorf("15:00 sessions: want 1, got %d", hour15.Sessions)
	}
	if hour15.InputTokens != 0 {
		t.Errorf("15:00 InputTokens: want 0 (token on capture hour, not session start hour), got %d", hour15.InputTokens)
	}
	// 16:00 桶:sessions=0(token 没有该 hour 的 session starts),tokens>0(metric captured 在这里)
	if hour16.Sessions != 0 {
		t.Errorf("16:00 sessions: want 0, got %d", hour16.Sessions)
	}
	if hour16.InputTokens != 100 {
		t.Errorf("16:00 InputTokens: want 100 (token on capture hour), got %d", hour16.InputTokens)
	}
}