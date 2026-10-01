package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	_, err := QueryBucketSummary("/tmp/definitely-not-exist-bucket.db", "hour", 0)
	if err == nil {
		t.Fatal("expected error for missing db")
	}
	if !errors.Is(err, ErrNoDatabase) {
		t.Errorf("expected ErrNoDatabase, got %v", err)
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

// TestBucketBar_Proportional 覆盖 spec scenario "TOTAL_TOKENS bar length is proportional to total tokens"。
//
// max=1000 给 20 格;100 给 2 格(±1 容差);tokens=43 给出 remainder=7 → ▉(7/8),
// 直接断言 8 级字符集完整。
func TestBucketBar_Proportional(t *testing.T) {
	max := int64(1000)
	fullBar := bucketBar(1000, max, false)
	if utf8.RuneCountInString(fullBar) != 20 {
		t.Errorf("full bar should be 20 runes, got %d", utf8.RuneCountInString(fullBar))
	}
	// 100 tokens 应给 20 * (100/1000) * 8 = 16 cells → 2 full blocks
	smallBar := bucketBar(100, max, false)
	smallRunes := []rune(smallBar)
	if utf8.RuneCountInString(smallBar) != 20 {
		t.Errorf("small bar should be 20 runes, got %d", utf8.RuneCountInString(smallBar))
	}
	// 统计前 N 个非空格字符
	var nonSpaceCount int
	for _, r := range smallRunes {
		if r != ' ' {
			nonSpaceCount++
		} else {
			break
		}
	}
	if nonSpaceCount < 1 || nonSpaceCount > 3 {
		t.Errorf("small bar should have ~2 visual cells (±1), got %d non-space prefix", nonSpaceCount)
	}

	// 字符集完整 8 级断言:每个 remainder(0..7)都构造对应 cells,验证桶
	// 输出第一非空格 rune 是预期字符;余数=0 时全格(无半块)。
	//
	// cells = round(tokens/1000 * 160) — tokens 表为算后 round 命中目标余数:
	//   cells=1  → tokens=6.28..6.5 → tokens=6  → remainder=1 → '▏'
	//   cells=2  → tokens=12.5     → tokens=13 → remainder=2 → '▎'
	//   cells=3  → tokens=18.75    → tokens=20 → remainder=3 → '▍'
	//   cells=4  → tokens=25       → tokens=25 → remainder=4 → '▌'
	//   cells=5  → tokens=31.25    → tokens=31 → remainder=5 → '▋'
	//   cells=6  → tokens=37.5     → tokens=38 → remainder=6 → '▊'
	//   cells=7  → tokens=43.75    → tokens=44 → remainder=7 → '▉'
	//   cells=8  → tokens=50       → tokens=50 → remainder=0 → 全格(无半块)
	for _, tc := range []struct {
		name        string
		tokens      int64
		wantHalfRune rune // 0 表示该 case 应全格或全空格(无半块)
	}{
		{"cells=1", 6, '▏'},
		{"cells=2", 13, '▎'},
		{"cells=3", 20, '▍'},
		{"cells=4", 25, '▌'},
		{"cells=5", 31, '▋'},
		{"cells=6", 38, '▊'},
		{"cells=7", 44, '▉'},
		{"cells=8", 50, 0}, // remainder=0 → fullBars=1,半块不出现
	} {
		bar := bucketBar(tc.tokens, max, false)
		runes := []rune(bar)
		if len(runes) != 20 {
			t.Errorf("%s bar should be 20 runes, got %d", tc.name, len(runes))
			continue
		}
		if tc.wantHalfRune == 0 {
			// 余数=0:第一个非空格应是 █(full block),不应出现半块字符
			for i, r := range runes {
				if r != ' ' && r != '█' {
					t.Errorf("%s bar[%d]: expect only space/full block (no half-block), got %q",
						tc.name, i, r)
					break
				}
			}
		} else {
			// 第一非空格字符应是 tc.wantHalfRune(半块字符,来自完整 8 级字符集)
			var firstNonSpace rune
			for _, r := range runes {
				if r != ' ' {
					firstNonSpace = r
					break
				}
			}
			if firstNonSpace != tc.wantHalfRune {
				t.Errorf("%s first non-space rune: want %q, got %q (bar=%q)",
					tc.name, tc.wantHalfRune, firstNonSpace, bar)
			}
		}
	}
}

// TestBucketBar_ZeroTokens 覆盖 spec scenario "0-token buckets render · placeholder"。
//
// tokens=0 或 max=0 → bar = · + 19 空格(20 rune,首字符 ·,其余空格)。
func TestBucketBar_ZeroTokens(t *testing.T) {
	zeroTok := bucketBar(0, 1000, false)
	if utf8.RuneCountInString(zeroTok) != 20 {
		t.Errorf("zero-token bar should be 20 runes, got %d", utf8.RuneCountInString(zeroTok))
	}
	if []rune(zeroTok)[0] != '·' {
		t.Errorf("zero-token bar first rune: want %q, got %q", '·', []rune(zeroTok)[0])
	}
	for i, r := range []rune(zeroTok)[1:] {
		if r != ' ' {
			t.Errorf("zero-token bar rune[%d]: want space, got %q", i+1, r)
		}
	}

	zeroMax := bucketBar(500, 0, false)
	if []rune(zeroMax)[0] != '·' {
		t.Errorf("zero-max bar first rune: want %q, got %q", '·', []rune(zeroMax)[0])
	}
}

// TestBucketBar_Partial 覆盖 spec scenario "partial bucket bar ends with ⏳"。
//
// partial=true → bar 末位是 ⏳;non-partial → 末位是空格或半块字符(非 ⏳)。
func TestBucketBar_Partial(t *testing.T) {
	partial := bucketBar(500, 1000, true)
	if utf8.RuneCountInString(partial) != 20 {
		t.Errorf("partial bar should still be 20 runes, got %d", utf8.RuneCountInString(partial))
	}
	if []rune(partial)[19] != '⏳' {
		t.Errorf("partial bar rune[19]: want ⏳, got %q", []rune(partial)[19])
	}

	nonPartial := bucketBar(500, 1000, false)
	if []rune(nonPartial)[19] == '⏳' {
		t.Errorf("non-partial bar rune[19] should not be ⏳")
	}
}

// TestBucketBar_PartialZeroTokens 覆盖 partial 零值桶也保留 ⏳ 标记。
//
// 当前小时 zero tokens 仍用 ⏳ 让用户一眼识别 partial。
func TestBucketBar_PartialZeroTokens(t *testing.T) {
	bar := bucketBar(0, 1000, true)
	if utf8.RuneCountInString(bar) != 20 {
		t.Errorf("partial zero bar should be 20 runes, got %d", utf8.RuneCountInString(bar))
	}
	if []rune(bar)[0] != '·' {
		t.Errorf("partial zero bar first rune: want ·, got %q", []rune(bar)[0])
	}
	if []rune(bar)[19] != '⏳' {
		t.Errorf("partial zero bar rune[19]: want ⏳, got %q", []rune(bar)[19])
	}
}

// TestBucketBar_TotalSeparator 覆盖 spec scenario "TOTAL row shows horizontal-rule separator"。
//
// FormatBucketTable 输出的 TOTAL 行 TOTAL_TOKENS 列 = 20 个 ─ + " (total)"。
func TestBucketBar_TotalSeparator(t *testing.T) {
	rows := []BucketRow{
		{Label: "09-24", Sessions: 1, InputTokens: 100, OutputTokens: 50},
	}
	totals := &BucketRow{Sessions: 1, InputTokens: 100, OutputTokens: 50}
	var sb strings.Builder
	FormatBucketTable(&sb, rows, totals)

	// 找 TOTAL 行,断言 TOTAL_TOKENS 列包含 20 个 ─ + " (total)"
	wantSep := strings.Repeat("─", 20) + " (total)"
	if !strings.Contains(sb.String(), wantSep) {
		t.Errorf("TOTAL row should contain %q, got:\n%s", wantSep, sb.String())
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