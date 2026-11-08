package collector

import (
	"context"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// startTestServer 起一个跑在随机端口的 collector，返回它自己。
//
// 用**真实 HTTP**（而不是直接调 handler）：激活路径是在 NewServer 内部注册到
// mux 上的，走真实请求才能验证"路径确实注册了"（未配置回调时应 404）。
func startTestServer(t *testing.T, opts ServerOptions) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	opts.Addr = ":0" // 随机端口
	opts.Store = store
	srv := NewServer(opts)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.Start(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Start 未在 2s 内返回")
		}
	})

	// 等端口就绪（Start 之后 Addr 才从 ":0" 变成真实地址）。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a := srv.Addr(); a != ":0" && a != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return srv
}

// postActivate 发一次激活声明请求。
func postActivate(t *testing.T, srv *Server, provider string) *http.Response {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	// 用 127.0.0.1 而不是 srv.Addr()：后者在 IPv6 机器上是 "[::]:port"，
	// 拼进 URL 会变成非法 IP-literal。
	url := fmt.Sprintf("http://127.0.0.1:%s%s?provider=%s", port, activatePath, neturl.QueryEscape(provider))
	resp, err := http.Post(url, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestServer_Activate_AcceptsConfiguredProvider 断言：已配置的 provider 被接受
// → 204 + Activity 被标记 + 回调被调用。
func TestServer_Activate_AcceptsConfiguredProvider(t *testing.T) {
	var mu sync.Mutex
	var got string
	srv := startTestServer(t, ServerOptions{
		OnActivate: func(name string) error {
			mu.Lock()
			got = name
			mu.Unlock()
			return nil
		},
	})

	resp := postActivate(t, srv, "minimax-m3")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("应返回 204, got %d", resp.StatusCode)
	}

	mu.Lock()
	gotName := got
	mu.Unlock()
	if gotName != "minimax-m3" {
		t.Errorf("回调应收到 provider 名 %q, got %q", "minimax-m3", gotName)
	}
	if _, ok := srv.Activity().Recent(time.Minute)["minimax-m3"]; !ok {
		t.Error("被接受的 provider 应被标记为活跃")
	}
}

// TestServer_Activate_RejectsUnknownProvider 断言：未知的 provider 被拒绝
// → 400，且**不被标记为活跃**（校验失败不能有副作用）。
func TestServer_Activate_RejectsUnknownProvider(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnActivate: func(name string) error {
			return fmt.Errorf("未知的 provider %q", name)
		},
	})

	resp := postActivate(t, srv, "not-configured")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("应返回 400, got %d", resp.StatusCode)
	}
	if _, ok := srv.Activity().Recent(time.Minute)["not-configured"]; ok {
		t.Error("被拒绝的 provider 不该被标记为活跃")
	}
}

// TestServer_Activate_NotRegisteredWithoutCallback 断言：未配置 OnActivate 时
// 该路径**不注册** → 404（不暴露一个什么都不做的端点）。
func TestServer_Activate_NotRegisteredWithoutCallback(t *testing.T) {
	srv := startTestServer(t, ServerOptions{})

	resp := postActivate(t, srv, "minimax-m3")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未配置回调时该路径不该注册（应 404）, got %d", resp.StatusCode)
	}
}

// TestServer_Activate_RequiresProviderName 断言：缺 provider 参数 → 400。
func TestServer_Activate_RequiresProviderName(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnActivate: func(name string) error { return nil },
	})

	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%s%s", port, activatePath), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺 provider 参数应返回 400, got %d", resp.StatusCode)
	}
}

// TestServer_Activate_RejectsNonPost 断言：非 POST → 405。
func TestServer_Activate_RejectsNonPost(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnActivate: func(name string) error { return nil },
	})

	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s%s?provider=x", port, activatePath))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应返回 405, got %d", resp.StatusCode)
	}
}
