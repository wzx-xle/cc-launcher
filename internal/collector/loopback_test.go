package collector

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// reqFrom 构造一个"来源地址可控"的请求。
//
// **为什么不用真实连接**：httptest 的真实连接永远来自 127.0.0.1，要测
// "非回环来源被拒"只能直接构造带指定 RemoteAddr 的请求。
func reqFrom(method, target, remote string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	return req
}

// TestActivate_RejectsNonLoopback 断言：**非回环来源**的激活声明被拒（403），
// 且**没有任何副作用**（不调回调、不标记活跃）。
func TestActivate_RejectsNonLoopback(t *testing.T) {
	var mu sync.Mutex
	var called bool
	srv := startTestServer(t, ServerOptions{
		OnActivate: func(name string) error {
			mu.Lock()
			called = true
			mu.Unlock()
			return nil
		},
	})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, reqFrom(http.MethodPost, activatePath+"?provider=p1", "192.168.1.50:51000"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("非回环来源应返回 403, got %d", w.Code)
	}
	mu.Lock()
	got := called
	mu.Unlock()
	if got {
		t.Error("被拒的请求不该调回调")
	}
	if _, ok := srv.Activity().Recent(time.Minute)["p1"]; ok {
		t.Error("被拒的 provider 不该被标记为活跃")
	}
}

// TestActivate_AcceptsLoopback 断言：回环来源的激活声明照常受理（204）。
func TestActivate_AcceptsLoopback(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnActivate: func(name string) error { return nil },
	})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, reqFrom(http.MethodPost, activatePath+"?provider=p1", "127.0.0.1:51000"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("回环来源应返回 204, got %d", w.Code)
	}
	if _, ok := srv.Activity().Recent(time.Minute)["p1"]; !ok {
		t.Error("被接受的 provider 应被标记为活跃")
	}
}

// TestActivate_IgnoresForwardedHeader 断言：请求头里的 X-Forwarded-For
// **不参与判定**——来源是回环就放行（daemon 是裸 HTTP server，没有可信的
// 转发链；看那个头等于把判定交给请求方）。
func TestActivate_IgnoresForwardedHeader(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnActivate: func(name string) error { return nil },
	})

	req := reqFrom(http.MethodPost, activatePath+"?provider=p1", "127.0.0.1:51000")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("回环来源应放行（不因 X-Forwarded-For 被拒）, got %d", w.Code)
	}
}

// TestQuotaEndpoint_LoopbackCallerGetsQuota 断言：回环来源能拿到额度文档。
func TestQuotaEndpoint_LoopbackCallerGetsQuota(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnQuota: func(name string) (any, int) {
			return map[string]any{"provider": name, "stale": false, "dimensions": []any{}}, http.StatusOK
		},
	})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, reqFrom(http.MethodGet, QuotaPath+"?provider=p1", "127.0.0.1:51000"))

	if w.Code != http.StatusOK {
		t.Fatalf("回环来源应返回 200, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get(QuotaEndpointHeader); got != QuotaEndpointHeaderValue {
		t.Errorf("响应应带端点标记头, got %q", got)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, w.Body.String())
	}
	if doc["provider"] != "p1" {
		t.Errorf("文档内容不对: %v", doc)
	}
}

// TestQuotaEndpoint_RejectsNonLoopback 断言：**非回环来源**被拒（403），且
// **不返回任何额度**。
func TestQuotaEndpoint_RejectsNonLoopback(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnQuota: func(name string) (any, int) {
			return map[string]any{"provider": name, "dimensions": []any{}}, http.StatusOK
		},
	})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, reqFrom(http.MethodGet, QuotaPath+"?provider=p1", "192.168.1.50:51000"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("非回环来源应返回 403, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "dimensions") {
		t.Errorf("被拒的请求不该返回额度: %s", w.Body.String())
	}
}

// TestQuotaEndpoint_IgnoresForwardedHeader 断言：请求头里的 X-Forwarded-For
// 不参与判定。
func TestQuotaEndpoint_IgnoresForwardedHeader(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnQuota: func(name string) (any, int) {
			return map[string]any{"provider": name}, http.StatusOK
		},
	})

	req := reqFrom(http.MethodGet, QuotaPath+"?provider=p1", "127.0.0.1:51000")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("回环来源应放行（不因 X-Forwarded-For 被拒）, got %d", w.Code)
	}
}

// TestQuotaEndpoint_RequiresProviderName 断言：缺 provider 参数 → 400。
func TestQuotaEndpoint_RequiresProviderName(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		OnQuota: func(name string) (any, int) {
			t.Error("缺 provider 名时不该调回调")
			return nil, http.StatusInternalServerError
		},
	})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, reqFrom(http.MethodGet, QuotaPath, "127.0.0.1:51000"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 provider 参数应返回 400, got %d", w.Code)
	}
}

// TestQuotaEndpoint_NotRegisteredWithoutCallback 断言：未配置 OnQuota 时该
// 路径**不注册** → 404（不暴露一个"什么都不做"的端点）。
func TestQuotaEndpoint_NotRegisteredWithoutCallback(t *testing.T) {
	srv := startTestServer(t, ServerOptions{})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, reqFrom(http.MethodGet, QuotaPath+"?provider=p1", "127.0.0.1:51000"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("未配置回调时该路径不该注册（应 404）, got %d", w.Code)
	}
}

// TestMetrics_NotRestrictedToLoopback 断言：**遥测端点不受回环限制**——
// WSL2 / 容器里的 Claude Code 经 NAT 连过来时源地址不是回环，收紧会打断遥测。
func TestMetrics_NotRestrictedToLoopback(t *testing.T) {
	srv := startTestServer(t, ServerOptions{})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, reqFrom(http.MethodPost, "/v1/metrics", "192.168.1.50:51000"))

	if w.Code == http.StatusForbidden {
		t.Fatal("遥测端点不该被回环限制挡住")
	}
	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
		t.Fatalf("遥测端点应照常服务（200/400）, got %d", w.Code)
	}
}

// TestDashboard_NotRestrictedToLoopback 断言：看板路由也不受回环限制（用户要
// 能从别的机器打开看板）。
func TestDashboard_NotRestrictedToLoopback(t *testing.T) {
	srv := startTestServer(t, ServerOptions{
		Mount: func(mux *http.ServeMux) {
			mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		},
	})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, reqFrom(http.MethodGet, "/ui", "192.168.1.50:51000"))

	if w.Code != http.StatusOK {
		t.Fatalf("看板应照常服务, got %d", w.Code)
	}
}

// TestIsLoopbackRequest 断言：回环判定的各种输入。
func TestIsLoopbackRequest(t *testing.T) {
	cases := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:1234", true},
		{"127.0.0.5:1234", true},
		{"[::1]:1234", true},
		{"192.168.1.50:1234", false},
		{"10.0.0.1:1234", false},
		{"[2001:db8::1]:1234", false},
		{"", false},
		{"garbage", false},
	}
	for _, c := range cases {
		got := isLoopbackRequest(reqFrom(http.MethodGet, "/", c.remote))
		if got != c.want {
			t.Errorf("isLoopbackRequest(%q) = %v, 想要 %v", c.remote, got, c.want)
		}
	}
}
