package web

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// clientWithCookie 造一个带 cookie jar 的客户端（token → cookie 的重定向要靠它）。
func (e *consoleEnv) clientWithCookie(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

// TestUI_ServesShellWithThreeViews 断言 /ui 是"外壳 + 三视图"结构。
func TestUI_ServesShellWithThreeViews(t *testing.T) {
	e := newConsoleServer(t)
	client := e.clientWithCookie(t)

	resp, err := client.Get(e.ts.URL + "/ui?token=" + e.token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，期望 200", resp.StatusCode)
	}
	body := make([]byte, 1<<20)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	// 顶栏三个主入口
	for _, v := range []string{`data-view="usage"`, `data-view="providers"`, `data-view="claude"`} {
		if !strings.Contains(html, v) {
			t.Errorf("页面应含 %s", v)
		}
	}
	// 三个视图容器
	for _, id := range []string{`id="view-usage"`, `id="view-providers"`, `id="view-claude"`} {
		if !strings.Contains(html, id) {
			t.Errorf("页面应含 %s", id)
		}
	}
	// 左栏容器
	if !strings.Contains(html, `id="sidebar"`) {
		t.Error("页面应含左栏容器")
	}
	// 用量视图的原有元素一个都不能少（回归：整块搬过来，不许漏）
	for _, id := range []string{
		`id="kpis"`, `id="trend"`, `id="trend-table"`, `id="legend"`,
		`id="share-project"`, `id="share-provider"`, `id="share-model"`,
		`id="sessions"`, `id="f-project"`, `id="f-provider"`, `id="f-model"`,
		`id="refresh"`, `id="auto"`, `id="trend-toggle"`, `id="generated-at"`,
	} {
		if !strings.Contains(html, id) {
			t.Errorf("用量视图应保留 %s", id)
		}
	}
	// 主题开关搬到了外壳
	if !strings.Contains(html, `id="theme-toggle"`) {
		t.Error("页面应含主题开关")
	}
	// 脚本引用
	for _, s := range []string{"usage.js", "highlight.js", "editor.js", "providers.js", "claude.js", "app.js"} {
		if !strings.Contains(html, "/ui/"+s) {
			t.Errorf("页面应引用 /ui/%s", s)
		}
	}
}

// getAsset 取一个静态资源。
//
// 资源的鉴权走「URL 带 token → 302 换 cookie」——所以要用带 cookie jar 的
// 客户端跟完重定向。
func (e *consoleEnv) getAsset(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	client := e.clientWithCookie(t)
	resp, err := client.Get(e.ts.URL + path + "?token=" + e.token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<20)
	n, _ := resp.Body.Read(buf)
	return resp, string(buf[:n])
}

// TestAssets_AllServed 断言每个前端资源都能取到（类型正确）。
func TestAssets_AllServed(t *testing.T) {
	e := newConsoleServer(t)
	cases := map[string]string{
		"app.js":       "application/javascript",
		"usage.js":     "application/javascript",
		"providers.js": "application/javascript",
		"claude.js":    "application/javascript",
		"editor.js":    "application/javascript",
		"highlight.js": "application/javascript",
		"style.css":    "text/css",
	}
	for name, wantType := range cases {
		resp, body := e.getAsset(t, "/ui/"+name)
		if resp.StatusCode != 200 {
			t.Errorf("%s 状态 = %d，期望 200", name, resp.StatusCode)
			continue
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, wantType) {
			t.Errorf("%s Content-Type = %q，期望含 %q", name, ct, wantType)
		}
		if len(body) == 0 {
			t.Errorf("%s 内容为空", name)
		}
	}
}

// TestAssets_RejectsNonAsset 断言资源路由不服务别的路径。
func TestAssets_RejectsNonAsset(t *testing.T) {
	e := newConsoleServer(t)
	for _, path := range []string{
		"/ui/index.html", // 页面只走 /ui
		"/ui/../web.go",  // 穿越
		"/ui/sub/dir.js", // 子目录
	} {
		resp, _ := e.getAsset(t, path)
		if resp.StatusCode != 404 {
			t.Errorf("%s 状态 = %d，期望 404", path, resp.StatusCode)
		}
	}
}

// TestHighlight_ServesJSONHighlighter 断言高亮器在资源里（防止忘了 embed）。
func TestAssets_HighlighterEmbedded(t *testing.T) {
	e := newConsoleServer(t)
	_, body := e.getAsset(t, "/ui/highlight.js")
	if !strings.Contains(body, "highlightJSON") {
		t.Error("highlight.js 应含 highlightJSON")
	}
}

// TestAssets_JSTestNotEmbedded 断言前端单测**不会**进二进制、也不会被路由服务。
//
// internal/web/jstest/ 下是 node 单测（highlight.js 的转义与 tokenizer）。
// 它们必须在 assets/ 之外——
//   - go:embed 只列了具体文件，jstest/ 不在其中 → 不进二进制（发布产物的字节级
//     可重现约定依赖这个：多 embed 一个文件就是多一份字节）
//   - /ui/<asset> 只服务 embed 进去的单层文件 → 路由也取不到
//
// 将来有人把测试挪进 assets/（或者把 embed 改成整个目录）时，这条会红。
func TestAssets_JSTestNotEmbedded(t *testing.T) {
	entries, err := assetFS.ReadDir("assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if strings.Contains(ent.Name(), ".test.") {
			t.Errorf("assets/ 下不该有测试文件（会被 embed 进二进制、也会被 /ui 路由服务）: %s", ent.Name())
		}
	}

	e := newConsoleServer(t)
	for _, p := range []string{"/ui/jstest/highlight.test.mjs", "/ui/highlight.test.mjs"} {
		resp, _ := e.getAsset(t, p)
		if resp.StatusCode != 404 {
			t.Errorf("%s 状态 = %d，期望 404", p, resp.StatusCode)
		}
	}
}

// TestAssets_UsageViewLogicIntact 断言用量视图的渲染逻辑还在（回归）。
//
// usage.js 是从原 app.js 整体搬来的——这里钉住几个关键渲染函数，防止
// 将来重构时把哪一块删了。
func TestAssets_UsageViewLogicIntact(t *testing.T) {
	e := newConsoleServer(t)
	_, body := e.getAsset(t, "/ui/usage.js")
	for _, fn := range []string{
		"function renderKpis", "function renderTrend", "function renderShare",
		"function renderTrendTable", "function renderSessions", "function load(",
	} {
		if !strings.Contains(body, fn) {
			t.Errorf("usage.js 应保留 %s", fn)
		}
	}
}
