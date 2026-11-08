package quota

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAssignPhases_SpreadsProviders 断言：同一刷新周期的一批额度池**均分周期
// 内的相位**——这是"8 个 provider 不挤在一起"的机制。
func TestAssignPhases_SpreadsProviders(t *testing.T) {
	const n = 8
	const intervalSec = 60

	jobs := map[string]job{}
	for i := 0; i < n; i++ {
		jobs[fmt.Sprintf("pool-%02d", i)] = job{interval: intervalSec * time.Second}
	}
	assignPhases(jobs)

	seen := map[int64]bool{}
	var phases []int64
	for pool, j := range jobs {
		sec := int64(j.phase.Seconds())
		if sec < 0 || sec >= intervalSec {
			t.Errorf("%s 相位越界: %d", pool, sec)
		}
		if seen[sec] {
			t.Errorf("相位重复: %d", sec)
		}
		seen[sec] = true
		phases = append(phases, sec)
	}

	// 均匀性：任意两个相邻相位的间隔 ≤ ceil(60/8)=8 秒
	sortInt64(phases)
	for i := 1; i < len(phases); i++ {
		if gap := phases[i] - phases[i-1]; gap > 8 {
			t.Errorf("相位间隔 %d 秒（%d → %d），应 ≤ 8", gap, phases[i-1], phases[i])
		}
	}
	// 首尾也要能接上（跨周期边界）
	if wrap := intervalSec - phases[len(phases)-1] + phases[0]; wrap > 8 {
		t.Errorf("跨周期边界的相位间隔 %d 秒，应 ≤ 8", wrap)
	}
}

// TestAssignPhases_KeepsExistingPhase 断言：已分配过相位的池**保持**自己的
// 相位（新的池不重排老的）。
func TestAssignPhases_KeepsExistingPhase(t *testing.T) {
	const intervalSec = 60
	// oldNext 的相位是 23（网格点 + 23 秒）
	oldNext := int64(1793950000/intervalSec*intervalSec + intervalSec + 23)

	jobs := map[string]job{
		"pool-old": {interval: intervalSec * time.Second, oldNext: oldNext},
		"pool-new": {interval: intervalSec * time.Second},
	}
	assignPhases(jobs)

	if got := int64(jobs["pool-old"].phase.Seconds()); got != 23 {
		t.Errorf("老池的相位应保持 23, got %d", got)
	}
	if got := int64(jobs["pool-new"].phase.Seconds()); got == 23 {
		t.Errorf("新池不该占用老池的相位 23")
	}
}

// TestNextRefreshAt_KeepsPhase 断言：next_at **递推**（不是 now+interval 重算）
// → 相位永久保持；错过周期后相位仍然保持。
func TestNextRefreshAt_KeepsPhase(t *testing.T) {
	const intervalSec = 60
	now := time.Unix(1793950000, 0)
	oldNext := int64(1793950000/intervalSec*intervalSec + intervalSec + 23)

	next := nextRefreshAt(now, intervalSec*time.Second, 0, oldNext)
	if phaseOf(next, intervalSec) != 23 {
		t.Errorf("递推后相位应仍是 23, got %d", phaseOf(next, intervalSec))
	}
	if next != oldNext+intervalSec {
		t.Errorf("next_at 应是 oldNext + interval, got %d", next)
	}

	// 错过了很多周期（serve 停了很久）→ 相位仍保持，且跳到未来
	stale := oldNext - 300
	next2 := nextRefreshAt(now, intervalSec*time.Second, 0, stale)
	if phaseOf(next2, intervalSec) != phaseOf(stale, intervalSec) {
		t.Errorf("错过周期后相位应保持, got %d want %d", phaseOf(next2, intervalSec), phaseOf(stale, intervalSec))
	}
	if next2 <= now.Unix() {
		t.Errorf("next_at 应跳到未来, got %d", next2)
	}
}

// TestNextRefreshAt_NewPoolLandsOnGrid 断言：新分配的池落在 interval 网格 +
// 相位上。
func TestNextRefreshAt_NewPoolLandsOnGrid(t *testing.T) {
	const intervalSec = 60
	now := time.Unix(1793950030, 0) // 网格内
	next := nextRefreshAt(now, intervalSec*time.Second, 15*time.Second, 0)
	if next <= now.Unix() {
		t.Fatalf("next_at 应在未来, got %d", next)
	}
	if phaseOf(next, intervalSec) != 15 {
		t.Errorf("相位应落在 15, got %d", phaseOf(next, intervalSec))
	}
}

// TestRefreshAll_MinGapBetweenRequests 断言：一轮里相邻两个请求之间**至少隔
// Configured 的间隔**（厂商侧限流的兜底）。
func TestRefreshAll_MinGapBetweenRequests(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	const gapMs = 120
	refs := []ProviderRef{}
	for i := 0; i < 3; i++ {
		refs = append(refs, ProviderRef{
			Name:   fmt.Sprintf("p%d", i),
			Config: &Config{Source: "newapi", Endpoint: srv.URL, MinRequestGap: gapMs * time.Millisecond},
			APIKey: fmt.Sprintf("sk-%d", i),
		})
	}
	st := NewStore(t.TempDir())
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(times) != 3 {
		t.Fatalf("应发 3 个请求, got %d", len(times))
	}
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		if gap < gapMs*time.Millisecond {
			t.Errorf("第 %d 与 %d 个请求间隔 %v, 应 ≥ %dms", i, i-1, gap, gapMs)
		}
	}
}

// TestRefreshAll_RateLimitBacksOff 断言：429 → 写退避 → 下一轮**不再请求**；
// 退避结束后再请求且**退避时长翻倍**；成功后**复位**。
func TestRefreshAll_RateLimitBacksOff(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	limited := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		isLimited := limited
		mu.Unlock()
		if isLimited {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, "too many requests")
			return
		}
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{{
		Name:   "p1",
		Config: &Config{Source: "newapi", Endpoint: srv.URL},
		APIKey: "sk-1",
	}}
	st := NewStore(t.TempDir())

	// 第 1 轮：被限流 → 退避
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if requests != 1 {
		t.Fatalf("应发 1 个请求, got %d", requests)
	}
	mu.Unlock()

	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	// 被限流时**不写额度条目**（只记退避状态）——额度数据是旧的或没有，
	// 不能被一次失败写成"空"。
	if _, ok := f.EntryFor("p1"); ok {
		t.Error("被限流时不该写额度条目")
	}
	pool := PoolID(srv.URL, "sk-1")
	b := f.BackoffFor(pool)
	if b.RetryAfter <= time.Now().Unix() {
		t.Errorf("应记录退避到未来, got %d", b.RetryAfter)
	}
	// 厂商 Retry-After=30 小于缺省的 60s → 取**较大者**（缺省 60s）
	if b.Seconds != 60 {
		t.Errorf("退避基数应为 60, got %d", b.Seconds)
	}

	// 第 2 轮：退避中 → **不发请求**
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if requests != 1 {
		t.Errorf("退避中不该再发请求, requests=%d", requests)
	}
	mu.Unlock()

	// 把退避时间改到过去 → 第 3 轮：又请求 → 仍被限流 → 退避翻倍
	f2, _ := st.Load()
	f2.Backoffs[pool] = Backoff{RetryAfter: time.Now().Unix() - 1, Seconds: f2.BackoffFor(pool).Seconds}
	if err := st.Save(f2); err != nil {
		t.Fatal(err)
	}
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if requests != 2 {
		t.Errorf("退避结束后应再发 1 个请求, requests=%d", requests)
	}
	mu.Unlock()
	f3, _ := st.Load()
	if got := f3.BackoffFor(pool).Seconds; got != 120 {
		t.Errorf("连续限流后退避应翻倍到 120, got %d", got)
	}

	// 厂商恢复 → 刷新成功 → 退避复位
	mu.Lock()
	limited = false
	mu.Unlock()
	f4, _ := st.Load()
	f4.Backoffs[pool] = Backoff{RetryAfter: time.Now().Unix() - 1, Seconds: f4.BackoffFor(pool).Seconds}
	if err := st.Save(f4); err != nil {
		t.Fatal(err)
	}
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	f5, _ := st.Load()
	b5 := f5.BackoffFor(pool)
	if b5.Seconds != 0 || b5.RetryAfter != 0 {
		t.Errorf("成功后退避状态应复位, got backoff=%d retryAfter=%d", b5.Seconds, b5.RetryAfter)
	}
}

// TestRefreshAll_RateLimitKeepsOldQuota 断言：被限流时**旧额度数据保留**
// （fetched_at 不变 → stale 自动生效），不是清空。
func TestRefreshAll_RateLimitKeepsOldQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	refs := []ProviderRef{{
		Name:   "p1",
		Config: &Config{Source: "newapi", Endpoint: srv.URL},
		APIKey: "sk-1",
	}}
	st := NewStore(t.TempDir())
	// 先造一条旧数据（fetched_at 是 10 分钟前 → 会触发刷新）
	f := &File{Version: 1, Entries: map[string]Entry{
		"p1": {
			Source:     "newapi",
			Pool:       "pool-1",
			FetchedAt:  time.Now().Add(-10 * time.Minute).Unix(),
			Dimensions: []Dimension{{ID: "quota", Kind: KindQuota, Unit: "USD"}},
		},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	before := f.Entries["p1"]

	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	after, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := after.Entries["p1"]
	if e.FetchedAt != before.FetchedAt {
		t.Errorf("被限流时 fetched_at 不该变（否则 stale 不生效）: %d → %d", before.FetchedAt, e.FetchedAt)
	}
	if len(e.Dimensions) != 1 {
		t.Errorf("旧额度数据不该被丢: %+v", e.Dimensions)
	}
	if after.BackoffFor(PoolID(srv.URL, "sk-1")).RetryAfter == 0 {
		t.Error("应记录退避时间")
	}
}

// TestFetchJSON_RateLimitError 断言：429 → RateLimitError（带 Retry-After）。
func TestFetchJSON_RateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "too many requests")
	}))
	defer srv.Close()

	_, _, err := fetchJSON(context.Background(), newHTTPClient(nil), srv.URL, "sk")
	if err == nil {
		t.Fatal("429 应报错")
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("应能用 errors.As 识别 RateLimitError, got %T", err)
	}
	if rl.Status != 429 {
		t.Errorf("Status = %d", rl.Status)
	}
	if rl.RetryAfter != 42*time.Second {
		t.Errorf("RetryAfter = %v, want 42s", rl.RetryAfter)
	}
}

// TestParseRetryAfter 断言 Retry-After 两种形式都能解析。
func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("120"); got != 120*time.Second {
		t.Errorf("秒数形式解析错: %v", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("空值应返回 0, got %v", got)
	}
	if got := parseRetryAfter("garbage"); got != 0 {
		t.Errorf("非法值应返回 0, got %v", got)
	}
	// HTTP-date 形式（过去的日期 → 0）
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("过去的日期应返回 0, got %v", got)
	}
}

// TestNextBackoff 断言：连续被限流 → 翻倍、封顶 30 分钟；厂商给了更大的
// Retry-After → 取它。
func TestNextBackoff(t *testing.T) {
	if got := nextBackoff(0, 0); got != time.Minute {
		t.Errorf("首次应退避 1 分钟, got %v", got)
	}
	if got := nextBackoff(60, 0); got != 2*time.Minute {
		t.Errorf("连续应翻倍到 2 分钟, got %v", got)
	}
	if got := nextBackoff(1800, 0); got != 30*time.Minute {
		t.Errorf("应封顶 30 分钟, got %v", got)
	}
	if got := nextBackoff(60, 5*time.Minute); got != 5*time.Minute {
		t.Errorf("厂商 Retry-After 更大时应取它, got %v", got)
	}
}

// TestStore_BackwardCompatible 断言：老格式数据文件能被读出（新增的三个字段
// 是零值）→ 退化成旧行为，不报错。
func TestStore_BackwardCompatible(t *testing.T) {
	st := NewStore(t.TempDir())
	// 手写一个老格式文件（没有 next_at / retry_after / backoff_seconds）
	old := `{
  "version": 1,
  "updated_at": 1793950000,
  "entries": {
    "p1": {
      "source": "minimax",
      "pool": "pool-1",
      "fetched_at": 1793950000,
      "dimensions": [{"id": "interval", "label": "5h", "kind": "window"}]
    }
  }
}`
	if err := writeFile(st.Path(), old); err != nil {
		t.Fatal(err)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatalf("老格式文件不该报错: %v", err)
	}
	e, ok := f.EntryFor("p1")
	if !ok {
		t.Fatal("条目应存在")
	}
	if e.NextAt != 0 || e.RetryAfter != 0 || e.BackoffSeconds != 0 {
		t.Errorf("新字段应为零值: %+v", e)
	}
	if len(e.Dimensions) != 1 || e.Dimensions[0].Label != "5h" {
		t.Errorf("维度应能读出: %+v", e.Dimensions)
	}
}

// TestRefreshAll_SkipsWhenNextAtInFuture 断言：next_at 在未来 → 跳过（相位
// 错开的核心判定）。
func TestRefreshAll_SkipsWhenNextAtInFuture(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{{
		Name:   "p1",
		Config: &Config{Source: "newapi", Endpoint: srv.URL},
		APIKey: "sk-1",
	}}
	st := NewStore(t.TempDir())
	// 相位未到（next_at 在未来）且记录也还新鲜 → 应跳过。
	//
	// **fetched_at 必须新**：真实系统里 next_at = 上次刷新时刻 + interval，
	// 所以"next_at 在未来"蕴含"上次刷新很近"。若 fetched_at 反而很旧
	//（真实系统不可达的状态），该池会被判为陈旧而立即刷新——那是 D2 的
	// 有意行为，见 TestRefreshAll_RefreshesStaleActivePool。
	f := &File{Version: 1, Entries: map[string]Entry{
		"p1": {
			Source:    "newapi",
			Pool:      "pool-1",
			FetchedAt: time.Now().Add(-10 * time.Second).Unix(),
			NextAt:    time.Now().Add(30 * time.Second).Unix(),
		},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Errorf("next_at 在未来 → 不该发请求, requests=%d", requests)
	}
}

// sortInt64 就地排序（测试辅助）。
func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// writeFile 写文件（测试辅助）。
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0600)
}

// TestRefreshAll_BackoffIsPerPool 断言：退避按**额度池**记录——provider 换了
// key（= 换了池）后，不该被老退避拦住。
//
// 这正是"限流是针对 key 的，额度也是按 key 分的"在实现上的落地。
func TestRefreshAll_BackoffIsPerPool(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	st := NewStore(t.TempDir())

	// 用 key-A → 池 A → 被限流 → 池 A 进入退避
	refA := ProviderRef{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "key-A"}
	if err := RefreshAll(context.Background(), []ProviderRef{refA}, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if requests != 1 {
		t.Fatalf("应发 1 个请求, got %d", requests)
	}
	mu.Unlock()
	if f, _ := st.Load(); f.BackoffFor(PoolID(srv.URL, "key-A")).RetryAfter == 0 {
		t.Fatal("池 A 应进入退避")
	}

	// 同一个 provider 名，但换成 key-B（= 池 B）→ 不该被池 A 的退避拦住
	refB := ProviderRef{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "key-B"}
	if err := RefreshAll(context.Background(), []ProviderRef{refB}, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Errorf("换 key（换池）后不该被老退避拦住, requests=%d（期望 2）", requests)
	}
}

// TestStore_MigratesLegacyBackoff 断言：v0.20.1 写在**条目**里的退避状态，
// 读出时并入对应池的 backoffs（不丢退避），并清掉条目里的旧字段。
func TestStore_MigratesLegacyBackoff(t *testing.T) {
	st := NewStore(t.TempDir())
	old := `{
  "version": 1,
  "updated_at": 1793950000,
  "entries": {
    "p1": {
      "source": "newapi",
      "pool": "pool-abc",
      "fetched_at": 1793950000,
      "retry_after": 1793959999,
      "backoff_seconds": 120
    }
  }
}`
	if err := writeFile(st.Path(), old); err != nil {
		t.Fatal(err)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatalf("老文件不该报错: %v", err)
	}
	b := f.BackoffFor("pool-abc")
	if b.RetryAfter != 1793959999 {
		t.Errorf("老退避应迁移到对应池: got %+v", b)
	}
	if b.Seconds != 120 {
		t.Errorf("退避基数应迁移: got %d", b.Seconds)
	}
	e, _ := f.EntryFor("p1")
	if e.RetryAfter != 0 || e.BackoffSeconds != 0 {
		t.Errorf("条目里的旧退避字段应被清掉: %+v", e)
	}
}

// TestRefreshAll_InactiveProviderKeepsEntry 断言：**不活跃的 provider 本轮不刷，
// 但它的条目保留**（"这轮没轮到它" ≠ "provider 被删了"）。
//
// 这是真机验证时抓到的 bug：Prune 的 live 集合误用了"过滤后的 refs" →
// 不活跃的 provider 的额度条目被当成残留清掉 → 用户 `ccl quota --provider X`
// 变成"没有记录"。
func TestRefreshAll_InactiveProviderKeepsEntry(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{
		{Name: "active", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-a"},
		{Name: "idle", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-b"},
	}
	st := NewStore(t.TempDir())
	// 先给两个都造条目（idle 的 fetched_at 很旧 → 若被刷就会被更新）
	if err := st.Update("idle", "newapi", "pool-b", []Dimension{{ID: "quota", Kind: KindQuota}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Update("active", "newapi", "pool-a", []Dimension{{ID: "quota", Kind: KindQuota}}); err != nil {
		t.Fatal(err)
	}
	// 两个条目的 fetched_at 都改旧 → 唯一的变量是"活跃与否"
	f0, _ := st.Load()
	for _, n := range []string{"idle", "active"} {
		e := f0.Entries[n]
		e.FetchedAt = time.Now().Add(-10 * time.Minute).Unix()
		f0.Entries[n] = e
	}
	if err := st.Save(f0); err != nil {
		t.Fatal(err)
	}

	// 只有 active 在活跃名单里
	active := map[string]bool{"active": true}
	if err := RefreshAll(context.Background(), refs, st, nil, active, 0); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 1 {
		t.Errorf("只该查活跃的那个, requests=%d", got)
	}

	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.EntryFor("idle"); !ok {
		t.Error("不活跃的 provider 的条目不该被删（这轮没刷它 ≠ provider 被删了）")
	}
	if _, ok := f.EntryFor("active"); !ok {
		t.Error("活跃的 provider 应有条目")
	}
}

// TestRefreshAll_NoneActiveKeepsAllEntries 断言：一个都不活跃 → 一个请求都不发，
// 但**所有条目都保留**。
func TestRefreshAll_NoneActiveKeepsAllEntries(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		fmt.Fprint(w, `{"code":true,"data":{}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{
		{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-1"},
		{Name: "p2", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-2"},
	}
	st := NewStore(t.TempDir())
	for _, n := range []string{"p1", "p2"} {
		if err := st.Update(n, "newapi", "pool-"+n, []Dimension{{ID: "quota", Kind: KindQuota}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{}, 0); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 0 {
		t.Errorf("没人活跃时不该发请求, requests=%d", got)
	}
	f, _ := st.Load()
	if len(f.Entries) != 2 {
		t.Errorf("条目不该被删: %+v", f.Entries)
	}
}

// TestRefreshOne_WritesWholePool 断言：手动刷新一个 provider 时，
// **同池的其他 provider 也一起更新**（同一把 key 的数据必须同源）。
func TestRefreshOne_WritesWholePool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":100,"total_used":1,"total_available":99}}`)
	}))
	defer srv.Close()

	st := NewStore(t.TempDir())
	// 两个 provider 共用同一把 key → 同一个池
	refA := ProviderRef{Name: "a", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "same-key"}
	refB := ProviderRef{Name: "b", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "same-key"}
	pool := PoolID(srv.URL, "same-key")
	for _, r := range []ProviderRef{refA, refB} {
		if err := st.Update(r.Name, "newapi", pool, []Dimension{{ID: "quota", Kind: KindQuota}}); err != nil {
			t.Fatal(err)
		}
	}
	if refA.APIKey != refB.APIKey {
		t.Fatal("两个 provider 应共用同一把 key（同一池）")
	}

	if _, err := RefreshOne(context.Background(), refA, st); err != nil {
		t.Fatal(err)
	}

	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	ea, ok := f.EntryFor("a")
	if !ok {
		t.Fatal("a 应有条目")
	}
	eb, ok := f.EntryFor("b")
	if !ok {
		t.Fatal("b 应有条目")
	}
	if eb.FetchedAt != ea.FetchedAt {
		t.Errorf("同池的两个 provider 应同时更新: a=%d b=%d", ea.FetchedAt, eb.FetchedAt)
	}
	if len(eb.Dimensions) != 1 {
		t.Errorf("同池的 b 也该拿到新数据: %+v", eb.Dimensions)
	}
}

// TestRefreshAll_LogsRoundSummary 断言：每轮刷新打一行**汇总**（四个分类计数），
// 用于诊断"这一轮到底查了几个、是不是被限流了"。
func TestRefreshAll_LogsRoundSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{
		{Name: "a", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-a"},
		{Name: "b", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-b"},
	}
	st := NewStore(t.TempDir())

	var logged []string
	err := RefreshAll(context.Background(), refs, st, func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}, map[string]bool{"a": true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(logged, "\n")
	if !strings.Contains(joined, "查了 1") {
		t.Errorf("汇总行应含「查了 1」（只有 a 活跃）, got:\n%s", joined)
	}
	if !strings.Contains(joined, "不活跃跳过 1") {
		t.Errorf("汇总行应含「不活跃跳过 1」, got:\n%s", joined)
	}
}
