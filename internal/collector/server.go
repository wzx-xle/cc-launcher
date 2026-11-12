package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// Server 监听 :4318，提供 OTel HTTP endpoint。
//
// 路由（telemetry-config-compat）：
//
//	POST /v1/metrics  → 解析 + 持久化 + 转发（如启用）
//	POST /v1/logs     → 不解析 + 仅回 200（HTTP 200，不持久不转发）
//	POST /v1/traces   → 不解析 + 仅回 200（HTTP 200，不持久不转发）
//	其它路径          → 404
//
// logs/traces 现在只是"应答"——用户的上游（per-signal endpoint 或 fallback 路径推到本地 daemon
// 的兜底场景）已直达远程 collector，daemon 不再 forward、不再 persist。本地 SQLite 永远不会有
// logs/traces 内容（隐私靠 daemon 架构，不靠覆盖 Claude Code exporter 配置）。
//
// 优雅关：调用 SetShuttingDown() 后 /v1/metrics 返回 503（避免新数据；in-flight 仍能完成）。
type Server struct {
	addr  string
	store *Store
	fwd   *Forwarder
	srv   *http.Server

	// activity 跟踪各 provider 最近的 OTel 活动（谁在用）。
	//
	// 额度刷新靠它决定"查哪些 provider"——额度数据的唯一消费者是状态栏，
	// 而状态栏只显示当前会话那个 provider 的额度（见 activity.go 的注释）。
	activity *Activity

	// onActivate 处理"某 provider 现在被使用"的声明请求（可能为 nil）。
	onActivate func(provider string) error

	// onQuota 处理"读某个 provider 的额度"的请求（可能为 nil）。
	onQuota func(provider string) (any, int)

	shuttingDown atomic.Bool // SIGTERM 时置 true → metrics handler 返回 503
}

// ServerOptions 配置 NewServer。
type ServerOptions struct {
	Addr    string // 监听地址，默认 ":4318"
	Store   *Store
	Forward *Forwarder // 可为 nil（禁用 forwarder）。metrics-only：logs/traces 不再走 forwarder

	// Mount 是可选的路由挂载入口（web-usage-stats-ui §D2）。
	//
	// 采集层与展示层解耦：**collector 不 import internal/web**，看板路由由
	// cmd 层注入（cmd/cc-launcher/serve.go 调 web.NewServer(...).Mount）。
	// 未来若要第二个 UI 入口，也只需要在 cmd 层多挂一次。
	Mount func(mux *http.ServeMux)

	// OnActivate 处理"某个 provider 现在被使用"的声明请求。
	//
	// **为什么需要它**：额度数据的唯一消费者是状态栏，而状态栏显示的是**当前
	// 会话那个 provider** 的额度。但"当前会话用了哪个 provider"只有 **ccl**
	// 知道（它在 exec Claude Code 之前就选定了）—— 让它声明一次，额度就能
	// 立刻刷新，不必等 Claude Code 起来推 OTel（实测那要等一个 OTel 周期，
	// daemon 重启后更是整整 60 秒的盲窗）。
	//
	// **回调负责两件事**：①校验这个名字是不是**已配置的** provider（名字来自
	// HTTP 查询串，防注入 / 防对任意 URL 发请求）；②通知刷新循环立即刷一轮。
	// 返回非 nil error → 拒绝该请求（HTTP 400）。
	//
	// nil = 不提供该能力 → `/ccl/activate` 路径**不注册**（404）。
	OnActivate func(provider string) error

	// OnQuota 返回某个 provider 的额度输出。第二个值是 HTTP 状态码：
	// 200 = 有数据；404 = 没有记录或 provider 未配置；422 = provider 没有额度接口。
	//
	// **为什么回调而不是在采集层直接实现**：额度数据住在 ~/.cc-launcher/
	// quota.json，provider 的刷新周期住在 providers 目录 —— 采集层不认这些
	// 路径（它只管 OTel 数据）。让 cmd 层注入，采集层只负责路由、来源校验与
	// JSON 编码（跟 OnActivate / Mount 一样的解耦纪律）。
	//
	// **为什么返回 any 而不是具体类型**：采集层不 import internal/quota ——额度
	// 的形状是"对外契约"，由 quota 包自己定义。
	//
	// nil = 不提供该能力 → `/ccl/quota` 路径**不注册**（404）。
	OnQuota func(provider string) (any, int)
}

// NewServer 构造 HTTP server，但不启动。调 Start() 才真正 listen。
func NewServer(opts ServerOptions) *Server {
	addr := opts.Addr
	if addr == "" {
		addr = ":4318"
	}
	s := &Server{
		addr:     addr,
		store:    opts.Store,
		fwd:      opts.Forward,
		activity: NewActivity(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics", s.handleMetrics)
	mux.HandleFunc("/v1/logs", s.handleAckOnly())
	mux.HandleFunc("/v1/traces", s.handleAckOnly())
	// **未配置 OnActivate → 不注册该路径** → 请求得到 404。
	// 不暴露一个"什么都不做"的端点（免得让人以为激活生效了）。
	if opts.OnActivate != nil {
		s.onActivate = opts.OnActivate
		mux.HandleFunc(activatePath, s.handleActivate)
	}
	// 同理：未配置 OnQuota → `/ccl/quota` 不注册（404）。
	if opts.OnQuota != nil {
		s.onQuota = opts.OnQuota
		mux.HandleFunc(QuotaPath, s.handleQuota)
	}
	if opts.Mount != nil {
		opts.Mount(mux)
	}
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// SetShuttingDown 标记 server 进入优雅关闭模式。
//
// /v1/metrics handler 会立即返回 503；in-flight 请求仍能完成（依赖 http.Server.Shutdown）。
// /v1/logs 和 /v1/traces 不受影响（不影响 forwarder）。
func (s *Server) SetShuttingDown() { s.shuttingDown.Store(true) }

// Addr 返回实际监听地址（含自动分配的端口）。
// Start 成功调用后才有意义。
func (s *Server) Addr() string {
	if s.srv == nil {
		return s.addr
	}
	return s.srv.Addr
}

// Start 阻塞直到 ctx 取消或 server 异常。
//
// 返回的 error：context.Canceled 视为正常关闭；其它为异常。
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err // 含 EADDRINUSE，调用方决定怎么提示
	}
	s.srv.Addr = ln.Addr().String()

	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		// graceful：等 in-flight 完成（不强制超时；由调用方设 ctx deadline）。
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutCtx)
	case err := <-errCh:
		if err == nil {
			return nil
		}
		return err
	}
}

// handleMetrics 解析 + 写 SQLite + 转发。
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.shuttingDown.Load() {
		http.Error(w, "daemon shutting down", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20)) // 16 MiB 上限
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 转发用：保存原 body bytes（forwarder 走原始 protobuf）
	forwardBody := append([]byte(nil), body...)

	result, err := ParseMetricsRequest(body)
	if err != nil {
		if IsParseError(err) {
			log.Printf("collector: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("collector: 解析异常: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 写 SQLite（异步；不阻塞 HTTP 响应）
	for _, sess := range result.Sessions {
		// 记活跃：这条 metrics 是哪个 provider 推来的（ccl 注入的
		// ccl.provider resource attribute）→ 额度刷新据此决定查谁。
		s.activity.Mark(sess.Provider, time.Now())
		if err := s.store.WriteSession(sess); err != nil {
			log.Printf("collector: WriteSession 失败: %v", err)
		}
	}
	for _, m := range result.Metrics {
		if err := s.store.WriteMetric(m); err != nil {
			log.Printf("collector: WriteMetric 失败: %v", err)
		}
	}

	// 转发（async；落盘失败不影响 forwarder）
	//
	// 转发前剥掉 ccl 追加的 resource attributes（ccl.provider / ccl.cwd /
	// ccl.provider_endpoint）——它们是本地 bookkeeping，对用户的远程 collector
	// 没价值，还可能泄露本机工作目录。剥不掉时**照常转发原 body**（不能因为
	// 剥不掉就把转发丢了）。
	if s.fwd != nil {
		out, err := stripCclAttributes(forwardBody)
		if err != nil {
			log.Printf("collector: 转发前剥 ccl 属性失败（按原样转发）: %v", err)
			out = forwardBody
		}
		s.fwd.Forward("metrics", out)
	}

	// 响应 OTel ExportMetricsServiceResponse（HTTP 200 + 0 rejected）
	resp := &collectormetrics.ExportMetricsServiceResponse{}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "marshal response: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Content-Length", strconv.Itoa(len(respBytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}

// Activity 返回 provider 活跃度跟踪器。
//
// 供 cmd 层（ccl serve 的额度刷新）判断"哪些 provider 最近有人在用"——
// 不活跃的 provider 不需要查额度（省请求 + 不触发厂商限流）。
func (s *Server) Activity() *Activity { return s.activity }

// Handler 返回 HTTP handler（测试用）。
//
// **为什么需要它**：回环来源的判定要看连接的**来源地址**，而 httptest 的真实
// 连接永远来自 127.0.0.1 —— 要测"非回环来源被拒"，只能直接构造带指定
// RemoteAddr 的请求打到 handler 上。
func (s *Server) Handler() http.Handler {
	if s.srv == nil {
		return http.NotFoundHandler()
	}
	return s.srv.Handler
}

// isLoopbackRequest 判断请求是否来自**回环地址**。
//
// **只看 RemoteAddr**（daemon 实际接受的那个连接的来源地址），**不看任何请求
// 头**：daemon 是裸 HTTP server，没有可信的转发链——读 X-Forwarded-For 之类
// 的头等于把判定交给请求方（伪造一个头就能拿到额度数据）。
//
// 判定不出来（RemoteAddr 格式怪 / 不是 IP）→ **当作非回环**（拒绝）：宁可
// 误伤一次本机调用，也不能漏放一个网络侧的。
func isLoopbackRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// 没有端口（或格式怪）→ 整体当 IP 解析。
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// QuotaPath 是 daemon 上只读额度端点的路径。
//
// 与 activatePath 同族：**ccl 自己 ↔ daemon 的内部交换**。只接受回环来源
// （见 isLoopbackRequest）。
const QuotaPath = "/ccl/quota"

// QuotaEndpointHeader 是额度端点在**每个**响应里带的头（取值见
// QuotaEndpointHeaderValue）。
//
// **为什么需要它**：daemon 可能是**旧版本**的进程（ccl 升级了但 daemon 还在
// 跑）——那时的 daemon 没有这个路径，mux 会回 404 "404 page not found"。
// 客户端要能区分"端点说没有记录"（404 **带**本头）与"端点根本不存在"
// （404 **不带**本头）：后者必须回落直读本地数据文件，而不是误报"没有额度
// 记录"（那会让状态栏显示成空的）。
const QuotaEndpointHeader = "X-Ccl-Endpoint"

// QuotaEndpointHeaderValue 是 QuotaEndpointHeader 的取值。
const QuotaEndpointHeaderValue = "quota"

// handleQuota 处理只读额度请求：回环来源 + provider 名 → 回调取额度 →
// 按回调给的状态码与值应答。
func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// **回环校验放在最前面**：非回环来源的请求不该进入任何业务判定（连
	// "provider 名有没有"都不该告诉它）。
	if !isLoopbackRequest(r) {
		http.Error(w, "only loopback callers are allowed", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("provider"))
	if name == "" {
		w.Header().Set(QuotaEndpointHeader, QuotaEndpointHeaderValue)
		http.Error(w, "missing provider", http.StatusBadRequest)
		return
	}
	v, code := s.onQuota(name)
	if code != http.StatusOK {
		msg := http.StatusText(code)
		if text, ok := v.(string); ok && text != "" {
			msg = text
		}
		w.Header().Set(QuotaEndpointHeader, QuotaEndpointHeaderValue)
		http.Error(w, msg, code)
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		w.Header().Set(QuotaEndpointHeader, QuotaEndpointHeaderValue)
		http.Error(w, "marshal quota: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set(QuotaEndpointHeader, QuotaEndpointHeaderValue)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// activatePath 是 provider 激活声明的路径。
//
// **为什么在 daemon 的端口上**（而不是另开一个）：daemon 已经在 :4318 监听，
// 多挂一个路径是几行代码；另开端口要改 fork 逻辑、要处理端口冲突，收益为零。
//
// **它暴露了什么**：能访问该端口的人可以让 daemon 去查某个**已配置** provider
// 的额度（响应是 204 空，不回传额度数据）。这个端口本来就裸露着 OTel 数据，
// 风险级别没有提升。
const activatePath = "/ccl/activate"

// handleActivate 处理 provider 激活声明：ccl 在启动 Claude Code 之前告诉
// daemon"本次会话用这个 provider" → daemon 立刻把它标记为活跃，并（经
// OnActivate 回调）触发一轮额度刷新。
//
// **白名单校验在 OnActivate 回调里做**（cmd 层知道 providers 目录）：名字来自
// HTTP 查询串，必须确认它是**真实配置的** provider，否则不该为它发请求。
//
// **只接受回环来源**（daemon 监听所有网卡 → 不限制的话，局域网内任意机器都
// 能让 daemon 带着用户的 API key 去查其已配置 provider 的额度端点）。判定看
// 连接的来源地址，不看任何请求头。
//
// 收紧**不会打断 ccl 自己**：daemon 是 ccl fork 出来的子进程（或用户在本机
// 手动起的）→ 声明方必然与 daemon 同机 → 永远是回环来源。
func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// **回环校验放在最前面**：被拒的请求**不得有任何副作用**——不校验
	// provider 名、不标记活跃、不触发刷新。
	if !isLoopbackRequest(r) {
		http.Error(w, "only loopback callers are allowed", http.StatusForbidden)
		return
	}
	if s.shuttingDown.Load() {
		http.Error(w, "daemon shutting down", http.StatusServiceUnavailable)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("provider"))
	if name == "" {
		http.Error(w, "missing provider", http.StatusBadRequest)
		return
	}
	// 回调先校验再通知：校验没过就不该有任何副作用（含标记活跃）。
	if err := s.onActivate(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 校验通过 → 标记活跃（额度刷新循环据此决定查谁）。
	s.activity.Mark(name, time.Now())
	w.WriteHeader(http.StatusNoContent)
}

// handleAckOnly 处理 logs/traces：仅回 200，不解析、不持久、不转发。
//
// telemetry-config-compat：logs/traces 由 Claude Code 直达远程 collector（per-signal endpoint），
// 兜底路径（fallback endpoint 被 ccl 改写到 localhost）偶发的 logs/traces 到达本地 daemon 时，
// 也只回 200 即可——forwarder 现为 metrics-only，logs/traces 不再经过 daemon。
func (s *Server) handleAckOnly() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// 不解析 body（兜底场景下 body 可能是合法 protobuf 也可能不是）；drain 即可
		// 让连接可复用。
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 16<<20))
		// 返回 200 表示收到。
		w.WriteHeader(http.StatusOK)
	}
}
