package collector

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Forwarder 把 OTel 信号转发到 CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT。
//
// 决策（见 design.md §14-19）：
//   - 自动检测：daemon 启动时读 CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT；非空且非 localhost → 启用
//   - localhost / 127.0.0.1 → 拒绝（防 loop）+ log warning
//   - auth 走 CC_FORWARD_AUTH_HEADER（如 "Authorization=Bearer xxx"）；不复用 ANTHROPIC_AUTH_TOKEN
//   - 5s timeout；非 2xx / 超时 → stderr 一行（不带 auth）+ drop；本地 SQLite 已落盘
//   - 异步 fire-and-forget：不阻塞 daemon
//
// 关闭由 daemon SIGTERM 触发；调用方负责让 in-flight 完成（设计：serve Shutdown + 短 drain）。
type Forwarder struct {
	enabled    bool
	target     string // 去掉末尾 /，加上 /v1/{metrics|logs|traces}
	authHeader string // 完整 "Authorization=Bearer xxx" 或空
	client     *http.Client
}

// envOrigEndpoint 是 ccl 注入到 settings.json 的备份值。daemon 启动时读这个判断是否启用 forwarder。
const envOrigEndpoint = "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"

// envAuthHeader 是用户显式设的 forwarder auth（不是 ANTHROPIC_AUTH_TOKEN）。
const envAuthHeader = "CC_FORWARD_AUTH_HEADER"

// NewForwarderFromEnv 检查 env 决定是否启用 forwarder。
//
// 返回的 *Forwarder 在 disabled 时所有 Forward 调用都是 no-op（不报错）。
// 启用条件：CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT 非空且不是 localhost/127.0.0.1。
func NewForwarderFromEnv() *Forwarder {
	endpoint := os.Getenv(envOrigEndpoint)
	auth := os.Getenv(envAuthHeader)
	if endpoint == "" {
		return NewForwarder("", auth) // disabled
	}
	if isLoopbackEndpoint(endpoint) {
		log.Printf("forwarder: %s=%s 指向 localhost；禁用以防 loop", envOrigEndpoint, endpoint)
		return NewForwarder("", auth) // disabled; log once
	}
	return NewForwarder(endpoint, auth)
}

// NewForwarder 手动构造 forwarder（测试用）。
//
// 不做 loopback 检查——调用方负责传入非 loopback 值。
func NewForwarder(endpoint, authHeader string) *Forwarder {
	f := &Forwarder{
		client:     &http.Client{Timeout: 5 * time.Second},
		authHeader: authHeader,
	}
	if endpoint == "" {
		return f
	}
	f.target = strings.TrimRight(endpoint, "/")
	f.enabled = true
	if endpoint != "" && !isLoopbackEndpoint(endpoint) {
		log.Printf("forwarder: 已启用 → %s%s/{metrics,logs,traces}", f.target, authMask(f.authHeader))
	}
	return f
}

// Enabled 返回是否启用（debug / 测试用）。
func (f *Forwarder) Enabled() bool { return f.enabled }

// Forward 异步把 body POST 到 forwarder target 对应 signal path。
//
// 立即返回；HTTP 在 goroutine 里执行。错误仅 log。
func (f *Forwarder) Forward(signal string, body []byte) {
	if !f.enabled {
		return
	}
	go f.post(signal, body)
}

// post 同步执行单次 POST。5s 超时；非 2xx / 超时 → log + drop。
func (f *Forwarder) post(signal string, body []byte) {
	url := f.target + "/v1/" + signal
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("forwarder: 建请求失败 %s: %v", url, err)
		return
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if f.authHeader != "" {
		// 形如 "Authorization=Bearer xxx"；按第一个 "=" 拆 header 名 + value
		// 允许用户写 "X-Custom: value" 含冒号形式。
		if idx := strings.IndexByte(f.authHeader, '='); idx > 0 {
			req.Header.Set(f.authHeader[:idx], f.authHeader[idx+1:])
		} else if idx := strings.Index(f.authHeader, ": "); idx > 0 {
			req.Header.Set(f.authHeader[:idx], f.authHeader[idx+2:])
		} else {
			// 整个字符串当 value，header 名默认 Authorization
			req.Header.Set("Authorization", f.authHeader)
		}
	}

	resp, err := f.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("forwarder: post to %s timeout after 5s", url)
		} else {
			log.Printf("forwarder: post to %s failed: %v", url, err)
		}
		return
	}
	defer resp.Body.Close()
	// 必须 drain 才能复用连接
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("forwarder: post to %s status=%d", url, resp.StatusCode)
	}
}

// isLoopbackEndpoint 判断 endpoint URL 主机是否为 localhost。
//
// 简化：只看字符串包含 "localhost" 或 "127.0.0.1"，足以覆盖 ccl 注入的两个值。
// 不解析完整 URL（避免引入 net/url 解析的 host 部分处理开销）。
func isLoopbackEndpoint(endpoint string) bool {
	return strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1")
}

// authMask 把 "Authorization=Bearer xyz" 掩为 "Authorization=***"，仅显示有/没配。
func authMask(auth string) string {
	if auth == "" {
		return ""
	}
	if idx := strings.IndexByte(auth, '='); idx > 0 {
		return fmt.Sprintf(" (%s=***)", auth[:idx])
	}
	if idx := strings.Index(auth, ": "); idx > 0 {
		return fmt.Sprintf(" (%s=***)", auth[:idx])
	}
	return " (auth=***)"
}