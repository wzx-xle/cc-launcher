package collector

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// Server 监听 :4318，提供 OTel HTTP endpoint。
//
// 路由：
//   POST /v1/metrics  → 解析 + 持久化 + 转发
//   POST /v1/logs     → 不解析 + 仅转发（HTTP 200）
//   POST /v1/traces   → 不解析 + 仅转发（HTTP 200）
//   其它路径          → 404
//
// 优雅关：调用 SetShuttingDown() 后 /v1/metrics 返回 503（避免新数据；in-flight 仍能完成）。
type Server struct {
	addr   string
	store  *Store
	fwd    *Forwarder
	srv    *http.Server

	shuttingDown atomic.Bool // SIGTERM 时置 true → metrics handler 返回 503
}

// ServerOptions 配置 NewServer。
type ServerOptions struct {
	Addr     string // 监听地址，默认 ":4318"
	Store    *Store
	Forward  *Forwarder // 可为 nil（禁用 forwarder）
}

// NewServer 构造 HTTP server，但不启动。调 Start() 才真正 listen。
func NewServer(opts ServerOptions) *Server {
	addr := opts.Addr
	if addr == "" {
		addr = ":4318"
	}
	s := &Server{
		addr:  addr,
		store: opts.Store,
		fwd:   opts.Forward,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics", s.handleMetrics)
	mux.HandleFunc("/v1/logs", s.handleForwardOnly("logs"))
	mux.HandleFunc("/v1/traces", s.handleForwardOnly("traces"))
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
	if s.fwd != nil {
		s.fwd.Forward("metrics", forwardBody)
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

// handleForwardOnly 处理 logs/traces：不解析，仅转发；返回 OTel OK（200）。
func (s *Server) handleForwardOnly(signal string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if s.fwd != nil {
			s.fwd.Forward(signal, body)
		}
		// 不解析、不写盘；返回 200 表示收到。
		w.WriteHeader(http.StatusOK)
	}
}