package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// newapiAdapter 适配 New API / one-api 系部署的令牌额度接口。
//
// 端点：<base_url>/api/usage/token（同前缀 + 固定后缀）
//
// **Match 恒为 false**：New API 可以部署在任何 host 下，无法靠 host 识别
// （靠 host 猜 = 猜错 = 拿着 API key 去打一个不是 New API 的地址）。
// 它只能通过 provider 配置里的 `"quota": {"source": "newapi"}` 显式启用。
type newapiAdapter struct{}

func (newapiAdapter) Name() string { return "newapi" }

// Match 恒为 false——New API 系必须显式配 source。
func (newapiAdapter) Match(string) bool { return false }

// Endpoint 推导 New API 的额度端点（同前缀加后缀）。
//
// 拼接前去掉 base_url 的尾斜杠：本机 iflytek 的 base_url 带尾斜杠，直接拼会
// 得到 .../chat//api/usage/token（实测该网关双斜杠也能通，但那是宽容行为
// 不是契约）。
//
// **端点带尾斜杠**（真机实测，2026-11-06）：New API 的路由就是
// `/api/usage/token/`——请求不带尾斜杠的 `/api/usage/token` 会收到 **301**。
// 带尾斜杠一步到位（200），不带则多一跳。
//
// **这一步为什么关键**：本机的讯飞网关把 New API 的 301 **剥掉挂载前缀**后
// 透传（`/api/llm/console/chat/api/usage/token` → Location `/api/usage/token/`）
// ——跟过去就落到网关根路径 → SSO 重定向 → **额度永远查不到**。一步到位
// （直接请求带尾斜杠的端点）绕开了这个坑。
func (newapiAdapter) Endpoint(baseURL string) (string, bool) {
	if baseURL == "" {
		return "", false
	}
	return trimTrailingSlash(baseURL) + "/api/usage/token/", true
}

// newapiDefaultQuotaPerUnit 是 New API 出厂默认的额度点换算比例
// （1 USD = 500000 额度点）。
//
// 实测该实例的 quota_per_unit 恰好等于这个默认值，但**通用 adapter 不应假定
// 能访问 /api/status**（标准路径在本机那个部署被 403）——所以这是一个"假设
// 默认值"，配置里的 quotaPerUnit 可以覆盖它。
const newapiDefaultQuotaPerUnit = 500000

// newapiDefaultDisplayUnit 是 New API 的默认显示单位。
const newapiDefaultDisplayUnit = "USD"

// newapiResp 是 New API 额度接口的响应。
type newapiResp struct {
	Code    bool   `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ExpiresAt          int64          `json:"expires_at"`
		ModelLimits        map[string]any `json:"model_limits"`
		ModelLimitsEnabled bool           `json:"model_limits_enabled"`
		Name               string         `json:"name"`
		Object             string         `json:"object"`
		TotalAvailable     float64        `json:"total_available"`
		TotalGranted       float64        `json:"total_granted"`
		TotalUsed          float64        `json:"total_used"`
		UnlimitedQuota     bool           `json:"unlimited_quota"`
	} `json:"data"`
	// Error 是认证层失败体（HTTP 401 + {"error":{...,"type":"new_api_error"}}）。
	Error *struct {
		Code      any    `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Type      string `json:"type"`
	} `json:"error"`
}

// Fetch 查询 New API 的令牌额度。
//
// **成败判据是 body 的 code 字段**（true = 成功）。同时要容忍认证层失败体
// （HTTP 401 + error 对象）——被拒的 key 会在接口自己应答之前就被网关拦下。
func (newapiAdapter) Fetch(ctx context.Context, p ProviderInfo) ([]Dimension, error) {
	endpoint := p.Endpoint
	if endpoint == "" {
		var ok bool
		endpoint, ok = newapiAdapter{}.Endpoint(p.BaseURL)
		if !ok {
			return nil, fmt.Errorf("无法从 base_url %q 推导 New API 额度端点", p.BaseURL)
		}
	}

	body, status, err := fetchJSON(ctx, newHTTPClient(p.Env), endpoint, p.APIKey)
	if err != nil {
		return nil, err
	}

	var resp newapiResp
	if err := json.Unmarshal(body, &resp); err != nil {
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("New API 额度接口返回 HTTP %d: %s", status, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("New API 额度响应解析失败: %w", err)
	}

	// 认证层失败：HTTP 401 + error 对象。
	if resp.Error != nil && !resp.Code {
		msg := resp.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		return nil, fmt.Errorf("New API 额度查询失败: %s", msg)
	}
	// API 层失败：code != true（实测形状为 {"code": false, "message": "..."}）。
	if !resp.Code {
		msg := resp.Message
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		return nil, fmt.Errorf("New API 额度查询失败: %s", msg)
	}

	perUnit := p.QuotaPerUnit
	if perUnit <= 0 {
		perUnit = newapiDefaultQuotaPerUnit
	}
	unit := p.DisplayUnit
	if unit == "" {
		unit = newapiDefaultDisplayUnit
	}

	d := Dimension{
		ID:   "quota",
		Kind: KindQuota,
		Unit: unit,
		Raw: map[string]any{
			"name":                 resp.Data.Name,
			"object":               resp.Data.Object,
			"model_limits":         resp.Data.ModelLimits,
			"model_limits_enabled": resp.Data.ModelLimitsEnabled,
		},
	}

	// **total_granted 是"建令牌时设的上限"**（天花板），total_available 是
	// "还剩多少"——方向相反，不能互换。
	d.Limit = f64(resp.Data.TotalGranted / perUnit)
	d.Used = f64(resp.Data.TotalUsed / perUnit)
	d.Remaining = f64(resp.Data.TotalAvailable / perUnit)
	d.Unlimited = resp.Data.UnlimitedQuota

	if resp.Data.ExpiresAt != 0 {
		exp := time.Unix(resp.Data.ExpiresAt, 0)
		d.ExpiresAt = &exp
	}

	d.Finalize()
	return []Dimension{d}, nil
}
