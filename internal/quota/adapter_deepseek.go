package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// deepseekAdapter 适配 DeepSeek 的余额接口。
//
// 端点：https://<base_url 的主机名>/user/balance（**同主机换路径**，不是换子域）
type deepseekAdapter struct{}

func (deepseekAdapter) Name() string { return "deepseek" }

// Match 识别 DeepSeek：剥掉 api. / www. 之后主机名恰好等于 deepseek.com。
func (deepseekAdapter) Match(baseURL string) bool {
	_, ok := deepseekHost(baseURL)
	return ok
}

// Endpoint 推导 DeepSeek 的额度端点（同主机换路径）。
func (deepseekAdapter) Endpoint(baseURL string) (string, bool) {
	host, ok := deepseekHost(baseURL)
	if !ok {
		return "", false
	}
	return "https://" + host + "/user/balance", true
}

// deepseekHost 剥掉 api. / www. 前缀并校验主机名；不匹配时返回 ("", false)。
//
// 返回的是**原始主机名**（含 api. 前缀）——DeepSeek 的端点是同主机换路径，
// 端点必须跟着 base_url 走。
func deepseekHost(baseURL string) (string, bool) {
	host := hostOf(baseURL)
	if host == "" {
		return "", false
	}
	h := strings.ToLower(host)
	h = strings.TrimPrefix(h, "api.")
	h = strings.TrimPrefix(h, "www.")
	if h != "deepseek.com" {
		return "", false
	}
	return host, true
}

// deepseekResp 是 DeepSeek 余额接口的响应。
type deepseekResp struct {
	IsAvailable  bool `json:"is_available"`
	BalanceInfos []struct {
		Currency        string `json:"currency"`
		TotalBalance    string `json:"total_balance"`
		GrantedBalance  string `json:"granted_balance"`
		ToppedUpBalance string `json:"topped_up_balance"`
	} `json:"balance_infos"`
}

// Fetch 查询 DeepSeek 余额。
//
// **成败看 HTTP 状态码**（401 = key 不对）。错误体**可能是纯文本**（不带
// Authorization 头时返回 "Authentication Fails (governor)"）——fetchJSON 在
// 非 2xx 时不会去解析 JSON，已经覆盖了这个情况。
func (deepseekAdapter) Fetch(ctx context.Context, p ProviderInfo) ([]Dimension, error) {
	endpoint := p.Endpoint
	if endpoint == "" {
		var ok bool
		endpoint, ok = deepseekAdapter{}.Endpoint(p.BaseURL)
		if !ok {
			return nil, fmt.Errorf("无法从 base_url %q 推导 DeepSeek 额度端点", p.BaseURL)
		}
	}

	body, status, err := fetchJSON(ctx, newHTTPClient(p.Env), endpoint, p.APIKey)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		// 错误体**可能是纯文本**（不带 Authorization 头时实测返回
		// "Authentication Fails (governor)"）——不强行解析成 JSON。
		return nil, fmt.Errorf("DeepSeek 额度接口返回 HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}

	var resp deepseekResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("DeepSeek 额度响应解析失败: %w", err)
	}

	d := Dimension{ID: "balance", Kind: KindBalance}

	if !resp.IsAvailable {
		// 账户不可用 → 剩余量为 0。
		d.Remaining = f64(0)
		d.Finalize()
		return []Dimension{d}, nil
	}

	if len(resp.BalanceInfos) == 0 {
		return nil, nil // 没有余额信息 → 维度缺失（不编造 0）
	}

	info := resp.BalanceInfos[0]
	// total_balance 是**剩余**金额（官方文档："总的可用余额"），不是"已用"。
	amount, err := strconv.ParseFloat(strings.TrimSpace(info.TotalBalance), 64)
	if err != nil {
		return nil, fmt.Errorf("DeepSeek 余额 %q 不是数字: %w", info.TotalBalance, err)
	}
	d.Remaining = f64(amount)
	d.Unit = info.Currency
	d.Raw = map[string]any{
		"is_available":      resp.IsAvailable,
		"granted_balance":   info.GrantedBalance,
		"topped_up_balance": info.ToppedUpBalance,
	}
	d.Finalize()

	// **没有总额度 → Limit 留 nil → 派生不出百分比 → kind = balance**。
	return []Dimension{d}, nil
}
