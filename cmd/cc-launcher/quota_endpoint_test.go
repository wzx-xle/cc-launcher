package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cc-launcher/internal/collector"
	"cc-launcher/internal/quota"
)

// fakeDaemon 起一个假 daemon，返回它的基地址。
//
// handler 由调用方给；默认会带上端点标记头（真 daemon 的每个响应都带）。
func fakeDaemon(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(collector.QuotaEndpointHeader, collector.QuotaEndpointHeaderValue)
		h(w, r)
	}))
	t.Cleanup(srv.Close)

	old := quotaDaemonBaseURL
	quotaDaemonBaseURL = func() string { return srv.URL }
	t.Cleanup(func() { quotaDaemonBaseURL = old })
	return srv.URL
}

// quotaDocJSON 造一份额度文档的 JSON（source 用来区分"来自端点"还是
// "来自本地文件"）。
func quotaDocJSON(provider, source string) string {
	return fmt.Sprintf(`{"provider":%q,"source":%q,"stale":false,"fetched_at":%d,"dimensions":[]}`,
		provider, source, time.Now().Unix())
}

// setupQuotaProviderAndEntry 造一个 provider + 一条本地额度记录。
func setupQuotaProviderAndEntry(t *testing.T, baseDir, name string) {
	t.Helper()
	writeQuotaProvider(t, baseDir, name, map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://api.minimax.cn/anthropic",
			"ANTHROPIC_AUTH_TOKEN": "sk-x",
		},
		"quota": map[string]any{"source": "minimax"},
	})
	st := quota.NewStore(baseDir)
	if err := st.Update(name, "local-source", "pool-1", []quota.Dimension{
		{ID: "interval", Label: "5h", Kind: quota.KindWindow, Duration: 18000 * time.Second,
			Used: quotaFloat(59), Limit: quotaFloat(100), Unit: "%"},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestRunQuota_PrefersDaemonEndpoint 断言：daemon 可达 → 命令走端点，输出的是
// **端点给的文档**。
func TestRunQuota_PrefersDaemonEndpoint(t *testing.T) {
	baseDir := quotaHome(t)
	setupQuotaProviderAndEntry(t, baseDir, "p1")

	var gotPath, gotProvider string
	fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotProvider = r.URL.Query().Get("provider")
		fmt.Fprint(w, quotaDocJSON("p1", "from-daemon"))
	})

	stdout, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	if gotPath != collector.QuotaPath {
		t.Errorf("应请求 %s, got %q", collector.QuotaPath, gotPath)
	}
	if gotProvider != "p1" {
		t.Errorf("查询参数 provider = %q, 想要 p1", gotProvider)
	}
	var out quota.Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, stdout)
	}
	if out.Source != "from-daemon" {
		t.Errorf("应打印端点给的文档, source = %q", out.Source)
	}
}

// TestRunQuota_Daemon404Exit1 断言：端点说"没有记录"（404）→ 退出码 1。
func TestRunQuota_Daemon404Exit1(t *testing.T) {
	baseDir := quotaHome(t)
	setupQuotaProviderAndEntry(t, baseDir, "p1")

	fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no record", http.StatusNotFound)
	})

	_, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 1 {
		t.Fatalf("端点 404 应退出码 1, got %d (stderr=%s)", code, stderr)
	}
}

// TestRunQuota_Daemon422Exit2 断言：端点说"没有额度接口"（422）→ 退出码 2。
func TestRunQuota_Daemon422Exit2(t *testing.T) {
	baseDir := quotaHome(t)
	setupQuotaProviderAndEntry(t, baseDir, "p1")

	fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no quota interface", http.StatusUnprocessableEntity)
	})

	_, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 2 {
		t.Fatalf("端点 422 应退出码 2, got %d (stderr=%s)", code, stderr)
	}
}

// serveLocalQuotaDoc 返回一个 handler：按**真 daemon 的做法**（读同一个数据
// 文件 + 同一份组装逻辑）给出额度文档。
func serveLocalQuotaDoc(t *testing.T, baseDir, name string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		ref, _, err := quota.LoadProviderRef(filepath.Join(baseDir, "providers"), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		file, err := quota.NewStore(baseDir).Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		entry, ok := file.EntryFor(name)
		if !ok {
			http.Error(w, "no record", http.StatusNotFound)
			return
		}
		out, ok := quota.BuildOutput(ref, entry, time.Now())
		if !ok {
			http.Error(w, "no record", http.StatusNotFound)
			return
		}
		data, err := json.Marshal(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(data)
	}
}

// TestRunQuota_FallsBackToLocalFile 断言：端点不可达 → **回落直读本地文件**，
// 且输出与走端点时**逐字节一致**。
func TestRunQuota_FallsBackToLocalFile(t *testing.T) {
	baseDir := quotaHome(t)
	setupQuotaProviderAndEntry(t, baseDir, "p1")

	// 先走端点拿一份输出（端点读的是同一个文件、用同一份组装逻辑）。
	fakeDaemon(t, serveLocalQuotaDoc(t, baseDir, "p1"))
	viaEndpoint, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}

	// 再让端点不可达 → 回落本地文件。
	old := quotaDaemonBaseURL
	quotaDaemonBaseURL = func() string { return "http://127.0.0.1:1" }
	defer func() { quotaDaemonBaseURL = old }()

	viaLocal, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}

	if viaEndpoint != viaLocal {
		t.Errorf("两条路径的输出应逐字节一致:\n端点: %s\n本地: %s", viaEndpoint, viaLocal)
	}
}

// TestRunQuota_OldDaemonFallsBackToLocalFile 断言：**旧版本 daemon**（没有这个
// 端点 → mux 回 404 且**不带**端点标记头）→ 回落直读本地文件，而不是误报
// "没有额度记录"。
//
// 这是真实的升级场景：ccl 升级了但 daemon 还在跑。
func TestRunQuota_OldDaemonFallsBackToLocalFile(t *testing.T) {
	baseDir := quotaHome(t)
	setupQuotaProviderAndEntry(t, baseDir, "p1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Go 的 mux 对未注册的路径回 404 + "404 page not found"（没有标记头）。
		http.NotFound(w, r)
	}))
	defer srv.Close()
	old := quotaDaemonBaseURL
	quotaDaemonBaseURL = func() string { return srv.URL }
	defer func() { quotaDaemonBaseURL = old }()

	stdout, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 0 {
		t.Fatalf("旧 daemon 应回落本地文件（退出码 0）, got %d (stderr=%s)", code, stderr)
	}
	var out quota.Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, stdout)
	}
	if out.Source != "local-source" {
		t.Errorf("应读到本地文件的数据, source = %q", out.Source)
	}
}

// TestServeQuota_StatusCodes 断言：daemon 端点的业务判定（provider 未配置 /
// 没有额度接口 / 没有记录 / 有记录）。
func TestServeQuota_StatusCodes(t *testing.T) {
	baseDir := quotaHome(t)

	// provider 未配置 → 404
	if _, code := serveQuota(baseDir, "nope"); code != http.StatusNotFound {
		t.Errorf("未配置的 provider 应返回 404, got %d", code)
	}
	// provider 没有额度接口 → 422
	writeQuotaProvider(t, baseDir, "noquota", map[string]any{
		"env": map[string]any{"ANTHROPIC_BASE_URL": "https://example.com"},
	})
	if _, code := serveQuota(baseDir, "noquota"); code != http.StatusUnprocessableEntity {
		t.Errorf("没有额度接口的 provider 应返回 422, got %d", code)
	}
	// 有额度接口但没有记录 → 404
	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env":   map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
		"quota": map[string]any{"source": "minimax"},
	})
	if _, code := serveQuota(baseDir, "p1"); code != http.StatusNotFound {
		t.Errorf("没有记录的 provider 应返回 404, got %d", code)
	}
	// 有记录 → 200
	setupQuotaProviderAndEntry(t, baseDir, "p1")
	v, code := serveQuota(baseDir, "p1")
	if code != http.StatusOK {
		t.Fatalf("有记录的 provider 应返回 200, got %d", code)
	}
	doc, ok := v.(quota.Output)
	if !ok {
		t.Fatalf("返回值应是 quota.Output, got %T", v)
	}
	if doc.Provider != "p1" || len(doc.Dimensions) != 1 {
		t.Errorf("文档内容不对: %+v", doc)
	}
}

// TestServeQuota_RejectsPathTraversal 断言：provider 名里的路径穿越被挡
// （名字来自 HTTP 查询串）。
func TestServeQuota_RejectsPathTraversal(t *testing.T) {
	baseDir := quotaHome(t)
	for _, name := range []string{"../escape", "..", "a/b"} {
		if _, code := serveQuota(baseDir, name); code != http.StatusNotFound {
			t.Errorf("路径穿越 %q 应返回 404, got %d", name, code)
		}
	}
}

// TestDaemonQuotaEndpoint_EndToEnd 断言：**真 collector + 真 serveQuota** 的
// 链路：回环来源拿到与 `ccl quota --json` **逐字段一致**的文档；非回环来源被拒。
func TestDaemonQuotaEndpoint_EndToEnd(t *testing.T) {
	baseDir := quotaHome(t)
	setupQuotaProviderAndEntry(t, baseDir, "p1")

	srv := collector.NewServer(collector.ServerOptions{
		OnQuota: func(name string) (any, int) { return serveQuota(baseDir, name) },
	})

	// 回环来源 → 200 + 文档
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, collector.QuotaPath+"?provider=p1", nil)
	req.RemoteAddr = "127.0.0.1:51000"
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("回环来源应返回 200, got %d (%s)", w.Code, w.Body.String())
	}
	var doc quota.Output
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, w.Body.String())
	}
	if doc.Provider != "p1" || doc.Source != "local-source" || len(doc.Dimensions) != 1 {
		t.Errorf("文档内容不对: %+v", doc)
	}

	// 与命令的输出逐字段一致
	stdout, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	var fromCmd quota.Output
	if err := json.Unmarshal([]byte(stdout), &fromCmd); err != nil {
		t.Fatal(err)
	}
	if fromCmd.Provider != doc.Provider || fromCmd.Source != doc.Source ||
		fromCmd.Stale != doc.Stale || fromCmd.FetchedAt != doc.FetchedAt ||
		len(fromCmd.Dimensions) != len(doc.Dimensions) {
		t.Errorf("端点与命令的文档不一致:\n端点: %+v\n命令: %+v", doc, fromCmd)
	}

	// 非回环来源 → 403，且不返回任何额度
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, collector.QuotaPath+"?provider=p1", nil)
	req.RemoteAddr = "192.168.1.50:51000"
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("非回环来源应返回 403, got %d", w.Code)
	}
	if w.Body.Len() > 0 && json.Valid(w.Body.Bytes()) {
		t.Errorf("被拒的请求不该返回额度文档: %s", w.Body.String())
	}
}

// TestRunQuota_RefreshDoesNotUseDaemon 断言：`--refresh` **不绕 daemon**——它
// 自己联网查 → 写文件 → 直接组装。
func TestRunQuota_RefreshDoesNotUseDaemon(t *testing.T) {
	baseDir := quotaHome(t)
	setupQuotaProviderAndEntry(t, baseDir, "p1")

	var hits int
	fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, quotaDocJSON("p1", "from-daemon"))
	})

	// refresh 会真的去查厂商接口 → 用一个假厂商端点。
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":100,"total_used":10,"total_available":90}}`)
	}))
	defer vendor.Close()

	providersDir := baseDir + "/providers"
	data, err := json.Marshal(map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://api.minimax.cn/anthropic",
			"ANTHROPIC_AUTH_TOKEN": "sk-x",
		},
		"quota": map[string]any{"source": "newapi", "endpoint": vendor.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providersDir, "p1.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := captureQuotaRun(t, "--provider", "p1", "--refresh", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	if hits != 0 {
		t.Errorf("--refresh 不该请求 daemon 端点, hits=%d", hits)
	}
}
