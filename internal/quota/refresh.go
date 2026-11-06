package quota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
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

// DefaultMinRequestGap 是同一轮内相邻两个请求之间的默认最小间隔。
//
// 500ms 的取舍：厂商侧限流通常是"每分钟 N 次"或"滑动窗口内 N 次"，把一轮里
// 的 N 个请求摊到几秒内能显著降低突发；再大就拖慢一轮（8 个 provider × 500ms
// = 4 秒）。
const DefaultMinRequestGap = 500 * time.Millisecond

const (
	// initialBackoff 是首次被限流后的退避时长。
	initialBackoff = time.Minute
	// maxBackoff 是退避时长的上限（连续被限流也不会无限等）。
	maxBackoff = 30 * time.Minute
)

// BackoffError 表示该 provider 正在退避中（被厂商限流后）。
//
// 命令层据此提示"还有 N 秒可以再试"，而不是干巴巴地失败。
type BackoffError struct {
	// RetryAfter 是可以再试的时刻（Unix 秒）。
	RetryAfter int64
}

// Error 实现 error。
func (e *BackoffError) Error() string {
	wait := e.RetryAfter - time.Now().Unix()
	if wait < 0 {
		wait = 0
	}
	return fmt.Sprintf("该 provider 正在退避中（厂商限流），%d 秒后可再试", wait)
}

// nextBackoff 算下一次被限流后的退避时长。
//
// 连续被限流 → 翻倍（1min → 2min → 4min …），封顶 30min。
// 厂商给了 Retry-After → 取它与退避时长的**较大者**（厂商说的更准）。
func nextBackoff(currentSeconds int64, retryAfter time.Duration) time.Duration {
	b := initialBackoff
	if currentSeconds > 0 {
		b = time.Duration(currentSeconds) * time.Second * 2
	}
	if b < initialBackoff {
		b = initialBackoff
	}
	if b > maxBackoff {
		b = maxBackoff
	}
	if retryAfter > b {
		b = retryAfter
	}
	return b
}

// assignPhases 给本轮待刷的额度池分配"刷新周期内的固定相位"。
//
// 相位的定义：池的刷新时刻落在 interval 的 epoch 网格上，偏移一个固定的
// phase 秒。于是同一周期的 n 个池**均匀铺开**在周期里，而不是挤在同一个 tick
// 上（厂商侧会限流——8 个 provider 都配 60s 时，每分钟必然有 8 个请求，
// 挤在一起打就会 429）。
//
// **周期长度不变**：刷新时刻 = 网格点 + 相位，相邻两次相差恰好一个 interval
// → 用户配 60 秒就是 60 秒（不是 1-2 倍）。
//
// 已分配过相位的池（oldNext != 0）**保持自己的相位**；新池从组内还没被占用的
// 相位里挑（从 round(k × interval / n) 起逐个找空闲的）。
//
// 相位**不重排**：provider 增减时老的不动（重排会让所有 provider 的刷新时刻
// 突变，用户会看到一批数据同时变旧）。
func assignPhases(jobs map[string]job) {
	groups := map[time.Duration][]string{}
	for pool, j := range jobs {
		groups[j.interval] = append(groups[j.interval], pool)
	}
	for interval, pools := range groups {
		sort.Strings(pools)
		step := int64(interval.Seconds())
		if step <= 0 {
			step = int64(DefaultRefreshInterval.Seconds())
		}
		// 已被占用的相位（已有相位的池）。
		used := map[int64]bool{}
		for _, pool := range pools {
			j := jobs[pool]
			if j.oldNext > 0 {
				used[phaseOf(j.oldNext, step)] = true
			}
		}
		n := len(pools)
		for k, pool := range pools {
			j := jobs[pool]
			if j.oldNext > 0 {
				j.phase = time.Duration(phaseOf(j.oldNext, step)) * time.Second
				jobs[pool] = j
				continue
			}
			// 新池：从 round(k × interval / n) 起找第一个空闲相位。
			offset := int64(math.Round(float64(k) * float64(step) / float64(n)))
			for i := int64(0); i < step; i++ {
				cand := (offset + i) % step
				if !used[cand] {
					used[cand] = true
					j.phase = time.Duration(cand) * time.Second
					break
				}
			}
			jobs[pool] = j
		}
	}
}

// phaseOf 取某个 next_at 在周期网格上的相位（秒）。
func phaseOf(nextAt, step int64) int64 {
	p := nextAt % step
	if p < 0 {
		p += step
	}
	return p
}

// nextRefreshAt 算该池下次该刷的时刻（Unix 秒）。
//
// **递推**（oldNext + interval）而不是"now + interval"重算——后者会把每次
// 刷新的耗时累积进周期（刷新慢 → 周期越拖越长），且相位会漂。
// 错过了（oldNext 已过期，比如 serve 停了很久）就反复 +interval 跳到未来，
// **相位仍然保持**。
//
// 新分配（oldNext == 0）：落在 interval 的 epoch 网格 + 相位上。
func nextRefreshAt(now time.Time, interval time.Duration, phase time.Duration, oldNext int64) int64 {
	step := int64(interval.Seconds())
	if step <= 0 {
		step = int64(DefaultRefreshInterval.Seconds())
	}
	if oldNext > 0 {
		next := oldNext + step
		for next <= now.Unix() {
			next += step
		}
		return next
	}
	// 新分配：now 之后的第一个网格点 + 相位。
	base := now.Unix()/step*step + step
	next := base + int64(phase.Seconds())
	for next <= now.Unix() {
		next += step
	}
	return next
}

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
	// MinRequestGap 是同一轮内相邻两个请求之间的最小间隔；0 表示用
	// DefaultMinRequestGap。
	//
	// **为什么要有**：厂商侧对"查询额度"这类接口常有速率限制，而"一轮里刷新
	// 多个 provider"天然是一串密集请求（相位错开只解决跨轮次，首轮仍密集）。
	MinRequestGap time.Duration
}

// Interval 返回实际生效的刷新周期。
func (c *Config) Interval() time.Duration {
	if c == nil || c.RefreshInterval <= 0 {
		return DefaultRefreshInterval
	}
	return c.RefreshInterval
}

// Gap 返回实际生效的轮内请求间隔。
func (c *Config) Gap() time.Duration {
	if c == nil || c.MinRequestGap <= 0 {
		return DefaultMinRequestGap
	}
	return c.MinRequestGap
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
	if v, ok := obj["minRequestGapMs"]; ok && v != nil {
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("quota.minRequestGapMs 必须是数字（毫秒）")
		}
		if f > 0 {
			cfg.MinRequestGap = time.Duration(f) * time.Millisecond
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
	// Skipped 是因为没到刷新周期或正在退避而跳过的 provider 数。
	Skipped int
	// Failed 是查询失败（**旧条目保留**）的 provider 数。
	Failed int
}

// job 是本轮待刷的一个额度池。
type job struct {
	adapter  Adapter
	endpoint string
	ref      ProviderRef
	interval time.Duration
	// oldNext 是该池已记录的"下次该刷的时刻"（0 = 还没分配过相位）。
	oldNext int64
	// oldEntry 是数据文件里的现有条目（写回时保留退避字段）。
	oldEntry Entry
	hasEntry bool
	// phase 是该池在刷新周期内的固定相位（秒）。
	phase time.Duration
}

// RefreshAll 刷新所有 provider 的额度（serve 的周期任务用）。
//
// 流程：
//  1. 读数据文件拿到每个 provider 的 fetched_at（**从文件读，不是内存**——
//     这样 `ccl quota --refresh` 刚写过的 provider 会自动被跳过）
//  2. 没到刷新周期、或正在退避的 provider 跳过这一轮
//  3. 按 (端点, key) 去重：同一额度池只查一次网络
//  4. **相位错开**：同一刷新周期的一批额度池均分周期内的相位，请求不挤在
//     一起（厂商侧会限流，见 quota-refresh-stagger change）
//  5. 查到结果 → 写给所有引用该额度池的 provider
//  6. 查失败 → **不写额度数据**（旧条目保留，fetched_at 越来越旧 → stale 自动
//     生效）；若是 429 → 写退避状态（持久化，跨进程跨重启生效）
//  7. 清理已不存在的 provider 的残留条目
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
		interval := ref.Config.Interval()

		// **退避中 → 跳过**（429 之后不要每轮硬打）。
		// 退避按**额度池**记录（限流是厂商针对那把 key 的，provider 换 key =
		// 换池 → 老退避不该拦住它）。
		if b := file.BackoffFor(pool); b.RetryAfter > now.Unix() {
			res.Skipped++
			continue
		}

		// 到期判定：优先 next_at（相位错开）；没有则退回 fetched_at + interval。
		if has && entry.FetchedAt > 0 {
			dueAt := entry.NextAt
			if dueAt == 0 {
				dueAt = entry.FetchedAt + int64(interval.Seconds())
			}
			if now.Unix() < dueAt {
				res.Skipped++
				continue
			}
		}
		if _, ok := jobs[pool]; !ok {
			j := job{adapter: ad, endpoint: endpoint, ref: ref, interval: interval}
			if has {
				j.oldNext = entry.NextAt
				j.oldEntry = entry
				j.hasEntry = true
			}
			jobs[pool] = j
		}
	}

	// **相位分配**：同一刷新周期的一批额度池均分周期内的相位。
	assignPhases(jobs)

	// 按 pool 排序遍历 → 请求顺序与相位顺序一致（先刷相位小的）。
	pools := make([]string, 0, len(jobs))
	for pool := range jobs {
		pools = append(pools, pool)
	}
	sort.Strings(pools)

	// 逐个额度池查询（串行 + 轮内最小间隔）。
	for i, pool := range pools {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		j := jobs[pool]

		// 轮内最小间隔：首轮（还没相位）时靠它兜底，避免请求挤在一起。
		if i > 0 {
			gap := j.ref.Config.Gap()
			if gap > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(gap):
				}
			}
		}

		dims, err := j.adapter.Fetch(ctx, j.ref.providerInfo(j.endpoint))
		if err != nil {
			res.Failed++
			// **429 → 退避**（持久化，按额度池）：不在这一轮硬打，也不让下一轮
			// 接着打。
			var rl *RateLimitError
			if errors.As(err, &rl) {
				backoff := nextBackoff(file.BackoffFor(pool).Seconds, rl.RetryAfter)
				retryAfter := now.Unix() + int64(backoff.Seconds())
				if err := st.SetBackoff(pool, retryAfter, int64(backoff.Seconds())); err != nil {
					logf("写额度池 %s 的退避状态失败: %v", poolLabel(pool), err)
				}
				logf("provider %s 被厂商限流（HTTP %d），退避 %s 后再试", j.ref.Name, rl.Status, backoff)
				continue
			}
			logf("provider %s 额度刷新失败（旧数据保留）: %v", j.ref.Name, err)
			continue
		}
		res.Refreshed++

		// 成功 → **退避状态复位**（退避住在 File.Backoffs 里，不随条目重建
		// 自动清零 → 必须显式清）。
		if err := st.ClearBackoff(pool); err != nil {
			logf("清除额度池 %s 的退避状态失败: %v", poolLabel(pool), err)
		}

		// 相位保持：next_at **递推**（oldNext + interval），不是 now + interval
		// ——后者会把刷新耗时累积进周期，且相位会漂。
		nextAt := nextRefreshAt(now, j.interval, j.phase, j.oldNext)

		// 结果写给**所有**引用该额度池的 provider（数据同源）。
		for _, ref := range refs {
			if poolOf[ref.Name] != pool {
				continue
			}
			e := Entry{
				Source:     j.adapter.Name(),
				Pool:       pool,
				Dimensions: dims,
				NextAt:     nextAt,
				// 成功 → 退避状态复位（RetryAfter / BackoffSeconds 留零值）。
			}
			if err := st.UpdateEntry(ref.Name, e); err != nil {
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
// 查询失败 → **不写额度数据**（旧条目保留）+ 返回 error。
// 命中 429 → 写退避状态（持久化），让 serve 下一轮也跳过。
func RefreshOne(ctx context.Context, ref ProviderRef, st *Store) ([]Dimension, error) {
	ad, endpoint, err := ref.Resolve()
	if err != nil {
		return nil, err
	}
	pool := PoolID(endpoint, ref.APIKey)

	// **退避中 → 不发请求**（手动刷不能冲掉退避：它打的是同一个接口）。
	// 退避按**额度池**记录 → 换 key（换池）后不受老退避影响。
	if f, err := st.Load(); err == nil {
		if b := f.BackoffFor(pool); b.RetryAfter > time.Now().Unix() {
			return nil, &BackoffError{RetryAfter: b.RetryAfter}
		}
	}

	dims, err := ad.Fetch(ctx, ref.providerInfo(endpoint))
	if err != nil {
		var rl *RateLimitError
		if errors.As(err, &rl) {
			backoff := nextBackoff(currentBackoff(st, pool), rl.RetryAfter)
			retryAfter := time.Now().Unix() + int64(backoff.Seconds())
			if werr := st.SetBackoff(pool, retryAfter, int64(backoff.Seconds())); werr != nil {
				log.Printf("quota: 写额度池 %s 的退避状态失败: %v", poolLabel(pool), werr)
			}
		}
		return nil, err
	}

	// 相位保持：已有 next_at 就递推（保持它在周期里的固定位置）。
	var oldNext int64
	if f, err := st.Load(); err == nil {
		if e, ok := f.EntryFor(ref.Name); ok {
			oldNext = e.NextAt
		}
	}
	interval := ref.Config.Interval()
	nextAt := nextRefreshAt(time.Now(), interval, 0, oldNext)

	e := Entry{
		Source:     ad.Name(),
		Pool:       pool,
		Dimensions: dims,
		NextAt:     nextAt,
	}
	if err := st.UpdateEntry(ref.Name, e); err != nil {
		return nil, err
	}
	// 成功 → 退避状态复位。
	if err := st.ClearBackoff(pool); err != nil {
		log.Printf("quota: 清除额度池 %s 的退避状态失败: %v", poolLabel(pool), err)
	}
	return dims, nil
}

// currentBackoff 读某个额度池当前的退避基数（秒；没有则 0）。
func currentBackoff(st *Store, pool string) int64 {
	f, err := st.Load()
	if err != nil {
		return 0
	}
	return f.BackoffFor(pool).Seconds
}

// poolLabel 给日志用的额度池短标识（截断到 8 位）。
func poolLabel(pool string) string {
	if len(pool) <= 8 {
		return pool
	}
	return pool[:8]
}

// MinRefreshInterval 返回一组 provider 里最小的刷新周期。
//
// 各 provider 自己的周期判定在轮内做（周期不同的 provider 互不干扰）。
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

// TickInterval 算 serve 的刷新 ticker 周期。
//
// **必须细于相位分辨率**，否则相位错开白做：相位以秒为单位分布在刷新周期
// 里（8 个 provider → 相位间隔 60/8 = 7.5 秒），ticker 若也是 60 秒，它会在
// 窗口边界**一次性把所有到期的都叫醒** → 请求又挤在一起（限流照旧）。
//
// 所以：周期 = 最小刷新周期 ÷ 该周期下的额度池数（下限 1 秒）。8 个 provider
// 配 60 秒 → ticker 每 7.5 秒醒一次 → 每轮大约只到期一个 → 请求真正散开。
//
// **整除向下取整**：相位是以秒为单位的整数，相邻相位的间隔会因取整而略小于
// 平均值（60/8 → 相位 0,8,15,23,30,38,45,53 → 最小间隔 7 秒）。ticker 若
// 取 7.5 秒，那对间隔 7 秒的两个相位会落进同一个 tick → 又挤在一起。向下取整
// 到 7 秒 → 每个 tick 恰好一个到期。
func TickInterval(refs []ProviderRef) time.Duration {
	// 按额度池去重（同池多 provider 只算一个池）。
	counts := map[time.Duration]int{}
	pools := map[string]bool{}
	for _, ref := range refs {
		_, endpoint, err := ref.Resolve()
		if err != nil {
			continue
		}
		pool := PoolID(endpoint, ref.APIKey)
		if pools[pool] {
			continue
		}
		pools[pool] = true
		counts[ref.Config.Interval()]++
	}
	if len(counts) == 0 {
		return DefaultRefreshInterval
	}
	min := DefaultRefreshInterval
	maxPools := 1
	for iv, c := range counts {
		if iv < min {
			min = iv
		}
		if c > maxPools {
			maxPools = c
		}
	}
	div := maxPools
	if div < 2 {
		div = 2
	}
	// 向下取整到整秒（相位是整数秒，ticker 不能比它更细——也不能比它更粗）。
	tickSec := int64(min/time.Second) / int64(div)
	if tickSec < 1 {
		tickSec = 1
	}
	return time.Duration(tickSec) * time.Second
}
