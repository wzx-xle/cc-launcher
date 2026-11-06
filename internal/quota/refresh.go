package quota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrNoQuotaInterface 表示"这个 provider 没有额度接口"。
//
// **纯离线可判**（读 provider 配置就知道）：既没配 quota 块，base_url 又
// 识别不出任何内置 adapter。命令层据此返回**退出码 2**（正常状态，不是
// 错误）——消费方据此回落到 Claude Code 自己的 rate_limits。
var ErrNoQuotaInterface = errors.New("该 provider 没有额度接口")

// Config 是 provider 配置里的 `quota` 块。
//
// 它是 **ccl 自己的元数据键**（与 note / order 同等），**不透传**给 Claude
// Code——不加进 provider.ReservedKeys 就会被当成 settings 键原样交付。
type Config struct {
	// Source 选内置 adapter（"minimax" / "deepseek" / "newapi"）。
	Source string
	// Endpoint 覆盖自动推导的额度端点（私有网关、二开路径）。
	Endpoint string
	// QuotaPerUnit 是额度点换算比例；0 表示用 adapter 的默认值。
	QuotaPerUnit float64
	// DisplayUnit 是显示单位；空表示用 adapter 的默认值。
	DisplayUnit string
	// RefreshInterval 是刷新周期；0 表示用 DefaultRefreshInterval。
	RefreshInterval time.Duration
}

// Interval 返回实际生效的刷新周期。
func (c *Config) Interval() time.Duration {
	if c == nil || c.RefreshInterval <= 0 {
		return DefaultRefreshInterval
	}
	return c.RefreshInterval
}

// ProviderRef 是一个 provider 的额度查询所需的全部信息。
type ProviderRef struct {
	// Name 是 provider 名（配置文件名去掉 .json）。
	Name string
	// Config 是 provider 配的 quota 块；nil 表示没配。
	Config *Config
	// ConfigErr 是 quota 块的解析错误（配置写错时保留，供命令层给出明确报错，
	// 而不是误报"没有额度接口"）。
	ConfigErr error
	// BaseURL 是 provider 的 ANTHROPIC_BASE_URL。
	BaseURL string
	// APIKey 是 provider 的凭证。
	APIKey string
	// Model 是 provider 用的模型名。
	Model string
	// Env 是 provider 的 env 块（代理设置从这里取）。
	Env map[string]string
}

// ParseConfig 解析 provider 配置里的 `quota` 块。
//
// 返回 (nil, nil) 表示没这个键。类型不对 → error（配置错误要说清楚，
// 不能静默忽略）。
func ParseConfig(raw any) (*Config, error) {
	if raw == nil {
		return nil, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("quota 块必须是对象，实际是 %T", raw)
	}
	cfg := &Config{}
	if v, ok := obj["source"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("quota.source 必须是字符串")
		}
		cfg.Source = strings.TrimSpace(s)
	}
	if v, ok := obj["endpoint"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("quota.endpoint 必须是字符串")
		}
		cfg.Endpoint = strings.TrimSpace(s)
	}
	if v, ok := obj["quotaPerUnit"]; ok && v != nil {
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("quota.quotaPerUnit 必须是数字")
		}
		cfg.QuotaPerUnit = f
	}
	if v, ok := obj["displayUnit"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("quota.displayUnit 必须是字符串")
		}
		cfg.DisplayUnit = strings.TrimSpace(s)
	}
	if v, ok := obj["refreshInterval"]; ok && v != nil {
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("quota.refreshInterval 必须是数字（秒）")
		}
		if f > 0 {
			cfg.RefreshInterval = time.Duration(f) * time.Second
		}
	}
	return cfg, nil
}

// toFloat 把 JSON 里的数字转成 float64。
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// Enumerate 枚举 providers 目录里**有额度接口**的 provider。
//
// "有额度接口"= 配了 quota 块，或 base_url 能被某个内置 adapter 识别。
// 两种情况都返回——不配 quota 块时靠 host 识别是合法的兜底路径
// （MiniMax / DeepSeek 认得出来）。
func Enumerate(providersDir string) ([]ProviderRef, error) {
	entries, err := os.ReadDir(providersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var refs []ProviderRef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(providersDir, e.Name()))
		if err != nil {
			continue
		}
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		ref, ok := refFromConfig(name, cfg)
		if !ok {
			continue
		}
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs, nil
}

// LoadProviderRef 读**单个** provider 的额度引用。
//
// 返回的第二个值 false = 这个 provider 没有额度接口（既没配 quota 块，
// base_url 又识别不出来）——**纯离线可判**。
//
// provider 文件不存在时返回 os.ErrNotExist 包装的错误（调用方据此区分
// "provider 不存在"与"provider 没有额度接口"——两者退出码不同）。
func LoadProviderRef(providersDir, name string) (ProviderRef, bool, error) {
	data, err := os.ReadFile(filepath.Join(providersDir, name+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return ProviderRef{Name: name}, false, fmt.Errorf("提供商 %q 不存在: %w", name, os.ErrNotExist)
		}
		return ProviderRef{Name: name}, false, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProviderRef{Name: name}, false, fmt.Errorf("提供商 %q 配置解析失败: %w", name, err)
	}
	ref, ok := refFromConfig(name, cfg)
	return ref, ok, nil
}

// refFromConfig 从 provider 的原始配置构造 ProviderRef。
//
// 返回的第二个值 false = 这个 provider 没有额度接口（不参与刷新）。
func refFromConfig(name string, cfg map[string]any) (ProviderRef, bool) {
	ref := ProviderRef{Name: name}

	if envAny, ok := cfg["env"].(map[string]any); ok {
		ref.Env = make(map[string]string, len(envAny))
		for k, v := range envAny {
			ref.Env[k] = valueToString(v)
		}
	}
	ref.BaseURL = ref.Env["ANTHROPIC_BASE_URL"]
	ref.APIKey = ref.Env["ANTHROPIC_AUTH_TOKEN"]
	if ref.APIKey == "" {
		ref.APIKey = ref.Env["ANTHROPIC_API_KEY"]
	}
	ref.Model = ref.Env["ANTHROPIC_MODEL"]
	if ref.Model == "" {
		if m, ok := cfg["model"].(string); ok {
			ref.Model = m
		}
	}

	// quota 块（类型不对时保留错误，但仍认为"有额度接口"——命令层据此给出
	// 明确报错，而不是误报"没有额度接口"）。
	if raw, ok := cfg["quota"]; ok && raw != nil {
		qc, err := ParseConfig(raw)
		if err != nil {
			ref.Config = &Config{}
			ref.ConfigErr = err
			return ref, true
		}
		ref.Config = qc
		return ref, true
	}

	// 没配 quota 块 → 靠 base_url 识别（MiniMax / DeepSeek）。
	if ref.BaseURL != "" {
		if _, ok := DetectAdapter(ref.BaseURL); ok {
			return ref, true
		}
	}
	return ref, false
}

// valueToString 把 env 块里的值转成字符串（非字符串 → 尽力转）。
func valueToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

// Resolve 解析某个 provider 用哪个 adapter、查哪个端点。
//
// 返回 ErrNoQuotaInterface = 这个 provider 没有额度接口（纯离线可判）。
func (r ProviderRef) Resolve() (Adapter, string, error) {
	if r.ConfigErr != nil {
		return nil, "", r.ConfigErr
	}
	var (
		ad Adapter
		ok bool
	)
	if r.Config != nil && r.Config.Source != "" {
		ad, ok = AdapterByName(r.Config.Source)
		if !ok {
			return nil, "", fmt.Errorf("未知的 quota.source %q（内置 adapter: %s）",
				r.Config.Source, strings.Join(AdapterNames(), ", "))
		}
	} else {
		ad, ok = DetectAdapter(r.BaseURL)
		if !ok {
			return nil, "", ErrNoQuotaInterface
		}
	}

	// 显式配置的端点优先（**不许推导**）。
	if r.Config != nil && r.Config.Endpoint != "" {
		return ad, r.Config.Endpoint, nil
	}
	endpoint, ok := ad.Endpoint(r.BaseURL)
	if !ok {
		return nil, "", fmt.Errorf("%s adapter 无法从 base_url %q 推导额度端点", ad.Name(), r.BaseURL)
	}
	return ad, endpoint, nil
}

// providerInfo 把 ProviderRef 转成 adapter 需要的输入。
func (r ProviderRef) providerInfo(endpoint string) ProviderInfo {
	p := ProviderInfo{
		BaseURL:  r.BaseURL,
		APIKey:   r.APIKey,
		Model:    r.Model,
		Endpoint: endpoint,
		Env:      r.Env,
	}
	if r.Config != nil {
		p.QuotaPerUnit = r.Config.QuotaPerUnit
		p.DisplayUnit = r.Config.DisplayUnit
	}
	return p
}

// PoolID 算额度池标识：sha256(端点 + "\x00" + API key)。
//
// **含 API key**：额度是**按 key 分**的，不是按 provider 名分的——同 key 的
// 不同 provider 共用一个额度池（如 minimax-m3 与 minimax-m3.1-flash）。
// **只存哈希，不存明文**：数据文件里不该躺凭证。
func PoolID(endpoint, apiKey string) string {
	sum := sha256.Sum256([]byte(endpoint + "\x00" + apiKey))
	return hex.EncodeToString(sum[:])
}

// RefreshResult 是一次刷新的结果。
type RefreshResult struct {
	// Refreshed 是本次真正发了请求的 provider 数（按额度池计）。
	Refreshed int
	// Skipped 是因为没到刷新周期而跳过的 provider 数。
	Skipped int
	// Failed 是查询失败（**旧条目保留**）的 provider 数。
	Failed int
}

// RefreshAll 刷新所有 provider 的额度（serve 的周期任务用）。
//
// 流程：
//  1. 读数据文件拿到每个 provider 的 fetched_at（**从文件读，不是内存**——
//     这样 `ccl quota --refresh` 刚写过的 provider 会自动被跳过）
//  2. 没到刷新周期的 provider 跳过这一轮
//  3. 按 (端点, key) 去重：同一额度池只查一次网络
//  4. 查到结果 → 写给所有引用该额度池的 provider
//  5. 查失败 → **不写文件**（旧条目保留，fetched_at 越来越旧 → stale 自动生效）
//  6. 清理已不存在的 provider 的残留条目
//
// logf 用于记录失败原因（进 serve 的日志，不进数据文件）。
func RefreshAll(ctx context.Context, refs []ProviderRef, st *Store, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	file, err := st.Load()
	if err != nil {
		logf("读额度数据文件失败（按空处理）: %v", err)
		file = &File{Version: quotaFileVersion, Entries: map[string]Entry{}}
	}

	now := time.Now()
	var res RefreshResult

	// 待查的 provider（按额度池去重）。
	type job struct {
		adapter  Adapter
		endpoint string
		ref      ProviderRef
	}
	jobs := make(map[string]job)  // pool → job
	poolOf := map[string]string{} // provider name → pool
	live := map[string]bool{}

	for _, ref := range refs {
		live[ref.Name] = true
		ad, endpoint, err := ref.Resolve()
		if err != nil {
			if !errors.Is(err, ErrNoQuotaInterface) {
				logf("provider %s 跳过: %v", ref.Name, err)
			}
			continue
		}
		pool := PoolID(endpoint, ref.APIKey)
		poolOf[ref.Name] = pool

		entry, has := file.EntryFor(ref.Name)
		if has && entry.FetchedAt > 0 {
			age := now.Sub(time.Unix(entry.FetchedAt, 0))
			if age < ref.Config.Interval() {
				res.Skipped++
				continue
			}
		}
		if _, ok := jobs[pool]; !ok {
			jobs[pool] = job{adapter: ad, endpoint: endpoint, ref: ref}
		}
	}

	// 逐个额度池查询。
	for pool, j := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dims, err := j.adapter.Fetch(ctx, j.ref.providerInfo(j.endpoint))
		if err != nil {
			res.Failed++
			logf("provider %s 额度刷新失败（旧数据保留）: %v", j.ref.Name, err)
			continue
		}
		res.Refreshed++
		// 结果写给**所有**引用该额度池的 provider（数据同源）。
		for _, ref := range refs {
			if poolOf[ref.Name] != pool {
				continue
			}
			if err := st.Update(ref.Name, j.adapter.Name(), pool, dims); err != nil {
				logf("写 provider %s 的额度条目失败: %v", ref.Name, err)
			}
		}
	}

	// 清理已不存在的 provider 的残留条目。
	if err := st.Prune(live); err != nil {
		logf("清理残留额度条目失败: %v", err)
	}
	return nil
}

// RefreshOne 刷新单个 provider 的额度并写回数据文件（`ccl quota --refresh` 用）。
//
// **不需要 serve 在跑**——这是 serve 被 `ccl serve --stop` 关掉时的逃生舱。
//
// 查询失败 → **不写文件**（旧条目保留）+ 返回 error。
func RefreshOne(ctx context.Context, ref ProviderRef, st *Store) ([]Dimension, error) {
	ad, endpoint, err := ref.Resolve()
	if err != nil {
		return nil, err
	}
	dims, err := ad.Fetch(ctx, ref.providerInfo(endpoint))
	if err != nil {
		return nil, err
	}
	pool := PoolID(endpoint, ref.APIKey)
	if err := st.Update(ref.Name, ad.Name(), pool, dims); err != nil {
		return nil, err
	}
	return dims, nil
}

// MinRefreshInterval 返回一组 provider 里最小的刷新周期（serve 的 ticker 周期）。
//
// ticker 用最小值 → 每个 provider 到期时一定能被这一轮覆盖到；各 provider
// 自己的周期判定在轮内做（周期不同的 provider 互不干扰）。
func MinRefreshInterval(refs []ProviderRef) time.Duration {
	min := DefaultRefreshInterval
	for _, ref := range refs {
		if iv := ref.Config.Interval(); iv < min {
			min = iv
		}
	}
	if min <= 0 {
		return DefaultRefreshInterval
	}
	return min
}
