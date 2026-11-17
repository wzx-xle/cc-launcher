package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// codex2apiAdapter 适配 codex2api 的 API Key 自助用量接口。
//
// 端点：<base_url>/api/key-usage/summary（别名 /api/key-usage/me）
//
// **Match 恒为 false**：codex2api 是自建服务，跑在任意 host 上（实测是裸 IP +
// 端口），没有任何可据以识别的主机特征。识别错比识别不到更糟——会拿着用户的
// API key 去打一个不是 codex2api 的地址。它只能通过 provider 配置里的
// `"quota": {"source": "codex2api"}` 显式启用（与 New API 同款处置）。
type codex2apiAdapter struct{}

func (codex2apiAdapter) Name() string { return "codex2api" }

// Match 恒为 false——codex2api 必须显式配 source。
func (codex2apiAdapter) Match(string) bool { return false }

// codex2apiSummaryPath 是自助用量接口的路径（相对 base_url）。
const codex2apiSummaryPath = "/api/key-usage/summary"

// Endpoint 推导 codex2api 的额度端点（同前缀 + 固定后缀）。
//
// 拼接前去掉 base_url 的尾斜杠（避免 `//api/key-usage/summary`）。
//
// **base_url 里的路径前缀会保留**：反代到子路径的部署（网关把 codex2api 挂在
// `/codex2api` 下）也能用同一个推导规则。
//
// **为什么不用 /api/key-usage/accounts**：那个端点给的是**上游 ChatGPT 账号**
// 的 5h/7d 限额百分比（usage_percent_5h / usage_percent_7d），不是本 key 的
// 额度；而且它默认要求管理员开「账号用量查询」权限，没开时返回 404。
func (codex2apiAdapter) Endpoint(baseURL string) (string, bool) {
	if baseURL == "" {
		return "", false
	}
	return trimTrailingSlash(baseURL) + codex2apiSummaryPath, true
}

// codex2apiDefaultDisplayUnit 是 codex2api 的默认显示单位。
//
// 上游按美元计费（usage_logs 里的 user_billed / cost_in_usd_ticks 都是美元）。
const codex2apiDefaultDisplayUnit = "USD"

// codex2apiSummaryResp 是自助用量接口的响应（只取用得到的字段）。
//
// **key 对象用指针**：响应里没有 key 时要能认出来（当成"额度 0"就是把
// 接口形状变了悄悄吞掉——那是"额度是 0"与"读不到额度"的区别）。
type codex2apiSummaryResp struct {
	Key *struct {
		Name        string         `json:"name"`
		QuotaLimit  float64        `json:"quota_limit"`
		QuotaUsed   float64        `json:"quota_used"`
		TotalUsed   float64        `json:"total_used"`
		ResetCount  int            `json:"reset_count"`
		LastResetAt *string        `json:"last_reset_at"`
		CreatedAt   string         `json:"created_at"`
		Status      string         `json:"status"`
		Limits      map[string]any `json:"limits"`
	} `json:"key"`
}

// Fetch 查询 codex2api 的 key 级额度。
//
// **口径（上游源码 database/postgres.go 实测）**：
//
//		UPDATE api_keys SET quota_used = ... + $1, total_used = ... + $1
//		-- ResetAPIKeyQuota：只清 quota_used，total_used 不动
//
//	  - quota_used  = 自上次**手动**重置以来的累计花费（"配额周期"由管理员点
//	    重置才换，没有天/周的自然周期）
//	  - total_used  = 历史总花费（永不清零）
//	  - quota_limit = 当前配额周期的上限，0 = 无限额度
//
// 所以**只用 quota_used 配 quota_limit**（同一口径，服务器的额度耗尽判定
// 就是 `QuotaLimit > 0 && QuotaUsed >= QuotaLimit`）；total_used 是另一个口径
// 的历史累计，混进来会算出假百分比 —— 它只进 Raw 供排查。
func (codex2apiAdapter) Fetch(ctx context.Context, p ProviderInfo) ([]Dimension, error) {
	endpoint := p.Endpoint
	if endpoint == "" {
		var ok bool
		endpoint, ok = codex2apiAdapter{}.Endpoint(p.BaseURL)
		if !ok {
			return nil, fmt.Errorf("无法从 base_url %q 推导 codex2api 额度端点", p.BaseURL)
		}
	}

	body, status, err := fetchJSON(ctx, newHTTPClient(p.Env), endpoint, p.APIKey)
	if err != nil {
		return nil, err
	}

	var resp codex2apiSummaryResp
	if err := json.Unmarshal(body, &resp); err != nil {
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("codex2api 额度接口返回 HTTP %d: %s",
				status, codex2apiBodySnippet(body))
		}
		return nil, fmt.Errorf("codex2api 额度响应解析失败: %w", err)
	}
	// **状态码先看**：404 有两种含义（服务端关了公开用量页 / key 不存在），
	// 401 是 key 无效或被停用 —— 都要把服务端的原文带上（用户据此知道该去
	// 后台开开关还是换 key）。
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("codex2api 额度接口返回 HTTP %d: %s",
			status, codex2apiBodySnippet(body))
	}
	// 响应里没有 key 对象 → 接口形状变了（不是"额度为 0"）。
	if resp.Key == nil {
		return nil, fmt.Errorf("codex2api 额度响应里没有 key 对象: %s", codex2apiBodySnippet(body))
	}

	key := resp.Key

	unit := p.DisplayUnit
	if unit == "" {
		unit = codex2apiDefaultDisplayUnit
	}

	d := Dimension{
		ID:   "quota",
		Kind: KindQuota,
		Unit: unit,
		Raw: map[string]any{
			"name":          key.Name,
			"status":        key.Status,
			"quota_limit":   key.QuotaLimit,
			"quota_used":    key.QuotaUsed,
			"total_used":    key.TotalUsed,
			"reset_count":   key.ResetCount,
			"created_at":    key.CreatedAt,
			"last_reset_at": key.LastResetAt,
		},
	}

	// **quota_limit == 0 = 无限额度**：没有上限就没有分母 → 不派生百分比，
	// 也不报总额（报 0 会让人以为"上限是 0，用量必然超"）。
	if key.QuotaLimit > 0 {
		d.Limit = f64(key.QuotaLimit)
		d.Used = f64(key.QuotaUsed)
		// 剩余量自己算：上游的自助接口不报它。夹到 0（用超了就是没剩，
		// 不是负数——负的剩余量会让消费方的条形图算出负百分比）。
		remaining := key.QuotaLimit - key.QuotaUsed
		if remaining < 0 {
			remaining = 0
		}
		d.Remaining = f64(remaining)
	} else {
		d.Unlimited = true
		d.Used = f64(key.QuotaUsed)
	}

	// **不填 ResetsAt**：quota_used 的"配额周期"结束于管理员手动点重置的那
	// 一刻 —— 不可知。填 last_reset_at（那是**过去**的时刻）会让人以为额度
	// 马上要恢复。它留在 Raw 里。

	d.Finalize()
	return []Dimension{d}, nil
}

// codex2apiBodySnippet 截断响应体摘要（用于错误信息）。
//
// 服务端的错误信息是排查的第一手线索（"API Key 自助用量页未启用"、
// "账号用量查询未启用"…），不能丢；但响应体可能很大（usage 报告里带
// recent_logs），所以截断。
func codex2apiBodySnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
