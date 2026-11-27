package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// arkRealResponse 是 2026-11-27 真机打回来的响应（medium 档个人版）。
//
// **它是有价值的历史事实，不是随手编的样例**：
//
//   - `Quota` / `Used` 是 **number**（官方文档写的是 string）→ 解析必须两种都吃
//   - `AFPDaily.Used` 是 **0**，而同刻 5h 窗口有 328.5542 AFP 消耗
//     → 日额度对文本模型推理不计入，所以只报三个窗口
//   - 三个窗口的 `Used` 相同、`Quota` 分别是 10000 / 35000 / 100000
//     → 与公开的档位表逐项吻合
const arkRealResponse = `{
  "ResponseMetadata": {"RequestId": "202611271405236B8F1B01DCF8E8554897", "Action": "GetAFPUsage", "Version": "2024-01-01", "Service": "ark", "Region": "cn-beijing"},
  "Result": {
    "PlanType": "medium",
    "AFPFiveHour": {"Quota": 10000, "Used": 328.5542, "SubscribeTime": 1795748093000, "ResetTime": 1795766093000},
    "AFPWeekly": {"Quota": 35000, "Used": 328.5542, "SubscribeTime": 1795363200000, "ResetTime": 1795968000000},
    "AFPMonthly": {"Quota": 100000, "Used": 328.5542, "SubscribeTime": 1795747501000, "ResetTime": 1798387199000},
    "AFPDaily": {"Quota": 50000, "Used": 0, "SubscribeTime": 1795708800000, "ResetTime": 1795795200000}
  }
}`

// arkTestProvider 造一个指向 mock server 的 provider 输入。
//
// **端点显式配**是为了绕开真实域名；region 仍从 base_url 推导（签名需要它）。
func arkTestProvider(endpoint string) ProviderInfo {
	return ProviderInfo{
		BaseURL:   "https://ark.cn-beijing.volces.com/api/plan",
		Endpoint:  endpoint,
		AccessKey: "AKTESTKEY",
		SecretKey: "SKTESTSECRET",
	}
}

// TestArkMatch 断言识别规则：**host 与 path 两个条件缺一不可**。
//
// 关键用例是按量部署——它跟 Agent Plan **同 host**，只看 host 会认错。
func TestArkMatch(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"https://ark.cn-beijing.volces.com/api/plan", true},
		{"https://ark.cn-shanghai.volces.com/api/plan", true},
		{"https://ark.cn-beijing.volces.com/api/plan/", true},
		// 按量部署：同 host，路径不同 → **不认**
		{"https://ark.cn-beijing.volces.com/api/v3", false},
		// 同路径，不同 host → 不认
		{"https://ark.example.com/api/plan", false},
		// 名字里带 ark 但不是官方域名 → 不认
		{"https://ark.cn-beijing.volces.com.evil.example/api/plan", false},
		{"https://notark.cn-beijing.volces.com/api/plan", false},
	}
	for _, tc := range cases {
		if got := (arkAdapter{}).Match(tc.baseURL); got != tc.want {
			t.Errorf("Match(%q) = %v，期望 %v", tc.baseURL, got, tc.want)
		}
	}
}

// TestArkEndpointDerivation 断言端点推导：管控面 host + 固定 query。
func TestArkEndpointDerivation(t *testing.T) {
	endpoint, ok := arkAdapter{}.Endpoint("https://ark.cn-beijing.volces.com/api/plan")
	if !ok {
		t.Fatal("应能推导出端点")
	}
	want := "https://ark.cn-beijing.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01"
	if endpoint != want {
		t.Errorf("端点 = %q，期望 %q", endpoint, want)
	}

	// 其它 region 自动跟随
	endpoint2, ok := arkAdapter{}.Endpoint("https://ark.cn-shanghai.volces.com/api/plan")
	if !ok || !strings.Contains(endpoint2, "ark.cn-shanghai.volcengineapi.com") {
		t.Errorf("端点应跟随 region: %q %v", endpoint2, ok)
	}

	// 认不出来 → 推不出端点
	if _, ok := (arkAdapter{}).Endpoint("https://ark.cn-beijing.volces.com/api/v3"); ok {
		t.Error("按量部署不该推导出端点")
	}
}

// TestArkFetchRealResponse 用**真机响应**做夹具。
//
// 断言：三个维度（5h / 7d / 30d）、数值正确、**没有 daily 维度**。
func TestArkFetchRealResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("请求没有 Authorization 头")
		}
		if r.Header.Get("X-Date") == "" || r.Header.Get("X-Content-Sha256") == "" {
			t.Error("请求缺少签名头")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(arkRealResponse))
	}))
	defer srv.Close()

	dims, err := arkAdapter{}.Fetch(context.Background(), arkTestProvider(srv.URL))
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if len(dims) != 3 {
		t.Fatalf("应有 3 个维度（5h/周/月），实际 %d 个: %+v", len(dims), dims)
	}

	// 顺序与 ID
	wantIDs := []string{"five_hour", "weekly", "monthly"}
	wantLabels := []string{"5h", "7d", "30d"}
	wantLimits := []float64{10000, 35000, 100000}
	for i, d := range dims {
		if d.ID != wantIDs[i] {
			t.Errorf("维度 %d 的 ID = %q，期望 %q", i, d.ID, wantIDs[i])
		}
		if d.Label != wantLabels[i] {
			t.Errorf("维度 %d 的 label = %q，期望 %q", i, d.Label, wantLabels[i])
		}
		if d.Kind != KindWindow {
			t.Errorf("维度 %d 的 kind = %q，期望 window", i, d.Kind)
		}
		if d.Unit != "AFP" {
			t.Errorf("维度 %d 的 unit = %q，期望 AFP", i, d.Unit)
		}
		if d.Limit == nil || *d.Limit != wantLimits[i] {
			t.Errorf("维度 %d 的 limit = %v，期望 %v", i, d.Limit, wantLimits[i])
		}
		if d.Used == nil || *d.Used != 328.5542 {
			t.Errorf("维度 %d 的 used = %v，期望 328.5542", i, d.Used)
		}
		if d.Remaining == nil || *d.Remaining != wantLimits[i]-328.5542 {
			t.Errorf("维度 %d 的 remaining = %v", i, d.Remaining)
		}
		if d.ResetsAt == nil {
			t.Errorf("维度 %d 缺重置时间", i)
		}
	}
}

// TestArkFetchNoDailyDimension 断言**日额度窗口不上报**。
//
// 实测 AFPDaily.Used 恒为 0（它只计图片/语音模型与 Harness 能力调用），
// 报出来只是给状态栏多占一行。
func TestArkFetchNoDailyDimension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(arkRealResponse))
	}))
	defer srv.Close()

	dims, err := arkAdapter{}.Fetch(context.Background(), arkTestProvider(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dims {
		if d.ID == "daily" {
			t.Errorf("不该报 daily 维度: %+v", d)
		}
	}
}

// TestArkAmountAcceptsNumberAndString 断言额度字段两种形态都能解。
//
// 官方文档写 string，真机返回 number —— 两种都得吃。
func TestArkAmountAcceptsNumberAndString(t *testing.T) {
	cases := []struct {
		json      string
		wantOK    bool
		wantValue float64
	}{
		{`10000`, true, 10000},
		{`"10000"`, true, 10000},
		{`328.5542`, true, 328.5542},
		{`"328.5542"`, true, 328.5542},
		{`0`, true, 0},
		{`""`, false, 0},
		{`null`, false, 0},
		{`"abc"`, false, 0},
		{`"12abc"`, false, 0},
	}
	for _, tc := range cases {
		var a arkAmount
		if err := a.UnmarshalJSON([]byte(tc.json)); err != nil {
			t.Errorf("UnmarshalJSON(%s) 返回了 error: %v", tc.json, err)
		}
		if a.ok != tc.wantOK || (tc.wantOK && a.value != tc.wantValue) {
			t.Errorf("UnmarshalJSON(%s) = {ok:%v value:%v}，期望 {ok:%v value:%v}",
				tc.json, a.ok, a.value, tc.wantOK, tc.wantValue)
		}
	}
}

// TestArkFetchUnparsableAmountDropsOnlyThatWindow 断言字段级降级。
//
// 某个窗口的额度读不出来 → 只丢那一个维度，**其余照报**。
func TestArkFetchUnparsableAmountDropsOnlyThatWindow(t *testing.T) {
	body := `{"Result":{"PlanType":"medium",
		"AFPFiveHour":{"Quota":"","Used":1},
		"AFPWeekly":{"Quota":35000,"Used":150},
		"AFPMonthly":{"Quota":100000,"Used":850.5}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	dims, err := arkAdapter{}.Fetch(context.Background(), arkTestProvider(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if len(dims) != 2 {
		t.Fatalf("应剩 2 个维度，实际 %d: %+v", len(dims), dims)
	}
	for _, d := range dims {
		if d.ID == "five_hour" {
			t.Error("额度读不出来的窗口不该被报出来")
		}
	}
}

// TestArkFetchZeroQuotaIsUnlimited 断言额度为 0 = 无上限。
func TestArkFetchZeroQuotaIsUnlimited(t *testing.T) {
	body := `{"Result":{"PlanType":"medium",
		"AFPFiveHour":{"Quota":0,"Used":0},
		"AFPWeekly":{"Quota":35000,"Used":150},
		"AFPMonthly":{"Quota":100000,"Used":850.5}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	dims, err := arkAdapter{}.Fetch(context.Background(), arkTestProvider(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	var five *Dimension
	for i := range dims {
		if dims[i].ID == "five_hour" {
			five = &dims[i]
		}
	}
	if five == nil {
		t.Fatal("缺少 five_hour 维度")
	}
	if !five.Unlimited {
		t.Error("额度为 0 应报成 unlimited")
	}
	if five.Limit != nil {
		t.Errorf("unlimited 时不该报总额度: %v", *five.Limit)
	}
	if five.UsedPct() != nil {
		t.Errorf("unlimited 时不该派生百分比: %v", *five.UsedPct())
	}
}

// TestArkFetchNoResultIsFailure 断言响应里没有 Result 是失败（不是"额度为 0"）。
func TestArkFetchNoResultIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ResponseMetadata":{"RequestId":"x"}}`))
	}))
	defer srv.Close()

	_, err := arkAdapter{}.Fetch(context.Background(), arkTestProvider(srv.URL))
	if err == nil {
		t.Fatal("没有 Result 应报错")
	}
	if !strings.Contains(err.Error(), "Result") {
		t.Errorf("错误信息应说清是形状变了: %v", err)
	}
}

// TestArkFetchRateLimit 断言 429 走 RateLimitError（刷新层据此退避）。
func TestArkFetchRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"Throttling","Message":"slow down"}}}`))
	}))
	defer srv.Close()

	_, err := arkAdapter{}.Fetch(context.Background(), arkTestProvider(srv.URL))
	if err == nil {
		t.Fatal("429 应报错")
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("429 应返回 RateLimitError，实际 %T: %v", err, err)
	}
	if rl.Status != http.StatusTooManyRequests {
		t.Errorf("状态码 = %d", rl.Status)
	}
}

// TestArkFetchErrorCarriesVendorCode 断言错误信息带**厂商自己的错误码**。
//
// 签名被拒（403）、凭证不对（401）、权限不足是三回事——笼统报状态码会让
// 用户查错方向。
func TestArkFetchErrorCarriesVendorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"ResponseMetadata":{"Error":{"CodeN":100026,"Code":"InvalidSecretToken","Message":"Invalid secret token, msg: token expired."}}}`))
	}))
	defer srv.Close()

	_, err := arkAdapter{}.Fetch(context.Background(), arkTestProvider(srv.URL))
	if err == nil {
		t.Fatal("403 应报错")
	}
	if !strings.Contains(err.Error(), "InvalidSecretToken") {
		t.Errorf("错误信息应带厂商错误码: %v", err)
	}
	if !strings.Contains(err.Error(), "token expired") {
		t.Errorf("错误信息应带厂商说明: %v", err)
	}
}

// TestArkFetchMissingCredentials 断言凭证缺失/只配一半时的报错要说清缺什么。
func TestArkFetchMissingCredentials(t *testing.T) {
	cases := []struct {
		name       string
		accessKey  string
		secretKey  string
		wantSubstr string
	}{
		{"都没配", "", "", "Access Key"},
		{"只配 accessKey", "AK", "", "一半"},
		{"只配 secretKey", "", "SK", "一半"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := arkTestProvider("http://127.0.0.1:1/")
			p.AccessKey = tc.accessKey
			p.SecretKey = tc.secretKey
			_, err := arkAdapter{}.Fetch(context.Background(), p)
			if err == nil {
				t.Fatal("凭证不全应报错")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("错误信息应含 %q: %v", tc.wantSubstr, err)
			}
			// **绝不能把凭证写进错误信息**。
			if tc.accessKey != "" && strings.Contains(err.Error(), tc.accessKey) {
				t.Errorf("错误信息泄露了 access key: %v", err)
			}
			if tc.secretKey != "" && strings.Contains(err.Error(), tc.secretKey) {
				t.Errorf("错误信息泄露了 secret key: %v", err)
			}
		})
	}
}

// TestArkPoolKey 断言额度池键是 **Access Key**（不是 API key）。
//
// 方舟的 AFP 额度是账号套餐级的：同一对 AK 下的多个推理 key 共用一个池。
func TestArkPoolKey(t *testing.T) {
	p := ProviderInfo{APIKey: "ark-推理key", AccessKey: "AK-LONGLIVED"}
	if got := (arkAdapter{}).PoolKey(p); got != "AK-LONGLIVED" {
		t.Errorf("池键 = %q，期望用 Access Key", got)
	}
	// 没有 AK 时回退 API key（池键为空会把所有 provider 挤进一个池）。
	p2 := ProviderInfo{APIKey: "ark-推理key"}
	if got := (arkAdapter{}).PoolKey(p2); got != "ark-推理key" {
		t.Errorf("无 AK 时应回退 API key, got %q", got)
	}
}

// TestArkPoolIDNotPlaintext 断言池键**不明文落盘**。
func TestArkPoolIDNotPlaintext(t *testing.T) {
	pool := PoolID("https://ark.cn-beijing.volcengineapi.com/?Action=GetAFPUsage", "AK-SECRET-VALUE")
	if strings.Contains(pool, "AK-SECRET-VALUE") {
		t.Errorf("池标识里不该有明文凭证: %s", pool)
	}
	if len(pool) != 64 {
		t.Errorf("池标识应是 sha256 十六进制: %s", pool)
	}
}

// TestArkResetsAtFromResetTime 断言重置时间取自 ResetTime（epoch 毫秒）。
func TestArkResetsAtFromResetTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(arkRealResponse))
	}))
	defer srv.Close()

	dims, err := arkAdapter{}.Fetch(context.Background(), arkTestProvider(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	want := time.UnixMilli(1795766093000).UTC()
	if dims[0].ResetsAt == nil || !dims[0].ResetsAt.Equal(want) {
		t.Errorf("重置时间 = %v，期望 %v", dims[0].ResetsAt, want)
	}
}
