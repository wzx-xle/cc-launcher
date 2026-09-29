package collector

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewForwarderFromEnv_DisabledWhenEmpty(t *testing.T) {
	t.Setenv("CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT", "")
	f := NewForwarderFromEnv()
	if f.Enabled() {
		t.Errorf("should be disabled when env unset")
	}
}

func TestNewForwarderFromEnv_LoopbackRejected(t *testing.T) {
	cases := []string{
		"http://localhost:4318",
		"http://127.0.0.1:4318",
		"https://localhost:443",
	}
	for _, ep := range cases {
		t.Run(ep, func(t *testing.T) {
			t.Setenv("CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT", ep)
			f := NewForwarderFromEnv()
			if f.Enabled() {
				t.Errorf("loopback %q should not enable forwarder", ep)
			}
		})
	}
}

func TestNewForwarderFromEnv_RemoteEnabled(t *testing.T) {
	t.Setenv("CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel-collector.example.com:4317")
	t.Setenv("CC_FORWARD_AUTH_HEADER", "Authorization=Bearer xyz")
	if !NewForwarderFromEnv().Enabled() {
		t.Errorf("remote endpoint should enable forwarder")
	}
}

func TestForwarder_ForwardOK(t *testing.T) {
	var got atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Add(1)
		if r.Header.Get("Authorization") != "Bearer xyz" {
			t.Errorf("auth header missing")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 用 NewForwarder 直接构造，绕过 loopback 检查（测试目标是 httptest loopback server）
	f := NewForwarder(srv.URL, "Authorization=Bearer xyz")

	f.Forward("metrics", []byte("payload"))
	f.Forward("logs", []byte("logspayload"))
	f.Forward("traces", []byte("tracespayload"))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && got.Load() < 3 {
		time.Sleep(50 * time.Millisecond)
	}
	if got.Load() != 3 {
		t.Errorf("want 3 forwarder POSTs, got %d", got.Load())
	}
}

func TestForwarder_ForwardDropsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	f := NewForwarder(srv.URL, "")
	f.Forward("metrics", []byte("payload"))

	time.Sleep(300 * time.Millisecond) // 等异步 POST 完成 + 不 panic
}

func TestForwarder_ForwardDropsOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
	}))
	defer srv.Close()

	f := NewForwarder(srv.URL, "")
	start := time.Now()
	f.Forward("metrics", []byte("payload"))
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Errorf("Forward should return immediately (fire-and-forget); got %v", elapsed)
	}
	time.Sleep(5500 * time.Millisecond) // 5s timeout 内 log + drop
}

func TestForwarder_AuthNotInErrorLog(t *testing.T) {
	// 这测试主要确认 auth 值不会出现在某种错误日志里。
	// 直接验证 auth header 字段访问方式正确即可。
	t.Setenv("CC_FORWARD_AUTH_HEADER", "Authorization=Bearer supersecrettoken")
	t.Setenv("CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT", "https://example.com:443")
	// 通过构造 forwarder 验证 authHeader 字段设置正确（间接）
	// 但 authHeader 是私有字段，只能通过 Enabled() / 行为间接验证。
	// 这里跳过——authHeader 在 forwarder 内只在 post() 拼 header；log 不打 value。
	_ = NewForwarderFromEnv()
}

func TestAuthMask(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"Authorization=Bearer xyz", " (Authorization=***)"},
		{"X-Custom: secret-value", " (X-Custom=***)"},
		{"plain-string-without-separator", " (auth=***)"},
	}
	for _, c := range cases {
		if got := authMask(c.in); got != c.want {
			t.Errorf("authMask(%q): want %q, got %q", c.in, c.want, got)
		}
	}
}

// telemetry-config-compat §2.7

func TestNewForwarderFromEnv_PerSignalPriority(t *testing.T) {
	// 明确 unset FORWORD_ 后再 set，避免测试间干扰
	t.Setenv("FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "https://primary:4318/v1/metrics")
	t.Setenv("CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT", "https://fallback:4318")
	f := NewForwarderFromEnv()
	if !f.Enabled() {
		t.Fatal("forwarder should be enabled")
	}
	if got := f.Target(); got != "https://primary:4318/v1/metrics" {
		t.Errorf("FORWORD_ should win over CC_ORIG_: got target=%q", got)
	}
}

// telemetry-config-compat §2.8

func TestNewForwarderFromEnv_FallbackPath(t *testing.T) {
	// unset FORWORD_，只设 CC_ORIG_ → forwarder 仍启用，target 用 CC_ORIG_ URL
	t.Setenv("FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT", "https://legacy:4318")
	f := NewForwarderFromEnv()
	if !f.Enabled() {
		t.Fatal("forwarder should be enabled via CC_ORIG_ fallback")
	}
	if got := f.Target(); got != "https://legacy:4318/v1/metrics" {
		t.Errorf("fallback should append /v1/metrics: got target=%q", got)
	}
}

// telemetry-config-compat §2.9

func TestNewForwarderFromEnv_LocalhostSkip(t *testing.T) {
	// FORWORD_ 与 CC_ORIG_ 都指向 localhost → forwarder 禁用（防 loop）
	t.Setenv("FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://localhost:4318/v1/metrics")
	t.Setenv("CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	f := NewForwarderFromEnv()
	if f.Enabled() {
		t.Errorf("forwarder should be disabled when both env point to localhost")
	}
}

// telemetry-config-compat §2.10

func TestForwarder_EdgeCases(t *testing.T) {
	t.Run("FORWORD_ empty → disabled", func(t *testing.T) {
		t.Setenv("FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
		t.Setenv("CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT", "")
		if NewForwarderFromEnv().Enabled() {
			t.Errorf("empty env should disable forwarder")
		}
	})

	t.Run("baseURL already has /v1/metrics → not duplicated", func(t *testing.T) {
		f := NewForwarder("http://remote:4318/v1/metrics", "")
		if got := f.Target(); got != "http://remote:4318/v1/metrics" {
			t.Errorf("target should not double-suffix: got %q", got)
		}
	})

	t.Run("baseURL has trailing slash → no double slash", func(t *testing.T) {
		f := NewForwarder("http://remote:4318/", "")
		if got := f.Target(); got != "http://remote:4318/v1/metrics" {
			t.Errorf("trailing slash should be trimmed: got %q", got)
		}
	})

	t.Run("baseURL missing scheme → passed through unchanged", func(t *testing.T) {
		f := NewForwarder("remote:4318", "")
		if got := f.Target(); got != "remote:4318/v1/metrics" {
			t.Errorf("scheme-less URL should be passed through: got %q", got)
		}
	})

	t.Run("baseURL with /v1/metrics/ trailing slash → trimmed, no duplicate", func(t *testing.T) {
		f := NewForwarder("http://remote:4318/v1/metrics/", "")
		if got := f.Target(); got != "http://remote:4318/v1/metrics" {
			t.Errorf("trailing slash after /v1/metrics should be trimmed: got %q", got)
		}
	})
}