package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cc-launcher/internal/collector"
	"cc-launcher/internal/quota"
	"cc-launcher/internal/state"
)

// TestDeclareProviderToDaemon_EscapesProviderName 断言：provider 名被正确
// URL 转义（daemon 收到的仍是原名）。
func TestDeclareProviderToDaemon_EscapesProviderName(t *testing.T) {
	var mu sync.Mutex
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.URL.Query().Get("provider")
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	declareProviderToDaemon(srv.URL, "a b&c=d")

	mu.Lock()
	defer mu.Unlock()
	if got != "a b&c=d" {
		t.Errorf("daemon 应收到原名, got %q", got)
	}
}

// TestDeclareProviderToDaemon_IgnoresUnreachableDaemon 断言：daemon 不可达时
// **静默忽略**（不报错、不 panic、快速返回 —— 绝不阻塞 Claude Code 启动）。
func TestDeclareProviderToDaemon_IgnoresUnreachableDaemon(t *testing.T) {
	// 找一个确定没人监听的端口（监听后立刻释放）。
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	start := time.Now()
	declareProviderToDaemon("http://"+addr, "p1") // 不该 panic
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Errorf("daemon 不可达时应快速返回, 耗时 %v", elapsed)
	}
}

// TestDeclareProviderToDaemon_SkipsEmptyProvider 断言：provider 名为空时不发
// 请求。
func TestDeclareProviderToDaemon_SkipsEmptyProvider(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	declareProviderToDaemon(srv.URL, "")

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("provider 名为空时不该发请求, hits=%d", got)
	}
}

// TestNotifyActivate_AcceptsConfiguredProvider 断言：已配置的 provider 名被
// 接受并送进激活通道。
func TestNotifyActivate_AcceptsConfiguredProvider(t *testing.T) {
	baseDir := quotaHome(t)
	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-1"},
	})

	ch := make(chan string, 1)
	if err := notifyActivate(ch, "p1", filepath.Join(baseDir, "providers")); err != nil {
		t.Fatalf("已配置的 provider 应被接受: %v", err)
	}
	select {
	case got := <-ch:
		if got != "p1" {
			t.Errorf("通道里应是 %q, got %q", "p1", got)
		}
	default:
		t.Error("接受后 provider 名应进激活通道")
	}
}

// TestNotifyActivate_RejectsUnknownProvider 断言：未配置的 provider 名被拒绝
// 且**不进通道**（否则 daemon 会为它发请求）。
func TestNotifyActivate_RejectsUnknownProvider(t *testing.T) {
	baseDir := quotaHome(t)
	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-1"},
	})

	ch := make(chan string, 1)
	if err := notifyActivate(ch, "not-configured", filepath.Join(baseDir, "providers")); err == nil {
		t.Error("未配置的 provider 应被拒绝")
	}
	select {
	case got := <-ch:
		t.Errorf("被拒绝的 provider 不该进通道, got %q", got)
	default:
	}
}

// TestNotifyActivate_RejectsPathTraversal 断言：路径穿越的名字被拒绝。
//
// provider 名来自 HTTP 查询串 → 不能让它拼出 providers 目录外的路径。
func TestNotifyActivate_RejectsPathTraversal(t *testing.T) {
	baseDir := quotaHome(t)
	providersDir := filepath.Join(baseDir, "providers")
	if err := os.MkdirAll(providersDir, 0755); err != nil {
		t.Fatal(err)
	}
	// providers 目录外放一个文件，尝试用 ../ 引用它。
	outside := filepath.Join(baseDir, "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	ch := make(chan string, 1)
	for _, name := range []string{"../outside", "..\\outside", "a/b", ".", ".."} {
		if err := notifyActivate(ch, name, providersDir); err == nil {
			t.Errorf("名字 %q 应被拒绝（防路径穿越）", name)
		}
	}
	select {
	case got := <-ch:
		t.Errorf("被拒绝的名字不该进通道, got %q", got)
	default:
	}
}

// TestMarkRecordedActiveProvider 断言：daemon 启动时把 state.json 里记录的
// active provider 标记为活跃（启动那轮因此有池可刷）。
func TestMarkRecordedActiveProvider(t *testing.T) {
	baseDir := quotaHome(t)
	if err := state.NewManager(baseDir).SetActiveProvider("p1"); err != nil {
		t.Fatal(err)
	}

	activity := collector.NewActivity()
	markRecordedActiveProvider(activity, baseDir)

	if _, ok := activity.Recent(time.Minute)["p1"]; !ok {
		t.Error("state.json 记录的 active provider 应被标记为活跃")
	}
}

// TestMarkRecordedActiveProvider_NoRecord 断言：没有记录时不标记（也不报错）。
func TestMarkRecordedActiveProvider_NoRecord(t *testing.T) {
	baseDir := quotaHome(t)

	activity := collector.NewActivity()
	markRecordedActiveProvider(activity, baseDir)

	if len(activity.Recent(time.Minute)) != 0 {
		t.Error("没有记录时不该标记任何 provider")
	}
}

// TestQuotaRefreshLoop_ActivationTriggersImmediateRefresh 断言（端到端）：
// 收到激活声明后，刷新循环**立刻**发一次请求（不等 ticker）。
//
// 这条覆盖的是"ccl 启动 Claude Code 前声明 provider → 那次会话的额度当场
// 可用"这条链路。
func TestQuotaRefreshLoop_ActivationTriggersImmediateRefresh(t *testing.T) {
	baseDir := quotaHome(t)

	// 额度接口桩（计请求数）。
	var mu sync.Mutex
	requests := 0
	quotaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer quotaSrv.Close()
	countRequests := func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}

	// provider 配置：quota 块指向桩。
	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://api.example.com",
			"ANTHROPIC_AUTH_TOKEN": "sk-1",
		},
		"quota": map[string]any{"source": "newapi", "endpoint": quotaSrv.URL},
	})

	// collector（带激活回调）。
	activateCh := make(chan string, 8)
	store, err := collector.OpenStore(filepath.Join(baseDir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv := collector.NewServer(collector.ServerOptions{
		Addr:  ":0",
		Store: store,
		OnActivate: func(provider string) error {
			return notifyActivate(activateCh, provider, filepath.Join(baseDir, "providers"))
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Start(ctx)

	// 等端口就绪。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a := srv.Addr(); a != ":0" && a != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 起刷新循环。
	go quotaRefreshLoop(ctx, baseDir, srv.Activity(), activateCh)

	// **启动那轮不该发请求**：池没有记录 + 没有活跃成员 → 不查。
	time.Sleep(300 * time.Millisecond)
	if got := countRequests(); got != 0 {
		t.Fatalf("启动那轮不该发请求（无记录 + 不活跃）, requests=%d", got)
	}

	// 激活声明 → 应立刻触发一轮刷新。
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%s/ccl/activate?provider=%s", port, neturl.QueryEscape("p1"))
	resp, err := http.Post(url, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("激活声明应返回 204, got %d", resp.StatusCode)
	}

	// 等请求到达（不等 ticker —— ticker 是 15 秒，这里只等 3 秒）。
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if countRequests() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := countRequests(); got != 1 {
		t.Errorf("激活声明后应立即刷新一轮, requests=%d", got)
	}

	// 额度数据应已写进数据文件（状态栏能读到）。
	entries, err := quota.NewStore(baseDir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := entries.EntryFor("p1"); !ok || e.FetchedAt == 0 {
		t.Errorf("激活刷新后应有 p1 的额度记录: %+v", e)
	}
}
