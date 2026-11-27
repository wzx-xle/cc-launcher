package quota

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// arkService 是火山引擎方舟的 service 名（签名凭据作用域的一部分）。
const arkService = "ark"

// arkAlgorithm 是火山引擎 V4 签名的算法标识。
const arkAlgorithm = "HMAC-SHA256"

// arkSigner 给一个请求计算火山引擎 V4 签名。
//
// **为什么自己实现而不引官方 SDK**：官方 `volcengine-go-sdk` 有 1600+ 个文件、
// 拖一堆传递依赖，而我们只需要 60 行标准库。签名算法已与官方 SDK 的
// `base.Credentials.Sign()` **逐字节比对通过**（见 arksign_test.go 的黄金向量）。
//
// **为什么签名要有专门的一份**：火山引擎的签名是**版本化的、逐字节精确**的——
// 凭据作用域写成 `AK//20261127/...`（双斜杠）、短日期写成完整时间戳、
// 或参与签名的头少算一个，服务端一律回 403，而错误信息完全看不出是哪一处错了。
// 把它独立成一个可单测的原件，比散落在 adapter 的字符串拼接里可靠得多。
type arkSigner struct {
	// accessKeyID 是 Access Key ID（AK）。
	accessKeyID string
	// secretAccessKey 是 Secret Access Key（SK）。
	secretAccessKey string
	// sessionToken 是 STS 临时凭证的会话令牌；长期 AK 留空。
	//
	// **临时凭证必须带这个头，且这个头必须参与签名**——否则 401
	// `InvalidSecretToken`（实测）。
	sessionToken string
	// region 是凭据作用域里的区域（如 `cn-beijing`）。
	region string
}

// arkSignedHeaders 决定哪些请求头参与签名。
//
// **规则来自官方 SDK 的 sortHeaders，不能简化成"只签那几个头"**：
//
//   - `Content-Type` / `Content-Md5` / `Host` / `X-Security-Token` 参与
//   - **所有 `X-` 前缀的头都参与**（`X-Date`、`X-Content-Sha256`、`X-Security-Token`…）
//   - 其余（`Authorization`、`Accept`、`User-Agent`…）不参与
//
// **为什么必须动态确定**：写死当前这四个头，在本 adapter 的当前形状下确实等价
// （实测与官方 SDK 逐字节相同），但将来多发一个 `X-` 头就会**漏签** → 服务端 403，
// 且报错完全看不出是漏签。这条护栏有单测（见 arksign_test.go）。
func arkSignedHeaders(req *http.Request) map[string]string {
	signed := map[string]string{
		// Go 把 Host 放在 req.Host 而不是 req.Header 里，得手动补上。
		"host": arkHostForSigning(req),
	}
	for name, values := range req.Header {
		if len(values) == 0 {
			continue
		}
		switch name {
		case "Content-Type", "Content-Md5", "X-Security-Token":
		default:
			// 其余头只有 X- 前缀的参与签名。
			if !strings.HasPrefix(name, "X-") {
				continue
			}
		}
		signed[strings.ToLower(name)] = values[0]
	}
	return signed
}

// arkHostForSigning 取参与签名的 host 值。
//
// 端口为 80 / 443 时去掉端口——官方 SDK 的 getCanonicalHeaders 就是这么做的，
// 与服务端的口径保持一致。
func arkHostForSigning(req *http.Request) string {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		if len(parts) == 2 && (parts[1] == "80" || parts[1] == "443") {
			host = parts[0]
		}
	}
	return host
}

// arkCanonicalHeaders 把参与签名的头排成规范形式。
//
// 每行是 `小写头名:去空白的值`，**每行以换行结尾**。
func arkCanonicalHeaders(signed map[string]string) (string, string) {
	names := make([]string, 0, len(signed))
	for name := range signed {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(signed[name]))
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(names, ";")
}

// arkCanonicalQuery 生成参与签名的查询串。
//
// **注意 `+` 要换成 `%20`**：Go 的 `url.Values.Encode()` 用 `+` 表示空格，
// 而签名要求 RFC3986 的 `%20`。不换的话，含空格的参数值会算出不同的签名。
func arkCanonicalQuery(query url.Values) string {
	return strings.ReplaceAll(query.Encode(), "+", "%20")
}

// arkBodyHash 算请求体的 sha256 十六进制值。
//
// 空 body 用**空串**的哈希（`e3b0c442…b855`），不是"{}"的哈希——
// 两者不同，算错就是 403。
func arkBodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// arkSign 给请求加上签名所需的头。
//
// 加三个头：`X-Date`（签名时刻，UTC）、`X-Content-Sha256`（请求体哈希）、
// `Authorization`（签名本身）。**不改动请求体、不改动 URL**。
//
// now 由调用方传入（而不是内部取当前时间）——这样单测能用固定时刻复现签名。
func arkSign(req *http.Request, body []byte, s arkSigner, now time.Time) {
	xDate := now.UTC().Format("20060102T150405Z")
	bodyHash := arkBodyHash(body)

	// 先把这两个头写进请求——它们自己要参与签名。
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", bodyHash)
	if s.sessionToken != "" {
		req.Header.Set("X-Security-Token", strings.TrimSpace(s.sessionToken))
	}

	signed := arkSignedHeaders(req)
	canonicalHeaders, signedHeaders := arkCanonicalHeaders(signed)

	// 凭据作用域：{短日期}/{区域}/{服务}/request
	//
	// **短日期是 8 位日期**（X-Date 的前 8 位），不是完整时间戳；
	// **前缀斜杠只有一个**——写成双斜杠服务端会拒。
	shortDate := xDate[:8]
	scope := strings.Join([]string{shortDate, s.region, arkService, "request"}, "/")

	// 规范请求：方法、路径、查询、头、签名头列表、体哈希——**用 \n 连接**。
	//
	// 注意 canonicalHeaders 段**自带结尾换行**，这里再接一个 \n，
	// 于是头段与签名头列表段之间存在一个空行——少这一个换行签名就全错。
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		arkCanonicalQuery(req.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		bodyHash,
	}, "\n")

	stringToSign := strings.Join([]string{
		arkAlgorithm,
		xDate,
		scope,
		arkBodyHash([]byte(canonicalRequest)),
	}, "\n")

	signature := arkSignature(s.secretAccessKey, shortDate, s.region, stringToSign)

	req.Header.Set("Authorization", arkAlgorithm+
		" Credential="+s.accessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+
		", Signature="+signature)
}

// arkSignature 派生签名密钥并算出签名。
//
// 密钥派生链（官方 SDK 的 signingKeyV4）：每一级都是"以上一级的密钥为 key、
// 以下一级字符串为消息"的 HMAC-SHA256，最后一级固定用 `"request"`。
func arkSignature(secretAccessKey, shortDate, region, stringToSign string) string {
	kDate := arkHMAC([]byte(secretAccessKey), shortDate)
	kRegion := arkHMAC(kDate, region)
	kService := arkHMAC(kRegion, arkService)
	kSigning := arkHMAC(kService, "request")
	return hex.EncodeToString(arkHMAC(kSigning, stringToSign))
}

// arkHMAC 算一次 HMAC-SHA256。
func arkHMAC(key []byte, content string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(content))
	return mac.Sum(nil)
}
