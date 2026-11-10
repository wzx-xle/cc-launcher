package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// vendorStub 是一个记录请求时刻的额度接口桩。
type vendorStub struct {
	srv *httptest.Server
	mu  sync.Mutex
	// times 记录每次请求的时刻（按到达顺序）。
	times []time.Time
	// status 是返回的状态码；429 时带 Retry-After。
	status int
}

func newVendorStub(t *testing.T) *vendorStub {
	t.Helper()
	st := &vendorStub{status: http.StatusOK}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.times = append(st.times, time.Now())
		status := st.status
		st.mu.Unlock()
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": {"total_quota": 100, "used_quota": 10}}`))
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func (s *vendorStub) requestTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Time, len(s.times))
	copy(out, s.times)
	return out
}

func (s *vendorStub) count() int { return len(s.requestTimes()) }

// TestVendorGap_SameEndpointSeparated 断言：**同一 endpoint** 的两个池，
// 第二次请求的发起时刻距第一次 ≥ vendor_gap。
//
// 这是 quota-per-vendor-rate-limit §D1 的守门人：厂商的限流是**时间窗口内的
// 请求数**，相位错开只保证"不在同一时刻"，管不住跨轮的相邻请求——实测两个
// 同厂商 provider 的 429 时刻只隔 8-20 秒。
func TestVendorGap_SameEndpointSeparated(t *testing.T) {
	stub := newVendorStub(t)
	st := NewStore(t.TempDir())
	throttle := NewEndpointThrottle()

	// 两个池：**同一个 endpoint**，不同的 key → 不同池，同一厂商
	//
	// **轮内间隔要显式调小**（10ms）：默认的 500ms 轮内间隔比厂商间隔还大，
	// 会把厂商间隔**掩盖**掉 → 关掉厂商间隔这条测试也不会红（没有鉴别力）。
	refs := []ProviderRef{
		{Name: "p1", Config: &Config{Source: "newapi", Endpoint: stub.srv.URL, MinRequestGap: 10 * time.Millisecond, VendorGap: 300 * time.Millisecond}, APIKey: "key-A"},
		{Name: "p2", Config: &Config{Source: "newapi", Endpoint: stub.srv.URL, MinRequestGap: 10 * time.Millisecond, VendorGap: 300 * time.Millisecond}, APIKey: "key-B"},
	}
	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{"p1": true, "p2": true}, 0, throttle); err != nil {
		t.Fatal(err)
	}

	times := stub.requestTimes()
	if len(times) != 2 {
		t.Fatalf("应发 2 个请求, got %d", len(times))
	}
	gap := times[1].Sub(times[0])
	if gap < 250*time.Millisecond {
		t.Errorf("同一 endpoint 的两个请求间隔 = %v，应 ≥ 300ms（留 50ms 余量给调度抖动）", gap)
	}
}

// TestVendorGap_DifferentEndpointNotDelayed 断言：**不同 endpoint** 的池不受
// 厂商间隔约束（间隔是按厂商分组的，不该拖累别的厂商）。
func TestVendorGap_DifferentEndpointNotDelayed(t *testing.T) {
	stubA := newVendorStub(t)
	stubB := newVendorStub(t)
	st := NewStore(t.TempDir())
	throttle := NewEndpointThrottle()

	refs := []ProviderRef{
		{Name: "p1", Config: &Config{Source: "newapi", Endpoint: stubA.srv.URL, MinRequestGap: 10 * time.Millisecond, VendorGap: 5 * time.Second}, APIKey: "key-A"},
		{Name: "p2", Config: &Config{Source: "newapi", Endpoint: stubB.srv.URL, MinRequestGap: 10 * time.Millisecond, VendorGap: 5 * time.Second}, APIKey: "key-B"},
	}
	start := time.Now()
	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{"p1": true, "p2": true}, 0, throttle); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("不同 endpoint 不该被厂商间隔拖慢，耗时 %v（上限 2s）", elapsed)
	}
	if stubA.count() != 1 || stubB.count() != 1 {
		t.Errorf("两个厂商各应发 1 个请求, got %d / %d", stubA.count(), stubB.count())
	}
}

// TestVendorGap_AcrossRounds 断言厂商间隔**跨轮**生效（节流器跨轮复用）。
//
// 这是 daemon 的真实形态：节流器由常驻进程持有，"上一轮末尾的请求"与"下一轮
// 开头的请求"之间也受约束。
func TestVendorGap_AcrossRounds(t *testing.T) {
	stub := newVendorStub(t)
	st := NewStore(t.TempDir())
	throttle := NewEndpointThrottle()

	refA := ProviderRef{Name: "p1", Config: &Config{Source: "newapi", Endpoint: stub.srv.URL, VendorGap: 300 * time.Millisecond}, APIKey: "key-A"}
	refB := ProviderRef{Name: "p2", Config: &Config{Source: "newapi", Endpoint: stub.srv.URL, VendorGap: 300 * time.Millisecond}, APIKey: "key-B"}

	// 第 1 轮：只刷 p1
	if err := RefreshAll(context.Background(), []ProviderRef{refA}, st, nil, map[string]bool{"p1": true}, 0, throttle); err != nil {
		t.Fatal(err)
	}
	// 第 2 轮：只刷 p2（**另一轮**）→ 仍应被厂商间隔隔开
	if err := RefreshAll(context.Background(), []ProviderRef{refB}, st, nil, map[string]bool{"p2": true}, 0, throttle); err != nil {
		t.Fatal(err)
	}

	times := stub.requestTimes()
	if len(times) != 2 {
		t.Fatalf("应发 2 个请求, got %d", len(times))
	}
	if gap := times[1].Sub(times[0]); gap < 250*time.Millisecond {
		t.Errorf("跨轮的同厂商请求间隔 = %v，应 ≥ 300ms", gap)
	}
}

// TestVendorGap_NilThrottleNoDelay 断言 throttle 为 nil 时不做节流
// （`ccl quota --refresh` 这类一次性调用没有历史，不该白等）。
func TestVendorGap_NilThrottleNoDelay(t *testing.T) {
	stub := newVendorStub(t)
	st := NewStore(t.TempDir())

	refs := []ProviderRef{
		{Name: "p1", Config: &Config{Source: "newapi", Endpoint: stub.srv.URL, MinRequestGap: 10 * time.Millisecond, VendorGap: 5 * time.Second}, APIKey: "key-A"},
		{Name: "p2", Config: &Config{Source: "newapi", Endpoint: stub.srv.URL, MinRequestGap: 10 * time.Millisecond, VendorGap: 5 * time.Second}, APIKey: "key-B"},
	}
	start := time.Now()
	if err := RefreshAll(context.Background(), refs, st, nil, map[string]bool{"p1": true, "p2": true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("nil 节流器不该等待，耗时 %v", elapsed)
	}
	if stub.count() != 2 {
		t.Errorf("应发 2 个请求, got %d", stub.count())
	}
}

// TestBackoff_ReduceNotClear 断言成功刷新把退避**降一级**而不是清零。
//
// 理由（quota-per-vendor-rate-limit §D4）：厂商的限流常是**速率型**的，一次
// 成功不代表可以立刻恢复高频。清零会让退避永远爬不起来——实测 13:59 已退避
// 4m，14:24 又从 1m 重新开始（中间那次成功把退避清零了）→ 反复撞 429。
func TestBackoff_ReduceNotClear(t *testing.T) {
	st := NewStore(t.TempDir())
	key := VendorBackoffKey("https://example.test")

	if err := st.SetBackoff(key, time.Now().Unix()+600, 600); err != nil {
		t.Fatal(err)
	}
	if err := st.ReduceBackoff(key); err != nil {
		t.Fatal(err)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.BackoffFor(key)
	if b.Seconds != 300 {
		t.Errorf("成功后退避应降一级（600 → 300）, got %d", b.Seconds)
	}

	// 连续成功 → 继续降 → 到下限后删除
	if err := st.ReduceBackoff(key); err != nil {
		t.Fatal(err)
	}
	f, _ = st.Load()
	if got := f.BackoffFor(key).Seconds; got != 150 {
		t.Errorf("应降到 150, got %d", got)
	}
	for i := 0; i < 5; i++ {
		if err := st.ReduceBackoff(key); err != nil {
			t.Fatal(err)
		}
	}
	f, _ = st.Load()
	if got := f.BackoffFor(key).Seconds; got != 0 {
		t.Errorf("降到下限后应删除退避记录, got %d", got)
	}
}

// TestVendorBackoffKey_DistinctFromPoolKey 断言厂商键与旧的池键**不冲突**
// （旧记录读不出 → 视为无退避，不会误判成"正在退避"）。
func TestVendorBackoffKey_DistinctFromPoolKey(t *testing.T) {
	v := VendorBackoffKey("https://example.test")
	p := PoolID("https://example.test", "sk-1")
	if v == p {
		t.Errorf("厂商键与池键不应相同: %q", v)
	}
	f := &File{Backoffs: map[string]Backoff{p: {RetryAfter: 1 << 40, Seconds: 300}}}
	if f.BackoffFor(v).RetryAfter != 0 {
		t.Error("厂商键不该读到旧的池级退避记录")
	}
}
