package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cc-launcher/internal/provider"
	"cc-launcher/internal/quota"
)

// consoleEnv 是控制台测试的环境（临时目录 + provider / Claude / 额度）。
type consoleEnv struct {
	ts           *httptest.Server
	token        string
	dir          string
	providersDir string
	backupsDir   string
	settingsPath string
	memoryPath   string
	store        *quota.Store
	pm           *provider.Manager
}

// newConsoleServer 造一个带 provider / Claude / 额度的控制台 server。
func newConsoleServer(t *testing.T) *consoleEnv {
	t.Helper()
	dir := t.TempDir()
	// 把主目录指到临时目录：claude 文件的 path 落在 home 下，脱敏成 ~/... 才有
	// 东西可断言；同时也让这批测试不受运行机器真实 HOME 的影响。
	// os.UserHomeDir() 在 Unix 读 $HOME、在 Windows 读 %USERPROFILE%，两个都设。
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	providersDir := filepath.Join(dir, "providers")
	backupsDir := filepath.Join(dir, "backups")
	settingsPath := filepath.Join(dir, "settings.json")
	memoryPath := filepath.Join(dir, "CLAUDE.md")
	tokenPath := filepath.Join(dir, TokenFileName)
	dbPath := filepath.Join(dir, "telemetry.db")
	makeTestDB(t, dbPath)

	pm := provider.NewManager(providersDir)
	pm.SetBackupDir(backupsDir)
	store := quota.NewStore(dir)

	srv, err := NewServer(Options{
		DBPath:             dbPath,
		TokenPath:          tokenPath,
		Providers:          pm,
		ClaudeSettingsPath: settingsPath,
		ClaudeMemoryPath:   memoryPath,
		BackupDir:          backupsDir,
		QuotaStore:         store,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	tok, err := LoadOrCreateToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	return &consoleEnv{
		ts: ts, token: tok, dir: dir,
		providersDir: providersDir, backupsDir: backupsDir,
		settingsPath: settingsPath, memoryPath: memoryPath,
		store: store, pm: pm,
	}
}

// do 发一个请求（带 token）。
//
// 写方法一律带 Content-Type: application/json —— 这是 writeGuard 的硬要求
// （防 CSRF：跨站表单发不出这个类型）。
func (e *consoleEnv) do(t *testing.T, method, path string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	isWrite := method == http.MethodPost || method == http.MethodPut || method == http.MethodDelete

	var rdr io.Reader
	switch v := body.(type) {
	case nil:
		if isWrite && method != http.MethodDelete {
			rdr = strings.NewReader("{}")
		}
	case string:
		rdr = strings.NewReader(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, e.ts.URL+path+"?token="+e.token, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if isWrite {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

// writeProvider 写一个 provider 配置文件。
func (e *consoleEnv) writeProvider(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(e.providersDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.providersDir, name+".json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// providerContent 造一个合法的 provider 配置。
const providerContent = `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "sk-test",
    "ANTHROPIC_MODEL": "deepseek-flash"
  },
  "note": "测试用",
  "order": 5
}`

// ---------------------------------------------------------------------------
// 列表与读取
// ---------------------------------------------------------------------------

func TestAPIProviders_List(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)
	e.writeProvider(t, "plain", `{"env":{"ANTHROPIC_AUTH_TOKEN":"k"}}`)

	resp, body := e.do(t, http.MethodGet, "/api/providers", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, body)
	}
	var items []providerItem
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("provider 数 = %d，期望 2", len(items))
	}
	// order 小的排前面
	if items[0].Name != "plain" || items[1].Name != "iflytek" {
		t.Fatalf("排序不对: %+v", items)
	}
	if items[1].Note != "测试用" {
		t.Fatalf("note = %q", items[1].Note)
	}
	if !items[1].HasCredential {
		t.Fatal("有 ANTHROPIC_AUTH_TOKEN 的 provider 应该 has_credential")
	}
	if items[1].HasQuota {
		t.Fatal("没配 quota 块的 provider 不该 has_quota")
	}
}

// TestAPIProviders_List_Empty 断言空目录返回**空列表**（`[]`）而不是 `null`。
//
// 任务 2.2 明确要求这条。前端 `items || []` 会把 null 悄悄吞掉，但返回 null
// 意味着"一个 provider 都没有"和"接口挂了"在响应体上长得一样。
func TestAPIProviders_List_Empty(t *testing.T) {
	e := newConsoleServer(t)

	resp, body := e.do(t, http.MethodGet, "/api/providers", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, body)
	}
	if trimmed := strings.TrimSpace(string(body)); trimmed != "[]" {
		t.Fatalf("空目录应返回 []，实际 = %s", trimmed)
	}
	var items []providerItem
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("provider 数 = %d，期望 0", len(items))
	}
}

// TestAPIProviders_List_NoteDefaultsToEmpty 断言没写 note 的 provider 返回空串
// （不是 null，也不是把字段省掉）。
//
// 任务 2.2 明确要求这条。前端左栏用它当副标题（`providers.js:225`）：
// `if (p.note)` —— 空串与 null 行为一致，但字段被省掉会让前端分不清
// "没有 note"和"后端还没实现这个字段"。
func TestAPIProviders_List_NoteDefaultsToEmpty(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "plain", `{"env":{"ANTHROPIC_AUTH_TOKEN":"k"}}`)

	resp, body := e.do(t, http.MethodGet, "/api/providers", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, body)
	}
	var items []providerItem
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("provider 数 = %d，期望 1", len(items))
	}
	if items[0].Note != "" {
		t.Fatalf("note = %q，期望空串", items[0].Note)
	}
	// 字段本身要在响应里（没被 omitempty 掉）
	if !strings.Contains(string(body), `"note":""`) {
		t.Fatalf("响应应含 \"note\":\"\"：%s", body)
	}
}

func TestAPIProvider_Read_ReturnsRawText(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	resp, body := e.do(t, http.MethodGet, "/api/providers/iflytek", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d", resp.StatusCode)
	}
	var env fileEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Content != providerContent {
		t.Fatalf("内容不逐字节一致:\n got %q\nwant %q", env.Content, providerContent)
	}
	if env.ETag == "" {
		t.Fatal("etag 不能为空")
	}
	if resp.Header.Get("ETag") != env.ETag {
		t.Fatal("ETag 头与响应体不一致")
	}
}

func TestAPIProvider_Read_Unknown(t *testing.T) {
	e := newConsoleServer(t)
	resp, _ := e.do(t, http.MethodGet, "/api/providers/nope", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("状态 = %d，期望 404", resp.StatusCode)
	}
}

// TestAPIProvider_Read_Traversal 断言路径穿越被挡（不是 500，是 404）。
func TestAPIProvider_Read_Traversal(t *testing.T) {
	e := newConsoleServer(t)
	for _, name := range []string{"..%2F..%2Fetc%2Fpasswd", "foo%2Fbar", ".."} {
		resp, _ := e.do(t, http.MethodGet, "/api/providers/"+name, nil, nil)
		if resp.StatusCode != 404 {
			t.Fatalf("%q 状态 = %d，期望 404", name, resp.StatusCode)
		}
	}
}

// ---------------------------------------------------------------------------
// 写入
// ---------------------------------------------------------------------------

func TestAPIProviderPut_Success(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	_, body := e.do(t, http.MethodGet, "/api/providers/iflytek", nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)

	newContent := `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-new"}}`
	resp, respBody := e.do(t, http.MethodPut, "/api/providers/iflytek",
		writeEnvelope{Content: newContent},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, respBody)
	}
	data, err := os.ReadFile(filepath.Join(e.providersDir, "iflytek.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != newContent {
		t.Fatalf("落盘内容 = %q，期望 %q", data, newContent)
	}
	// 旧内容进了备份
	matches, _ := filepath.Glob(filepath.Join(e.backupsDir, "iflytek.json-*.bak"))
	if len(matches) != 1 {
		t.Fatalf("备份数 = %d，期望 1", len(matches))
	}
	old, _ := os.ReadFile(matches[0])
	if string(old) != providerContent {
		t.Fatalf("备份内容不对: %q", old)
	}
}

func TestAPIProviderPut_StaleETag_Conflict(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	// 客户端拿着旧 etag（这里伪造一个错的）
	resp, body := e.do(t, http.MethodPut, "/api/providers/iflytek",
		writeEnvelope{Content: `{"env":{"ANTHROPIC_AUTH_TOKEN":"x"}}`},
		map[string]string{"If-Match": "deadbeefdeadbeef"})
	if resp.StatusCode != 409 {
		t.Fatalf("状态 = %d，期望 409，body = %s", resp.StatusCode, body)
	}
	var conflict conflictBody
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Content != providerContent {
		t.Fatal("409 应该带回当前文件内容")
	}
	// 文件没变
	data, _ := os.ReadFile(filepath.Join(e.providersDir, "iflytek.json"))
	if string(data) != providerContent {
		t.Fatalf("文件被改了: %q", data)
	}
}

// TestAPIProviderPut_MissingIfMatch_Conflict 断言**不带 If-Match** 的写入被拒。
//
// 任务 3.2 要求"If-Match 必填"。缺 If-Match 走到乐观锁那一步，
// etagMatches("" , 当前) 返回 false → 409（providers.go:136）。
//
// 这条不能省：If-Match 是"别覆盖别人的改动"的唯一开关，它一旦变成可选，
// 网页就能静默覆盖 Claude Code 刚写进 settings.json 的内容。
func TestAPIProviderPut_MissingIfMatch_Conflict(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	resp, body := e.do(t, http.MethodPut, "/api/providers/iflytek",
		writeEnvelope{Content: `{"env":{"ANTHROPIC_AUTH_TOKEN":"x"}}`}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("状态 = %d，期望 409（不带 If-Match 不该写盘），body = %s", resp.StatusCode, body)
	}
	data, _ := os.ReadFile(filepath.Join(e.providersDir, "iflytek.json"))
	if string(data) != providerContent {
		t.Fatalf("文件被改了: %q", data)
	}
}

func TestAPIProviderPut_InvalidSchema_Rejected(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	_, body := e.do(t, http.MethodGet, "/api/providers/iflytek", nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)

	// env 不是对象 → 已知键类型错 → Claude Code 会丢整个 settings 文件
	resp, respBody := e.do(t, http.MethodPut, "/api/providers/iflytek",
		writeEnvelope{Content: `{"env":"not-an-object"}`},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 400 {
		t.Fatalf("状态 = %d，期望 400，body = %s", resp.StatusCode, respBody)
	}
	var vb validationBody
	if err := json.Unmarshal(respBody, &vb); err != nil {
		t.Fatal(err)
	}
	if len(vb.Errors) == 0 {
		t.Fatal("应该带错误列表")
	}
	// 文件没变
	data, _ := os.ReadFile(filepath.Join(e.providersDir, "iflytek.json"))
	if string(data) != providerContent {
		t.Fatalf("文件被改了: %q", data)
	}
}

func TestAPIProviderPut_MalformedJSON_Rejected(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	_, body := e.do(t, http.MethodGet, "/api/providers/iflytek", nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)

	resp, _ := e.do(t, http.MethodPut, "/api/providers/iflytek",
		writeEnvelope{Content: `{not json`},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 400 {
		t.Fatalf("状态 = %d，期望 400", resp.StatusCode)
	}
	data, _ := os.ReadFile(filepath.Join(e.providersDir, "iflytek.json"))
	if string(data) != providerContent {
		t.Fatalf("文件被改了: %q", data)
	}
}

func TestAPIProviderCreate_FromTemplate(t *testing.T) {
	e := newConsoleServer(t)

	resp, body := e.do(t, http.MethodPost, "/api/providers",
		createProviderRequest{Name: "my-deepseek", Template: "deepseek"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, body)
	}
	data, err := os.ReadFile(filepath.Join(e.providersDir, "my-deepseek.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ANTHROPIC_BASE_URL") {
		t.Fatalf("模板内容不对: %s", data)
	}
}

func TestAPIProviderCreate_DuplicateName(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	resp, _ := e.do(t, http.MethodPost, "/api/providers",
		createProviderRequest{Name: "iflytek", Template: "deepseek"}, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("状态 = %d，期望 409", resp.StatusCode)
	}
}

func TestAPIProviderCreate_InvalidName(t *testing.T) {
	e := newConsoleServer(t)
	for _, name := range []string{"../evil", "foo/bar", ""} {
		resp, _ := e.do(t, http.MethodPost, "/api/providers",
			createProviderRequest{Name: name, Template: "deepseek"}, nil)
		if resp.StatusCode != 400 {
			t.Fatalf("名字 %q 状态 = %d，期望 400", name, resp.StatusCode)
		}
	}
}

// TestAPIProviderCreate_WithContent 断言 `{name, content}` 形态的创建。
//
// spec：body 可以是"模板名"**或**"完整配置文本 + 名字"（spec.md:81）。
// 只测模板分支的话，"直接贴一段 JSON 进来"这条路径就是裸奔的。
//
// 同时断言**content 优先于 template**（两个都给时用 content）——
// 顺序不能反，否则用户贴了 JSON 却拿到模板骨架，存盘时才发现。
func TestAPIProviderCreate_WithContent(t *testing.T) {
	e := newConsoleServer(t)

	content := `{"env":{"ANTHROPIC_BASE_URL":"https://api.example.com/anthropic","ANTHROPIC_AUTH_TOKEN":"sk-x"},"note":"手写的"}`
	resp, body := e.do(t, http.MethodPost, "/api/providers",
		createProviderRequest{Name: "custom", Template: "deepseek", Content: content}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, body)
	}
	// 落盘内容逐字节等于给的内容（content 优先于 template）
	data, err := os.ReadFile(filepath.Join(e.providersDir, "custom.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("落盘内容 =\n%q\n期望\n%q", data, content)
	}
}

// TestAPIProviderCreate_EmptyBody_Rejected 断言既没 template 也没 content → 400。
//
// 两种建法都没有 → 不该凭空造一个空 provider 出来。
func TestAPIProviderCreate_EmptyBody_Rejected(t *testing.T) {
	e := newConsoleServer(t)

	resp, body := e.do(t, http.MethodPost, "/api/providers",
		createProviderRequest{Name: "empty"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("状态 = %d，期望 400，body = %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(e.providersDir, "empty.json")); err == nil {
		t.Fatal("不该创建出 provider 文件")
	}
}

func TestAPIProviderCopy(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	resp, body := e.do(t, http.MethodPost, "/api/providers/iflytek/copy",
		createProviderRequest{Name: "iflytek-copy"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, body)
	}
	data, err := os.ReadFile(filepath.Join(e.providersDir, "iflytek-copy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != providerContent {
		t.Fatalf("复制内容不对: %q", data)
	}
}

func TestAPIProviderDelete_RemovesQuotaEntry(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)
	e.writeProvider(t, "other", `{"env":{"ANTHROPIC_AUTH_TOKEN":"k"}}`)

	// 造一条额度条目
	if err := e.store.Update("iflytek", "newapi", "pool-x", []quota.Dimension{
		{ID: "quota", Label: "USD", Kind: quota.KindQuota},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Update("other", "newapi", "pool-y", []quota.Dimension{
		{ID: "quota", Label: "USD", Kind: quota.KindQuota},
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := e.do(t, http.MethodDelete, "/api/providers/iflytek", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(e.providersDir, "iflytek.json")); err == nil {
		t.Fatal("配置文件应该被删了")
	}
	file, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file.EntryFor("iflytek"); ok {
		t.Fatal("额度条目应该被清掉了")
	}
	if _, ok := file.EntryFor("other"); !ok {
		t.Fatal("别的 provider 的条目不该被清掉")
	}
}

// TestAPIProviderDelete_NotFound 断言删一个**不存在的** provider → 404，
// 且**不误删别的 provider 的额度条目**。
//
// 任务 3.5 明确要求"provider 不存在 → 404"这一半；spec 另有更硬的一句：
// "The delete endpoint MUST NOT remove a provider's quota entry when the
// provider does not exist."（spec.md:87）
//
// 为什么盯这条：删除的实现是"先删文件，再 prune 掉所有活着的 provider 之外的
// 额度条目"。如果哪天 404 分支被改成"照样 prune"，就会把**别的** provider 的
// 额度数据清掉——删一个不存在的名字，把别人的额度搞没了。
func TestAPIProviderDelete_NotFound(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "other", `{"env":{"ANTHROPIC_AUTH_TOKEN":"k"}}`)

	// 造一条别的 provider 的额度条目
	if err := e.store.Update("other", "newapi", "pool-y", []quota.Dimension{
		{ID: "quota", Label: "USD", Kind: quota.KindQuota},
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := e.do(t, http.MethodDelete, "/api/providers/nope", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("状态 = %d，期望 404，body = %s", resp.StatusCode, body)
	}
	// 别的 provider 的额度条目不该被清掉
	file, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file.EntryFor("other"); !ok {
		t.Fatal("别的 provider 的额度条目不该被清掉")
	}
	// 别的 provider 的配置文件也不该被动
	if _, err := os.Stat(filepath.Join(e.providersDir, "other.json")); err != nil {
		t.Fatalf("other.json 还在吗: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 额度
// ---------------------------------------------------------------------------

// quotaProviderContent 造一个声明了额度接口的 provider。
func quotaProviderContent(endpoint string) string {
	return `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "sk-test"
  },
  "quota": {"source": "deepseek", "endpoint": "` + endpoint + `"}
}`
}

func TestAPIProviderQuota_ThreeStates(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "p1", quotaProviderContent("https://example.invalid/balance"))

	// 1) 没有条目 → found=false, has_quota=true（"没采到" ≠ "无限额"）
	resp, body := e.do(t, http.MethodGet, "/api/providers/p1/quota", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d", resp.StatusCode)
	}
	var q quotaResponse
	if err := json.Unmarshal(body, &q); err != nil {
		t.Fatal(err)
	}
	if q.Found {
		t.Fatal("没有条目时 found 应该是 false")
	}
	if !q.HasQuota {
		t.Fatal("声明了额度接口 → has_quota 应该是 true")
	}

	// 2) 新鲜条目
	used, limit := 30.0, 100.0
	if err := e.store.Update("p1", "deepseek", "pool-1", []quota.Dimension{
		{ID: "quota", Label: "USD", Kind: quota.KindQuota, Used: &used, Limit: &limit},
	}); err != nil {
		t.Fatal(err)
	}
	_, body = e.do(t, http.MethodGet, "/api/providers/p1/quota", nil, nil)
	_ = json.Unmarshal(body, &q)
	if !q.Found || q.Stale {
		t.Fatalf("新鲜条目应该是 found=true stale=false: %+v", q)
	}
	if len(q.Dimensions) != 1 {
		t.Fatalf("维度数 = %d", len(q.Dimensions))
	}

	// 3) 过期条目（fetched_at 是 10 分钟前）
	//
	// 直接写数据文件：Store.UpdateEntry 会把 fetched_at 盖成 now
	// （那是刷新路径要的语义，这里要的是"造一条旧数据"）。
	file, _ := e.store.Load()
	entry, _ := file.EntryFor("p1")
	entry.FetchedAt = time.Now().Add(-10 * time.Minute).Unix()
	file.Entries["p1"] = entry
	if err := e.store.Save(file); err != nil {
		t.Fatal(err)
	}
	_, body = e.do(t, http.MethodGet, "/api/providers/p1/quota", nil, nil)
	_ = json.Unmarshal(body, &q)
	if !q.Found || !q.Stale {
		t.Fatalf("过期条目应该是 found=true stale=true: %+v", q)
	}
}

func TestAPIProviderQuota_NoQuotaInterface(t *testing.T) {
	e := newConsoleServer(t)
	// base_url 识别不出任何 adapter，且没配 quota 块 → 没有额度接口
	e.writeProvider(t, "p1", `{"env":{"ANTHROPIC_BASE_URL":"https://example.invalid/anthropic","ANTHROPIC_AUTH_TOKEN":"k"}}`)

	_, body := e.do(t, http.MethodGet, "/api/providers/p1/quota", nil, nil)
	var q quotaResponse
	_ = json.Unmarshal(body, &q)
	if q.Found || q.HasQuota {
		t.Fatalf("没有额度接口应该 found=false has_quota=false: %+v", q)
	}
}

func TestAPIProviderQuotaRefresh_Success(t *testing.T) {
	// 假厂商端点
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"5.17","granted_balance":"0.16","topped_up_balance":"5.01"}]}`))
	}))
	defer fake.Close()

	e := newConsoleServer(t)
	e.writeProvider(t, "p1", quotaProviderContent(fake.URL))

	resp, body := e.do(t, http.MethodPost, "/api/providers/p1/quota/refresh", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, body)
	}
	var q quotaResponse
	if err := json.Unmarshal(body, &q); err != nil {
		t.Fatal(err)
	}
	if !q.Found || len(q.Dimensions) != 1 {
		t.Fatalf("刷新结果不对: %+v", q)
	}
	// 值写回了数据文件
	file, _ := e.store.Load()
	entry, ok := file.EntryFor("p1")
	if !ok {
		t.Fatal("数据文件里没有条目")
	}
	if entry.Dimensions[0].Remaining == nil {
		t.Fatal("维度应该有剩余量")
	}
}

func TestAPIProviderQuotaRefresh_Backoff(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "p1", quotaProviderContent("https://example.invalid/balance"))

	// 造退避状态：算出这个 provider 的额度池
	ref, hasQuota, err := quota.LoadProviderRef(e.providersDir, "p1")
	if err != nil || !hasQuota {
		t.Fatalf("载入 provider 失败: %v (hasQuota=%v)", err, hasQuota)
	}
	ad, endpoint, err := ref.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	pool := quota.PoolID(endpoint, ref.APIKey)
	if err := e.store.SetBackoff(pool, time.Now().Add(120*time.Second).Unix(), 120); err != nil {
		t.Fatal(err)
	}
	_ = ad

	resp, body := e.do(t, http.MethodPost, "/api/providers/p1/quota/refresh", nil, nil)
	if resp.StatusCode != 429 {
		t.Fatalf("状态 = %d，期望 429，body = %s", resp.StatusCode, body)
	}
	var errBody map[string]any
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if _, ok := errBody["retry_after"]; !ok {
		t.Fatalf("应该带 retry_after: %s", body)
	}
}

// TestAPIProviderQuotaRefresh_FailureKeepsPrevious 断言**刷新失败时旧条目保留**。
//
// spec 场景 "a failed refresh keeps the previous values"（spec.md:179-182）：
// 厂商接口报错时，数据文件里应该还留着上一次的额度（慢慢变 stale），
// 而不是被清空或写成 0 —— "显示 0"会被用户读成"额度用完了"。
//
// 用 500 而不是 429：429 会触发退避（另一个测试覆盖），这里要的是
// "普通失败"这条路径——quota.RefreshOne 在取数失败时提前 return，
// 走不到写数据文件那一步（internal/quota/refresh.go:875-886）。
func TestAPIProviderQuotaRefresh_FailureKeepsPrevious(t *testing.T) {
	// 假厂商端点：总是 500
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer fake.Close()

	e := newConsoleServer(t)
	e.writeProvider(t, "p1", quotaProviderContent(fake.URL))

	// 先造一条旧条目
	used, limit := 30.0, 100.0
	if err := e.store.Update("p1", "deepseek", "pool-1", []quota.Dimension{
		{ID: "quota", Label: "USD", Kind: quota.KindQuota, Used: &used, Limit: &limit},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}

	resp, body := e.do(t, http.MethodPost, "/api/providers/p1/quota/refresh", nil, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("状态 = %d，期望 502，body = %s", resp.StatusCode, body)
	}

	// 旧条目还在，值没变
	after, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := after.EntryFor("p1")
	if !ok {
		t.Fatal("刷新失败后旧条目应该还在")
	}
	oldEntry, _ := before.EntryFor("p1")
	if len(entry.Dimensions) != len(oldEntry.Dimensions) {
		t.Fatalf("维度数变了: %d → %d", len(oldEntry.Dimensions), len(entry.Dimensions))
	}
	if len(entry.Dimensions) > 0 {
		got := entry.Dimensions[0]
		if got.Used == nil || *got.Used != 30.0 {
			t.Fatalf("旧值被改了: used = %v", got.Used)
		}
		if got.Limit == nil || *got.Limit != 100.0 {
			t.Fatalf("旧值被改了: limit = %v", got.Limit)
		}
	}
}

// ---------------------------------------------------------------------------
// Claude 配置
// ---------------------------------------------------------------------------

func TestAPIClaudeFiles_List(t *testing.T) {
	e := newConsoleServer(t)
	resp, body := e.do(t, http.MethodGet, "/api/claude/files", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d", resp.StatusCode)
	}
	var items []claudeFileItem
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("文件数 = %d，期望 2", len(items))
	}
	if items[0].ID != ClaudeFileSettings || items[1].ID != ClaudeFileMemory {
		t.Fatalf("文件标识不对: %+v", items)
	}
}

// TestAPIClaudeFiles_PathIsTildeForm 断言文件列表返回的 path 是 ~ 形式，
// 且响应体里不含主目录的绝对路径。
//
// 为什么这条要单独断言：/api/claude/files 是**局域网可访问**的接口（daemon 监听
// :4318 全网卡），绝对路径会把用户名暴露给同网段拿到 token 的人。
func TestAPIClaudeFiles_PathIsTildeForm(t *testing.T) {
	e := newConsoleServer(t)
	resp, body := e.do(t, http.MethodGet, "/api/claude/files", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d", resp.StatusCode)
	}
	var items []claudeFileItem
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("文件数 = %d，期望 2", len(items))
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Path, "~/") {
			t.Errorf("%s 的 path 应以 ~/ 开头, got %q", it.ID, it.Path)
		}
		if strings.Contains(it.Path, e.dir) {
			t.Errorf("%s 的 path 不该含主目录绝对路径 %q, got %q", it.ID, e.dir, it.Path)
		}
	}
	// 整个响应体里都不能出现主目录
	if strings.Contains(string(body), e.dir) {
		t.Errorf("响应体不该含主目录绝对路径 %q, got:\n%s", e.dir, body)
	}
	// 顺带确认脱敏的是**展示值**：列表里显示 ~/...，但读写仍走真实绝对路径
	if err := os.WriteFile(e.settingsPath, []byte(`{"a":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, fbody := e.do(t, http.MethodGet, "/api/claude/file/"+ClaudeFileSettings, nil, nil)
	var env fileEnvelope
	if err := json.Unmarshal(fbody, &env); err != nil {
		t.Fatal(err)
	}
	if env.Content != `{"a":1}` {
		t.Errorf("按真实路径读回的内容 = %q，期望 %q", env.Content, `{"a":1}`)
	}
}

func TestAPIClaudeFile_UnknownID(t *testing.T) {
	e := newConsoleServer(t)
	resp, _ := e.do(t, http.MethodGet, "/api/claude/file/anything-else", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("状态 = %d，期望 404", resp.StatusCode)
	}
}

func TestAPIClaudeFile_ReadAndWrite(t *testing.T) {
	e := newConsoleServer(t)
	if err := os.WriteFile(e.memoryPath, []byte("# 旧内容\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, body := e.do(t, http.MethodGet, "/api/claude/file/"+ClaudeFileMemory, nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)
	if env.Content != "# 旧内容\n" {
		t.Fatalf("内容 = %q", env.Content)
	}

	resp, respBody := e.do(t, http.MethodPut, "/api/claude/file/"+ClaudeFileMemory,
		writeEnvelope{Content: "# 新内容\n"},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, respBody)
	}
	data, _ := os.ReadFile(e.memoryPath)
	if string(data) != "# 新内容\n" {
		t.Fatalf("落盘 = %q", data)
	}
	// 备份
	matches, _ := filepath.Glob(filepath.Join(e.backupsDir, "CLAUDE.md-*.bak"))
	if len(matches) != 1 {
		t.Fatalf("备份数 = %d，期望 1", len(matches))
	}
}

func TestAPIClaudeFile_StaleETag(t *testing.T) {
	e := newConsoleServer(t)
	if err := os.WriteFile(e.memoryPath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	resp, body := e.do(t, http.MethodPut, "/api/claude/file/"+ClaudeFileMemory,
		writeEnvelope{Content: "new"},
		map[string]string{"If-Match": "deadbeefdeadbeef"})
	if resp.StatusCode != 409 {
		t.Fatalf("状态 = %d，期望 409，body = %s", resp.StatusCode, body)
	}
	data, _ := os.ReadFile(e.memoryPath)
	if string(data) != "old" {
		t.Fatalf("文件被改了: %q", data)
	}
}

func TestAPIClaudeFile_SettingsInvalidSchema(t *testing.T) {
	e := newConsoleServer(t)
	if err := os.WriteFile(e.settingsPath, []byte(`{"env":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(t, http.MethodGet, "/api/claude/file/"+ClaudeFileSettings, nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)

	resp, respBody := e.do(t, http.MethodPut, "/api/claude/file/"+ClaudeFileSettings,
		writeEnvelope{Content: `{"env":"not-an-object"}`},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 400 {
		t.Fatalf("状态 = %d，期望 400，body = %s", resp.StatusCode, respBody)
	}
	data, _ := os.ReadFile(e.settingsPath)
	if string(data) != `{"env":{}}` {
		t.Fatalf("文件被改了: %q", data)
	}
}

func TestAPIClaudeFile_SettingsValid(t *testing.T) {
	e := newConsoleServer(t)
	if err := os.WriteFile(e.settingsPath, []byte(`{"env":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(t, http.MethodGet, "/api/claude/file/"+ClaudeFileSettings, nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)

	newContent := "{\n  \"env\": {\n    \"API_TIMEOUT_MS\": \"3000000\"\n  }\n}\n"
	resp, respBody := e.do(t, http.MethodPut, "/api/claude/file/"+ClaudeFileSettings,
		writeEnvelope{Content: newContent},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, respBody)
	}
	data, _ := os.ReadFile(e.settingsPath)
	if string(data) != newContent {
		t.Fatalf("落盘 = %q", data)
	}
}

// ---------------------------------------------------------------------------
// 闸与鉴权
// ---------------------------------------------------------------------------

func TestWriteGuard_RejectsNonJSONContentType(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	req, err := http.NewRequest(http.MethodPut, e.ts.URL+"/api/providers/iflytek?token="+e.token,
		strings.NewReader(`{"content":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Fatalf("状态 = %d，期望 415", resp.StatusCode)
	}
}

func TestWriteGuard_RejectsCrossOrigin(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	req, err := http.NewRequest(http.MethodPut, e.ts.URL+"/api/providers/iflytek?token="+e.token,
		strings.NewReader(`{"content":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("状态 = %d，期望 403", resp.StatusCode)
	}
}

func TestWriteGuard_AllowsSameOrigin(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "iflytek", providerContent)

	_, body := e.do(t, http.MethodGet, "/api/providers/iflytek", nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)

	req, err := http.NewRequest(http.MethodPut, e.ts.URL+"/api/providers/iflytek?token="+e.token,
		strings.NewReader(`{"content":"{\"env\":{\"ANTHROPIC_AUTH_TOKEN\":\"x\"}}"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", e.ts.URL)
	req.Header.Set("If-Match", env.ETag)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("状态 = %d，期望 200，body = %s", resp.StatusCode, b)
	}
}

func TestConsole_RequiresAuth(t *testing.T) {
	e := newConsoleServer(t)
	for _, path := range []string{
		"/api/providers",
		"/api/providers/iflytek",
		"/api/claude/files",
		"/api/claude/file/settings",
	} {
		resp, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("%s 状态 = %d，期望 401", path, resp.StatusCode)
		}
	}
}

// TestAPIProviderQuota_TraversalRejected 断言额度接口也挡住路径穿越。
//
// quota.LoadProviderRef 自己不校验名字（直接拼路径读文件）——web 层
// 必须先过白名单，否则等于让它去读 providers 目录外的 .json。
func TestAPIProviderQuota_TraversalRejected(t *testing.T) {
	e := newConsoleServer(t)
	for _, name := range []string{"..%2F..%2Fetc%2Fpasswd", "foo%2Fbar"} {
		resp, _ := e.do(t, http.MethodGet, "/api/providers/"+name+"/quota", nil, nil)
		if resp.StatusCode != 404 {
			t.Fatalf("%q 额度接口状态 = %d，期望 404", name, resp.StatusCode)
		}
		resp, _ = e.do(t, http.MethodPost, "/api/providers/"+name+"/quota/refresh", nil, nil)
		if resp.StatusCode != 404 {
			t.Fatalf("%q 刷新接口状态 = %d，期望 404", name, resp.StatusCode)
		}
	}
}
