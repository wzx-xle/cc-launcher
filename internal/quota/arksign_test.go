package quota

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// arkGoldenInstant 是黄金向量用的固定签名时刻。
//
// **必须是固定时刻**——签名把 X-Date 算进哈希，用"当前时间"就永远算不出
// 同一个签名，测试也就锁不住外部契约了。
func arkGoldenInstant(t *testing.T) time.Time {
	t.Helper()
	ts, err := time.Parse("20060102T150405Z", "20261127T032146Z")
	if err != nil {
		t.Fatalf("解析固定时刻失败: %v", err)
	}
	return ts
}

// arkGoldenRequest 造一个与真机请求同形状的请求。
func arkGoldenRequest() *http.Request {
	req, err := http.NewRequest("POST",
		"https://ark.cn-beijing.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01",
		strings.NewReader("{}"))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestArkSignGoldenVector 断言签名与官方 SDK 的输出**逐字节相同**。
//
// **这条测试是签名实现的全部外部契约。** 两个向量都由官方
// `volcengine-go-sdk v1.0.260` 的 `base.Credentials.Sign()` 产出——
// 2026-11-27 已实测确认与自研实现一致。
//
// 任何一个字节对不上，服务端就是 403，而错误信息完全看不出是哪一处错了。
func TestArkSignGoldenVector(t *testing.T) {
	cases := []struct {
		name         string
		sessionToken string
		wantAuth     string
	}{
		{
			// 长期 AK：不带 session token，参与签名的头是四个。
			name: "长期 access key",
			wantAuth: "HMAC-SHA256 Credential=AKTESTKEY/20261127/cn-beijing/ark/request, " +
				"SignedHeaders=content-type;host;x-content-sha256;x-date, " +
				"Signature=17647640ac5545aa9c6d4990d0059db3ff19d4a098411b9381157953d62f78a5",
		},
		{
			// STS 临时凭证：带 session token，且**该头参与签名**。
			name:         "临时 access key 带 session token",
			sessionToken: "FAKESESSIONTOKEN0123456789abcdefghijklmnopqrstuvwxyz",
			wantAuth: "HMAC-SHA256 Credential=AKTESTKEY/20261127/cn-beijing/ark/request, " +
				"SignedHeaders=content-type;host;x-content-sha256;x-date;x-security-token, " +
				"Signature=0cb9af2422c98eaf62e4c9df114f8aa841e2038a0fd0a4e7a97385059bbb4666",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := arkGoldenRequest()
			signer := arkSigner{
				accessKeyID:     "AKTESTKEY",
				secretAccessKey: "SKTESTSECRET",
				sessionToken:    tc.sessionToken,
				region:          "cn-beijing",
			}
			arkSign(req, []byte("{}"), signer, arkGoldenInstant(t))

			if got := req.Header.Get("Authorization"); got != tc.wantAuth {
				t.Errorf("Authorization 不匹配\n got: %s\nwant: %s", got, tc.wantAuth)
			}
			if got := req.Header.Get("X-Date"); got != "20261127T032146Z" {
				t.Errorf("X-Date = %q，期望 20261127T032146Z", got)
			}
			// sha256("{}")
			const wantBodyHash = "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
			if got := req.Header.Get("X-Content-Sha256"); got != wantBodyHash {
				t.Errorf("X-Content-Sha256 = %q，期望 %s", got, wantBodyHash)
			}
		})
	}
}

// TestArkSignSessionTokenIsSent 断言 session token 真的发出去（不只是参与签名）。
//
// 参与签名但不发送 → 服务端认不出这是临时凭证 → 401 InvalidSecretToken（实测）。
func TestArkSignSessionTokenIsSent(t *testing.T) {
	req := arkGoldenRequest()
	signer := arkSigner{
		accessKeyID:     "AKTESTKEY",
		secretAccessKey: "SKTESTSECRET",
		sessionToken:    "FAKESESSIONTOKEN",
		region:          "cn-beijing",
	}
	arkSign(req, []byte("{}"), signer, arkGoldenInstant(t))

	if got := req.Header.Get("X-Security-Token"); got != "FAKESESSIONTOKEN" {
		t.Errorf("X-Security-Token = %q，期望 FAKESESSIONTOKEN", got)
	}
}

// TestArkSignExtraVendorHeaderIsSigned 是**护栏测试**。
//
// 官方规则是"所有 `X-` 前缀的头都参与签名"。若有人把参与签名的头改成写死的
// 列表，多发一个 `X-` 头就会**漏签** → 服务端 403，而错误信息完全看不出是漏签。
func TestArkSignExtraVendorHeaderIsSigned(t *testing.T) {
	req := arkGoldenRequest()
	req.Header.Set("X-Extra-Thing", "some-value")
	signer := arkSigner{
		accessKeyID:     "AKTESTKEY",
		secretAccessKey: "SKTESTSECRET",
		region:          "cn-beijing",
	}
	arkSign(req, []byte("{}"), signer, arkGoldenInstant(t))

	auth := req.Header.Get("Authorization")
	if !strings.Contains(auth, "x-extra-thing") {
		t.Errorf("额外的 X- 头没有参与签名（漏签会导致 403）: %s", auth)
	}
	// 非 X- 前缀的头不参与签名。
	req2 := arkGoldenRequest()
	req2.Header.Set("Accept", "application/json")
	arkSign(req2, []byte("{}"), signer, arkGoldenInstant(t))
	if strings.Contains(req2.Header.Get("Authorization"), "accept") {
		t.Errorf("非 X- 前缀的头不该参与签名: %s", req2.Header.Get("Authorization"))
	}
}

// TestArkSignCanonicalQueryEscapesSpace 断言查询串里的空格编码成 %20 而不是 +。
//
// Go 的 url.Values.Encode() 用 `+` 表示空格，而签名要求 RFC3986 的 `%20`。
// 不换就会算出不同的签名 → 403。
func TestArkSignCanonicalQueryEscapesSpace(t *testing.T) {
	q := url.Values{"Name": {"a b"}}
	got := arkCanonicalQuery(q)
	if got != "Name=a%20b" {
		t.Errorf("canonical query = %q，期望 Name=a%%20b", got)
	}
	if strings.Contains(got, "+") {
		t.Errorf("canonical query 里不该有 '+': %q", got)
	}
}

// TestArkSignEmptyBody 用**空 body**而不是 "{}"。
//
// 两者哈希不同：空 body 用 sha256(空串) = e3b0c442…b855。
// 算错就是 403。
func TestArkSignEmptyBody(t *testing.T) {
	const wantEmptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := arkBodyHash(nil); got != wantEmptyHash {
		t.Errorf("空 body 的哈希 = %q，期望 %s", got, wantEmptyHash)
	}
	if got := arkBodyHash([]byte{}); got != wantEmptyHash {
		t.Errorf("空切片的哈希 = %q，期望 %s", got, wantEmptyHash)
	}
}

// TestArkHostForSigning 断言端口 80/443 被去掉、其它端口保留。
//
// 与官方 SDK 的 getCanonicalHeaders 同口径——服务端按同一口径算。
func TestArkHostForSigning(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ark.cn-beijing.volcengineapi.com", "ark.cn-beijing.volcengineapi.com"},
		{"ark.cn-beijing.volcengineapi.com:443", "ark.cn-beijing.volcengineapi.com"},
		{"ark.cn-beijing.volcengineapi.com:80", "ark.cn-beijing.volcengineapi.com"},
		{"ark.cn-beijing.volcengineapi.com:8443", "ark.cn-beijing.volcengineapi.com:8443"},
	}
	for _, tc := range cases {
		req, err := http.NewRequest("POST", "https://"+tc.in+"/?Action=X", nil)
		if err != nil {
			t.Fatalf("构造请求失败: %v", err)
		}
		if got := arkHostForSigning(req); got != tc.want {
			t.Errorf("host(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}
