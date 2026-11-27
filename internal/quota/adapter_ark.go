package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// arkAdapter 适配火山引擎方舟 Agent Plan 个人版的套餐额度。
//
// 端点：<管控面>/?Action=GetAFPUsage&Version=2024-01-01
//
// **鉴权只有 Access Key 签名一种**（实测：拿推理用的 `ark-…` API Key 打过去
// 是 400 `InvalidAuthorization`，无鉴权是 400 `InvalidCredential`）——所以它
// 需要的是 AK/SK，不是 provider 的 ANTHROPIC_AUTH_TOKEN。签名见 arksign.go。
//
// **额度是账号级的，不是 API Key 级的**：接口的入参里没有 API key 的位置
// （签名只有 AK 代表"调用者是谁"，body 固定 `{}`），它返回的是调用者**账号**
// 的套餐额度。官方口径也一致："主账号下所有子账号共享该额度池"。
type arkAdapter struct{}

// Name 返回适配器名（provider 配置里的 quota.source）。
func (arkAdapter) Name() string { return "ark" }

// Match 判断 base URL 是否是方舟的 Agent Plan 部署。
//
// **host 与 path 两个条件缺一不可**：按量部署是**同一个 host**
// （`ark.cn-beijing.volces.com`）但路径是 `/api/v3`，它没有 Agent Plan 套餐——
// 只看 host 会把按量用户也识别成套餐用户，然后拿着不存在的套餐去查。
func (arkAdapter) Match(baseURL string) bool {
	_, ok := arkRegionOf(baseURL)
	return ok
}

// arkDataPlaneSuffix 是方舟数据面域名的后缀（`ark.<region>.volces.com`）。
const arkDataPlaneSuffix = ".volces.com"

// arkControlPlaneSuffix 是方舟管控面域名的后缀（`ark.<region>.volcengineapi.com`）。
//
// **管控面与数据面不是同一个 host** —— 不能拿 base URL 换后缀了事。
const arkControlPlaneSuffix = ".volcengineapi.com"

// arkPlanPathSegment 是 Agent Plan 在 base URL 里的路径特征。
//
// `/api/plan` 是方舟文档里 Agent Plan 的专用路径前缀，不是通用约定。
const arkPlanPathSegment = "/api/plan"

// arkRegionOf 从 base URL 解析方舟的 region。
//
// 返回 false 表示"这不是一个能识别的 Agent Plan 部署"。
func arkRegionOf(baseURL string) (string, bool) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", false
	}
	region, ok := arkRegionFromHost(u.Hostname(), arkDataPlaneSuffix)
	if !ok {
		return "", false
	}
	if !strings.Contains(u.Path, arkPlanPathSegment) {
		return "", false
	}
	return region, true
}

// arkRegionFromEndpoint 从管控面端点解析 region。
//
// **为什么需要它**：端点可以由用户显式配（`quota.endpoint`），这时 base URL
// 未必能解析出 region——但签名必须要 region（它在凭据作用域里）。
func arkRegionFromEndpoint(endpoint string) (string, bool) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", false
	}
	return arkRegionFromHost(u.Hostname(), arkControlPlaneSuffix)
}

// arkRegionFromHost 从 `ark.<region>.<suffix>` 形状的 host 里取 region。
func arkRegionFromHost(host, suffix string) (string, bool) {
	if !strings.HasPrefix(host, "ark.") || !strings.HasSuffix(host, suffix) {
		return "", false
	}
	region := strings.TrimSuffix(strings.TrimPrefix(host, "ark."), suffix)
	// region 里不该再有点（否则说明 host 形状不对，如 a.b.c.volces.com）
	if region == "" || strings.Contains(region, ".") {
		return "", false
	}
	return region, true
}

// arkUsageEndpoint 是查套餐额度的端点模板（region 由 base URL 推导）。
const arkUsageEndpoint = "https://ark.%s.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01"

// Endpoint 推导方舟的额度端点。
//
// **端点不从 base URL 拼**：管控面与数据面不是同一个 host，且端点带固定的
// query（Action / Version）——它是管控面接口的固定形状。
func (arkAdapter) Endpoint(baseURL string) (string, bool) {
	region, ok := arkRegionOf(baseURL)
	if !ok {
		return "", false
	}
	return fmt.Sprintf(arkUsageEndpoint, region), true
}

// PoolKey 返回方舟的额度池键：**Access Key**。
//
// **为什么不是 API key**：方舟的 AFP 额度是**账号套餐级**的——同一对 AK 下的
// 多个推理 API Key 共用同一个套餐配额。按 API key 分池会让同一份额度被查多次、
// 存多份、还多消耗管控面的配额。
//
// **实测印证**：子账号 AK 与主账号 AK 查到的额度完全一致（额度挂在账号上）。
//
// **没有 AK 时回退 API Key**：池键为空会把所有 provider 挤进同一个池。
func (arkAdapter) PoolKey(p ProviderInfo) string {
	if p.AccessKey != "" {
		return p.AccessKey
	}
	return p.APIKey
}

// arkUnit 是方舟额度的显示单位。
//
// **AFP = Agent Fuel Point**（厂商文档里写成 "Agent Frame Point"，是笔误）。
// 1 AFP 不是固定的 token 数——抵扣系数随模型变（deepseek-v4.1-flash 的系数
// 是 2.5，deepseek-v4-flash 是 0.5），所以**不换算成 token 显示**。
const arkUnit = "AFP"

// arkAmount 兼容厂商把额度写成 number 或 string 两种形态。
//
// **官方文档写的是 string（`Quota string`），实测返回的是 number**
// （`"Quota":10000`）——厂商改文档或改实现都是它的事，两种都要能解析。
//
// 字段缺失 / null / 空串 / 解析失败 → ok = false（**"不知道"**）。
//
// **为什么不能当成 0**：0 是"额度是 0 / 用量为 0"这个**结论**，解析失败是
// "不知道"。两者混同就是"额度查询坏了"被读成"额度没了"。
type arkAmount struct {
	value float64
	ok    bool
}

// UnmarshalJSON 实现 json.Unmarshaler。
//
// **解析失败不返回 error**：字段级非法只该丢**那一个维度**，不该让整个响应
// 解析失败——那会把"某个字段变了"放大成"额度全查不到"。
func (a *arkAmount) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		unquoted, err := strconv.Unquote(s)
		if err != nil {
			return nil
		}
		s = unquoted
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	a.value = f
	a.ok = true
	return nil
}

// arkWindow 是单个窗口的额度。
type arkWindow struct {
	// Quota 是该窗口的额度上限（AFP）。
	Quota arkAmount `json:"Quota"`
	// Used 是该窗口已用的量（AFP）。
	Used arkAmount `json:"Used"`
	// ResetTime 是下次重置时间（Unix 毫秒）。
	ResetTime int64 `json:"ResetTime"`
	// SubscribeTime 是当前计费窗口的起始时间（Unix 毫秒）。
	SubscribeTime int64 `json:"SubscribeTime"`
}

// arkUsageResp 是 GetAFPUsage 的响应（只取用得到的字段）。
//
// **Result 是指针**：响应里没有它时要能认出来（当成"额度是 0"就是把接口
// 形状变了悄悄吞掉——那是"额度是 0"与"读不到额度"的区别）。
type arkUsageResp struct {
	Result *struct {
		PlanType string `json:"PlanType"`
		// FiveHour 是近 5 小时窗口。
		FiveHour arkWindow `json:"AFPFiveHour"`
		// Weekly 是近一周窗口。
		Weekly arkWindow `json:"AFPWeekly"`
		// Monthly 是近一月窗口。
		Monthly arkWindow `json:"AFPMonthly"`
		// Daily 是近一天窗口——**不报**，见 Fetch 的注释。
		Daily arkWindow `json:"AFPDaily"`
	} `json:"Result"`
}

// arkFetchBody 是请求体。
//
// 接口无入参，但**必须有 body**——官方 cURL 示例是 `-d '{}'`，签名里的
// X-Content-Sha256 与 body 哈希都按这个 `{}` 算。
const arkFetchBody = "{}"

// Fetch 查询方舟的套餐额度。
//
// **报三个维度（5h / 周 / 月），不报日额度**：接口返回四个窗口，但
// `AFPDaily.Used` 在**文本模型推理场景恒为 0**——实测同一时刻 5h 窗口有
// 328.5542 AFP 消耗而 daily 是 0。方舟文档写明日限额"仅限视觉/语音模型、
// Harness"，即它计的是图片/语音模型与 Harness 能力调用。官方 CLI 的
// `arkcli usage plan` 同样只输出这三个窗口。
//
// **报的是账号额度，不是本 provider 的**：见 arkAdapter 的类型注释。
func (arkAdapter) Fetch(ctx context.Context, p ProviderInfo) ([]Dimension, error) {
	// 凭证检查在**这里**做，不在配置层：别的厂商的额度接口根本不用 AK/SK，
	// "要不要 AK/SK、配全了没有"只有 adapter 知道。
	switch {
	case p.AccessKey == "" && p.SecretKey == "":
		return nil, fmt.Errorf("方舟额度查询需要 Access Key：请在 quota 块里配 accessKey / secretKey，或设环境变量 %s / %s",
			arkAccessKeyEnv, arkSecretKeyEnv)
	case p.AccessKey == "" || p.SecretKey == "":
		return nil, fmt.Errorf("方舟额度查询的 Access Key 只配了一半（accessKey 与 secretKey 必须同时有）")
	}

	endpoint := p.Endpoint
	region, _ := arkRegionOf(p.BaseURL)
	if endpoint == "" {
		var ok bool
		endpoint, ok = arkAdapter{}.Endpoint(p.BaseURL)
		if !ok {
			return nil, fmt.Errorf("无法从 base_url %q 推导方舟额度端点", p.BaseURL)
		}
	}
	if region == "" {
		// 端点可能是显式配的 → 从端点 host 反推 region（签名需要它）。
		region, _ = arkRegionFromEndpoint(endpoint)
	}
	if region == "" {
		return nil, fmt.Errorf("无法确定方舟的 region（base_url %q 与端点 %q 都认不出来）", p.BaseURL, endpoint)
	}

	body := []byte(arkFetchBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造方舟额度请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	arkSign(req, body, arkSigner{
		accessKeyID:     p.AccessKey,
		secretAccessKey: p.SecretKey,
		region:          region,
	}, time.Now())

	// 走 provider 自己的网络路径（代理设置来自 provider 的 env 块）。
	resp, err := newHTTPClient(p.Env).Do(req)
	if err != nil {
		return nil, fmt.Errorf("方舟额度请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读方舟额度响应失败: %w", err)
	}

	// **429 单独识别**：它不是"接口挂了"，是"你查得太勤了" → 刷新层要退避，
	// 而不是每轮硬打（见 RateLimitError）。
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &RateLimitError{
			Status:     resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			Msg:        quotaBodySnippet(raw),
		}
	}

	var parsed arkUsageResp
	parseErr := json.Unmarshal(raw, &parsed)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// **带上厂商自己的错误码与说明**：签名被拒、凭证不对、权限不足是三回事，
		// 笼统报"HTTP 403"会让用户查错方向。签名还依赖本机时钟（±15 分钟），
		// 时间跳变也表现为鉴权失败。
		return nil, fmt.Errorf("方舟额度接口返回 HTTP %d: %s", resp.StatusCode, arkErrorInfo(raw))
	}
	if parseErr != nil {
		return nil, fmt.Errorf("方舟额度响应解析失败: %w", parseErr)
	}
	// 响应里没有 Result → 接口形状变了（不是"额度为 0"）。
	if parsed.Result == nil {
		return nil, fmt.Errorf("方舟额度响应里没有 Result 对象: %s", quotaBodySnippet(raw))
	}

	result := parsed.Result
	planType := result.PlanType

	dims := make([]Dimension, 0, 3)
	// 三个窗口：5h / 周 / 月。
	for _, w := range []struct {
		id       string
		duration time.Duration
		window   arkWindow
	}{
		{"five_hour", 5 * time.Hour, result.FiveHour},
		{"weekly", 7 * 24 * time.Hour, result.Weekly},
		{"monthly", 30 * 24 * time.Hour, result.Monthly},
	} {
		if d, ok := arkDimension(w.id, w.duration, w.window, planType); ok {
			dims = append(dims, d)
		}
	}
	return dims, nil
}

// arkDimension 把一个窗口转成额度维度。
//
// **窗口时长是近似的**：5 小时窗口实测锚在首次请求（正好 5 小时），周窗口是
// 自然周，月窗口是**订阅月**（订阅日 → 次月同日 23:59:59，不是固定 30 天）。
// 时长只用于**派生展示标签**（5h / 7d / 30d），准确的重置时刻由接口给的
// ResetTime 承载——两者不冲突。
func arkDimension(id string, duration time.Duration, w arkWindow, planType string) (Dimension, bool) {
	if !w.Quota.ok {
		// 额度上限读不出来 → 没分母 → 报不了百分比，也不该报"0"。
		return Dimension{}, false
	}

	d := Dimension{
		ID:       id,
		Kind:     KindWindow,
		Duration: duration,
		Unit:     arkUnit,
		Raw:      map[string]any{"plan_type": planType},
	}
	if w.Quota.value == 0 {
		// **额度为 0 = 没有上限**：没有上限就没有分母 → 不派生百分比，
		// 也不报总额（报 0 会让人以为"上限是 0，用量必然超"）。
		//
		// 剩余量同样不报——无限额度下"剩余"是 ∞，没有有限值可报。
		d.Unlimited = true
	} else {
		d.Limit = f64(w.Quota.value)
	}
	if w.Used.ok {
		d.Used = f64(w.Used.value)
		if !d.Unlimited {
			// 剩余量自己算：接口不报它。夹到 0（用超了就是没剩，不是负数——
			// 负的剩余量会让消费方的条形图算出负百分比）。
			remaining := w.Quota.value - w.Used.value
			if remaining < 0 {
				remaining = 0
			}
			d.Remaining = f64(remaining)
		}
	}
	if w.ResetTime > 0 {
		resets := time.UnixMilli(w.ResetTime).UTC()
		d.ResetsAt = &resets
	}
	d.Finalize()
	return d, true
}

// arkErrorInfo 从错误响应里抽出厂商自己的错误码与说明。
//
// 方舟把错误放在 `ResponseMetadata.Error` 里（Code / Message / CodeN）。
// **带上它是有用的**：签名被拒（403）、凭证不对（401）、限流（429）是三回事，
// 笼统报状态码会让用户查错方向。
func arkErrorInfo(body []byte) string {
	var parsed struct {
		ResponseMetadata struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.ResponseMetadata.Error == nil {
		return quotaBodySnippet(body)
	}
	e := parsed.ResponseMetadata.Error
	if e.Code == "" && e.Message == "" {
		return quotaBodySnippet(body)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
