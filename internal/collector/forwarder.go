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

// Forwarder 把 OTel metrics 信号转发到用户的远程 collector。
//
// 决策（见 telemetry-config-compat/design.md 决策 3）：
//   - 现为 metrics-only：logs/traces 不再经 daemon 转发（用户上游已直达远程）
//   - 自动检测：daemon 启动时按以下优先级读 env：
//     1. FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT（per-signal metrics 备份，telemetry-config-compat 新增）
//     2. CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT（fallback，保留向后兼容）
//     任一非空且非 localhost/127.0.0.1 → 启用
//   - localhost / 127.0.0.1 → 拒绝（防 loop）+ log warning
//   - auth 走 CC_FORWARD_AUTH_HEADER（如 "Authorization=Bearer xxx"）；不复用 ANTHROPIC_AUTH_TOKEN
//   - 5s timeout；非 2xx / 超时 → stderr 一行（不带 auth）+ drop；本地 SQLite 已落盘
//   - 异步 fire-and-forget：不阻塞 daemon
//   - URL 固定为完整路径（含 /v1/metrics 后缀），不再按类型拼路径
//
// 关闭由 daemon SIGTERM 触发；调用方负责让 in-flight 完成（设计：serve Shutdown + 短 drain）。
type Forwarder struct {
	enabled    bool
	target     string // 已经带 /v1/metrics 后缀的完整 URL；为空时 disabled
	authHeader string // 完整 "Authorization=Bearer xxx" 或空
	client     *http.Client
}

// envForwardMetricsEndpoint 是 ccl 注入到 settings.json 的 per-signal metrics 备份。
// telemetry-config-compat 决策 2：per-signal metrics 用 FORWORD_ 命名空间。
const envForwardMetricsEndpoint = "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"

// envOrigEndpoint 是 ccl 注入的 fallback 备份；metrics per-signal 未设时仍能触发 forwarder。
// telemetry-config-compat 决策 3：兜底触发条件保留，避免破坏只配 fallback 端点的用户。
const envOrigEndpoint = "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"

// envAuthHeader 是用户显式设的 forwarder auth（不是 ANTHROPIC_AUTH_TOKEN）。
const envAuthHeader = "CC_FORWARD_AUTH_HEADER"

// NewForwarderFromEnv 按优先级检查 env 决定是否启用 forwarder（metrics-only）。
//
// 优先级（telemetry-config-compat 决策 3）：
//   1. FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT 非空且非 localhost → 用它
//   2. CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT 非空且非 localhost → 用它
//   3. 都未设或都是 loopback → 禁用
//
// 返回的 *Forwarder 在 disabled 时所有 Forward 调用都是 no-op（不报错）。
func NewForwarderFromEnv() *Forwarder {
	auth := os.Getenv(envAuthHeader)

	// 优先：per-signal metrics 备份
	if endpoint := os.Getenv(envForwardMetricsEndpoint); endpoint != "" {
		if !isLoopbackEndpoint(endpoint) {
			return NewForwarder(endpoint, auth)
		}
		log.Printf("forwarder: %s=%s 指向 localhost；跳过尝试 fallback", envForwardMetricsEndpoint, endpoint)
		// 不直接禁用：fallback 可能仍可用
	}

	// 兜底：fallback endpoint 备份
	if endpoint := os.Getenv(envOrigEndpoint); endpoint != "" {
		if !isLoopbackEndpoint(endpoint) {
			return NewForwarder(endpoint, auth)
		}
		log.Printf("forwarder: %s=%s 指向 localhost；禁用以防 loop", envOrigEndpoint, endpoint)
		return NewForwarder("", auth) // disabled
	}

	return NewForwarder("", auth) // disabled
}

// NewForwarder 手动构造 forwarder（测试用）。
//
// endpoint 规范化：
//   - 已含 /v1/metrics 后缀 → 原样保留
//   - 否则 → trimRight("/") + 拼接 "/v1/metrics"
//   - scheme 不解析、不补全（透传用户原值）
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
	f.target = normalizeForwardTarget(endpoint)
	f.enabled = true
	if !isLoopbackEndpoint(endpoint) {
		log.Printf("forwarder: 已启用 → %s%s", f.target, authMask(f.authHeader))
	}
	return f
}

// normalizeForwardTarget 规范化 forwarder 目标 URL：确保以 /v1/metrics 结尾。
//
// 规则：
//   - "http://remote:4318/v1/metrics" → "http://remote:4318/v1/metrics"（不再追加）
//   - "http://remote:4318/"          → "http://remote:4318/v1/metrics"（trim 末尾 /）
//   - "remote:4318"                   → "remote:4318/v1/metrics"（透传 + 追加）
//   - "http://remote:4318/v1/metrics/" → "http://remote:4318/v1/metrics"（trim + 不重复追加）
func normalizeForwardTarget(endpoint string) string {
	trimmed := strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(trimmed, "/v1/metrics") {
		return trimmed
	}
	return trimmed + "/v1/metrics"
}

// Enabled 返回是否启用（debug / 测试用）。
func (f *Forwarder) Enabled() bool { return f.enabled }

// Target 返回规范化后的完整目标 URL（含 /v1/metrics 后缀）。测试用。
func (f *Forwarder) Target() string { return f.target }

// Forward 异步把 body POST 到 forwarder target。
//
// 立即返回；HTTP 在 goroutine 里执行。错误仅 log。
// signal 参数保留向后兼容，但实际 url 总是 f.target（metrics-only）。
func (f *Forwarder) Forward(signal string, body []byte) {
	if !f.enabled {
		return
	}
	go f.post(signal, body)
}

// post 同步执行单次 POST。5s 超时；非 2xx / 超时 → log + drop。
//
// signal 入参保留是为了与 Forward() 接口对齐；实际 URL 固定为 f.target（metrics-only）。
func (f *Forwarder) post(_ string, body []byte) {
	url := f.target
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