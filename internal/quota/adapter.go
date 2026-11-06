package quota

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Adapter 是某厂商的额度适配器。
//
// **厂商差异全部关在这个盒子里**（provider-quota design.md §决策 4）：识别规则、
// 端点推导、响应解析、成功判据都归 adapter。ccl 的其它层（store / refresh /
// 命令）只管去重、周期、文件、输出——三家厂商的失败协议完全不同，把它们混在
// 一起就是一堆 if-else。
type Adapter interface {
	// Name 是适配器名（写进输出的 source 字段，也用于 provider 配置的 source 字段）。
	Name() string
	// Match 判断该适配器是否负责这个 provider（看 base_url）。
	//
	// **只用 host 启发式时，宁可识别不出来，也不要识别错**：识别错会推导出
	// 一个不存在的端点，然后拿着用户的 API key 去打它。
	Match(baseURL string) bool
	// Endpoint 推导额度端点。第二个返回值 false = 推导不出来。
	Endpoint(baseURL string) (string, bool)
	// Fetch 发起请求并解析，返回统一的维度列表。
	//
	// **Fetch 拥有整个请求**：认证头、超时、重定向策略都归 adapter（不同厂商
	// 的要求可能不同）。成败也由 adapter 自己判定——三家的失败协议零交集：
	// MiniMax 错误也返回 HTTP 200（判据在 body 的 base_resp.status_code）、
	// DeepSeek 看 HTTP 状态码（401）、New API 看 body 的 code 字段。
	Fetch(ctx context.Context, p ProviderInfo) ([]Dimension, error)
}

// ProviderInfo 是查询一个 provider 额度所需的输入。
type ProviderInfo struct {
	// BaseURL 是 provider 的 ANTHROPIC_BASE_URL。
	BaseURL string
	// APIKey 是 provider 的凭证（Bearer）。
	APIKey string
	// Model 是本次使用的模型名（厂商若按模型分池则需要；当前三家都是 key 级）。
	Model string

	// Endpoint 是**显式配置**的额度端点；非空时 adapter 必须用它，不许推导。
	Endpoint string
	// QuotaPerUnit 是额度点换算比例；0 表示用 adapter 的默认值。
	QuotaPerUnit float64
	// DisplayUnit 是显示单位；空表示用 adapter 的默认值。
	DisplayUnit string
	// Env 是 provider 的 env 块——**代理设置从这里取**（provider 的 env 是经
	// --settings 交付给 Claude Code 的，不是 ccl 进程的环境）。
	Env map[string]string
}

// adapters 是内置 adapter 注册表。加一个厂商 = 一个新文件 + 这里加一行。
var adapters = []Adapter{
	minimaxAdapter{},
	deepseekAdapter{},
	newapiAdapter{},
}

// AdapterByName 按名字（provider 配置里的 source 字段）找 adapter。
func AdapterByName(name string) (Adapter, bool) {
	for _, a := range adapters {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// AdapterNames 列出所有内置 adapter 的名字（错误提示与文档用）。
func AdapterNames() []string {
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Name())
	}
	return names
}

// DetectAdapter 按 base_url 猜 adapter（**兜底**路径）。
//
// 只有 host 规则明确时才识别：识别错比识别不到更糟（会拿着 API key 去打一个
// 不存在的端点）。New API 系无法靠 host 识别（任何 host 都可能是它），必须
// 在 provider 配置里显式写 source。
func DetectAdapter(baseURL string) (Adapter, bool) {
	for _, a := range adapters {
		if a.Match(baseURL) {
			return a, true
		}
	}
	return nil, false
}

// httpTimeout 是额度请求的超时。
//
// 5 秒：实测 MiniMax 的额度接口约 150ms；超时是"厂商接口挂了"的兜底，
// 不能太长（serve 的刷新要在一个周期内跑完），也不能太短（跨代理时慢）。
const httpTimeout = 5 * time.Second

// RateLimitError 表示厂商限流（HTTP 429）。
//
// **为什么要单独一个类型**：429 不是"接口挂了"——它说的是"你查得太勤了"。
// 刷新层对这两种失败的处置完全不同：
//
//   - 普通失败 → 旧条目保留，下一轮照常再试
//   - 限流     → **退避**（等一段时间再试），否则每轮硬打会让限流永远不恢复
//
// 三家的失败协议各不相同（MiniMax 看 body、DeepSeek 看 HTTP 状态码、New API
// 看 body 的 code），但 **429 是 HTTP 层的共性** → 在拿到状态码的地方统一
// 识别。
type RateLimitError struct {
	// Status 是 HTTP 状态码（429）。
	Status int
	// RetryAfter 是厂商给的 Retry-After（响应头）；没有则 0。
	RetryAfter time.Duration
	// Msg 是响应体摘要（截断）。
	Msg string
}

// Error 实现 error。
func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("额度接口限流（HTTP %d，厂商建议 %s 后重试）: %s",
			e.Status, e.RetryAfter.Round(time.Second), e.Msg)
	}
	return fmt.Sprintf("额度接口限流（HTTP %d）: %s", e.Status, e.Msg)
}

// parseRetryAfter 解析 Retry-After 响应头。
//
// 两种形式都认：秒数（"120"）与 HTTP-date。没给或解析不了 → 0（调用方退回
// 自己的退避策略）。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// newHTTPClient 造一个走 **provider 自己网络路径** 的 HTTP 客户端。
//
// **为什么不能用 http.ProxyFromEnvironment**：它读的是**进程环境**，而
// provider 的 env 块是经 --settings 交付给 Claude Code 的——daemon 从 ccl
// fork 继承的是用户 shell 的环境，不是 provider 的。一个"必须走代理才能访问"
// 的 provider，Claude Code 能用而额度查询会超时。
//
// **为什么跟随重定向且不自定义 CheckRedirect**：Go 默认的跨 http→https 重定向
// 会保留 Authorization（shouldCopyHeaderOnRedirect 按 url.Hostname() 剥端口
// 比对，同主机即保留）。ccl **不主动升级 scheme**——只提供 http 的内网额度
// 服务升级后会 TLS 握手失败，额度永远查不到；用户显式配的 endpoint 尤其
// 不该被改。
func newHTTPClient(env map[string]string) *http.Client {
	return newHTTPClientWithDial(env, nil)
}

// newHTTPClientWithDial 是 newHTTPClient 的可注入 dial 版本（测试用）。
//
// dial 非 nil 时覆盖 Transport 的拨号函数——测试用它把 "quota.example.com"
// 转到本地测试服务器，从而在**不走真实网络**的前提下验证代理路由。
func newHTTPClientWithDial(env map[string]string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
	pc := proxyConfigFromEnv(env)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		return pc.proxyFor(req.URL)
	}
	if dial != nil {
		tr.DialContext = dial
	}
	return &http.Client{Transport: tr, Timeout: httpTimeout}
}

// proxyConfig 是 provider env 块里的代理设置。
type proxyConfig struct {
	httpProxy  string
	httpsProxy string
	noProxy    []string
}

// proxyConfigFromEnv 从 provider 的 env 块里取代理设置。
//
// 大小写两种拼写都认（HTTP_PROXY / http_proxy），与 Go 的 httpproxy 一致。
func proxyConfigFromEnv(env map[string]string) proxyConfig {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(env[k]); v != "" {
				return v
			}
		}
		return ""
	}
	noProxy := get("NO_PROXY", "no_proxy")
	var list []string
	for _, item := range strings.Split(noProxy, ",") {
		if s := strings.TrimSpace(item); s != "" {
			list = append(list, s)
		}
	}
	return proxyConfig{
		httpProxy:  get("HTTP_PROXY", "http_proxy"),
		httpsProxy: get("HTTPS_PROXY", "https_proxy"),
		noProxy:    list,
	}
}

// proxyFor 决定某个请求要不要走代理、走哪个代理。
//
// 语义对齐 Go 的 httpproxy：
//   - host 命中 NO_PROXY → 直连
//   - https 请求用 HTTPS_PROXY，没配则回退 HTTP_PROXY
//   - http 请求用 HTTP_PROXY
func (p proxyConfig) proxyFor(u *url.URL) (*url.URL, error) {
	if u == nil {
		return nil, nil
	}
	host := u.Hostname()
	if p.matchesNoProxy(host) {
		return nil, nil
	}
	raw := p.httpProxy
	if u.Scheme == "https" && p.httpsProxy != "" {
		raw = p.httpsProxy
	}
	if raw == "" {
		return nil, nil
	}
	pu, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("代理地址 %q 无效: %w", raw, err)
	}
	return pu, nil
}

// matchesNoProxy 判断 host 是否被 NO_PROXY 排除（按后缀匹配）。
//
// 语义对齐 Go 的 httpproxy：条目 ".example.com" 排除 "api.example.com"；
// 条目 "example.com" 排除 "example.com" 与 "*.example.com"。
func (p proxyConfig) matchesNoProxy(host string) bool {
	if host == "" {
		return false
	}
	// localhost 与回环地址永远直连（Go 的 httpproxy 同款行为）。
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, entry := range p.noProxy {
		if entry == "*" {
			return true
		}
		// 带端口的条目：先按 host:port 精确匹配。
		if h, _, err := net.SplitHostPort(entry); err == nil {
			if strings.EqualFold(h, host) {
				return true
			}
			continue
		}
		e := strings.ToLower(entry)
		h := strings.ToLower(host)
		if strings.HasPrefix(e, ".") {
			if h == e[1:] || strings.HasSuffix(h, e) {
				return true
			}
			continue
		}
		if h == e || strings.HasSuffix(h, "."+e) {
			return true
		}
	}
	return false
}

// trimTrailingSlash 去掉 URL 末尾的斜杠。
//
// 本机 iflytek 的 base_url 带尾斜杠（https://one.iflytek.com/api/llm/console/chat/），
// 直接拼 /api/usage/token 会得到 .../chat//api/usage/token。实测该网关双斜杠
// 也能通，但那是宽容行为不是契约——别的网关未必。
func trimTrailingSlash(s string) string {
	return strings.TrimRight(s, "/")
}

// hostOf 取 URL 的主机名（不含端口）；解析失败返回 ""。
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
