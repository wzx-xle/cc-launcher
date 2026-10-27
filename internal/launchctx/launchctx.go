// Package launchctx 负责把「本次启动的上下文」编码进 OTEL_RESOURCE_ATTRIBUTES，
// 随 Claude Code 的 OTLP export 一起送到 daemon。
//
// 背景（web-usage-stats-ui design.md §D15）：Claude Code 的 OTel payload 里
// **没有** cwd / provider，真值只存在于「谁启动的、在哪个目录、用的哪个 provider」。
// ccl 在 exec Claude Code 前是知道这三件事的，把它们写进子进程环境的
// OTEL_RESOURCE_ATTRIBUTES，Claude Code 的 OTel SDK 就会把它们当作
// resource attributes 随 metrics 推送，daemon 直接取。
//
// 编码纪律（实测得出的硬约束，见 design.md §D15「实测得到的编码规则」）：
//   - Claude Code 按 `,` 拆条目、按 `=` 拆键值；值里出现空格 / 控制字符 /
//     非 ASCII 会被判为非法
//   - **一个非法值会让整份 map 被丢弃**（不是只丢那个属性）→ 所有值强制
//     percent-encode（unreserved 之外全编）
//   - 编码后 > 255 字符的属性直接不注入（同样会毒掉整份 map）
package launchctx

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	// EnvResourceAttributes 是承载上下文的环境变量名。
	EnvResourceAttributes = "OTEL_RESOURCE_ATTRIBUTES"

	// KeyProvider / KeyCwd / KeyProviderEndpoint 是 ccl 追加的三个属性键。
	KeyProvider         = "ccl.provider"
	KeyCwd              = "ccl.cwd"
	KeyProviderEndpoint = "ccl.provider_endpoint"

	// MaxEncodedLen 是单个属性值的编码后长度上限。
	//
	// 超过上限的属性**直接丢弃**而不是截断——截断后的值既不是原值，也可能
	// 仍不合法；丢弃只损失这一个维度，其余属性照常送达。
	MaxEncodedLen = 255
)

// Attr 是一个待注入的属性。Value 是**原始值**（未编码）。
type Attr struct {
	Key   string
	Value string
}

// Context 是一次 ccl 启动的上下文。
//
// 语义（design.md §D15「语义边界」）：
//   - Provider：启动时选中的 provider 名（provider 配置文件名），是快照而非当前值
//   - Cwd：本次启动时的工作目录（`--resume` 时是当前目录，不是会话原始目录）
//   - ProviderEndpoint：provider 的 ANTHROPIC_BASE_URL 的 host
type Context struct {
	Provider         string
	Cwd              string
	ProviderEndpoint string
}

// Attrs 返回可注入的属性列表。
//
// 规则：
//   - 空值直接跳过（没有 provider 就没有 ccl.provider 这一维度）
//   - 编码后长度 > MaxEncodedLen 的属性直接丢弃（不截断、不降级）
//
// 顺序固定为 provider → cwd → provider_endpoint，保证同一份输入产出稳定的
// 字符串（便于测试与 diff）。
func (c Context) Attrs() []Attr {
	candidates := []Attr{
		{Key: KeyProvider, Value: c.Provider},
		{Key: KeyCwd, Value: c.Cwd},
		{Key: KeyProviderEndpoint, Value: c.ProviderEndpoint},
	}
	out := make([]Attr, 0, len(candidates))
	for _, a := range candidates {
		if a.Value == "" {
			continue
		}
		if len(Encode(a.Value)) > MaxEncodedLen {
			continue
		}
		out = append(out, a)
	}
	return out
}

// Merge 把 ccl 的属性合并进已有的 OTEL_RESOURCE_ATTRIBUTES 值。
//
// 合并规则（spec/session-context-attrs「ccl merges with a pre-existing
// attribute value」）：
//   - 用户原值保留（**原样**保留，不重新编码——那是用户的值，ccl 不改写）
//   - ccl 的同名 key 以 ccl 的为准（用户条目里同 key 的那条被剔除）
//   - ccl 的条目追加在后面
func Merge(existing string, attrs []Attr) string {
	owned := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		owned[a.Key] = true
	}

	parts := make([]string, 0, len(attrs)+4)
	if existing != "" {
		for entry := range strings.SplitSeq(existing, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			key, _, ok := strings.Cut(entry, "=")
			if ok && owned[strings.TrimSpace(key)] {
				// ccl 的同名 key 以 ccl 为准 → 剔除用户旧条目。
				continue
			}
			parts = append(parts, entry)
		}
	}
	for _, a := range attrs {
		parts = append(parts, a.Key+"="+Encode(a.Value))
	}
	return strings.Join(parts, ",")
}

// EndpointHost 从 ANTHROPIC_BASE_URL 里取 host（不含路径、不含凭据）。
//
// 无法解析时返回空串——端点未知时该维度留空，不编造。
func EndpointHost(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

const hexDigits = "0123456789ABCDEF"

// isUnreserved 判定字节是否属于 RFC 3986 的 unreserved 集合：
// ALPHA / DIGIT / "-" / "." / "_" / "~"。
func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '-' || c == '.' || c == '_' || c == '~':
		return true
	}
	return false
}

// Encode 把原始值 percent-encode 成只含 unreserved 字符的字符串。
//
// 所有非 unreserved 字节（含空格、逗号、分号、反斜杠、非 ASCII）一律
// 编成 %XX（大写十六进制）。
func Encode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0x0f])
	}
	return b.String()
}

// Decode 还原 percent-encoded 值，与 Encode 互为逆运算。
//
// 非法转义（非 % 开头、截断、非 hex）返回 error——调用方遇到非法值时
// 应当视为「该属性不可用」，而不是猜一个值。
func Decode(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) {
			return "", fmt.Errorf("percent 转义被截断: %q", s)
		}
		h1, ok1 := unhex(s[i+1])
		h2, ok2 := unhex(s[i+2])
		if !ok1 || !ok2 {
			return "", fmt.Errorf("percent 转义非法: %q", s[i:i+3])
		}
		b.WriteByte(h1<<4 | h2)
		i += 2
	}
	return b.String(), nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
