package web

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"cc-launcher/internal/stats"
)

// Options 配置看板 server。
type Options struct {
	// DBPath 是 telemetry.db 路径（只读打开）。
	DBPath string
	// TokenPath 是 token 文件路径（~/.cc-launcher/web-token）。
	TokenPath string
	// Now 是时钟注入点（测试用）；nil = time.Now。
	Now func() time.Time
}

// Server 是看板的 HTTP handler 集合。
//
// 由 cmd 层挂到 daemon 的 mux 上（collector.ServerOptions.Mount）——
// collector 不反向依赖 web，依赖方向单向。
type Server struct {
	opts  Options
	token string // 启动时读到的 token（兜底用）
}

// NewServer 构造 Server：加载（或首次生成）token。
func NewServer(opts Options) (*Server, error) {
	tok, err := LoadOrCreateToken(opts.TokenPath)
	if err != nil {
		return nil, err
	}
	return &Server{opts: opts, token: tok}, nil
}

// Token 返回当前 token（`ccl web` 打印 URL 用）。
func (s *Server) Token() string { return s.token }

// currentToken 返回**磁盘上**当前的 token。
//
// 每次请求都重读：`ccl web --rotate` 覆盖 token 文件后，daemon 必须立刻
// 用新 token 校验——否则"轮换即吊销"要等 daemon 重启才生效。
// 读失败时退回启动时缓存的值（token 文件短暂不可读不该把看板全锁死）。
func (s *Server) currentToken() string {
	if s.opts.TokenPath != "" {
		if data, err := os.ReadFile(s.opts.TokenPath); err == nil {
			if tok := strings.TrimSpace(string(data)); tok != "" {
				return tok
			}
		}
	}
	return s.token
}

func (s *Server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// Mount 把看板路由挂到 mux 上。
//
//	/ui        → 页面
//	/ui/<asset> → 静态资源
//	/api/stats → JSON 数据
func (s *Server) Mount(mux *http.ServeMux) {
	mux.Handle("/ui", s.protect(http.HandlerFunc(s.handleUI), false))
	mux.Handle("/ui/", s.protect(http.HandlerFunc(s.handleAsset), false))
	mux.Handle("/api/stats", s.protect(http.HandlerFunc(s.handleAPIStats), true))
}

// protect 是鉴权中间件。
//
// 流程：
//  1. 请求带 ?token=<tok> → 常量时间比对；命中则种 cookie 并 302 重定向到
//     去掉 query 的地址（token 不留在地址栏 / 浏览器历史 / Referer 里）
//  2. 否则校验 session cookie；有效则放行
//  3. 都没通过 → 401：页面返 HTML 提示，API 返 JSON {"error":"unauthorized"}
//
// jsonMode 决定未鉴权时的响应形态。
func (s *Server) protect(next http.Handler, jsonMode bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.currentToken()

		// 1) URL 换 cookie
		if tok := r.URL.Query().Get("token"); tok != "" {
			if !tokenEqual(tok, token) {
				s.unauthorized(w, jsonMode)
				return
			}
			if jsonMode {
				// API 不做重定向（重定向会把 fetch 变成 HTML）。直接放行。
				next.ServeHTTP(w, r)
				return
			}
			http.SetCookie(w, s.newSessionCookie(token))
			target := r.URL.Path
			http.Redirect(w, r, target, http.StatusFound)
			return
		}

		// 2) cookie 放行
		if c, err := r.Cookie(SessionCookieName); err == nil {
			if VerifySession(token, c.Value, s.now()) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// 3) 未鉴权
		s.unauthorized(w, jsonMode)
	})
}

// newSessionCookie 造一个 session cookie。
func (s *Server) newSessionCookie(token string) *http.Cookie {
	issued := s.now()
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    SignSession(token, issued),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// 不设 Secure——多数局域网场景走 http，设了 cookie 会被浏览器丢弃。
		MaxAge: int(SessionTTL / time.Second),
	}
}

// unauthorized 返回 401。
func (s *Server) unauthorized(w http.ResponseWriter, jsonMode bool) {
	if jsonMode {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(unauthorizedHTML))
}

// unauthorizedHTML 是未鉴权时页面侧的极简提示。
const unauthorizedHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>需要访问 token</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;margin:0;padding:3rem 1.5rem;
     background:#fbfbfd;color:#1c1c1e;line-height:1.6}
.card{max-width:32rem;margin:0 auto;background:#fff;border:1px solid #e4e4e7;border-radius:12px;padding:1.5rem}
code{background:#f4f4f5;padding:.15em .4em;border-radius:4px}
@media (prefers-color-scheme:dark){
  body{background:#111113;color:#f4f4f5}
  .card{background:#1c1c1e;border-color:#2e2e31}
  code{background:#2e2e31}
}
</style></head>
<body><div class="card">
<h1 style="font-size:1.15rem;margin:0 0 .75rem">需要访问 token</h1>
<p>看板受 token 保护。请在终端运行 <code>ccl web</code> 获取带 token 的访问 URL。</p>
</div></body></html>
`

// ---------------------------------------------------------------------------
// 数据接口
// ---------------------------------------------------------------------------

// apiResponse 是 /api/stats 的响应体。
//
// 字段白名单（web-usage-stats-ui §D11 / spec R6）：**不透传 extra_labels**——
// 那里含 user.id（主机指纹哈希）/ host.arch / os.version，直接 marshal 到
// 局域网可访问的 API 等于泄露主机指纹。
type apiResponse struct {
	Window      string       `json:"window"`
	Since       time.Time    `json:"since"`
	GroupBy     string       `json:"group_by"`
	GeneratedAt time.Time    `json:"generated_at"`
	Totals      apiTotals    `json:"totals"`
	Sessions    []apiSession `json:"sessions"`
	Buckets     []apiBucket  `json:"buckets"`
	Projects    []apiShare   `json:"projects"`
	Providers   []apiShare   `json:"providers"`
	Models      []apiShare   `json:"models"`
}

// apiTotals 是窗口内的汇总。
type apiTotals struct {
	Input         int64   `json:"input"`
	Output        int64   `json:"output"`
	CacheCreation int64   `json:"cache_creation"`
	CacheRead     int64   `json:"cache_read"`
	CostUSD       float64 `json:"cost_usd"`
	Sessions      int     `json:"sessions"`
}

// apiSession 是明细表的一行（与终端表列一一对应）。
type apiSession struct {
	StartedAt      time.Time `json:"started_at"`
	Project        string    `json:"project"`
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	Input          int64     `json:"input"`
	Output         int64     `json:"output"`
	CacheCreation  int64     `json:"cache_creation"`
	CacheRead      int64     `json:"cache_read"`
	CostUSD        float64   `json:"cost_usd"`
}

// apiBucket 是趋势图的一个时间桶。
type apiBucket struct {
	Label         string  `json:"label"`
	BucketISO     string  `json:"bucket_iso"`
	IsCurrent     bool    `json:"is_current"`
	Sessions      int64   `json:"sessions"`
	Input         int64   `json:"input"`
	Output        int64   `json:"output"`
	CacheCreation int64   `json:"cache_creation"`
	CacheRead     int64   `json:"cache_read"`
	CostUSD       float64 `json:"cost_usd"`
}

// apiShare 是占比表的一项。
type apiShare struct {
	Name      string  `json:"name"`
	Tokens    int64   `json:"tokens"`
	CostUSD   float64 `json:"cost_usd"`
	TokenShare float64 `json:"token_share"`
	CostShare  float64 `json:"cost_share"`
}

// unknownLabel 是"未知维度"的显示名。
//
// 空字符串不是合法维度名：直接 `claude` 启动的会话（没走 ccl）provider /
// cwd 为空，看板要显示"未知"，而不是显示一个空的维度桶
// （spec/session-context-attrs §"the launch context is absent for sessions
// started without ccl"）。
const unknownLabel = "未知"

// windowSpec 是一个窗口的语义定义。
type windowSpec struct {
	label string // "24h" / "7d" / ...
	days  int    // 对应 --day N 的 N
	hourly bool  // true = hourly 桶（24h 窗口）
}

// parseWindow 解析 window 参数；未知值返回 ok=false（→ 400）。
func parseWindow(v string) (windowSpec, bool) {
	switch v {
	case "":
		return windowSpec{label: "7d", days: 7}, true
	case "24h":
		return windowSpec{label: "24h", days: 1, hourly: true}, true
	case "7d":
		return windowSpec{label: "7d", days: 7}, true
	case "30d":
		return windowSpec{label: "30d", days: 30}, true
	case "90d":
		return windowSpec{label: "90d", days: 90}, true
	}
	return windowSpec{}, false
}

// parseGroup 解析 group 参数；未知值返回 ok=false（→ 400）。
func parseGroup(v string) (string, bool) {
	switch v {
	case "":
		return "session", true
	case "session", "project", "provider", "model":
		return v, true
	}
	return "", false
}

// handleAPIStats 处理 GET /api/stats。
func (s *Server) handleAPIStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()

	win, ok := parseWindow(q.Get("window"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown window: "+q.Get("window"))
		return
	}
	group, ok := parseGroup(q.Get("group"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown group: "+q.Get("group"))
		return
	}

	filter := stats.Query{
		Project:  q.Get("project"),
		Provider: q.Get("provider"),
		Model:    q.Get("model"),
	}
	days := win.days

	// 主查询：用户选的 group + 桶聚合
	main := filter
	main.Buckets = &days
	switch group {
	case "project":
		main.ByProject = true
	case "provider":
		main.ByProvider = true
	case "model":
		main.ByModel = true
	}

	// 占比用的两次分组查询：与主查询**同窗口同过滤**（Buckets 同样设上，
	// QueryStats 据此把 detail 起点对齐到最旧桶那天的午夜）。
	byProject := filter
	byProject.Buckets = &days
	byProject.ByProject = true

	byModel := filter
	byModel.Buckets = &days
	byModel.ByModel = true

	mainRes, err := stats.QueryStats(s.opts.DBPath, main)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	projRes, err := stats.QueryStats(s.opts.DBPath, byProject)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	modelRes, err := stats.QueryStats(s.opts.DBPath, byModel)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// provider 占比的口径必须与 CLI 的 --by-provider 一致（按 provider 真聚合）。
	//
	// 主查询的 provider 列能不能直接复用，取决于分组：
	//   - group=session：每行一个 session，按 provider 在内存里分组 == 真聚合
	//     （同一批 metric，只是切分方式不同）→ 直接复用，省一次查询
	//   - group=provider：主查询本身就是 provider 聚合 → 直接复用
	//   - group=project / model：每行是一个分组（一个 project / model），
	//     行的 provider 列只是该分组里**任意一个** provider → 直接复用会
	//     把多 provider 的分组错误归到其中一个 → 必须单独查一次
	providerRows := mainRes.Sessions
	if group == "project" || group == "model" {
		byProvider := filter
		byProvider.Buckets = &days
		byProvider.ByProvider = true
		providerRes, err := stats.QueryStats(s.opts.DBPath, byProvider)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		providerRows = providerRes.Sessions
	}

	// 会话数：**不能**用 len(mainRes.Sessions)——分组查询下那是"分组数"
	//（group=provider 时是 provider 个数），不是会话数。单独数一次窗口内
	// 有活动的 session（口径与 detail 行一致；Buckets 同样设上 → 窗口起点
	// 与主查询对齐）。
	countQ := filter
	countQ.Buckets = &days
	sessionCount, err := stats.CountSessionsInWindow(s.opts.DBPath, countQ)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := apiResponse{
		Window:      win.label,
		Since:       mainRes.Since,
		GroupBy:     group,
		GeneratedAt: s.now(),
		Totals: apiTotals{
			Input:         mainRes.Totals.InputTokens,
			Output:        mainRes.Totals.OutputTokens,
			CacheCreation: mainRes.Totals.CacheCreate,
			CacheRead:     mainRes.Totals.CacheRead,
			CostUSD:       mainRes.Totals.CostUSD,
			Sessions:      int(sessionCount),
		},
		Sessions:  toAPISessions(mainRes.Sessions),
		Buckets:   toAPIBuckets(mainRes.BucketRows, win),
		Projects:  sharesFrom(projRes.Sessions, "project", mainRes.Totals),
		Providers: sharesFrom(providerRows, "provider", mainRes.Totals),
		Models:    sharesFrom(modelRes.Sessions, "model", mainRes.Totals),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(resp)
}

// writeJSONError 返回 JSON 错误响应。
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// toAPISessions 把 stats.SessionStats 转成 web DTO（字段白名单）。
func toAPISessions(rows []stats.SessionStats) []apiSession {
	out := make([]apiSession, 0, len(rows))
	for _, r := range rows {
		started := time.Time{}
		if r.StartedAt != nil {
			started = *r.StartedAt
		}
		out = append(out, apiSession{
			StartedAt:      started,
			Project:        orUnknown(r.Project),
			Provider:       orUnknown(r.Provider),
			Model:          orUnknown(r.Model),
			Input:          r.InputTokens,
			Output:         r.OutputTokens,
			CacheCreation:  r.CacheCreate,
			CacheRead:      r.CacheRead,
			CostUSD:        r.CostUSD,
		})
	}
	return out
}

// orUnknown 把空维度映射成"未知"。
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return unknownLabel
	}
	return s
}

// toAPIBuckets 把桶行转成 web DTO。
//
// 24h 窗口补齐到 24 个 hourly 槽：CLI 的 `--day 1` 只给出"到当前小时"为止
// 的桶（0..currentHour），看板要画完整的一天，未来的槽补零。
func toAPIBuckets(rows []stats.BucketRow, win windowSpec) []apiBucket {
	out := make([]apiBucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, apiBucket{
			Label:         r.Label,
			BucketISO:     r.BucketISO,
			IsCurrent:     r.IsCurrent,
			Sessions:      r.Sessions,
			Input:         r.InputTokens,
			Output:        r.OutputTokens,
			CacheCreation: r.CacheCreate,
			CacheRead:     r.CacheRead,
			CostUSD:       r.CostUSD,
		})
	}
	if win.hourly {
		out = padHourlyBuckets(out)
	}
	return out
}

// padHourlyBuckets 把 hourly 桶补齐到 24 个（0..23 点）。
func padHourlyBuckets(rows []apiBucket) []apiBucket {
	if len(rows) >= 24 || len(rows) == 0 {
		return rows
	}
	// 已有桶从 00:00 起连续；以第一桶的 ISO 推算缺失的尾部桶。
	first := rows[0]
	base, err := time.Parse("2006-01-02T15:04", first.BucketISO)
	if err != nil {
		return rows
	}
	out := make([]apiBucket, 0, 24)
	out = append(out, rows...)
	for i := len(rows); i < 24; i++ {
		t := base.Add(time.Duration(i) * time.Hour)
		out = append(out, apiBucket{
			Label:     t.Format("15:00"),
			BucketISO: t.Format("2006-01-02T15:04"),
		})
	}
	return out
}

// sharesFrom 把聚合行转成占比表。
//
// dim ∈ {"project","provider","model"}：决定取哪一列做分组名。
// 口径与 CLI 一致——CLI 的 --by-provider 也是按该列分组的。
func sharesFrom(rows []stats.SessionStats, dim string, totals stats.SessionStats) []apiShare {
	type agg struct {
		tokens int64
		cost   float64
	}
	byName := map[string]*agg{}
	for _, r := range rows {
		name := ""
		switch dim {
		case "project":
			name = r.Project
		case "provider":
			name = r.Provider
		case "model":
			name = r.Model
		}
		name = orUnknown(name)
		a := byName[name]
		if a == nil {
			a = &agg{}
			byName[name] = a
		}
		a.tokens += r.InputTokens + r.OutputTokens + r.CacheCreate + r.CacheRead
		a.cost += r.CostUSD
	}

	totalTokens := totals.InputTokens + totals.OutputTokens + totals.CacheCreate + totals.CacheRead
	out := make([]apiShare, 0, len(byName))
	for name, a := range byName {
		s := apiShare{Name: name, Tokens: a.tokens, CostUSD: a.cost}
		if totalTokens > 0 {
			s.TokenShare = float64(a.tokens) / float64(totalTokens)
		}
		if totals.CostUSD > 0 {
			s.CostShare = a.cost / totals.CostUSD
		}
		out = append(out, s)
	}
	// tokens 降序；同 tokens 按名字排（稳定）
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ---------------------------------------------------------------------------
// 页面与静态资源
// ---------------------------------------------------------------------------

// handleUI 返回看板页面。
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := indexHTML()
	if err != nil {
		http.Error(w, "read asset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 页面本身不缓存（资源带版本号 query）
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// handleAsset 返回静态资源（app.js / style.css）。
func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	switch name {
	case "app.js":
		data, err := assetFS.ReadFile("assets/app.js")
		if err != nil {
			http.Error(w, "read asset: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	case "style.css":
		data, err := assetFS.ReadFile("assets/style.css")
		if err != nil {
			http.Error(w, "read asset: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

// assetVersion 是静态资源的版本号（用于 ?v= query 防缓存）。
//
// 取自资源内容的长度——本次改动不引入构建期变量，长度变化即视为版本变化。
func assetVersion() string {
	data, err := assetFS.ReadFile("assets/app.js")
	if err != nil {
		return "0"
	}
	return strconv.Itoa(len(data))
}
