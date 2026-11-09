package web

import (
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// writeGuard 是写接口的闸：**内容类型 + 同源**。
//
// 为什么需要：session cookie 是 SameSite=Lax —— 跨站的**表单提交**会带着
// cookie 发出去（Lax 允许"顶级导航"的跨站请求，表单 POST 属于其中）。
// 如果写接口接受 application/x-www-form-urlencoded 或 text/plain，
// 一个恶意页面就能借用户的 cookie 改掉他的 provider 配置（里面是明文凭证）。
//
// 两道闸：
//
//  1. Content-Type 必须是 application/json —— 跨站表单发不出这个类型
//     （非简单类型会触发 CORS 预检，预检失败浏览器就不发实际请求）
//  2. Origin（若存在）必须与请求同源 —— 挡住"预检过了但 Origin 不对"的情况
//
// Origin 缺失时不拦：curl / 脚本客户端不带 Origin，那是正常用法，
// 真正的边界是鉴权（token + cookie）。
func writeGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isJSONContentType(r.Header.Get("Content-Type")) {
			writeJSONError(w, http.StatusUnsupportedMediaType,
				"写接口要求 Content-Type: application/json")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
			writeJSONError(w, http.StatusForbidden, "跨源请求被拒绝")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isJSONContentType 判断 Content-Type 是不是 application/json（允许带参数）。
func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

// sameOrigin 判断 Origin 头与请求是否同源。
//
// host 必须完全一致（含端口）；scheme 不一致也拒。
func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	scheme := u.Scheme
	if r.TLS != nil {
		scheme = "https"
	} else if scheme == "" {
		scheme = "http"
	}
	return strings.EqualFold(u.Scheme, scheme)
}
