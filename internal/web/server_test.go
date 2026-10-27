package web

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// makeTestDB 造一个带 sessions / metrics 的 telemetry.db。
//
// 三条 session：
//   - s1: provider=minimax, cwd=/home/u/cc-launcher, model=MiniMax-M2
//   - s2: provider=deepseek, cwd=/home/u/other, model=deepseek-chat
//   - s3: 无 provider / 无 cwd（模拟直接 `claude` 启动的会话）
func makeTestDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		started_at INTEGER NOT NULL,
		ended_at INTEGER,
		provider TEXT NOT NULL DEFAULT '',
		provider_endpoint TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS metrics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		name TEXT NOT NULL,
		value REAL NOT NULL,
		labels TEXT NOT NULL DEFAULT '{}',
		captured_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UnixMilli()
	rows := []struct {
		id, provider, cwd, model string
	}{
		{"s1", "minimax", "/home/u/cc-launcher", "MiniMax-M2"},
		{"s2", "deepseek", "/home/u/other", "deepseek-chat"},
		{"s3", "", "", ""},
		// s4 与 s1 同 project 但**不同 provider** —— 让 project 分组里的
		// provider 不唯一，用来抓"provider 占比复用了分组行的 provider 列"
		// （那会把整个 project 的用量归到其中任意一个 provider 上）。
		{"s4", "deepseek", "/home/u/cc-launcher", "MiniMax-M2"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO sessions (id, started_at, provider, cwd, model)
			VALUES (?, ?, ?, ?, ?)`, r.id, now, r.provider, r.cwd, r.model); err != nil {
			t.Fatal(err)
		}
		// 每条 session：input 100 / output 10 / cacheRead 5 + cost
		tokens := []struct {
			typ string
			val float64
		}{
			{"input", 100}, {"output", 10}, {"cacheCreation", 0}, {"cacheRead", 5},
		}
		for _, tk := range tokens {
			labels, _ := json.Marshal(map[string]string{
				"type": tk.typ, "model": r.model,
				"user.id": "fingerprint-secret", "host.arch": "x86_64", "os.version": "6.1.0",
			})
			if _, err := db.Exec(`INSERT INTO metrics (session_id, name, value, labels, captured_at)
				VALUES (?, 'claude_code.token.usage', ?, ?, ?)`, r.id, tk.val, string(labels), now); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO metrics (session_id, name, value, labels, captured_at)
			VALUES (?, 'claude_code.cost.usage', 0.5, '{}', ?)`, r.id, now); err != nil {
			t.Fatal(err)
		}
	}
}

// newTestServer 造一个 web.Server + mux（临时目录里的 token 文件与 db）。
func newTestServer(t *testing.T) (*httptest.Server, *Server, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")
	makeTestDB(t, dbPath)
	tokenPath := filepath.Join(dir, TokenFileName)

	srv, err := NewServer(Options{DBPath: dbPath, TokenPath: tokenPath})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, srv, tokenPath
}

// TestTokenFile_Created 断言首次启动生成 64 位 hex token + 0600 权限。
func TestTokenFile_Created(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TokenFileName)
	tok, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 {
		t.Fatalf("token 应为 64 个 hex 字符, got %d (%q)", len(tok), tok)
	}
	for _, c := range tok {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("token 含非小写 hex 字符: %q", tok)
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token 文件权限应为 0600, got %o", perm)
	}
}

// TestTokenFile_Preserved 断言已存在的 token 原样保留。
func TestTokenFile_Preserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TokenFileName)
	if err := os.WriteFile(path, []byte("deadbeef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "deadbeef" {
		t.Fatalf("已有 token 应原样保留, got %q", tok)
	}
}

// TestAuth_NoToken 断言无 token / 无 cookie → 401。
func TestAuth_NoToken(t *testing.T) {
	ts, _, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}

	resp2, err := http.Get(ts.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp2.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("API 401 应带 error 字段: %v", body)
	}
}

// TestAuth_WrongToken 断言错误 token → 401 且不种 cookie。
func TestAuth_WrongToken(t *testing.T) {
	ts, _, _ := newTestServer(t)
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(ts.URL + "/ui?token=wrong")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			t.Fatal("错误 token 不应种 cookie")
		}
	}
}

// TestAuth_ValidTokenRedirects 断言正确 token → 302 + Set-Cookie + 地址栏无 token。
func TestAuth_ValidTokenRedirects(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(ts.URL + "/ui?token=" + srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("want 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, "token=") {
		t.Fatalf("重定向地址不应带 token: %q", loc)
	}
	if loc != "/ui" {
		t.Fatalf("重定向地址应为 /ui, got %q", loc)
	}
	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatal("未种 session cookie")
	}
	if !found.HttpOnly {
		t.Error("cookie 应为 HttpOnly")
	}
	if found.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite 应为 Lax, got %v", found.SameSite)
	}
	if found.Path != "/" {
		t.Errorf("cookie Path 应为 /, got %q", found.Path)
	}
	if found.MaxAge <= 0 {
		t.Error("cookie 应带有效期")
	}
	if strings.Contains(found.Value, srv.Token()) {
		t.Error("cookie 值不应包含 token 明文")
	}
}

// TestAuth_CookieAccessesAPI 断言 cookie 能访问 API。
func TestAuth_CookieAccessesAPI(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, err := newJar()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.Get(ts.URL + "/ui?token=" + srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp2, err := client.Get(ts.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp2.StatusCode)
	}
}

// TestAuth_RotateInvalidatesSession 断言轮换 token 后旧 cookie 失效。
func TestAuth_RotateInvalidatesSession(t *testing.T) {
	ts, srv, tokenPath := newTestServer(t)
	jar, err := newJar()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.Get(ts.URL + "/ui?token=" + srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 轮换
	if _, err := RotateToken(tokenPath); err != nil {
		t.Fatal(err)
	}

	resp2, err := client.Get(ts.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("轮换后旧 cookie 应失效, got %d", resp2.StatusCode)
	}
}

// TestAPI_UnknownWindow 断言未知 window → 400。
func TestAPI_UnknownWindow(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	resp2, err := client.Get(ts.URL + "/api/stats?window=7days")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp2.StatusCode)
	}
}

// TestAPI_UnknownGroup 断言未知 group → 400。
func TestAPI_UnknownGroup(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	resp2, err := client.Get(ts.URL + "/api/stats?group=whatever")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp2.StatusCode)
	}
}

// TestAPI_DefaultWindow 断言默认窗口是 7d，7 个 daily 桶。
func TestAPI_DefaultWindow(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	data := fetchStats(t, client, ts.URL+"/api/stats")
	if data.Window != "7d" {
		t.Fatalf("window 应为 7d, got %q", data.Window)
	}
	if len(data.Buckets) != 7 {
		t.Fatalf("7d 窗口应有 7 个桶, got %d", len(data.Buckets))
	}
}

// TestAPI_24hWindow 断言 24h 窗口给 24 个 hourly 桶。
func TestAPI_24hWindow(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	data := fetchStats(t, client, ts.URL+"/api/stats?window=24h")
	if data.Window != "24h" {
		t.Fatalf("window 应为 24h, got %q", data.Window)
	}
	if len(data.Buckets) != 24 {
		t.Fatalf("24h 窗口应有 24 个桶, got %d", len(data.Buckets))
	}
}

// TestAPI_ProviderFilter 断言 provider 过滤生效，且 totals 与行和一致。
func TestAPI_ProviderFilter(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	data := fetchStats(t, client, ts.URL+"/api/stats?provider=minimax")
	for _, s := range data.Sessions {
		if !strings.Contains(s.Provider, "minimax") {
			t.Fatalf("provider 过滤未生效: %+v", s)
		}
	}
	if len(data.Sessions) != 1 {
		t.Fatalf("应只剩 1 条 session, got %d", len(data.Sessions))
	}
	var sum int64
	for _, s := range data.Sessions {
		sum += s.Input + s.Output + s.CacheCreation + s.CacheRead
	}
	if data.Totals.Input+data.Totals.Output+data.Totals.CacheCreation+data.Totals.CacheRead != sum {
		t.Fatalf("totals != 行和")
	}
}

// TestAPI_GroupByProvider 断言 group=provider 生效。
func TestAPI_GroupByProvider(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	data := fetchStats(t, client, ts.URL+"/api/stats?group=provider")
	if data.GroupBy != "provider" {
		t.Fatalf("group_by 应为 provider, got %q", data.GroupBy)
	}
	if len(data.Sessions) != 3 {
		t.Fatalf("provider 分组应有 3 行（minimax/deepseek/未知）, got %d", len(data.Sessions))
	}
	// 会话数必须是**会话数**（4），不是分组数（3）
	if data.Totals.Sessions != 4 {
		t.Fatalf("totals.sessions 应为 4（窗口内活动会话数）, got %d", data.Totals.Sessions)
	}
}

// TestAPI_ProviderShareUnderProjectGroup 断言 group=project 时 provider 占比
// 仍是**真聚合**（不是"每个 project 分组归给其中一个 provider"）。
func TestAPI_ProviderShareUnderProjectGroup(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	data := fetchStats(t, client, ts.URL+"/api/stats?group=project")
	if len(data.Providers) != 3 {
		t.Fatalf("provider 占比应有 3 项（minimax/deepseek/未知）, got %d: %+v",
			len(data.Providers), data.Providers)
	}
	var sum float64
	for _, p := range data.Providers {
		sum += p.TokenShare
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("provider 占比之和应为 1, got %v", sum)
	}
}

// TestAPI_ProviderBreakdown 断言 providers 占比数组存在且每个 provider 一项。
func TestAPI_ProviderBreakdown(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	data := fetchStats(t, client, ts.URL+"/api/stats")
	if len(data.Providers) != 3 {
		t.Fatalf("providers 应有 3 项, got %d: %+v", len(data.Providers), data.Providers)
	}
	if len(data.Projects) != 3 {
		t.Fatalf("projects 应有 3 项, got %d", len(data.Projects))
	}
	if len(data.Models) != 3 {
		t.Fatalf("models 应有 3 项, got %d", len(data.Models))
	}
}

// TestAPI_NoLabelLeak 断言响应里不出现 labels 的值（主机指纹 / arch / os 版本）。
func TestAPI_NoLabelLeak(t *testing.T) {
	ts, srv, _ := newTestServer(t)
	jar, _ := newJar()
	client := &http.Client{Jar: jar}
	resp, _ := client.Get(ts.URL + "/ui?token=" + srv.Token())
	resp.Body.Close()

	raw := fetchRaw(t, client, ts.URL+"/api/stats")
	for _, secret := range []string{"fingerprint-secret", "x86_64", "6.1.0", "extra_labels"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("响应泄露了 label 内容 %q", secret)
		}
	}
}

// TestSessionCookie_Verify 断言签名校验：篡改 / 过期 / 未来签发都无效。
func TestSessionCookie_Verify(t *testing.T) {
	token := "0123456789abcdef"
	now := time.Now()
	value := SignSession(token, now)
	if !VerifySession(token, value, now) {
		t.Fatal("刚签发的 cookie 应有效")
	}
	if VerifySession(token, value, now.Add(SessionTTL+time.Minute)) {
		t.Fatal("过期 cookie 应无效")
	}
	if VerifySession(token, value, now.Add(-2*time.Hour)) {
		t.Fatal("未来签发的 cookie 应无效")
	}
	if VerifySession("other-token", value, now) {
		t.Fatal("换 token 后旧 cookie 应无效")
	}
	if VerifySession(token, value+"x", now) {
		t.Fatal("篡改的 cookie 应无效")
	}
}

// newJar 造一个空的 cookie jar。
func newJar() (http.CookieJar, error) {
	return cookiejar.New(nil)
}

// fetchStats 取一次 /api/stats 并解析。
func fetchStats(t *testing.T, client *http.Client, url string) apiResponse {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d", url, resp.StatusCode)
	}
	var data apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	return data
}

// fetchRaw 取一次 URL 的原始响应体。
func fetchRaw(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAssets_Embedded 断言三个前端资源都在二进制里且非空。
func TestAssets_Embedded(t *testing.T) {
	for _, name := range []string{"assets/index.html", "assets/app.js", "assets/style.css"} {
		data, err := assetFS.ReadFile(name)
		if err != nil {
			t.Fatalf("%s 未内嵌: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s 是空文件", name)
		}
	}
}

// TestAssets_JSSyntaxValid 用 node --check 校验 app.js 语法。
//
// 意义：看板完全由 JS 驱动，app.js 有语法错误时页面会**静默空白**（HTML 正常
// 返回、JS 不执行），没有单元测试能发现这种故障——只能靠解析器。
// 环境里没有 node 时跳过（不引入 node 依赖）。
func TestAssets_JSSyntaxValid(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("环境里没有 node，跳过 JS 语法检查")
	}
	data, err := assetFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "app.js")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", "--check", tmp).CombinedOutput()
	if err != nil {
		t.Fatalf("app.js 语法错误: %v\n%s", err, out)
	}
}
