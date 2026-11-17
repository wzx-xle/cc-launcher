package quota

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEndpointDerivation 断言端点推导是 **per-adapter** 的（三家规律各不相同）。
//
// 任何全局规则都会推出错端点：识别成 MiniMax 就推 www.<host>，接 DeepSeek 会
// 推出 www.deepseek.com（错的）。
func TestEndpointDerivation(t *testing.T) {
	cases := []struct {
		adapter  Adapter
		baseURL  string
		want     string
		wantFail bool
	}{
		// MiniMax：换子域（api. → www.）+ 换路径
		{minimaxAdapter{}, "https://api.minimax.cn/anthropic", "https://www.minimax.cn/v1/token_plan/remains", false},
		{minimaxAdapter{}, "https://api.minimax.io/anthropic", "https://www.minimax.io/v1/token_plan/remains", false},
		// DeepSeek：同主机 + 换路径
		{deepseekAdapter{}, "https://api.deepseek.com/anthropic", "https://api.deepseek.com/user/balance", false},
		// New API：同前缀 + 固定后缀
		{newapiAdapter{}, "https://one.example.com/api/llm/console/chat", "https://one.example.com/api/llm/console/chat/api/usage/token/", false},
		// 尾斜杠规范化：拼接前去掉 base_url 的尾斜杠
		{newapiAdapter{}, "https://one.example.com/api/llm/console/chat/", "https://one.example.com/api/llm/console/chat/api/usage/token/", false},
		// codex2api：同前缀 + 固定后缀（公开自助用量页）
		{codex2apiAdapter{}, "http://49.51.34.139:8317", "http://49.51.34.139:8317/api/key-usage/summary", false},
		{codex2apiAdapter{}, "http://49.51.34.139:8317/", "http://49.51.34.139:8317/api/key-usage/summary", false},
		// base_url 里的路径前缀保留（反代到子路径的部署）
		{codex2apiAdapter{}, "https://gw.example.com/codex2api", "https://gw.example.com/codex2api/api/key-usage/summary", false},
		// 识别不出来 → 推导不出端点
		{minimaxAdapter{}, "https://minimax-proxy.corp.example/anthropic", "", true},
		{deepseekAdapter{}, "https://api.example.com/anthropic", "", true},
		{codex2apiAdapter{}, "", "", true},
	}
	for _, tc := range cases {
		got, ok := tc.adapter.Endpoint(tc.baseURL)
		if tc.wantFail {
			if ok {
				t.Errorf("%s.Endpoint(%q) = %q, 期望推导失败", tc.adapter.Name(), tc.baseURL, got)
			}
			continue
		}
		if !ok {
			t.Errorf("%s.Endpoint(%q) 推导失败", tc.adapter.Name(), tc.baseURL)
			continue
		}
		if got != tc.want {
			t.Errorf("%s.Endpoint(%q) = %q, want %q", tc.adapter.Name(), tc.baseURL, got, tc.want)
		}
	}
}

// TestMatch 断言 host 识别规则：认得 minimax.cn / minimax.io / deepseek.com，
// **认不得**"含 minimax 但不相等"的自建域名。
func TestMatch(t *testing.T) {
	if !(minimaxAdapter{}).Match("https://api.minimax.cn/anthropic") {
		t.Error("api.minimax.cn 应被识别")
	}
	if !(minimaxAdapter{}).Match("https://www.minimax.io/anthropic") {
		t.Error("www.minimax.io 应被识别")
	}
	if (minimaxAdapter{}).Match("https://minimax-proxy.corp.example/anthropic") {
		t.Error("minimax-proxy.corp.example 不该被识别（含 minimax 但不相等）")
	}
	if !(deepseekAdapter{}).Match("https://api.deepseek.com/anthropic") {
		t.Error("api.deepseek.com 应被识别")
	}
	if (deepseekAdapter{}).Match("https://api.deepseek-proxy.example/anthropic") {
		t.Error("deepseek-proxy.example 不该被识别")
	}
	// New API 无法靠 host 识别（任何 host 都可能是它）→ 必须显式配 source。
	if (newapiAdapter{}).Match("https://one.example.com/api/llm/console/chat") {
		t.Error("New API 不该靠 host 识别")
	}
	// codex2api 同理：跑在任意 host 上（实测是裸 IP + 端口）→ 只能显式配 source。
	// 认错的后果是拿着用户的 API key 去打一个不是 codex2api 的地址。
	if (codex2apiAdapter{}).Match("http://49.51.34.139:8317") {
		t.Error("codex2api 不该靠 host 识别")
	}
}

// TestDetectAdapter 断言按 host 的兜底检测。
func TestDetectAdapter(t *testing.T) {
	ad, ok := DetectAdapter("https://api.minimax.cn/anthropic")
	if !ok || ad.Name() != "minimax" {
		t.Errorf("应识别为 minimax: %v %v", ad, ok)
	}
	if _, ok := DetectAdapter("https://api.example.com/anthropic"); ok {
		t.Error("认不出的 host 不该被检测到")
	}
}

// TestAdapterByName 断言按 source 查找。
func TestAdapterByName(t *testing.T) {
	for _, name := range []string{"minimax", "deepseek", "newapi", "codex2api"} {
		ad, ok := AdapterByName(name)
		if !ok {
			t.Errorf("adapter %q 应存在", name)
			continue
		}
		if ad.Name() != name {
			t.Errorf("adapter 名不匹配: %q vs %q", ad.Name(), name)
		}
	}
	if _, ok := AdapterByName("nope"); ok {
		t.Error("不存在的 adapter 名不该被找到")
	}
}

// --- MiniMax -------------------------------------------------------------

// TestMiniMaxFetch 断言 MiniMax 的解析：剩余% → 已用%、窗口时长从
// start/end 算、重置时间从 remains_time 算。
func TestMiniMaxFetch(t *testing.T) {
	body := `{
	  "model_remains": [{
	    "start_time": 1793943914255,
	    "end_time": 1793961914255,
	    "remains_time": 3600000,
	    "model_name": "general",
	    "weekly_start_time": 1793718756984,
	    "weekly_end_time": 1794323556984,
	    "weekly_remains_time": 86400000,
	    "current_interval_status": 1,
	    "current_weekly_status": 1,
	    "current_interval_remaining_percent": 41,
	    "current_weekly_remaining_percent": 70
	  }],
	  "base_resp": { "status_code": 0, "status_msg": "success" }
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	dims, err := (minimaxAdapter{}).Fetch(context.Background(), ProviderInfo{
		BaseURL:  "https://api.minimax.cn/anthropic",
		APIKey:   "sk-test",
		Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(dims) != 2 {
		t.Fatalf("应有 2 个维度, got %d", len(dims))
	}

	interval := dims[0]
	if interval.Kind != KindWindow {
		t.Errorf("kind = %q", interval.Kind)
	}
	// 窗口时长从 end_time - start_time 算（18000000ms = 18000s）
	if interval.Duration != 18000*time.Second {
		t.Errorf("窗口时长 = %v, want 18000s", interval.Duration)
	}
	if interval.Label != "5h" {
		t.Errorf("label = %q, want 5h", interval.Label)
	}
	// 剩余 41% → 已用 59%
	if pct := interval.UsedPct(); pct == nil || *pct != 59 {
		t.Errorf("已用%% = %v, want 59", pct)
	}
	// 重置时间：now + remains_time
	if interval.ResetsAt == nil {
		t.Error("resets_at 不该为空")
	} else if d := time.Until(*interval.ResetsAt); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("重置时间应约在 1 小时后, got %v", d)
	}
	// 状态字段只透传进 Raw，不解释
	if interval.Raw["current_interval_status"] != 1 {
		t.Errorf("status 应透传进 Raw: %+v", interval.Raw)
	}

	weekly := dims[1]
	if weekly.Duration != 604800*time.Second {
		t.Errorf("周窗口时长 = %v, want 604800s", weekly.Duration)
	}
	if weekly.Label != "7d" {
		t.Errorf("label = %q, want 7d", weekly.Label)
	}
	if pct := weekly.UsedPct(); pct == nil || *pct != 30 {
		t.Errorf("周已用%% = %v, want 30", pct)
	}
}

// TestMiniMaxFetch_ErrorIsHTTP200 断言 **错误也返回 HTTP 200** 时要认出来
// （判据是 body 的 base_resp.status_code，不是 HTTP 状态码）。
func TestMiniMaxFetch_ErrorIsHTTP200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"base_resp":{"status_code":1004,"status_msg":"login fail"}}`)
	}))
	defer srv.Close()

	_, err := (minimaxAdapter{}).Fetch(context.Background(), ProviderInfo{
		APIKey: "sk-bad", Endpoint: srv.URL,
	})
	if err == nil {
		t.Fatal("status_code != 0 应报错")
	}
	if !strings.Contains(err.Error(), "login fail") {
		t.Errorf("错误应带上 status_msg: %v", err)
	}
}

// TestMiniMaxFetch_GeneralPreferred 断言优先取 model_name == "general"。
func TestMiniMaxFetch_GeneralPreferred(t *testing.T) {
	body := `{"model_remains":[
	  {"model_name":"other","start_time":0,"end_time":18000000,"current_interval_remaining_percent":10},
	  {"model_name":"general","start_time":0,"end_time":18000000,"current_interval_remaining_percent":41}
	],"base_resp":{"status_code":0}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	dims, err := (minimaxAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if pct := dims[0].UsedPct(); pct == nil || *pct != 59 {
		t.Errorf("应取 general 条目的 59%%, got %v", pct)
	}
}

// TestMiniMaxFetch_EmptyArray 断言空数组 → 维度缺失（**不编造 0**）。
func TestMiniMaxFetch_EmptyArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"model_remains":[],"base_resp":{"status_code":0}}`)
	}))
	defer srv.Close()

	dims, err := (minimaxAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(dims) != 0 {
		t.Errorf("空数组不该产出维度: %+v", dims)
	}
}

// --- DeepSeek -------------------------------------------------------------

// TestDeepSeekFetch 断言余额解析：字符串数字转 float、货币 → unit、
// **没有总额度 → 无百分比 → kind = balance**。
func TestDeepSeekFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"0.16","topped_up_balance":"109.84"}]}`)
	}))
	defer srv.Close()

	dims, err := (deepseekAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(dims) != 1 {
		t.Fatalf("应有 1 个维度, got %d", len(dims))
	}
	d := dims[0]
	if d.Kind != KindBalance {
		t.Errorf("kind = %q, want balance", d.Kind)
	}
	if d.Unit != "CNY" {
		t.Errorf("unit = %q", d.Unit)
	}
	if d.Label != "CNY" {
		t.Errorf("label = %q, want CNY", d.Label)
	}
	if d.Remaining == nil || *d.Remaining != 110 {
		t.Errorf("remaining = %v, want 110", d.Remaining)
	}
	if d.Limit != nil {
		t.Errorf("无总额度 → Limit 应为 nil, got %v", *d.Limit)
	}
	if pct := d.UsedPct(); pct != nil {
		t.Errorf("无分母 → 不该有百分比, got %v", *pct)
	}
}

// TestDeepSeekFetch_PlainTextError 断言**纯文本**错误体要被认出来
// （不带 Authorization 头时实测返回 "Authentication Fails (governor)"）。
func TestDeepSeekFetch_PlainTextError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "Authentication Fails (governor)")
	}))
	defer srv.Close()

	_, err := (deepseekAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err == nil {
		t.Fatal("401 应报错")
	}
	if !strings.Contains(err.Error(), "Authentication Fails") {
		t.Errorf("错误应带上纯文本内容: %v", err)
	}
}

// TestDeepSeekFetch_Unavailable 断言 is_available=false → 剩余 0。
func TestDeepSeekFetch_Unavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_available":false,"balance_infos":[]}`)
	}))
	defer srv.Close()

	dims, err := (deepseekAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(dims) != 1 || dims[0].Remaining == nil || *dims[0].Remaining != 0 {
		t.Errorf("不可用 → remaining 应为 0: %+v", dims)
	}
}

// --- New API --------------------------------------------------------------

// TestNewAPIFetch 断言令牌额度解析：total_granted → 分母、单位换算、
// unlimited、expires_at。
func TestNewAPIFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":true,"message":"ok","data":{
		  "expires_at":0,"model_limits":{},"model_limits_enabled":false,
		  "name":"jlcao3","object":"token_usage",
		  "total_available":199949277,"total_granted":200000000,"total_used":50723,
		  "unlimited_quota":false}}`)
	}))
	defer srv.Close()

	dims, err := (newapiAdapter{}).Fetch(context.Background(), ProviderInfo{
		Endpoint:     srv.URL,
		QuotaPerUnit: 500000,
		DisplayUnit:  "USD",
	})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	d := dims[0]
	if d.Kind != KindQuota {
		t.Errorf("kind = %q, want quota", d.Kind)
	}
	if d.Unit != "USD" {
		t.Errorf("unit = %q", d.Unit)
	}
	if d.Limit == nil || *d.Limit != 400 {
		t.Errorf("总量应换算成 400, got %v", d.Limit)
	}
	if d.Used == nil || *d.Used != 0.101446 {
		t.Errorf("已用应换算成 0.101446, got %v", d.Used)
	}
	if d.Remaining == nil || *d.Remaining != 399.898554 {
		t.Errorf("剩余应换算成 399.898554, got %v", d.Remaining)
	}
	if d.ExpiresAt != nil {
		t.Errorf("expires_at=0 → 不该有到期时间: %v", d.ExpiresAt)
	}
	if d.Raw["model_limits_enabled"] != false {
		t.Errorf("model_limits 应透传进 Raw: %+v", d.Raw)
	}
}

// TestNewAPIFetch_DefaultQuotaPerUnit 断言没配 quotaPerUnit 时用 500000
// （New API 出厂默认）。
func TestNewAPIFetch_DefaultQuotaPerUnit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":200000000,"total_used":2576,"total_available":199997424}}`)
	}))
	defer srv.Close()

	dims, err := (newapiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if dims[0].Limit == nil || *dims[0].Limit != 400 {
		t.Errorf("总量应换算成 400, got %v", dims[0].Limit)
	}
	if dims[0].Unit != "USD" {
		t.Errorf("默认显示单位应为 USD, got %q", dims[0].Unit)
	}
}

// TestNewAPIFetch_CodeFalse 断言 code=false 是失败。
func TestNewAPIFetch_CodeFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":false,"message":"令牌已过期"}`)
	}))
	defer srv.Close()

	_, err := (newapiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err == nil {
		t.Fatal("code=false 应报错")
	}
	if !strings.Contains(err.Error(), "令牌已过期") {
		t.Errorf("错误应带上 message: %v", err)
	}
}

// TestNewAPIFetch_RejectedKey 断言 401 + error 对象被认出来。
func TestNewAPIFetch_RejectedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":401,"message":"无效的令牌","request_id":"abc","type":"new_api_error"}}`)
	}))
	defer srv.Close()

	_, err := (newapiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err == nil {
		t.Fatal("401 应报错")
	}
	if !strings.Contains(err.Error(), "无效的令牌") {
		t.Errorf("错误应带上 error.message: %v", err)
	}
}

// TestNewAPIFetch_Unlimited 断言 unlimited_quota=true → 无百分比。
func TestNewAPIFetch_Unlimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":0,"total_used":0,"total_available":0,"unlimited_quota":true}}`)
	}))
	defer srv.Close()

	dims, err := (newapiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !dims[0].Unlimited {
		t.Error("应标记为无限额度")
	}
	if pct := dims[0].UsedPct(); pct != nil {
		t.Errorf("无限额度 → 不该有百分比, got %v", *pct)
	}
}

// TestNewAPIFetch_ExpiresAt 断言 expires_at != 0 → 有到期时间。
func TestNewAPIFetch_ExpiresAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1,"expires_at":1794323556}}`)
	}))
	defer srv.Close()

	dims, err := (newapiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if dims[0].ExpiresAt == nil {
		t.Fatal("应有到期时间")
	}
	if dims[0].ExpiresAt.Unix() != 1794323556 {
		t.Errorf("到期时间 = %v", dims[0].ExpiresAt.Unix())
	}
}

// --- codex2api ------------------------------------------------------------

// codex2apiSummaryBody 是实测的响应形状（2026-11-17，v3.1.6）的节选。
//
// key 对象里的字段口径（上游源码 database/postgres.go）：
//   - quota_used  = 自上次**手动**重置以来的累计花费（配额周期由管理员点重置决定）
//   - total_used  = 历史总花费（永不清零）
//   - quota_limit = 当前配额周期的上限，0 = 无限额度
const codex2apiSummaryBody = `{
  "key": {
    "name": "王志翔", "key": "sk-8****...****54f9",
    "quota_limit": 100, "quota_used": 0.8024772, "total_used": 12.5,
    "reset_count": 1, "last_reset_at": "2026-11-17T10:00:00+08:00",
    "limits": {"allow_live": true, "upstream_channel": "codex"},
    "status": "active", "created_at": "2026-11-17T15:38:11+08:00"
  },
  "range": {"name": "30d", "start": "2026-10-18T18:23:33+08:00", "end": "2026-11-17T18:23:33+08:00"},
  "usage": {"summary": {"requests": 45, "tokens": 1667493, "user_billed": 0.8024772}},
  "model_request_usage": []
}`

// TestCodex2APIFetch 断言解析：quota_used / quota_limit → 已用 / 总额。
func TestCodex2APIFetch(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, codex2apiSummaryBody)
	}))
	defer srv.Close()

	dims, err := (codex2apiAdapter{}).Fetch(context.Background(), ProviderInfo{
		Endpoint: srv.URL,
		APIKey:   "sk-secret",
	})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if gotAuth != "Bearer sk-secret" {
		t.Errorf("Authorization = %q, 应为 Bearer <key>", gotAuth)
	}
	if len(dims) != 1 {
		t.Fatalf("应有 1 个维度, got %d", len(dims))
	}
	d := dims[0]
	if d.Kind != KindQuota {
		t.Errorf("kind = %q, want quota", d.Kind)
	}
	if d.Unit != "USD" {
		t.Errorf("unit = %q, want USD", d.Unit)
	}
	if d.Used == nil || *d.Used != 0.8024772 {
		t.Errorf("已用 = %v, want 0.8024772", d.Used)
	}
	if d.Limit == nil || *d.Limit != 100 {
		t.Errorf("总额 = %v, want 100", d.Limit)
	}
	if d.Remaining == nil || *d.Remaining < 99.19 || *d.Remaining > 99.2 {
		t.Errorf("剩余 = %v, want ≈99.1975", d.Remaining)
	}
	if d.Unlimited {
		t.Error("有额度上限时不该标记为无限")
	}
	if pct := d.UsedPct(); pct == nil || *pct < 0.8 || *pct > 0.81 {
		t.Errorf("已用百分比 = %v, want ≈0.802", pct)
	}
}

// TestCodex2APIFetch_NoResetTime 断言**不填重置时间**。
//
// quota_used 的"配额周期"结束于管理员手动点重置的那一刻——不可知。填
// last_reset_at（那是**过去**的时刻）会让人以为额度马上要恢复。
func TestCodex2APIFetch_NoResetTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, codex2apiSummaryBody)
	}))
	defer srv.Close()

	dims, err := (codex2apiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if dims[0].ResetsAt != nil {
		t.Errorf("不该有重置时间: %v", dims[0].ResetsAt)
	}
}

// TestCodex2APIFetch_Unlimited 断言 quota_limit=0 → 无限额度、不派生百分比。
//
// 0 = 这个 key 没有金额上限 → 没有分母 → 百分比无意义（不是"用了 0%"）。
func TestCodex2APIFetch_Unlimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"key":{"name":"王志翔","quota_limit":0,"quota_used":0.8024772,"total_used":0.8024772,"reset_count":0,"status":"active"}}`)
	}))
	defer srv.Close()

	dims, err := (codex2apiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	d := dims[0]
	if !d.Unlimited {
		t.Error("应标记为无限额度")
	}
	if d.Limit != nil {
		t.Errorf("无限额度不该有总额: %v", *d.Limit)
	}
	if pct := d.UsedPct(); pct != nil {
		t.Errorf("无限额度 → 不该有百分比, got %v", *pct)
	}
	if d.Used == nil || *d.Used != 0.8024772 {
		t.Errorf("已用仍应有值: %v", d.Used)
	}
}

// TestCodex2APIFetch_TotalUsedNotUsed 断言 **total_used 不当已用量**。
//
// total_used 是历史累计（永不清零），quota_limit 是当前周期的上限——两个口径
// 混用会算出假百分比。total_used 只进 Raw 供排查。
func TestCodex2APIFetch_TotalUsedNotUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, codex2apiSummaryBody)
	}))
	defer srv.Close()

	dims, err := (codex2apiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	d := dims[0]
	if d.Used == nil || *d.Used == 12.5 {
		t.Errorf("已用不该用 total_used: %v", d.Used)
	}
	if got, ok := d.Raw["total_used"].(float64); !ok || got != 12.5 {
		t.Errorf("total_used 该进 Raw: %+v", d.Raw)
	}
}

// TestCodex2APIFetch_DisplayUnit 断言 displayUnit 能覆盖单位。
func TestCodex2APIFetch_DisplayUnit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, codex2apiSummaryBody)
	}))
	defer srv.Close()

	dims, err := (codex2apiAdapter{}).Fetch(context.Background(), ProviderInfo{
		Endpoint:    srv.URL,
		DisplayUnit: "CNY",
	})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if dims[0].Unit != "CNY" {
		t.Errorf("unit = %q, want CNY", dims[0].Unit)
	}
}

// TestCodex2APIFetch_NotFound 断言 404 是失败（服务端关了公开用量页）。
func TestCodex2APIFetch_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"API Key 自助用量页未启用"}`)
	}))
	defer srv.Close()

	dims, err := (codex2apiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err == nil {
		t.Fatal("404 应报错")
	}
	if dims != nil {
		t.Errorf("失败时不该返回维度: %+v", dims)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("错误应带上状态码: %v", err)
	}
}

// TestCodex2APIFetch_BadJSON 断言解析失败是失败（厂商改了字段）。
func TestCodex2APIFetch_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"key":"not-an-object"}`)
	}))
	defer srv.Close()

	dims, err := (codex2apiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	if err == nil {
		t.Fatal("解析失败应报错")
	}
	if dims != nil {
		t.Errorf("失败时不该返回维度: %+v", dims)
	}
}

// TestCodex2APIFetch_RateLimited 断言 429 被认成限流（走退避，不当普通失败）。
func TestCodex2APIFetch_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":"slow down"}`)
	}))
	defer srv.Close()

	_, err := (codex2apiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL})
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("429 应是 RateLimitError, got %v", err)
	}
	if rle.Status != http.StatusTooManyRequests {
		t.Errorf("状态码 = %d", rle.Status)
	}
}

// --- 重定向 ---------------------------------------------------------------

// TestFetchFollowsRedirect 断言跟随重定向且**保留 Authorization**。
//
// Go 实测：跨 http→https 重定向会保留 Authorization（shouldCopyHeaderOnRedirect
// 按 url.Hostname() 剥端口比对，同主机即保留）。
func TestFetchFollowsRedirect(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			gotAuth = r.Header.Get("Authorization")
			fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer srv.Close()

	_, err := (newapiAdapter{}).Fetch(context.Background(), ProviderInfo{
		Endpoint: srv.URL + "/start",
		APIKey:   "sk-secret",
	})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if gotAuth != "Bearer sk-secret" {
		t.Errorf("重定向后 Authorization = %q, 应保留", gotAuth)
	}
}

// TestFetchDoesNotUpgradeScheme 断言 ccl **不主动把 http 升级成 https**。
//
// 只提供 http 的内网额度服务升级后会 TLS 握手失败 → 额度永远查不到；
// 用户显式配的 endpoint 尤其不该被改。
func TestFetchDoesNotUpgradeScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			t.Error("不该把请求升级成 https")
		}
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	if _, err := (newapiAdapter{}).Fetch(context.Background(), ProviderInfo{Endpoint: srv.URL}); err != nil {
		t.Fatalf("不该报错: %v", err)
	}
}
