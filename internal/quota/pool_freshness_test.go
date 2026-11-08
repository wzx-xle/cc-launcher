package quota

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// quotaStub 是一个计数的额度接口桩。
//
// 计数是**并发安全**的：刷新是串行的，但测试可能并发观察。
type quotaStub struct {
	mu       sync.Mutex
	requests int
	srv      *httptest.Server
}

// newQuotaStub 起一个返回固定额度数据的桩。
func newQuotaStub(t *testing.T) *quotaStub {
	t.Helper()
	s := &quotaStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests++
		s.mu.Unlock()
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// count 返回已收到的请求数。
func (s *quotaStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// ref 造一个指向桩的 provider 引用（key 相同 → 同一个额度池）。
func (s *quotaStub) ref(name, key string) ProviderRef {
	return ProviderRef{
		Name:   name,
		Config: &Config{Source: "newapi", Endpoint: s.srv.URL},
		APIKey: key,
	}
}

// TestRefreshAll_PoolMembersShareOneRecord 断言：**同额度池**的 provider 记录
// 的是**同一时刻**的数据。
//
// 这正是真机抓到过的 bug：active 过滤做在 provider 级 → 不活跃的成员在赋
// poolOf 之前就 continue → 写回时按 poolOf 匹配不上 → 同池两个 provider 显示
// 不同时刻的数据（实测差 38 分钟）。
func TestRefreshAll_PoolMembersShareOneRecord(t *testing.T) {
	stub := newQuotaStub(t)
	// p1 与 p2 同 endpoint + 同 key → 同池。
	refs := []ProviderRef{stub.ref("p1", "sk-same"), stub.ref("p2", "sk-same")}

	st := NewStore(t.TempDir())
	// 只有 p1 活跃。
	active := map[string]bool{"p1": true}

	if err := RefreshAll(context.Background(), refs, st, nil, active, 0); err != nil {
		t.Fatal(err)
	}

	if got := stub.count(); got != 1 {
		t.Errorf("同池只该发 1 个请求, got %d", got)
	}

	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	e1, ok1 := f.EntryFor("p1")
	e2, ok2 := f.EntryFor("p2")
	if !ok1 || !ok2 {
		t.Fatalf("同池两个 provider 都该有记录: p1=%v p2=%v", ok1, ok2)
	}
	if e1.FetchedAt != e2.FetchedAt {
		t.Errorf("同池必须记录同一时刻: p1=%d p2=%d", e1.FetchedAt, e2.FetchedAt)
	}
	if e1.NextAt != e2.NextAt {
		t.Errorf("同池的下次刷新时刻必须一致: p1=%d p2=%d", e1.NextAt, e2.NextAt)
	}
	if len(e2.Dimensions) == 0 {
		t.Error("不活跃的同池成员也必须拿到数据")
	}
}

// TestRefreshAll_InactivePoolRefreshedWhenStale 断言：不活跃的池，记录已旧于
// 保鲜阈值时**仍会被刷新**（兜底保鲜）。
func TestRefreshAll_InactivePoolRefreshedWhenStale(t *testing.T) {
	stub := newQuotaStub(t)
	refs := []ProviderRef{stub.ref("p1", "sk-1")}

	st := NewStore(t.TempDir())
	// 20 分钟前抓的 → 超过 10 分钟保鲜阈值。
	f := &File{Version: 1, Entries: map[string]Entry{
		"p1": {Source: "newapi", Pool: "pool-1", FetchedAt: time.Now().Add(-20 * time.Minute).Unix()},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	// 没有活跃的 provider（active 非 nil 且为空名单）。
	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{}, DefaultFreshnessThreshold); err != nil {
		t.Fatal(err)
	}
	if got := stub.count(); got != 1 {
		t.Errorf("记录超过保鲜阈值的池应被刷新, requests=%d", got)
	}
}

// TestRefreshAll_InactivePoolNotRefreshedBeforeThreshold 断言：不活跃的池，记录
// 还没旧到保鲜阈值时**不被刷**。
//
// **这条是保鲜规则的关键防线**：若沿用 refreshInterval（默认 60 秒）判到期，
// 不活跃池每 60 秒就会被刷一次 → 保鲜规则失效 → 退化成"全刷" → 429 的根因。
func TestRefreshAll_InactivePoolNotRefreshedBeforeThreshold(t *testing.T) {
	stub := newQuotaStub(t)
	refs := []ProviderRef{stub.ref("p1", "sk-1")}

	st := NewStore(t.TempDir())
	// 5 分钟前抓的 → 还没到 10 分钟阈值。
	f := &File{Version: 1, Entries: map[string]Entry{
		"p1": {Source: "newapi", Pool: "pool-1", FetchedAt: time.Now().Add(-5 * time.Minute).Unix()},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	// 连跑三轮：每一轮都不该发请求。
	for i := 0; i < 3; i++ {
		if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{}, DefaultFreshnessThreshold); err != nil {
			t.Fatal(err)
		}
	}
	if got := stub.count(); got != 0 {
		t.Errorf("未到保鲜阈值的不活跃池不该被刷, requests=%d", got)
	}
}

// TestRefreshAll_RefreshesStaleActivePool 断言：活跃的池，记录已经陈旧时
// **立即被刷**（不等 next_at）。
//
// 这是"用户切到一个很久没用的 provider 时当场拿到新数据"的实现基础。
func TestRefreshAll_RefreshesStaleActivePool(t *testing.T) {
	stub := newQuotaStub(t)
	refs := []ProviderRef{stub.ref("p1", "sk-1")}

	st := NewStore(t.TempDir())
	// 30 分钟前抓的（陈旧），但 next_at 在 30 秒后（相位还没到）。
	f := &File{Version: 1, Entries: map[string]Entry{
		"p1": {
			Source:    "newapi",
			Pool:      "pool-1",
			FetchedAt: time.Now().Add(-30 * time.Minute).Unix(),
			NextAt:    time.Now().Add(30 * time.Second).Unix(),
		},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{"p1": true}, 0); err != nil {
		t.Fatal(err)
	}
	if got := stub.count(); got != 1 {
		t.Errorf("记录陈旧的活跃池应立即被刷（不等 next_at）, requests=%d", got)
	}
}

// TestRefreshAll_FreshnessThresholdIgnoresLongInterval 断言：保鲜阈值**不被
// refreshInterval 拉长**。
//
// 回归：用户把 refreshInterval 配成 900 秒（15 分钟）时，若用
// max(next_at, fetched_at+freshness) 判到期，next_at（= fetched_at+900）会盖过
// 保鲜阈值 → 实际保鲜 15 分钟，违反"数据最多旧 10 分钟"。
func TestRefreshAll_FreshnessThresholdIgnoresLongInterval(t *testing.T) {
	stub := newQuotaStub(t)
	refs := []ProviderRef{{
		Name:   "p1",
		Config: &Config{Source: "newapi", Endpoint: stub.srv.URL, RefreshInterval: 900 * time.Second},
		APIKey: "sk-1",
	}}

	st := NewStore(t.TempDir())
	// 12 分钟前抓的 → 超过 10 分钟保鲜阈值（但没到 15 分钟的 refreshInterval）。
	f := &File{Version: 1, Entries: map[string]Entry{
		"p1": {
			Source:    "newapi",
			Pool:      "pool-1",
			FetchedAt: time.Now().Add(-12 * time.Minute).Unix(),
			NextAt:    time.Now().Add(-11 * time.Minute).Unix(),
		},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{}, DefaultFreshnessThreshold); err != nil {
		t.Fatal(err)
	}
	if got := stub.count(); got != 1 {
		t.Errorf("超过保鲜阈值就该刷（不被 refreshInterval 拉长）, requests=%d", got)
	}
}

// TestRefreshAll_BackoffBeatsFreshness 断言：**退避中的池不被刷**，哪怕它已经
// 超过保鲜阈值。
//
// 限流保护优先于保鲜 —— 否则退避会被保鲜规则架空。
func TestRefreshAll_BackoffBeatsFreshness(t *testing.T) {
	stub := newQuotaStub(t)
	refs := []ProviderRef{stub.ref("p1", "sk-1")}

	st := NewStore(t.TempDir())
	pool := PoolID(stub.srv.URL, "sk-1")
	f := &File{
		Version: 1,
		Entries: map[string]Entry{
			"p1": {Source: "newapi", Pool: pool, FetchedAt: time.Now().Add(-20 * time.Minute).Unix()},
		},
		Backoffs: map[string]Backoff{
			pool: {RetryAfter: time.Now().Add(5 * time.Minute).Unix(), Seconds: 300},
		},
	}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{}, DefaultFreshnessThreshold); err != nil {
		t.Fatal(err)
	}
	if got := stub.count(); got != 0 {
		t.Errorf("退避中的池不该被刷, requests=%d", got)
	}
}

// TestRefreshAll_InactivePoolWithoutRecordIsNotQueried 断言：**从没查过**的池
// 不因为保鲜而被查（没人看的、连数据都没有的池，不值得发请求）。
func TestRefreshAll_InactivePoolWithoutRecordIsNotQueried(t *testing.T) {
	stub := newQuotaStub(t)
	refs := []ProviderRef{stub.ref("p1", "sk-1")}

	st := NewStore(t.TempDir()) // 空文件 → 没有任何记录

	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{}, DefaultFreshnessThreshold); err != nil {
		t.Fatal(err)
	}
	if got := stub.count(); got != 0 {
		t.Errorf("没有记录的不活跃池不该被查, requests=%d", got)
	}
}

// TestRefreshAll_ActivePoolWithoutRecordIsQueried 断言：**从没查过**但活跃的池
// 会被立即查（第一次用它的 provider 必须能拿到数据）。
func TestRefreshAll_ActivePoolWithoutRecordIsQueried(t *testing.T) {
	stub := newQuotaStub(t)
	refs := []ProviderRef{stub.ref("p1", "sk-1")}

	st := NewStore(t.TempDir()) // 空文件 → 没有任何记录

	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{"p1": true}, DefaultFreshnessThreshold); err != nil {
		t.Fatal(err)
	}
	if got := stub.count(); got != 1 {
		t.Errorf("没有记录的活跃池应被立即查, requests=%d", got)
	}
}

// TestRefreshAll_ResultCountsPools 断言：RefreshResult 的计数单位是**额度池**。
func TestRefreshAll_ResultCountsPools(t *testing.T) {
	stub := newQuotaStub(t)
	// 两个同池成员 + 一个异池成员。
	refs := []ProviderRef{
		stub.ref("p1", "sk-same"),
		stub.ref("p2", "sk-same"),
		stub.ref("p3", "sk-other"),
	}

	st := NewStore(t.TempDir())
	// 记录是 2 分钟前抓的 → 超过 60 秒刷新周期 → 活跃池到期。
	f := &File{Version: 1, Entries: map[string]Entry{
		"p1": {Source: "newapi", Pool: "x", FetchedAt: time.Now().Add(-2 * time.Minute).Unix()},
		"p2": {Source: "newapi", Pool: "x", FetchedAt: time.Now().Add(-2 * time.Minute).Unix()},
		"p3": {Source: "newapi", Pool: "y", FetchedAt: time.Now().Add(-2 * time.Minute).Unix()},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	var logged []string
	if err := RefreshAll(context.Background(), refs, st, func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}, map[string]bool{"p1": true}, 0); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, l := range logged {
		joined += l + "\n"
	}
	// 2 个池（不是 3 个 provider）：同池的 p1/p2 合成一个活跃池，p3 不活跃。
	if !strings.Contains(joined, "查了 1") {
		t.Errorf("汇总行应含「查了 1」（2 个池里只有 1 个活跃）, got:\n%s", joined)
	}
	if !strings.Contains(joined, "不活跃跳过 1") {
		t.Errorf("汇总行应含「不活跃跳过 1」（按池计，不是按 provider）, got:\n%s", joined)
	}
	if !strings.Contains(joined, "（池）") {
		t.Errorf("汇总行应标注计数单位是池, got:\n%s", joined)
	}
}
