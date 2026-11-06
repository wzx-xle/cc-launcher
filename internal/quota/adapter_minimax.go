package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// minimaxAdapter 适配 MiniMax 的额度接口。
//
// 端点：https://www.<剥掉 api./www. 前缀的主机名>/v1/token_plan/remains
//
// **不许硬编码单一域名**：.cn 与 .io 的 key 不通用（实测 .cn 的 key 打 .io
// 返回 2049 invalid api key），端点必须跟着 base_url 的域名走。
type minimaxAdapter struct{}

func (minimaxAdapter) Name() string { return "minimax" }

// Match 识别 MiniMax：剥掉前导 api. / www. 之后主机名**恰好等于**
// minimax.cn 或 minimax.io。
//
// 为什么要求"恰好等于"：用"包含 minimax"会把 minimax-proxy.corp.example
// 这类自建域名误判成 MiniMax，推出 www.minimax-proxy.corp.example 这种垃圾
// 端点。宁可识别不出来，也不要识别错（识别错 = 拿着 API key 去打一个不存在
// 的地址）。
func (minimaxAdapter) Match(baseURL string) bool {
	_, ok := minimaxHost(baseURL)
	return ok
}

// Endpoint 推导 MiniMax 的额度端点（换子域 + 换路径）。
func (minimaxAdapter) Endpoint(baseURL string) (string, bool) {
	host, ok := minimaxHost(baseURL)
	if !ok {
		return "", false
	}
	return "https://www." + host + "/v1/token_plan/remains", true
}

// minimaxHost 剥掉 api. / www. 前缀并校验主机名；不匹配时返回 ("", false)。
func minimaxHost(baseURL string) (string, bool) {
	host := hostOf(baseURL)
	if host == "" {
		return "", false
	}
	h := strings.ToLower(host)
	h = strings.TrimPrefix(h, "api.")
	h = strings.TrimPrefix(h, "www.")
	if h != "minimax.cn" && h != "minimax.io" {
		return "", false
	}
	return h, true
}

// minimaxResp 是 MiniMax 额度接口的响应。
type minimaxResp struct {
	ModelRemains []minimaxRemain `json:"model_remains"`
	BaseResp     struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

// minimaxRemain 是 model_remains 数组里的一项。
type minimaxRemain struct {
	StartTime   int64 `json:"start_time"`
	EndTime     int64 `json:"end_time"`
	RemainsTime int64 `json:"remains_time"`
	ModelName   string `json:"model_name"`

	WeeklyStartTime   int64 `json:"weekly_start_time"`
	WeeklyEndTime     int64 `json:"weekly_end_time"`
	WeeklyRemainsTime int64 `json:"weekly_remains_time"`

	CurrentIntervalStatus int `json:"current_interval_status"`
	CurrentWeeklyStatus   int `json:"current_weekly_status"`

	CurrentIntervalRemainingPercent float64 `json:"current_interval_remaining_percent"`
	CurrentWeeklyRemainingPercent   float64 `json:"current_weekly_remaining_percent"`

	// 按次计数的 4 个字段：语义未确认（"按次计费的套餐"？），只透传进 Raw。
	CurrentIntervalTotalCount int64 `json:"current_interval_total_count"`
	CurrentIntervalUsageCount int64 `json:"current_interval_usage_count"`
	CurrentWeeklyTotalCount   int64 `json:"current_weekly_total_count"`
	CurrentWeeklyUsageCount   int64 `json:"current_weekly_usage_count"`
}

// Fetch 查询 MiniMax 额度。
//
// **成败判据是 body 的 base_resp.status_code，不是 HTTP 状态码**——实测错误
// 也返回 HTTP 200（status_code 1004 = "login fail…"、2049 = "invalid api key"）。
func (minimaxAdapter) Fetch(ctx context.Context, p ProviderInfo) ([]Dimension, error) {
	endpoint, ok := p.Endpoint, p.Endpoint != ""
	if !ok {
		endpoint, ok = minimaxAdapter{}.Endpoint(p.BaseURL)
		if !ok {
			return nil, fmt.Errorf("无法从 base_url %q 推导 MiniMax 额度端点", p.BaseURL)
		}
	}

	body, status, err := fetchJSON(ctx, newHTTPClient(p.Env), endpoint, p.APIKey)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("MiniMax 额度接口返回 HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}

	var resp minimaxResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("MiniMax 额度响应解析失败: %w", err)
	}
	if resp.BaseResp.StatusCode != 0 {
		msg := resp.BaseResp.StatusMsg
		if msg == "" {
			msg = fmt.Sprintf("status_code=%d", resp.BaseResp.StatusCode)
		}
		return nil, fmt.Errorf("MiniMax 额度查询失败: %s", msg)
	}

	// 取 model_name == "general" 的条目；没有则取第一项。
	entry, ok := pickGeneralRemain(resp.ModelRemains)
	if !ok {
		// 数组为空 → 对应维度缺失（**不编造 0**：没有 0% 这种额度）。
		return nil, nil
	}

	now := time.Now()
	var dims []Dimension

	// 5 小时窗口（实测恒为 18000s，但**从响应算**——不许写死）。
	if d, ok := minimaxWindow("interval", entry.StartTime, entry.EndTime,
		entry.CurrentIntervalRemainingPercent, entry.RemainsTime, now); ok {
		d.Raw = map[string]any{
			"current_interval_status":       entry.CurrentIntervalStatus,
			"current_interval_total_count":  entry.CurrentIntervalTotalCount,
			"current_interval_usage_count":  entry.CurrentIntervalUsageCount,
			"current_weekly_status":         entry.CurrentWeeklyStatus,
			"current_weekly_total_count":    entry.CurrentWeeklyTotalCount,
			"current_weekly_usage_count":    entry.CurrentWeeklyUsageCount,
			"model_name":                    entry.ModelName,
		}
		d.Finalize()
		dims = append(dims, d)
	}

	// 周窗口。
	if d, ok := minimaxWindow("weekly", entry.WeeklyStartTime, entry.WeeklyEndTime,
		entry.CurrentWeeklyRemainingPercent, entry.WeeklyRemainsTime, now); ok {
		d.Raw = map[string]any{
			"current_interval_status":      entry.CurrentIntervalStatus,
			"current_weekly_status":        entry.CurrentWeeklyStatus,
			"current_interval_total_count": entry.CurrentIntervalTotalCount,
			"current_interval_usage_count": entry.CurrentIntervalUsageCount,
			"current_weekly_total_count":   entry.CurrentWeeklyTotalCount,
			"current_weekly_usage_count":   entry.CurrentWeeklyUsageCount,
			"model_name":                   entry.ModelName,
		}
		d.Finalize()
		dims = append(dims, d)
	}

	return dims, nil
}

// pickGeneralRemain 选 model_remains 里的一条：优先 model_name == "general"。
func pickGeneralRemain(list []minimaxRemain) (minimaxRemain, bool) {
	for _, r := range list {
		if r.ModelName == "general" {
			return r, true
		}
	}
	if len(list) > 0 {
		return list[0], true
	}
	return minimaxRemain{}, false
}

// minimaxWindow 构造一个窗口维度。
//
// 时长从 start/end **算**（end - start，毫秒）；重置时间从 remains_time
// （距重置的毫秒数）+ 当前时间算。
func minimaxWindow(id string, startMS, endMS int64, remainingPct float64, remainsMS int64, now time.Time) (Dimension, bool) {
	d := Dimension{
		ID:    id,
		Kind:  KindWindow,
		Unit:  "%",
		Limit: f64(100),
	}
	if endMS > startMS {
		d.Duration = time.Duration(endMS-startMS) * time.Millisecond
	}
	// 剩余百分比 → 已用百分比（统一语义：同一行里不会一个朝左一个朝右）。
	used := clampPct(100 - remainingPct)
	d.Used = f64(used)
	if remainsMS > 0 {
		resets := now.Add(time.Duration(remainsMS) * time.Millisecond)
		d.ResetsAt = &resets
	}
	return d, true
}

// fetchJSON 发一个 GET 请求并读回响应体。
//
// 统一带 Authorization: Bearer <key>。
//
// **只负责"发出去 + 读回来"**：HTTP 状态码的成败判定归各 adapter（三家协议
// 不同——MiniMax 的错误也返回 200，判据在 body 里）。错误体可能是纯文本
// （DeepSeek 实测），所以这里不做任何 JSON 解析。
func fetchJSON(ctx context.Context, client *http.Client, endpoint, apiKey string) (body []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("构造额度请求失败: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("额度请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读额度响应失败: %w", err)
	}
	return raw, resp.StatusCode, nil
}
