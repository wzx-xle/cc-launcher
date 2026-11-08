package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeProvider 写一个 provider 配置文件。
func writeProvider(t *testing.T, dir, name string, cfg map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// TestEnumerate 断言：枚举 providers 目录里**有额度接口**的 provider。
func TestEnumerate(t *testing.T) {
	dir := t.TempDir()
	writeProvider(t, dir, "minimax-m3", map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://api.minimax.cn/anthropic",
			"ANTHROPIC_AUTH_TOKEN": "sk-a",
		},
	})
	writeProvider(t, dir, "official", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-official"},
	})
	writeProvider(t, dir, "iflytek", map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://one.iflytek.com/api/llm/console/chat/",
			"ANTHROPIC_AUTH_TOKEN": "sk-i",
		},
		"quota": map[string]any{"source": "newapi", "quotaPerUnit": 500000},
	})

	refs, err := Enumerate(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, r := range refs {
		names[r.Name] = true
	}
	if !names["minimax-m3"] {
		t.Error("minimax-m3 应被枚举（靠 base_url 识别）")
	}
	if !names["iflytek"] {
		t.Error("iflytek 应被枚举（配了 quota 块）")
	}
	if names["official"] {
		t.Error("official 没有额度接口，不该被枚举")
	}
}

// TestParseConfig 断言 quota 块的解析与默认值。
func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"source":          "newapi",
		"endpoint":        "https://quota.example.com/api/usage/token",
		"quotaPerUnit":    float64(500000),
		"displayUnit":     "USD",
		"refreshInterval": float64(300),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "newapi" || cfg.Endpoint != "https://quota.example.com/api/usage/token" {
		t.Errorf("解析错误: %+v", cfg)
	}
	if cfg.QuotaPerUnit != 500000 || cfg.DisplayUnit != "USD" {
		t.Errorf("解析错误: %+v", cfg)
	}
	if cfg.RefreshInterval != 300*time.Second {
		t.Errorf("refreshInterval = %v, want 300s", cfg.RefreshInterval)
	}
	if cfg.Interval() != 300*time.Second {
		t.Errorf("Interval() = %v", cfg.Interval())
	}

	// 缺 refreshInterval → 缺省 60 秒
	cfg2, err := ParseConfig(map[string]any{"source": "minimax"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Interval() != 60*time.Second {
		t.Errorf("缺省刷新周期应为 60s, got %v", cfg2.Interval())
	}

	// 类型不对 → 报错
	if _, err := ParseConfig(map[string]any{"source": 42}); err == nil {
		t.Error("source 类型不对应报错")
	}
	if _, err := ParseConfig("not-an-object"); err == nil {
		t.Error("quota 块不是对象应报错")
	}

	// 没有 quota 键 → (nil, nil)
	c, err := ParseConfig(nil)
	if err != nil || c != nil {
		t.Errorf("没配 quota 时应返回 (nil, nil): %v %v", c, err)
	}
}

// TestResolve 断言 adapter 与端点的解析（含显式覆盖）。
func TestResolve(t *testing.T) {
	// 显式 source
	ref := ProviderRef{
		Name:    "iflytek",
		Config:  &Config{Source: "newapi"},
		BaseURL: "https://one.iflytek.com/api/llm/console/chat/",
	}
	ad, endpoint, err := ref.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ad.Name() != "newapi" {
		t.Errorf("adapter = %q", ad.Name())
	}
	if endpoint != "https://one.iflytek.com/api/llm/console/chat/api/usage/token/" {
		t.Errorf("端点 = %q", endpoint)
	}

	// 显式 endpoint 覆盖推导
	ref2 := ProviderRef{
		Name:    "x",
		Config:  &Config{Source: "newapi", Endpoint: "https://quota.example.com/api/usage/token"},
		BaseURL: "https://one.iflytek.com/api/llm/console/chat/",
	}
	_, endpoint2, err := ref2.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if endpoint2 != "https://quota.example.com/api/usage/token" {
		t.Errorf("显式端点应覆盖推导: %q", endpoint2)
	}

	// 没有 source → 靠 base_url 识别
	ref3 := ProviderRef{Name: "m", BaseURL: "https://api.minimax.cn/anthropic"}
	ad3, endpoint3, err := ref3.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ad3.Name() != "minimax" || endpoint3 != "https://www.minimax.cn/v1/token_plan/remains" {
		t.Errorf("识别错误: %q %q", ad3.Name(), endpoint3)
	}

	// 认不出来 → ErrNoQuotaInterface
	ref4 := ProviderRef{Name: "o"}
	if _, _, err := ref4.Resolve(); err != ErrNoQuotaInterface {
		t.Errorf("应返回 ErrNoQuotaInterface, got %v", err)
	}

	// source 写错 → 普通 error（不是"没有额度接口"）
	ref5 := ProviderRef{Name: "b", Config: &Config{Source: "nope"}}
	_, _, err = ref5.Resolve()
	if err == nil || err == ErrNoQuotaInterface {
		t.Errorf("未知 source 应报普通错误, got %v", err)
	}
}

// TestPoolID 断言额度池标识含 key 且不泄露明文。
func TestPoolID(t *testing.T) {
	a := PoolID("https://www.minimax.cn/v1/token_plan/remains", "sk-same")
	b := PoolID("https://www.minimax.cn/v1/token_plan/remains", "sk-same")
	c := PoolID("https://www.minimax.cn/v1/token_plan/remains", "sk-other")
	d := PoolID("https://api.deepseek.com/user/balance", "sk-same")

	if a != b {
		t.Error("同 (端点, key) 应得到同 pool")
	}
	if a == c {
		t.Error("不同 key 应得到不同 pool")
	}
	if a == d {
		t.Error("不同端点应得到不同 pool")
	}
	if strings.Contains(a, "sk-same") {
		t.Error("pool 不该含明文 key")
	}
}

// TestRefreshAll_DedupesSharedPool 断言：**共用一把 key 的 provider 只查一次网络**。
func TestRefreshAll_DedupesSharedPool(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":200000000,"total_used":2576,"total_available":199997424}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{
		{Name: "minimax-m3", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-same"},
		{Name: "minimax-m3.1-flash", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-same"},
		{Name: "other", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-other"},
	}
	st := NewStore(t.TempDir())
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	// 两个额度池（sk-same / sk-other）→ 2 次请求，不是 3 次
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("请求次数 = %d, want 2（按 (端点,key) 去重）", got)
	}
	// 但结果写给所有引用者
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"minimax-m3", "minimax-m3.1-flash", "other"} {
		e, ok := f.EntryFor(name)
		if !ok {
			t.Fatalf("%s 应有条目", name)
		}
		if len(e.Dimensions) != 1 {
			t.Errorf("%s 应有 1 个维度, got %d", name, len(e.Dimensions))
		}
	}
	// 同 pool 的两个 provider 的 pool 标识相同
	e1, _ := f.EntryFor("minimax-m3")
	e2, _ := f.EntryFor("minimax-m3.1-flash")
	if e1.Pool != e2.Pool {
		t.Errorf("同 key 的 pool 应相同: %q vs %q", e1.Pool, e2.Pool)
	}
}

// TestRefreshAll_SkipsWithinInterval 断言：没到刷新周期 → 不发请求。
func TestRefreshAll_SkipsWithinInterval(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{
		{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk"},
	}
	st := NewStore(t.TempDir())

	// 第一轮：刷新
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("第一轮应发 1 次请求")
	}
	// 第二轮：fetched_at 是刚才 → 跳过
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("没到周期不该再发请求, calls=%d", calls)
	}
}

// TestRefreshAll_IntervalFromFile 断言：周期判定读的是**文件里的** fetched_at，
// 不是内存 map —— `ccl quota --refresh` 刚写过的 provider，serve 下一轮会跳过。
func TestRefreshAll_IntervalFromFile(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{
		{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk"},
	}
	st := NewStore(t.TempDir())

	// 模拟"另一个进程（ccl quota --refresh）刚写过"
	if err := st.Update("p1", "newapi", "pool", []Dimension{{ID: "quota", Kind: KindQuota}}); err != nil {
		t.Fatal(err)
	}
	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("刚写过的 provider 不该被再查, calls=%d", calls)
	}
}

// TestRefreshAll_RefreshesWhenStale 断言：数据旧了（超过周期）→ 刷新。
func TestRefreshAll_RefreshesWhenStale(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	refs := []ProviderRef{
		{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk"},
	}
	st := NewStore(t.TempDir())
	if err := st.Update("p1", "newapi", "pool", nil); err != nil {
		t.Fatal(err)
	}
	// 把 fetched_at 改成 10 分钟前
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := f.Entries["p1"]
	e.FetchedAt = time.Now().Add(-10 * time.Minute).Unix()
	f.Entries["p1"] = e
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	if err := RefreshAll(context.Background(), refs, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("过期数据应被刷新, calls=%d", calls)
	}
}

// TestRefreshAll_KeepsOldEntryOnFailure 断言：刷新失败**不丢弃**旧条目
// （旧数据的 fetched_at 不变 → 越来越旧 → stale 自动生效）。
func TestRefreshAll_KeepsOldEntryOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()

	refs := []ProviderRef{
		{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk"},
	}
	st := NewStore(t.TempDir())
	if err := st.Update("p1", "newapi", "pool", []Dimension{{ID: "quota", Kind: KindQuota, Unit: "USD"}}); err != nil {
		t.Fatal(err)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	// 把 fetched_at 改旧 → 触发这一轮刷新（否则会因为"没到周期"被跳过）
	e := f.Entries["p1"]
	e.FetchedAt = time.Now().Add(-10 * time.Minute).Unix()
	f.Entries["p1"] = e
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	before := f.Entries["p1"]

	var logged []string
	if err := RefreshAll(context.Background(), refs, st, func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}, nil, 0); err != nil {
		t.Fatal(err)
	}
	f2, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	after, ok := f2.EntryFor("p1")
	if !ok {
		t.Fatal("失败后旧条目不该被丢")
	}
	if after.FetchedAt != before.FetchedAt {
		t.Errorf("失败后 fetched_at 不该变: %d → %d", before.FetchedAt, after.FetchedAt)
	}
	if len(after.Dimensions) != 1 {
		t.Errorf("失败后旧维度不该被丢: %+v", after.Dimensions)
	}
	if len(logged) == 0 {
		t.Error("失败原因应进日志")
	}
}

// TestRefreshAll_PrunesRemovedProviders 断言：已删除的 provider 的条目被清理。
func TestRefreshAll_PrunesRemovedProviders(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Update("gone", "newapi", "pool", nil); err != nil {
		t.Fatal(err)
	}
	if err := RefreshAll(context.Background(), nil, st, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.EntryFor("gone"); ok {
		t.Error("已不存在的 provider 的条目应被清掉")
	}
}

// TestRefreshOne 断言：单 provider 刷新写回数据文件。
func TestRefreshOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	st := NewStore(t.TempDir())
	ref := ProviderRef{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk"}
	dims, err := RefreshOne(context.Background(), ref, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(dims) != 1 {
		t.Fatalf("应有 1 个维度")
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.EntryFor("p1"); !ok {
		t.Error("应写回数据文件")
	}
}

// TestRefreshOne_FailureKeepsEntry 断言：刷新失败 → 不写文件。
func TestRefreshOne_FailureKeepsEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	st := NewStore(t.TempDir())
	if err := st.Update("p1", "newapi", "pool", []Dimension{{ID: "quota", Kind: KindQuota}}); err != nil {
		t.Fatal(err)
	}
	ref := ProviderRef{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk"}
	if _, err := RefreshOne(context.Background(), ref, st); err == nil {
		t.Fatal("应报错")
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := f.EntryFor("p1")
	if !ok || len(e.Dimensions) != 1 {
		t.Errorf("失败后旧条目不该被覆盖: %+v", e)
	}
}

// --- 代理 -----------------------------------------------------------------

// TestProxyConfig 断言 NO_PROXY 的匹配语义（大小写两种拼写都认、按后缀匹配）。
func TestProxyConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		host string
		want bool // 是否直连
	}{
		{"未命中 → 走代理", map[string]string{"HTTP_PROXY": "http://p:1"}, "api.example.com", false},
		{"NO_PROXY 命中 → 直连", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": ".example.com"}, "api.example.com", true},
		{"小写 no_proxy 同样生效", map[string]string{"HTTP_PROXY": "http://p:1", "no_proxy": ".example.com"}, "api.example.com", true},
		{"精确主机名", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": "api.example.com"}, "api.example.com", true},
		{"子域也算命中", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": "example.com"}, "api.example.com", true},
		{"其它域名不命中", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": ".other.com"}, "api.example.com", false},
		{"没配代理 → 直连", map[string]string{}, "api.example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pc := proxyConfigFromEnv(tc.env)
			u, err := neturl.Parse("https://" + tc.host)
			if err != nil {
				t.Fatal(err)
			}
			got, err := pc.proxyFor(u)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want && got != nil {
				t.Errorf("应直连, got proxy %v", got)
			}
			if !tc.want && got == nil {
				t.Error("应走代理")
			}
		})
	}
}

// TestProxyRoutingEndToEnd 断言：额度请求真的按 provider 的 env 块决定走不走代理。
//
// 用自定义 dial 把 "quota.example.com" 转到本地测试服务器：命中 NO_PROXY 时
// 直连（打 target），未命中时走代理（打 proxy）。
func TestProxyRoutingEndToEnd(t *testing.T) {
	var directHits, proxyHits int32

	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&directHits, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer targetSrv.Close()
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer proxySrv.Close()

	targetAddr := strings.TrimPrefix(targetSrv.URL, "http://")
	proxyAddr := strings.TrimPrefix(proxySrv.URL, "http://")

	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "quota.example.com") {
			atomic.AddInt32(&directHits, 0)
			return (&net.Dialer{}).DialContext(ctx, network, targetAddr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, proxyAddr)
	}

	run := func(env map[string]string) {
		atomic.StoreInt32(&directHits, 0)
		atomic.StoreInt32(&proxyHits, 0)
		client := newHTTPClientWithDial(env, dial)
		req, _ := http.NewRequest(http.MethodGet, "http://quota.example.com/usage", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// 配了代理、没配 NO_PROXY → 走代理
	run(map[string]string{"HTTP_PROXY": proxySrv.URL})
	if atomic.LoadInt32(&proxyHits) != 1 || atomic.LoadInt32(&directHits) != 0 {
		t.Errorf("应走代理: proxy=%d direct=%d", proxyHits, directHits)
	}

	// NO_PROXY 命中 → 直连
	run(map[string]string{"HTTP_PROXY": proxySrv.URL, "NO_PROXY": ".example.com"})
	if atomic.LoadInt32(&proxyHits) != 0 || atomic.LoadInt32(&directHits) != 1 {
		t.Errorf("应直连: proxy=%d direct=%d", proxyHits, directHits)
	}

	// 小写 no_proxy 同样生效
	run(map[string]string{"HTTP_PROXY": proxySrv.URL, "no_proxy": ".example.com"})
	if atomic.LoadInt32(&proxyHits) != 0 || atomic.LoadInt32(&directHits) != 1 {
		t.Errorf("小写 no_proxy 应生效: proxy=%d direct=%d", proxyHits, directHits)
	}

	// 没配代理 → 直连
	run(map[string]string{})
	if atomic.LoadInt32(&proxyHits) != 0 || atomic.LoadInt32(&directHits) != 1 {
		t.Errorf("没配代理应直连: proxy=%d direct=%d", proxyHits, directHits)
	}
}
