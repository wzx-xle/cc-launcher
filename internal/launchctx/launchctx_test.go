package launchctx

import (
	"strings"
	"testing"
)

// TestEncode_OnlyUnreserved 断言编码后的值只由 unreserved 字符与百分号转义
// 组成（spec/session-context-attrs §"attribute values are percent-encoded"）。
//
// 允许出现的字符：unreserved 集合 + '%'（转义引导符）。不允许出现
// 空格 / 逗号 / 分号 / 反斜杠 / 非 ASCII 字节——这些正是 Claude Code 判为
// "非法值 → 丢弃整份属性 map"的触发条件。
func TestEncode_OnlyUnreserved(t *testing.T) {
	cases := []string{
		"/home/u/my project",
		"/home/u/a,b;c",
		"/home/u/测试项目",
		"plain-path_1.0~x",
		"tab\there\nnewline",
	}
	for _, in := range cases {
		out := Encode(in)
		for i := 0; i < len(out); i++ {
			c := out[i]
			if c == '%' {
				continue
			}
			if !isUnreserved(c) {
				t.Fatalf("Encode(%q) = %q 含非法字符 %q", in, out, c)
			}
		}
		// % 必须是合法的 %XX 转义
		for i := 0; i < len(out); i++ {
			if out[i] != '%' {
				continue
			}
			if i+2 >= len(out) {
				t.Fatalf("Encode(%q) = %q 有截断的转义", in, out)
			}
			if _, ok := unhex(out[i+1]); !ok {
				t.Fatalf("Encode(%q) = %q 转义非法", in, out)
			}
			if _, ok := unhex(out[i+2]); !ok {
				t.Fatalf("Encode(%q) = %q 转义非法", in, out)
			}
		}
	}
}

// TestEncodeDecode_RoundTrip 断言编码可逆：decode 回来与原值逐字节一致。
//
// 覆盖四种情形：空格路径 / 逗号路径 / 中文路径 / 超长路径。
func TestEncodeDecode_RoundTrip(t *testing.T) {
	cases := []string{
		"/home/u/my project",                  // 空格
		"/home/u/a,b;c\\d",                    // 逗号 / 分号 / 反斜杠
		"/home/u/测试项目",                      // 中文（多字节）
		"/home/u/" + strings.Repeat("x", 300), // 超长
	}
	for _, in := range cases {
		encoded := Encode(in)
		got, err := Decode(encoded)
		if err != nil {
			t.Fatalf("Decode(%q) 报错: %v", encoded, err)
		}
		if got != in {
			t.Fatalf("round-trip 不一致:\n in = %q\nout = %q", in, got)
		}
	}
}

// TestAttrs_OmitsOverLong 断言编码后超长的属性被直接丢弃，其余属性保留。
func TestAttrs_OmitsOverLong(t *testing.T) {
	long := "/home/u/" + strings.Repeat("x", MaxEncodedLen) // 编码后 > 255
	c := Context{
		Provider:         "minimax",
		Cwd:              long,
		ProviderEndpoint: "api.minimax.cn",
	}
	attrs := c.Attrs()
	if len(attrs) != 2 {
		t.Fatalf("want 2 attrs (provider + endpoint), got %d: %+v", len(attrs), attrs)
	}
	for _, a := range attrs {
		if a.Key == KeyCwd {
			t.Fatalf("超长 cwd 不应被注入: %+v", attrs)
		}
	}
	if attrs[0].Key != KeyProvider || attrs[0].Value != "minimax" {
		t.Fatalf("第一个属性应是 provider, got %+v", attrs[0])
	}
	if attrs[1].Key != KeyProviderEndpoint || attrs[1].Value != "api.minimax.cn" {
		t.Fatalf("第二个属性应是 provider_endpoint, got %+v", attrs[1])
	}
}

// TestAttrs_OmitsEmpty 断言空值维度不产生属性。
func TestAttrs_OmitsEmpty(t *testing.T) {
	attrs := Context{Provider: "minimax"}.Attrs()
	if len(attrs) != 1 || attrs[0].Key != KeyProvider {
		t.Fatalf("空维度不应产生属性, got %+v", attrs)
	}
}

// TestMerge_PreservesUserValue 断言用户原值保留、ccl 的同名 key 以 ccl 为准。
func TestMerge_PreservesUserValue(t *testing.T) {
	attrs := Context{Provider: "minimax", Cwd: "/home/u/proj"}.Attrs()

	got := Merge("team=platform", attrs)
	if !strings.Contains(got, "team=platform") {
		t.Fatalf("用户原值应保留, got %q", got)
	}
	if !strings.Contains(got, KeyProvider+"=minimax") {
		t.Fatalf("ccl 属性应存在, got %q", got)
	}

	got = Merge(KeyProvider+"=stale,team=platform", attrs)
	if strings.Contains(got, "stale") {
		t.Fatalf("ccl 同名 key 应覆盖用户旧值, got %q", got)
	}
	if !strings.Contains(got, "team=platform") {
		t.Fatalf("用户原值应保留, got %q", got)
	}
}

// TestMerge_EmptyExisting 断言没有既有值时只输出 ccl 自己的条目。
func TestMerge_EmptyExisting(t *testing.T) {
	attrs := Context{Provider: "minimax"}.Attrs()
	got := Merge("", attrs)
	if got != KeyProvider+"=minimax" {
		t.Fatalf("got %q", got)
	}
}

// TestEndpointHost 断言从 base URL 提取 host（不含路径）。
func TestEndpointHost(t *testing.T) {
	cases := map[string]string{
		"https://api.minimax.cn/anthropic": "api.minimax.cn",
		"https://api.deepseek.com":         "api.deepseek.com",
		"http://127.0.0.1:8080":             "127.0.0.1:8080",
		"":                                 "",
		"://bad":                           "",
	}
	for in, want := range cases {
		if got := EndpointHost(in); got != want {
			t.Fatalf("EndpointHost(%q) = %q, want %q", in, got, want)
		}
	}
}
